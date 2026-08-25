package ingestion

import (
	"fmt"
	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	typev1 "github.com/leedenison/portfoliodb/proto/type/v1"
	"github.com/leedenison/portfoliodb/server/db"
	"github.com/leedenison/portfoliodb/server/residual"
	"github.com/leedenison/portfoliodb/server/txtype"
	"github.com/shopspring/decimal"
	"strings"
)

// Weighing a posting.
//
// A group's postings are in different commodities, so a plain sum of quantity is
// meaningless: a buy is +10 AAPL and -1855 USD. Balance is checked on weight, as
// in beancount, whose get_weight is cost > price > units. A posting converts at
// its price when the units its counter-leg is expected in differ from its own;
// otherwise it weighs its own quantity in its own commodity. A price is per
// underlying unit, so converting also multiplies by the instrument's contract
// size -- 100 for an option, 1 for anything quoted in the units it trades in.
// See docs/adr/0024-group-balance-is-checked-on-weight.md.
//
// What a group fails to balance to is not worked out here. Weights are stored
// beside the postings, and the store settles a group from them once its postings
// are in -- see settle in server/db/postgres/txs.go. That is the only place a
// counterparty is written, so an upload, a period replace and a regroup cannot
// disagree about what a group owes.

// balanceInstrument is what balancing needs to know about a posting's commodity.
// Currencies are instruments, so telling money from a security is a property of
// the resolved instrument rather than of the tx type -- which matters, because a
// broker can report a cash movement under a security tx type.
type balanceInstrument struct {
	isCurrency bool
	currency   string
	// Units of the underlying one unit of quantity delivers, which is what a
	// price has to be multiplied by to reach the consideration. 1 for anything
	// quoted in the units it trades in; 100 for a standard option contract.
	contractSize decimal.Decimal
}

// optionContractSize is the OCC standard deliverable. It belongs to the asset
// class rather than to the instrument: an OCC symbol exists only for a
// standardised contract, and contract_multiplier records the deviation from this
// that a corporate action can leave behind, not the size itself. See the column
// comment in server/migrations/001_initial.sql.
var optionContractSize = decimal.NewFromInt(100)

// parseOptDec parses an optional decimal wire field, preserving absence. An
// unset field is nil; so is an empty string, since proto3 implicit presence
// cannot distinguish the two on a field of this shape.
func parseOptDec(s *string) (*decimal.Decimal, error) {
	if s == nil || *s == "" {
		return nil, nil
	}
	d, err := decimal.NewFromString(*s)
	if err != nil {
		return nil, err
	}
	return &d, nil
}

// commodity names what a weight is denominated in. A converted weight is named by
// its currency and an unconverted one by its instrument, so both have to reduce to
// the same name or a trade's two legs would never cancel. An unresolved posting
// falls back to its description, which still balances it against itself.
type commodity struct {
	currency     string
	instrumentID string
	description  string
}

func (c commodity) key() string {
	switch {
	case c.currency != "":
		return residual.CurrencyPrefix + c.currency
	case c.instrumentID != "":
		return residual.InstrumentPrefix + c.instrumentID
	default:
		return residual.DescriptionPrefix + c.description
	}
}

// ownCommodity names the posting's own units.
func ownCommodity(tx *apiv1.Tx, instID string, inst balanceInstrument) commodity {
	if inst.isCurrency && inst.currency != "" {
		return commodity{currency: strings.ToUpper(inst.currency)}
	}
	if instID != "" {
		return commodity{instrumentID: instID}
	}
	return commodity{description: tx.GetInstrumentDescription()}
}

// weightOf returns the amount a posting contributes to the group's balance, and
// the commodity it contributes in.
//
// qty and price are the posting's parsed quantity and unit price. price is nil
// when the source supplied none, which is not the same as a price of zero -- the
// first case below turns on exactly that distinction.
//
// The declared type set is a parameter rather than read off the posting so that
// the weight-neutrality check can run the same rule once per candidate; every
// live weighing passes the posting's own set.
func weightOf(tx *apiv1.Tx, types []typev1.TxType, qty decimal.Decimal, price *decimal.Decimal, instID string, inst balanceInstrument) (decimal.Decimal, commodity) {
	own := ownCommodity(tx, instID, inst)
	settle := settleCurrency(tx)
	convert := func() (decimal.Decimal, commodity) {
		// contractSize is 1 for every instrument quoted in the units it trades
		// in, which includes every currency -- so this is a no-op on the FX case
		// below and applies only where a price is per underlying unit.
		size := inst.contractSize
		if !size.IsPositive() {
			size = decimal.NewFromInt(1)
		}
		// Quantity, price and contract size are all exact and this is closed
		// under multiplication, so the weight is exact too. That is what lets a
		// group's balance be a plain sum against zero.
		return qty.Mul(*price).Mul(size), commodity{currency: settle}
	}
	switch {
	// No price, so nothing to convert at. An exchange event with no price leaves a
	// residual in the security itself, which is the signal that the source omitted
	// a price -- nothing else produces one.
	case price == nil:
		return qty, own
	case settle == "":
		return qty, own
	// The money leg of an exchange event is already in the units the group
	// balances in. Beancount has the same guard implicitly: nobody annotates a
	// plain cash posting with a price.
	case own.currency != "" && own.currency == settle:
		return qty, own
	// The asset leg of a trade is the one type whose counter-leg is money
	// rather than the commodity the posting is in, so it converts at its price.
	// Every other type moves a commodity without converting it -- a transfer, a
	// charge and a dividend all have their counter-leg in the units they are
	// already in -- and under the every-candidate rule an ambiguous set does not
	// convert, which weight neutrality makes harmless: a priced set whose
	// members disagree is rejected at ingest.
	case txtype.MustBe(types, typev1.TxType_TRADE_ASSET):
		return convert()
	// A movement event across currencies -- a EUR dividend settling into a USD
	// account -- does have its counter-leg in different units after all, and the
	// price is the FX rate. This is beancount's `3877.41 EUR @ 1.35 USD`.
	case own.currency != "" && own.currency != settle:
		return convert()
	default:
		return qty, own
	}
}

// weighPosting parses a posting's decimals and applies the weight rule. ok is false
// when either is malformed, which leaves the posting out of its group's sums rather
// than failing the batch -- routing exists so imperfect source data still lands, and
// the group is then left unbalanced, the same state it was in before routing existed.
// The protovalidate patterns reject a malformed value at the interceptor for every
// unary RPC, so this is reachable only from an internal caller.
func weighPosting(tx *apiv1.Tx, instID string, inst balanceInstrument) (decimal.Decimal, commodity, bool) {
	qty, err := decimal.NewFromString(tx.GetQuantity())
	if err != nil {
		return decimal.Decimal{}, ownCommodity(tx, instID, inst), false
	}
	price, err := parseOptDec(tx.UnitPrice)
	if err != nil {
		return decimal.Decimal{}, ownCommodity(tx, instID, inst), false
	}
	amount, c := weightOf(tx, tx.GetBrokerTxType(), qty, price, instID, inst)
	return amount, c, true
}

// validateWeightNeutrality rejects a posting whose declared type set names
// candidates that weigh it differently. Weight is stored against a deferred
// constraint that has no way to hold a maybe, so a set is admissible only if
// every member yields the same amount in the same commodity for the posting's
// own quantity, price and currencies -- checked by running the real weight rule
// once per candidate, never a summary of it. In practice only a priced security
// row can diverge, which is the case where the source has already answered the
// question: a price is what a trade has and a transfer does not.
// See docs/adr/0046-declared-ambiguity-is-bounded-by-weight-neutrality.md.
//
// rowIdx maps each tx back to its position in the uploaded batch, so an error
// names the row the user can see. A posting whose decimals do not parse is
// skipped here; the weight rule leaves it out of its group's sums.
func validateWeightNeutrality(txs []*apiv1.Tx, rowIdx []int, instrumentIDs []string, instruments map[string]balanceInstrument) []*apiv1.ValidationError {
	var errs []*apiv1.ValidationError
	for i, t := range txs {
		set := t.GetBrokerTxType()
		if len(set) < 2 {
			continue
		}
		qty, err := decimal.NewFromString(t.GetQuantity())
		if err != nil {
			continue
		}
		price, err := parseOptDec(t.UnitPrice)
		if err != nil {
			continue
		}
		inst := instruments[instrumentIDs[i]]
		firstAmount, firstC := weightOf(t, set[:1], qty, price, instrumentIDs[i], inst)
		for _, candidate := range set[1:] {
			amount, c := weightOf(t, []typev1.TxType{candidate}, qty, price, instrumentIDs[i], inst)
			if amount.Equal(firstAmount) && c.key() == firstC.key() {
				continue
			}
			errs = append(errs, &apiv1.ValidationError{
				RowIndex: int32(rowIdx[i]),
				Field:    "broker_tx_type",
				Message: fmt.Sprintf("candidates %s and %s weigh the posting differently; a priced row must declare which it is",
					set[0], candidate),
			})
			break
		}
	}
	return errs
}

// weights returns what each posting contributes to its group's balance, parallel to
// txs, for storing alongside them. It applies the same rule routeResiduals sums on,
// so a stored weight and the residual routed against it cannot disagree.
//
// A posting the rule cannot weigh contributes zero in its own commodity, which is the
// stored form of routeResiduals leaving it out of the sums.
func weights(txs []*apiv1.Tx, instrumentIDs []string, instruments map[string]balanceInstrument) []db.Weight {
	out := make([]db.Weight, len(txs))
	for i, t := range txs {
		amount, c, _ := weighPosting(t, instrumentIDs[i], instruments[instrumentIDs[i]])
		out[i] = db.Weight{Amount: amount, Commodity: c.key()}
	}
	return out
}

// balanceInstruments reduces the resolved instruments to what balancing needs:
// whether each is money, and if so which currency. Telling the two apart is a
// property of the instrument, not of the tx type -- which matters, because a
// broker can report a cash movement under a security tx type.
func balanceInstruments(byID map[string]*db.InstrumentRow) map[string]balanceInstrument {
	out := make(map[string]balanceInstrument, len(byID))
	for id, r := range byID {
		inst := balanceInstrument{contractSize: decimal.NewFromInt(1)}
		if r.AssetClass != nil && *r.AssetClass == db.AssetClassCash {
			inst.isCurrency = true
			// A cash instrument has a listing degenerately -- the currency it
			// holds is the currency it trades in -- so it has exactly one line
			// and that line's currency is the money this is. Read off the line
			// because that is where a currency lives; a security carries none.
			//
			// Anything else leaves the currency empty, which weighs the posting
			// in its own instrument rather than in money. A cash row with no line
			// is not one migration 002 can produce, and guessing between several
			// would weigh two currencies as one.
			if len(r.Listings) == 1 {
				inst.currency = strings.ToUpper(r.Listings[0].Currency)
			}
		}
		if r.AssetClass != nil && *r.AssetClass == db.AssetClassOption {
			// A multiplier of zero would weigh a whole trade to nothing. The
			// column is NOT NULL DEFAULT 1 so the database cannot supply one,
			// but silently voiding a leg is too quiet a failure to risk.
			multiplier := r.ContractMultiplier
			if !multiplier.IsPositive() {
				multiplier = decimal.NewFromInt(1)
			}
			inst.contractSize = optionContractSize.Mul(multiplier)
		}
		out[id] = inst
	}
	return out
}
