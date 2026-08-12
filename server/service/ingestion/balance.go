package ingestion

import (
	"context"
	"fmt"
	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	typev1 "github.com/leedenison/portfoliodb/proto/type/v1"
	"github.com/leedenison/portfoliodb/server/db"
	"github.com/leedenison/portfoliodb/server/residual"
	"github.com/leedenison/portfoliodb/server/txtype"
	"github.com/shopspring/decimal"
	"google.golang.org/protobuf/proto"
	"sort"
	"strings"
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

// routedPosting is a counterparty the server writes to make a group balance.
// currency is set when the commodity is money and its instrument still has to be
// looked up; instrumentID is set when it is carried over from the leg balanced.
//
// weight is carried rather than recomputed from the finished tx. Weighing it again
// would give the same answer -- a routed posting has no price, so it weighs its own
// quantity in its own commodity -- but the residual it negates is the value the group
// has to be balanced against, and carrying it means the two cannot drift.
type routedPosting struct {
	tx           *apiv1.Tx
	currency     string
	instrumentID string
	weight       db.Weight
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
// inventory and ledger's Imbalance:<CUR> both can. Which account type each takes is
// residual.Type's. Replace-by-period shares the family half of that rule, so whether a
// residual reads as a transfer or as a missing leg does not depend on which path
// produced it; only this one applies the tolerance. See residual.SplitType.
//
// Boundary legs are posted first and then weighed with the rest, so what is left to
// call a residual is what remains after every side the data names. A dividend's
// income and a charge's expense are named by the posting's own type; only what no
// type accounts for reaches residual.Type. The two must not be netted: a dividend
// beside a charge in one group would otherwise produce a single leg for the
// difference, and the account it landed in would be a coin toss.
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
		var resolved []typev1.TxType
		add := func(t *apiv1.Tx, amount decimal.Decimal, c commodity) {
			k := c.key()
			if _, seen := sums[k]; !seen {
				keys = append(keys, k)
				commodities[k] = c
				descs[k] = t.GetInstrumentDescription()
			}
			// The zero value is 0, so a first contribution needs no init.
			sums[k] = sums[k].Add(amount)
		}
		for _, i := range idxs {
			t := txs[i]
			resolved = append(resolved, t.GetResolvedTxType())
			amount, c, ok := weighPosting(t, instrumentIDs[i], instruments[instrumentIDs[i]])
			if !ok {
				continue
			}
			add(t, amount, c)
			acct, named := boundaryFor(t)
			if !named {
				continue
			}
			// The mirror of the posting's weight, not of its quantity: a priced
			// leg weighs at its consideration, and mirroring the weight is what
			// makes the group sum to zero whichever it is.
			out = append(out, routedFor(t, ref, c, t.GetInstrumentDescription(),
				amount.Neg(), acct, db.BoundaryPurpose))
			add(t, amount.Neg(), c)
		}
		// Sorted so a group's routed postings come out in a fixed order whatever
		// the map iteration gave.
		sort.Strings(keys)
		first := txs[idxs[0]]
		for _, k := range keys {
			c := commodities[k]
			if sums[k].IsZero() {
				continue
			}
			amount := sums[k].Neg()
			out = append(out, routedFor(first, ref, c, descs[k], amount,
				residual.Type(k, amount, resolved), db.RoutedPurpose))
		}
	}
	return out
}

// boundaryFor returns the account a posting's other side sits in, for a posting the
// server is entitled to name one for.
//
// Only a stated posting in the user's own account gets one. A leg the server routed
// has no other side of its own -- it is already somebody's -- and a leg already in a
// boundary account is the other side.
func boundaryFor(t *apiv1.Tx) (typev1.AccountType, bool) {
	if t.GetSyntheticPurpose() != "" {
		return typev1.AccountType_ACCOUNT_TYPE_UNSPECIFIED, false
	}
	switch t.GetAccountType() {
	case typev1.AccountType_ACCOUNT_TYPE_USER, typev1.AccountType_ACCOUNT_TYPE_UNSPECIFIED:
	default:
		return typev1.AccountType_ACCOUNT_TYPE_UNSPECIFIED, false
	}
	return residual.Boundary(t.GetResolvedTxType())
}

// routedFor builds the counterparty posting for one commodity's residual. It keeps
// the broker account, date and tx type of the group it balances, so the residual
// stays attributable to the account that produced it and to the kind of event that
// left it -- which is what the imbalance report reads.
//
// It says in synthetic_purpose that the server made it. That is what a later replace
// or regroup finds it by, so that it is thrown away and derived again against the
// legs the group ends with rather than being preserved as though a source had stated
// it.
func routedFor(first *apiv1.Tx, ref string, c commodity, desc string, amount decimal.Decimal, accountType typev1.AccountType, purpose string) routedPosting {
	tx := &apiv1.Tx{
		Timestamp:        proto.CloneOf(first.GetTimestamp()),
		BrokerTxType:     append([]typev1.TxType(nil), first.GetBrokerTxType()...),
		ResolvedTxType:   first.GetResolvedTxType(),
		Quantity:         amount.String(),
		Account:          first.GetAccount(),
		GroupRef:         ref,
		AccountType:      accountType,
		SyntheticPurpose: purpose,
	}
	// The routed posting weighs the residual it negates, in the commodity that
	// residual accumulated in. That is what makes the group sum to zero.
	weight := db.Weight{Amount: amount, Commodity: c.key()}
	if c.currency != "" {
		// A currency posting's description is the code, matching how an ordinary
		// cash row arrives, so nothing downstream has to treat it specially.
		tx.InstrumentDescription = c.currency
		tx.TradingCurrency = c.currency
		tx.SettlementCurrency = c.currency
		return routedPosting{tx: tx, currency: c.currency, weight: weight}
	}
	tx.InstrumentDescription = desc
	tx.TradingCurrency = first.GetTradingCurrency()
	tx.SettlementCurrency = first.GetSettlementCurrency()
	return routedPosting{tx: tx, instrumentID: c.instrumentID, weight: weight}
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
// instrument of the leg it balances. It returns the txs, their instruments and their
// weights in step, and last the commodities whose residual could not be given an
// instrument.
//
// A residual with nowhere to go is left out and named in the last return. The
// caller fails the job on it: the group would otherwise be stored unbalanced, and
// the balance constraint rejects that at COMMIT, taking the whole upload with it.
// Naming the commodity is the only way the failure says anything useful. Currencies
// are seeded, so this is a safety net rather than a live path.
func resolveRouted(ctx context.Context, database db.InstrumentDB, routed []routedPosting) ([]*apiv1.Tx, []string, []db.Weight, []string) {
	var txs []*apiv1.Tx
	var ids []string
	var ws []db.Weight
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
		ws = append(ws, r.weight)
	}
	return txs, ids, ws, unresolved
}
