package corporateevents

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/leedenison/portfoliodb/server/db"
	"github.com/leedenison/portfoliodb/server/pluginutil"
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
func RunWorker(ctx context.Context, database db.DB, registry *Registry, tel db.TelemetryDB, log *slog.Logger, trigger <-chan struct{}, workers *worker.Registry) {
	if tel == nil {
		tel = db.NopTelemetry{}
	}
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
			runID := tel.StartRun(ctx, db.TelemetryRun{Kind: db.TelemetryRunCorporateEventCycle})
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
	const name = "corporate_event_fetcher"
	defer func() {
		if workers != nil {
			workers.SetIdle(name)
			workers.CycleDone(name)
		}
	}()

	// Adjust every option whose identity predates an effective split on its
	// underlying. Deferred so it runs at the end of the cycle -- after any fetch,
	// so it sees splits that just landed -- but also on the early returns below,
	// because it needs neither a plugin nor a held instrument: splits can arrive
	// through ImportCorporateEvents, and an adjustment that failed then would
	// otherwise never be retried on an installation with no fetch plugins
	// enabled.
	//
	// It is deliberately not gated on whether a split landed in this cycle.
	// Coverage is written as soon as events are persisted, so a pass that failed
	// after that point would never see the plugin called again. Driving it off
	// the stored identity instead makes it retry on its own, and picks up a
	// future-dated split once its ex_date passes.
	defer func() {
		if ctx.Err() == nil {
			ProcessPendingOptionSplits(ctx, database, "", log)
		}
	}()

	held, err := database.HeldEventBearingInstruments(ctx)
	if err != nil {
		if log != nil {
			log.ErrorContext(ctx, "corporate event fetch: held instruments", "err", err)
		}
		return err
	}
	if len(held) == 0 {
		return nil
	}

	configs, err := database.ListEnabledPluginConfigs(ctx, db.PluginCategoryCorporateEvent)
	if err != nil {
		if log != nil {
			log.ErrorContext(ctx, "corporate event fetch: list configs", "err", err)
		}
		return err
	}
	if len(configs) == 0 {
		return nil
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
		return nil
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
		return err
	}
	instRows, err := database.ListInstrumentsByIDs(ctx, instIDs)
	if err != nil {
		if log != nil {
			log.ErrorContext(ctx, "corporate event fetch: load instruments", "err", err)
		}
		return err
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
		return err
	}
	coverageByInst := make(map[string][]db.CorporateEventCoverage, len(coverage))
	for _, c := range coverage {
		coverageByInst[c.InstrumentID] = append(coverageByInst[c.InstrumentID], c)
	}

	// Exclusive, so the last day fetched is today + DefaultLookaheadDays.
	endBefore := time.Now().UTC().Truncate(db.Day).AddDate(0, 0, DefaultLookaheadDays+1)
	for _, h := range held {
		if ctx.Err() != nil {
			return nil
		}
		inst := instByID[h.InstrumentID]
		if inst == nil {
			continue
		}
		processInstrument(ctx, database, plugins, inst, h.EarliestTxDate, endBefore,
			coverageByInst[h.InstrumentID], blocked[h.InstrumentID], log)
	}
	return nil
}

// processInstrument fills the missing date intervals for one instrument by
// walking plugins in precedence order. The required range is the half-open
// interval [earliestTxDate, endBefore). Coverage rows for the instrument are
// subtracted to produce the missing intervals; each missing interval is
// offered to plugins one at a time. The first plugin that returns a
// successful response (including an empty result) records coverage and
// claims that interval; lower-precedence plugins are not consulted for it.
func processInstrument(ctx context.Context, database db.DB, plugins []pluginEntry, inst *db.InstrumentRow, earliestTxDate, endBefore time.Time, coverage []db.CorporateEventCoverage, blocked map[string]bool, log *slog.Logger) {
	missing := computeMissingIntervals(earliestTxDate, endBefore, coverage)
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
			if !pluginutil.PluginAccepts(pe.plugin.AcceptableAssetClasses(), pe.plugin.AcceptableExchanges(), pe.plugin.AcceptableCurrencies(), inst) {
				continue
			}
			// Both grains, for the reason the price fetcher gives: an events
			// request is keyed on a ticker as often as on anything else, and a
			// ticker now lives on the listing. Which grain an event belongs to
			// is 0150.
			ids := pluginutil.FilterIdentifiers(pe.plugin.SupportedIdentifierTypes(), inst.AllIdentifiers())
			// Merge provider-specific identifiers for this plugin.
			for _, pi := range inst.AllProviderIdentifiers() {
				if pi.Provider == pe.id {
					ids = append(ids, db.IdentifierInput{
						Ref: db.InstrumentRef{Type: pi.Type, Value: pi.Value, Domain: pi.Domain},
					})
				}
			}
			ids = pluginutil.FilterIdentifiers(pe.plugin.SupportedIdentifierTypes(), ids)
			if len(ids) == 0 {
				continue
			}

			callCtx, callCancel := context.WithTimeout(ctx, pluginutil.TimeoutFromConfig(pe.config, DefaultPluginTimeout))
			result, err := pe.plugin.FetchEvents(callCtx, pe.config, pluginutil.ToIdentifiers(ids), assetClass, gap.From, gap.Before)
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
					var regular []CashDividend
					for _, d := range result.CashDividends {
						if d.Type == "SC" {
							insertSpecialDividend(ctx, database, inst.ID, pe.id, d, log)
						} else {
							regular = append(regular, d)
						}
					}
					if len(regular) > 0 {
						unfiled, err := database.UpsertCashDividends(ctx, dividendsToDB(inst.ID, pe.id, regular))
						if err != nil {
							if log != nil {
								log.ErrorContext(ctx, "corporate event fetch: upsert dividends",
									"plugin", pe.id, "instrument", inst.ID, "err", err)
							}
							continue
						}
						// A currency none of the security's lines are quoted in
						// names no line, and a dividend that names no line is
						// reviewed rather than filed against a guess.
						for _, d := range unfiled {
							QueueUnhandledDividend(ctx, database, d, "UNATTRIBUTABLE_DIVIDEND",
								"Dividend in a currency no listing is quoted in:", log)
						}
					}
				}
			}
			if err := database.UpsertCorporateEventCoverage(ctx, inst.ID, pe.id, gap.From, gap.Before, nil); err != nil {
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

	// Options on this underlying are handled by the once-per-cycle pass in
	// runCycle, which finds its own work and so does not depend on a split
	// having landed in this cycle.
	if splitsLanded {
		if err := database.RecomputeSplitAdjustments(ctx, inst.ID); err != nil {
			if log != nil {
				log.ErrorContext(ctx, "corporate event fetch: recompute split adjustments",
					"instrument", inst.ID, "err", err)
			}
		}
	}
}

// computeMissingIntervals returns the [earliestTxDate, endBefore) date
// intervals that are not covered by any of the supplied coverage rows.
// Adjacent coverage intervals are not merged here -- they are already merged in
// the DB by UpsertCorporateEventCoverage.
func computeMissingIntervals(earliestTxDate, endBefore time.Time, coverage []db.CorporateEventCoverage) []db.DateRange {
	if !earliestTxDate.Before(endBefore) {
		return nil
	}
	needed := []db.DateRange{{From: earliestTxDate, Before: endBefore}}
	cached := make([]db.DateRange, 0, len(coverage))
	for _, c := range coverage {
		cached = append(cached, db.DateRange{From: c.CoveredFrom, Before: c.CoveredBefore})
	}
	return db.SubtractRanges(needed, db.MergeRanges(cached))
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
		out[i] = dividendToDB(instrumentID, provider, d)
	}
	return out
}

// dividendToDB converts one plugin dividend to its stored shape. It names the
// security and the currency and leaves the line to UpsertCashDividends, which is
// the one place the currency picks a listing.
func dividendToDB(instrumentID, provider string, d CashDividend) db.CashDividend {
	typ := d.Type
	if typ == "" {
		typ = "CD"
	}
	row := db.CashDividend{
		InstrumentID: instrumentID,
		ExDate:       d.ExDate,
		Amount:       d.Amount,
		Currency:     d.Currency,
		Frequency:    d.Frequency,
		Type:         typ,
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
	return row
}

// insertSpecialDividend stores a special cash dividend as an unhandled
// corporate event. A special dividend is not the regular series the calendar is
// for, so it is reviewed rather than filed.
func insertSpecialDividend(ctx context.Context, database db.CorporateEventDB, instrumentID, provider string, d CashDividend, log *slog.Logger) {
	QueueUnhandledDividend(ctx, database, dividendToDB(instrumentID, provider, d),
		"SPECIAL_CASH_DIVIDEND", "Special cash dividend", log)
}

// QueueUnhandledDividend stores a dividend that cannot be filed as an unhandled
// corporate event, with its details in the JSONB data field.
//
// Two kinds reach it: a special dividend, which is not part of the regular
// series, and one whose currency names no line of the security, which nothing
// can attribute. The second is not a provider error -- a broker converting a
// dividend into the account currency reports a real payment under a currency the
// security does not trade in -- so it is put in front of a person rather than
// dropped or used to invent a listing. See
// docs/adr/0073-a-dividend-names-a-line-it-does-not-mint.md.
func QueueUnhandledDividend(ctx context.Context, database db.CorporateEventDB, d db.CashDividend, eventType, description string, log *slog.Logger) {
	type dividendData struct {
		Amount          string `json:"amount"`
		Currency        string `json:"currency"`
		PayDate         string `json:"pay_date,omitempty"`
		RecordDate      string `json:"record_date,omitempty"`
		DeclarationDate string `json:"declaration_date,omitempty"`
		Frequency       string `json:"frequency,omitempty"`
		DividendType    string `json:"dividend_type"`
		DataProvider    string `json:"data_provider"`
	}
	sd := dividendData{
		Amount:          d.Amount,
		Currency:        d.Currency,
		PayDate:         optDay(d.PayDate),
		RecordDate:      optDay(d.RecordDate),
		DeclarationDate: optDay(d.DeclarationDate),
		Frequency:       d.Frequency,
		DividendType:    d.Type,
		DataProvider:    d.DataProvider,
	}
	data, err := json.Marshal(sd)
	if err != nil {
		if log != nil {
			log.ErrorContext(ctx, "corporate event fetch: marshal unhandled dividend",
				"instrument", d.InstrumentID, "event_type", eventType, "err", err)
		}
		return
	}
	exDate := d.ExDate
	event := db.UnhandledCorporateEvent{
		InstrumentID: d.InstrumentID,
		EventType:    eventType,
		ExDate:       &exDate,
		Detail:       fmt.Sprintf("%s %s %s (provider: %s)", description, d.Amount, d.Currency, d.DataProvider),
		Data:         data,
	}
	if err := database.InsertUnhandledCorporateEvent(ctx, event); err != nil {
		if log != nil {
			log.ErrorContext(ctx, "corporate event fetch: insert unhandled dividend",
				"instrument", d.InstrumentID, "event_type", eventType, "err", err)
		}
	}
}

// optDay formats an optional date the way the JSONB payload carries it.
func optDay(t *time.Time) string {
	if t == nil || t.IsZero() {
		return ""
	}
	return t.Format("2006-01-02")
}
