package identification

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"time"

	backoff "github.com/cenkalti/backoff/v4"
	"github.com/leedenison/portfoliodb/server/db"
	"github.com/leedenison/portfoliodb/server/derivative"
	"github.com/leedenison/portfoliodb/server/identifier"
	"github.com/leedenison/portfoliodb/server/telemetry"
)

const (
	DefaultPluginTimeout = 30 * time.Second
	MaxResolveDepth      = 2
)

// PluginRetryBackoff is the delay before retrying a failed plugin call. Variable (not const) so tests can shorten it.
var PluginRetryBackoff = 2 * time.Second

// ResolveResult holds the outcome of plugin-based instrument resolution.
type ResolveResult struct {
	InstrumentID string
	HadTimeout   bool // at least one plugin timed out
	HadError     bool // at least one plugin returned a non-ErrNotIdentified error
	Identified   bool // a plugin successfully identified the instrument
}

// FallbackFunc is called when no identifier plugin resolves the instrument.
// It must return an instrument ID, typically by calling EnsureInstrument.
type FallbackFunc func(ctx context.Context, database db.DB) (string, error)

// ResolveByHintsDBOnly looks up each hint by (type, domain, value) and returns unique instrument IDs (in order of first occurrence).
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
func ResolveByHintsDBOnly(ctx context.Context, database db.InstrumentDB, hints []identifier.Identifier) ([]string, error) {
	seen := make(map[string]bool)
	var ids []string
	for _, h := range hints {
		if h.Type == "" || h.Value == "" {
			continue
		}
		// Normalize OCC to compact form (DB stores compact).
		value := h.Value
		if h.Type == "OCC" {
			if compact, ok := derivative.OCCCompact(value); ok {
				value = compact
			}
		}
		id, err := database.FindInstrumentByIdentifier(ctx, h.Type, h.Domain, value)
		if err != nil {
			return nil, err
		}
		if id == "" && h.Domain == "" {
			id, err = database.FindInstrumentByTypeAndValue(ctx, h.Type, value)
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

// FilterIdentifierHints keeps only hints whose Type is in the controlled vocabulary (identifier.AllowedIdentifierTypes).
// Invalid types are discarded and logged at debug.
func FilterIdentifierHints(ctx context.Context, hints []identifier.Identifier, logger *slog.Logger) []identifier.Identifier {
	if len(hints) == 0 {
		return nil
	}
	l := resolveLogger(logger)
	out := make([]identifier.Identifier, 0, len(hints))
	for _, h := range hints {
		typ := strings.TrimSpace(h.Type)
		if typ == "" {
			continue
		}
		if identifier.AllowedIdentifierTypes[typ] {
			out = append(out, h)
		} else {
			l.DebugContext(ctx, "identifier hint discarded: type not in vocabulary", "type", typ, "value", h.Value)
		}
	}
	return out
}

// ResolveWithPlugins calls enabled identifier plugins with the given hints, merges results, and ensures the instrument.
// When storeSourceDescription is true and a plugin succeeds, (source, instrumentDescription) is added as a
// non-canonical BROKER_DESCRIPTION identifier. If no plugin identifies the instrument, fallback is called.
// depth tracks recursion for underlying resolution; callers pass 0.
func ResolveWithPlugins(
	ctx context.Context,
	database db.DB,
	registry *identifier.Registry,
	broker, source, instrumentDescription string,
	hints identifier.Hints,
	identifierHints []identifier.Identifier,
	storeSourceDescription bool,
	fallback FallbackFunc,
	counter telemetry.CounterIncrementer,
	logger *slog.Logger,
	depth int,
) (ResolveResult, error) {
	l := resolveLogger(logger)

	// Adjust OCC hints for known stock splits before any lookups.
	identifierHints = AdjustOCCForKnownSplits(ctx, database, identifierHints)

	// If all hints already resolve to one instrument in DB, use it (avoids plugin call).
	ids, err := ResolveByHintsDBOnly(ctx, database, identifierHints)
	if err != nil {
		return ResolveResult{}, err
	}
	if len(ids) == 1 {
		return ResolveResult{InstrumentID: ids[0], Identified: true}, nil
	}

	configs, err := database.ListEnabledPluginConfigs(ctx, db.PluginCategoryIdentifier)
	if err != nil {
		return ResolveResult{}, err
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
		if !identifier.ShouldAttemptPlugin(p.AcceptableInstrumentKinds(), p.AcceptableSecurityTypes(), hints.InstrumentKind, hints.SecurityTypeHint) {
			continue
		}
		inputs = append(inputs, pluginInput{config: c, plugin: p})
	}
	if len(inputs) > 0 && counter != nil {
		counter.Incr(ctx, "instruments.resolution.totals.identify.attempts")
	}
	if len(inputs) == 0 {
		l.DebugContext(ctx, "instrument resolution: no enabled identifier plugins", "source", source, "instrument_description", instrumentDescription)
	}

	// Winner selection relies on inputs being ordered by precedence (descending),
	// which is guaranteed by ListEnabledPluginConfigs. The first successful
	// result in iteration order wins.
	type result struct {
		inst *identifier.Instrument
		ids  []identifier.Identifier
		err  error
	}
	results := make([]result, len(inputs))
	var wg sync.WaitGroup
	for i := range inputs {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			in := inputs[idx]
			timeout := timeoutFromConfig(in.config.Config)
			inst, ids, err := callPluginWithRetry(ctx, in.plugin, in.config.Config, broker, source, instrumentDescription, hints, identifierHints, timeout, PluginRetryBackoff)
			results[idx] = result{inst: inst, ids: ids, err: err}
		}(i)
	}
	wg.Wait()

	for i := range results {
		in := inputs[i]
		r := &results[i]
		if r.err != nil {
			l.DebugContext(ctx, "identifier plugin result", "plugin_id", in.config.PluginID, "instrument_description", instrumentDescription, "err", r.err)
		} else if r.inst != nil {
			l.DebugContext(ctx, "identifier plugin result", "plugin_id", in.config.PluginID, "instrument_description", instrumentDescription, "instrument_name", r.inst.Name, "instrument", instrumentSummary(r.inst), "identifiers", HintsSummary(r.ids))
		} else {
			l.DebugContext(ctx, "identifier plugin result", "plugin_id", in.config.PluginID, "instrument_description", instrumentDescription, "result", "not_identified")
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
		l.DebugContext(ctx, "identifier plugin chosen", "plugin_id", inputs[winnerIdx].config.PluginID, "instrument_description", instrumentDescription, "instrument_name", winner.inst.Name)
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
		if len(inst.UnderlyingIdentifiers) > 0 && depth < MaxResolveDepth {
			uHints := identifier.Hints{
				SecurityTypeHint: identifier.UnderlyingSecTypeHint(inst.AssetClass),
			}
			uIdnHints := make([]identifier.Identifier, len(inst.UnderlyingIdentifiers))
			copy(uIdnHints, inst.UnderlyingIdentifiers)
			// Underlying resolution: no source description, no fallback needed (use nil).
			uResult, uErr := ResolveWithPlugins(ctx, database, registry, broker, source, "", uHints, uIdnHints, false, nil, counter, logger, depth+1)
			if uErr != nil {
				l.WarnContext(ctx, "underlying resolution failed", "instrument_description", instrumentDescription, "err", uErr)
			} else if uResult.InstrumentID != "" {
				underlyingID = uResult.InstrumentID
			}
		}
		var optFields *db.OptionFields
		if inst.AssetClass == db.AssetClassOption {
			optFields = optionFieldsFromIdentifiers(mergedIds)
		}
		id, err := database.EnsureInstrument(ctx, inst.AssetClass, inst.Exchange, inst.Currency, inst.Name, inst.CIK, inst.SICCode, identifiers, underlyingID, validFrom, validTo, optFields)
		if err != nil {
			return ResolveResult{}, err
		}
		return ResolveResult{InstrumentID: id, Identified: true}, nil
	}

	// Unresolved: call fallback if provided.
	if fallback != nil {
		id, err := fallback(ctx, database)
		if err != nil {
			return ResolveResult{}, err
		}
		return ResolveResult{InstrumentID: id, HadTimeout: hadTimeout, HadError: hadOtherErr}, nil
	}
	return ResolveResult{HadTimeout: hadTimeout, HadError: hadOtherErr}, nil
}

// HintsSummary returns a short summary of hints for debug logging (e.g. "TICKER:AAPL, FIGI:...").
func HintsSummary(hints []identifier.Identifier) string {
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

// pluginConfigJSON is the shape we read from identifier_plugin_config.config (JSONB).
type pluginConfigJSON struct {
	TimeoutSeconds *int `json:"timeout_seconds"`
}

// timeoutFromConfig parses config JSON and returns timeout; uses default if missing or invalid.
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

// callPluginWithRetry calls Identify with exponential backoff retry.
// ErrNotIdentified is treated as a permanent error (no retry). Each attempt gets its own
// context timeout derived from the parent so cancellation still propagates.
func callPluginWithRetry(ctx context.Context, p identifier.Plugin, config []byte, broker, source, instrumentDescription string, hints identifier.Hints, identifierHints []identifier.Identifier, timeout, initialBackoff time.Duration) (*identifier.Instrument, []identifier.Identifier, error) {
	var inst *identifier.Instrument
	var ids []identifier.Identifier

	bo := backoff.NewExponentialBackOff()
	bo.InitialInterval = initialBackoff
	bo.MaxElapsedTime = 0 // controlled by MaxRetries, not elapsed time
	bCtx := backoff.WithContext(backoff.WithMaxRetries(bo, 1), ctx)

	err := backoff.Retry(func() error {
		attemptCtx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		var attemptErr error
		inst, ids, attemptErr = p.Identify(attemptCtx, config, broker, source, instrumentDescription, hints, identifierHints)
		if attemptErr == nil {
			return nil
		}
		if errors.Is(attemptErr, identifier.ErrNotIdentified) {
			return backoff.Permanent(attemptErr)
		}
		return attemptErr
	}, bCtx)

	return inst, ids, err
}

// optionFieldsFromIdentifiers extracts strike, expiry, and put/call from the
// OCC identifier in the merged identifier set. Returns nil when no valid OCC
// is found.
func optionFieldsFromIdentifiers(ids []identifier.Identifier) *db.OptionFields {
	for _, idn := range ids {
		if idn.Type != "OCC" {
			continue
		}
		// DB stores compact form; pad to 21-char for ParseOptionTicker.
		padded, ok := derivative.OCCPadded(idn.Value)
		if !ok {
			continue
		}
		parsed, ok := derivative.ParseOptionTicker(padded)
		if !ok {
			continue
		}
		if parsed.Strike <= 0 || parsed.Expiry.IsZero() || parsed.PutCall == "" {
			continue
		}
		return &db.OptionFields{
			Strike:  parsed.Strike,
			Expiry:  parsed.Expiry,
			PutCall: parsed.PutCall,
		}
	}
	return nil
}

// resolveLogger returns the provided logger or falls back to slog.Default().
func resolveLogger(l *slog.Logger) *slog.Logger {
	if l != nil {
		return l
	}
	return slog.Default()
}
