package postgres

import (
	"context"
	"database/sql"
	"errors"
	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	typev1 "github.com/leedenison/portfoliodb/proto/type/v1"
	"github.com/leedenison/portfoliodb/server/db"
	"google.golang.org/protobuf/types/known/timestamppb"
	"maps"
	"testing"
	"time"
)

// initTx builds a pad denominated in the share count current on its own date, which
// is what the tests that are not about denomination want.
func initTx(at time.Time, qty float64) db.InitializeTx {
	return db.InitializeTx{Timestamp: at, Quantity: decf(qty), ShareCountBasis: at}
}

// addSplit records a stock split. Splits are written by the corporate event path in
// production; these tests need one to exist, not to arrive.
func addSplit(t *testing.T, p *Postgres, instID string, exDate time.Time, from, to int) {
	t.Helper()
	_, err := p.q.ExecContext(context.Background(), `
		INSERT INTO stock_splits (instrument_id, ex_date, split_from, split_to, data_provider)
		VALUES ($1, $2, $3, $4, 'test')
	`, instID, exDate, from, to)
	if err != nil {
		t.Fatalf("insert split: %v", err)
	}
}

func TestCreateHoldingDeclaration(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	userID, _ := p.GetOrCreateUser(ctx, "sub|decl1", "U", "u@u.com")
	instID, _ := p.EnsureInstrument(ctx, "", "", "", "", "", "", []db.IdentifierInput{{Type: "BROKER_DESCRIPTION", Domain: "IBKR", Value: "AAPL", Canonical: false}}, "", nil, nil, nil)

	asOf := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)
	row, err := p.CreateHoldingDeclaration(ctx, userID, "IBKR", "acct1", instID, "150.5", asOf, time.Time{})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if row.Broker != "IBKR" || row.Account != "acct1" || row.DeclaredQty != "150.5" {
		t.Fatalf("unexpected row: %+v", row)
	}
	if row.InstrumentID != instID {
		t.Fatalf("instrument_id mismatch: got %s want %s", row.InstrumentID, instID)
	}
}

// TestCreateHoldingDeclaration_ManyPerHolding pins the widened key: a holding may
// carry a declaration at each of several dates -- the earliest seeding its opening
// balance and the later ones checked against it -- but not two at one date, which
// would be two answers to the same question with nothing to choose between them.
func TestCreateHoldingDeclaration_ManyPerHolding(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	userID, _ := p.GetOrCreateUser(ctx, "sub|decl2", "U", "u@u.com")
	instID, _ := p.EnsureInstrument(ctx, "", "", "", "", "", "", []db.IdentifierInput{{Type: "BROKER_DESCRIPTION", Domain: "IBKR", Value: "GOOG", Canonical: false}}, "", nil, nil, nil)

	asOf := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)
	later := time.Date(2025, 12, 31, 0, 0, 0, 0, time.UTC)
	if _, err := p.CreateHoldingDeclaration(ctx, userID, "IBKR", "acct1", instID, "100", asOf, time.Time{}); err != nil {
		t.Fatalf("first create: %v", err)
	}
	if _, err := p.CreateHoldingDeclaration(ctx, userID, "IBKR", "acct1", instID, "200", later, time.Time{}); err != nil {
		t.Fatalf("second create at a later date: %v", err)
	}

	_, err := p.CreateHoldingDeclaration(ctx, userID, "IBKR", "acct1", instID, "300", asOf, time.Time{})
	if !errors.Is(err, db.ErrDuplicate) {
		t.Fatalf("second create at the same date: want db.ErrDuplicate, got %v", err)
	}
}

// TestListHoldingDeclarations_DerivesKind checks the pad/assert discriminator, which
// is computed from the dates rather than stored. The earliest declaration for a
// holding is its pad; every later one is an assertion. Two holdings are seeded so a
// query that partitioned wrongly would call both earliest rows pads, or neither.
func TestListHoldingDeclarations_DerivesKind(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	userID, _ := p.GetOrCreateUser(ctx, "sub|decl-kind", "U", "u@k.com")
	instA, _ := p.EnsureInstrument(ctx, "", "", "", "", "", "", []db.IdentifierInput{{Type: "BROKER_DESCRIPTION", Domain: "IBKR", Value: "KA", Canonical: false}}, "", nil, nil, nil)
	instB, _ := p.EnsureInstrument(ctx, "", "", "", "", "", "", []db.IdentifierInput{{Type: "BROKER_DESCRIPTION", Domain: "IBKR", Value: "KB", Canonical: false}}, "", nil, nil, nil)

	d2021 := time.Date(2021, 1, 1, 0, 0, 0, 0, time.UTC)
	d2022 := time.Date(2022, 12, 31, 0, 0, 0, 0, time.UTC)
	d2023 := time.Date(2023, 12, 31, 0, 0, 0, 0, time.UTC)
	// Inserted newest first, so the answer cannot come from insertion order.
	for _, seed := range []struct {
		inst string
		asOf time.Time
		qty  string
	}{
		{instA, d2023, "650"},
		{instA, d2022, "500"},
		{instA, d2021, "500"},
		{instB, d2022, "10"},
	} {
		if _, err := p.CreateHoldingDeclaration(ctx, userID, "IBKR", "acct1", seed.inst, seed.qty, seed.asOf, time.Time{}); err != nil {
			t.Fatalf("seed %s at %s: %v", seed.inst, seed.asOf.Format("2006-01-02"), err)
		}
	}

	rows, err := p.ListHoldingDeclarations(ctx, userID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	got := map[string]apiv1.DeclarationKind{}
	for _, r := range rows {
		got[r.InstrumentID+"@"+r.AsOfDate.Format("2006-01-02")] = r.Kind
	}
	want := map[string]apiv1.DeclarationKind{
		instA + "@2021-01-01": apiv1.DeclarationKind_DECLARATION_KIND_PAD,
		instA + "@2022-12-31": apiv1.DeclarationKind_DECLARATION_KIND_ASSERT,
		instA + "@2023-12-31": apiv1.DeclarationKind_DECLARATION_KIND_ASSERT,
		// Earliest for its own holding, so a pad despite being later than instA's.
		instB + "@2022-12-31": apiv1.DeclarationKind_DECLARATION_KIND_PAD,
	}
	if !maps.Equal(got, want) {
		t.Fatalf("derived kinds: got %v, want %v", got, want)
	}

	// A single-row read has to reach the same answer, which is why the derivation is
	// a correlated subquery and not a window over the rows the query returns.
	for _, r := range rows {
		one, err := p.GetHoldingDeclaration(ctx, r.ID)
		if err != nil {
			t.Fatalf("get %s: %v", r.ID, err)
		}
		if one.Kind != r.Kind {
			t.Errorf("kind for %s: list says %v, get says %v", r.ID, r.Kind, one.Kind)
		}
	}
}

func TestUpdateHoldingDeclaration(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	userID, _ := p.GetOrCreateUser(ctx, "sub|decl3", "U", "u@u.com")
	instID, _ := p.EnsureInstrument(ctx, "", "", "", "", "", "", []db.IdentifierInput{{Type: "BROKER_DESCRIPTION", Domain: "IBKR", Value: "MSFT", Canonical: false}}, "", nil, nil, nil)

	asOf := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)
	row, _ := p.CreateHoldingDeclaration(ctx, userID, "IBKR", "acct1", instID, "100", asOf, time.Time{})

	newDate := time.Date(2025, 7, 1, 0, 0, 0, 0, time.UTC)
	updated, err := p.UpdateHoldingDeclaration(ctx, row.ID, "200", newDate, time.Time{})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.DeclaredQty != "200" {
		t.Fatalf("qty not updated: got %s", updated.DeclaredQty)
	}
	if !updated.AsOfDate.Equal(newDate) {
		t.Fatalf("date not updated: got %v", updated.AsOfDate)
	}
}

func TestDeleteHoldingDeclaration(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	userID, _ := p.GetOrCreateUser(ctx, "sub|decl4", "U", "u@u.com")
	instID, _ := p.EnsureInstrument(ctx, "", "", "", "", "", "", []db.IdentifierInput{{Type: "BROKER_DESCRIPTION", Domain: "IBKR", Value: "TSLA", Canonical: false}}, "", nil, nil, nil)

	asOf := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)
	row, _ := p.CreateHoldingDeclaration(ctx, userID, "IBKR", "acct1", instID, "50", asOf, time.Time{})

	if err := p.DeleteHoldingDeclaration(ctx, row.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	// Verify it's gone
	_, err := p.GetHoldingDeclaration(ctx, row.ID)
	if err == nil {
		t.Fatal("expected error after delete")
	}
}

func TestDeleteHoldingDeclaration_NotFound(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	err := p.DeleteHoldingDeclaration(ctx, "00000000-0000-0000-0000-000000000001")
	if err != sql.ErrNoRows {
		t.Fatalf("expected sql.ErrNoRows, got %v", err)
	}
}

func TestListHoldingDeclarations(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	userID, _ := p.GetOrCreateUser(ctx, "sub|decl5", "U", "u@u.com")
	inst1, _ := p.EnsureInstrument(ctx, "", "", "", "", "", "", []db.IdentifierInput{{Type: "BROKER_DESCRIPTION", Domain: "IBKR", Value: "A1", Canonical: false}}, "", nil, nil, nil)
	inst2, _ := p.EnsureInstrument(ctx, "", "", "", "", "", "", []db.IdentifierInput{{Type: "BROKER_DESCRIPTION", Domain: "IBKR", Value: "A2", Canonical: false}}, "", nil, nil, nil)

	asOf := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)
	if _, err := p.CreateHoldingDeclaration(ctx, userID, "IBKR", "acct1", inst1, "100", asOf, time.Time{}); err != nil {
		t.Fatalf("create decl 1: %v", err)
	}
	if _, err := p.CreateHoldingDeclaration(ctx, userID, "IBKR", "acct1", inst2, "200", asOf, time.Time{}); err != nil {
		t.Fatalf("create decl 2: %v", err)
	}

	rows, err := p.ListHoldingDeclarations(ctx, userID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2, got %d", len(rows))
	}
}

func TestGetPortfolioStartDate(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	userID, _ := p.GetOrCreateUser(ctx, "sub|decl6", "U", "u@u.com")

	// No txs: should return nil
	startDate, err := p.GetPortfolioStartDate(ctx, userID)
	if err != nil {
		t.Fatalf("get start date: %v", err)
	}
	if startDate != nil {
		t.Fatalf("expected nil, got %v", startDate)
	}

	// Add a tx
	instID, _ := p.EnsureInstrument(ctx, "", "", "", "", "", "", []db.IdentifierInput{{Type: "BROKER_DESCRIPTION", Domain: "IBKR", Value: "SD1", Canonical: false}}, "", nil, nil, nil)
	ts := time.Date(2025, 3, 15, 10, 0, 0, 0, time.UTC)
	tx := &apiv1.Tx{Timestamp: timestamppb.New(ts), InstrumentDescription: "SD1", BrokerTxType: []typev1.TxType{typev1.TxType_TRADE_ASSET}, ResolvedTxType: typev1.TxType_TRADE_ASSET, Quantity: "10", Account: "acct1"}
	if err := createTx(ctx, p, userID, "IBKR", "acct1", "", tx, instID, nil); err != nil {
		t.Fatalf("create tx: %v", err)
	}

	startDate, err = p.GetPortfolioStartDate(ctx, userID)
	if err != nil {
		t.Fatalf("get start date: %v", err)
	}
	if startDate == nil {
		t.Fatal("expected non-nil start date")
	}
	if !startDate.Equal(ts) {
		t.Fatalf("expected %v, got %v", ts, *startDate)
	}
}

func TestComputeRunningBalance(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	userID, _ := p.GetOrCreateUser(ctx, "sub|decl7", "U", "u@u.com")
	instID, _ := p.EnsureInstrument(ctx, "", "", "", "", "", "", []db.IdentifierInput{{Type: "BROKER_DESCRIPTION", Domain: "IBKR", Value: "RB1", Canonical: false}}, "", nil, nil, nil)

	ts1 := time.Date(2025, 3, 1, 10, 0, 0, 0, time.UTC)
	ts2 := time.Date(2025, 3, 15, 10, 0, 0, 0, time.UTC)
	if err := createTx(ctx, p, userID, "IBKR", "acct1", "", &apiv1.Tx{Timestamp: timestamppb.New(ts1), InstrumentDescription: "RB1", BrokerTxType: []typev1.TxType{typev1.TxType_TRADE_ASSET}, ResolvedTxType: typev1.TxType_TRADE_ASSET, Quantity: "100", Account: "acct1"}, instID, nil); err != nil {
		t.Fatalf("create buy: %v", err)
	}
	if err := createTx(ctx, p, userID, "IBKR", "acct1", "", &apiv1.Tx{Timestamp: timestamppb.New(ts2), InstrumentDescription: "RB1", BrokerTxType: []typev1.TxType{typev1.TxType_TRADE_ASSET}, ResolvedTxType: typev1.TxType_TRADE_ASSET, Quantity: "-30", Account: "acct1"}, instID, nil); err != nil {
		t.Fatalf("create sell: %v", err)
	}

	from := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2025, 12, 31, 23, 59, 59, 0, time.UTC)
	bal, err := p.ComputeRunningBalance(ctx, userID, "IBKR", "acct1", instID, from, to, from)
	if err != nil {
		t.Fatalf("compute: %v", err)
	}
	if bal.String() != "70" {
		t.Fatalf("expected 70, got %v", bal)
	}
}

func TestUpsertAndDeleteInitializeTx(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	userID, _ := p.GetOrCreateUser(ctx, "sub|decl8", "U", "u@u.com")
	instID, _ := p.EnsureInstrument(ctx, "", "", "", "", "", "", []db.IdentifierInput{{Type: "BROKER_DESCRIPTION", Domain: "IBKR", Value: "UI1", Canonical: false}}, "", nil, nil, nil)

	ts := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	err := p.UpsertInitializeTx(ctx, userID, "IBKR", "acct1", instID, initTx(ts, 50))
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}

	// The ledger view is unfiltered, so both legs of the pad show up: the pad
	// itself and the EQUITY counterparty that balances it.
	if got := initQtyByAccountType(t, p, userID); !maps.Equal(got, map[typev1.AccountType]string{
		typev1.AccountType_ACCOUNT_TYPE_USER:   "50",
		typev1.AccountType_ACCOUNT_TYPE_EQUITY: "-50",
	}) {
		t.Fatalf("INITIALIZE legs after create: got %v", got)
	}

	// The pad's type is not caller data: value entering from outside the user's
	// holdings is TRANSFER_EXTERNAL by definition, and the upsert writes that
	// constant on every leg.
	var mistyped int
	if err := p.q.QueryRowContext(ctx, `
		SELECT count(*) FROM txs
		WHERE user_id = $1 AND synthetic_purpose = 'INITIALIZE'
		  AND (broker_tx_type <> ARRAY['TRANSFER_EXTERNAL'] OR resolved_tx_type <> 'TRANSFER_EXTERNAL')
	`, userID).Scan(&mistyped); err != nil {
		t.Fatalf("read pad tx types: %v", err)
	}
	if mistyped != 0 {
		t.Errorf("INITIALIZE legs not typed TRANSFER_EXTERNAL: %d", mistyped)
	}

	// Upsert again with different qty (should update, not duplicate). Both legs
	// move together: a recalculation that shifted only the pad would leave the
	// group unbalanced.
	err = p.UpsertInitializeTx(ctx, userID, "IBKR", "acct1", instID, initTx(ts, 75))
	if err != nil {
		t.Fatalf("upsert update: %v", err)
	}
	if got := initQtyByAccountType(t, p, userID); !maps.Equal(got, map[typev1.AccountType]string{
		typev1.AccountType_ACCOUNT_TYPE_USER:   "75",
		typev1.AccountType_ACCOUNT_TYPE_EQUITY: "-75",
	}) {
		t.Fatalf("INITIALIZE legs after recalculation: got %v", got)
	}

	// Deleting takes both legs: the group is the unit of deletion, so no code path
	// can leave half the event behind.
	err = p.DeleteInitializeTx(ctx, userID, "IBKR", "acct1", instID)
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if got := initQtyByAccountType(t, p, userID); len(got) != 0 {
		t.Fatalf("INITIALIZE legs after delete: want none, got %v", got)
	}
	if got := countOrphanGroups(t, p, userID); got != 0 {
		t.Errorf("orphan tx_groups after delete: want 0, got %d", got)
	}
}

// initQtyByAccountType returns the quantity of each INITIALIZE posting a user
// has, keyed by account type. It reads through ListTxs, which is deliberately
// unfiltered, so both the pad and its counterparty are visible.
func initQtyByAccountType(t *testing.T, p *Postgres, userID string) map[typev1.AccountType]string {
	t.Helper()
	txs, _, err := p.ListTxs(context.Background(), userID, nil, "", nil, nil, false, 50, "")
	if err != nil {
		t.Fatalf("list txs: %v", err)
	}
	out := map[typev1.AccountType]string{}
	for _, pt := range txs {
		if pt.GetTx().GetSyntheticPurpose() != "INITIALIZE" {
			continue
		}
		at := pt.GetTx().GetAccountType()
		if _, dup := out[at]; dup {
			t.Fatalf("more than one INITIALIZE posting of account type %v", at)
		}
		out[at] = pt.GetTx().GetQuantity()
	}
	return out
}

func TestUpsertInitializeTx_GroupsThePosting(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	userID, _ := p.GetOrCreateUser(ctx, "sub|init-grp", "U", "u@init.com")
	instID, _ := p.EnsureInstrument(ctx, "", "", "", "", "", "", []db.IdentifierInput{{Type: "BROKER_DESCRIPTION", Domain: "IBKR", Value: "IG1", Canonical: false}}, "", nil, nil, nil)

	at := time.Date(2025, 2, 1, 0, 0, 0, 0, time.UTC)
	if err := p.UpsertInitializeTx(ctx, userID, "IBKR", "acct1", instID, initTx(at, 50)); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	// A derived posting has no ingestion job, so its group carries a NULL job_id.
	var groupID string
	var jobID *string
	var groupTs time.Time
	err := p.q.QueryRowContext(ctx, `
		SELECT DISTINCT g.id, g.job_id, g.timestamp FROM txs t JOIN tx_groups g ON g.id = t.group_id
		WHERE t.user_id = $1 AND t.synthetic_purpose = 'INITIALIZE'
	`, userID).Scan(&groupID, &jobID, &groupTs)
	if err != nil {
		t.Fatalf("read group: %v", err)
	}
	if jobID != nil {
		t.Errorf("job_id: want NULL, got %v", *jobID)
	}
	if !groupTs.Equal(at) {
		t.Errorf("group timestamp: want %v, got %v", at, groupTs)
	}

	// Recalculating a declaration must move the existing group rather than
	// replacing it, or every recalc would orphan one.
	moved := at.Add(48 * time.Hour)
	if err := p.UpsertInitializeTx(ctx, userID, "IBKR", "acct1", instID, initTx(moved, 75)); err != nil {
		t.Fatalf("re-upsert: %v", err)
	}
	var groupID2 string
	var groupTs2 time.Time
	err = p.q.QueryRowContext(ctx, `
		SELECT DISTINCT g.id, g.timestamp FROM txs t JOIN tx_groups g ON g.id = t.group_id
		WHERE t.user_id = $1 AND t.synthetic_purpose = 'INITIALIZE'
	`, userID).Scan(&groupID2, &groupTs2)
	if err != nil {
		t.Fatalf("read group after update: %v", err)
	}
	if groupID2 != groupID {
		t.Errorf("group id changed on update: %s -> %s", groupID, groupID2)
	}
	if !groupTs2.Equal(moved) {
		t.Errorf("group timestamp after update: want %v, got %v", moved, groupTs2)
	}
	if got := countGroups(t, p, userID); got != 1 {
		t.Errorf("tx_groups after re-upsert: want 1, got %d", got)
	}

	if err := p.DeleteInitializeTx(ctx, userID, "IBKR", "acct1", instID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if got := countGroups(t, p, userID); got != 0 {
		t.Errorf("tx_groups after delete: want 0, got %d", got)
	}
}

// TestUpsertInitializeTx_WritesTheEquityCounterparty verifies the pad is written
// with the leg that balances it. A pad has no counterparty in the source data, so
// the EQUITY posting is what lets the group sum to zero. It has to be equal and
// opposite, in the same account, instrument, group and share count basis --
// otherwise a stock split adjusts the two legs differently and the pair drifts.
func TestUpsertInitializeTx_WritesTheEquityCounterparty(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	userID, _ := p.GetOrCreateUser(ctx, "sub|init-equity", "U", "u@eq.com")
	instID, _ := p.EnsureInstrument(ctx, "", "", "", "", "", "", []db.IdentifierInput{{Type: "BROKER_DESCRIPTION", Domain: "IBKR", Value: "EQ1", Canonical: false}}, "", nil, nil, nil)

	at := time.Date(2025, 2, 1, 0, 0, 0, 0, time.UTC)
	if err := p.UpsertInitializeTx(ctx, userID, "IBKR", "acct1", instID, initTx(at, 50)); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	var legs, groups, bases, sum float64
	if err := p.q.QueryRowContext(ctx, `
		SELECT count(*), count(DISTINCT group_id), count(DISTINCT share_count_basis), sum(quantity)
		FROM txs
		WHERE user_id = $1 AND synthetic_purpose = 'INITIALIZE'
		  AND broker = 'IBKR' AND account = 'acct1' AND instrument_id = $2
	`, userID, instID).Scan(&legs, &groups, &bases, &sum); err != nil {
		t.Fatalf("read legs: %v", err)
	}
	if legs != 2 {
		t.Errorf("INITIALIZE postings: want 2, got %v", legs)
	}
	if groups != 1 {
		t.Errorf("the pad and its counterparty must share one group, got %v groups", groups)
	}
	if bases != 1 {
		t.Errorf("the two legs must share a share_count_basis, got %v distinct", bases)
	}
	if sum != 0 {
		t.Errorf("the pad's group must sum to zero, got %v", sum)
	}

	// The counterparty is excluded from holdings, so the declared opening balance
	// still reads as declared rather than netting to nothing.
	holdings, _, err := p.ComputeHoldings(ctx, userID, nil, "", nil)
	if err != nil {
		t.Fatalf("holdings: %v", err)
	}
	if len(holdings) != 1 || holdings[0].SplitAdjustedQuantity != "50" {
		t.Fatalf("holdings: want a single holding of 50, got %+v", holdings)
	}
}

// TestComputeRunningBalance_excludesNonUser verifies the balance an INITIALIZE pad is
// derived from counts only the user's own postings. A counterparty leg shares the
// broker account and instrument, so summing it would halve the pad and restate the
// declared opening balance.
func TestComputeRunningBalance_excludesNonUser(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	userID, _ := p.GetOrCreateUser(ctx, "sub|decl-acct-type", "U", "u@rb.com")
	instID, _ := p.EnsureInstrument(ctx, "", "", "", "", "", "", []db.IdentifierInput{{Type: "BROKER_DESCRIPTION", Domain: "IBKR", Value: "RB2", Canonical: false}}, "", nil, nil, nil)

	ts := time.Date(2025, 3, 1, 10, 0, 0, 0, time.UTC)
	if err := createTx(ctx, p, userID, "IBKR", "acct1", "", &apiv1.Tx{Timestamp: timestamppb.New(ts), InstrumentDescription: "RB2", BrokerTxType: []typev1.TxType{typev1.TxType_TRADE_ASSET}, ResolvedTxType: typev1.TxType_TRADE_ASSET, Quantity: "100", Account: "acct1"}, instID, nil); err != nil {
		t.Fatalf("create buy: %v", err)
	}
	if err := createTx(ctx, p, userID, "IBKR", "acct1", "", &apiv1.Tx{Timestamp: timestamppb.New(ts), InstrumentDescription: "RB2", BrokerTxType: []typev1.TxType{typev1.TxType_TRADE_ASSET}, ResolvedTxType: typev1.TxType_TRADE_ASSET, Quantity: "-100", Account: "acct1", AccountType: typev1.AccountType_ACCOUNT_TYPE_EQUITY}, instID, nil); err != nil {
		t.Fatalf("create equity leg: %v", err)
	}

	from := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2025, 12, 31, 23, 59, 59, 0, time.UTC)
	bal, err := p.ComputeRunningBalance(ctx, userID, "IBKR", "acct1", instID, from, to, from)
	if err != nil {
		t.Fatalf("compute: %v", err)
	}
	if bal.String() != "100" {
		t.Fatalf("running balance = %v, want 100: the EQUITY leg must not be summed", bal)
	}
}

// TestCreateHoldingDeclaration_DefaultsShareCountBasis pins the as-traded default:
// a declaration that says nothing about denomination is in the share count current
// on the date it refers to.
func TestCreateHoldingDeclaration_DefaultsShareCountBasis(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	userID, _ := p.GetOrCreateUser(ctx, "sub|decl-basis-default", "U", "u@b.com")
	instID, _ := p.EnsureInstrument(ctx, "", "", "", "", "", "", []db.IdentifierInput{{Type: "BROKER_DESCRIPTION", Domain: "IBKR", Value: "BD1", Canonical: false}}, "", nil, nil, nil)

	asOf := time.Date(2021, 1, 1, 0, 0, 0, 0, time.UTC)
	row, err := p.CreateHoldingDeclaration(ctx, userID, "IBKR", "acct1", instID, "500", asOf, time.Time{})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if !row.ShareCountBasis.Equal(asOf) {
		t.Fatalf("share_count_basis: want %v (as_of_date), got %v", asOf, row.ShareCountBasis)
	}

	// A stated basis survives, and moving as_of_date afterwards does not restate it:
	// the denomination is what the user said, not a function of the date.
	stated := time.Date(2025, 8, 5, 0, 0, 0, 0, time.UTC)
	row, err = p.CreateHoldingDeclaration(ctx, userID, "IBKR", "acct2", instID, "500", asOf, stated)
	if err != nil {
		t.Fatalf("create with basis: %v", err)
	}
	if !row.ShareCountBasis.Equal(stated) {
		t.Fatalf("stated share_count_basis: want %v, got %v", stated, row.ShareCountBasis)
	}
	moved := time.Date(2022, 1, 1, 0, 0, 0, 0, time.UTC)
	row, err = p.UpdateHoldingDeclaration(ctx, row.ID, "500", moved, time.Time{})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if !row.ShareCountBasis.Equal(stated) {
		t.Fatalf("share_count_basis after moving as_of_date: want %v, got %v", stated, row.ShareCountBasis)
	}
}

// TestComputeRunningBalance_ConvertsToDeclarationBasis is the reason the balance is
// not a plain SUM(quantity). The two postings below are recorded either side of a
// 2:1 split and are in different units, so adding them raw gives 150 -- a number in
// no share count at all. Converted, the same history is 100 pre-split shares or 200
// post-split ones, and which of those a declaration is compared against is the
// declaration's to state.
func TestComputeRunningBalance_ConvertsToDeclarationBasis(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	userID, _ := p.GetOrCreateUser(ctx, "sub|decl-basis-conv", "U", "u@b.com")
	instID, _ := p.EnsureInstrument(ctx, "", "", "", "", "", "", []db.IdentifierInput{{Type: "BROKER_DESCRIPTION", Domain: "IBKR", Value: "BC1", Canonical: false}}, "", nil, nil, nil)

	preSplit := time.Date(2021, 3, 1, 10, 0, 0, 0, time.UTC)
	exDate := time.Date(2022, 6, 1, 0, 0, 0, 0, time.UTC)
	postSplit := time.Date(2023, 3, 1, 10, 0, 0, 0, time.UTC)
	addSplit(t, p, instID, exDate, 1, 2)

	// 50 shares bought before the split, and 50 more after -- 100 post-split shares
	// for the first buy, plus 50, is 150 in post-split terms and 75 in pre-split.
	if err := createTx(ctx, p, userID, "IBKR", "acct1", "", &apiv1.Tx{Timestamp: timestamppb.New(preSplit), InstrumentDescription: "BC1", BrokerTxType: []typev1.TxType{typev1.TxType_TRADE_ASSET}, ResolvedTxType: typev1.TxType_TRADE_ASSET, Quantity: "50", Account: "acct1"}, instID, nil); err != nil {
		t.Fatalf("create pre-split buy: %v", err)
	}
	if err := createTx(ctx, p, userID, "IBKR", "acct1", "", &apiv1.Tx{Timestamp: timestamppb.New(postSplit), InstrumentDescription: "BC1", BrokerTxType: []typev1.TxType{typev1.TxType_TRADE_ASSET}, ResolvedTxType: typev1.TxType_TRADE_ASSET, Quantity: "50", Account: "acct1"}, instID, nil); err != nil {
		t.Fatalf("create post-split buy: %v", err)
	}

	from := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name  string
		basis time.Time
		want  string
	}{
		{"pre-split basis", preSplit, "75"},
		{"post-split basis", postSplit, "150"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bal, err := p.ComputeRunningBalance(ctx, userID, "IBKR", "acct1", instID, from, to, tc.basis)
			if err != nil {
				t.Fatalf("compute: %v", err)
			}
			if bal.String() != tc.want {
				t.Fatalf("running balance = %v, want %s", bal, tc.want)
			}
		})
	}
}

// TestListHoldingDeclarations_ChecksAgainstTheHolding is the point of the whole
// pad/assert split: an assertion is measured against what the transactions add up
// to, so a missing one shows up as a difference. The pad is included in that sum --
// an assertion checks the holding as the user sees it -- which is also why the pad's
// own check is always level.
func TestListHoldingDeclarations_ChecksAgainstTheHolding(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	userID, _ := p.GetOrCreateUser(ctx, "sub|decl-verify", "U", "u@v.com")
	instID, _ := p.EnsureInstrument(ctx, "", "", "", "", "", "", []db.IdentifierInput{{Type: "BROKER_DESCRIPTION", Domain: "IBKR", Value: "VF1", Canonical: false}}, "", nil, nil, nil)

	padDate := time.Date(2021, 1, 1, 0, 0, 0, 0, time.UTC)
	buyAt := time.Date(2022, 6, 1, 10, 0, 0, 0, time.UTC)
	assertDate := time.Date(2023, 12, 31, 0, 0, 0, 0, time.UTC)

	// A pad of 500 at the start, one real buy of 150, and an assertion of 650.
	if err := p.UpsertInitializeTx(ctx, userID, "IBKR", "acct1", instID, db.InitializeTx{
		Timestamp: padDate, Quantity: decf(500), ShareCountBasis: padDate,
	}); err != nil {
		t.Fatalf("upsert pad: %v", err)
	}
	if err := createTx(ctx, p, userID, "IBKR", "acct1", "", &apiv1.Tx{Timestamp: timestamppb.New(buyAt), InstrumentDescription: "VF1", BrokerTxType: []typev1.TxType{typev1.TxType_TRADE_ASSET}, ResolvedTxType: typev1.TxType_TRADE_ASSET, Quantity: "150", Account: "acct1"}, instID, nil); err != nil {
		t.Fatalf("create buy: %v", err)
	}
	if _, err := p.CreateHoldingDeclaration(ctx, userID, "IBKR", "acct1", instID, "500", padDate, time.Time{}); err != nil {
		t.Fatalf("create pad declaration: %v", err)
	}
	if _, err := p.CreateHoldingDeclaration(ctx, userID, "IBKR", "acct1", instID, "650", assertDate, time.Time{}); err != nil {
		t.Fatalf("create assertion: %v", err)
	}

	checked := func() map[string]string {
		t.Helper()
		rows, err := p.ListHoldingDeclarations(ctx, userID)
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		out := map[string]string{}
		for _, r := range rows {
			if r.Verified == nil {
				t.Fatalf("declaration %s came back unchecked", r.AsOfDate.Format("2006-01-02"))
			}
			if r.Verified.InexactBases != 0 {
				t.Errorf("no split falls in this window, so the check should be exact; got %d inexact bases", r.Verified.InexactBases)
			}
			out[r.AsOfDate.Format("2006-01-02")] = r.Verified.ComputedQty.String()
		}
		return out
	}

	if got, want := checked(), map[string]string{"2021-01-01": "500", "2023-12-31": "650"}; !maps.Equal(got, want) {
		t.Fatalf("computed quantities: got %v, want %v", got, want)
	}

	// Lose the buy, the way a converter dropping a row would. The assertion is the
	// only thing in the system that notices.
	if _, err := p.q.ExecContext(ctx, `
		DELETE FROM tx_groups g WHERE g.user_id = $1 AND EXISTS (
			SELECT 1 FROM txs t WHERE t.group_id = g.id AND t.synthetic_purpose IS NULL)
	`, userID); err != nil {
		t.Fatalf("delete buy: %v", err)
	}
	if got, want := checked(), map[string]string{"2021-01-01": "500", "2023-12-31": "500"}; !maps.Equal(got, want) {
		t.Fatalf("computed quantities after losing the buy: got %v, want %v", got, want)
	}
}

// TestListHoldingDeclarations_ChecksAcrossASplit denominates both sides of the
// comparison. The postings are recorded either side of a 2:1 split, so an assertion
// stated in pre-split shares and one stated in post-split shares describe the same
// holding with different numbers, and both have to reconcile.
func TestListHoldingDeclarations_ChecksAcrossASplit(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	userID, _ := p.GetOrCreateUser(ctx, "sub|decl-verify-split", "U", "u@v.com")
	instID, _ := p.EnsureInstrument(ctx, "", "", "", "", "", "", []db.IdentifierInput{{Type: "BROKER_DESCRIPTION", Domain: "IBKR", Value: "VS1", Canonical: false}}, "", nil, nil, nil)

	preSplit := time.Date(2021, 3, 1, 10, 0, 0, 0, time.UTC)
	exDate := time.Date(2022, 6, 1, 0, 0, 0, 0, time.UTC)
	postSplit := time.Date(2023, 3, 1, 10, 0, 0, 0, time.UTC)
	addSplit(t, p, instID, exDate, 1, 2)

	for _, at := range []time.Time{preSplit, postSplit} {
		if err := createTx(ctx, p, userID, "IBKR", "acct1", "", &apiv1.Tx{Timestamp: timestamppb.New(at), InstrumentDescription: "VS1", BrokerTxType: []typev1.TxType{typev1.TxType_TRADE_ASSET}, ResolvedTxType: typev1.TxType_TRADE_ASSET, Quantity: "50", Account: "acct1"}, instID, nil); err != nil {
			t.Fatalf("create buy at %s: %v", at.Format("2006-01-02"), err)
		}
	}

	// 50 shares bought pre-split are 100 post-split, plus 50 more: 150 in post-split
	// terms, 75 in pre-split terms. The two as_of dates differ only because a holding
	// takes one declaration per date; both are after the last buy, so the two
	// declarations see the same postings.
	for _, tc := range []struct {
		name  string
		asOf  time.Time
		basis time.Time
		want  string
	}{
		{"pre-split basis", time.Date(2023, 12, 30, 0, 0, 0, 0, time.UTC), preSplit, "75"},
		{"post-split basis", time.Date(2023, 12, 31, 0, 0, 0, 0, time.UTC), postSplit, "150"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			row, err := p.CreateHoldingDeclaration(ctx, userID, "IBKR", "acct1", instID, tc.want, tc.asOf, tc.basis)
			if err != nil {
				t.Fatalf("create declaration: %v", err)
			}
			got, err := p.GetHoldingDeclaration(ctx, row.ID)
			if err != nil {
				t.Fatalf("get: %v", err)
			}
			if got.Verified == nil {
				t.Fatal("declaration came back unchecked")
			}
			if got.Verified.ComputedQty.String() != tc.want {
				t.Fatalf("computed qty = %s, want %s", got.Verified.ComputedQty, tc.want)
			}
		})
	}
}

// TestDeleteDeclarationWithInitializeTx_KeepsThePadForSurvivors covers what deleting
// one of a holding's declarations does to the pad. Deleting one that leaves others
// behind rewrites the pad from whichever now pads the holding; deleting the last one
// takes the pad with it, since there is nothing left to pad to.
func TestDeleteDeclarationWithInitializeTx_KeepsThePadForSurvivors(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	userID, _ := p.GetOrCreateUser(ctx, "sub|decl-del-pad", "U", "u@d.com")
	instID, _ := p.EnsureInstrument(ctx, "", "", "", "", "", "", []db.IdentifierInput{{Type: "BROKER_DESCRIPTION", Domain: "IBKR", Value: "DP1", Canonical: false}}, "", nil, nil, nil)

	pad := time.Date(2021, 1, 1, 0, 0, 0, 0, time.UTC)
	next := time.Date(2023, 12, 31, 0, 0, 0, 0, time.UTC)
	padRow, err := p.CreateDeclarationWithInitializeTx(ctx, userID, "IBKR", "acct1", instID, "500", pad, time.Time{}, initTx(pad, 500))
	if err != nil {
		t.Fatalf("create pad declaration: %v", err)
	}
	nextRow, err := p.CreateHoldingDeclaration(ctx, userID, "IBKR", "acct1", instID, "650", next, time.Time{})
	if err != nil {
		t.Fatalf("create assertion: %v", err)
	}

	// Delete the pad's declaration, promoting the assertion behind it.
	promoted := initTx(next, 650)
	if err := p.DeleteDeclarationWithInitializeTx(ctx, padRow.ID, userID, "IBKR", "acct1", instID, &promoted); err != nil {
		t.Fatalf("delete pad declaration: %v", err)
	}
	if got := initQtyByAccountType(t, p, userID); !maps.Equal(got, map[typev1.AccountType]string{
		typev1.AccountType_ACCOUNT_TYPE_USER:   "650",
		typev1.AccountType_ACCOUNT_TYPE_EQUITY: "-650",
	}) {
		t.Fatalf("INITIALIZE legs after promoting: got %v", got)
	}

	// Delete the last declaration and the pad goes with it.
	if err := p.DeleteDeclarationWithInitializeTx(ctx, nextRow.ID, userID, "IBKR", "acct1", instID, nil); err != nil {
		t.Fatalf("delete last declaration: %v", err)
	}
	if got := initQtyByAccountType(t, p, userID); len(got) != 0 {
		t.Fatalf("INITIALIZE legs after deleting the last declaration: want none, got %v", got)
	}
	if got := countOrphanGroups(t, p, userID); got != 0 {
		t.Errorf("orphan tx_groups after delete: want 0, got %d", got)
	}
}

// TestUpsertInitializeTx_DenominatesThePad verifies the pad is stored in its
// declaration's share count rather than in the one current on the portfolio start
// date. The two are unrelated, and letting the txs trigger infer the basis from the
// timestamp made a pad declared in today's terms read as pre-split shares.
func TestUpsertInitializeTx_DenominatesThePad(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	userID, _ := p.GetOrCreateUser(ctx, "sub|init-basis", "U", "u@b.com")
	instID, _ := p.EnsureInstrument(ctx, "", "", "", "", "", "", []db.IdentifierInput{{Type: "BROKER_DESCRIPTION", Domain: "IBKR", Value: "IB1", Canonical: false}}, "", nil, nil, nil)

	startDay := time.Date(2021, 1, 1, 0, 0, 0, 0, time.UTC)
	basis := time.Date(2025, 8, 5, 0, 0, 0, 0, time.UTC)
	if err := p.UpsertInitializeTx(ctx, userID, "IBKR", "acct1", instID, db.InitializeTx{
		Timestamp: startDay, Quantity: decf(50), ShareCountBasis: basis,
	}); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	assertPadBasis := func(want time.Time) {
		t.Helper()
		var legs int
		if err := p.q.QueryRowContext(ctx, `
			SELECT count(*) FROM txs
			WHERE user_id = $1 AND synthetic_purpose = 'INITIALIZE'
			  AND share_count_basis = $2
		`, userID, want).Scan(&legs); err != nil {
			t.Fatalf("read basis: %v", err)
		}
		if legs != 2 {
			t.Fatalf("postings denominated at %v: want both legs, got %d", want, legs)
		}
	}
	assertPadBasis(basis)

	// Moving the portfolio start date moves the pad's timestamp. Its denomination is
	// the declaration's and must not follow.
	moved := startDay.AddDate(0, 0, -30)
	if err := p.UpsertInitializeTx(ctx, userID, "IBKR", "acct1", instID, db.InitializeTx{
		Timestamp: moved, Quantity: decf(75), ShareCountBasis: basis,
	}); err != nil {
		t.Fatalf("re-upsert: %v", err)
	}
	assertPadBasis(basis)
}
