package inflationfetcher

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/leedenison/portfoliodb/server/db"
	"github.com/leedenison/portfoliodb/server/telemetry"
	"github.com/leedenison/portfoliodb/server/worker"
)

const DefaultInflationPluginTimeout = 60 * time.Second

// gapStart is the earliest month for which we attempt to fetch inflation data.
var gapStart = time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)

// RunWorker processes inflation fetch cycles triggered via the trigger channel.
// It blocks until ctx is cancelled. Each signal on trigger runs one cycle;
// rapid signals are debounced (buffered channel of size 1).
func RunWorker(ctx context.Context, database db.DB, registry *Registry, counter telemetry.CounterIncrementer, log *slog.Logger, trigger <-chan struct{}, workers *worker.Registry) {
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
			runCycle(ctx, database, registry, counter, log, workers)
		}
	}
}

// pluginEntry pairs a registered plugin with its config row.
type pluginEntry struct {
	id     string
	plugin Plugin
	config []byte
}

func runCycle(ctx context.Context, database db.DB, registry *Registry, counter telemetry.CounterIncrementer, log *slog.Logger, workers *worker.Registry) {
	const name = "inflation_fetcher"
	if counter != nil {
		counter.Incr(ctx, "inflation_fetcher.cycles")
	}
	defer func() {
		if workers != nil {
			workers.SetIdle(name)
		}
	}()

	currencies, err := database.DistinctDisplayCurrencies(ctx)
	if err != nil {
		if log != nil {
			log.ErrorContext(ctx, "inflation fetch: display currencies", "err", err)
		}
		return
	}
	if len(currencies) == 0 {
		return
	}

	configs, err := database.ListEnabledPluginConfigs(ctx, db.PluginCategoryInflation)
	if err != nil {
		if log != nil {
			log.ErrorContext(ctx, "inflation fetch: list configs", "err", err)
		}
		return
	}
	if len(configs) == 0 {
		return
	}

	var plugins []pluginEntry
	for _, cfg := range configs {
		p := registry.Get(cfg.PluginID)
		if p == nil {
			continue
		}
		plugins = append(plugins, pluginEntry{
			id:     cfg.PluginID,
			plugin: p,
			config: cfg.Config,
		})
	}
	if len(plugins) == 0 {
		return
	}

	if workers != nil {
		workers.SetRunning(name, fmt.Sprintf("Fetching inflation for %d currencies", len(currencies)))
	}

	processCurrencies(ctx, database, plugins, currencies, log)
}

func processCurrencies(ctx context.Context, database db.DB, plugins []pluginEntry, currencies []string, log *slog.Logger) {
	// Current month's 1st: we fetch up to (but not including) this month since
	// current month data may not be available yet.
	now := time.Now().UTC()
	endMonth := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)

	for _, currency := range currencies {
		if ctx.Err() != nil {
			return
		}
		processCurrency(ctx, database, plugins, currency, endMonth, log)
	}
}

func processCurrency(ctx context.Context, database db.DB, plugins []pluginEntry, currency string, endMonth time.Time, log *slog.Logger) {
	for _, pe := range plugins {
		if !pluginAcceptsCurrency(pe.plugin, currency) {
			continue
		}

		coverage, err := database.InflationCoverage(ctx, currency)
		if err != nil {
			if log != nil {
				log.ErrorContext(ctx, "inflation fetch: coverage", "currency", currency, "err", err)
			}
			return
		}

		gapFrom, gapTo := computeGapRange(coverage, endMonth)
		if !gapFrom.Before(gapTo) {
			return // no gaps
		}

		callCtx, callCancel := context.WithTimeout(ctx, timeoutFromConfig(pe.config))
		result, err := pe.plugin.FetchInflation(callCtx, pe.config, currency, gapFrom, gapTo)
		callCancel()

		if err != nil {
			if err == ErrNoData {
				continue // try next plugin
			}
			if log != nil {
				log.WarnContext(ctx, "inflation fetch: plugin error",
					"plugin", pe.id, "currency", currency, "err", err)
			}
			continue
		}

		if len(result.Indices) == 0 {
			continue
		}

		indices := toDBIndices(currency, pe.id, result.Indices)
		if err := database.UpsertInflationIndices(ctx, indices); err != nil {
			if log != nil {
				log.ErrorContext(ctx, "inflation fetch: upsert",
					"currency", currency, "err", err)
			}
		}
		return // success, stop trying plugins for this currency
	}
}

// pluginAcceptsCurrency checks if a plugin supports the given currency.
func pluginAcceptsCurrency(p Plugin, currency string) bool {
	for _, c := range p.SupportedCurrencies() {
		if strings.EqualFold(c, currency) {
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
			Currency:     currency,
			Month:        idx.Month,
			IndexValue:   idx.IndexValue,
			BaseYear:     idx.BaseYear,
			DataProvider: provider,
		}
	}
	return out
}

type pluginConfigJSON struct {
	TimeoutSeconds *int `json:"timeout_seconds"`
}

// timeoutFromConfig parses timeout_seconds from plugin config JSON; defaults to DefaultInflationPluginTimeout.
func timeoutFromConfig(config []byte) time.Duration {
	if len(config) == 0 {
		return DefaultInflationPluginTimeout
	}
	var c pluginConfigJSON
	if err := json.Unmarshal(config, &c); err != nil {
		return DefaultInflationPluginTimeout
	}
	if c.TimeoutSeconds == nil || *c.TimeoutSeconds <= 0 {
		return DefaultInflationPluginTimeout
	}
	return time.Duration(*c.TimeoutSeconds) * time.Second
}

// Trigger sends a non-blocking signal on an inflation trigger channel.
// Safe to call with a nil channel.
func Trigger(ch chan<- struct{}) {
	if ch == nil {
		return
	}
	select {
	case ch <- struct{}{}:
	default:
	}
}
