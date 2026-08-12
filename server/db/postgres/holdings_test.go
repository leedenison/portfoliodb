package postgres

import (
	"context"
	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	typev1 "github.com/leedenison/portfoliodb/proto/type/v1"
	"github.com/leedenison/portfoliodb/server/db"
	"google.golang.org/protobuf/types/known/timestamppb"
	"testing"
	"time"
)

// TestComputeHoldings_instrumentNameOverTxDescription verifies that the holdings
// instrument_description field uses the instrument's canonical name when the
// instrument has been resolved, falling back to the transaction description
// only when no instrument name is set.  This is important because the
// transaction description reflects the broker's label (e.g. "MSFT MICROSOFT
// CORP" on a dividend) while the instrument name reflects what the instrument
// actually is (e.g. "USD" for a cash instrument).
func TestComputeHoldings_instrumentNameOverTxDescription(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	userID, _ := p.GetOrCreateUser(ctx, "sub|desc", "U", "u@u.com")
	_, _ = p.CreatePortfolio(ctx, userID, "P")
	now := time.Now()
	from := timestamppb.New(now.Add(-1 * time.Hour))
	to := timestamppb.New(now)
	ts := timestamppb.New(now.Add(-30 * time.Minute))

	// Create a cash instrument with a canonical name "USD".
	cashID, err := p.EnsureInstrument(ctx, "CASH", "", "USD", "USD", "", "",
		[]db.IdentifierInput{{Type: "CURRENCY", Value: "USD", Canonical: true}}, "", nil, nil, nil)
	if err != nil {
		t.Fatalf("ensure cash instrument: %v", err)
	}

	// An income transaction whose tx description is the source security, not
	// the cash instrument name.
	txs := []*apiv1.Tx{
		{Timestamp: ts, InstrumentDescription: "MSFT MICROSOFT CORP", BrokerTxType: []typev1.TxType{typev1.TxType_INCOME}, ResolvedTxType: typev1.TxType_INCOME, Quantity: "137.08", Account: ""},
	}
	if err := p.ReplaceTxsInPeriod(ctx, userID, "IBKR", "", from, to, txs, []string{cashID}, nil, nil); err != nil {
		t.Fatalf("replace: %v", err)
	}

	holdings, _, err := p.ComputeHoldings(ctx, userID, nil, "", nil)
	if err != nil {
		t.Fatalf("holdings: %v", err)
	}
	if len(holdings) != 1 {
		t.Fatalf("expected 1 holding, got %d", len(holdings))
	}
	h := holdings[0]
	if h.InstrumentDescription != "USD" {
		t.Errorf("holding instrument_description = %q, want %q (instrument name, not tx description)", h.InstrumentDescription, "USD")
	}
}

// TestComputeHoldings_signedQuantity verifies holdings are SUM(quantity) with no type-based sign flip.
// Sells have negative quantity; buys positive. A position that is net short has negative holding.
func TestComputeHoldings_signedQuantity(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	userID, _ := p.GetOrCreateUser(ctx, "sub|hold", "U", "u@u.com")
	_, _ = p.CreatePortfolio(ctx, userID, "P")
	now := time.Now()
	from := timestamppb.New(now.Add(-1 * time.Hour))
	to := timestamppb.New(now)
	ts := timestamppb.New(now.Add(-30 * time.Minute))
	// Only a sell with negative quantity: no buys. Net position should be -5.
	txs := []*apiv1.Tx{
		{Timestamp: ts, InstrumentDescription: "GOOG", BrokerTxType: []typev1.TxType{typev1.TxType_TRADE_ASSET}, ResolvedTxType: typev1.TxType_TRADE_ASSET, Quantity: "-5", Account: ""},
	}
	instID, err := p.EnsureInstrument(ctx, "", "", "", "", "", "", []db.IdentifierInput{{Type: "BROKER_DESCRIPTION", Domain: "IBKR", Value: "GOOG", Canonical: false}}, "", nil, nil, nil)
	if err != nil {
		t.Fatalf("ensure instrument: %v", err)
	}
	err = p.ReplaceTxsInPeriod(ctx, userID, "IBKR", "", from, to, txs, []string{instID}, nil, nil)
	if err != nil {
		t.Fatalf("replace: %v", err)
	}
	holdings, _, err := p.ComputeHoldings(ctx, userID, nil, "", nil)
	if err != nil {
		t.Fatalf("holdings: %v", err)
	}
	var googQty string
	for _, h := range holdings {
		if h.InstrumentDescription == "GOOG" {
			googQty = h.SplitAdjustedQuantity
			break
		}
	}
	if googQty != "-5" {
		t.Fatalf("expected GOOG quantity -5 (signed quantity, no type-based flip), got %v", googQty)
	}
}

// TestComputeHoldings_fractionalQuantitiesSumExactly pins the reason the quantity
// columns are NUMERIC. Ten buys of 0.1 sum to exactly 1 in decimal; in binary
// floating point they sum to 0.9999999999999999, and that residual reached the
// user as the holding quantity. qty_is_zero's old epsilon hid the closed-position
// case but nothing guarded the open one.
func TestComputeHoldings_fractionalQuantitiesSumExactly(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	userID, _ := p.GetOrCreateUser(ctx, "sub|exact", "U", "u@u.com")
	now := time.Now()
	from := timestamppb.New(now.Add(-1 * time.Hour))
	to := timestamppb.New(now)
	at := timestamppb.New(now.Add(-30 * time.Minute))

	txs := make([]*apiv1.Tx, 0, 10)
	instIDs := make([]string, 0, 10)
	instID, err := p.EnsureInstrument(ctx, "", "", "", "", "", "", []db.IdentifierInput{
		{Type: "BROKER_DESCRIPTION", Domain: "IBKR", Value: "FRAC", Canonical: false},
	}, "", nil, nil, nil)
	if err != nil {
		t.Fatalf("ensure instrument: %v", err)
	}
	for i := 0; i < 10; i++ {
		txs = append(txs, &apiv1.Tx{
			Timestamp: at, InstrumentDescription: "FRAC",
			BrokerTxType: []typev1.TxType{typev1.TxType_TRADE_ASSET}, ResolvedTxType: typev1.TxType_TRADE_ASSET, Quantity: "0.1",
		})
		instIDs = append(instIDs, instID)
	}
	if err := p.ReplaceTxsInPeriod(ctx, userID, "IBKR", "", from, to, txs, instIDs, nil, nil); err != nil {
		t.Fatalf("replace: %v", err)
	}

	holdings, _, err := p.ComputeHoldings(ctx, userID, nil, "", nil)
	if err != nil {
		t.Fatalf("holdings: %v", err)
	}
	var got string
	for _, h := range holdings {
		if h.InstrumentDescription == "FRAC" {
			got = h.SplitAdjustedQuantity
			break
		}
	}
	// Exact equality, not approxEq: that is the whole point.
	if got != "1" {
		t.Fatalf("ten buys of 0.1: got %v want exactly 1", got)
	}
}

// TestComputeHoldings_excludesNonUserAccountTypes verifies the counterparty leg of a
// one-sided event does not net against the holding it balances. The EQUITY leg here is
// what an INITIALIZE pad's counterparty looks like: same instrument, same broker
// account, equal and opposite. Without the account_type predicate the two sum to zero
// and the declared opening balance silently disappears.
func TestComputeHoldings_excludesNonUserAccountTypes(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	userID, _ := p.GetOrCreateUser(ctx, "sub|acct-type-hold", "U", "u@ah.com")
	now := time.Now()
	from := timestamppb.New(now.Add(-1 * time.Hour))
	to := timestamppb.New(now)
	ts := timestamppb.New(now.Add(-30 * time.Minute))
	instID, err := p.EnsureInstrument(ctx, "", "", "", "", "", "",
		[]db.IdentifierInput{{Type: "BROKER_DESCRIPTION", Domain: "IBKR", Value: "TSCO", Canonical: false}}, "", nil, nil, nil)
	if err != nil {
		t.Fatalf("ensure instrument: %v", err)
	}
	txs := []*apiv1.Tx{
		{Timestamp: ts, InstrumentDescription: "TSCO", BrokerTxType: []typev1.TxType{typev1.TxType_TRADE_ASSET}, ResolvedTxType: typev1.TxType_TRADE_ASSET, Quantity: "40", Account: "A"},
		{Timestamp: ts, InstrumentDescription: "TSCO", BrokerTxType: []typev1.TxType{typev1.TxType_TRADE_ASSET}, ResolvedTxType: typev1.TxType_TRADE_ASSET, Quantity: "-40", Account: "A", AccountType: typev1.AccountType_ACCOUNT_TYPE_EQUITY},
	}
	if err := p.ReplaceTxsInPeriod(ctx, userID, "IBKR", "", from, to, txs, []string{instID, instID}, nil, nil); err != nil {
		t.Fatalf("replace: %v", err)
	}

	holdings, _, err := p.ComputeHoldings(ctx, userID, nil, "", nil)
	if err != nil {
		t.Fatalf("holdings: %v", err)
	}
	if len(holdings) != 1 {
		t.Fatalf("expected 1 holding, got %d", len(holdings))
	}
	if got := holdings[0].SplitAdjustedQuantity; got != "40" {
		t.Errorf("holding quantity = %v, want 40: the EQUITY counter-leg must not net against the position", got)
	}
}

// splitStraddlingHolding seeds one instrument with a split between two postings and
// returns the user and instrument ids. The buy is denominated in the pre-split share
// count and the sell in the post-split one, which is the case the closed-position
// test used to get wrong: the raw quantities are in different units and their sum
// means nothing.
func splitStraddlingHolding(t *testing.T, p *Postgres, sub, buyQty, sellQty string, from, to int) (string, string) {
	t.Helper()
	ctx := context.Background()
	userID, _ := p.GetOrCreateUser(ctx, sub, "U", sub+"@u.com")
	instID, err := p.EnsureInstrument(ctx, "", "", "", "", "", "",
		[]db.IdentifierInput{{Type: "BROKER_DESCRIPTION", Domain: "IBKR", Value: "SPL" + sub, Canonical: false}}, "", nil, nil, nil)
	if err != nil {
		t.Fatalf("ensure instrument: %v", err)
	}
	preSplit := time.Date(2021, 3, 1, 10, 0, 0, 0, time.UTC)
	exDate := time.Date(2022, 6, 1, 0, 0, 0, 0, time.UTC)
	postSplit := time.Date(2023, 3, 1, 10, 0, 0, 0, time.UTC)
	// The split goes in first so the insert trigger denominates each posting's
	// split_adjusted_quantity against it.
	addSplit(t, p, instID, exDate, from, to)

	buy := &apiv1.Tx{Timestamp: timestamppb.New(preSplit), InstrumentDescription: "SPL" + sub,
		BrokerTxType: []typev1.TxType{typev1.TxType_TRADE_ASSET}, ResolvedTxType: typev1.TxType_TRADE_ASSET, Quantity: buyQty, Account: "A"}
	if err := createTx(ctx, p, userID, "IBKR", "A", "", buy, instID, nil); err != nil {
		t.Fatalf("create pre-split buy: %v", err)
	}
	sell := &apiv1.Tx{Timestamp: timestamppb.New(postSplit), InstrumentDescription: "SPL" + sub,
		BrokerTxType: []typev1.TxType{typev1.TxType_TRADE_ASSET}, ResolvedTxType: typev1.TxType_TRADE_ASSET, Quantity: sellQty, Account: "A"}
	if err := createTx(ctx, p, userID, "IBKR", "A", "", sell, instID, nil); err != nil {
		t.Fatalf("create post-split sell: %v", err)
	}
	// The insert trigger seeds the adjusted columns from the raw ones; converting
	// them is the recompute pass's job, which ingestion runs after a split lands.
	if err := p.RecomputeSplitAdjustments(ctx, instID); err != nil {
		t.Fatalf("recompute split adjustments: %v", err)
	}
	return userID, instID
}

// TestComputeHoldings_closedAcrossSplit is the phantom holding 0074 names. 100 shares
// bought before a 2:1 split are 200 after it, so selling 200 closes the position --
// but the raw quantities sum to -100, and a test against that sum reported a short
// position the user never held.
func TestComputeHoldings_closedAcrossSplit(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	userID, _ := splitStraddlingHolding(t, p, "sub|split-closed", "100", "-200", 1, 2)

	holdings, _, err := p.ComputeHoldings(ctx, userID, nil, "", nil)
	if err != nil {
		t.Fatalf("holdings: %v", err)
	}
	if len(holdings) != 0 {
		t.Fatalf("expected no holdings, got %+v", holdings)
	}
}

// TestComputeHoldings_openAcrossSplit is the other half of the same error: the raw
// sum is zero here, so the holding used to vanish, though half the position is still
// held.
func TestComputeHoldings_openAcrossSplit(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	userID, _ := splitStraddlingHolding(t, p, "sub|split-open", "100", "-100", 1, 2)

	holdings, _, err := p.ComputeHoldings(ctx, userID, nil, "", nil)
	if err != nil {
		t.Fatalf("holdings: %v", err)
	}
	if len(holdings) != 1 {
		t.Fatalf("expected 1 holding, got %+v", holdings)
	}
	// 100 pre-split shares are 200 post-split; selling 100 leaves 100.
	if got := holdings[0].SplitAdjustedQuantity; got != "100" {
		t.Fatalf("holding quantity = %v, want 100", got)
	}
}

// TestComputeHoldings_closedAcrossInexactSplit is why the closed-position test is a
// bound rather than an equality against zero. A 3:1 reverse split converts by a
// third, which has no exact decimal form, so each pre-split posting rounds at the
// split-adjusted columns' declared scale. Two buys of 10 store 3.333333333333 each;
// the 6.666666666667 the broker then sold is what the position actually was, and
// the sum of the three lands 1e-12 from zero. The position is closed and the
// holding must not be listed.
func TestComputeHoldings_closedAcrossInexactSplit(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	userID, _ := p.GetOrCreateUser(ctx, "sub|split-inexact", "U", "u@si.com")
	instID, err := p.EnsureInstrument(ctx, "", "", "", "", "", "",
		[]db.IdentifierInput{{Type: "BROKER_DESCRIPTION", Domain: "IBKR", Value: "REV", Canonical: false}}, "", nil, nil, nil)
	if err != nil {
		t.Fatalf("ensure instrument: %v", err)
	}
	preSplit := time.Date(2021, 3, 1, 10, 0, 0, 0, time.UTC)
	exDate := time.Date(2022, 6, 1, 0, 0, 0, 0, time.UTC)
	postSplit := time.Date(2023, 3, 1, 10, 0, 0, 0, time.UTC)
	addSplit(t, p, instID, exDate, 3, 1)

	for _, q := range []string{"10", "10"} {
		buy := &apiv1.Tx{Timestamp: timestamppb.New(preSplit), InstrumentDescription: "REV",
			BrokerTxType: []typev1.TxType{typev1.TxType_TRADE_ASSET}, ResolvedTxType: typev1.TxType_TRADE_ASSET, Quantity: q, Account: "A"}
		if err := createTx(ctx, p, userID, "IBKR", "A", "", buy, instID, nil); err != nil {
			t.Fatalf("create pre-split buy: %v", err)
		}
	}
	sell := &apiv1.Tx{Timestamp: timestamppb.New(postSplit), InstrumentDescription: "REV",
		BrokerTxType: []typev1.TxType{typev1.TxType_TRADE_ASSET}, ResolvedTxType: typev1.TxType_TRADE_ASSET, Quantity: "-6.666666666667", Account: "A"}
	if err := createTx(ctx, p, userID, "IBKR", "A", "", sell, instID, nil); err != nil {
		t.Fatalf("create post-split sell: %v", err)
	}
	if err := p.RecomputeSplitAdjustments(ctx, instID); err != nil {
		t.Fatalf("recompute split adjustments: %v", err)
	}

	holdings, _, err := p.ComputeHoldings(ctx, userID, nil, "", nil)
	if err != nil {
		t.Fatalf("holdings: %v", err)
	}
	if len(holdings) != 0 {
		t.Fatalf("expected no holdings, got %+v", holdings)
	}
}

// TestComputeHoldingsForPortfolio_closedAcrossSplit pins the same fix in the
// portfolio query, which carries its own copy of the aggregate.
func TestComputeHoldingsForPortfolio_closedAcrossSplit(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	userID, _ := splitStraddlingHolding(t, p, "sub|split-port", "100", "-200", 1, 2)
	port, err := p.CreatePortfolio(ctx, userID, "P")
	if err != nil {
		t.Fatalf("create portfolio: %v", err)
	}
	if err := p.SetPortfolioFilters(ctx, port.GetId(), []db.PortfolioFilter{
		{FilterType: "broker", FilterValue: "IBKR"},
	}); err != nil {
		t.Fatalf("set filters: %v", err)
	}

	holdings, _, err := p.ComputeHoldingsForPortfolio(ctx, port.GetId(), nil)
	if err != nil {
		t.Fatalf("holdings: %v", err)
	}
	if len(holdings) != 0 {
		t.Fatalf("expected no holdings, got %+v", holdings)
	}
}
