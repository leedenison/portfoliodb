package inflationfetcher

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/leedenison/portfoliodb/server/currency"
	"github.com/leedenison/portfoliodb/server/db"
	"github.com/leedenison/portfoliodb/server/pluginutil"
	"github.com/leedenison/portfoliodb/server/worker"
	"github.com/shopspring/decimal"
)

const DefaultInflationPluginTimeout = 60 * time.Second

// gapStart is the earliest month for which we attempt to fetch inflation data.
var gapStart = time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)

// RunWorker processes inflation fetch cycles triggered via the trigger channel.
// It blocks until ctx is cancelled. Each signal on trigger runs one cycle;
// rapid signals are debounced (buffered channel of size 1).
func RunWorker(ctx context.Context, database db.DB, registry *Registry, tel db.TelemetryDB, log *slog.Logger, trigger <-chan struct{}, workers *worker.Registry) {
	if tel == nil {
		tel = db.NopTelemetry{}
	}
	const name = "inflation_fetcher"
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
			runID := tel.StartRun(ctx, db.TelemetryRun{Kind: db.TelemetryRunInflationCycle})
			outcome := db.TelemetryOutcomeSuccess
			if err := runCycle(ctx, database, registry, log, workers); err != nil {
				outcome = db.TelemetryOutcomeFailed
			}
			tel.EndRun(ctx, runID, outcome)
		}
	}
}

// pluginEntry pairs a registered plugin with its config row.
type pluginEntry struct {
	id     string
	plugin Plugin
	config []byte
}

func runCycle(ctx context.Context, database db.DB, registry *Registry, log *slog.Logger, workers *worker.Registry) error {
	const name = "inflation_fetcher"
	defer func() {
		if workers != nil {
			workers.SetIdle(name)
			workers.CycleDone(name)
		}
	}()

	currencies, err := database.DistinctDisplayCurrencies(ctx)
	if err != nil {
		if log != nil {
			log.ErrorContext(ctx, "inflation fetch: display currencies", "err", err)
		}
		return err
	}
	if len(currencies) == 0 {
		if log != nil {
			log.InfoContext(ctx, "inflation fetch: no display currencies configured, skipping")
		}
		return nil
	}

	configs, err := database.ListEnabledPluginConfigs(ctx, db.PluginCategoryInflation)
	if err != nil {
		if log != nil {
			log.ErrorContext(ctx, "inflation fetch: list configs", "err", err)
		}
		return err
	}
	if len(configs) == 0 {
		if log != nil {
			log.InfoContext(ctx, "inflation fetch: no enabled inflation plugins, skipping")
		}
		return nil
	}

	var plugins []pluginEntry
	for _, cfg := range configs {
		p := registry.Get(cfg.PluginID)
		if p == nil {
			if log != nil {
				log.WarnContext(ctx, "inflation fetch: plugin not in registry", "plugin", cfg.PluginID)
			}
			continue
		}
		plugins = append(plugins, pluginEntry{
			id:     cfg.PluginID,
			plugin: p,
			config: cfg.Config,
		})
	}
	if len(plugins) == 0 {
		return nil
	}

	if workers != nil {
		workers.SetRunning(name, fmt.Sprintf("Fetching inflation for %d currencies", len(currencies)))
	}

	processCurrencies(ctx, database, plugins, currencies, log)
	return nil
}

func processCurrencies(ctx context.Context, database db.DB, plugins []pluginEntry, currencies []string, log *slog.Logger) {
	// Current month's 1st: we fetch up to (but not including) this month since
	// current month data may not be available yet.
	now := time.Now().UTC()
	endMonth := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)

	for _, code := range currencies {
		if ctx.Err() != nil {
			return
		}
		processCurrency(ctx, database, plugins, code, endMonth, log)
	}
}

func processCurrency(ctx context.Context, database db.DB, plugins []pluginEntry, code string, endMonth time.Time, log *slog.Logger) {
	for _, pe := range plugins {
		if !pluginAcceptsCurrency(pe.plugin, code) {
			continue
		}

		coverage, err := database.InflationCoverage(ctx, code)
		if err != nil {
			if log != nil {
				log.ErrorContext(ctx, "inflation fetch: coverage", "currency", code, "err", err)
			}
			return
		}

		gapFrom, gapTo := computeGapRange(coverage, endMonth)
		if !gapFrom.Before(gapTo) {
			return // no gaps
		}

		callCtx, callCancel := context.WithTimeout(ctx, pluginutil.TimeoutFromConfig(pe.config, DefaultInflationPluginTimeout))
		result, err := pe.plugin.FetchInflation(callCtx, pe.config, code, gapFrom, gapTo)
		callCancel()

		if err != nil {
			if err == ErrNoData {
				continue // try next plugin
			}
			if log != nil {
				log.WarnContext(ctx, "inflation fetch: plugin error",
					"plugin", pe.id, "currency", code, "err", err)
			}
			continue
		}

		if len(result.Indices) == 0 {
			continue
		}

		indices := toDBIndices(code, pe.id, result.Indices)
		if err := database.UpsertInflationIndices(ctx, indices, nil); err != nil {
			if log != nil {
				log.ErrorContext(ctx, "inflation fetch: upsert",
					"currency", code, "err", err)
			}
			return
		}
		if log != nil {
			log.InfoContext(ctx, "inflation fetch: stored indices",
				"plugin", pe.id, "currency", code, "count", len(indices))
		}
		return // success, stop trying plugins for this currency
	}
	if log != nil {
		log.InfoContext(ctx, "inflation fetch: no plugin supports currency", "currency", code)
	}
}

// pluginAcceptsCurrency checks if a plugin supports the given currency.
//
// On the family, as every currency comparison is: a plugin publishing an index
// for GBP publishes it for the line quoted in pence, the two being one currency
// under a different unit prefix (adr/0068).
func pluginAcceptsCurrency(p Plugin, code string) bool {
	for _, c := range p.SupportedCurrencies() {
		if currency.Same(c, code) {
			return true
		}
	}
	return false
}

// computeGapRange determines the date range [from, to) of missing months.
// It finds the earliest gap from gapStart to endMonth given existing coverage.
func computeGapRange(coverage []time.Time, endMonth time.Time) (time.Time, time.Time) {
	if len(coverage) == 0 {
		return gapStart, endMonth
	}

	// Build a set of covered months for quick lookup.
	covered := make(map[time.Time]bool, len(coverage))
	for _, m := range coverage {
		covered[m] = true
	}

	// Find first missing month.
	var firstMissing time.Time
	found := false
	for m := gapStart; m.Before(endMonth); m = m.AddDate(0, 1, 0) {
		if !covered[m] {
			if !found {
				firstMissing = m
				found = true
			}
		}
	}
	if !found {
		return endMonth, endMonth // no gaps
	}
	return firstMissing, endMonth
}

func toDBIndices(currency, provider string, indices []MonthlyIndex) []db.InflationIndex {
	out := make([]db.InflationIndex, len(indices))
	for i, idx := range indices {
		out[i] = db.InflationIndex{
			Currency: currency,
			Month:    idx.Month,
			// The plugin seam: ONS publishes index values as JSON numbers, so
			// this is where one becomes exact. Matches how the price fetcher
			// converts a provider bar.
			IndexValue:   decimal.NewFromFloat(idx.IndexValue),
			BaseYear:     idx.BaseYear,
			DataProvider: provider,
		}
	}
	return out
}
