package pricefetcher

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/leedenison/portfoliodb/server/db"
	"github.com/leedenison/portfoliodb/server/pluginutil"
	"github.com/leedenison/portfoliodb/server/telemetry"
	"github.com/leedenison/portfoliodb/server/worker"
)

const DefaultPricePluginTimeout = 60 * time.Second

// RunWorker processes price fetch cycles triggered via the trigger channel.
// It blocks until ctx is cancelled. Each signal on trigger runs one cycle;
// rapid signals are debounced (buffered channel of size 1).
func RunWorker(ctx context.Context, database db.DB, registry *Registry, counter telemetry.CounterIncrementer, log *slog.Logger, trigger <-chan struct{}, workers *worker.Registry) {
	const name = "price_fetcher"
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
	id          string
	plugin      Plugin
	config      []byte
	maxHistDays *int
}

func runCycle(ctx context.Context, database db.DB, registry *Registry, counter telemetry.CounterIncrementer, log *slog.Logger, workers *worker.Registry) {
	const name = "price_fetcher"
	if counter != nil {
		counter.Incr(ctx, "price_fetcher.cycles")
	}
	defer func() {
		if workers != nil {
			workers.SetIdle(name)
		}
	}()

	opts := db.HeldRangesOpts{ExtendToToday: true}

	gaps, err := database.PriceGaps(ctx, opts)
	if err != nil {
		if log != nil {
			log.ErrorContext(ctx, "price fetch: gaps", "err", err)
		}
		return
	}

	fxGaps, err := database.FXGaps(ctx, opts)
	if err != nil {
		if log != nil {
			log.ErrorContext(ctx, "price fetch: fx gaps", "err", err)
		}
		return
	}

	allGaps := make([]db.InstrumentDateRanges, 0, len(gaps)+len(fxGaps))
	allGaps = append(allGaps, gaps...)
	allGaps = append(allGaps, fxGaps...)
	if len(allGaps) == 0 {
		return
	}

	if workers != nil {
		workers.SetRunning(name, fmt.Sprintf("Fetching prices for %d instruments", len(allGaps)))
	}

	configs, err := database.ListEnabledPluginConfigs(ctx, db.PluginCategoryPrice)
	if err != nil {
		if log != nil {
			log.ErrorContext(ctx, "price fetch: list configs", "err", err)
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
			id:          cfg.PluginID,
			plugin:      p,
			config:      cfg.Config,
			maxHistDays: cfg.MaxHistoryDays,
		})
	}
	if len(plugins) == 0 {
		return
	}

	// Batch-load blocked (instrument, plugin) pairs.
	instIDs := extractInstrumentIDs(allGaps)
	blocked, err := database.BlockedPluginsForInstruments(ctx, instIDs)
	if err != nil {
		if log != nil {
			log.ErrorContext(ctx, "price fetch: load blocks", "err", err)
		}
		return
	}

	// Batch-load all instruments for the gaps
	instRows, err := database.ListInstrumentsByIDs(ctx, instIDs)
	if err != nil {
		if log != nil {
			log.ErrorContext(ctx, "price fetch: load instruments", "err", err)
		}
		return
	}
	instByID := make(map[string]*db.InstrumentRow, len(instRows))
	for _, r := range instRows {
		instByID[r.ID] = r
	}

	// Per-plugin coverage, so a range one plugin has already answered for is not
	// put to it again -- including ranges it answered with nothing.
	coverage, err := database.PriceCoverageByPlugin(ctx, instIDs)
	if err != nil {
		if log != nil {
			log.ErrorContext(ctx, "price fetch: load coverage", "err", err)
		}
		return
	}

	processGaps(ctx, database, plugins, allGaps, instByID, blocked, coverage, log)
}

// processGaps iterates instrument gaps and fetches prices from matching plugins.
func processGaps(ctx context.Context, database db.DB, plugins []pluginEntry, gaps []db.InstrumentDateRanges, instByID map[string]*db.InstrumentRow, blocked map[string]map[string]bool, coverage map[string]map[string][]db.DateRange, log *slog.Logger) {
	// One fetch time for the whole cycle, so every row a back-adjusting plugin
	// returns shares the same share count basis.
	now := time.Now()
	for _, ig := range gaps {
		if ctx.Err() != nil {
			return
		}
		inst := instByID[ig.InstrumentID]
		if inst == nil {
			if log != nil {
				log.WarnContext(ctx, "price fetch: instrument not found", "id", ig.InstrumentID)
			}
			continue
		}

		fetchedByPlugin := false
		for _, pe := range plugins {
			if !pluginutil.PluginAccepts(pe.plugin.AcceptableAssetClasses(), pe.plugin.AcceptableExchanges(), pe.plugin.AcceptableCurrencies(), inst) {
				continue
			}
			if blocked[ig.InstrumentID][pe.id] {
				continue
			}
			ids := pluginutil.FilterIdentifiers(pe.plugin.SupportedIdentifierTypes(), inst.Identifiers)
			// Merge provider-specific identifiers for this plugin.
			for _, pi := range inst.ProviderIdentifiers {
				if pi.Provider == pe.id {
					ids = append(ids, db.IdentifierInput{Type: pi.Type, Domain: pi.Domain, Value: pi.Value})
				}
			}
			ids = pluginutil.FilterIdentifiers(pe.plugin.SupportedIdentifierTypes(), ids)
			if len(ids) == 0 {
				continue
			}

			// Ranges this plugin has already answered for are not put to it
			// again, whether it returned a series or nothing at all.
			outstanding := db.SubtractRanges(ig.Ranges, coverage[ig.InstrumentID][pe.id])
			if len(outstanding) == 0 {
				// Nothing left to ask this one. It has not handled the
				// instrument, so the next plugin still gets its turn.
				continue
			}

			pfIDs := toPricefetcherIDs(ids)
			allOK := true
			for _, gap := range outstanding {
				gap := gap // copy for truncation
				if pe.maxHistDays != nil && *pe.maxHistDays > 0 {
					cutoff := time.Now().UTC().Truncate(db.Day).AddDate(0, 0, -*pe.maxHistDays)
					if !gap.Before.After(cutoff) {
						// The plugin cannot reach this far back and never will,
						// so record it as covered by this plugin rather than
						// rediscovering the same gap every cycle. Other plugins
						// keep their own coverage and are still offered it.
						coverRange(ctx, database, ig.InstrumentID, pe.id, gap, "history limit", log)
						continue
					}
					if gap.From.Before(cutoff) {
						// The unreachable head is settled for this plugin even
						// though the tail is about to be fetched.
						coverRange(ctx, database, ig.InstrumentID, pe.id,
							db.DateRange{From: gap.From, Before: cutoff}, "history limit", log)
						gap.From = cutoff
					}
				}
				var assetClass string
				if inst.AssetClass != nil {
					assetClass = *inst.AssetClass
				}
				callCtx, callCancel := context.WithTimeout(ctx, pluginutil.TimeoutFromConfig(pe.config, DefaultPricePluginTimeout))
				result, err := pe.plugin.FetchPrices(callCtx, pe.config, pfIDs, assetClass, gap.From, gap.Before)
				callCancel()
				if err != nil {
					var permErr *ErrPermanent
					if errors.As(err, &permErr) {
						_ = database.CreatePriceFetchBlock(ctx, ig.InstrumentID, pe.id, permErr.Reason)
						if log != nil {
							log.WarnContext(ctx, "price fetch: permanent block",
								"plugin", pe.id, "instrument", ig.InstrumentID, "reason", permErr.Reason)
						}
						allOK = false
						break // skip remaining ranges, try next plugin
					}
					if err == ErrNoData {
						// "Nothing here" is an answer, not a failure to answer:
						// record it so a pre-IPO, delisted or untraded range is
						// settled for this plugin instead of being asked about
						// on every cycle forever.
						coverRange(ctx, database, ig.InstrumentID, pe.id, gap, "no data", log)
						continue
					}
					if log != nil {
						log.WarnContext(ctx, "price fetch: plugin error",
							"plugin", pe.id, "instrument", ig.InstrumentID, "err", err)
					}
					allOK = false
					break
				}

				prices := barsToEODPrices(ig.InstrumentID, pe.id, result.Bars, result.ShareCountBasis, now)
				if err := database.UpsertPricesForRange(ctx, ig.InstrumentID, pe.id, prices, gap.From, gap.Before, nil); err != nil {
					if log != nil {
						log.ErrorContext(ctx, "price fetch: upsert", "instrument", ig.InstrumentID, "err", err)
					}
					allOK = false
					break
				}
			}
			if allOK {
				fetchedByPlugin = true
				break
			}
			// On error, try next plugin for this instrument.
		}
		_ = fetchedByPlugin
	}
}

// coverRange records that a plugin has settled a range without supplying bars.
// A failure to record is logged rather than propagated: the fetch itself
// succeeded, and the only cost is asking again next cycle.
func coverRange(ctx context.Context, database db.DB, instrumentID, pluginID string, r db.DateRange, reason string, log *slog.Logger) {
	if !r.Before.After(r.From) {
		return
	}
	if err := database.UpsertPricesForRange(ctx, instrumentID, pluginID, nil, r.From, r.Before, nil); err != nil && log != nil {
		log.WarnContext(ctx, "price fetch: record empty coverage",
			"plugin", pluginID, "instrument", instrumentID, "reason", reason, "err", err)
	}
}

// extractInstrumentIDs returns unique instrument IDs from gaps.
func extractInstrumentIDs(gaps []db.InstrumentDateRanges) []string {
	out := make([]string, len(gaps))
	for i, g := range gaps {
		out[i] = g.InstrumentID
	}
	return out
}

func toPricefetcherIDs(ids []db.IdentifierInput) []Identifier {
	out := make([]Identifier, len(ids))
	for i, id := range ids {
		out[i] = Identifier{Type: id.Type, Domain: id.Domain, Value: id.Value}
	}
	return out
}

// barsToEODPrices converts plugin bars to price rows, resolving the plugin's
// declared denomination to a per-row share count basis. An as-traded bar is
// denominated in the share count current on its own date; a back-adjusted
// series is denominated in the share count current when we fetched it.
func barsToEODPrices(instrumentID, provider string, bars []DailyBar, basis ShareCountBasis, fetchedAt time.Time) []db.EODPrice {
	out := make([]db.EODPrice, len(bars))
	for i, b := range bars {
		scb := b.Date
		if basis == AsOfFetch {
			scb = fetchedAt
		}
		out[i] = db.EODPrice{
			InstrumentID:    instrumentID,
			PriceDate:       b.Date,
			Open:            b.Open,
			High:            b.High,
			Low:             b.Low,
			Close:           b.Close,
			Volume:          b.Volume,
			AdjustedClose:   b.AdjustedClose,
			DataProvider:    provider,
			ShareCountBasis: &scb,
		}
	}
	return out
}
