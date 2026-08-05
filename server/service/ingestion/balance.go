package ingestion

import (
	"context"
	"fmt"
	"sort"
	"strings"

	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	"github.com/leedenison/portfoliodb/server/db"
	"github.com/shopspring/decimal"
	"google.golang.org/protobuf/proto"
)

// Balancing a tx group.
//
// A group's postings are in different commodities, so a plain sum of quantity is
// meaningless: a buy is +10 AAPL and -1855 USD. Balance is checked on weight, as
// in beancount, whose get_weight is cost > price > units. A posting converts at
// its price when the units its counter-leg is expected in differ from its own;
// otherwise it weighs its own quantity in its own commodity. A price is per
// underlying unit, so converting also multiplies by the instrument's contract
// size -- 100 for an option, 1 for anything quoted in the units it trades in.
// Weights accumulate per commodity, and whatever is left over is routed to an
// explicit posting rather than rejected. See
// docs/adr/0024-group-balance-is-checked-on-weight.md.

// exchangeTypes are the tx types whose counter-leg is money rather than the
// commodity the posting is in: a security is exchanged for cash, so the security
// leg has to be converted at its price to reach the other side. Every other type
// moves a commodity without converting it -- a journal, a charge and a dividend
// all have their counter-leg in the units they are already in -- and converting
// one would move its residual into the wrong commodity, which for a JRNLSEC means
// losing the transferred shares.
var exchangeTypes = map[apiv1.TxType]bool{
	apiv1.TxType_BUYDEBT:    true,
	apiv1.TxType_BUYFUTURE:  true,
	apiv1.TxType_BUYMF:      true,
	apiv1.TxType_BUYOPT:     true,
	apiv1.TxType_BUYOTHER:   true,
	apiv1.TxType_BUYSTOCK:   true,
	apiv1.TxType_SELLDEBT:   true,
	apiv1.TxType_SELLFUTURE: true,
	apiv1.TxType_SELLMF:     true,
	apiv1.TxType_SELLOPT:    true,
	apiv1.TxType_SELLOTHER:  true,
	apiv1.TxType_SELLSTOCK:  true,
	apiv1.TxType_REINVEST:   true,
	apiv1.TxType_CLOSUREOPT: true,
}

// transferTypes are the journals whose other side is a different account. Their
// residual is an unmatched transfer rather than a data-quality problem, so it is
// routed to TRANSFER_CLEARING and holds the value in transit until the pair is
// matched.
var transferTypes = map[apiv1.TxType]bool{
	apiv1.TxType_TRANSFER: true,
	apiv1.TxType_JRNLFUND: true,
	apiv1.TxType_JRNLSEC:  true,
}

// Tolerances below which a residual is the source disagreeing with itself rather
// than a leg it left out. A trade of 37 shares at 12.3456 costs 456.7872 against a
// broker cash row of -456.79: the 0.0028 is an artefact of the source being
// written to 2dp, not a real discrepancy. Beancount infers half the last
// significant digit for exactly this case, and half a cent is what it would infer
// for 2dp money.
//
// Quantities are exact decimals now, so this is no longer absorbing arithmetic
// error -- it is the disagreement between two figures the source rounded
// differently, which exactness does not remove. Both beancount and ledger keep a
// tolerance for the same reason. Inferring it from the scale of the contributing
// amounts, rather than fixing it, is a change to how residuals get classified and
// is deliberately not made here.
//
// The tolerance decides the account type a residual is routed to, not whether it
// is routed at all. Suppressing the small ones would leave the group summing to a
// small non-zero value, which is exactly what the balance constraint rejects; and
// dropping them into IMBALANCE alongside genuinely missing legs would throw away
// the one thing already known about them. See
// docs/adr/0024-group-balance-is-checked-on-weight.md.
var (
	moneyTolerance     = decimal.RequireFromString("0.005")
	commodityTolerance = decimal.New(1, -6)
)

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
		return "cur:" + c.currency
	case c.instrumentID != "":
		return "inst:" + c.instrumentID
	default:
		return "desc:" + c.description
	}
}

// tolerance returns the residual below which a difference in this commodity reads
// as the source's own rounding rather than as a leg it omitted.
func (c commodity) tolerance() decimal.Decimal {
	if c.currency != "" {
		return moneyTolerance
	}
	return commodityTolerance
}

// routedPosting is a counterparty the server writes to make a group balance.
// currency is set when the commodity is money and its instrument still has to be
// looked up; instrumentID is set when it is carried over from the leg balanced.
type routedPosting struct {
	tx           *apiv1.Tx
	currency     string
	instrumentID string
}

// settleCurrency is the currency a converted weight is denominated in. Settlement
// is what the group is paid in; trading is the fallback for a source that reports
// only the instrument's own denomination.
func settleCurrency(tx *apiv1.Tx) string {
	if c := strings.ToUpper(strings.TrimSpace(tx.GetSettlementCurrency())); c != "" {
		return c
	}
	return strings.ToUpper(strings.TrimSpace(tx.GetTradingCurrency()))
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
func weightOf(tx *apiv1.Tx, qty decimal.Decimal, price *decimal.Decimal, instID string, inst balanceInstrument) (decimal.Decimal, commodity) {
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
	case exchangeTypes[tx.GetType()]:
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

// groupPostings returns the indices of each group's postings in input order,
// and the group refs in a stable order. Postings with no ref are each their own
// group, and are given a synthetic ref so a routed counterparty can join them.
// Refs are scoped to one upload and never stored, so synthesising them is local.
func groupPostings(txs []*apiv1.Tx) ([]string, map[string][]int) {
	prefix := "g"
	for _, t := range txs {
		for strings.HasPrefix(t.GetGroupRef(), prefix) {
			prefix += "_"
		}
	}
	var order []string
	byRef := map[string][]int{}
	for i, t := range txs {
		ref := t.GetGroupRef()
		if ref == "" {
			ref = fmt.Sprintf("%s%d", prefix, i)
			t.GroupRef = ref
		}
		if _, seen := byRef[ref]; !seen {
			order = append(order, ref)
		}
		byRef[ref] = append(byRef[ref], i)
	}
	return order, byRef
}

// routeResiduals returns the counterparty postings that make every group balance.
// Only a group that already sums to exactly zero produces none. A group can produce
// more than one when its residual spans commodities, as beancount's residual
// inventory and ledger's Imbalance:<CUR> both can. The account type says which kind
// of residual it is: IMBALANCE for a leg the source omitted, TRANSFER_CLEARING for
// the unmatched side of a journal, and SOURCE_ROUNDING for a difference small
// enough to be the source's own rounding.
//
// It assigns a synthetic group_ref to any posting that has none, so that a routed
// counterparty is stored in the same group as the posting it balances.
//
// A posting whose quantity or price is not a decimal is left out of its group's
// sums rather than failing the batch: routing exists so imperfect source data
// still lands, and the group is then left unbalanced -- the same state it was in
// before routing existed. The protovalidate patterns reject a malformed value at
// the interceptor for every unary RPC, so this is reachable only from an internal
// caller.
func routeResiduals(txs []*apiv1.Tx, instrumentIDs []string, instruments map[string]balanceInstrument) []routedPosting {
	order, byRef := groupPostings(txs)
	var out []routedPosting
	for _, ref := range order {
		idxs := byRef[ref]
		sums := map[string]decimal.Decimal{}
		commodities := map[string]commodity{}
		// The description to give a residual in a security commodity, taken from
		// the leg it balances rather than invented.
		descs := map[string]string{}
		var keys []string
		transfer := false
		for _, i := range idxs {
			t := txs[i]
			if transferTypes[t.GetType()] {
				transfer = true
			}
			qty, err := decimal.NewFromString(t.GetQuantity())
			if err != nil {
				continue
			}
			price, err := parseOptDec(t.UnitPrice)
			if err != nil {
				continue
			}
			amount, c := weightOf(t, qty, price, instrumentIDs[i], instruments[instrumentIDs[i]])
			k := c.key()
			if _, seen := sums[k]; !seen {
				keys = append(keys, k)
				commodities[k] = c
				descs[k] = t.GetInstrumentDescription()
			}
			// The zero value is 0, so a first contribution needs no init.
			sums[k] = sums[k].Add(amount)
		}
		// Sorted so a group's routed postings come out in a fixed order whatever
		// the map iteration gave.
		sort.Strings(keys)
		first := txs[idxs[0]]
		residual := apiv1.AccountType_ACCOUNT_TYPE_IMBALANCE
		if transfer {
			residual = apiv1.AccountType_ACCOUNT_TYPE_TRANSFER_CLEARING
		}
		for _, k := range keys {
			c := commodities[k]
			if sums[k].IsZero() {
				continue
			}
			// Below tolerance the residual is the source disagreeing with itself
			// rather than a leg the source left out, and saying so is worth more
			// than leaving it unposted: it keeps the group summing to exactly zero
			// while classifying the difference as what it is. A sub-tolerance
			// residual on a journal is rounding too, so this beats the transfer
			// case rather than the other way round.
			accountType := residual
			if sums[k].Abs().LessThan(c.tolerance()) {
				accountType = apiv1.AccountType_ACCOUNT_TYPE_SOURCE_ROUNDING
			}
			out = append(out, routedFor(first, ref, c, descs[k], sums[k].Neg(), accountType))
		}
	}
	return out
}

// routedFor builds the counterparty posting for one commodity's residual. It keeps
// the broker account, date and tx type of the group it balances, so the residual
// stays attributable to the account that produced it and to the kind of event that
// left it -- which is what the imbalance report reads.
func routedFor(first *apiv1.Tx, ref string, c commodity, desc string, amount decimal.Decimal, accountType apiv1.AccountType) routedPosting {
	tx := &apiv1.Tx{
		Timestamp:   proto.CloneOf(first.GetTimestamp()),
		Type:        first.GetType(),
		Quantity:    amount.String(),
		Account:     first.GetAccount(),
		GroupRef:    ref,
		AccountType: accountType,
	}
	if c.currency != "" {
		// A currency posting's description is the code, matching how an ordinary
		// cash row arrives, so nothing downstream has to treat it specially.
		tx.InstrumentDescription = c.currency
		tx.TradingCurrency = c.currency
		tx.SettlementCurrency = c.currency
		return routedPosting{tx: tx, currency: c.currency}
	}
	tx.InstrumentDescription = desc
	tx.TradingCurrency = first.GetTradingCurrency()
	tx.SettlementCurrency = first.GetSettlementCurrency()
	return routedPosting{tx: tx, instrumentID: c.instrumentID}
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
			if r.Currency != nil {
				inst.currency = strings.ToUpper(*r.Currency)
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

// resolveRouted turns the routed postings into txs with a resolved instrument,
// ready to store alongside the postings they balance. A residual in a currency
// resolves through the seeded currency instruments; one in a security carries the
// instrument of the leg it balances. The third return names the commodities whose
// residual could not be given an instrument.
//
// A residual with nowhere to go is dropped rather than failed: routing exists so
// that imperfect source data still lands, and refusing an import because its
// residual has no instrument would defeat that. The group is then left unbalanced,
// which is the same state it was in before routing existed.
func resolveRouted(ctx context.Context, database db.InstrumentDB, routed []routedPosting) ([]*apiv1.Tx, []string, []string) {
	var txs []*apiv1.Tx
	var ids []string
	var unresolved []string
	byCurrency := map[string]string{}
	for _, r := range routed {
		id := r.instrumentID
		if r.currency != "" {
			cached, ok := byCurrency[r.currency]
			if !ok {
				var err error
				cached, err = database.FindInstrumentByIdentifier(ctx, "CURRENCY", "", r.currency)
				if err != nil {
					cached = ""
				}
				byCurrency[r.currency] = cached
			}
			id = cached
		}
		if id == "" {
			// The commodity as best we can name it: the currency code when the
			// residual is money, otherwise the description of the leg it balances,
			// whose own instrument never resolved either.
			name := r.currency
			if name == "" {
				name = r.tx.GetInstrumentDescription()
			}
			unresolved = append(unresolved, name)
			continue
		}
		txs = append(txs, r.tx)
		ids = append(ids, id)
	}
	return txs, ids, unresolved
}
