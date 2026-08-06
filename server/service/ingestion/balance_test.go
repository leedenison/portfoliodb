package ingestion

import (
	"testing"
	"time"

	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	"github.com/leedenison/portfoliodb/server/db"
	"github.com/shopspring/decimal"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const (
	aaplID = "inst-aapl"
	usdID  = "inst-usd"
	eurID  = "inst-eur"
	optID  = "inst-opt"
)

// balanceFixtures is the instrument map the routing tests resolve against: two
// currencies and two securities.
func balanceFixtures() map[string]balanceInstrument {
	return balanceInstruments(map[string]*db.InstrumentRow{
		aaplID: {ID: aaplID, AssetClass: strPtr(db.AssetClassStock)},
		optID:  {ID: optID, AssetClass: strPtr(db.AssetClassOption)},
		usdID:  {ID: usdID, AssetClass: strPtr(db.AssetClassCash), Currency: strPtr("USD")},
		eurID:  {ID: eurID, AssetClass: strPtr(db.AssetClassCash), Currency: strPtr("EUR")},
	})
}

// posting builds one leg. price is nil for a source that supplied none, which is
// not the same as a price of zero.
type posting struct {
	desc     string
	typ      apiv1.TxType
	qty      string
	price    *string
	settle   string
	trading  string
	instID   string
	groupRef string
}

func (p posting) tx(at time.Time) *apiv1.Tx {
	return &apiv1.Tx{
		Timestamp:             timestamppb.New(at),
		InstrumentDescription: p.desc,
		Type:                  p.typ,
		Quantity:              p.qty,
		UnitPrice:             p.price,
		SettlementCurrency:    p.settle,
		TradingCurrency:       p.trading,
		Account:               "ACC-1",
		GroupRef:              p.groupRef,
	}
}

// routed is a routed posting reduced to what the tests assert on.
type routed struct {
	commodity string // currency code, or the instrument id for a security
	quantity  string // decimal, as the wire carries it

	accountType apiv1.AccountType
}

func TestRouteResiduals(t *testing.T) {
	at := time.Date(2025, 3, 1, 0, 0, 0, 0, time.UTC)
	price := func(v string) *string { return &v }

	cases := []struct {
		name     string
		postings []posting
		want     []routed
	}{{
		// The case ADR 0021 was written about: Fidelity reports its own cash row,
		// and the converter groups the two. Nothing is left over, so nothing is
		// routed -- deriving a cash leg here would double count.
		name: "complete trade routes nothing",
		postings: []posting{
			{desc: "AAPL", typ: apiv1.TxType_BUYSTOCK, qty: "10", price: price("185.50"), settle: "USD", instID: aaplID, groupRef: "t1"},
			{desc: "USD", typ: apiv1.TxType_BUYSTOCK, qty: "-1855", settle: "USD", trading: "USD", instID: usdID, groupRef: "t1"},
		},
		want: nil,
	}, {
		// A broker that nets its commission into the cash total leaves exactly the
		// fee behind. This is what the imbalance report is for.
		name: "netted fee is left as the residual",
		postings: []posting{
			{desc: "AAPL", typ: apiv1.TxType_BUYSTOCK, qty: "10", price: price("185.50"), settle: "USD", instID: aaplID, groupRef: "t1"},
			{desc: "USD", typ: apiv1.TxType_BUYSTOCK, qty: "-1866.95", settle: "USD", trading: "USD", instID: usdID, groupRef: "t1"},
		},
		want: []routed{{commodity: "USD", quantity: "11.95", accountType: apiv1.AccountType_ACCOUNT_TYPE_IMBALANCE}},
	}, {
		// The motivating case: shares recorded, cash not. The missing cash becomes
		// visible instead of being silently absorbed.
		name: "priced buy with no cash row routes the missing cash",
		postings: []posting{
			{desc: "AAPL", typ: apiv1.TxType_BUYSTOCK, qty: "10", price: price("185.50"), settle: "USD", instID: aaplID},
		},
		want: []routed{{commodity: "USD", quantity: "-1855", accountType: apiv1.AccountType_ACCOUNT_TYPE_IMBALANCE}},
	}, {
		// No price to convert at, so the residual stays in the security. A
		// share-denominated imbalance is uniquely diagnostic: a missing fee or a
		// missing cash row can never produce one.
		name: "unpriced buy routes a share-denominated residual",
		postings: []posting{
			{desc: "AAPL", typ: apiv1.TxType_BUYSTOCK, qty: "10", settle: "USD", instID: aaplID},
		},
		want: []routed{{commodity: aaplID, quantity: "-10", accountType: apiv1.AccountType_ACCOUNT_TYPE_IMBALANCE}},
	}, {
		// Subtracting a zero-valued asset is a balanced transaction. It converts at
		// the declared price of zero, so nothing is left over -- which is why a
		// declared zero has to stay distinct from an absent price.
		name: "option expiring at a price of zero routes nothing",
		postings: []posting{
			{desc: "OPT", typ: apiv1.TxType_CLOSUREOPT, qty: "-1", price: price("0"), settle: "USD", instID: optID},
		},
		want: nil,
	}, {
		// An option is quoted per share and traded per contract, so its leg only
		// reaches the cash it was exchanged for once the contract size is applied.
		// Figures from local/masters/U7033034_20250107_20260107.qfx: 8 contracts at
		// 20.1105585 settles at -16088.4468 before commission.
		name: "option trade balances against the cash it settled for",
		postings: []posting{
			{desc: "OPT", typ: apiv1.TxType_BUYOPT, qty: "8", price: price("20.1105585"), settle: "USD", instID: optID, groupRef: "t1"},
			{desc: "USD", typ: apiv1.TxType_CASHFLOW, qty: "-16088.4468", price: price("1"), settle: "USD", trading: "USD", instID: usdID, groupRef: "t1"},
		},
		want: nil,
	}, {
		// The commission is what is left over, not the other 99% of the trade.
		name: "option trade leaves only its netted commission",
		postings: []posting{
			{desc: "OPT", typ: apiv1.TxType_BUYOPT, qty: "8", price: price("20.1105585"), settle: "USD", instID: optID, groupRef: "t1"},
			{desc: "USD", typ: apiv1.TxType_CASHFLOW, qty: "-16095.867048", price: price("1"), settle: "USD", trading: "USD", instID: usdID, groupRef: "t1"},
		},
		want: []routed{{commodity: "USD", quantity: "7.420248", accountType: apiv1.AccountType_ACCOUNT_TYPE_IMBALANCE}},
	}, {
		// A share is quoted in the units it trades in, so nothing is multiplied.
		name: "a share trade is unaffected by the contract size",
		postings: []posting{
			{desc: "AAPL", typ: apiv1.TxType_BUYSTOCK, qty: "8", price: price("20.1105585"), settle: "USD", instID: aaplID, groupRef: "t1"},
			{desc: "USD", typ: apiv1.TxType_CASHFLOW, qty: "-160.884468", price: price("1"), settle: "USD", trading: "USD", instID: usdID, groupRef: "t1"},
		},
		want: nil,
	}, {
		// 456.7872 against a cash row rounded to 456.79. The residual is real but is
		// an artefact of the source being written to 2dp, so it is routed as rounding
		// rather than as a leg the converter missed. It is still routed: leaving it
		// unposted is what would stop the group summing to exactly zero.
		name: "sub-cent difference routes as source rounding",
		postings: []posting{
			{desc: "AAPL", typ: apiv1.TxType_BUYSTOCK, qty: "37", price: price("12.3456"), settle: "USD", instID: aaplID, groupRef: "t1"},
			{desc: "USD", typ: apiv1.TxType_BUYSTOCK, qty: "-456.79", settle: "USD", trading: "USD", instID: usdID, groupRef: "t1"},
		},
		want: []routed{{commodity: "USD", quantity: "0.0028", accountType: apiv1.AccountType_ACCOUNT_TYPE_SOURCE_ROUNDING}},
	}, {
		// Half a cent is the boundary and the tolerance is exclusive, so this is a
		// missing leg rather than rounding. The pair of cases either side of it is
		// what pins the comparison down.
		name: "a residual at the tolerance is an imbalance, not rounding",
		postings: []posting{
			{desc: "AAPL", typ: apiv1.TxType_BUYSTOCK, qty: "1", price: price("100"), settle: "USD", instID: aaplID, groupRef: "t1"},
			{desc: "USD", typ: apiv1.TxType_BUYSTOCK, qty: "-99.995", settle: "USD", trading: "USD", instID: usdID, groupRef: "t1"},
		},
		want: []routed{{commodity: "USD", quantity: "-0.005", accountType: apiv1.AccountType_ACCOUNT_TYPE_IMBALANCE}},
	}, {
		// A journal whose two sides disagree by a fraction of a cent disagrees because
		// one statement rounded, not because a side is missing, so rounding beats the
		// transfer classification rather than the other way round.
		name: "sub-cent difference on a journal routes as rounding, not clearing",
		postings: []posting{
			{desc: "USD", typ: apiv1.TxType_JRNLFUND, qty: "1000.001", settle: "USD", trading: "USD", instID: usdID, groupRef: "j1"},
			{desc: "USD", typ: apiv1.TxType_JRNLFUND, qty: "-1000", settle: "USD", trading: "USD", instID: usdID, groupRef: "j1"},
		},
		want: []routed{{commodity: "USD", quantity: "-0.001", accountType: apiv1.AccountType_ACCOUNT_TYPE_SOURCE_ROUNDING}},
	}, {
		name: "dividend with its income leg routes nothing",
		postings: []posting{
			{desc: "USD", typ: apiv1.TxType_INCOME, qty: "23.40", settle: "USD", trading: "USD", instID: usdID, groupRef: "d1"},
			{desc: "USD", typ: apiv1.TxType_INCOME, qty: "-23.40", settle: "USD", trading: "USD", instID: usdID, groupRef: "d1"},
		},
		want: nil,
	}, {
		// Until converters emit the income leg, a bare dividend routes its whole
		// value. Early imbalance figures are dominated by this rather than by the
		// missing fees the mechanism is aimed at.
		name: "bare dividend routes its full value",
		postings: []posting{
			{desc: "USD", typ: apiv1.TxType_INCOME, qty: "23.40", settle: "USD", trading: "USD", instID: usdID},
		},
		// The routed quantity is canonical rather than a mirror of the input's
		// spelling: 23.40 in, -23.4 out. Scale carries no meaning here, and the
		// client compares these strings.
		want: []routed{{commodity: "USD", quantity: "-23.4", accountType: apiv1.AccountType_ACCOUNT_TYPE_IMBALANCE}},
	}, {
		// A journal moves a commodity without converting it, so the clearing leg
		// holds the shares in transit rather than a frozen cash value.
		name: "one-sided securities journal clears in the security",
		postings: []posting{
			{desc: "AAPL", typ: apiv1.TxType_JRNLSEC, qty: "-10", price: price("185.50"), settle: "USD", instID: aaplID},
		},
		want: []routed{{commodity: aaplID, quantity: "10", accountType: apiv1.AccountType_ACCOUNT_TYPE_TRANSFER_CLEARING}},
	}, {
		// Both sides in one group cancel in the security. Converting each at its own
		// statement's price would invent a residual.
		name: "both journal sides in one group route nothing",
		postings: []posting{
			{desc: "AAPL", typ: apiv1.TxType_JRNLSEC, qty: "-10", price: price("185.50"), settle: "USD", instID: aaplID, groupRef: "j1"},
			{desc: "AAPL", typ: apiv1.TxType_JRNLSEC, qty: "10", price: price("186.20"), settle: "USD", instID: aaplID, groupRef: "j1"},
		},
		want: nil,
	}, {
		name: "one-sided cash journal clears in the currency",
		postings: []posting{
			{desc: "USD", typ: apiv1.TxType_JRNLFUND, qty: "1000", settle: "USD", trading: "USD", instID: usdID},
		},
		want: []routed{{commodity: "USD", quantity: "-1000", accountType: apiv1.AccountType_ACCOUNT_TYPE_TRANSFER_CLEARING}},
	}, {
		// A cash row mistyped as a security sale still weighs its own quantity: the
		// leg is already in the units the group balances in. Without that guard a
		// -1855 USD row carrying a price of 185.50 would weigh -344,102 USD.
		name: "cash row mistyped as a sale weighs its own quantity",
		postings: []posting{
			{desc: "Cash", typ: apiv1.TxType_SELLSTOCK, qty: "-1855", price: price("185.50"), settle: "USD", trading: "USD", instID: usdID},
		},
		want: []routed{{commodity: "USD", quantity: "1855", accountType: apiv1.AccountType_ACCOUNT_TYPE_IMBALANCE}},
	}, {
		// A movement across currencies does have its counter-leg in different units,
		// and the price is the FX rate. Beancount's `3877.41 EUR @ 1.35 USD`.
		name: "cross-currency dividend converts at the fx rate",
		postings: []posting{
			{desc: "EUR", typ: apiv1.TxType_INCOME, qty: "100", price: price("1.10"), settle: "USD", trading: "EUR", instID: eurID},
		},
		want: []routed{{commodity: "USD", quantity: "-110", accountType: apiv1.AccountType_ACCOUNT_TYPE_IMBALANCE}},
	}, {
		// A residual can span commodities, as beancount's residual inventory and
		// ledger's Imbalance:<CUR> both can.
		name: "residual spanning two commodities routes one posting each",
		postings: []posting{
			{desc: "AAPL", typ: apiv1.TxType_BUYSTOCK, qty: "10", settle: "USD", instID: aaplID, groupRef: "m1"},
			{desc: "USD", typ: apiv1.TxType_INCOME, qty: "25", settle: "USD", trading: "USD", instID: usdID, groupRef: "m1"},
		},
		// Ordered by commodity so a group's routed postings do not depend on map
		// iteration: currencies first, then instruments.
		want: []routed{
			{commodity: "USD", quantity: "-25", accountType: apiv1.AccountType_ACCOUNT_TYPE_IMBALANCE},
			{commodity: aaplID, quantity: "-10", accountType: apiv1.AccountType_ACCOUNT_TYPE_IMBALANCE},
		},
	}, {
		// The shape a deposit into a Fidelity product account arrives in: three legs
		// in one account, only two of them journals, netting to the money that
		// landed. One transfer-typed leg is enough to classify the group, because
		// the residual is the other side of the movement and that is in another
		// account. Were it read leg by leg the deposit would route to imbalance.
		name: "one transfer leg routes a mixed group's residual to clearing",
		postings: []posting{
			{desc: "USD", typ: apiv1.TxType_JRNLFUND, qty: "20000", settle: "USD", trading: "USD", instID: usdID, groupRef: "d1"},
			{desc: "USD", typ: apiv1.TxType_CASHFLOW, qty: "-20000", settle: "USD", trading: "USD", instID: usdID, groupRef: "d1"},
			{desc: "USD", typ: apiv1.TxType_JRNLFUND, qty: "20000", settle: "USD", trading: "USD", instID: usdID, groupRef: "d1"},
		},
		want: []routed{{commodity: "USD", quantity: "-20000", accountType: apiv1.AccountType_ACCOUNT_TYPE_TRANSFER_CLEARING}},
	}}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			txs := make([]*apiv1.Tx, len(tc.postings))
			ids := make([]string, len(tc.postings))
			for i, p := range tc.postings {
				txs[i] = p.tx(at)
				ids[i] = p.instID
			}
			got := routeResiduals(txs, ids, balanceFixtures())
			if len(got) != len(tc.want) {
				t.Fatalf("routed %d postings, want %d: %s", len(got), len(tc.want), describeRouted(got))
			}
			for i, w := range tc.want {
				g := got[i]
				commodity := g.instrumentID
				if g.currency != "" {
					commodity = g.currency
				}
				if commodity != w.commodity {
					t.Errorf("routed[%d] commodity = %q, want %q", i, commodity, w.commodity)
				}
				// Exact: the weights the residual is summed from are decimals
				// now, so there is no arithmetic error to tolerate.
				if g.tx.GetQuantity() != w.quantity {
					t.Errorf("routed[%d] quantity = %v, want %v", i, g.tx.GetQuantity(), w.quantity)
				}
				if g.tx.GetAccountType() != w.accountType {
					t.Errorf("routed[%d] account type = %v, want %v", i, g.tx.GetAccountType(), w.accountType)
				}
			}
		})
	}
}

// TestRouteResiduals_KeepsAttribution verifies the routed posting stays
// attributable to the account and event that produced it, which is what the
// per-broker total in the imbalance report reads.
func TestRouteResiduals_KeepsAttribution(t *testing.T) {
	at := time.Date(2025, 3, 1, 12, 0, 0, 0, time.UTC)
	price := "185.50"
	txs := []*apiv1.Tx{{
		Timestamp:             timestamppb.New(at),
		InstrumentDescription: "AAPL",
		Type:                  apiv1.TxType_BUYSTOCK,
		Quantity:              "10",
		UnitPrice:             &price,
		SettlementCurrency:    "USD",
		Account:               "ACC-9",
		GroupRef:              "t1",
	}}
	got := routeResiduals(txs, []string{aaplID}, balanceFixtures())
	if len(got) != 1 {
		t.Fatalf("routed %d postings, want 1", len(got))
	}
	r := got[0].tx
	if r.GetAccount() != "ACC-9" {
		t.Errorf("account = %q, want ACC-9", r.GetAccount())
	}
	if r.GetType() != apiv1.TxType_BUYSTOCK {
		t.Errorf("tx type = %v, want the type of the event it balances", r.GetType())
	}
	if !r.GetTimestamp().AsTime().Equal(at) {
		t.Errorf("timestamp = %v, want %v", r.GetTimestamp().AsTime(), at)
	}
	if r.GetGroupRef() != "t1" {
		t.Errorf("group_ref = %q, want t1: the residual must land in the group it balances", r.GetGroupRef())
	}
	if r.GetInstrumentDescription() != "USD" {
		t.Errorf("description = %q, want the currency code", r.GetInstrumentDescription())
	}
}

// TestRouteResiduals_GroupsUngroupedPostings verifies a posting uploaded without
// a group_ref is given one, so its residual is stored in the same group rather
// than in a group of its own.
func TestRouteResiduals_GroupsUngroupedPostings(t *testing.T) {
	at := time.Date(2025, 3, 1, 0, 0, 0, 0, time.UTC)
	txs := []*apiv1.Tx{
		posting{desc: "AAPL", typ: apiv1.TxType_BUYSTOCK, qty: "10", instID: aaplID}.tx(at),
		posting{desc: "MSFT", typ: apiv1.TxType_BUYSTOCK, qty: "5", instID: aaplID}.tx(at),
	}
	got := routeResiduals(txs, []string{aaplID, aaplID}, balanceFixtures())
	if len(got) != 2 {
		t.Fatalf("routed %d postings, want 2", len(got))
	}
	for i, tx := range txs {
		if tx.GetGroupRef() == "" {
			t.Fatalf("posting %d was left without a group ref", i)
		}
		if got[i].tx.GetGroupRef() != tx.GetGroupRef() {
			t.Errorf("residual %d ref = %q, want %q", i, got[i].tx.GetGroupRef(), tx.GetGroupRef())
		}
	}
	if txs[0].GetGroupRef() == txs[1].GetGroupRef() {
		t.Error("two ungrouped postings must not be merged into one group")
	}
}

// TestRouteResiduals_SyntheticRefsDoNotCollide verifies a synthesised ref cannot
// capture a posting the upload named itself, which would merge two events.
func TestRouteResiduals_SyntheticRefsDoNotCollide(t *testing.T) {
	at := time.Date(2025, 3, 1, 0, 0, 0, 0, time.UTC)
	txs := []*apiv1.Tx{
		posting{desc: "AAPL", typ: apiv1.TxType_BUYSTOCK, qty: "10", instID: aaplID, groupRef: "g1"}.tx(at),
		posting{desc: "MSFT", typ: apiv1.TxType_BUYSTOCK, qty: "5", instID: aaplID}.tx(at),
	}
	routeResiduals(txs, []string{aaplID, aaplID}, balanceFixtures())
	if txs[1].GetGroupRef() == "g1" {
		t.Error("a synthesised ref collided with one the upload supplied")
	}
}

func describeRouted(rs []routedPosting) string {
	out := ""
	for _, r := range rs {
		commodity := r.instrumentID
		if r.currency != "" {
			commodity = r.currency
		}
		out += proto.CloneOf(r.tx).String() + " [" + commodity + "] "
	}
	return out
}

// The contract size a price is multiplied by to reach the consideration. The 100
// belongs to the asset class -- an OCC symbol exists only for a standardised
// contract -- while contract_multiplier records the deviation a corporate action
// can leave behind. See the column comment in server/migrations/001_initial.sql.
func TestBalanceInstruments_ContractSize(t *testing.T) {
	cases := []struct {
		name string
		row  *db.InstrumentRow
		want string
	}{{
		name: "standard option contract delivers 100 shares",
		row:  &db.InstrumentRow{AssetClass: strPtr(db.AssetClassOption), ContractMultiplier: decimal.RequireFromString("1")},
		want: "100",
	}, {
		name: "a 3:2 deliverable is recorded as 1.5, meaning 150",
		row:  &db.InstrumentRow{AssetClass: strPtr(db.AssetClassOption), ContractMultiplier: decimal.RequireFromString("1.5")},
		want: "150",
	}, {
		// The column is NOT NULL DEFAULT 1 so the database cannot supply this,
		// but a zero would weigh a whole trade to nothing.
		name: "an absent multiplier falls back to the standard",
		row:  &db.InstrumentRow{AssetClass: strPtr(db.AssetClassOption)},
		want: "100",
	}, {
		name: "a share is quoted in the units it trades in",
		row:  &db.InstrumentRow{AssetClass: strPtr(db.AssetClassStock), ContractMultiplier: decimal.RequireFromString("1")},
		want: "1",
	}, {
		name: "so is a currency",
		row:  &db.InstrumentRow{AssetClass: strPtr(db.AssetClassCash), Currency: strPtr("USD")},
		want: "1",
	}, {
		// A future's size varies per contract and nothing stores it, so one
		// weighs as it always has. See docs/issues/0072.
		name: "a future is left as it was",
		row:  &db.InstrumentRow{AssetClass: strPtr(db.AssetClassFuture), ContractMultiplier: decimal.RequireFromString("1")},
		want: "1",
	}}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := balanceInstruments(map[string]*db.InstrumentRow{"i": tc.row})["i"].contractSize
			if got.String() != tc.want {
				t.Errorf("contract size = %v, want %v", got, tc.want)
			}
		})
	}
}

// A non-standard deliverable balances against the cash it actually settled for,
// which is what makes the multiplier worth reading rather than assuming 100.
func TestRouteResiduals_NonStandardDeliverable(t *testing.T) {
	at := time.Date(2025, 3, 1, 0, 0, 0, 0, time.UTC)
	price := func(v string) *string { return &v }
	instruments := balanceInstruments(map[string]*db.InstrumentRow{
		optID: {ID: optID, AssetClass: strPtr(db.AssetClassOption), ContractMultiplier: decimal.RequireFromString("1.5")},
		usdID: {ID: usdID, AssetClass: strPtr(db.AssetClassCash), Currency: strPtr("USD")},
	})

	// 2 contracts of 150 shares at 3.00 is 900, not the 600 a standard one costs.
	txs := []*apiv1.Tx{
		posting{desc: "OPT", typ: apiv1.TxType_BUYOPT, qty: "2", price: price("3"), settle: "USD", instID: optID, groupRef: "t1"}.tx(at),
		posting{desc: "USD", typ: apiv1.TxType_CASHFLOW, qty: "-900", price: price("1"), settle: "USD", trading: "USD", instID: usdID, groupRef: "t1"}.tx(at),
	}

	got := routeResiduals(txs, []string{optID, usdID}, instruments)
	if len(got) != 0 {
		t.Fatalf("routed %d postings, want none: %s", len(got), describeRouted(got))
	}
}

// TestWeights covers the stored form of each branch of the weight rule. The values
// are the ones routeResiduals sums on, so what is asserted here is what a group's
// balance is checked against.
func TestWeights(t *testing.T) {
	at := time.Date(2025, 3, 1, 0, 0, 0, 0, time.UTC)
	price := func(v string) *string { return &v }
	instruments := balanceFixtures()

	cases := []struct {
		name      string
		posting   posting
		instID    string
		amount    string
		commodity string
	}{{
		// The converting branch: a security leg reaches its counter-leg's units at
		// its price, so it weighs money rather than shares.
		name:      "a priced buy weighs its consideration in the settlement currency",
		posting:   posting{desc: "AAPL", typ: apiv1.TxType_BUYSTOCK, qty: "10", price: price("185.50"), settle: "USD"},
		instID:    aaplID,
		amount:    "1855",
		commodity: "cur:USD",
	}, {
		// A price is per underlying unit, so the contract size is part of the
		// consideration rather than a correction applied to it.
		name:      "an option leg weighs by its contract size",
		posting:   posting{desc: "OPT", typ: apiv1.TxType_BUYOPT, qty: "8", price: price("20.1105585"), settle: "USD"},
		instID:    optID,
		amount:    "16088.4468",
		commodity: "cur:USD",
	}, {
		// The settlement-currency guard: a cash row is already in the units the
		// group balances in, so its price is not a conversion rate whatever the
		// broker typed in the tx type column.
		name:      "a cash row typed as a sale weighs its own quantity",
		posting:   posting{desc: "USD", typ: apiv1.TxType_SELLSTOCK, qty: "-1855", price: price("185.50"), settle: "USD", trading: "USD"},
		instID:    usdID,
		amount:    "-1855",
		commodity: "cur:USD",
	}, {
		// No price is not a price of zero: there is nothing to convert at, so the
		// weight stays in the security and the missing price shows up as a
		// share-denominated residual.
		name:      "an unpriced buy weighs shares in its instrument",
		posting:   posting{desc: "AAPL", typ: apiv1.TxType_BUYSTOCK, qty: "10", settle: "USD"},
		instID:    aaplID,
		amount:    "10",
		commodity: "inst:" + aaplID,
	}, {
		// A movement across currencies does have its counter-leg in other units,
		// and the price is the FX rate.
		name:      "a cross-currency dividend converts at the FX rate",
		posting:   posting{desc: "EUR", typ: apiv1.TxType_INCOME, qty: "100", price: price("1.35"), settle: "USD", trading: "EUR"},
		instID:    eurID,
		amount:    "135",
		commodity: "cur:USD",
	}, {
		// A journal moves a commodity without converting it, so the weight holds
		// the shares rather than a frozen cash value.
		name:      "a securities journal weighs shares even with a price",
		posting:   posting{desc: "AAPL", typ: apiv1.TxType_JRNLSEC, qty: "-10", price: price("185.50"), settle: "USD"},
		instID:    aaplID,
		amount:    "-10",
		commodity: "inst:" + aaplID,
	}, {
		// The fallback that keeps weight_commodity non-empty: a posting whose
		// instrument never resolved still balances against itself.
		name:      "an unresolved posting falls back to its description",
		posting:   posting{desc: "MYSTERY CORP", typ: apiv1.TxType_BUYSTOCK, qty: "5"},
		instID:    "",
		amount:    "5",
		commodity: "desc:MYSTERY CORP",
	}}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := weights([]*apiv1.Tx{tc.posting.tx(at)}, []string{tc.instID}, instruments)
			if len(got) != 1 {
				t.Fatalf("weights returned %d entries, want 1", len(got))
			}
			if got[0].Amount.String() != tc.amount {
				t.Errorf("amount = %v, want %v", got[0].Amount, tc.amount)
			}
			if got[0].Commodity != tc.commodity {
				t.Errorf("commodity = %q, want %q", got[0].Commodity, tc.commodity)
			}
		})
	}
}

// TestWeights_GroupSumsToZeroOnceRouted is the property the balance constraint will
// check: the stored weights of a group, including the routed counterparty, sum to
// exactly zero in every commodity. The netted fee makes it non-trivial -- the group
// as supplied does not balance, and it is the routed posting that closes it.
func TestWeights_GroupSumsToZeroOnceRouted(t *testing.T) {
	at := time.Date(2025, 3, 1, 0, 0, 0, 0, time.UTC)
	price := func(v string) *string { return &v }
	instruments := balanceFixtures()

	txs := []*apiv1.Tx{
		posting{desc: "AAPL", typ: apiv1.TxType_BUYSTOCK, qty: "10", price: price("185.50"), settle: "USD", instID: aaplID, groupRef: "t1"}.tx(at),
		posting{desc: "USD", typ: apiv1.TxType_BUYSTOCK, qty: "-1866.95", settle: "USD", trading: "USD", instID: usdID, groupRef: "t1"}.tx(at),
	}
	ids := []string{aaplID, usdID}

	routed := routeResiduals(txs, ids, instruments)
	if len(routed) != 1 {
		t.Fatalf("routed %d postings, want 1: %s", len(routed), describeRouted(routed))
	}

	all := append(weights(txs, ids, instruments), routed[0].weight)
	sums := map[string]decimal.Decimal{}
	for _, w := range all {
		sums[w.Commodity] = sums[w.Commodity].Add(w.Amount)
	}
	for commodity, sum := range sums {
		if !sum.IsZero() {
			t.Errorf("weights sum to %v in %s, want exactly 0", sum, commodity)
		}
	}
}
