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

	processGaps(ctx, database, plugins, allGaps, instByID, blocked, log)
}

// processGaps iterates instrument gaps and fetches prices from matching plugins.
func processGaps(ctx context.Context, database db.DB, plugins []pluginEntry, gaps []db.InstrumentDateRanges, instByID map[string]*db.InstrumentRow, blocked map[string]map[string]bool, log *slog.Logger) {
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
			if len(ids) == 0 {
				continue
			}

			pfIDs := toPricefetcherIDs(ids)
			allOK := true
			for _, gap := range ig.Ranges {
				gap := gap // copy for truncation
				if pe.maxHistDays != nil && *pe.maxHistDays > 0 {
					cutoff := time.Now().UTC().Truncate(db.Day).AddDate(0, 0, -*pe.maxHistDays)
					if !gap.To.After(cutoff) {
						continue // entire gap older than history limit
					}
					if gap.From.Before(cutoff) {
						gap.From = cutoff
					}
				}
				var assetClass string
				if inst.AssetClass != nil {
					assetClass = *inst.AssetClass
				}
					callCtx, callCancel := context.WithTimeout(ctx, pluginutil.TimeoutFromConfig(pe.config, DefaultPricePluginTimeout))
				result, err := pe.plugin.FetchPrices(callCtx, pe.config, pfIDs, assetClass, gap.From, gap.To)
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
						continue
					}
					if log != nil {
						log.WarnContext(ctx, "price fetch: plugin error",
							"plugin", pe.id, "instrument", ig.InstrumentID, "err", err)
					}
					allOK = false
					break
				}

				prices := barsToEODPrices(ig.InstrumentID, pe.id, result.Bars)
				if err := database.UpsertPricesWithFill(ctx, ig.InstrumentID, pe.id, prices, gap.From, gap.To); err != nil {
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

func barsToEODPrices(instrumentID, provider string, bars []DailyBar) []db.EODPrice {
	out := make([]db.EODPrice, len(bars))
	for i, b := range bars {
		out[i] = db.EODPrice{
			InstrumentID: instrumentID,
			PriceDate:    b.Date,
			Open:         b.Open,
			High:         b.High,
			Low:          b.Low,
			Close:        b.Close,
			Volume:       b.Volume,
			DataProvider: provider,
		}
	}
	return out
}

