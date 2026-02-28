package ingestion

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/leedenison/portfoliodb/server/db"
	"github.com/leedenison/portfoliodb/server/identifier"
)

// Resolution order: (1) DB lookup by (broker, instrument_description), (2) in-batch cache,
// (3) if still unresolved, call enabled plugins in parallel (timeout from config, retry once with backoff).
// Identification errors are recorded for fallbacks and do not fail the job.

const (
	defaultPluginTimeout = 30 * time.Second
	pluginRetryBackoff   = 2 * time.Second
)

// Distinct messages for identification errors (per spec).
const (
	MsgBrokerDescriptionOnly = "broker description only"
	MsgPluginTimeout         = "plugin timeout"
	MsgPluginUnavailable     = "plugin unavailable"
)

// resolveResult holds the outcome of resolving one (broker, instrument_description).
type resolveResult struct {
	InstrumentID   string
	IdErr          *db.IdentificationError
	FirstRowIndex  int32
}

// cacheKey returns a key for the batch cache. Same (broker, description) in a batch resolves once.
func cacheKey(broker, instrumentDescription string) string {
	return broker + "\x00" + instrumentDescription
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
func callPluginWithRetry(ctx context.Context, p identifier.Plugin, broker, instrumentDescription string, timeout time.Duration) (*identifier.Instrument, []identifier.Identifier, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	inst, ids, err := p.Identify(ctx, broker, instrumentDescription)
	if err == nil || errors.Is(err, identifier.ErrNotIdentified) {
		return inst, ids, err
	}
	// Retry once with backoff (use new context so retry is not cancelled by parent)
	time.Sleep(pluginRetryBackoff)
	ctx2, cancel2 := context.WithTimeout(context.Background(), timeout)
	defer cancel2()
	inst, ids, err2 := p.Identify(ctx2, broker, instrumentDescription)
	if err2 != nil {
		return nil, nil, err2
	}
	return inst, ids, err2
}

// Resolve resolves (broker, instrumentDescription) to an instrument_id using DB, then batch cache, then enabled plugins.
// If cache is non-nil and has an entry for the key, that result is returned without calling plugins.
// Otherwise plugins are called in parallel (each with its own timeout from config), results merged by precedence;
// on fallback (broker-description-only), idErr is set with a distinct message and the job is not failed.
func Resolve(ctx context.Context, database db.DB, registry *identifier.Registry, broker, instrumentDescription string, cache map[string]resolveResult, rowIndex int32) (resolveResult, error) {
	key := cacheKey(broker, instrumentDescription)
	if cache != nil {
		if r, ok := cache[key]; ok {
			return r, nil
		}
	}

	// 1) DB lookup
	id, err := database.FindInstrumentByBrokerDescription(ctx, broker, instrumentDescription)
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

	// 2) Call enabled plugins in parallel
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

	// Precedence is already descending from ListEnabledPluginConfigs.
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
			inst, ids, err := callPluginWithRetry(ctx, in.plugin, broker, instrumentDescription, timeout)
			results[idx] = result{precedence: in.config.Precedence, inst: inst, ids: ids, err: err}
		}(i)
	}
	wg.Wait()

	// Merge: use highest-precedence successful result for instrument metadata; merge identifiers from all
	// successful plugins so that for each identifier type the value from the highest-precedence plugin wins.
	// Results align with inputs, which are already ordered by precedence desc.
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
		// Merge identifiers: for each type, take the value from the first (highest-precedence) plugin that provided it.
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
		hasBroker := false
		for _, idn := range mergedIds {
			identifiers = append(identifiers, db.IdentifierInput{Type: idn.Type, Value: idn.Value, Canonical: true})
			if idn.Type == broker && idn.Value == instrumentDescription {
				hasBroker = true
			}
		}
		if !hasBroker {
			identifiers = append(identifiers, db.IdentifierInput{Type: broker, Value: instrumentDescription, Canonical: false})
		}
		inst := winner.inst
		id, err := database.EnsureInstrument(ctx, inst.AssetClass, inst.Exchange, inst.Currency, inst.Name, identifiers)
		if err != nil {
			return resolveResult{}, err
		}
		r := resolveResult{InstrumentID: id, FirstRowIndex: rowIndex}
		if cache != nil {
			cache[key] = r
		}
		return r, nil
	}

	// Unresolved: broker-description-only and record identification error.
	// Use instrumentDescription as name so the instrument row has a human-readable label.
	id, err = database.EnsureInstrument(ctx, "", "", "", instrumentDescription, []db.IdentifierInput{{Type: broker, Value: instrumentDescription, Canonical: false}})
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
		IdErr: &db.IdentificationError{
			RowIndex:               rowIndex,
			InstrumentDescription: instrumentDescription,
			Message:                msg,
		},
	}
	if cache != nil {
		cache[key] = r
	}
	return r, nil
}
