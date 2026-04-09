package ingestion

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"slices"
	"time"

	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	"github.com/leedenison/portfoliodb/server/db"
	"github.com/leedenison/portfoliodb/server/identifier"
	"github.com/leedenison/portfoliodb/server/identifier/description"
	"github.com/leedenison/portfoliodb/server/service/identification"
	"github.com/leedenison/portfoliodb/server/telemetry"
)

// identifierHintsFromTx converts proto identifier_hints to []identifier.Identifier for Resolve.
// Type is converted from IdentifierType enum to string (vocabulary name). Invalid hint types are discarded and logged at debug.
func identifierHintsFromTx(ctx context.Context, tx *apiv1.Tx) []identifier.Identifier {
	if tx == nil || len(tx.GetIdentifierHints()) == 0 {
		return nil
	}
	var raw []identifier.Identifier
	for _, h := range tx.IdentifierHints {
		if h.GetType() == apiv1.IdentifierType_IDENTIFIER_TYPE_UNSPECIFIED || h.GetValue() == "" {
			continue
		}
		typeStr := apiv1.IdentifierType_name[int32(h.GetType())]
		raw = append(raw, identifier.Identifier{Type: typeStr, Domain: h.GetDomain(), Value: h.GetValue()})
	}
	return identification.FilterIdentifierHints(ctx, raw, ingestionLogger())
}

// Resolution order: (1) DB lookup by (source, instrument_description), (2) in-batch cache,
// (3) if still unresolved, call enabled plugins in parallel (timeout from config, retry once with backoff).
// Identification errors are recorded for fallbacks and do not fail the job.

// ingestionLogger returns the logger for plugin/resolution logs (category server/service/ingestion when set).
func ingestionLogger() *slog.Logger {
	if ingestionLog != nil {
		return ingestionLog
	}
	return slog.Default()
}

// Distinct messages for identification errors (per spec).
const (
	MsgExtractionFailed      = "description extraction failed"
	MsgBrokerDescriptionOnly = "broker description only"
	MsgPluginTimeout         = "plugin timeout"
	MsgPluginUnavailable     = "plugin unavailable"
)

// resolveResult holds the outcome of resolving one (source, instrument_description).
type resolveResult struct {
	InstrumentID  string
	IdErr         *db.IdentificationError
	FirstRowIndex int32
}

// cacheKey returns a key for the batch cache.  When no identifier hints are
// supplied, same (source, description) resolves once.  When identifier hints
// are present they are appended so that two transactions with the same
// description but different hints (e.g. a security buy and the corresponding
// cash leg) cache independently.
func cacheKey(source, instrumentDescription string) string {
	return source + "\x00" + instrumentDescription
}

func cacheKeyWithHints(source, instrumentDescription string, hints []identifier.Identifier) string {
	if len(hints) == 0 {
		return cacheKey(source, instrumentDescription)
	}
	// Sort so that the key is order-independent.
	sorted := slices.Clone(hints)
	slices.SortFunc(sorted, func(a, b identifier.Identifier) int {
		if c := cmp.Compare(a.Type, b.Type); c != 0 {
			return c
		}
		return cmp.Compare(a.Value, b.Value)
	})
	k := source + "\x00" + instrumentDescription
	for _, h := range sorted {
		k += "\x00" + h.Type + ":" + h.Value
	}
	return k
}

// shortHashForBatch returns a short stable id (first 8 hex chars of SHA256) for batch description extraction response matching.
func shortHashForBatch(key string) string {
	h := sha256.Sum256([]byte(key))
	return hex.EncodeToString(h[:])[:8]
}

// batchItemIDs returns the IDs of batch items for debug logging.
func batchItemIDs(items []description.BatchItem) []string {
	ids := make([]string, len(items))
	for i := range items {
		ids[i] = items[i].ID
	}
	return ids
}

// batchItemDescByID returns the instrument description for the batch item with the given ID, or "" if not found.
func batchItemDescByID(items []description.BatchItem, id string) string {
	for i := range items {
		if items[i].ID == id {
			return items[i].InstrumentDescription
		}
	}
	return ""
}

// runDescriptionPluginsBatch runs description plugins on all items via ExtractBatch. Only items whose security type is acceptable to a plugin are passed to that plugin. First plugin that returns a non-empty map wins. Result is keyed by BatchItem.ID.
func runDescriptionPluginsBatch(ctx context.Context, database db.PluginConfigDB, descRegistry *description.Registry, counter telemetry.CounterIncrementer, broker, source string, items []description.BatchItem) (map[string][]identifier.Identifier, error) {
	if descRegistry == nil || len(items) == 0 {
		return nil, nil
	}
	configs, err := database.ListEnabledPluginConfigs(ctx, db.PluginCategoryDescription)
	if err != nil {
		return nil, err
	}
	if len(configs) > 0 && counter != nil {
		counter.IncrBy(ctx, "instruments.resolution.totals.description.attempts", int64(len(items)))
	}
	resolved := make(map[string]bool)
	merged := make(map[string][]identifier.Identifier)
	for _, c := range configs {
		p := descRegistry.Get(c.PluginID)
		if p == nil {
			continue
		}
		acceptableKinds := p.AcceptableInstrumentKinds()
		acceptableTypes := p.AcceptableSecurityTypes()
		var filtered []description.BatchItem
		for _, item := range items {
			if resolved[item.ID] {
				continue
			}
			if identifier.ShouldAttemptPlugin(acceptableKinds, acceptableTypes, item.Hints.InstrumentKind, item.Hints.SecurityTypeHint) {
				filtered = append(filtered, item)
			}
		}
		if len(filtered) == 0 {
			ingestionLogger().DebugContext(ctx, "description plugin batch skipped (no items with acceptable security type)", "plugin_id", c.PluginID)
			continue
		}
		out, err := p.ExtractBatch(ctx, c.Config, broker, source, filtered)
		if err != nil {
			if counter != nil {
				counter.Incr(ctx, "instruments.resolution.totals.description.plugin_error")
			}
			ingestionLogger().DebugContext(ctx, "description plugin batch result: error", "plugin_id", c.PluginID, "err", err)
			continue
		}
		hasAny := false
		for id, hints := range out {
			filteredHints := identification.FilterIdentifierHints(ctx, hints, ingestionLogger())
			if len(filteredHints) > 0 {
				merged[id] = filteredHints
				resolved[id] = true
				hasAny = true
			}
		}
		if hasAny {
			for id, hints := range out {
				if len(hints) > 0 {
					ingestionLogger().DebugContext(ctx, "description plugin batch result: hints", "plugin_id", c.PluginID, "batch_id", id, "instrument_description", batchItemDescByID(filtered, id), "hints", identification.HintsSummary(hints))
				}
			}
		} else {
			ingestionLogger().DebugContext(ctx, "description plugin batch result: no hints", "plugin_id", c.PluginID, "batch_ids", batchItemIDs(filtered))
		}
	}
	if len(merged) > 0 {
		return merged, nil
	}
	if counter != nil {
		counter.Incr(ctx, "instruments.resolution.totals.description.no_hints")
	}
	return nil, nil
}

// Resolve resolves (source, instrumentDescription) to an instrument_id using the batch cache, then (when no client
// identifier_hints) pre-extracted description hints, then identifier plugins.
// The caller is responsible for populating cache (DB hits by source+description) and extractedHintsCache
// (hints from description plugins) before calling Resolve; see the pre-pass in process().
// When client supplies identifier_hints, resolution is by identifiers only and (source, description) is not persisted
// to the DB as a BROKER_DESCRIPTION identifier (though results are still cached in the in-memory batch cache).
// hints are optional (exchange, currency, MIC, security type). counter is optional; when non-nil and plugins are invoked, instrument.identify.attempts is incremented.
func Resolve(ctx context.Context, database db.DB, registry *identifier.Registry, broker, source, instrumentDescription string, hints identifier.Hints, identifierHints []identifier.Identifier, cache map[string]resolveResult, rowIndex int32, counter telemetry.CounterIncrementer, extractedHintsCache map[string][]identifier.Identifier, hintsValidAt *time.Time) (resolveResult, error) {
	key := cacheKeyWithHints(source, instrumentDescription, identifierHints)
	if cache != nil {
		if r, ok := cache[key]; ok {
			return r, nil
		}
	}

	// Path A: client supplied identifier_hints -- resolve by identifiers only.
	// "Do not store" means no persistent BROKER_DESCRIPTION identifier is
	// created in the DB (the `false` arg to resolveWithIdentifierPlugins).
	// The in-memory batch cache above is still used to avoid repeated DB
	// lookups within the same upload.
	if len(identifierHints) > 0 {
		ids, err := identification.ResolveByHintsDBOnly(ctx, database, identifierHints)
		if err != nil {
			return resolveResult{}, err
		}
		if len(ids) > 1 {
			return resolveResult{}, fmt.Errorf("conflicting identifier hints resolve to different instruments")
		}
		if len(ids) == 1 {
			r := resolveResult{InstrumentID: ids[0], FirstRowIndex: rowIndex}
			if cache != nil {
				cache[key] = r
			}
			return r, nil
		}
		// No DB hit: call identifier plugins with hints; do not persist (source, description) as BROKER_DESCRIPTION.
		return resolveWithIdentifierPlugins(ctx, database, registry, broker, source, instrumentDescription, hints, identifierHints, cache, key, rowIndex, counter, false, hintsValidAt)
	}

	// Path B: no client hints -- use pre-extracted description hints, then identifier plugins.
	// DB lookup by (source, description) and batch description extraction are handled by the
	// caller's pre-pass; DB hits are already in cache (caught above), misses proceed here.
	var extractedHints []identifier.Identifier
	if extractedHintsCache != nil {
		extractedHints = extractedHintsCache[key]
	}
	if len(extractedHints) == 0 {
		// Extraction failed: ensure broker-description-only and record error.
		// Identifier plugins are never called in this path, so no Redis counters and no OpenFIGI.
		if counter != nil {
			counter.Incr(ctx, "instruments.resolution.totals.description.extraction_failed")
		}
		ingestionLogger().InfoContext(ctx, "instrument resolution: description extraction failed, using broker description only", "source", source, "instrument_description", instrumentDescription)
		instID, ensureErr := database.EnsureInstrument(ctx, "", "", "", instrumentDescription, "", "", []db.IdentifierInput{{Type: "BROKER_DESCRIPTION", Domain: source, Value: instrumentDescription, Canonical: false}}, "", nil, nil, nil)
		if ensureErr != nil {
			return resolveResult{}, ensureErr
		}
		r := resolveResult{
			InstrumentID:  instID,
			FirstRowIndex: rowIndex,
			IdErr:         &db.IdentificationError{RowIndex: rowIndex, InstrumentDescription: instrumentDescription, Message: MsgExtractionFailed},
		}
		if cache != nil {
			cache[key] = r
		}
		return r, nil
	}

	// When extraction returned both MIC_TICKER and OPENFIGI_SHARE_CLASS, resolve each separately and validate they match.
	// If they resolve to different instruments, increment counter, log error, and use MIC_TICKER.
	hintsToUse := extractedHints
	tickerHints := hintsByType(extractedHints, "MIC_TICKER")
	figiHints := hintsByType(extractedHints, "OPENFIGI_SHARE_CLASS")
	if len(tickerHints) > 0 && len(figiHints) > 0 {
		// Resolve with nil cache and nil counter so we don't pollute cache or double-count identify attempts.
		resultByTicker, _ := resolveWithIdentifierPlugins(ctx, database, registry, broker, source, instrumentDescription, hints, tickerHints, nil, key, rowIndex, nil, true, hintsValidAt)
		resultByFigi, _ := resolveWithIdentifierPlugins(ctx, database, registry, broker, source, instrumentDescription, hints, figiHints, nil, key, rowIndex, nil, true, hintsValidAt)
		idByTicker := resultByTicker.InstrumentID
		idByFigi := resultByFigi.InstrumentID
		// Consider "unresolved" (broker-description-only) as empty for mismatch check
		if idByTicker != "" && idByFigi != "" && idByTicker != idByFigi {
			if counter != nil {
				counter.Incr(ctx, "instruments.resolution.totals.description.identifier_mismatch")
			}
			ingestionLogger().ErrorContext(ctx, "MIC_TICKER and OPENFIGI_SHARE_CLASS resolved to different instruments; using MIC_TICKER",
				"source", source, "instrument_description", instrumentDescription,
				"instrument_id_by_ticker", idByTicker, "instrument_id_by_figi", idByFigi)
			hintsToUse = tickerHints
		}
	}

	// Resolve by (validated) hints; always store (source, description) when ensuring.
	return resolveWithIdentifierPlugins(ctx, database, registry, broker, source, instrumentDescription, hints, hintsToUse, cache, key, rowIndex, counter, true, hintsValidAt)
}

// hintsByType returns hints whose Type equals typ (e.g. "MIC_TICKER", "OPENFIGI_SHARE_CLASS").
func hintsByType(hints []identifier.Identifier, typ string) []identifier.Identifier {
	var out []identifier.Identifier
	for _, h := range hints {
		if h.Type == typ {
			out = append(out, h)
		}
	}
	return out
}

// resolveWithIdentifierPlugins delegates to the shared identification package and wraps the result
// in ingestion-specific resolveResult with cache and error handling.
func resolveWithIdentifierPlugins(ctx context.Context, database db.DB, registry *identifier.Registry, broker, source, instrumentDescription string, hints identifier.Hints, identifierHints []identifier.Identifier, cache map[string]resolveResult, key string, rowIndex int32, counter telemetry.CounterIncrementer, storeSourceDescription bool, hintsValidAt *time.Time) (resolveResult, error) {
	// Ingestion-specific fallback: broker-description-only instrument.
	fallback := func(ctx context.Context, database db.DB) (string, error) {
		return database.EnsureInstrument(ctx, "", "", "", instrumentDescription, "", "",
			[]db.IdentifierInput{{Type: "BROKER_DESCRIPTION", Domain: source, Value: instrumentDescription, Canonical: false}},
			"", nil, nil, nil)
	}

	result, err := identification.ResolveWithPlugins(ctx, database, registry, broker, source, instrumentDescription, hints, identifierHints, storeSourceDescription, fallback, counter, ingestionLogger(), 0, hintsValidAt)
	if err != nil {
		return resolveResult{}, err
	}

	r := resolveResult{InstrumentID: result.InstrumentID, FirstRowIndex: rowIndex}
	if !result.Identified {
		msg := MsgBrokerDescriptionOnly
		if result.HadTimeout {
			msg = MsgPluginTimeout
		} else if result.HadError {
			msg = MsgPluginUnavailable
		}
		r.IdErr = &db.IdentificationError{RowIndex: rowIndex, InstrumentDescription: instrumentDescription, Message: msg}
	}
	if cache != nil {
		cache[key] = r
	}
	return r, nil
}
