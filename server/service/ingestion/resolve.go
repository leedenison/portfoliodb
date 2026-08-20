package ingestion

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	typev1 "github.com/leedenison/portfoliodb/proto/type/v1"
	"github.com/leedenison/portfoliodb/server/db"
	"github.com/leedenison/portfoliodb/server/identifier"
	"github.com/leedenison/portfoliodb/server/identifier/candidate"
	"github.com/leedenison/portfoliodb/server/service/identification"
	"log/slog"
	"slices"
	"strings"
	"time"
)

// identifierHintsFromTx converts proto identifier_hints to []identifier.Identifier for Resolve.
// Type is converted from IdentifierType enum to string (vocabulary name). Invalid hint types are discarded and logged at debug.
func identifierHintsFromTx(ctx context.Context, tx *apiv1.Tx) []identifier.Identifier {
	if tx == nil || len(tx.GetIdentifierHints()) == 0 {
		return nil
	}
	var raw []identifier.Identifier
	for _, h := range tx.IdentifierHints {
		if h.GetType() == typev1.IdentifierType_IDENTIFIER_TYPE_UNSPECIFIED || h.GetValue() == "" {
			continue
		}
		typeStr := typev1.IdentifierType_name[int32(h.GetType())]
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
	// Which lookup answered, when the pre-pass answered from the database. A
	// description and a set of identifiers are different questions with different
	// costs, and a key that resolved by one must not be recorded as having
	// resolved by the other. Empty for a result this batch worked out.
	DBHitOutcome string
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

// identityComplete reports whether what a source stated already picks out one
// listing, so that a candidate plugin has nothing left to offer.
//
// Completeness is about the venue, and about nothing else. A source that named
// one has said the last thing that changes which instrument resolution lands on:
// the currency, the ISIN and the rest all follow from the listing, and an
// identifier plugin fills them in from its own data at no cost. A source that
// did not has left a choice open that no amount of provider lookup closes --
// a bare ticker maps to every listing of that symbol in the world, and an ISIN
// maps to every venue the security trades on -- and choosing among them is what
// this stage is for.
//
// So a MIC_TICKER carrying its MIC is complete and a bare one is not, and an
// ISIN, CUSIP or SEDOL alone is not. The exceptions name the instrument rather
// than a listing of it:
//
//   - A currency or an FX pair is the cash or FX instrument, entire.
//   - A contract symbol -- OCC, OPRA, FUT_OPT -- carries its own underlying,
//     expiry, right and strike, and names its market by construction.
//   - A FIGI is a provider's key into the provider's own data, which a model
//     asked to improve on could only invent.
//
// A BROKER_DESCRIPTION states no security at all, so it leaves the identity as
// incomplete as it found it.
func identityComplete(stated []identifier.Identifier) bool {
	for _, id := range stated {
		switch id.Type {
		case "CURRENCY", "FX_PAIR", "OCC", "OPRA", "FUT_OPT",
			"OPENFIGI_SHARE_CLASS", "OPENFIGI_COMPOSITE":
			return true
		case "MIC_TICKER", "OPENFIGI_TICKER":
			if strings.TrimSpace(id.Domain) != "" {
				return true
			}
		}
	}
	return false
}

// shortHashForBatch returns a short stable id (first 8 hex chars of SHA256) for batch description extraction response matching.
func shortHashForBatch(key string) string {
	h := sha256.Sum256([]byte(key))
	return hex.EncodeToString(h[:])[:8]
}

// batchItemIDs returns the IDs of batch items for debug logging.
func batchItemIDs(items []candidate.BatchItem) []string {
	ids := make([]string, len(items))
	for i := range items {
		ids[i] = items[i].ID
	}
	return ids
}

// batchItemDescByID returns the instrument description for the batch item with the given ID, or "" if not found.
func batchItemDescByID(items []candidate.BatchItem, id string) string {
	for i := range items {
		if items[i].ID == id {
			return items[i].InstrumentDescription
		}
	}
	return ""
}

// candidateTokens renders a plugin's token usage for storage. Nil stays nil,
// which is what keeps the token columns null rather than zero for a plugin that
// costs nothing to call.
func candidateTokens(u *candidate.Usage) *db.TelemetryTokens {
	if u == nil {
		return nil
	}
	return &db.TelemetryTokens{
		Prompt:     u.PromptTokens,
		Completion: u.CompletionTokens,
		Total:      u.TotalTokens,
	}
}

// runCandidatePluginsBatch runs candidate plugins on all items via ProposeBatch. Only items whose security type is acceptable to a plugin are passed to that plugin. First plugin that returns a non-empty map wins. Result is keyed by BatchItem.ID.
//
// It also returns what became of each item, keyed by BatchItem.ID, which is stage
// one of the item's resolution key. The not-attempted members are the skips: an
// item no enabled plugin accepted was never put to one, and an installation with
// no candidate plugins at all attempts nothing.
//
// One candidate_plugin_call row is written per ProposeBatch call, including the
// calls that fail: a plugin populates its telemetry on every path, and a call that
// errored is the one most worth having a row for. The row hangs off the run rather
// than off a key because one call covers many descriptions at once.
func runCandidatePluginsBatch(ctx context.Context, deps ingestDeps, broker, source string, items []candidate.BatchItem) (map[string][]identifier.Identifier, map[string]string, error) {
	outcomes := make(map[string]string, len(items))
	noPlugins := func() map[string]string {
		for _, item := range items {
			outcomes[item.ID] = db.TelemetryExtractionNotAttemptedNoPlugins
		}
		return outcomes
	}
	if deps.CandidateRegistry == nil || len(items) == 0 {
		return nil, noPlugins(), nil
	}
	configs, err := deps.DB.ListEnabledPluginConfigs(ctx, db.PluginCategoryCandidate)
	if err != nil {
		return nil, nil, err
	}
	resolved := make(map[string]bool)
	// attempted is an item that reached at least one plugin. One that reached none
	// was excluded by every plugin's type filter, which is a skip rather than a
	// failure to find anything.
	attempted := make(map[string]bool)
	anyPlugin := false
	merged := make(map[string][]identifier.Identifier)
	for _, c := range configs {
		p := deps.CandidateRegistry.Get(c.PluginID)
		if p == nil {
			continue
		}
		anyPlugin = true
		acceptableKinds := p.AcceptableInstrumentKinds()
		acceptableTypes := p.AcceptableSecurityTypes()
		var filtered []candidate.BatchItem
		for _, item := range items {
			if resolved[item.ID] {
				continue
			}
			if identifier.ShouldAttemptPlugin(acceptableKinds, acceptableTypes, item.Hints.InstrumentKind, item.Hints.SecurityTypeHint) {
				filtered = append(filtered, item)
				attempted[item.ID] = true
			}
		}
		if len(filtered) == 0 {
			ingestionLogger().DebugContext(ctx, "candidate plugin batch skipped (no items with acceptable security type)", "plugin_id", c.PluginID)
			continue
		}
		started := time.Now()
		res, err := p.ProposeBatch(ctx, c.Config, broker, source, filtered)
		call := db.TelemetryCandidatePluginCall{
			RunID:      deps.RunID,
			PluginID:   c.PluginID,
			Precedence: c.Precedence,
			BatchSize:  len(filtered),
			Outcome:    string(res.Telemetry.Outcome),
			Tokens:     candidateTokens(res.Telemetry.Tokens),
			Duration:   time.Since(started),
		}
		if err != nil {
			// A plugin that returned an error without saying how it went is not
			// meant to happen, but the row is worth more than the exactness here.
			if call.Outcome == "" {
				call.Outcome = string(candidate.OutcomeError)
			}
			deps.writeCandidatePluginCall(ctx, call)
			ingestionLogger().DebugContext(ctx, "candidate plugin batch result: error", "plugin_id", c.PluginID, "err", err)
			continue
		}
		out := res.Proposed
		hasAny := false
		for id, proposals := range out {
			ids := make([]identifier.Identifier, 0, len(proposals))
			for _, pr := range proposals {
				ids = append(ids, pr.Identifier)
			}
			filteredHints := identification.FilterProposals(ctx, deps.DB, ids, ingestionLogger())
			if len(filteredHints) > 0 {
				merged[id] = filteredHints
				resolved[id] = true
				hasAny = true
				call.ItemsWithHints++
			}
		}
		deps.writeCandidatePluginCall(ctx, call)
		if hasAny {
			for id := range out {
				if len(merged[id]) > 0 {
					ingestionLogger().DebugContext(ctx, "candidate plugin batch result: proposals", "plugin_id", c.PluginID, "batch_id", id, "instrument_description", batchItemDescByID(filtered, id), "proposals", identification.HintsSummary(merged[id]))
				}
			}
		} else {
			ingestionLogger().DebugContext(ctx, "candidate plugin batch result: no hints", "plugin_id", c.PluginID, "batch_ids", batchItemIDs(filtered))
		}
	}
	if !anyPlugin {
		return nil, noPlugins(), nil
	}
	for _, item := range items {
		switch {
		case resolved[item.ID]:
			outcomes[item.ID] = db.TelemetryExtractionHintsFound
		case attempted[item.ID]:
			outcomes[item.ID] = db.TelemetryExtractionNoHints
		default:
			outcomes[item.ID] = db.TelemetryExtractionNotAttemptedTypeFilter
		}
	}
	if len(merged) > 0 {
		return merged, outcomes, nil
	}
	return nil, outcomes, nil
}

// Resolve resolves (source, instrumentDescription) to an instrument_id using the
// batch cache, then the candidate plugins' proposals for the key, then the
// identifier plugins. Where the source stated identifiers the proposals rank
// among the listings those produced; where it stated none they are the only key
// there is.
//
// pre is what proposeCandidates worked out, and arrives whole: the resolutions,
// the conflicts and the proposals are answers to one question asked once, and a
// caller holding the first without the second would resolve a conflicting key
// through the plugins instead of raising it. Both database lookups happen there,
// so this asks the database nothing a key has already been looked up by.
// When client supplies identifier_hints, resolution is by identifiers only and (source, description) is not persisted
// to the DB as a BROKER_DESCRIPTION identifier (though results are still cached in the in-memory batch cache).
// hints are optional (exchange, currency, MIC, security type).
func Resolve(ctx context.Context, database db.DB, registry *identifier.Registry, broker, source, instrumentDescription string, hints identifier.Hints, identifierHints []identifier.Identifier, pre prePass, rowIndex int32, hintsValidAt *time.Time, keys *resolutionKeys) (resolveResult, error) {
	cache, conflicts, proposedHintsCache := pre.resolved, pre.conflicts, pre.proposed
	key := cacheKeyWithHints(source, instrumentDescription, identifierHints)
	if cache != nil {
		if r, ok := cache[key]; ok {
			// A hit nothing in this batch resolved came from the pre-pass, and
			// carries which lookup answered it -- by description or by the
			// identifiers the source stated. One this batch did resolve has
			// already stamped its key, and the first stamp is the one that stands.
			outcome := r.DBHitOutcome
			if outcome == "" {
				outcome = db.TelemetryResolutionDBSourceDescription
			}
			keys.end(ctx, key, outcome, r.InstrumentID)
			return r, nil
		}
	}

	// Path A: client supplied identifier_hints -- resolve by identifiers only.
	// "Do not store" means no persistent BROKER_DESCRIPTION identifier is
	// created in the DB (the `false` arg to resolveWithIdentifierPlugins).
	// The in-memory batch cache above is still used to avoid repeated DB
	// lookups within the same upload.
	if len(identifierHints) > 0 {
		// The pre-pass did the lookup: a single match is in the cache and was
		// returned above, and more than one is recorded here. Asking again would
		// be the same query for the same answer.
		if conflicts[key] {
			keys.end(ctx, key, db.TelemetryResolutionConflictingHints, "")
			return resolveResult{}, fmt.Errorf("conflicting identifier hints resolve to different instruments")
		}
		// No DB hit: call identifier plugins with hints; do not persist (source, description) as BROKER_DESCRIPTION.
		//
		// Any proposals are what the candidate plugins offered for the gap this
		// key's stated identifiers left -- a venue for an ISIN that named none.
		// They are passed apart from the stated ones and stay that way: they
		// choose between the listings the stated identifier produced, and
		// introduce none of their own. See adr/0057.
		return resolveWithIdentifierPlugins(ctx, database, registry, broker, source, instrumentDescription, identifier.Identity{Stated: identifierHints, Proposed: proposedHintsCache[key], Hints: hints}, cache, key, rowIndex, false, hintsValidAt, keys, db.TelemetryPurposePrimary)
	}

	// Path B: no client hints -- use pre-extracted description hints, then identifier plugins.
	// DB lookup by (source, description) and batch description extraction are handled by the
	// caller's pre-pass; DB hits are already in cache (caught above), misses proceed here.
	var extractedHints []identifier.Identifier
	if proposedHintsCache != nil {
		extractedHints = proposedHintsCache[key]
	}
	if len(extractedHints) == 0 {
		// Extraction failed: ensure broker-description-only and record error.
		// Identifier plugins are never called in this path, so no identification
		// attempt is opened and OpenFIGI is never reached.
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
		keys.end(ctx, key, db.TelemetryResolutionExtractionFailed, instID)
		return r, nil
	}

	// When extraction returned both MIC_TICKER and OPENFIGI_SHARE_CLASS, resolve each separately and validate they match.
	// If they resolve to different instruments, record the mismatch on the key, log
	// the error, and use MIC_TICKER.
	hintsToUse := extractedHints
	tickerHints := hintsByType(extractedHints, "MIC_TICKER")
	figiHints := hintsByType(extractedHints, "OPENFIGI_SHARE_CLASS")
	if len(tickerHints) > 0 && len(figiHints) > 0 {
		// Resolve with a nil cache: these two are probes, so they must not pollute
		// it. They do record an attempt each, named for what they are -- the pair
		// is real work against real plugins, and calling them mismatch_check is
		// what stops them inflating the denominator of a failure rate over primary
		// attempts.
		resultByTicker, _ := resolveWithIdentifierPlugins(ctx, database, registry, broker, source, instrumentDescription, identifier.Identity{Stated: tickerHints, Hints: hints}, nil, key, rowIndex, true, hintsValidAt, keys, db.TelemetryPurposeMismatchCheck)
		resultByFigi, _ := resolveWithIdentifierPlugins(ctx, database, registry, broker, source, instrumentDescription, identifier.Identity{Stated: figiHints, Hints: hints}, nil, key, rowIndex, true, hintsValidAt, keys, db.TelemetryPurposeMismatchCheck)
		idByTicker := resultByTicker.InstrumentID
		idByFigi := resultByFigi.InstrumentID
		// Consider "unresolved" (broker-description-only) as empty for mismatch check
		if idByTicker != "" && idByFigi != "" && idByTicker != idByFigi {
			keys.mismatch(key)
			ingestionLogger().ErrorContext(ctx, "MIC_TICKER and OPENFIGI_SHARE_CLASS resolved to different instruments; using MIC_TICKER",
				"source", source, "instrument_description", instrumentDescription,
				"instrument_id_by_ticker", idByTicker, "instrument_id_by_figi", idByFigi)
			hintsToUse = tickerHints
		}
	}

	// Resolve by (validated) hints; always store (source, description) when ensuring.
	return resolveWithIdentifierPlugins(ctx, database, registry, broker, source, instrumentDescription, identifier.Identity{Stated: hintsToUse, Hints: hints}, cache, key, rowIndex, true, hintsValidAt, keys, db.TelemetryPurposePrimary)
}

// hintDiffsSummary formats hint diffs as a readable string (e.g. "Currency: USD->EUR, ISIN: US037->US038").
func hintDiffsSummary(diffs []identifier.HintDiff) string {
	var b strings.Builder
	for i, d := range diffs {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(d.Field)
		b.WriteString(": ")
		b.WriteString(d.HintValue)
		b.WriteString("->")
		b.WriteString(d.ResolvedValue)
	}
	return b.String()
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
// purpose names the attempt this call records. Only the primary one stamps the
// key: the mismatch-check probes run against the same key and would otherwise
// stamp it with their own answer before the resolution that decides it.
func resolveWithIdentifierPlugins(ctx context.Context, database db.DB, registry *identifier.Registry, broker, source, instrumentDescription string, ident identifier.Identity, cache map[string]resolveResult, key string, rowIndex int32, storeSourceDescription bool, hintsValidAt *time.Time, keys *resolutionKeys, purpose string) (resolveResult, error) {
	// Ingestion-specific fallback: broker-description-only instrument.
	fallback := func(ctx context.Context, database db.DB) (string, error) {
		return database.EnsureInstrument(ctx, "", "", "", instrumentDescription, "", "",
			[]db.IdentifierInput{{Type: "BROKER_DESCRIPTION", Domain: source, Value: instrumentDescription, Canonical: false}},
			"", nil, nil, nil)
	}

	result, err := identification.ResolveWithPlugins(ctx, database, registry, broker, source, instrumentDescription, ident, storeSourceDescription, fallback, keys.attempt(key, purpose), ingestionLogger(), 0, hintsValidAt)
	if err != nil {
		return resolveResult{}, err
	}
	if len(result.HintDiffs) > 0 {
		summary := hintDiffsSummary(result.HintDiffs)
		ingestionLogger().InfoContext(ctx, "instrument resolved with hint differences",
			"source", source,
			"instrument_description", instrumentDescription,
			"instrument_id", result.InstrumentID,
			"diffs", summary,
		)
		if purpose == db.TelemetryPurposePrimary {
			keys.hintDiff(key, summary)
		}
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
	if purpose == db.TelemetryPurposePrimary {
		keys.end(ctx, key, resolutionOutcome(result), r.InstrumentID)
	}
	return r, nil
}
