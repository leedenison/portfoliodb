package pricefetcher

import (
	"context"
	"errors"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/leedenison/portfoliodb/server/db"
	"github.com/leedenison/portfoliodb/server/telemetry"
)

// RunWorker processes price fetch cycles triggered via the trigger channel.
// It blocks until ctx is cancelled. Each signal on trigger runs one cycle;
// rapid signals are debounced (buffered channel of size 1).
func RunWorker(ctx context.Context, database db.DB, registry *Registry, counter telemetry.CounterIncrementer, log *slog.Logger, trigger <-chan struct{}) {
	for {
		select {
		case <-ctx.Done():
			return
		case _, ok := <-trigger:
			if !ok {
				return
			}
			runCycle(ctx, database, registry, counter, log)
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

func runCycle(ctx context.Context, database db.DB, registry *Registry, counter telemetry.CounterIncrementer, log *slog.Logger) {
	opts := db.HeldRangesOpts{ExtendToToday: true, LookbackDays: 5}

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

	processGaps(ctx, database, plugins, allGaps, blocked, log)
}

// processGaps iterates instrument gaps and fetches prices from matching plugins.
func processGaps(ctx context.Context, database db.DB, plugins []pluginEntry, gaps []db.InstrumentDateRanges, blocked map[string]map[string]bool, log *slog.Logger) {
	for _, ig := range gaps {
		if ctx.Err() != nil {
			return
		}
		inst, err := database.GetInstrument(ctx, ig.InstrumentID)
		if err != nil || inst == nil {
			if log != nil {
				log.WarnContext(ctx, "price fetch: get instrument", "id", ig.InstrumentID, "err", err)
			}
			continue
		}

		fetchedByPlugin := false
		for _, pe := range plugins {
			if !pluginAccepts(pe.plugin, inst) {
				continue
			}
			if blocked[ig.InstrumentID][pe.id] {
				continue
			}
			ids := filterIdentifiers(pe.plugin.SupportedIdentifierTypes(), inst.Identifiers)
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
				result, err := pe.plugin.FetchPrices(ctx, pe.config, pfIDs, assetClass, gap.From, gap.To)
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

				// Look up seed for LOCF before the gap range.
				seedClose, seedProvider, hasSeed, seedErr := database.LastRealPrice(ctx, ig.InstrumentID, gap.From)
				if seedErr != nil && log != nil {
					log.WarnContext(ctx, "price fetch: seed lookup", "instrument", ig.InstrumentID, "err", seedErr)
				}

				prices := fillGaps(ig.InstrumentID, pe.id, result.Bars, gap.From, gap.To, seedClose, seedProvider, hasSeed)
				if len(prices) > 0 {
					if err := database.UpsertPrices(ctx, prices); err != nil {
						if log != nil {
							log.ErrorContext(ctx, "price fetch: upsert", "instrument", ig.InstrumentID, "err", err)
						}
						allOK = false
						break
					}
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

// fillGaps generates LOCF-filled prices for every calendar day in [from, to).
// Real bars are emitted as-is; dates without a bar get a synthetic price
// carrying forward the last known close. If no seed exists and no bars
// precede a date, that date is skipped (will be reported as unpriced).
func fillGaps(instrumentID, provider string, bars []DailyBar, from, to time.Time, seedClose float64, seedProvider string, hasSeed bool) []db.EODPrice {
	if len(bars) == 0 && !hasSeed {
		return nil
	}

	// Sort bars by date and build lookup map.
	sort.Slice(bars, func(i, j int) bool { return bars[i].Date.Before(bars[j].Date) })
	barMap := make(map[time.Time]DailyBar, len(bars))
	for _, b := range bars {
		barMap[b.Date.Truncate(db.Day)] = b
	}

	lastClose := seedClose
	lastProvider := seedProvider
	haveClose := hasSeed

	days := int(to.Sub(from) / db.Day)
	out := make([]db.EODPrice, 0, days)

	for d := from; d.Before(to); d = d.Add(db.Day) {
		day := d.Truncate(db.Day)
		if b, ok := barMap[day]; ok {
			// Real bar from provider.
			out = append(out, db.EODPrice{
				InstrumentID: instrumentID,
				PriceDate:    day,
				Open:         b.Open,
				High:         b.High,
				Low:          b.Low,
				Close:        b.Close,
				Volume:       b.Volume,
				DataProvider: provider,
				Synthetic:    false,
			})
			lastClose = b.Close
			lastProvider = provider
			haveClose = true
		} else if haveClose {
			// Synthetic forward-fill.
			out = append(out, db.EODPrice{
				InstrumentID: instrumentID,
				PriceDate:    day,
				Close:        lastClose,
				DataProvider: lastProvider,
				Synthetic:    true,
			})
		}
		// else: no seed yet, skip this date.
	}
	return out
}

// extractInstrumentIDs returns unique instrument IDs from gaps.
func extractInstrumentIDs(gaps []db.InstrumentDateRanges) []string {
	out := make([]string, len(gaps))
	for i, g := range gaps {
		out[i] = g.InstrumentID
	}
	return out
}

// pluginAccepts checks whether a plugin can handle the given instrument
// based on asset class, exchange, and currency filters.
func pluginAccepts(p Plugin, inst *db.InstrumentRow) bool {
	if ac := p.AcceptableAssetClasses(); len(ac) > 0 && inst.AssetClass != nil && *inst.AssetClass != "" {
		if !ac[*inst.AssetClass] {
			return false
		}
	}
	if ex := p.AcceptableExchanges(); len(ex) > 0 && inst.ExchangeMIC != nil && *inst.ExchangeMIC != "" {
		if !ex[*inst.ExchangeMIC] {
			return false
		}
	}
	if cu := p.AcceptableCurrencies(); len(cu) > 0 && inst.Currency != nil && *inst.Currency != "" {
		if !cu[strings.ToUpper(*inst.Currency)] {
			return false
		}
	}
	return true
}

// filterIdentifiers returns identifiers whose type is in the supported set.
func filterIdentifiers(supported []string, ids []db.IdentifierInput) []db.IdentifierInput {
	set := make(map[string]bool, len(supported))
	for _, t := range supported {
		set[t] = true
	}
	var out []db.IdentifierInput
	for _, id := range ids {
		if set[id.Type] {
			out = append(out, id)
		}
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

// Trigger sends a non-blocking signal on a price trigger channel.
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
