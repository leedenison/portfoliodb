package ingestion

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	"github.com/leedenison/portfoliodb/server/db"
	"github.com/leedenison/portfoliodb/server/identifier"
	"github.com/leedenison/portfoliodb/server/identifier/description"
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
	return filterIdentifierHints(ctx, raw)
}

// filterIdentifierHints keeps only hints whose Type is in the controlled vocabulary (identifier.AllowedIdentifierTypes).
// Invalid types are discarded and logged at debug; ingestion is not halted.
func filterIdentifierHints(ctx context.Context, hints []identifier.Identifier) []identifier.Identifier {
	if len(hints) == 0 {
		return nil
	}
	out := make([]identifier.Identifier, 0, len(hints))
	for _, h := range hints {
		typ := strings.TrimSpace(h.Type)
		if typ == "" {
			continue
		}
		if identifier.AllowedIdentifierTypes[typ] {
			out = append(out, h)
		} else {
			ingestionLogger().DebugContext(ctx, "identifier hint discarded: type not in vocabulary", "type", typ, "value", h.Value)
		}
	}
	return out
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

// hintsSummary returns a short summary of hints for debug logging (e.g. "TICKER:AAPL, FIGI:...").
func hintsSummary(hints []identifier.Identifier) string {
	if len(hints) == 0 {
		return ""
	}
	var b strings.Builder
	for i, h := range hints {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(h.Type)
		if h.Domain != "" {
			b.WriteString("(")
			b.WriteString(h.Domain)
			b.WriteString(")")
		}
		b.WriteString(":")
		b.WriteString(h.Value)
	}
	return b.String()
}

// instrumentSummary returns a short summary of an instrument for debug logging.
func instrumentSummary(inst *identifier.Instrument) string {
	if inst == nil {
		return ""
	}
	return inst.Name + " (" + inst.AssetClass + "/" + inst.Exchange + ")"
}

const (
	defaultPluginTimeout = 30 * time.Second
	pluginRetryBackoff   = 2 * time.Second
)

// Distinct messages for identification errors (per spec).
const (
	MsgExtractionFailed     = "description extraction failed"
	MsgBrokerDescriptionOnly = "broker description only"
	MsgPluginTimeout         = "plugin timeout"
	MsgPluginUnavailable     = "plugin unavailable"
)

// resolveResult holds the outcome of resolving one (source, instrument_description).
type resolveResult struct {
	InstrumentID   string
	IdErr          *db.IdentificationError
	FirstRowIndex  int32
}

// cacheKey returns a key for the batch cache. Same (source, description) in a batch resolves once.
func cacheKey(source, instrumentDescription string) string {
	return source + "\x00" + instrumentDescription
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

// resolveByHintsDBOnly looks up each hint by (type, domain, value) and returns unique instrument IDs (in order of first occurrence).
//
// Fallback when domain is empty: If the hint has domain == "" and the exact (type, domain, value) lookup finds nothing,
// we perform a second lookup by (type, value) only, ignoring domain. That allows e.g. (TICKER, "", "AAPL") to match
// a stored row (TICKER, "US", "AAPL"). We do this because:
//   - Empty domain means the user supplied only a ticker (no exchange), or we only extracted a ticker from the
//     instrument description (e.g. a description plugin returned "AAPL" with no exchange). In those cases the user
//     is effectively saying "resolve this to any valid ticker/exchange combo."
//   - In storage we persist TICKER with domain set to the exchange code (e.g. "US" for US exchanges). So
//     FindInstrumentByIdentifier(type, "", value) looks for domain IS NULL and does not match those rows. The
//     fallback FindInstrumentByTypeAndValue(type, value) matches any domain; if exactly one instrument has that
//     (type, value), we use it. If multiple instruments match (same ticker on different exchanges), the fallback
//     returns "" and we do not resolve (ambiguous).
func resolveByHintsDBOnly(ctx context.Context, database db.InstrumentDB, hints []identifier.Identifier) ([]string, error) {
	seen := make(map[string]bool)
	var ids []string
	for _, h := range hints {
		if h.Type == "" || h.Value == "" {
			continue
		}
		id, err := database.FindInstrumentByIdentifier(ctx, h.Type, h.Domain, h.Value)
		if err != nil {
			return nil, err
		}
		if id == "" && h.Domain == "" {
			id, err = database.FindInstrumentByTypeAndValue(ctx, h.Type, h.Value)
			if err != nil {
				return nil, err
			}
		}
		if id != "" && !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	return ids, nil
}

const singleItemBatchID = "1"

// runDescriptionPlugins runs enabled description plugins in series by precedence; returns hints from the first that returns ≥1.
// Only calls plugins that accept the hint's security type. Uses ExtractBatch with a single BatchItem.
// If descRegistry is nil, returns nil (no hints) without calling the database.
// When counter is non-nil, increments description.extraction.plugin_error on plugin errors and description.extraction.no_hints when at least one plugin was tried and none returned hints.
func runDescriptionPlugins(ctx context.Context, database db.DescriptionPluginDB, descRegistry *description.Registry, counter telemetry.CounterIncrementer, broker, source, instrumentDescription string, hints identifier.Hints) ([]identifier.Identifier, error) {
	if descRegistry == nil {
		return nil, nil
	}
	configs, err := database.ListEnabledDescriptionPluginConfigs(ctx)
	if err != nil {
		return nil, err
	}
	if len(configs) > 0 && counter != nil {
		counter.Incr(ctx, "instruments.resolution.totals.description.attempts")
	}
	items := []description.BatchItem{{ID: singleItemBatchID, InstrumentDescription: instrumentDescription, Hints: hints}}
	var tried int
	for _, c := range configs {
		p := descRegistry.Get(c.PluginID)
		if p == nil {
			continue
		}
		acceptable := p.AcceptableSecurityTypes()
		if len(acceptable) > 0 && !acceptable[hints.SecurityTypeHint] {
			ingestionLogger().DebugContext(ctx, "description plugin skipped (security type not acceptable)", "plugin_id", c.PluginID, "instrument_description", instrumentDescription, "security_type_hint", hints.SecurityTypeHint)
			continue
		}
		tried++
		out, err := p.ExtractBatch(ctx, c.Config, broker, source, items)
		if err != nil {
			if counter != nil {
				counter.Incr(ctx, "instruments.resolution.totals.description.plugin_error")
			}
			ingestionLogger().DebugContext(ctx, "description plugin result: error", "plugin_id", c.PluginID, "instrument_description", instrumentDescription, "err", err)
			continue
		}
		extracted := out[singleItemBatchID]
		if len(extracted) > 0 {
			ingestionLogger().DebugContext(ctx, "description plugin result: hints", "plugin_id", c.PluginID, "instrument_description", instrumentDescription, "hints", hintsSummary(extracted))
			return filterIdentifierHints(ctx, extracted), nil
		}
		ingestionLogger().DebugContext(ctx, "description plugin result: no hints", "plugin_id", c.PluginID, "instrument_description", instrumentDescription)
	}
	if tried > 0 {
		if counter != nil {
			counter.Incr(ctx, "instruments.resolution.totals.description.no_hints")
		}
		ingestionLogger().DebugContext(ctx, "description extraction: no plugin returned hints", "source", source, "instrument_description", instrumentDescription)
	}
	return nil, nil
}

// runDescriptionPluginsBatch runs description plugins on all items via ExtractBatch. Only items whose security type is acceptable to a plugin are passed to that plugin. First plugin that returns a non-empty map wins. Result is keyed by BatchItem.ID.
func runDescriptionPluginsBatch(ctx context.Context, database db.DescriptionPluginDB, descRegistry *description.Registry, counter telemetry.CounterIncrementer, broker, source string, items []description.BatchItem) (map[string][]identifier.Identifier, error) {
	if descRegistry == nil || len(items) == 0 {
		return nil, nil
	}
	configs, err := database.ListEnabledDescriptionPluginConfigs(ctx)
	if err != nil {
		return nil, err
	}
	if len(configs) > 0 && counter != nil {
		counter.IncrBy(ctx, "instruments.resolution.totals.description.attempts", int64(len(items)))
	}
	for _, c := range configs {
		p := descRegistry.Get(c.PluginID)
		if p == nil {
			continue
		}
		acceptable := p.AcceptableSecurityTypes()
		var filtered []description.BatchItem
		for _, item := range items {
			if len(acceptable) == 0 || acceptable[item.Hints.SecurityTypeHint] {
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
			filteredHints := filterIdentifierHints(ctx, hints)
			out[id] = filteredHints
			if len(filteredHints) > 0 {
				hasAny = true
			}
		}
		if hasAny {
			for id, hints := range out {
				ingestionLogger().DebugContext(ctx, "description plugin batch result: hints", "plugin_id", c.PluginID, "batch_id", id, "instrument_description", batchItemDescByID(filtered, id), "hints", hintsSummary(hints))
			}
			return out, nil
		}
		ingestionLogger().DebugContext(ctx, "description plugin batch result: no hints", "plugin_id", c.PluginID, "batch_ids", batchItemIDs(filtered))
	}
	if counter != nil {
		counter.Incr(ctx, "instruments.resolution.totals.description.no_hints")
	}
	return nil, nil
}

// pluginConfigJSON is the shape we read from identifier_plugin_config.config (JSONB).
type pluginConfigJSON struct {
	TimeoutSeconds *int `json:"timeout_seconds"`
}

// timeoutFromConfig parses config JSON and returns timeout; uses default if missing or invalid.
func timeoutFromConfig(config []byte) time.Duration {
	if len(config) == 0 {
		return defaultPluginTimeout
	}
	var c pluginConfigJSON
	if err := json.Unmarshal(config, &c); err != nil {
		return defaultPluginTimeout
	}
	if c.TimeoutSeconds == nil || *c.TimeoutSeconds <= 0 {
		return defaultPluginTimeout
	}
	return time.Duration(*c.TimeoutSeconds) * time.Second
}

// callPluginWithRetry calls Identify once; on non-ErrNotIdentified error, sleeps backoff and tries once more.
func callPluginWithRetry(ctx context.Context, p identifier.Plugin, config []byte, broker, source, instrumentDescription string, hints identifier.Hints, identifierHints []identifier.Identifier, timeout time.Duration) (*identifier.Instrument, []identifier.Identifier, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	inst, ids, err := p.Identify(ctx, config, broker, source, instrumentDescription, hints, identifierHints)
	if err == nil || errors.Is(err, identifier.ErrNotIdentified) {
		return inst, ids, err
	}
	// Retry once with backoff (use new context so retry is not cancelled by parent)
	time.Sleep(pluginRetryBackoff)
	ctx2, cancel2 := context.WithTimeout(context.Background(), timeout)
	defer cancel2()
	inst, ids, err2 := p.Identify(ctx2, config, broker, source, instrumentDescription, hints, identifierHints)
	if err2 != nil {
		return nil, nil, err2
	}
	return inst, ids, err2
}

// Resolve resolves (source, instrumentDescription) to an instrument_id using DB, then batch cache, then (when no client identifier_hints) description plugins to extract hints, then identifier plugins.
// When client supplies identifier_hints, resolution is by identifiers only and (source, description) is not stored.
// hints are optional (exchange, currency, MIC, security type). counter is optional; when non-nil and plugins are invoked, instrument.identify.attempts is incremented.
// When extractedHintsCache is non-nil (e.g. from runDescriptionPluginsBatch), it is used instead of calling description plugins; key is cacheKey(source, instrumentDescription).
// When sourceDescriptionCheckedMiss is non-nil and key is in the map, the (source, description) lookup was already done (e.g. in bulk pre-pass) and missed; skip DB lookup and proceed as if id was "".
func Resolve(ctx context.Context, database db.DB, registry *identifier.Registry, descRegistry *description.Registry, broker, source, instrumentDescription string, hints identifier.Hints, identifierHints []identifier.Identifier, cache map[string]resolveResult, rowIndex int32, counter telemetry.CounterIncrementer, extractedHintsCache map[string][]identifier.Identifier, sourceDescriptionCheckedMiss map[string]bool) (resolveResult, error) {
	key := cacheKey(source, instrumentDescription)
	if cache != nil {
		if r, ok := cache[key]; ok {
			// If this tx has client identifier_hints, verify they resolve to the cached instrument (batch conflict check).
			if len(identifierHints) > 0 {
				ids, err := resolveByHintsDBOnly(ctx, database, identifierHints)
				if err != nil {
					return resolveResult{}, err
				}
				if len(ids) > 1 {
					return resolveResult{}, fmt.Errorf("conflicting identifier hints resolve to different instruments")
				}
				if len(ids) == 1 && ids[0] != r.InstrumentID {
					return resolveResult{}, fmt.Errorf("conflicting identifier hints resolve to different instruments")
				}
			}
			return r, nil
		}
	}

	// Path A: client supplied identifier_hints — resolve by identifiers only; do not store (source, description).
	if len(identifierHints) > 0 {
		ids, err := resolveByHintsDBOnly(ctx, database, identifierHints)
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
		// No DB hit: call identifier plugins with hints; do not store (source, description).
		return resolveWithIdentifierPlugins(ctx, database, registry, broker, source, instrumentDescription, hints, identifierHints, cache, key, rowIndex, counter, false)
	}

	// Path B: no client hints — DB lookup by (source, description), then description plugins, then identifier plugins.
	var id string
	if sourceDescriptionCheckedMiss != nil && sourceDescriptionCheckedMiss[key] {
		id = "" // already looked up in bulk pre-pass and missed
	} else {
		var err error
		id, err = database.FindInstrumentBySourceDescription(ctx, source, instrumentDescription)
		if err != nil {
			return resolveResult{}, err
		}
	}
	if id != "" {
		r := resolveResult{InstrumentID: id, FirstRowIndex: rowIndex}
		if cache != nil {
			cache[key] = r
		}
		return r, nil
	}

	var extractedHints []identifier.Identifier
	if extractedHintsCache != nil {
		extractedHints = extractedHintsCache[key]
	}
	if extractedHints == nil {
		// Run description plugins in series; first that returns ≥1 hint wins.
		var err error
		extractedHints, err = runDescriptionPlugins(ctx, database, descRegistry, counter, broker, source, instrumentDescription, hints)
		if err != nil {
			return resolveResult{}, err
		}
	}
	if len(extractedHints) == 0 {
		// Extraction failed: ensure broker-description-only and record error.
		// Identifier plugins are never called in this path, so no Redis counters and no OpenFIGI.
		if counter != nil {
			counter.Incr(ctx, "instruments.resolution.totals.description.extraction_failed")
		}
		ingestionLogger().InfoContext(ctx, "instrument resolution: description extraction failed, using broker description only", "source", source, "instrument_description", instrumentDescription)
		var ensureErr error
		id, ensureErr = database.EnsureInstrument(ctx, "", "", "", instrumentDescription, []db.IdentifierInput{{Type: "BROKER_DESCRIPTION", Domain: source, Value: instrumentDescription, Canonical: false}}, "", nil, nil)
		if ensureErr != nil {
			return resolveResult{}, ensureErr
		}
		r := resolveResult{
			InstrumentID:  id,
			FirstRowIndex: rowIndex,
			IdErr:         &db.IdentificationError{RowIndex: rowIndex, InstrumentDescription: instrumentDescription, Message: MsgExtractionFailed},
		}
		if cache != nil {
			cache[key] = r
		}
		return r, nil
	}

	// When extraction returned both TICKER and OPENFIGI_SHARE_CLASS, resolve each separately and validate they match.
	// If they resolve to different instruments, increment counter, log error, and use TICKER.
	hintsToUse := extractedHints
	tickerHints := hintsByType(extractedHints, "TICKER")
	figiHints := hintsByType(extractedHints, "OPENFIGI_SHARE_CLASS")
	if len(tickerHints) > 0 && len(figiHints) > 0 {
		// Resolve with nil cache and nil counter so we don't pollute cache or double-count identify attempts.
		resultByTicker, _ := resolveWithIdentifierPlugins(ctx, database, registry, broker, source, instrumentDescription, hints, tickerHints, nil, key, rowIndex, nil, true)
		resultByFigi, _ := resolveWithIdentifierPlugins(ctx, database, registry, broker, source, instrumentDescription, hints, figiHints, nil, key, rowIndex, nil, true)
		idByTicker := resultByTicker.InstrumentID
		idByFigi := resultByFigi.InstrumentID
		// Consider "unresolved" (broker-description-only) as empty for mismatch check
		if idByTicker != "" && idByFigi != "" && idByTicker != idByFigi {
			if counter != nil {
				counter.Incr(ctx, "instruments.resolution.totals.description.identifier_mismatch")
			}
			ingestionLogger().ErrorContext(ctx, "TICKER and OPENFIGI_SHARE_CLASS resolved to different instruments; using TICKER",
				"source", source, "instrument_description", instrumentDescription,
				"instrument_id_by_ticker", idByTicker, "instrument_id_by_figi", idByFigi)
			hintsToUse = tickerHints
		}
	}

	// Resolve by (validated) hints; always store (source, description) when ensuring.
	return resolveWithIdentifierPlugins(ctx, database, registry, broker, source, instrumentDescription, hints, hintsToUse, cache, key, rowIndex, counter, true)
}

// hintsByType returns hints whose Type equals typ (e.g. "TICKER", "OPENFIGI_SHARE_CLASS").
func hintsByType(hints []identifier.Identifier, typ string) []identifier.Identifier {
	var out []identifier.Identifier
	for _, h := range hints {
		if h.Type == typ {
			out = append(out, h)
		}
	}
	return out
}

// resolveWithIdentifierPlugins calls enabled identifier plugins with the given hints, merges results, and ensures instrument.
// When storeSourceDescription is false (client supplied hints), (source, description) is not added to identifiers.
func resolveWithIdentifierPlugins(ctx context.Context, database db.DB, registry *identifier.Registry, broker, source, instrumentDescription string, hints identifier.Hints, identifierHints []identifier.Identifier, cache map[string]resolveResult, key string, rowIndex int32, counter telemetry.CounterIncrementer, storeSourceDescription bool) (resolveResult, error) {
	// Optional: if all hints already resolve to one instrument in DB, use it (avoids plugin call).
	ids, err := resolveByHintsDBOnly(ctx, database, identifierHints)
	if err != nil {
		return resolveResult{}, err
	}
	if len(ids) == 1 {
		r := resolveResult{InstrumentID: ids[0], FirstRowIndex: rowIndex}
		if cache != nil {
			cache[key] = r
		}
		return r, nil
	}

	configs, err := database.ListEnabledPluginConfigs(ctx)
	if err != nil {
		return resolveResult{}, err
	}
	type pluginInput struct {
		config db.PluginConfigRow
		plugin identifier.Plugin
	}
	var inputs []pluginInput
	for _, c := range configs {
		p := registry.Get(c.PluginID)
		if p == nil {
			continue
		}
		acceptable := p.AcceptableSecurityTypes()
		if len(acceptable) > 0 && !acceptable[hints.SecurityTypeHint] {
			continue
		}
		inputs = append(inputs, pluginInput{config: c, plugin: p})
	}
	if len(inputs) > 0 && counter != nil {
		counter.Incr(ctx, "instruments.resolution.totals.identify.attempts")
	}
	if len(inputs) == 0 {
		ingestionLogger().DebugContext(ctx, "instrument resolution: no enabled identifier plugins", "source", source, "instrument_description", instrumentDescription)
	}

	type result struct {
		precedence int
		inst       *identifier.Instrument
		ids        []identifier.Identifier
		err        error
	}
	results := make([]result, len(inputs))
	var wg sync.WaitGroup
	for i := range inputs {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			in := inputs[idx]
			timeout := timeoutFromConfig(in.config.Config)
			inst, ids, err := callPluginWithRetry(ctx, in.plugin, in.config.Config, broker, source, instrumentDescription, hints, identifierHints, timeout)
			results[idx] = result{precedence: in.config.Precedence, inst: inst, ids: ids, err: err}
		}(i)
	}
	wg.Wait()

	for i := range results {
		in := inputs[i]
		r := &results[i]
		if r.err != nil {
			ingestionLogger().DebugContext(ctx, "identifier plugin result", "plugin_id", in.config.PluginID, "instrument_description", instrumentDescription, "err", r.err)
		} else if r.inst != nil {
			ingestionLogger().DebugContext(ctx, "identifier plugin result", "plugin_id", in.config.PluginID, "instrument_description", instrumentDescription, "instrument_name", r.inst.Name, "instrument", instrumentSummary(r.inst), "identifiers", hintsSummary(r.ids))
		} else {
			ingestionLogger().DebugContext(ctx, "identifier plugin result", "plugin_id", in.config.PluginID, "instrument_description", instrumentDescription, "result", "not_identified")
		}
	}

	var winner *result
	var winnerIdx int
	var hadTimeout, hadOtherErr bool
	for i := range results {
		r := &results[i]
		if r.err == nil && r.inst != nil {
			if winner == nil {
				winner = r
				winnerIdx = i
			}
			continue
		}
		if errors.Is(r.err, identifier.ErrNotIdentified) {
			continue
		}
		if errors.Is(r.err, context.DeadlineExceeded) {
			hadTimeout = true
		} else {
			hadOtherErr = true
		}
	}

	if winner != nil {
		ingestionLogger().DebugContext(ctx, "identifier plugin chosen", "plugin_id", inputs[winnerIdx].config.PluginID, "instrument_description", instrumentDescription, "instrument_name", winner.inst.Name)
		seenType := make(map[string]bool)
		var mergedIds []identifier.Identifier
		for i := range results {
			r := &results[i]
			if r.err != nil || r.inst == nil {
				continue
			}
			for _, idn := range r.ids {
				if !seenType[idn.Type] {
					seenType[idn.Type] = true
					mergedIds = append(mergedIds, idn)
				}
			}
		}
		identifiers := make([]db.IdentifierInput, 0, len(mergedIds)+1)
		hasSource := false
		for _, idn := range mergedIds {
			identifiers = append(identifiers, db.IdentifierInput{Type: idn.Type, Domain: idn.Domain, Value: idn.Value, Canonical: true})
			if idn.Type == "BROKER_DESCRIPTION" && idn.Domain == source && idn.Value == instrumentDescription {
				hasSource = true
			}
		}
		if storeSourceDescription && !hasSource {
			identifiers = append(identifiers, db.IdentifierInput{Type: "BROKER_DESCRIPTION", Domain: source, Value: instrumentDescription, Canonical: false})
		}
		inst := winner.inst
		var underlyingID string
		var validFrom, validTo *time.Time
		if inst.ValidFrom != nil {
			validFrom = inst.ValidFrom
		}
		if inst.ValidTo != nil {
			validTo = inst.ValidTo
		}
		if inst.Underlying != nil && len(inst.UnderlyingIdentifiers) > 0 {
			underlyingIdns := make([]db.IdentifierInput, 0, len(inst.UnderlyingIdentifiers))
			for _, idn := range inst.UnderlyingIdentifiers {
				underlyingIdns = append(underlyingIdns, db.IdentifierInput{Type: idn.Type, Domain: idn.Domain, Value: idn.Value, Canonical: true})
			}
			underlyingID, err = database.EnsureInstrument(ctx, inst.Underlying.AssetClass, inst.Underlying.Exchange, inst.Underlying.Currency, inst.Underlying.Name, underlyingIdns, "", inst.Underlying.ValidFrom, inst.Underlying.ValidTo)
			if err != nil {
				return resolveResult{}, err
			}
		}
		id, err := database.EnsureInstrument(ctx, inst.AssetClass, inst.Exchange, inst.Currency, inst.Name, identifiers, underlyingID, validFrom, validTo)
		if err != nil {
			return resolveResult{}, err
		}
		r := resolveResult{InstrumentID: id, FirstRowIndex: rowIndex}
		if cache != nil {
			cache[key] = r
		}
		return r, nil
	}

	// Unresolved: broker-description-only
	id, err := database.EnsureInstrument(ctx, "", "", "", instrumentDescription, []db.IdentifierInput{{Type: "BROKER_DESCRIPTION", Domain: source, Value: instrumentDescription, Canonical: false}}, "", nil, nil)
	if err != nil {
		return resolveResult{}, err
	}
	msg := MsgBrokerDescriptionOnly
	if hadTimeout {
		msg = MsgPluginTimeout
	} else if hadOtherErr {
		msg = MsgPluginUnavailable
	}
	r := resolveResult{
		InstrumentID:  id,
		FirstRowIndex: rowIndex,
		IdErr:         &db.IdentificationError{RowIndex: rowIndex, InstrumentDescription: instrumentDescription, Message: msg},
	}
	if cache != nil {
		cache[key] = r
	}
	return r, nil
}
