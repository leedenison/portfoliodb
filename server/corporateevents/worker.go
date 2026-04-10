package corporateevents

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/leedenison/portfoliodb/server/db"
	"github.com/leedenison/portfoliodb/server/telemetry"
	"github.com/leedenison/portfoliodb/server/worker"
)

const (
	// DefaultPluginTimeout bounds a single FetchEvents call when the plugin
	// config does not override timeout_seconds.
	DefaultPluginTimeout = 60 * time.Second

	// DefaultLookaheadDays is the number of days past today included in the
	// required fetch range, so declared-but-unpaid dividends are picked up
	// as soon as the provider lists them.
	DefaultLookaheadDays = 30
)

// RunWorker processes corporate event fetch cycles triggered via the trigger
// channel. It blocks until ctx is cancelled. Each signal on trigger runs one
// cycle; rapid signals are debounced via a buffered channel of size 1.
func RunWorker(ctx context.Context, database db.DB, registry *Registry, counter telemetry.CounterIncrementer, log *slog.Logger, trigger <-chan struct{}, workers *worker.Registry) {
	const name = "corporate_event_fetcher"
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
	const name = "corporate_event_fetcher"
	if counter != nil {
		counter.Incr(ctx, "corporate_event_fetcher.cycles")
	}
	defer func() {
		if workers != nil {
			workers.SetIdle(name)
		}
	}()

	held, err := database.HeldEventBearingInstruments(ctx)
	if err != nil {
		if log != nil {
			log.ErrorContext(ctx, "corporate event fetch: held instruments", "err", err)
		}
		return
	}
	if len(held) == 0 {
		return
	}

	configs, err := database.ListEnabledPluginConfigs(ctx, db.PluginCategoryCorporateEvent)
	if err != nil {
		if log != nil {
			log.ErrorContext(ctx, "corporate event fetch: list configs", "err", err)
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
		plugins = append(plugins, pluginEntry{id: cfg.PluginID, plugin: p, config: cfg.Config})
	}
	if len(plugins) == 0 {
		return
	}

	if workers != nil {
		workers.SetRunning(name, fmt.Sprintf("Fetching corporate events for %d instruments", len(held)))
	}

	instIDs := make([]string, len(held))
	for i, h := range held {
		instIDs[i] = h.InstrumentID
	}
	blocked, err := database.BlockedCorporateEventPluginsForInstruments(ctx, instIDs)
	if err != nil {
		if log != nil {
			log.ErrorContext(ctx, "corporate event fetch: load blocks", "err", err)
		}
		return
	}
	instRows, err := database.ListInstrumentsByIDs(ctx, instIDs)
	if err != nil {
		if log != nil {
			log.ErrorContext(ctx, "corporate event fetch: load instruments", "err", err)
		}
		return
	}
	instByID := make(map[string]*db.InstrumentRow, len(instRows))
	for _, r := range instRows {
		instByID[r.ID] = r
	}
	coverage, err := database.ListCorporateEventCoverage(ctx, instIDs)
	if err != nil {
		if log != nil {
			log.ErrorContext(ctx, "corporate event fetch: load coverage", "err", err)
		}
		return
	}
	coverageByInst := make(map[string][]db.CorporateEventCoverage, len(coverage))
	for _, c := range coverage {
		coverageByInst[c.InstrumentID] = append(coverageByInst[c.InstrumentID], c)
	}

	endDate := time.Now().UTC().Truncate(db.Day).AddDate(0, 0, DefaultLookaheadDays)
	for _, h := range held {
		if ctx.Err() != nil {
			return
		}
		inst := instByID[h.InstrumentID]
		if inst == nil {
			continue
		}
		processInstrument(ctx, database, plugins, inst, h.EarliestTxDate, endDate,
			coverageByInst[h.InstrumentID], blocked[h.InstrumentID], log)
	}
}

// processInstrument fills the missing date intervals for one instrument by
// walking plugins in precedence order. The required range is the closed
// interval [earliestTxDate, endDate]. Coverage rows for the instrument are
// subtracted to produce the missing intervals; each missing interval is
// offered to plugins one at a time. The first plugin that returns a
// successful response (including an empty result) records coverage and
// claims that interval; lower-precedence plugins are not consulted for it.
func processInstrument(ctx context.Context, database db.DB, plugins []pluginEntry, inst *db.InstrumentRow, earliestTxDate, endDate time.Time, coverage []db.CorporateEventCoverage, blocked map[string]bool, log *slog.Logger) {
	missing := computeMissingIntervals(earliestTxDate, endDate, coverage)
	if len(missing) == 0 {
		return
	}

	var assetClass string
	if inst.AssetClass != nil {
		assetClass = *inst.AssetClass
	}

	splitsLanded := false
	for _, gap := range missing {
		filled := false
		for _, pe := range plugins {
			if blocked[pe.id] {
				continue
			}
			if !pluginAccepts(pe.plugin, inst) {
				continue
			}
			ids := filterIdentifiers(pe.plugin.SupportedIdentifierTypes(), inst.Identifiers)
			if len(ids) == 0 {
				continue
			}

			callCtx, callCancel := context.WithTimeout(ctx, timeoutFromConfig(pe.config))
			result, err := pe.plugin.FetchEvents(callCtx, pe.config, toPluginIdentifiers(ids), assetClass, gap.From, gap.To)
			callCancel()
			if err != nil {
				var permErr *ErrPermanent
				if errors.As(err, &permErr) {
					_ = database.CreateCorporateEventFetchBlock(ctx, inst.ID, pe.id, permErr.Reason)
					if log != nil {
						log.WarnContext(ctx, "corporate event fetch: permanent block",
							"plugin", pe.id, "instrument", inst.ID, "reason", permErr.Reason)
					}
					continue
				}
				if errors.Is(err, ErrNoData) {
					continue
				}
				// Transient or unknown error: leave the gap untouched and try
				// the next plugin (which may not have the same problem).
				if log != nil {
					log.WarnContext(ctx, "corporate event fetch: plugin error",
						"plugin", pe.id, "instrument", inst.ID, "err", err)
				}
				continue
			}

			// Success path: write events (possibly empty) and record coverage.
			if result != nil {
				if len(result.Splits) > 0 {
					if err := database.UpsertStockSplits(ctx, splitsToDB(inst.ID, pe.id, result.Splits)); err != nil {
						if log != nil {
							log.ErrorContext(ctx, "corporate event fetch: upsert splits",
								"plugin", pe.id, "instrument", inst.ID, "err", err)
						}
						continue
					}
					splitsLanded = true
				}
				if len(result.CashDividends) > 0 {
					if err := database.UpsertCashDividends(ctx, dividendsToDB(inst.ID, pe.id, result.CashDividends)); err != nil {
						if log != nil {
							log.ErrorContext(ctx, "corporate event fetch: upsert dividends",
								"plugin", pe.id, "instrument", inst.ID, "err", err)
						}
						continue
					}
				}
			}
			if err := database.UpsertCorporateEventCoverage(ctx, inst.ID, pe.id, gap.From, gap.To); err != nil {
				if log != nil {
					log.ErrorContext(ctx, "corporate event fetch: upsert coverage",
						"plugin", pe.id, "instrument", inst.ID, "err", err)
				}
				continue
			}
			filled = true
			break
		}
		_ = filled
	}

	if splitsLanded {
		if err := database.RecomputeSplitAdjustments(ctx, inst.ID); err != nil {
			if log != nil {
				log.ErrorContext(ctx, "corporate event fetch: recompute split adjustments",
					"instrument", inst.ID, "err", err)
			}
		}

		// Process option contracts on this underlying.
		allSplits, err := database.ListStockSplits(ctx, inst.ID)
		if err != nil {
			if log != nil {
				log.ErrorContext(ctx, "corporate event fetch: list splits for options",
					"instrument", inst.ID, "err", err)
			}
		} else {
			ProcessOptionSplits(ctx, database, inst.ID, allSplits, log, nil)
		}

		// Recompute split-adjusted values for options on this underlying.
		// split_factor_at looks up splits via underlying_id, so option txs
		// need recomputing whenever the underlying's splits change.
		options, err := database.ListOptionsByUnderlying(ctx, inst.ID)
		if err == nil {
			for _, opt := range options {
				if rerr := database.RecomputeSplitAdjustments(ctx, opt.ID); rerr != nil {
					if log != nil {
						log.ErrorContext(ctx, "corporate event fetch: recompute option adjustments",
							"option", opt.ID, "err", rerr)
					}
				}
			}
		}
	}
}

// computeMissingIntervals returns the closed [from, to] date intervals that
// are not covered by any of the supplied coverage rows. The required range
// is [earliestTxDate, endDate]. Adjacent coverage intervals are not merged
// here -- they are already merged in the DB by UpsertCorporateEventCoverage.
//
// Implementation: convert closed intervals to half-open [from, to+1day),
// reuse db.SubtractRanges, then convert back to closed.
func computeMissingIntervals(earliestTxDate, endDate time.Time, coverage []db.CorporateEventCoverage) []db.DateRange {
	if !earliestTxDate.Before(endDate) && !earliestTxDate.Equal(endDate) {
		return nil
	}
	needed := []db.DateRange{{From: earliestTxDate, To: endDate.Add(db.Day)}}
	cached := make([]db.DateRange, 0, len(coverage))
	for _, c := range coverage {
		cached = append(cached, db.DateRange{From: c.CoveredFrom, To: c.CoveredTo.Add(db.Day)})
	}
	cached = db.MergeRanges(cached)
	gaps := db.SubtractRanges(needed, cached)
	out := make([]db.DateRange, 0, len(gaps))
	for _, g := range gaps {
		// Convert half-open back to closed.
		out = append(out, db.DateRange{From: g.From, To: g.To.Add(-db.Day)})
	}
	return out
}

// pluginAccepts checks whether a plugin can handle the given instrument.
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

func toPluginIdentifiers(ids []db.IdentifierInput) []Identifier {
	out := make([]Identifier, len(ids))
	for i, id := range ids {
		out[i] = Identifier{Type: id.Type, Domain: id.Domain, Value: id.Value}
	}
	return out
}

func splitsToDB(instrumentID, provider string, splits []Split) []db.StockSplit {
	out := make([]db.StockSplit, len(splits))
	for i, s := range splits {
		out[i] = db.StockSplit{
			InstrumentID: instrumentID,
			ExDate:       s.ExDate,
			SplitFrom:    s.SplitFrom,
			SplitTo:      s.SplitTo,
			DataProvider: provider,
		}
	}
	return out
}

func dividendsToDB(instrumentID, provider string, dividends []CashDividend) []db.CashDividend {
	out := make([]db.CashDividend, len(dividends))
	for i, d := range dividends {
		row := db.CashDividend{
			InstrumentID: instrumentID,
			ExDate:       d.ExDate,
			Amount:       d.Amount,
			Currency:     d.Currency,
			Frequency:    d.Frequency,
			DataProvider: provider,
		}
		if !d.PayDate.IsZero() {
			t := d.PayDate
			row.PayDate = &t
		}
		if !d.RecordDate.IsZero() {
			t := d.RecordDate
			row.RecordDate = &t
		}
		if !d.DeclarationDate.IsZero() {
			t := d.DeclarationDate
			row.DeclarationDate = &t
		}
		out[i] = row
	}
	return out
}

type pluginConfigJSON struct {
	TimeoutSeconds *int `json:"timeout_seconds"`
}

// timeoutFromConfig parses timeout_seconds from plugin config JSON and falls
// back to DefaultPluginTimeout when missing or invalid.
func timeoutFromConfig(config []byte) time.Duration {
	if len(config) == 0 {
		return DefaultPluginTimeout
	}
	var c pluginConfigJSON
	if err := json.Unmarshal(config, &c); err != nil {
		return DefaultPluginTimeout
	}
	if c.TimeoutSeconds == nil || *c.TimeoutSeconds <= 0 {
		return DefaultPluginTimeout
	}
	return time.Duration(*c.TimeoutSeconds) * time.Second
}

// Trigger sends a non-blocking signal on a corporate event trigger channel.
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
