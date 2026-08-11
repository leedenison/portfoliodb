package grouping

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/leedenison/portfoliodb/server/db"
	"github.com/leedenison/portfoliodb/server/telemetry"
	"github.com/leedenison/portfoliodb/server/worker"
)

const name = "grouping"

// RunWorker derives transaction groups on each signal, and blocks until ctx is
// cancelled. Rapid signals are debounced by the buffered trigger channel.
//
// A job rather than part of ingestion, for the reason transfer matching is: the legs
// of one event can arrive in separate imports, so the partition is a function of all
// stored state rather than of one upload's payload. Inline it would also have to fail
// an otherwise-correct import to report a failure, or swallow one inside a success.
//
// Two things signal it, both on the same channel. The TriggerGrouping RPC is what an
// external cron job or CLI calls, and is how a cycle runs on a cadence -- there is no
// clock in this process. The ingestion worker fires it after a tx import commits, so
// legs that have just landed beside older ones are joined without waiting for the
// next tick.
func RunWorker(ctx context.Context, database db.DB, counter telemetry.CounterIncrementer, log *slog.Logger, trigger <-chan struct{}, workers *worker.Registry, apply bool) {
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
			runCycle(ctx, database, counter, log, workers, apply)
		}
	}
}

// runCycle derives the partition of every neighbourhood a seed reaches, reports how
// it differs from what is stored, and writes the difference where asked to.
//
// apply is off by default and 0098 is what turns it on, once a shadow run over a
// corpus has been looked at. Off, the cycle is a measurement and can leave nothing
// behind; on, it writes only what it disagrees with, so a cycle that repartitions
// nothing writes nothing either way.
func runCycle(ctx context.Context, database db.DB, counter telemetry.CounterIncrementer, log *slog.Logger, workers *worker.Registry, apply bool) {
	if counter != nil {
		counter.Incr(ctx, "grouping.cycles")
	}
	defer func() {
		if workers != nil {
			workers.SetIdle(name)
		}
	}()

	// Every user, seeded from the groups holding something a missing leg would
	// explain. An import nudge could seed from its own job instead, which is
	// narrower; it is not wired that way yet because a shadow cycle wants the
	// widest view it can get of where the engine and the converters differ.
	seeds, err := database.ListGroupingSeeds(ctx, db.GroupingSeedOpts{Residual: true})
	if err != nil {
		if log != nil {
			log.ErrorContext(ctx, "grouping: list seeds", "err", err)
		}
		return
	}
	if len(seeds) == 0 {
		return
	}
	if workers != nil {
		workers.SetRunning(name, fmt.Sprintf("Grouping from %d postings", len(seeds)))
	}
	if counter != nil {
		counter.IncrBy(ctx, "grouping.seeds", int64(len(seeds)))
	}

	total := Divergence{}
	for _, userID := range usersOf(seeds) {
		d, err := cycleUser(ctx, database, userID, byUser(seeds, userID), apply)
		if err != nil {
			if log != nil {
				log.ErrorContext(ctx, "grouping: derive", "user", userID, "err", err)
			}
			continue
		}
		total.Groups += d.Groups
		total.Stored += d.Stored
		total.Agreed += d.Agreed
		total.Joined += d.Joined
		total.Split += d.Split
		for _, e := range d.Examples {
			total.note(e)
		}
	}

	if counter != nil {
		counter.IncrBy(ctx, "grouping.groups", int64(total.Groups))
		counter.IncrBy(ctx, "grouping.agreed", int64(total.Agreed))
		counter.IncrBy(ctx, "grouping.joined", int64(total.Joined))
		counter.IncrBy(ctx, "grouping.split", int64(total.Split))
	}
	if log != nil {
		// Logged whether or not anything differs. A cycle that agrees everywhere is
		// the result 0098 is waiting for, so it is worth being able to see it
		// happen rather than inferring it from silence.
		log.InfoContext(ctx, "grouping shadow",
			"derived", total.Groups, "stored", total.Stored, "agreed", total.Agreed,
			"joined", total.Joined, "split", total.Split, "examples", total.Examples)
	}
}

// cycleUser grows each seed's neighbourhood, partitions it, and measures the result
// against what is stored.
//
// Neighbourhoods are grown one seed at a time and the ones that overlap are answered
// from the same reads, since a posting already held is never asked for again. A seed
// swallowed by an earlier neighbourhood costs one round that finds nothing.
func cycleUser(ctx context.Context, database db.DB, userID string, seeds []db.GroupingPosting, apply bool) (Divergence, error) {
	rules := DefaultRules()
	opts := DefaultOpts()
	done := map[string]bool{}
	var total Divergence
	for _, seed := range seeds {
		if done[seed.ID] {
			continue
		}
		ps, err := Neighbourhood(ctx, userID, []db.GroupingPosting{seed}, rules, database)
		if err != nil {
			return Divergence{}, err
		}
		for _, p := range ps {
			done[p.ID] = true
		}
		gs := Partition(ps, rules, opts)
		if apply {
			// Written per neighbourhood rather than per cycle: each is its own
			// transaction, so a failure costs one region rather than the run, and
			// the balance constraint is evaluated over a set of groups that were
			// all decided together.
			if _, err := database.ApplyGrouping(ctx, userID, Diff(ps, gs)); err != nil {
				return Divergence{}, err
			}
		}
		d := Compare(ps, gs)
		total.Groups += d.Groups
		total.Stored += d.Stored
		total.Agreed += d.Agreed
		total.Joined += d.Joined
		total.Split += d.Split
		for _, e := range d.Examples {
			total.note(e)
		}
	}
	return total, nil
}

// usersOf lists the users the seeds belong to, in the order the read returned them,
// so a cycle is deterministic.
func usersOf(seeds []db.GroupingPosting) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range seeds {
		if seen[s.UserID] {
			continue
		}
		seen[s.UserID] = true
		out = append(out, s.UserID)
	}
	return out
}

func byUser(seeds []db.GroupingPosting, userID string) []db.GroupingPosting {
	var out []db.GroupingPosting
	for _, s := range seeds {
		if s.UserID == userID {
			out = append(out, s)
		}
	}
	return out
}
