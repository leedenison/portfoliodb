package postgres

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	"github.com/leedenison/portfoliodb/server/db"
	"github.com/shopspring/decimal"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// seedValuationLoad builds a portfolio of instruments each holding years of
// weekday bars, which is the shape the valuation query is slowest on: a wide
// date grid over a hypertable with a gap at every weekend.
func seedValuationLoad(t testing.TB, p *Postgres, instruments, years int) (string, time.Time, time.Time) {
	t.Helper()
	ctx := context.Background()

	userID, err := p.GetOrCreateUser(ctx, "sub|bench", "Bench", "bench@bench.com")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	from := d(2020, 1, 1)
	before := from.AddDate(years, 0, 0)
	buy := time.Date(2019, 12, 31, 12, 0, 0, 0, time.UTC)

	// One ReplaceTxsInPeriod call for all of them: it replaces everything in the
	// window, so per-instrument calls would each wipe the last.
	var txs []*apiv1.Tx
	var instIDs []string
	for i := 0; i < instruments; i++ {
		desc := fmt.Sprintf("BENCH%03d", i)
		instID, err := p.EnsureInstrument(ctx, "STOCK", "", "USD", desc, "", "", []db.IdentifierInput{
			{Type: "BROKER_DESCRIPTION", Domain: "BENCH", Value: desc, Canonical: false},
		}, "", nil, nil, nil)
		if err != nil {
			t.Fatalf("ensure instrument: %v", err)
		}
		txs = append(txs, &apiv1.Tx{
			Timestamp: timestamppb.New(buy), InstrumentDescription: desc,
			Type: apiv1.TxType_BUYSTOCK, Quantity: 100, Account: "main",
		})
		instIDs = append(instIDs, instID)
	}
	if err := p.ReplaceTxsInPeriod(ctx, userID, "BENCH", "",
		timestamppb.New(buy.Add(-time.Hour)), timestamppb.New(buy.Add(time.Hour)),
		txs, instIDs, nil); err != nil {
		t.Fatalf("replace txs: %v", err)
	}

	for _, instID := range instIDs {
		var bars []db.EODPrice
		for day := from; day.Before(before); day = day.Add(db.Day) {
			if day.Weekday() == time.Saturday || day.Weekday() == time.Sunday {
				continue
			}
			bars = append(bars, db.EODPrice{
				InstrumentID: instID, PriceDate: day,
				Close: decf(100).Add(decimal.NewFromInt(int64(day.YearDay() % 50))), DataProvider: "bench",
			})
		}
		if err := p.UpsertPricesForRange(ctx, instID, "bench", bars, from, before, nil); err != nil {
			t.Fatalf("upsert bars: %v", err)
		}
	}
	return userID, from, before
}

// TestValuationQueryPerformance times the read-time carry-forward and prints the
// plan. It reports rather than asserts: a threshold here would only be flaky.
//
// Measured when the carry-forward moved out of the write path: 860ms mean for
// 50 instruments over 5 years, against 739ms for the same query with the price
// CTE reduced to the flat scan the stored-fill design used. The 16% is an
// overstatement, since that comparison scans only the real bars while the
// stored-fill design also held a row for every weekend and holiday.
//
// Run with: BENCH_VALUATION=1 make db-test
func TestValuationQueryPerformance(t *testing.T) {
	if os.Getenv("BENCH_VALUATION") == "" {
		t.Skip("set BENCH_VALUATION=1 to run")
	}
	p := testDBTx(t)
	ctx := context.Background()

	const instruments, years = 50, 5
	seedStart := time.Now()
	userID, from, before := seedValuationLoad(t, p, instruments, years)
	t.Logf("seeded %d instruments x %d years in %s", instruments, years, time.Since(seedStart).Round(time.Millisecond))

	// Warm caches, then time.
	if _, err := p.GetUserValuation(ctx, userID, from, before, "USD"); err != nil {
		t.Fatalf("warmup: %v", err)
	}
	const runs = 5
	var total time.Duration
	for i := 0; i < runs; i++ {
		start := time.Now()
		points, err := p.GetUserValuation(ctx, userID, from, before, "USD")
		if err != nil {
			t.Fatalf("valuation: %v", err)
		}
		total += time.Since(start)
		if len(points) == 0 {
			t.Fatal("no valuation points")
		}
	}
	t.Logf("read-time carry-forward: %s mean over %d runs (%d instruments x %d days)",
		(total / runs).Round(time.Millisecond), runs, instruments, int(before.Sub(from).Hours()/24))

	// The pre-change shape for comparison: a flat scan of eod_prices joined by
	// exact date, which is what the query did when every non-trading day had a
	// stored row. Values differ (weekends read as unpriced), but the cost of the
	// price step is comparable and that is what is being measured.
	var plan string
	rows, err := p.q.QueryContext(ctx,
		`EXPLAIN (ANALYZE, BUFFERS) `+valuationQuery(false), userID, from, before, "USD")
	if err != nil {
		t.Fatalf("explain: %v", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			t.Fatalf("scan plan: %v", err)
		}
		plan += line + "\n"
	}
	t.Logf("plan:\n%s", plan)
}
