package transfermatch

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/leedenison/portfoliodb/server/db"
	"github.com/leedenison/portfoliodb/server/telemetry"
	"github.com/leedenison/portfoliodb/server/worker"
)

const name = "transfer_match"

// RunWorker pairs the unmatched sides of transfers on each signal, and blocks until
// ctx is cancelled. Rapid signals are debounced by the buffered trigger channel.
//
// A job rather than part of ingestion, because the second side of a transfer can
// arrive in a later import: matching is a function of all stored state, not of one
// upload's payload. Inline it would also have to fail an otherwise-correct import to
// report a failure, or swallow one inside a success path.
//
// Two things signal it, both on the same channel, as the price fetcher has. The
// TriggerTransferMatch RPC is the one an external cron job or CLI calls, and is how
// matching runs on a cadence -- there is no clock in this process. The ingestion
// worker fires it too, after a tx import commits, so a pair whose second side has
// just landed does not wait for the next tick. Neither is redundant: a cadence alone
// would leave a just-imported transfer reported as unmatched until it came round,
// and the ingestion nudge alone would never retry a cycle that failed.
func RunWorker(ctx context.Context, database db.DB, counter telemetry.CounterIncrementer, log *slog.Logger, trigger <-chan struct{}, workers *worker.Registry) {
	if workers != nil {
		workers.SetIdle(name)
	}
	for {
		select {
		case <-ctx.Done():
			return
		case _, ok := <-trigger:
			if !ok {
				return
			}
			runCycle(ctx, database, counter, log, workers)
		}
	}
}

// runCycle reads every side no match names and records the pairs it can identify.
//
// Whole-corpus rather than scoped to the upload that triggered it: a side stored
// months ago becomes matchable the moment its counterpart arrives, and the side that
// arrived is not necessarily in the import being processed. The read is bounded by
// the partial index over residual postings and by the anti-join, so a corpus with
// nothing outstanding costs one query.
func runCycle(ctx context.Context, database db.DB, counter telemetry.CounterIncrementer, log *slog.Logger, workers *worker.Registry) {
	if counter != nil {
		counter.Incr(ctx, "transfer_match.cycles")
	}
	defer func() {
		if workers != nil {
			workers.SetIdle(name)
		}
	}()

	sides, err := database.ListUnmatchedTransferSides(ctx, db.TransferSideOpts{})
	if err != nil {
		if log != nil {
			log.ErrorContext(ctx, "transfer match: list sides", "err", err)
		}
		return
	}
	if len(sides) == 0 {
		return
	}
	if workers != nil {
		workers.SetRunning(name, fmt.Sprintf("Matching %d transfer sides", len(sides)))
	}
	if counter != nil {
		counter.IncrBy(ctx, "transfer_match.sides", int64(len(sides)))
	}

	matches := Match(sides, DefaultOpts())
	if len(matches) == 0 {
		return
	}
	written, err := database.CreateTransferMatches(ctx, matches)
	if err != nil {
		if log != nil {
			log.ErrorContext(ctx, "transfer match: create matches", "err", err)
		}
		return
	}
	if counter != nil {
		// Pairs, not sides, which is why this is not called ".matched": next to
		// ".sides" a reader would divide one by the other and get half the ratio.
		counter.IncrBy(ctx, "transfer_match.pairs", int64(written))
	}
	if log != nil {
		// The residue is the point of the number, not a shortfall: a deposit from
		// outside an account has no counterpart and must stay unmatched.
		log.InfoContext(ctx, "transfer match",
			"sides", len(sides), "matched", written, "unmatched", len(sides)-written*2)
	}
}
