package postgres

import (
	"context"
	"fmt"
	"github.com/google/uuid"
	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	typev1 "github.com/leedenison/portfoliodb/proto/type/v1"
	"github.com/leedenison/portfoliodb/server/db"
	"google.golang.org/protobuf/types/known/timestamppb"
	"os"
	"testing"
	"time"
)

// seedBalancedGroups writes groups balanced two-leg trades, the shape a broker export
// lands as: a converting security leg and the cash it settled for. It returns the user
// and the instrument the security legs point at.
//
// One ReplaceTxsInPeriod call for all of them, inside the test's transaction, so the
// insert cost measured is the one a real import pays.
func seedBalancedGroups(t testing.TB, p *Postgres, groups int) (string, string) {
	t.Helper()
	ctx := context.Background()

	userID, err := p.GetOrCreateUser(ctx, "sub|balance-bench", "Bench", "bench@balance.com")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	instID, err := p.EnsureInstrument(ctx, "STOCK", "", "USD", "BENCH", "", "",
		[]db.IdentifierInput{{Type: "MIC_TICKER", Domain: "XNAS", Value: "BENCH", Canonical: true}}, "", nil, nil, nil)
	if err != nil {
		t.Fatalf("ensure instrument: %v", err)
	}
	usd, err := p.EnsureInstrument(ctx, "CASH", "", "USD", "USD", "", "",
		[]db.IdentifierInput{{Type: "CURRENCY", Value: "USD", Canonical: true}}, "", nil, nil, nil)
	if err != nil {
		t.Fatalf("ensure USD: %v", err)
	}

	base := time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC)
	price := "100"
	var txs []*apiv1.Tx
	var ids []string
	var ws []db.Weight
	for i := range groups {
		ref := fmt.Sprintf("g%d", i)
		at := timestamppb.New(base.Add(time.Duration(i) * time.Minute))
		txs = append(txs,
			&apiv1.Tx{Timestamp: at, InstrumentDescription: "BENCH", BrokerTxType: []typev1.TxType{typev1.TxType_TRADE_ASSET}, ResolvedTxType: typev1.TxType_TRADE_ASSET,
				Quantity: "10", UnitPrice: &price, SettlementCurrency: "USD", GroupRef: ref},
			&apiv1.Tx{Timestamp: at, InstrumentDescription: "USD", BrokerTxType: []typev1.TxType{typev1.TxType_TRADE_ASSET}, ResolvedTxType: typev1.TxType_TRADE_ASSET,
				Quantity: "-1000", SettlementCurrency: "USD", TradingCurrency: "USD", GroupRef: ref})
		ids = append(ids, instID, usd)
		ws = append(ws,
			db.Weight{Amount: decf(1000), Commodity: "cur:USD"},
			db.Weight{Amount: decf(-1000), Commodity: "cur:USD"})
	}
	from := timestamppb.New(base.Add(-time.Hour))
	before := timestamppb.New(base.Add(time.Duration(groups+60) * time.Minute))
	if err := p.ReplaceTxsInPeriod(ctx, userID, "BENCH", "", from, before, txs, ids, ws, nil); err != nil {
		t.Fatalf("seed groups: %v", err)
	}
	return userID, instID
}

// seedPairedJournals writes groups of two journal legs that both weigh in the same
// security, which is the shape a merge has to keep balanced: both legs name the
// commodity by instrument, so both move when the instrument does. It returns the user,
// the instrument the postings point at, and a second instrument to merge it into.
func seedPairedJournals(t testing.TB, p *Postgres, groups int) (string, string, string) {
	t.Helper()
	ctx := context.Background()

	userID, err := p.GetOrCreateUser(ctx, "sub|merge-bench", "Bench", "bench@merge.com")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	mergedAway, err := p.EnsureInstrument(ctx, "STOCK", "", "USD", "MRGA", "", "",
		[]db.IdentifierInput{{Type: "MIC_TICKER", Domain: "XNAS", Value: "MRGA", Canonical: true}}, "", nil, nil, nil)
	if err != nil {
		t.Fatalf("ensure merged-away: %v", err)
	}
	survivor, err := p.EnsureInstrument(ctx, "STOCK", "", "USD", "MRGB", "", "",
		[]db.IdentifierInput{{Type: "MIC_TICKER", Domain: "XNAS", Value: "MRGB", Canonical: true}}, "", nil, nil, nil)
	if err != nil {
		t.Fatalf("ensure survivor: %v", err)
	}

	base := time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC)
	commodity := "inst:" + mergedAway
	var txs []*apiv1.Tx
	var ids []string
	var ws []db.Weight
	for i := range groups {
		ref := fmt.Sprintf("j%d", i)
		at := timestamppb.New(base.Add(time.Duration(i) * time.Minute))
		txs = append(txs,
			&apiv1.Tx{Timestamp: at, InstrumentDescription: "MRGA", BrokerTxType: []typev1.TxType{typev1.TxType_TRANSFER}, ResolvedTxType: typev1.TxType_TRANSFER, Quantity: "-10", GroupRef: ref},
			&apiv1.Tx{Timestamp: at, InstrumentDescription: "MRGA", BrokerTxType: []typev1.TxType{typev1.TxType_TRANSFER}, ResolvedTxType: typev1.TxType_TRANSFER, Quantity: "10", GroupRef: ref})
		ids = append(ids, mergedAway, mergedAway)
		ws = append(ws,
			db.Weight{Amount: decf(-10), Commodity: commodity},
			db.Weight{Amount: decf(10), Commodity: commodity})
	}
	from := timestamppb.New(base.Add(-time.Hour))
	before := timestamppb.New(base.Add(time.Duration(groups+60) * time.Minute))
	if err := p.ReplaceTxsInPeriod(ctx, userID, "MERGE", "", from, before, txs, ids, ws, nil); err != nil {
		t.Fatalf("seed journals: %v", err)
	}
	return userID, mergedAway, survivor
}

// TestBalanceConstraintPerformance times what the deferred balance constraint costs an
// import. It reports rather than asserts: a threshold here would only be flaky.
//
// The constraint is DEFERRABLE INITIALLY DEFERRED, so its work happens at COMMIT. This
// runs under testDBTx, which rolls back -- but SET CONSTRAINTS ... IMMEDIATE drains the
// queued checks at exactly the point COMMIT would have run them, so the drain time is
// the COMMIT-time cost, measurable separately from the insert.
//
// Postgres requires AFTER ... FOR EACH ROW for a constraint trigger, so a
// statement-level check over transition tables is not available: every posting queues
// its own event and each group is therefore checked once per leg.
//
// Measured on a 10,000-posting import (5,000 two-leg groups) against
// timescale/timescaledb:latest-pg16 in docker:
//
//	insert                      4.1s   (413us per posting)
//	deferred check drain        208ms  (21us per posting)
//	split recompute, guarded    630ms
//	split recompute, unguarded  800ms
//
// So the constraint costs about 5% of an import it makes unbypassable, which settles
// the question the row-level trigger raised: Postgres requires AFTER ... FOR EACH ROW
// for a constraint trigger, so a statement-level check over transition tables is not
// available and each group is checked once per leg. At 21us a check that is
// affordable, and deduplicating the checks within a transaction is not worth the
// machinery.
//
// The guarded/unguarded pair is the WHEN clause on the UPDATE trigger. The recompute
// rewrites split_adjusted_* for every posting in the table and leaves weight alone, so
// the guard turns 10,000 queued checks into none for 170ms on this size -- growing
// with the table, since the recompute is whole-table and the import is not.
//
// Run with: BENCH_BALANCE=1 make db-test
func TestBalanceConstraintPerformance(t *testing.T) {
	if os.Getenv("BENCH_BALANCE") == "" {
		t.Skip("set BENCH_BALANCE=1 to run")
	}
	ctx := context.Background()
	const groups = 5000

	// Constrained: seed, then drain the queue COMMIT would have drained.
	p := testDBTx(t)
	start := time.Now()
	_, instID := seedBalancedGroups(t, p, groups)
	insert := time.Since(start)

	start = time.Now()
	if err := drainBalanceChecks(t, p); err != nil {
		t.Fatalf("drain: %v", err)
	}
	drain := time.Since(start)

	t.Logf("constrained: insert %s, deferred check drain %s (%d postings in %d groups)",
		insert.Round(time.Millisecond), drain.Round(time.Millisecond), groups*2, groups)
	t.Logf("per posting: insert %s, check %s",
		(insert / time.Duration(groups*2)).Round(time.Microsecond),
		(drain / time.Duration(groups*2)).Round(time.Microsecond))

	// The split-adjustment recompute over the whole table: the worst case in the
	// schema for the UPDATE trigger, and what its WHEN guard exists for.
	if _, err := p.q.ExecContext(ctx, `
		INSERT INTO stock_splits (instrument_id, ex_date, split_from, split_to, data_provider)
		VALUES ($1::uuid, '2025-01-01'::date, 1, 3, 'bench')
	`, instID); err != nil {
		t.Fatalf("insert split: %v", err)
	}
	start = time.Now()
	if err := p.RecomputeSplitAdjustments(ctx, ""); err != nil {
		t.Fatalf("recompute: %v", err)
	}
	recompute := time.Since(start)
	if err := drainBalanceChecks(t, p); err != nil {
		t.Fatalf("drain after recompute: %v", err)
	}
	t.Logf("whole-table split recompute, guarded (touches every posting, weight untouched): %s",
		recompute.Round(time.Millisecond))

	// The same recompute with the WHEN guard removed, so the guard's value is a
	// measured delta rather than an assumption. Dropping and recreating a trigger
	// inside the test transaction is safe: the rollback takes it with everything else.
	if _, err := p.q.ExecContext(ctx, `
		DROP TRIGGER trg_tx_group_balance_update ON txs;
		CREATE CONSTRAINT TRIGGER trg_tx_group_balance_update
		  AFTER UPDATE ON txs
		  DEFERRABLE INITIALLY DEFERRED
		  FOR EACH ROW EXECUTE FUNCTION check_tx_group_balance();
	`); err != nil {
		t.Fatalf("replace the update trigger: %v", err)
	}
	start = time.Now()
	if err := p.RecomputeSplitAdjustments(ctx, ""); err != nil {
		t.Fatalf("unguarded recompute: %v", err)
	}
	if err := drainBalanceChecks(t, p); err != nil {
		t.Fatalf("drain after unguarded recompute: %v", err)
	}
	unguarded := time.Since(start)
	t.Logf("whole-table split recompute, unguarded (one queued check per posting): %s -- the WHEN guard saves %s",
		unguarded.Round(time.Millisecond), (unguarded - recompute).Round(time.Millisecond))

	t.Log("all figures are one run against a docker postgres and are indicative, not a threshold")
}

// TestBalanceConstraintMergePerformance times the one path that legitimately fires the
// UPDATE trigger for every posting it touches. It is a separate test because the
// DROP TRIGGER above takes an ACCESS EXCLUSIVE lock on txs for the rest of that
// transaction, which would block the connection this one opens.
//
// Measured on 10,000 postings: 950ms, 95us per posting. Dearer than the deferred
// check because each row is checked as it is updated rather than once at the end,
// and it is the price of the merge keeping weight_commodity in step with
// instrument_id. A merge touching this many postings is not a routine event.
//
// Run with: BENCH_BALANCE=1 make db-test
func TestBalanceConstraintMergePerformance(t *testing.T) {
	if os.Getenv("BENCH_BALANCE") == "" {
		t.Skip("set BENCH_BALANCE=1 to run")
	}
	ctx := context.Background()
	const groups = 5000

	// Paired journals: both legs weigh in the instrument being merged, so both move
	// together, which is the case the merge has to keep balanced.
	p := testDBTx(t)
	_, mergedAway, survivor := seedPairedJournals(t, p, groups)
	if err := drainBalanceChecks(t, p); err != nil {
		t.Fatalf("seeded journals do not balance: %v", err)
	}

	start := time.Now()
	if err := p.runInTx(ctx, func(exec queryable) error {
		return mergeInstruments(ctx, exec, uuid.MustParse(survivor), uuid.MustParse(mergedAway))
	}); err != nil {
		t.Fatalf("merge: %v", err)
	}
	merge := time.Since(start)
	t.Logf("merge rewriting weight_commodity on %d postings, each checked: %s (%s per posting)",
		groups*2, merge.Round(time.Millisecond),
		(merge / time.Duration(groups*2)).Round(time.Microsecond))
}
