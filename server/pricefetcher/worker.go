package pricefetcher

import (
	"context"
	"log/slog"
	"strings"

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

func runCycle(ctx context.Context, database db.DB, registry *Registry, counter telemetry.CounterIncrementer, log *slog.Logger) {
	gaps, err := database.PriceGaps(ctx, db.HeldRangesOpts{ExtendToToday: true, LookbackDays: 5})
	if err != nil {
		if log != nil {
			log.ErrorContext(ctx, "price fetch: gaps", "err", err)
		}
		return
	}
	if len(gaps) == 0 {
		return
	}

	configs, err := database.ListEnabledPricePluginConfigs(ctx)
	if err != nil {
		if log != nil {
			log.ErrorContext(ctx, "price fetch: list configs", "err", err)
		}
		return
	}
	if len(configs) == 0 {
		return
	}

	// Build plugin list with their config, ordered by precedence (highest first).
	type pluginEntry struct {
		id     string
		plugin Plugin
		config []byte
	}
	var plugins []pluginEntry
	for _, cfg := range configs {
		p := registry.Get(cfg.PluginID)
		if p == nil {
			continue
		}
		plugins = append(plugins, pluginEntry{id: cfg.PluginID, plugin: p, config: cfg.Config})
	}
	if len(plugins) == 0 {
		return
	}

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
			ids := filterIdentifiers(pe.plugin.SupportedIdentifierTypes(), inst.Identifiers)
			if len(ids) == 0 {
				continue
			}

			pfIDs := toPricefetcherIDs(ids)
			allOK := true
			for _, gap := range ig.Ranges {
				result, err := pe.plugin.FetchPrices(ctx, pe.config, pfIDs, inst.AssetClass, gap.From, gap.To)
				if err != nil {
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
				if len(result.Bars) > 0 {
					prices := barsToEODPrices(ig.InstrumentID, pe.id, result.Bars)
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

// pluginAccepts checks whether a plugin can handle the given instrument
// based on asset class, exchange, and currency filters.
func pluginAccepts(p Plugin, inst *db.InstrumentRow) bool {
	if ac := p.AcceptableAssetClasses(); len(ac) > 0 && inst.AssetClass != "" {
		if !ac[inst.AssetClass] {
			return false
		}
	}
	if ex := p.AcceptableExchanges(); len(ex) > 0 && inst.Exchange != "" {
		if !ex[inst.Exchange] {
			return false
		}
	}
	if cu := p.AcceptableCurrencies(); len(cu) > 0 && inst.Currency != "" {
		if !cu[strings.ToUpper(inst.Currency)] {
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
