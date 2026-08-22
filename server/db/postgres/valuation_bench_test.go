package postgres

import (
	"context"
	"fmt"
	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	typev1 "github.com/leedenison/portfoliodb/proto/type/v1"
	"github.com/leedenison/portfoliodb/server/db"
	"github.com/shopspring/decimal"
	"google.golang.org/protobuf/types/known/timestamppb"
	"os"
	"testing"
	"time"
)

// valuationLoad is the portfolio shape a valuation benchmark runs against.
//
// Every field past the first two is here because leaving it out took a branch
// out of the plan. A load of one transaction per instrument, USD throughout,
// with bars starting exactly at the window left fx_instruments, span_seeds and
// display_fx_instrument returning no rows and one scan of filled_prices marked
// "never executed" -- so the figure it produced covered the cheap path and
// reported nothing about the rest.
type valuationLoad struct {
	instruments int
	years       int

	// Transactions per instrument, spread across the window. Each cell of the
	// instrument-by-day grid resolves its position through a LATERAL that
	// rescans the instrument's whole transaction history, so cost carries a
	// term in this as well as in the grid. One transaction each hides it: the
	// scan finds its row immediately and there is nothing to discard.
	txsPerInstrument int

	// Every nth instrument is denominated in a currency other than USD. With
	// none, fx_instruments is empty, both FX joins fall away and the conversion
	// CASE -- the one place the query leaves exact decimals -- never runs.
	foreignEvery int

	// Every nth instrument stops being priced halfway through the window: the
	// delisting the carry-forward's coverage bound exists to stop. Without one
	// the bound is present but never load-bearing, since every span runs to the
	// end of the window anyway.
	delistedEvery int

	// Matched transfers spread across the window, each in transit for five days.
	// With none, the clearing branch of the posting filter matches nothing and
	// portfolio_in_flight_txs is never probed for a row it can return, so the
	// figure reports only the cost of asking.
	matchedTransfers int
}

// foreignCurrencies are cycled across the instruments valuationLoad marks as
// foreign. Two rather than one so that a display currency of GBP still leaves
// EUR converting through a cross rate rather than every foreign holding being
// the display currency itself.
var foreignCurrencies = []string{"EUR", "GBP"}

// preWindow is how much bar history to lay down before the valuation window
// opens. span_seeds looks for the last bar inside the coverage span but before
// the window, which is what stops an instrument reading as unpriced until its
// first bar inside the window; with the series starting exactly at the window
// there is nothing for it to find. Thirty days is enough to be found over a
// holiday period and cheap to store.
const preWindow = 30 * db.Day

// seedValuationLoad builds a portfolio of instruments each holding years of
// weekday bars, which is the shape the valuation query is slowest on: a wide
// date grid over a hypertable with a gap at every weekend. It returns the user
// and the half-open valuation window.
func seedValuationLoad(t testing.TB, p *Postgres, load valuationLoad) (userID, portfolioID string, from, before time.Time) {
	t.Helper()
	ctx := context.Background()

	userID, err := p.GetOrCreateUser(ctx, "sub|bench", "Bench", "bench@bench.com")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	from = d(2020, 1, 1)
	before = from.AddDate(load.years, 0, 0)
	histFrom := from.Add(-preWindow)
	// The opening position predates the bar history, so every day of the window
	// has a holding to value rather than opening on a closed position.
	open := histFrom.Add(-db.Day)
	// Where a delisted instrument's coverage stops. Half way through, so the
	// carry-forward has a span to run out of and a stretch of window past it.
	delistedBefore := from.Add(before.Sub(from) / 2)

	// One FX pair instrument per foreign currency, priced across the whole
	// history. Valuation reads these through the same filled_prices CTE as any
	// other instrument, so they need real bars and real coverage, not just a row.
	fxIDs := make(map[string]string, len(foreignCurrencies))
	for _, cur := range foreignCurrencies {
		id, _, err := p.EnsureInstrument(ctx, "FX", "", "", "", "", "", []db.IdentifierInput{
			{
				Ref:       db.InstrumentRef{Type: "FX_PAIR", Value: cur + "USD", Domain: ""},
				Canonical: true,
			}}, nil, "", nil, nil, nil)
		if err != nil {
			t.Fatalf("ensure fx pair %s: %v", cur, err)
		}
		fxIDs[cur] = id
		seedWeekdayBars(t, p, id, histFrom, before, decf(1.1))
	}

	// One ReplaceTxsInPeriod call for all of them: it replaces everything in the
	// window, so per-instrument calls would each wipe the last.
	var txs []*apiv1.Tx
	var instIDs []string
	type seeded struct {
		id       string
		delisted bool
	}
	var insts []seeded
	for i := range load.instruments {
		desc := fmt.Sprintf("BENCH%03d", i)
		cur := "USD"
		if load.foreignEvery > 0 && i%load.foreignEvery == 0 {
			cur = foreignCurrencies[(i/load.foreignEvery)%len(foreignCurrencies)]
		}
		instID, _, err := p.EnsureInstrument(ctx, "STOCK", "", cur, desc, "", "", []db.IdentifierInput{
			{
				Ref:       db.InstrumentRef{Type: "BROKER_DESCRIPTION", Value: desc, Domain: "BENCH"},
				Canonical: false,
			}}, nil, "", nil, nil, nil)
		if err != nil {
			t.Fatalf("ensure instrument: %v", err)
		}
		insts = append(insts, seeded{
			id:       instID,
			delisted: load.delistedEvery > 0 && i%load.delistedEvery == 0,
		})

		// The opening buy, then the rest spread evenly across the window. All
		// buys, so the running position never returns to zero and qty_is_zero
		// never reads the holding as closed part way through.
		for j := range load.txsPerInstrument {
			at := open
			if j > 0 {
				at = from.Add(before.Sub(from) * time.Duration(j) / time.Duration(load.txsPerInstrument))
			}
			txs = append(txs, &apiv1.Tx{
				OrderDate: timestamppb.New(at),
				TradeDate: timestamppb.New(at), InstrumentDescription: desc,
				BrokerTxType:   []typev1.TxType{typev1.TxType_TRADE_ASSET},
				ResolvedTxType: typev1.TxType_TRADE_ASSET,
				Quantity:       "10", Account: "main", TradingCurrency: cur,
			})
			instIDs = append(instIDs, instID)
		}
	}
	if err := p.ReplaceTxsInPeriod(ctx, userID, "BENCH", "",
		timestamppb.New(open.Add(-time.Hour)), timestamppb.New(before.Add(time.Hour)),
		txs, instIDs, nil, nil); err != nil {
		t.Fatalf("replace txs: %v", err)
	}

	for _, inst := range insts {
		barsBefore := before
		if inst.delisted {
			barsBefore = delistedBefore
		}
		seedWeekdayBars(t, p, inst.id, histFrom, barsBefore, decf(100))
	}

	seedMatchedTransfers(t, p, userID, load.matchedTransfers, from, before)

	// One broker filter, so the portfolio is the whole load: the portfolio-mode
	// query differs from the user-mode one in the filter view it joins and in
	// probing portfolio_in_flight_txs, and holding the data constant is what
	// leaves those two as the only difference between the timings.
	port, err := p.CreatePortfolio(ctx, userID, "Bench")
	if err != nil {
		t.Fatalf("create portfolio: %v", err)
	}
	if err := p.SetPortfolioFilters(ctx, port.Id, []db.PortfolioFilter{
		{FilterType: "broker", FilterValue: "BENCH"},
	}); err != nil {
		t.Fatalf("set portfolio filters: %v", err)
	}

	// Everything above was written inside the test's transaction, which rolls
	// back, so autovacuum never sees any of it and the planner would otherwise
	// cost the query against a database it believes to be empty. That is not a
	// harmless difference: with no statistics the join above the day grid is
	// estimated at 111 rows against 91,350, and with them at 11,208 -- three
	// orders of magnitude wrong becomes one, which is the difference between a
	// plan that tips into a nested loop and one that does not.
	//
	// So this is what makes the benchmark measure the server rather than the
	// harness. What it does not measure is an import's own aftermath, where the
	// statistics are real but describe the table as it was before the import.
	// See 0103.
	if _, err := p.q.ExecContext(ctx,
		`ANALYZE txs, tx_groups, transfer_matches, eod_prices, price_coverage, instruments, instrument_identifiers`); err != nil {
		t.Fatalf("analyze: %v", err)
	}
	return userID, port.Id, from, before
}

// seedMatchedTransfers writes n matched cash transfers, each moving money out of
// one account and into another five days later, spread evenly across the window.
//
// Written as raw postings rather than through ingestion because the replacement
// period above already owns the whole window, and because the group ids are needed
// to write the links and a RETURNING is the cheapest way to have them. Each pair is
// balanced in its own right, so the group invariant holds as it would in production.
func seedMatchedTransfers(t testing.TB, p *Postgres, userID string, n int, from, before time.Time) {
	t.Helper()
	if n == 0 {
		return
	}
	ctx := context.Background()
	cashID, err := p.FindInstrumentByIdentifier(ctx, "CURRENCY", "", "USD")
	if err != nil || cashID == "" {
		t.Fatalf("USD cash instrument not found: %v", err)
	}
	// A group and its two legs: the account's own and the clearing counterparty
	// that holds the value in transit, equal and opposite.
	side := func(account string, at time.Time, qty string) string {
		var groupID string
		if err := p.q.QueryRowContext(ctx, `
			INSERT INTO tx_groups (user_id, timestamp) VALUES ($1::uuid, $2::timestamptz)
			RETURNING id`, userID, at).Scan(&groupID); err != nil {
			t.Fatalf("create transfer group: %v", err)
		}
		if _, err := p.q.ExecContext(ctx, `
			INSERT INTO txs (user_id, broker, account, order_date, trade_date, instrument_description,
				instrument_id, broker_tx_type, resolved_tx_type, quantity, account_type,
				group_id, weight, weight_commodity, share_count_basis, split_adjusted_quantity)
			SELECT $1::uuid, 'BENCH', $2, $3::timestamptz, $3::timestamptz, 'USD CASH', $4::uuid,
				ARRAY['TRANSFER'], 'TRANSFER', q, at, $5::uuid, q, 'cur:USD',
				$3::timestamptz::date, q
			FROM (VALUES ($6::numeric, 'USER'), (-$6::numeric, 'TRANSFER_CLEARING')) v(q, at)
		`, userID, account, at, cashID, groupID, qty); err != nil {
			t.Fatalf("insert transfer legs: %v", err)
		}
		return groupID
	}
	span := before.Sub(from)
	for i := range n {
		depart := from.Add(span * time.Duration(i) / time.Duration(n))
		fromGroup := side(fmt.Sprintf("xfer-out-%03d", i), depart, "-1000")
		toGroup := side(fmt.Sprintf("xfer-in-%03d", i), depart.Add(5*db.Day), "1000")
		if _, err := p.q.ExecContext(ctx, `
			INSERT INTO transfer_matches (user_id, from_group_id, to_group_id, instrument_id, method)
			VALUES ($1::uuid, $2::uuid, $3::uuid, $4::uuid, 'REFERENCE')
		`, userID, fromGroup, toGroup, cashID); err != nil {
			t.Fatalf("insert transfer match: %v", err)
		}
	}
}

// seedWeekdayBars writes one bar per weekday over [from, before) and declares
// coverage for the whole range. Coverage spans the weekends too: a shut market
// is a date the provider answered for, and the carry-forward is what fills it.
func seedWeekdayBars(t testing.TB, p *Postgres, instrumentID string, from, before time.Time, base decimal.Decimal) {
	t.Helper()
	var bars []db.EODPrice
	for day := from; day.Before(before); day = day.Add(db.Day) {
		if day.Weekday() == time.Saturday || day.Weekday() == time.Sunday {
			continue
		}
		bars = append(bars, db.EODPrice{
			InstrumentID: instrumentID, PriceDate: day,
			Close: base.Add(decimal.NewFromInt(int64(day.YearDay() % 50))), DataProvider: "bench",
		})
	}
	if err := p.UpsertPricesForRange(context.Background(), instrumentID, "bench", bars, from, before, nil); err != nil {
		t.Fatalf("upsert bars: %v", err)
	}
}

// TestValuationQueryPerformance times the read-time carry-forward and prints the
// plan. It reports rather than asserts: a threshold here would only be flaky.
//
// It runs the same seeded portfolio twice, in USD and in GBP. The pair is the
// measurement: displaying in USD leaves display_fx_instrument empty and only the
// foreign holdings convert, while displaying in GBP puts every holding through
// the cross-rate branch and brings in a third pass over filled_prices. The
// difference is what FX conversion costs, which a single-currency run reported
// as zero because it never executed any of it.
//
// What that comes to at the load below: 382ms in USD and 430ms in GBP, so FX is
// around a tenth and is kept for the coverage rather than the cost -- without a
// foreign holding and a foreign display currency, four of the query's steps
// return no rows. The carry-forward over prices is the largest single node at
// roughly 40%, which is what this query was always assumed to be about.
//
// It read 3.4s before daily_holdings stopped resolving each position with a
// per-day lookup, 85% of which was that one node: it ran once per cell of the
// instrument-by-day grid, 91,350 times, rescanning the whole cumulative CTE to
// keep 11 rows out of 1,000. Its cost was linear in transactions as well as in
// the grid, which is why the old load of one transaction per instrument reported
// it as 183ms and hid the term. That is the measurement this load exists for.
//
// Any figure from here is only comparable to another one taken at the same
// work_mem, against the same valuationLoad, and with statistics. The test stack
// pins the first rather than letting timescaledb-tune size it from whatever the
// container could see; the second is stated below, and changing it invalidates
// the history; the third is what the ANALYZE in seedValuationLoad is for. At
// this load the sorts peak at 8.8MB in memory against the pinned 16MB; below
// about 9MB they spill and the same query reads a fifth slower for no other
// reason.
//
// Recorded when the carry-forward moved out of the write path, against a load of
// 50 instruments over 5 years with one transaction each and no FX: 860ms, against
// 739ms for the same query with the price CTE reduced to the flat scan the
// stored-fill design used. That 16% was an overstatement, since the comparison
// scanned only real bars while the stored-fill design also held a row for every
// weekend and holiday. Those numbers predate this load and are not comparable to
// what it prints now.
//
// Run with: BENCH_VALUATION=1 make db-test
func TestValuationQueryPerformance(t *testing.T) {
	if os.Getenv("BENCH_VALUATION") == "" {
		t.Skip("set BENCH_VALUATION=1 to run")
	}
	p := testDBTx(t)
	ctx := context.Background()

	load := valuationLoad{
		instruments:      50,
		years:            5,
		txsPerInstrument: 20,
		foreignEvery:     5,
		delistedEvery:    10,
		matchedTransfers: 20,
	}
	seedStart := time.Now()
	userID, portfolioID, from, before := seedValuationLoad(t, p, load)
	days := int(before.Sub(from) / db.Day)
	t.Logf("seeded %d instruments x %d years (%d txs each, 1 in %d foreign, 1 in %d delisted) in %s",
		load.instruments, load.years, load.txsPerInstrument, load.foreignEvery, load.delistedEvery,
		time.Since(seedStart).Round(time.Millisecond))

	// Both scopes, because they are different queries: user mode asks only whether
	// a match names the group, while portfolio mode joins the filter view and probes
	// portfolio_in_flight_txs, which joins it twice more. The gap between the two
	// lines is what that costs.
	scopes := []struct {
		name string
		run  func(display string) ([]db.ValuationPoint, error)
	}{
		{"user", func(display string) ([]db.ValuationPoint, error) {
			return p.GetUserValuation(ctx, userID, from, before, display)
		}},
		{"portfolio", func(display string) ([]db.ValuationPoint, error) {
			return p.GetPortfolioValuation(ctx, portfolioID, from, before, display)
		}},
	}
	for _, scope := range scopes {
		for _, display := range []string{"USD", "GBP"} {
			// Warm caches, then time.
			if _, err := scope.run(display); err != nil {
				t.Fatalf("warmup %s %s: %v", scope.name, display, err)
			}
			const runs = 5
			var total time.Duration
			for range runs {
				start := time.Now()
				points, err := scope.run(display)
				if err != nil {
					t.Fatalf("valuation %s %s: %v", scope.name, display, err)
				}
				total += time.Since(start)
				if len(points) == 0 {
					t.Fatalf("no valuation points in %s %s", scope.name, display)
				}
			}
			t.Logf("%s display %s: %s mean over %d runs (%d instruments x %d days)",
				scope.name, display, (total / runs).Round(time.Millisecond), runs, load.instruments, days)
		}
	}

	// The plan for the fullest shape of each: displaying in a currency that is not
	// USD is the only one in which every FX branch is reachable, and portfolio mode
	// is the only one that reaches the filter view at all.
	for _, mode := range []struct {
		name  string
		query string
		scope string
	}{
		{"user", valuationQuery(false), userID},
		{"portfolio", valuationQuery(true), portfolioID},
	} {
		var plan string
		rows, err := p.q.QueryContext(ctx,
			`EXPLAIN (ANALYZE, BUFFERS) `+mode.query, mode.scope, from, before, "GBP")
		if err != nil {
			t.Fatalf("explain %s: %v", mode.name, err)
		}
		for rows.Next() {
			var line string
			if err := rows.Scan(&line); err != nil {
				_ = rows.Close()
				t.Fatalf("scan %s plan: %v", mode.name, err)
			}
			plan += line + "\n"
		}
		_ = rows.Close()
		t.Logf("%s plan:\n%s", mode.name, plan)
	}
}
