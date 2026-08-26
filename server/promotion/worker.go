// Package promotion turns identity claims enough users hold into facts the
// instance holds.
//
// A mapping learned from a broker file is owned by whoever uploaded it and
// resolves for them alone. What lifts it out of that scope is other users
// supplying the same mapping: their agreement rules out a doctored, stale or
// misattributed file, which is the only thing agreement between users can rule
// out -- they all read the same mapping out of the same broker security master,
// so it says nothing about whether the broker is right. See
// docs/adr/0063-identity-claims-are-owned-until-users-corroborate-them.md.
package promotion

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/leedenison/portfoliodb/server/db"
	"github.com/leedenison/portfoliodb/server/worker"
)

const name = "promotion"

// RunWorker promotes corroborated identity claims on each signal, and blocks
// until ctx is cancelled. Rapid signals are debounced by the buffered trigger
// channel.
//
// Two things signal it, as they do the transfer matcher. The
// TriggerPromotionSweep RPC is the one an external cron job or CLI calls, and is
// how the sweep runs on a cadence -- there is no clock in this process. The
// ingestion worker fires it after a tx import commits, so a mapping whose last
// corroborating upload has just landed does not wait for the next tick.
//
// The threshold is read at the top of every cycle rather than at startup, so an
// admin changing it takes effect on the next sweep rather than on the next
// deployment.
func RunWorker(ctx context.Context, database db.DB, tel db.TelemetryDB, log *slog.Logger, trigger <-chan struct{}, workers *worker.Registry) {
	if tel == nil {
		tel = db.NopTelemetry{}
	}
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
			runID := tel.StartRun(ctx, db.TelemetryRun{Kind: db.TelemetryRunPromotionCycle})
			outcome := db.TelemetryOutcomeSuccess
			if err := runCycle(ctx, database, log, workers); err != nil {
				outcome = db.TelemetryOutcomeFailed
			}
			tel.EndRun(ctx, runID, outcome)
		}
	}
}

// runCycle promotes what it can and reports what it could not.
//
// Whole-corpus rather than scoped to the upload that triggered it: the upload
// that carries a mapping over the threshold is the last of several, and the rows
// it corroborates were written by other users months ago. The read is bounded by
// the partial index over owned rows, so an instance whose claims have all been
// promoted costs one query.
func runCycle(ctx context.Context, database db.DB, log *slog.Logger, workers *worker.Registry) error {
	defer func() {
		if workers != nil {
			workers.SetIdle(name)
			workers.CycleDone(name)
		}
	}()

	threshold, err := database.PromotionThreshold(ctx)
	if err != nil {
		if log != nil {
			log.ErrorContext(ctx, "promotion: read threshold", "err", err)
		}
		return err
	}
	if workers != nil {
		workers.SetRunning(name, fmt.Sprintf("Promoting mappings %d users agree on", threshold))
	}
	res, err := database.PromoteCorroboratedIdentifiers(ctx, threshold)
	if err != nil {
		if log != nil {
			log.ErrorContext(ctx, "promotion: promote", "err", err)
		}
		return err
	}
	// Asked after the promotion rather than before, so what it reports is what
	// this sweep is leaving behind rather than what it started with.
	contested, err := database.CountUnpromotableClaims(ctx)
	if err != nil {
		if log != nil {
			log.ErrorContext(ctx, "promotion: count contested claims", "err", err)
		}
		return err
	}
	if log != nil && (res.Promoted > 0 || res.AlreadyHeld > 0 || contested > 0) {
		// contested is the residue and not a shortfall: users disagreeing about
		// a mapping is what the sweep must leave for a person, and a number that
		// stays put is the signal rather than the noise.
		log.InfoContext(ctx, "promotion sweep",
			"threshold", threshold, "promoted", res.Promoted, "already_held", res.AlreadyHeld,
			"claims_cleared", res.ClaimsCleared, "contested", contested)
	}
	return nil
}
