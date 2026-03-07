package ingestion

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	"github.com/leedenison/portfoliodb/server/db"
	"github.com/leedenison/portfoliodb/server/identifier"
	"github.com/leedenison/portfoliodb/server/identifier/description"
	"github.com/leedenison/portfoliodb/server/telemetry"
)

// identifierHintsFromTx converts proto identifier_hints to []identifier.Identifier for Resolve.
func identifierHintsFromTx(tx *apiv1.Tx) []identifier.Identifier {
	if tx == nil || len(tx.GetIdentifierHints()) == 0 {
		return nil
	}
	out := make([]identifier.Identifier, 0, len(tx.IdentifierHints))
	for _, h := range tx.IdentifierHints {
		if h.GetType() != "" && h.GetValue() != "" {
			out = append(out, identifier.Identifier{Type: h.GetType(), Domain: h.GetDomain(), Value: h.GetValue()})
		}
	}
	return out
}

// Resolution order: (1) DB lookup by (source, instrument_description), (2) in-batch cache,
// (3) if still unresolved, call enabled plugins in parallel (timeout from config, retry once with backoff).
// Identification errors are recorded for fallbacks and do not fail the job.

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

// resolveByHintsDBOnly looks up each hint by (type, domain, value) and returns unique instrument IDs (in order of first occurrence).
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
		if id != "" && !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	return ids, nil
}

// runDescriptionPlugins runs enabled description plugins in series by precedence; returns hints from the first that returns ≥1.
// If descRegistry is nil, returns nil (no hints) without calling the database.
// When counter is non-nil, increments description.extraction.plugin_error on plugin errors and description.extraction.no_hints when no plugin returns hints.
func runDescriptionPlugins(ctx context.Context, database db.DescriptionPluginDB, descRegistry *description.Registry, counter telemetry.CounterIncrementer, broker, source, instrumentDescription, exchangeCodeHint, currencyHint, micHint string) ([]identifier.Identifier, error) {
	if descRegistry == nil {
		return nil, nil
	}
	configs, err := database.ListEnabledDescriptionPluginConfigs(ctx)
	if err != nil {
		return nil, err
	}
	for _, c := range configs {
		p := descRegistry.Get(c.PluginID)
		if p == nil {
			continue
		}
		hints, err := p.Extract(ctx, c.Config, broker, source, instrumentDescription, exchangeCodeHint, currencyHint, micHint)
		if err != nil {
			if counter != nil {
				counter.Incr(ctx, "description.extraction.plugin_error")
			}
			slog.DebugContext(ctx, "description plugin returned error", "plugin_id", c.PluginID, "instrument_description", instrumentDescription, "err", err)
			continue // try next plugin
		}
		if len(hints) > 0 {
			return hints, nil
		}
	}
	if counter != nil {
		counter.Incr(ctx, "description.extraction.no_hints")
	}
	slog.DebugContext(ctx, "description extraction: no plugin returned hints", "source", source, "instrument_description", instrumentDescription)
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
func callPluginWithRetry(ctx context.Context, p identifier.Plugin, config []byte, broker, source, instrumentDescription, exchangeCodeHint, currencyHint, micHint string, identifierHints []identifier.Identifier, timeout time.Duration) (*identifier.Instrument, []identifier.Identifier, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	inst, ids, err := p.Identify(ctx, config, broker, source, instrumentDescription, exchangeCodeHint, currencyHint, micHint, identifierHints)
	if err == nil || errors.Is(err, identifier.ErrNotIdentified) {
		return inst, ids, err
	}
	// Retry once with backoff (use new context so retry is not cancelled by parent)
	time.Sleep(pluginRetryBackoff)
	ctx2, cancel2 := context.WithTimeout(context.Background(), timeout)
	defer cancel2()
	inst, ids, err2 := p.Identify(ctx2, config, broker, source, instrumentDescription, exchangeCodeHint, currencyHint, micHint, identifierHints)
	if err2 != nil {
		return nil, nil, err2
	}
	return inst, ids, err2
}

// Resolve resolves (source, instrumentDescription) to an instrument_id using DB, then batch cache, then (when no client identifier_hints) description plugins to extract hints, then identifier plugins.
// When client supplies identifier_hints, resolution is by identifiers only and (source, description) is not stored.
// Hints (exchangeCodeHint, currencyHint, micHint) are optional. counter is optional; when non-nil and plugins are invoked, instrument.identify.attempts is incremented.
func Resolve(ctx context.Context, database db.DB, registry *identifier.Registry, descRegistry *description.Registry, broker, source, instrumentDescription, exchangeCodeHint, currencyHint, micHint string, identifierHints []identifier.Identifier, cache map[string]resolveResult, rowIndex int32, counter telemetry.CounterIncrementer) (resolveResult, error) {
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
		return resolveWithIdentifierPlugins(ctx, database, registry, broker, source, instrumentDescription, exchangeCodeHint, currencyHint, micHint, identifierHints, cache, key, rowIndex, counter, false)
	}

	// Path B: no client hints — DB lookup by (source, description), then description plugins, then identifier plugins.
	id, err := database.FindInstrumentBySourceDescription(ctx, source, instrumentDescription)
	if err != nil {
		return resolveResult{}, err
	}
	if id != "" {
		r := resolveResult{InstrumentID: id, FirstRowIndex: rowIndex}
		if cache != nil {
			cache[key] = r
		}
		return r, nil
	}

	// Run description plugins in series; first that returns ≥1 hint wins.
	extractedHints, err := runDescriptionPlugins(ctx, database, descRegistry, counter, broker, source, instrumentDescription, exchangeCodeHint, currencyHint, micHint)
	if err != nil {
		return resolveResult{}, err
	}
	if len(extractedHints) == 0 {
		// Extraction failed: ensure broker-description-only and record error.
		// Identifier plugins are never called in this path, so no Redis counters and no OpenFIGI.
		if counter != nil {
			counter.Incr(ctx, "instrument.resolution.description_extraction_failed")
		}
		slog.InfoContext(ctx, "instrument resolution: description extraction failed, using broker description only", "source", source, "instrument_description", instrumentDescription)
		id, err = database.EnsureInstrument(ctx, "", "", "", instrumentDescription, []db.IdentifierInput{{Type: source, Domain: "", Value: instrumentDescription, Canonical: false}}, "", nil, nil)
		if err != nil {
			return resolveResult{}, err
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

	// Resolve by extracted hints; always store (source, description) when ensuring.
	return resolveWithIdentifierPlugins(ctx, database, registry, broker, source, instrumentDescription, exchangeCodeHint, currencyHint, micHint, extractedHints, cache, key, rowIndex, counter, true)
}

// resolveWithIdentifierPlugins calls enabled identifier plugins with the given hints, merges results, and ensures instrument.
// When storeSourceDescription is false (client supplied hints), (source, description) is not added to identifiers.
func resolveWithIdentifierPlugins(ctx context.Context, database db.DB, registry *identifier.Registry, broker, source, instrumentDescription, exchangeCodeHint, currencyHint, micHint string, identifierHints []identifier.Identifier, cache map[string]resolveResult, key string, rowIndex int32, counter telemetry.CounterIncrementer, storeSourceDescription bool) (resolveResult, error) {
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
		inputs = append(inputs, pluginInput{config: c, plugin: p})
	}
	if len(inputs) > 0 && counter != nil {
		counter.Incr(ctx, "instrument.identify.attempts")
	}
	if len(inputs) == 0 {
		slog.DebugContext(ctx, "instrument resolution: no enabled identifier plugins", "source", source, "instrument_description", instrumentDescription)
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
			inst, ids, err := callPluginWithRetry(ctx, in.plugin, in.config.Config, broker, source, instrumentDescription, exchangeCodeHint, currencyHint, micHint, identifierHints, timeout)
			results[idx] = result{precedence: in.config.Precedence, inst: inst, ids: ids, err: err}
		}(i)
	}
	wg.Wait()

	var winner *result
	var hadTimeout, hadOtherErr bool
	for i := range results {
		r := &results[i]
		if r.err == nil && r.inst != nil {
			if winner == nil {
				winner = r
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
			if idn.Type == source && idn.Value == instrumentDescription {
				hasSource = true
			}
		}
		if storeSourceDescription && !hasSource {
			identifiers = append(identifiers, db.IdentifierInput{Type: source, Domain: "", Value: instrumentDescription, Canonical: false})
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
	id, err := database.EnsureInstrument(ctx, "", "", "", instrumentDescription, []db.IdentifierInput{{Type: source, Domain: "", Value: instrumentDescription, Canonical: false}}, "", nil, nil)
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
