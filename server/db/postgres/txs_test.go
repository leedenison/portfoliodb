package postgres

import (
	"context"
	"testing"
	"time"

	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	"github.com/leedenison/portfoliodb/server/db"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestReplaceTxsInPeriod_and_ComputeHoldings(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	userID, _ := p.GetOrCreateUser(ctx, "sub|tx", "U", "u@u.com")
	_, _ = p.CreatePortfolio(ctx, userID, "P")
	now := time.Now()
	from := timestamppb.New(now.Add(-2 * time.Hour))
	to := timestamppb.New(now)
	ts1 := timestamppb.New(now.Add(-90 * time.Minute))
	ts2 := timestamppb.New(now.Add(-30 * time.Minute))
	txs := []*apiv1.Tx{
		{Timestamp: ts1, InstrumentDescription: "AAPL", Type: apiv1.TxType_BUYSTOCK, Quantity: 10, Account: ""},
		{Timestamp: ts2, InstrumentDescription: "AAPL", Type: apiv1.TxType_SELLSTOCK, Quantity: -3, Account: ""},
	}
	instID, err := p.EnsureInstrument(ctx, "", "", "", "", "", "", []db.IdentifierInput{{Type: "BROKER_DESCRIPTION", Domain: "IBKR", Value: "AAPL", Canonical: false}}, "", nil, nil, nil)
	if err != nil {
		t.Fatalf("ensure instrument: %v", err)
	}
	instrumentIDs := []string{instID, instID}
	err = p.ReplaceTxsInPeriod(ctx, userID, "IBKR", from, to, txs, instrumentIDs, nil)
	if err != nil {
		t.Fatalf("replace: %v", err)
	}
	holdings, asOf, err := p.ComputeHoldings(ctx, userID, nil, "", nil)
	if err != nil {
		t.Fatalf("holdings: %v", err)
	}
	if asOf == nil {
		t.Fatal("asOf should be set")
	}
	var aaplQty float64
	for _, h := range holdings {
		if h.InstrumentDescription == "AAPL" {
			aaplQty = h.Quantity
			break
		}
	}
	if aaplQty != 7 {
		t.Fatalf("expected AAPL quantity 7 (10 + -3), got %v", aaplQty)
	}
}

func TestReplaceTxsInPeriod_PreservesSyntheticInitializeTx(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	userID, _ := p.GetOrCreateUser(ctx, "sub|synth", "U", "u@u.com")
	_, _ = p.CreatePortfolio(ctx, userID, "P")

	now := time.Now()
	from := timestamppb.New(now.Add(-2 * time.Hour))
	to := timestamppb.New(now)
	// Synthetic INITIALIZE tx timestamp falls inside [from, to]
	initTs := now.Add(-90 * time.Minute)

	instID, err := p.EnsureInstrument(ctx, "", "", "", "", "", "", []db.IdentifierInput{{Type: "BROKER_DESCRIPTION", Domain: "IBKR", Value: "MSFT", Canonical: false}}, "", nil, nil, nil)
	if err != nil {
		t.Fatalf("ensure instrument: %v", err)
	}

	// Seed an INITIALIZE synthetic tx and an unrelated real tx, both inside the period.
	if err := p.UpsertInitializeTx(ctx, userID, "IBKR", "", instID, "BUYOTHER", initTs, 42); err != nil {
		t.Fatalf("upsert initialize: %v", err)
	}
	oldTx := []*apiv1.Tx{
		{Timestamp: timestamppb.New(now.Add(-80 * time.Minute)), InstrumentDescription: "MSFT", Type: apiv1.TxType_BUYSTOCK, Quantity: 5, Account: ""},
	}
	if err := p.ReplaceTxsInPeriod(ctx, userID, "IBKR", from, to, oldTx, []string{instID}, nil); err != nil {
		t.Fatalf("seed real tx: %v", err)
	}

	// Replace real txs in the same period with a fresh set; synthetic must survive.
	newTxs := []*apiv1.Tx{
		{Timestamp: timestamppb.New(now.Add(-60 * time.Minute)), InstrumentDescription: "MSFT", Type: apiv1.TxType_BUYSTOCK, Quantity: 7, Account: ""},
		{Timestamp: timestamppb.New(now.Add(-20 * time.Minute)), InstrumentDescription: "MSFT", Type: apiv1.TxType_SELLSTOCK, Quantity: -2, Account: ""},
	}
	if err := p.ReplaceTxsInPeriod(ctx, userID, "IBKR", from, to, newTxs, []string{instID, instID}, nil); err != nil {
		t.Fatalf("replace: %v", err)
	}

	// Verify both new real txs and the synthetic INITIALIZE row are present.
	rows, _, err := p.ListTxs(ctx, userID, nil, "", from, to, false, 100, "")
	if err != nil {
		t.Fatalf("list txs: %v", err)
	}
	var sawInit, sawBuy, sawSell bool
	var sawOldBuy bool
	for _, r := range rows {
		switch {
		case r.GetTx().GetSyntheticPurpose() == "INITIALIZE":
			sawInit = true
			if r.GetTx().GetQuantity() != 42 {
				t.Errorf("synthetic qty: want 42, got %v", r.GetTx().GetQuantity())
			}
		case r.GetTx().GetQuantity() == 7:
			sawBuy = true
		case r.GetTx().GetQuantity() == -2:
			sawSell = true
		case r.GetTx().GetQuantity() == 5:
			sawOldBuy = true
		}
	}
	if !sawInit {
		t.Error("synthetic INITIALIZE tx was deleted by ReplaceTxsInPeriod")
	}
	if !sawBuy || !sawSell {
		t.Errorf("new real txs missing: buy=%v sell=%v", sawBuy, sawSell)
	}
	if sawOldBuy {
		t.Error("old real tx survived ReplaceTxsInPeriod")
	}
}

func TestCreateTx_AppendOnly(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	userID, _ := p.GetOrCreateUser(ctx, "sub|up", "U", "u@u.com")
	_, _ = p.CreatePortfolio(ctx, userID, "P")
	ts := timestamppb.Now()
	tx1 := &apiv1.Tx{Timestamp: ts, InstrumentDescription: "GOOG", Type: apiv1.TxType_BUYSTOCK, Quantity: 5, Account: ""}
	tx2 := &apiv1.Tx{Timestamp: ts, InstrumentDescription: "GOOG", Type: apiv1.TxType_BUYSTOCK, Quantity: 10, Account: ""}
	instID, err := p.EnsureInstrument(ctx, "", "", "", "", "", "", []db.IdentifierInput{{Type: "BROKER_DESCRIPTION", Domain: "IBKR", Value: "GOOG", Canonical: false}}, "", nil, nil, nil)
	if err != nil {
		t.Fatalf("ensure instrument: %v", err)
	}
	if err := p.CreateTx(ctx, userID, "IBKR", "", tx1, instID); err != nil {
		t.Fatalf("first create: %v", err)
	}
	if err := p.CreateTx(ctx, userID, "IBKR", "", tx2, instID); err != nil {
		t.Fatalf("second create: %v", err)
	}
	holdings, _, _ := p.ComputeHoldings(ctx, userID, nil, "", nil)
	for _, h := range holdings {
		if h.InstrumentDescription == "GOOG" && h.Quantity != 15 {
			t.Fatalf("append-only: expected total quantity 15, got %v", h.Quantity)
		}
	}
}

func TestListTxs_BrokerFilterAndOrder(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	userID, _ := p.GetOrCreateUser(ctx, "sub|order", "U", "u@u.com")
	instID, err := p.EnsureInstrument(ctx, "", "", "", "", "", "", []db.IdentifierInput{{Type: "BROKER_DESCRIPTION", Domain: "IBKR", Value: "ORD", Canonical: false}}, "", nil, nil, nil)
	if err != nil {
		t.Fatalf("ensure instrument: %v", err)
	}
	now := time.Now().Truncate(time.Second)
	// Quantity doubles as an identity marker so ordering can be asserted.
	seed := []struct {
		broker apiv1.Broker
		offset time.Duration
		qty    float64
	}{
		{apiv1.Broker_IBKR, -3 * time.Hour, 1},
		{apiv1.Broker_FIDELITY, -2 * time.Hour, 2},
		{apiv1.Broker_IBKR, -1 * time.Hour, 3},
	}
	for _, s := range seed {
		// Seed through the same enum-to-string mapping the filter uses, rather than
		// a literal: the stored strings are not simply the enum names.
		brokerStr, err := brokerToStr(s.broker)
		if err != nil {
			t.Fatalf("broker to str: %v", err)
		}
		tx := &apiv1.Tx{Timestamp: timestamppb.New(now.Add(s.offset)), InstrumentDescription: "ORD", Type: apiv1.TxType_BUYSTOCK, Quantity: s.qty}
		if err := p.CreateTx(ctx, userID, brokerStr, "", tx, instID); err != nil {
			t.Fatalf("create tx: %v", err)
		}
	}

	qtys := func(rows []*apiv1.PortfolioTx) []float64 {
		out := make([]float64, len(rows))
		for i, r := range rows {
			out[i] = r.GetTx().GetQuantity()
		}
		return out
	}
	fidelity := apiv1.Broker_FIDELITY.Enum()

	cases := []struct {
		name       string
		broker     *apiv1.Broker
		descending bool
		want       []float64
	}{
		{"ascending, all brokers", nil, false, []float64{1, 2, 3}},
		{"descending, all brokers", nil, true, []float64{3, 2, 1}},
		{"broker filter", fidelity, false, []float64{2}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rows, _, err := p.ListTxs(ctx, userID, tc.broker, "", nil, nil, tc.descending, 50, "")
			if err != nil {
				t.Fatalf("list txs: %v", err)
			}
			got := qtys(rows)
			if len(got) != len(tc.want) {
				t.Fatalf("want %v, got %v", tc.want, got)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("want %v, got %v", tc.want, got)
				}
			}
		})
	}

	// The most recent transaction for one broker in a single row -- the query the
	// import extension issues to size its fetch window.
	rows, _, err := p.ListTxs(ctx, userID, apiv1.Broker_IBKR.Enum(), "", nil, nil, true, 1, "")
	if err != nil {
		t.Fatalf("latest tx: %v", err)
	}
	if len(rows) != 1 || rows[0].GetTx().GetQuantity() != 3 {
		t.Fatalf("latest IBKR tx: want qty 3, got %v", qtys(rows))
	}
}

// Rows sharing a timestamp must not be skipped or repeated across a page
// boundary. Ordering by timestamp alone is not a total order, so the id
// tiebreaker is what makes offset paging stable.
func TestListTxs_TiedTimestampsPageBoundary(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	userID, _ := p.GetOrCreateUser(ctx, "sub|ties", "U", "u@u.com")
	instID, err := p.EnsureInstrument(ctx, "", "", "", "", "", "", []db.IdentifierInput{{Type: "BROKER_DESCRIPTION", Domain: "IBKR", Value: "TIE", Canonical: false}}, "", nil, nil, nil)
	if err != nil {
		t.Fatalf("ensure instrument: %v", err)
	}
	// Six transactions on one date, as a broker statement that carries no time
	// of day would produce.
	ts := timestamppb.New(time.Now().Truncate(24 * time.Hour))
	const total = 6
	for i := 0; i < total; i++ {
		tx := &apiv1.Tx{Timestamp: ts, InstrumentDescription: "TIE", Type: apiv1.TxType_BUYSTOCK, Quantity: float64(i + 1)}
		if err := p.CreateTx(ctx, userID, "IBKR", "", tx, instID); err != nil {
			t.Fatalf("create tx %d: %v", i, err)
		}
	}

	for _, descending := range []bool{false, true} {
		name := "ascending"
		if descending {
			name = "descending"
		}
		t.Run(name, func(t *testing.T) {
			seen := map[float64]int{}
			token := ""
			for pages := 0; ; pages++ {
				if pages > total {
					t.Fatal("paging did not terminate")
				}
				rows, next, err := p.ListTxs(ctx, userID, nil, "", nil, nil, descending, 2, token)
				if err != nil {
					t.Fatalf("list txs: %v", err)
				}
				for _, r := range rows {
					seen[r.GetTx().GetQuantity()]++
				}
				if next == "" {
					break
				}
				token = next
			}
			if len(seen) != total {
				t.Fatalf("want %d distinct txs across pages, got %d: %v", total, len(seen), seen)
			}
			for qty, count := range seen {
				if count != 1 {
					t.Errorf("tx qty %v returned %d times across pages", qty, count)
				}
			}
		})
	}
}

func TestListTxsByPortfolio_ComputeHoldingsForPortfolio(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	userID, _ := p.GetOrCreateUser(ctx, "sub|pv", "U", "u@u.com")
	port, err := p.CreatePortfolio(ctx, userID, "P")
	if err != nil {
		t.Fatalf("create portfolio: %v", err)
	}
	// No filters: portfolio view should return no txs
	txs, tok, err := p.ListTxsByPortfolio(ctx, port.GetId(), nil, nil, nil, false, 50, "")
	if err != nil {
		t.Fatalf("ListTxsByPortfolio no filters: %v", err)
	}
	if len(txs) != 0 || tok != "" {
		t.Fatalf("no filters should return 0 txs, got %d %q", len(txs), tok)
	}
	holdings, asOf, err := p.ComputeHoldingsForPortfolio(ctx, port.GetId(), nil)
	if err != nil {
		t.Fatalf("ComputeHoldingsForPortfolio no filters: %v", err)
	}
	if len(holdings) != 0 || asOf == nil {
		t.Fatalf("no filters holdings: %v asOf %v", holdings, asOf)
	}
	// Add broker=IBKR filter
	if err := p.SetPortfolioFilters(ctx, port.GetId(), []db.PortfolioFilter{{FilterType: "broker", FilterValue: "IBKR"}}); err != nil {
		t.Fatalf("set filters: %v", err)
	}
	now := time.Now()
	from := timestamppb.New(now.Add(-2 * time.Hour))
	to := timestamppb.New(now)
	ts1 := timestamppb.New(now.Add(-90 * time.Minute))
	ts2 := timestamppb.New(now.Add(-30 * time.Minute))
	txList := []*apiv1.Tx{
		{Timestamp: ts1, InstrumentDescription: "AAPL", Type: apiv1.TxType_BUYSTOCK, Quantity: 10, Account: ""},
		{Timestamp: ts2, InstrumentDescription: "AAPL", Type: apiv1.TxType_SELLSTOCK, Quantity: -3, Account: ""},
	}
	instID, err := p.EnsureInstrument(ctx, "", "", "", "", "", "", []db.IdentifierInput{{Type: "BROKER_DESCRIPTION", Domain: "IBKR", Value: "AAPL", Canonical: false}}, "", nil, nil, nil)
	if err != nil {
		t.Fatalf("ensure instrument: %v", err)
	}
	if err := p.ReplaceTxsInPeriod(ctx, userID, "IBKR", from, to, txList, []string{instID, instID}, nil); err != nil {
		t.Fatalf("replace txs: %v", err)
	}
	txs, tok, err = p.ListTxsByPortfolio(ctx, port.GetId(), nil, nil, nil, false, 50, "")
	if err != nil {
		t.Fatalf("ListTxsByPortfolio: %v", err)
	}
	if len(txs) != 2 || tok != "" {
		t.Fatalf("expected 2 txs, got %d nextToken=%q", len(txs), tok)
	}
	holdings, asOf, err = p.ComputeHoldingsForPortfolio(ctx, port.GetId(), nil)
	if err != nil {
		t.Fatalf("ComputeHoldingsForPortfolio: %v", err)
	}
	if asOf == nil {
		t.Fatal("asOf should be set")
	}
	var aaplQty float64
	for _, h := range holdings {
		if h.InstrumentDescription == "AAPL" {
			aaplQty = h.Quantity
			break
		}
	}
	if aaplQty != 7 {
		t.Fatalf("expected AAPL quantity 7 (10-3), got %v", aaplQty)
	}
}

// TestListTxsByPortfolio_ANDBetweenCategories verifies AND-between-categories semantics:
// a tx must match at least one filter in every category that has filters.
func TestListTxsByPortfolio_ANDBetweenCategories(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	userID, _ := p.GetOrCreateUser(ctx, "sub|and", "U", "u@u.com")
	port, err := p.CreatePortfolio(ctx, userID, "P")
	if err != nil {
		t.Fatalf("create portfolio: %v", err)
	}
	// Filters: broker=IBKR AND account=A (tx must match both categories)
	if err := p.SetPortfolioFilters(ctx, port.GetId(), []db.PortfolioFilter{
		{FilterType: "broker", FilterValue: "IBKR"},
		{FilterType: "account", FilterValue: "A"},
	}); err != nil {
		t.Fatalf("set filters: %v", err)
	}
	ts := timestamppb.New(time.Now().Add(-1 * time.Hour))
	instID, err := p.EnsureInstrument(ctx, "", "", "", "", "", "", []db.IdentifierInput{{Type: "BROKER_DESCRIPTION", Domain: "IBKR", Value: "X", Canonical: false}}, "", nil, nil, nil)
	if err != nil {
		t.Fatalf("ensure instrument: %v", err)
	}
	// Tx1: IBKR, account "B" -> matches broker but NOT account -> excluded
	if err := p.CreateTx(ctx, userID, "IBKR", "B", &apiv1.Tx{Timestamp: ts, InstrumentDescription: "X", Type: apiv1.TxType_BUYSTOCK, Quantity: 1, Account: "B"}, instID); err != nil {
		t.Fatalf("create tx1: %v", err)
	}
	// Tx2: SCHB, account "A" -> matches account but NOT broker -> excluded
	if err := p.CreateTx(ctx, userID, "SCHB", "A", &apiv1.Tx{Timestamp: ts, InstrumentDescription: "X", Type: apiv1.TxType_BUYSTOCK, Quantity: 2, Account: "A"}, instID); err != nil {
		t.Fatalf("create tx2: %v", err)
	}
	// Tx3: IBKR, account "A" -> matches both -> included
	if err := p.CreateTx(ctx, userID, "IBKR", "A", &apiv1.Tx{Timestamp: ts, InstrumentDescription: "X", Type: apiv1.TxType_BUYSTOCK, Quantity: 3, Account: "A"}, instID); err != nil {
		t.Fatalf("create tx3: %v", err)
	}
	txs, _, err := p.ListTxsByPortfolio(ctx, port.GetId(), nil, nil, nil, false, 50, "")
	if err != nil {
		t.Fatalf("ListTxsByPortfolio: %v", err)
	}
	if len(txs) != 1 {
		t.Fatalf("expected 1 tx (AND between categories), got %d: %+v", len(txs), txs)
	}
	if txs[0].GetTx().GetQuantity() != 3 {
		t.Fatalf("expected tx3 (qty=3), got qty=%v", txs[0].GetTx().GetQuantity())
	}
	holdings, _, err := p.ComputeHoldingsForPortfolio(ctx, port.GetId(), nil)
	if err != nil {
		t.Fatalf("ComputeHoldingsForPortfolio: %v", err)
	}
	var totalQty float64
	for _, h := range holdings {
		totalQty += h.Quantity
	}
	if totalQty != 3 {
		t.Fatalf("expected total quantity 3, got %v", totalQty)
	}
}
