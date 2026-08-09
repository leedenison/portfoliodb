package postgres

import (
	"context"
	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	typev1 "github.com/leedenison/portfoliodb/proto/type/v1"
	"github.com/leedenison/portfoliodb/server/db"
	"github.com/shopspring/decimal"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
	"strconv"
	"testing"
	"time"
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
		{Timestamp: ts1, InstrumentDescription: "AAPL", Type: typev1.TxType_BUYSTOCK, Quantity: "10", Account: ""},
		{Timestamp: ts2, InstrumentDescription: "AAPL", Type: typev1.TxType_SELLSTOCK, Quantity: "-3", Account: ""},
	}
	instID, err := p.EnsureInstrument(ctx, "", "", "", "", "", "", []db.IdentifierInput{{Type: "BROKER_DESCRIPTION", Domain: "IBKR", Value: "AAPL", Canonical: false}}, "", nil, nil, nil)
	if err != nil {
		t.Fatalf("ensure instrument: %v", err)
	}
	instrumentIDs := []string{instID, instID}
	err = p.ReplaceTxsInPeriod(ctx, userID, "IBKR", "", from, to, txs, instrumentIDs, nil, nil)
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
	var aaplQty string
	for _, h := range holdings {
		if h.InstrumentDescription == "AAPL" {
			aaplQty = h.SplitAdjustedQuantity
			break
		}
	}
	if aaplQty != "7" {
		t.Fatalf("expected AAPL quantity 7 (10 + -3), got %v", aaplQty)
	}
}

func TestReplaceTxsInPeriod_PeriodBeforeIsExclusive(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	userID, _ := p.GetOrCreateUser(ctx, "sub|replace-bound", "U", "u@bound.com")
	instID, err := p.EnsureInstrument(ctx, "", "", "", "", "", "", []db.IdentifierInput{{Type: "BROKER_DESCRIPTION", Domain: "IBKR", Value: "BND", Canonical: false}}, "", nil, nil, nil)
	if err != nil {
		t.Fatalf("ensure instrument: %v", err)
	}
	boundary := time.Date(2025, 4, 1, 0, 0, 0, 0, time.UTC)
	// Quantity doubles as an identity marker. One row inside the window, one
	// sitting exactly on the exclusive bound.
	for _, seed := range []struct {
		at  time.Time
		qty string
	}{
		{boundary.Add(-time.Hour), "1"},
		{boundary, "2"},
	} {
		tx := &apiv1.Tx{Timestamp: timestamppb.New(seed.at), InstrumentDescription: "BND", Type: typev1.TxType_BUYSTOCK, Quantity: seed.qty, Account: ""}
		if err := createTx(ctx, p, userID, "IBKR", "", "", tx, instID, nil); err != nil {
			t.Fatalf("create tx: %v", err)
		}
	}

	// Replacing [March 1, April 1) with nothing must delete only the earlier row:
	// a window abutting the next one must not reach into it.
	if err := p.ReplaceTxsInPeriod(ctx, userID, "IBKR", "",
		timestamppb.New(time.Date(2025, 3, 1, 0, 0, 0, 0, time.UTC)), timestamppb.New(boundary),
		nil, nil, nil, nil); err != nil {
		t.Fatalf("replace: %v", err)
	}

	rows, _, err := p.ListTxs(ctx, userID, nil, "", nil, nil, false, 50, "")
	if err != nil {
		t.Fatalf("list txs: %v", err)
	}
	if len(rows) != 1 || rows[0].GetTx().GetQuantity() != "2" {
		t.Fatalf("want only the tx on the bound to survive, got %d rows", len(rows))
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
	if err := p.UpsertInitializeTx(ctx, userID, "IBKR", "", instID, db.InitializeTx{
		TxType: "BUYOTHER", Timestamp: initTs, Quantity: decf(42), ShareCountBasis: initTs,
	}); err != nil {
		t.Fatalf("upsert initialize: %v", err)
	}
	oldTx := []*apiv1.Tx{
		{Timestamp: timestamppb.New(now.Add(-80 * time.Minute)), InstrumentDescription: "MSFT", Type: typev1.TxType_BUYSTOCK, Quantity: "5", Account: ""},
	}
	if err := p.ReplaceTxsInPeriod(ctx, userID, "IBKR", "", from, to, oldTx, []string{instID}, nil, nil); err != nil {
		t.Fatalf("seed real tx: %v", err)
	}

	// Replace real txs in the same period with a fresh set; synthetic must survive.
	newTxs := []*apiv1.Tx{
		{Timestamp: timestamppb.New(now.Add(-60 * time.Minute)), InstrumentDescription: "MSFT", Type: typev1.TxType_BUYSTOCK, Quantity: "7", Account: ""},
		{Timestamp: timestamppb.New(now.Add(-20 * time.Minute)), InstrumentDescription: "MSFT", Type: typev1.TxType_SELLSTOCK, Quantity: "-2", Account: ""},
	}
	if err := p.ReplaceTxsInPeriod(ctx, userID, "IBKR", "", from, to, newTxs, []string{instID, instID}, nil, nil); err != nil {
		t.Fatalf("replace: %v", err)
	}

	// Verify both new real txs and the synthetic INITIALIZE row are present.
	rows, _, err := p.ListTxs(ctx, userID, nil, "", from, to, false, 100, "")
	if err != nil {
		t.Fatalf("list txs: %v", err)
	}
	var sawInit, sawInitEquity, sawBuy, sawSell bool
	var sawOldBuy bool
	for _, r := range rows {
		switch {
		// The pad and its EQUITY counterparty are one group, so a replace has to
		// spare both or neither.
		case r.GetTx().GetSyntheticPurpose() == "INITIALIZE" &&
			r.GetTx().GetAccountType() == typev1.AccountType_ACCOUNT_TYPE_EQUITY:
			sawInitEquity = true
			if r.GetTx().GetQuantity() != "-42" {
				t.Errorf("synthetic counterparty qty: want -42, got %v", r.GetTx().GetQuantity())
			}
		case r.GetTx().GetSyntheticPurpose() == "INITIALIZE":
			sawInit = true
			if r.GetTx().GetQuantity() != "42" {
				t.Errorf("synthetic qty: want 42, got %v", r.GetTx().GetQuantity())
			}
		case r.GetTx().GetQuantity() == "7":
			sawBuy = true
		case r.GetTx().GetQuantity() == "-2":
			sawSell = true
		case r.GetTx().GetQuantity() == "5":
			sawOldBuy = true
		}
	}
	if !sawInit || !sawInitEquity {
		t.Errorf("synthetic INITIALIZE group was deleted by ReplaceTxsInPeriod: pad=%v counterparty=%v", sawInit, sawInitEquity)
	}
	if !sawBuy || !sawSell {
		t.Errorf("new real txs missing: buy=%v sell=%v", sawBuy, sawSell)
	}
	if sawOldBuy {
		t.Error("old real tx survived ReplaceTxsInPeriod")
	}
	// The synthetic posting's group must survive with it. Deleting by group is
	// what makes replace-by-period safe, so a group that outlives its posting or
	// a posting that outlives its group both break the invariant.
	if got := countGroups(t, p, userID); got != 3 {
		t.Errorf("tx_groups after replace: want 3 (2 new + 1 synthetic), got %d", got)
	}
	if got := countOrphanGroups(t, p, userID); got != 0 {
		t.Errorf("orphan tx_groups after replace: want 0, got %d", got)
	}
}

// countGroups returns the number of tx_groups rows for a user.
func countGroups(t *testing.T, p *Postgres, userID string) int {
	t.Helper()
	var n int
	if err := p.q.QueryRowContext(context.Background(),
		`SELECT count(*) FROM tx_groups WHERE user_id = $1`, userID).Scan(&n); err != nil {
		t.Fatalf("count groups: %v", err)
	}
	return n
}

// countOrphanGroups returns the number of a user's tx_groups with no postings.
func countOrphanGroups(t *testing.T, p *Postgres, userID string) int {
	t.Helper()
	var n int
	if err := p.q.QueryRowContext(context.Background(), `
		SELECT count(*) FROM tx_groups g
		WHERE g.user_id = $1 AND NOT EXISTS (SELECT 1 FROM txs t WHERE t.group_id = g.id)
	`, userID).Scan(&n); err != nil {
		t.Fatalf("count orphan groups: %v", err)
	}
	return n
}

func TestCreateTx_CreatesGroup(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	userID, _ := p.GetOrCreateUser(ctx, "sub|grp-create", "U", "u@grp.com")
	instID, err := p.EnsureInstrument(ctx, "", "", "", "", "", "", []db.IdentifierInput{{Type: "BROKER_DESCRIPTION", Domain: "IBKR", Value: "NVDA", Canonical: false}}, "", nil, nil, nil)
	if err != nil {
		t.Fatalf("ensure instrument: %v", err)
	}
	jobID, err := p.CreateJob(ctx, db.CreateJobParams{UserID: userID, JobType: "tx", Broker: "IBKR", Source: "IBKR:test:csv"})
	if err != nil {
		t.Fatalf("create job: %v", err)
	}
	at := time.Date(2025, 6, 2, 14, 30, 0, 0, time.UTC)
	tx := &apiv1.Tx{Timestamp: timestamppb.New(at), InstrumentDescription: "NVDA", Type: typev1.TxType_BUYSTOCK, Quantity: "4", Account: "A"}
	if err := createTx(ctx, p, userID, "IBKR", "A", jobID, tx, instID, nil); err != nil {
		t.Fatalf("create tx: %v", err)
	}

	var groupTs time.Time
	var groupJob string
	err = p.q.QueryRowContext(ctx, `
		SELECT g.timestamp, g.job_id FROM txs t JOIN tx_groups g ON g.id = t.group_id
		WHERE t.user_id = $1
	`, userID).Scan(&groupTs, &groupJob)
	if err != nil {
		t.Fatalf("read group: %v", err)
	}
	if !groupTs.Equal(at) {
		t.Errorf("group timestamp: want %v, got %v", at, groupTs)
	}
	if groupJob != jobID {
		t.Errorf("group job_id: want %q, got %q", jobID, groupJob)
	}
}

// TestCreateTxGroup_PutsEveryPostingInOneGroup verifies the append path stores a
// posting and the counterparty routed to balance it as one economic event. If it
// gave them a group each, an appended trade could never balance and the balance
// invariant would be out of reach on that path.
func TestCreateTxGroup_PutsEveryPostingInOneGroup(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	userID, _ := p.GetOrCreateUser(ctx, "sub|grp-append", "U", "u@append.com")
	instID, err := p.EnsureInstrument(ctx, "", "", "", "", "", "", []db.IdentifierInput{{Type: "BROKER_DESCRIPTION", Domain: "IBKR", Value: "NFLX", Canonical: false}}, "", nil, nil, nil)
	if err != nil {
		t.Fatalf("ensure instrument: %v", err)
	}
	at := time.Date(2025, 6, 2, 14, 30, 0, 0, time.UTC)
	// The two carry different refs on the way in; the append path is one group
	// regardless, so neither can split it.
	txs := []*apiv1.Tx{
		{Timestamp: timestamppb.New(at), InstrumentDescription: "NFLX", Type: typev1.TxType_BUYSTOCK, Quantity: "4", Account: "A", GroupRef: "a"},
		{Timestamp: timestamppb.New(at), InstrumentDescription: "NFLX", Type: typev1.TxType_BUYSTOCK, Quantity: "-4", Account: "A", GroupRef: "b", AccountType: typev1.AccountType_ACCOUNT_TYPE_IMBALANCE},
	}
	if err := p.CreateTxGroup(ctx, userID, "IBKR", "A", "", txs, []string{instID, instID}, nil, nil); err != nil {
		t.Fatalf("create tx group: %v", err)
	}
	if got := countGroups(t, p, userID); got != 1 {
		t.Errorf("tx_groups: want 1, got %d", got)
	}
	var postings, sum float64
	if err := p.q.QueryRowContext(ctx, `
		SELECT count(*), sum(quantity) FROM txs WHERE user_id = $1
	`, userID).Scan(&postings, &sum); err != nil {
		t.Fatalf("read postings: %v", err)
	}
	if postings != 2 || sum != 0 {
		t.Errorf("stored postings: want 2 summing to 0, got %v summing to %v", postings, sum)
	}
	// The caller's refs are not stored, so mutating them cannot leak out.
	if txs[0].GetGroupRef() != "a" || txs[1].GetGroupRef() != "b" {
		t.Errorf("caller's group refs were mutated: %q, %q", txs[0].GetGroupRef(), txs[1].GetGroupRef())
	}
}

// TestReplaceTxsInPeriod_DeletesRoutedPostingsWithTheirGroup verifies a routed
// counterparty is taken by the cascade like any other posting, so a re-upload
// cannot leave a stale residual behind to be double counted.
func TestReplaceTxsInPeriod_DeletesRoutedPostingsWithTheirGroup(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	userID, _ := p.GetOrCreateUser(ctx, "sub|routed-del", "U", "u@routed.com")
	instID, err := p.EnsureInstrument(ctx, "", "", "", "", "", "", []db.IdentifierInput{{Type: "BROKER_DESCRIPTION", Domain: "IBKR", Value: "IMB", Canonical: false}}, "", nil, nil, nil)
	if err != nil {
		t.Fatalf("ensure instrument: %v", err)
	}
	base := time.Date(2025, 7, 1, 0, 0, 0, 0, time.UTC)
	from, to := timestamppb.New(base), timestamppb.New(base.Add(24*time.Hour))
	txs := []*apiv1.Tx{
		{Timestamp: timestamppb.New(base.Add(time.Hour)), InstrumentDescription: "IMB", Type: typev1.TxType_BUYSTOCK, Quantity: "10", Account: "A", GroupRef: "t1"},
		{Timestamp: timestamppb.New(base.Add(time.Hour)), InstrumentDescription: "IMB", Type: typev1.TxType_BUYSTOCK, Quantity: "-10", Account: "A", GroupRef: "t1", AccountType: typev1.AccountType_ACCOUNT_TYPE_IMBALANCE},
	}
	if err := p.ReplaceTxsInPeriod(ctx, userID, "IBKR", "", from, to, txs, []string{instID, instID}, nil, nil); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := p.ReplaceTxsInPeriod(ctx, userID, "IBKR", "", from, to, nil, nil, nil, nil); err != nil {
		t.Fatalf("replace with nothing: %v", err)
	}
	var remaining int
	if err := p.q.QueryRowContext(ctx, `SELECT count(*) FROM txs WHERE user_id = $1`, userID).Scan(&remaining); err != nil {
		t.Fatalf("count txs: %v", err)
	}
	if remaining != 0 {
		t.Errorf("postings after replace: want 0, got %d", remaining)
	}
	if got := countGroups(t, p, userID); got != 0 {
		t.Errorf("tx_groups after replace: want 0, got %d", got)
	}
}

func TestReplaceTxsInPeriod_CreatesGroupPerTx(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	userID, _ := p.GetOrCreateUser(ctx, "sub|grp-per-tx", "U", "u@grp2.com")
	instID, err := p.EnsureInstrument(ctx, "", "", "", "", "", "", []db.IdentifierInput{{Type: "BROKER_DESCRIPTION", Domain: "IBKR", Value: "TSLA", Canonical: false}}, "", nil, nil, nil)
	if err != nil {
		t.Fatalf("ensure instrument: %v", err)
	}
	base := time.Date(2025, 5, 1, 0, 0, 0, 0, time.UTC)
	txs := []*apiv1.Tx{
		{Timestamp: timestamppb.New(base.Add(time.Hour)), InstrumentDescription: "TSLA", Type: typev1.TxType_BUYSTOCK, Quantity: "1", Account: ""},
		{Timestamp: timestamppb.New(base.Add(2 * time.Hour)), InstrumentDescription: "TSLA", Type: typev1.TxType_BUYSTOCK, Quantity: "2", Account: ""},
		{Timestamp: timestamppb.New(base.Add(3 * time.Hour)), InstrumentDescription: "TSLA", Type: typev1.TxType_SELLSTOCK, Quantity: "-1", Account: ""},
	}
	from, to := timestamppb.New(base), timestamppb.New(base.Add(24*time.Hour))
	if err := p.ReplaceTxsInPeriod(ctx, userID, "IBKR", "", from, to, txs, []string{instID, instID, instID}, nil, nil); err != nil {
		t.Fatalf("replace: %v", err)
	}

	var distinct, withGroup int
	if err := p.q.QueryRowContext(ctx,
		`SELECT count(DISTINCT group_id), count(group_id) FROM txs WHERE user_id = $1`, userID).
		Scan(&distinct, &withGroup); err != nil {
		t.Fatalf("count groups: %v", err)
	}
	if withGroup != 3 {
		t.Errorf("postings with a group: want 3, got %d", withGroup)
	}
	if distinct != 3 {
		t.Errorf("distinct groups: want 3 (one per posting), got %d", distinct)
	}
}

func TestReplaceTxsInPeriod_GroupsByGroupRef(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	userID, _ := p.GetOrCreateUser(ctx, "sub|grp-ref", "U", "u@ref.com")
	instID, err := p.EnsureInstrument(ctx, "", "", "", "", "", "", []db.IdentifierInput{{Type: "BROKER_DESCRIPTION", Domain: "IBKR", Value: "VOD", Canonical: false}}, "", nil, nil, nil)
	if err != nil {
		t.Fatalf("ensure instrument: %v", err)
	}
	base := time.Date(2025, 3, 1, 0, 0, 0, 0, time.UTC)
	// A trade and its cash leg share a ref; a separately-reported fee names none.
	txs := []*apiv1.Tx{
		{Timestamp: timestamppb.New(base.Add(time.Hour)), InstrumentDescription: "VOD", Type: typev1.TxType_SELLSTOCK, Quantity: "-100", Account: "", GroupRef: "ref-1"},
		{Timestamp: timestamppb.New(base.Add(2 * time.Hour)), InstrumentDescription: "VOD", Type: typev1.TxType_CASHFLOW, Quantity: "125", Account: "", GroupRef: "ref-1"},
		{Timestamp: timestamppb.New(base.Add(3 * time.Hour)), InstrumentDescription: "VOD", Type: typev1.TxType_INVEXPENSE, Quantity: "-7.5", Account: ""},
	}
	from, to := timestamppb.New(base), timestamppb.New(base.Add(24*time.Hour))
	ids := []string{instID, instID, instID}
	if err := p.ReplaceTxsInPeriod(ctx, userID, "IBKR", "", from, to, txs, ids, nil, nil); err != nil {
		t.Fatalf("replace: %v", err)
	}

	if got := countGroups(t, p, userID); got != 2 {
		t.Fatalf("tx_groups: want 2 (one shared, one ungrouped fee), got %d", got)
	}
	// The group takes the timestamp of the first leg that named it, not the last.
	var legs int
	var groupTs time.Time
	err = p.q.QueryRowContext(ctx, `
		SELECT count(*), max(g.timestamp) FROM txs t JOIN tx_groups g ON g.id = t.group_id
		WHERE t.user_id = $1
		  AND t.group_id = (SELECT group_id FROM txs WHERE user_id = $1 AND quantity = -100)
		GROUP BY t.group_id
	`, userID).Scan(&legs, &groupTs)
	if err != nil {
		t.Fatalf("read shared group: %v", err)
	}
	if legs != 2 {
		t.Errorf("postings in the shared group: want 2, got %d", legs)
	}
	if want := base.Add(time.Hour); !groupTs.Equal(want) {
		t.Errorf("group timestamp: want the first leg's %v, got %v", want, groupTs)
	}
}

func TestReplaceTxsInPeriod_GroupRefScopedToUpload(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	userID, _ := p.GetOrCreateUser(ctx, "sub|grp-scope", "U", "u@scope.com")
	instID, err := p.EnsureInstrument(ctx, "", "", "", "", "", "", []db.IdentifierInput{{Type: "BROKER_DESCRIPTION", Domain: "IBKR", Value: "BP", Canonical: false}}, "", nil, nil, nil)
	if err != nil {
		t.Fatalf("ensure instrument: %v", err)
	}
	base := time.Date(2025, 4, 1, 0, 0, 0, 0, time.UTC)
	upload := func(period time.Time) {
		t.Helper()
		txs := []*apiv1.Tx{
			{Timestamp: timestamppb.New(period.Add(time.Hour)), InstrumentDescription: "BP", Type: typev1.TxType_BUYSTOCK, Quantity: "10", Account: "", GroupRef: "same-ref"},
			{Timestamp: timestamppb.New(period.Add(time.Hour)), InstrumentDescription: "BP", Type: typev1.TxType_CASHFLOW, Quantity: "-50", Account: "", GroupRef: "same-ref"},
		}
		from, to := timestamppb.New(period), timestamppb.New(period.Add(24*time.Hour))
		if err := p.ReplaceTxsInPeriod(ctx, userID, "IBKR", "", from, to, txs, []string{instID, instID}, nil, nil); err != nil {
			t.Fatalf("replace: %v", err)
		}
	}
	// The same ref in a second upload must not join the first upload's group: refs
	// are scoped to one request and carry no meaning across them.
	upload(base)
	upload(base.Add(48 * time.Hour))

	if got := countGroups(t, p, userID); got != 2 {
		t.Errorf("tx_groups across two uploads: want 2, got %d", got)
	}
	var distinct int
	if err := p.q.QueryRowContext(ctx,
		`SELECT count(DISTINCT group_id) FROM txs WHERE user_id = $1`, userID).Scan(&distinct); err != nil {
		t.Fatalf("count distinct groups: %v", err)
	}
	if distinct != 2 {
		t.Errorf("distinct groups referenced by postings: want 2, got %d", distinct)
	}
}

func TestReplaceTxsInPeriod_DeletesWholeGroups(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	userID, _ := p.GetOrCreateUser(ctx, "sub|grp-whole", "U", "u@whole.com")
	instID, err := p.EnsureInstrument(ctx, "", "", "", "", "", "", []db.IdentifierInput{{Type: "BROKER_DESCRIPTION", Domain: "IBKR", Value: "GSK", Canonical: false}}, "", nil, nil, nil)
	if err != nil {
		t.Fatalf("ensure instrument: %v", err)
	}
	base := time.Date(2025, 8, 1, 0, 0, 0, 0, time.UTC)
	from, to := timestamppb.New(base), timestamppb.New(base.Add(24*time.Hour))
	seed := []*apiv1.Tx{
		{Timestamp: timestamppb.New(base.Add(time.Hour)), InstrumentDescription: "GSK", Type: typev1.TxType_BUYSTOCK, Quantity: "10", Account: "", GroupRef: "r"},
		{Timestamp: timestamppb.New(base.Add(time.Hour)), InstrumentDescription: "GSK", Type: typev1.TxType_CASHFLOW, Quantity: "-50", Account: "", GroupRef: "r"},
	}
	if err := p.ReplaceTxsInPeriod(ctx, userID, "IBKR", "", from, to, seed, []string{instID, instID}, nil, nil); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := p.ReplaceTxsInPeriod(ctx, userID, "IBKR", "", from, to, nil, nil, nil, nil); err != nil {
		t.Fatalf("replace with nothing: %v", err)
	}

	// Deleting by group must take every leg: a surviving orphan leg would be half
	// an economic event.
	rows, _, err := p.ListTxs(ctx, userID, nil, "", nil, nil, false, 50, "")
	if err != nil {
		t.Fatalf("list txs: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("postings after replacing the group with nothing: want 0, got %d", len(rows))
	}
	if got := countGroups(t, p, userID); got != 0 {
		t.Errorf("tx_groups after replace: want 0, got %d", got)
	}
}

func TestReplaceTxsInPeriod_DeletesGroupsInPeriod(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	userID, _ := p.GetOrCreateUser(ctx, "sub|grp-replace", "U", "u@grp3.com")
	instID, err := p.EnsureInstrument(ctx, "", "", "", "", "", "", []db.IdentifierInput{{Type: "BROKER_DESCRIPTION", Domain: "IBKR", Value: "AMD", Canonical: false}}, "", nil, nil, nil)
	if err != nil {
		t.Fatalf("ensure instrument: %v", err)
	}
	base := time.Date(2025, 7, 1, 0, 0, 0, 0, time.UTC)
	from, to := timestamppb.New(base), timestamppb.New(base.Add(24*time.Hour))
	seed := []*apiv1.Tx{
		{Timestamp: timestamppb.New(base.Add(time.Hour)), InstrumentDescription: "AMD", Type: typev1.TxType_BUYSTOCK, Quantity: "1", Account: ""},
		{Timestamp: timestamppb.New(base.Add(2 * time.Hour)), InstrumentDescription: "AMD", Type: typev1.TxType_BUYSTOCK, Quantity: "2", Account: ""},
	}
	if err := p.ReplaceTxsInPeriod(ctx, userID, "IBKR", "", from, to, seed, []string{instID, instID}, nil, nil); err != nil {
		t.Fatalf("seed: %v", err)
	}
	replacement := []*apiv1.Tx{
		{Timestamp: timestamppb.New(base.Add(3 * time.Hour)), InstrumentDescription: "AMD", Type: typev1.TxType_BUYSTOCK, Quantity: "9", Account: ""},
	}
	if err := p.ReplaceTxsInPeriod(ctx, userID, "IBKR", "", from, to, replacement, []string{instID}, nil, nil); err != nil {
		t.Fatalf("replace: %v", err)
	}

	// The replaced groups must go with their postings rather than accumulating.
	if got := countGroups(t, p, userID); got != 1 {
		t.Errorf("tx_groups after replace: want 1, got %d", got)
	}
	if got := countOrphanGroups(t, p, userID); got != 0 {
		t.Errorf("orphan tx_groups after replace: want 0, got %d", got)
	}
}

func TestCreateTx_AppendOnly(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	userID, _ := p.GetOrCreateUser(ctx, "sub|up", "U", "u@u.com")
	_, _ = p.CreatePortfolio(ctx, userID, "P")
	ts := timestamppb.Now()
	tx1 := &apiv1.Tx{Timestamp: ts, InstrumentDescription: "GOOG", Type: typev1.TxType_BUYSTOCK, Quantity: "5", Account: ""}
	tx2 := &apiv1.Tx{Timestamp: ts, InstrumentDescription: "GOOG", Type: typev1.TxType_BUYSTOCK, Quantity: "10", Account: ""}
	instID, err := p.EnsureInstrument(ctx, "", "", "", "", "", "", []db.IdentifierInput{{Type: "BROKER_DESCRIPTION", Domain: "IBKR", Value: "GOOG", Canonical: false}}, "", nil, nil, nil)
	if err != nil {
		t.Fatalf("ensure instrument: %v", err)
	}
	if err := createTx(ctx, p, userID, "IBKR", "", "", tx1, instID, nil); err != nil {
		t.Fatalf("first create: %v", err)
	}
	if err := createTx(ctx, p, userID, "IBKR", "", "", tx2, instID, nil); err != nil {
		t.Fatalf("second create: %v", err)
	}
	holdings, _, _ := p.ComputeHoldings(ctx, userID, nil, "", nil)
	for _, h := range holdings {
		if h.InstrumentDescription == "GOOG" && h.SplitAdjustedQuantity != "15" {
			t.Fatalf("append-only: expected total quantity 15, got %v", h.SplitAdjustedQuantity)
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
		broker typev1.Broker
		offset time.Duration
		qty    string
	}{
		{typev1.Broker_IBKR, -3 * time.Hour, "1"},
		{typev1.Broker_FIDELITY, -2 * time.Hour, "2"},
		{typev1.Broker_IBKR, -1 * time.Hour, "3"},
	}
	for _, s := range seed {
		// Seed through the same enum-to-string mapping the filter uses, rather than
		// a literal: the stored strings are not simply the enum names.
		brokerStr, err := brokerToStr(s.broker)
		if err != nil {
			t.Fatalf("broker to str: %v", err)
		}
		tx := &apiv1.Tx{Timestamp: timestamppb.New(now.Add(s.offset)), InstrumentDescription: "ORD", Type: typev1.TxType_BUYSTOCK, Quantity: s.qty}
		if err := createTx(ctx, p, userID, brokerStr, "", "", tx, instID, nil); err != nil {
			t.Fatalf("create tx: %v", err)
		}
	}

	qtys := func(rows []*apiv1.PortfolioTx) []string {
		out := make([]string, len(rows))
		for i, r := range rows {
			out[i] = r.GetTx().GetQuantity()
		}
		return out
	}
	fidelity := typev1.Broker_FIDELITY.Enum()

	cases := []struct {
		name       string
		broker     *typev1.Broker
		descending bool
		want       []string
	}{
		{"ascending, all brokers", nil, false, []string{"1", "2", "3"}},
		{"descending, all brokers", nil, true, []string{"3", "2", "1"}},
		{"broker filter", fidelity, false, []string{"2"}},
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
	rows, _, err := p.ListTxs(ctx, userID, typev1.Broker_IBKR.Enum(), "", nil, nil, true, 1, "")
	if err != nil {
		t.Fatalf("latest tx: %v", err)
	}
	if len(rows) != 1 || rows[0].GetTx().GetQuantity() != "3" {
		t.Fatalf("latest IBKR tx: want qty 3, got %v", qtys(rows))
	}
}

func TestListTxs_PeriodBeforeIsExclusive(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	userID, _ := p.GetOrCreateUser(ctx, "sub|period", "U", "u@period.com")
	instID, err := p.EnsureInstrument(ctx, "", "", "", "", "", "", []db.IdentifierInput{{Type: "BROKER_DESCRIPTION", Domain: "IBKR", Value: "PER", Canonical: false}}, "", nil, nil, nil)
	if err != nil {
		t.Fatalf("ensure instrument: %v", err)
	}
	brokerStr, err := brokerToStr(typev1.Broker_IBKR)
	if err != nil {
		t.Fatalf("broker to str: %v", err)
	}
	boundary := time.Date(2025, 3, 10, 0, 0, 0, 0, time.UTC)
	// Quantity doubles as an identity marker.
	for _, seed := range []struct {
		at  time.Time
		qty string
	}{
		{boundary.Add(-time.Second), "1"},
		{boundary, "2"},
	} {
		tx := &apiv1.Tx{Timestamp: timestamppb.New(seed.at), InstrumentDescription: "PER", Type: typev1.TxType_BUYSTOCK, Quantity: seed.qty}
		if err := createTx(ctx, p, userID, brokerStr, "", "", tx, instID, nil); err != nil {
			t.Fatalf("create tx: %v", err)
		}
	}

	// A tx exactly on periodBefore is out; the instant before it is in.
	rows, _, err := p.ListTxs(ctx, userID, nil, "", nil, timestamppb.New(boundary), false, 50, "")
	if err != nil {
		t.Fatalf("list txs: %v", err)
	}
	if len(rows) != 1 || rows[0].GetTx().GetQuantity() != "1" {
		t.Fatalf("want only the tx before the boundary, got %d rows", len(rows))
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
		tx := &apiv1.Tx{Timestamp: ts, InstrumentDescription: "TIE", Type: typev1.TxType_BUYSTOCK, Quantity: strconv.Itoa(i + 1)}
		if err := createTx(ctx, p, userID, "IBKR", "", "", tx, instID, nil); err != nil {
			t.Fatalf("create tx %d: %v", i, err)
		}
	}

	for _, descending := range []bool{false, true} {
		name := "ascending"
		if descending {
			name = "descending"
		}
		t.Run(name, func(t *testing.T) {
			seen := map[string]int{}
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
		{Timestamp: ts1, InstrumentDescription: "AAPL", Type: typev1.TxType_BUYSTOCK, Quantity: "10", Account: ""},
		{Timestamp: ts2, InstrumentDescription: "AAPL", Type: typev1.TxType_SELLSTOCK, Quantity: "-3", Account: ""},
	}
	instID, err := p.EnsureInstrument(ctx, "", "", "", "", "", "", []db.IdentifierInput{{Type: "BROKER_DESCRIPTION", Domain: "IBKR", Value: "AAPL", Canonical: false}}, "", nil, nil, nil)
	if err != nil {
		t.Fatalf("ensure instrument: %v", err)
	}
	if err := p.ReplaceTxsInPeriod(ctx, userID, "IBKR", "", from, to, txList, []string{instID, instID}, nil, nil); err != nil {
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
	var aaplQty string
	for _, h := range holdings {
		if h.InstrumentDescription == "AAPL" {
			aaplQty = h.SplitAdjustedQuantity
			break
		}
	}
	if aaplQty != "7" {
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
	if err := createTx(ctx, p, userID, "IBKR", "B", "", &apiv1.Tx{Timestamp: ts, InstrumentDescription: "X", Type: typev1.TxType_BUYSTOCK, Quantity: "1", Account: "B"}, instID, nil); err != nil {
		t.Fatalf("create tx1: %v", err)
	}
	// Tx2: SCHB, account "A" -> matches account but NOT broker -> excluded
	if err := createTx(ctx, p, userID, "SCHB", "A", "", &apiv1.Tx{Timestamp: ts, InstrumentDescription: "X", Type: typev1.TxType_BUYSTOCK, Quantity: "2", Account: "A"}, instID, nil); err != nil {
		t.Fatalf("create tx2: %v", err)
	}
	// Tx3: IBKR, account "A" -> matches both -> included
	if err := createTx(ctx, p, userID, "IBKR", "A", "", &apiv1.Tx{Timestamp: ts, InstrumentDescription: "X", Type: typev1.TxType_BUYSTOCK, Quantity: "3", Account: "A"}, instID, nil); err != nil {
		t.Fatalf("create tx3: %v", err)
	}
	txs, _, err := p.ListTxsByPortfolio(ctx, port.GetId(), nil, nil, nil, false, 50, "")
	if err != nil {
		t.Fatalf("ListTxsByPortfolio: %v", err)
	}
	if len(txs) != 1 {
		t.Fatalf("expected 1 tx (AND between categories), got %d: %+v", len(txs), txs)
	}
	if txs[0].GetTx().GetQuantity() != "3" {
		t.Fatalf("expected tx3 (qty=3), got qty=%v", txs[0].GetTx().GetQuantity())
	}
	holdings, _, err := p.ComputeHoldingsForPortfolio(ctx, port.GetId(), nil)
	if err != nil {
		t.Fatalf("ComputeHoldingsForPortfolio: %v", err)
	}
	var totalQty string
	for _, h := range holdings {
		totalQty += h.SplitAdjustedQuantity
	}
	if totalQty != "3" {
		t.Fatalf("expected total quantity 3, got %v", totalQty)
	}
}

// TestReplaceTxsInPeriod_RoundTripsAccountType verifies a posting's account_type
// survives the write and comes back on the read, and that an upload which says
// nothing about kind stores USER rather than an unspecified value.
func TestReplaceTxsInPeriod_RoundTripsAccountType(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	userID, _ := p.GetOrCreateUser(ctx, "sub|acct-type", "U", "u@acct.com")
	instID, err := p.EnsureInstrument(ctx, "", "", "", "", "", "",
		[]db.IdentifierInput{{Type: "BROKER_DESCRIPTION", Domain: "IBKR", Value: "USD", Canonical: false}}, "", nil, nil, nil)
	if err != nil {
		t.Fatalf("ensure instrument: %v", err)
	}
	base := time.Date(2025, 5, 1, 0, 0, 0, 0, time.UTC)
	// A dividend as a balanced group: cash into the account, and the income it
	// came from. Both legs keep the same broker and account.
	txs := []*apiv1.Tx{
		{Timestamp: timestamppb.New(base.Add(time.Hour)), InstrumentDescription: "USD", Type: typev1.TxType_INCOME, Quantity: "23.4", Account: "A", GroupRef: "div-1"},
		{Timestamp: timestamppb.New(base.Add(time.Hour)), InstrumentDescription: "USD", Type: typev1.TxType_INCOME, Quantity: "-23.4", Account: "A", GroupRef: "div-1", AccountType: typev1.AccountType_ACCOUNT_TYPE_INCOME},
	}
	from, to := timestamppb.New(base), timestamppb.New(base.Add(24*time.Hour))
	if err := p.ReplaceTxsInPeriod(ctx, userID, "IBKR", "", from, to, txs, []string{instID, instID}, nil, nil); err != nil {
		t.Fatalf("replace: %v", err)
	}

	var user, income int
	if err := p.q.QueryRowContext(ctx, `
		SELECT count(*) FILTER (WHERE account_type = 'USER'),
		       count(*) FILTER (WHERE account_type = 'INCOME')
		FROM txs WHERE user_id = $1
	`, userID).Scan(&user, &income); err != nil {
		t.Fatalf("count by account type: %v", err)
	}
	if user != 1 || income != 1 {
		t.Errorf("stored account types: want 1 USER and 1 INCOME, got %d and %d", user, income)
	}

	// The ledger view is not filtered: both legs come back, so a group can be seen
	// to balance.
	got, _, err := p.ListTxs(ctx, userID, nil, "", nil, nil, false, 50, "")
	if err != nil {
		t.Fatalf("ListTxs: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("ListTxs returned %d postings, want both legs", len(got))
	}
	seen := map[typev1.AccountType]bool{}
	for _, ptx := range got {
		seen[ptx.GetTx().GetAccountType()] = true
	}
	if !seen[typev1.AccountType_ACCOUNT_TYPE_USER] || !seen[typev1.AccountType_ACCOUNT_TYPE_INCOME] {
		t.Errorf("ListTxs account types: want USER and INCOME, got %v", seen)
	}
}

// TestReplaceTxsInPeriod_RoundTripsSourceReferences verifies the two evidence
// columns reach storage as the source wrote them, and that a source that wrote
// nothing stores NULL rather than an empty string. The distinction is the whole
// point of the columns: absent evidence has to be told from an empty reference,
// because only a derived posting is expected to carry none at all.
func TestReplaceTxsInPeriod_RoundTripsSourceReferences(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	userID, _ := p.GetOrCreateUser(ctx, "sub|src-ref", "U", "u@ref.com")
	instID, err := p.EnsureInstrument(ctx, "", "", "", "", "", "",
		[]db.IdentifierInput{{Type: "BROKER_DESCRIPTION", Domain: "Fidelity", Value: "GBP", Canonical: false}}, "", nil, nil, nil)
	if err != nil {
		t.Fatalf("ensure instrument: %v", err)
	}
	base := time.Date(2025, 4, 15, 0, 0, 0, 0, time.UTC)
	// The two sides of one transfer hop, as Fidelity reports them: adjacent
	// references, and only the receiving side naming where the money came from.
	txs := []*apiv1.Tx{
		{Timestamp: timestamppb.New(base), InstrumentDescription: "GBP", Type: typev1.TxType_TRANSFER,
			Quantity: "-20000", Account: "AG10000001", BrokerRef: "971613411"},
		{Timestamp: timestamppb.New(base), InstrumentDescription: "GBP", Type: typev1.TxType_TRANSFER,
			Quantity: "20000", Account: "AW10000001", BrokerRef: "971613414", CounterpartyAccount: "AG10000001"},
	}
	from, to := timestamppb.New(base), timestamppb.New(base.Add(24*time.Hour))
	if err := p.ReplaceTxsInPeriod(ctx, userID, "Fidelity", "", from, to, txs, []string{instID, instID}, nil, nil); err != nil {
		t.Fatalf("replace: %v", err)
	}

	rows, err := p.q.QueryContext(ctx, `
		SELECT account, broker_ref, counterparty_account FROM txs
		WHERE user_id = $1 ORDER BY account
	`, userID)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	defer rows.Close()
	type stored struct {
		ref          *string
		counterparty *string
	}
	got := map[string]stored{}
	for rows.Next() {
		var account string
		var s stored
		if err := rows.Scan(&account, &s.ref, &s.counterparty); err != nil {
			t.Fatalf("scan: %v", err)
		}
		got[account] = s
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("stored %d postings, want 2", len(got))
	}
	departure, arrival := got["AG10000001"], got["AW10000001"]
	if departure.ref == nil || *departure.ref != "971613411" {
		t.Errorf("departure broker_ref = %v, want 971613411", departure.ref)
	}
	// The source named no counterparty on the departure, so nothing is stored --
	// not an empty string.
	if departure.counterparty != nil {
		t.Errorf("departure counterparty_account = %q, want NULL", *departure.counterparty)
	}
	if arrival.ref == nil || *arrival.ref != "971613414" {
		t.Errorf("arrival broker_ref = %v, want 971613414", arrival.ref)
	}
	if arrival.counterparty == nil || *arrival.counterparty != "AG10000001" {
		t.Errorf("arrival counterparty_account = %v, want AG10000001", arrival.counterparty)
	}
}

// TestTxs_AccountTypeCheckConstraint verifies the vocabulary is enforced by the
// database, not only by the enum on the way in.
func TestTxs_AccountTypeCheckConstraint(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	userID, _ := p.GetOrCreateUser(ctx, "sub|acct-check", "U", "u@check.com")
	_, err := p.q.ExecContext(ctx, `
		INSERT INTO txs (user_id, broker, account, timestamp, instrument_description,
		                 tx_type, quantity, split_adjusted_quantity, share_count_basis,
		                 account_type, weight, weight_commodity, group_id)
		VALUES ($1, 'IBKR', 'A', now(), 'X', 'BUYSTOCK', 1, 1, current_date, 'Imbalance.USD', 1, 'desc:X', $2::uuid)
	`, userID, newTxGroup(t, p, userID))
	if err == nil {
		t.Fatal("insert with an account_type outside the vocabulary: want error, got none")
	}
}

// TestReplaceTxsInPeriod_RoundTripsZeroUnitPrice verifies a declared price of
// zero stays distinct from no price at all. Balancing converts a purchase or
// sale at its price, so an option expiring worthless converts at zero and its
// group balances, while a row whose converter supplied no price cannot convert.
// Collapsing the two would let a missing price balance silently.
func TestReplaceTxsInPeriod_RoundTripsZeroUnitPrice(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	userID, _ := p.GetOrCreateUser(ctx, "sub|zero-price", "U", "u@zero.com")
	instID, err := p.EnsureInstrument(ctx, "", "", "", "", "", "",
		[]db.IdentifierInput{{Type: "BROKER_DESCRIPTION", Domain: "IBKR", Value: "OPT", Canonical: false}}, "", nil, nil, nil)
	if err != nil {
		t.Fatalf("ensure instrument: %v", err)
	}
	base := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)
	txs := []*apiv1.Tx{
		{Timestamp: timestamppb.New(base.Add(time.Hour)), InstrumentDescription: "OPT", Type: typev1.TxType_CLOSUREOPT, Quantity: "-1", Account: "A", UnitPrice: proto.String("0")},
		{Timestamp: timestamppb.New(base.Add(2 * time.Hour)), InstrumentDescription: "OPT", Type: typev1.TxType_BUYOPT, Quantity: "1", Account: "A"},
	}
	from, to := timestamppb.New(base), timestamppb.New(base.Add(24*time.Hour))
	if err := p.ReplaceTxsInPeriod(ctx, userID, "IBKR", "", from, to, txs, []string{instID, instID}, nil, nil); err != nil {
		t.Fatalf("replace: %v", err)
	}

	var zeros, nulls int
	if err := p.q.QueryRowContext(ctx, `
		SELECT count(*) FILTER (WHERE unit_price = 0),
		       count(*) FILTER (WHERE unit_price IS NULL)
		FROM txs WHERE user_id = $1
	`, userID).Scan(&zeros, &nulls); err != nil {
		t.Fatalf("count by unit_price: %v", err)
	}
	if zeros != 1 || nulls != 1 {
		t.Errorf("stored unit_price: want one zero and one NULL, got %d and %d", zeros, nulls)
	}

	got, _, err := p.ListTxs(ctx, userID, nil, "", nil, nil, false, 50, "")
	if err != nil {
		t.Fatalf("ListTxs: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("ListTxs returned %d postings, want 2", len(got))
	}
	byType := map[typev1.TxType]*apiv1.Tx{}
	for _, ptx := range got {
		byType[ptx.GetTx().GetType()] = ptx.GetTx()
	}
	// A present "0" and an absent field are different things on the wire as well
	// as in the column: optional string keeps the explicit presence that optional
	// double had, so an option expiring worthless still converts at zero and its
	// group balances.
	if p := byType[typev1.TxType_CLOSUREOPT].UnitPrice; p == nil || *p != "0" {
		t.Errorf("expired option unit_price: want a present zero, got %v", p)
	}
	if p := byType[typev1.TxType_BUYOPT].UnitPrice; p != nil {
		t.Errorf("unpriced buy unit_price: want absent, got %v", *p)
	}
}

// TestReplaceTxsInPeriod_StoresWeight verifies the weight the caller supplies reaches
// the columns the balance constraint reads, rather than being recomputed or dropped.
// A converting leg and its cash counter-leg are both written, because a group balances
// only when the two agree on the commodity they weigh in.
func TestReplaceTxsInPeriod_StoresWeight(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	userID, _ := p.GetOrCreateUser(ctx, "sub|weight", "U", "u@w.com")
	instID, err := p.EnsureInstrument(ctx, "STOCK", "", "USD", "", "", "",
		[]db.IdentifierInput{{Type: "MIC_TICKER", Domain: "XNAS", Value: "AAPL", Canonical: true}}, "", nil, nil, nil)
	if err != nil {
		t.Fatalf("ensure instrument: %v", err)
	}
	price := "185.50"
	now := time.Now()
	from, to := timestamppb.New(now.Add(-time.Hour)), timestamppb.New(now.Add(time.Hour))
	txs := []*apiv1.Tx{
		{Timestamp: timestamppb.New(now), InstrumentDescription: "AAPL", Type: typev1.TxType_BUYSTOCK,
			Quantity: "10", UnitPrice: &price, SettlementCurrency: "USD", GroupRef: "g1"},
		{Timestamp: timestamppb.New(now), InstrumentDescription: "USD", Type: typev1.TxType_BUYSTOCK,
			Quantity: "-1855", SettlementCurrency: "USD", TradingCurrency: "USD", GroupRef: "g1"},
	}
	ws := []db.Weight{
		{Amount: decf(1855), Commodity: "cur:USD"},
		{Amount: decf(-1855), Commodity: "cur:USD"},
	}
	if err := p.ReplaceTxsInPeriod(ctx, userID, "IBKR", "", from, to, txs, []string{instID, instID}, ws, nil); err != nil {
		t.Fatalf("replace txs: %v", err)
	}

	var residual decimal.Decimal
	err = p.q.QueryRowContext(ctx, `
		SELECT SUM(weight) FROM txs WHERE user_id = $1 AND weight_commodity = 'cur:USD'
	`, userID).Scan(&residual)
	if err != nil {
		t.Fatalf("sum weights: %v", err)
	}
	if !residual.IsZero() {
		t.Errorf("group weights sum to %v in cur:USD, want exactly 0", residual)
	}
}

// TestReplaceTxsInPeriod_DefaultsWeightWhenAbsent verifies what a nil weights slice
// means: each posting weighs its own quantity in its own instrument, which is what the
// weight rule returns for a posting with no price. The fixtures that pass nil are then
// writing a defensible weight rather than a placeholder.
func TestReplaceTxsInPeriod_DefaultsWeightWhenAbsent(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	userID, _ := p.GetOrCreateUser(ctx, "sub|weight-default", "U", "u@wd.com")
	instID, err := p.EnsureInstrument(ctx, "STOCK", "", "USD", "", "", "",
		[]db.IdentifierInput{{Type: "MIC_TICKER", Domain: "XNAS", Value: "MSFT", Canonical: true}}, "", nil, nil, nil)
	if err != nil {
		t.Fatalf("ensure instrument: %v", err)
	}
	now := time.Now()
	from, to := timestamppb.New(now.Add(-time.Hour)), timestamppb.New(now.Add(time.Hour))
	txs := []*apiv1.Tx{
		{Timestamp: timestamppb.New(now), InstrumentDescription: "MSFT", Type: typev1.TxType_BUYSTOCK, Quantity: "7"},
	}
	if err := p.ReplaceTxsInPeriod(ctx, userID, "IBKR", "", from, to, txs, []string{instID}, nil, nil); err != nil {
		t.Fatalf("replace txs: %v", err)
	}

	var weight decimal.Decimal
	var commodity string
	err = p.q.QueryRowContext(ctx, `
		SELECT weight, weight_commodity FROM txs WHERE user_id = $1
	`, userID).Scan(&weight, &commodity)
	if err != nil {
		t.Fatalf("read weight: %v", err)
	}
	if weight.String() != "7" {
		t.Errorf("weight = %v, want 7 (the posting's own quantity)", weight)
	}
	if want := "inst:" + instID; commodity != want {
		t.Errorf("weight_commodity = %q, want %q", commodity, want)
	}
}

// The export names an instrument by the identifier bestIdentifierJoin picks, so
// that every export surfacing one identifier per instrument agrees which one. An
// instrument carrying both a ticker and the broker description it resolved under
// exports as the ticker.
func TestListTxsForExport_UsesTheBestIdentifier(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	userID, _ := p.GetOrCreateUser(ctx, "sub|exp-id", "U", "u@exp-id.com")
	instID, err := p.EnsureInstrument(ctx, "", "", "", "", "", "", []db.IdentifierInput{
		{Type: "BROKER_DESCRIPTION", Domain: "Fidelity:web:fidelity-csv", Value: "APPLE INC", Canonical: false},
		{Type: "MIC_TICKER", Domain: "XNAS", Value: "AAPL", Canonical: true},
	}, "", nil, nil, nil)
	if err != nil {
		t.Fatalf("ensure instrument: %v", err)
	}
	now := time.Now().Truncate(time.Second)
	tx := &apiv1.Tx{Timestamp: timestamppb.New(now), InstrumentDescription: "APPLE INC", Type: typev1.TxType_BUYSTOCK, Quantity: "10"}
	if err := createTx(ctx, p, userID, "FIDELITY", "acct", "", tx, instID, nil); err != nil {
		t.Fatalf("create tx: %v", err)
	}

	rows, err := p.ListTxsForExport(ctx, userID)
	if err != nil {
		t.Fatalf("list txs for export: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}
	if rows[0].IdentifierType != "MIC_TICKER" || rows[0].IdentifierValue != "AAPL" || rows[0].IdentifierDomain != "XNAS" {
		t.Fatalf("identifier = %s/%s/%s, want MIC_TICKER/AAPL/XNAS",
			rows[0].IdentifierType, rows[0].IdentifierValue, rows[0].IdentifierDomain)
	}
	if rows[0].Broker != "FIDELITY" || rows[0].Account != "acct" || rows[0].AccountType != "USER" {
		t.Fatalf("row = %+v", rows[0])
	}
	if !rows[0].Quantity.Equal(decimal.RequireFromString("10")) {
		t.Fatalf("quantity = %s", rows[0].Quantity)
	}
}

// A synthetic INITIALIZE pad and its EQUITY counterparty are both excluded. They
// are derived from a holding declaration, which the archive carries as the
// declaration it came from, and re-importing one as a real transaction would
// collide with the partial unique index that allows one per holding.
func TestListTxsForExport_ExcludesSyntheticGroups(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	userID, _ := p.GetOrCreateUser(ctx, "sub|exp-syn", "U", "u@exp-syn.com")
	instID, err := p.EnsureInstrument(ctx, "", "", "", "", "", "", []db.IdentifierInput{
		{Type: "BROKER_DESCRIPTION", Domain: "IBKR", Value: "SYN", Canonical: false},
	}, "", nil, nil, nil)
	if err != nil {
		t.Fatalf("ensure instrument: %v", err)
	}
	now := time.Now().Truncate(time.Second)
	if err := p.UpsertInitializeTx(ctx, userID, "IBKR", "acct1", instID, initTx(now, 50)); err != nil {
		t.Fatalf("upsert initialize tx: %v", err)
	}
	tx := &apiv1.Tx{Timestamp: timestamppb.New(now), InstrumentDescription: "SYN", Type: typev1.TxType_BUYSTOCK, Quantity: "10"}
	if err := createTx(ctx, p, userID, "IBKR", "acct1", "", tx, instID, nil); err != nil {
		t.Fatalf("create tx: %v", err)
	}

	rows, err := p.ListTxsForExport(ctx, userID)
	if err != nil {
		t.Fatalf("list txs for export: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want only the real posting: %+v", len(rows), rows)
	}
	if rows[0].Description == "INITIALIZE" {
		t.Fatalf("exported a synthetic posting: %+v", rows[0])
	}
}

// The basis is reported only where it differs from the posting's own date. The
// column is NOT NULL and the insert trigger seeds it from the timestamp, so a
// raw select would stamp a redundant date onto every posting in the file.
func TestListTxsForExport_ShareCountBasisOnlyWhenRestated(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	userID, _ := p.GetOrCreateUser(ctx, "sub|exp-scb", "U", "u@exp-scb.com")
	instID, err := p.EnsureInstrument(ctx, "", "", "", "", "", "", []db.IdentifierInput{
		{Type: "BROKER_DESCRIPTION", Domain: "IBKR", Value: "SCB", Canonical: false},
	}, "", nil, nil, nil)
	if err != nil {
		t.Fatalf("ensure instrument: %v", err)
	}
	asTraded := time.Date(2024, 1, 15, 10, 0, 0, 0, time.UTC)
	restatedAt := time.Date(2024, 2, 20, 10, 0, 0, 0, time.UTC)
	basis := time.Date(2025, 7, 1, 0, 0, 0, 0, time.UTC)
	if err := createTx(ctx, p, userID, "IBKR", "a", "",
		&apiv1.Tx{Timestamp: timestamppb.New(asTraded), InstrumentDescription: "SCB", Type: typev1.TxType_BUYSTOCK, Quantity: "1"},
		instID, nil); err != nil {
		t.Fatalf("create as-traded tx: %v", err)
	}
	if err := createTx(ctx, p, userID, "IBKR", "a", "",
		&apiv1.Tx{Timestamp: timestamppb.New(restatedAt), InstrumentDescription: "SCB", Type: typev1.TxType_BUYSTOCK, Quantity: "2"},
		instID, &basis); err != nil {
		t.Fatalf("create restated tx: %v", err)
	}

	rows, err := p.ListTxsForExport(ctx, userID)
	if err != nil {
		t.Fatalf("list txs for export: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want 2", len(rows))
	}
	if rows[0].ShareCountBasis != nil {
		t.Fatalf("as-traded posting reports a basis: %v", rows[0].ShareCountBasis)
	}
	if rows[1].ShareCountBasis == nil || !rows[1].ShareCountBasis.Equal(basis) {
		t.Fatalf("restated basis = %v, want %s", rows[1].ShareCountBasis, basis)
	}
}

// Ordered by broker, then group, then posting, so the export assembles its
// windows and groups in a single scan and two exports of the same data agree.
func TestListTxsForExport_OrderedForGrouping(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	userID, _ := p.GetOrCreateUser(ctx, "sub|exp-ord", "U", "u@exp-ord.com")
	instID, err := p.EnsureInstrument(ctx, "", "", "", "", "", "", []db.IdentifierInput{
		{Type: "BROKER_DESCRIPTION", Domain: "IBKR", Value: "ORDX", Canonical: false},
	}, "", nil, nil, nil)
	if err != nil {
		t.Fatalf("ensure instrument: %v", err)
	}
	base := time.Date(2024, 5, 1, 12, 0, 0, 0, time.UTC)
	// Written newest first and under two brokers, so passing means the query
	// ordered them rather than the insert order surviving.
	seed := []struct {
		broker string
		offset time.Duration
		qty    string
	}{
		{"IBKR", 2 * time.Hour, "3"},
		{"FIDELITY", time.Hour, "2"},
		{"FIDELITY", 0, "1"},
	}
	for _, s := range seed {
		tx := &apiv1.Tx{Timestamp: timestamppb.New(base.Add(s.offset)), InstrumentDescription: "ORDX", Type: typev1.TxType_BUYSTOCK, Quantity: s.qty}
		if err := createTx(ctx, p, userID, s.broker, "a", "", tx, instID, nil); err != nil {
			t.Fatalf("create tx: %v", err)
		}
	}

	rows, err := p.ListTxsForExport(ctx, userID)
	if err != nil {
		t.Fatalf("list txs for export: %v", err)
	}
	got := make([]string, len(rows))
	for i, r := range rows {
		got[i] = r.Broker + ":" + r.Quantity.String()
	}
	want := []string{"FIDELITY:1", "FIDELITY:2", "IBKR:3"}
	for i := range want {
		if i >= len(got) || got[i] != want[i] {
			t.Fatalf("order = %v, want %v", got, want)
		}
	}
}
