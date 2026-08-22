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
	// A result was found and not trusted: the whole identity was proposed and
	// nothing the source stated agreed with what it resolved to. Distinct from
	// MsgBrokerDescriptionOnly, which is nobody answering at all.
	MsgProposalUnconfirmed = "proposed identifier unconfirmed"
)

// resolveResult holds the outcome of resolving one (source, instrument_description).
type resolveResult struct {
	InstrumentID string
	// The currency line the resolution landed on, empty where nothing named one.
	// Carried so that a posting can name it; the write that does is 0149.
	ListingID     string
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
// Completeness is about which listing, and about nothing else. A source that
// named one has said the last thing that changes which instrument resolution
// lands on: the currency, the ISIN and the rest all follow from the listing, and
// an identifier plugin fills them in from its own data at no cost. A source that
// did not has left a choice open that no amount of provider lookup closes --
// a bare ticker maps to every listing of that symbol in the world, and an ISIN
// maps to every line the security trades in -- and choosing among them is what
// this stage is for.
//
// So a MIC_TICKER carrying its MIC is complete and a bare one is not, and an
// ISIN or CUSIP alone is not. Every listing-grain type that carries no domain is
// complete on its own, which is what a SEDOL and a composite FIGI are: each is
// issued per market and so names the line outright. The rest of the exceptions
// name the instrument rather than a listing of it:
//
//   - A currency or an FX pair is the cash or FX instrument, entire.
//   - A contract symbol -- OCC, OPRA, FUT_OPT -- carries its own underlying,
//     expiry, right and strike, and names its market by construction.
//   - A share class FIGI is a provider's key into the provider's own data, which
//     a model asked to improve on could only invent.
//
// A BROKER_DESCRIPTION states no security at all, so it leaves the identity as
// incomplete as it found it.
func identityComplete(stated []identifier.Identifier) bool {
	for _, id := range stated {
		switch id.Type {
		case "CURRENCY", "FX_PAIR", "OCC", "OPRA", "FUT_OPT",
			"OPENFIGI_SHARE_CLASS", "OPENFIGI_COMPOSITE", "SEDOL":
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
func runCandidatePluginsBatch(ctx context.Context, deps ingestDeps, broker, source string, items []candidate.BatchItem) (map[string]keyProposals, map[string]string, error) {
	outcomes := make(map[string]string, len(items))
	noPlugins := func() map[string]string {
		for _, item := range items {
			outcomes[item.ID] = db.TelemetryCandidateNotAttemptedNoPlugins
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
	// One filter for the whole batch, so the exchange lookups a chunk repeats are
	// paid for once.
	filter := identification.NewProposalFilter(ctx, deps.DB, ingestionLogger())
	resolved := make(map[string]bool)
	// attempted is an item that reached at least one plugin. One that reached none
	// was excluded by every plugin's type filter, which is a skip rather than a
	// failure to find anything.
	attempted := make(map[string]bool)
	anyPlugin := false
	merged := make(map[string]keyProposals)
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
		kept := make(map[string][]candidate.Proposal)
		for id, proposals := range res.Proposed {
			for _, pr := range proposals {
				filteredID, ok := filter(pr.Identifier)
				if !ok {
					continue
				}
				pr.Identifier = filteredID
				kept[id] = append(kept[id], pr)
			}
			if len(kept[id]) > 0 {
				resolved[id] = true
				call.ItemsCompleted++
				call.FieldsProposed += len(kept[id])
			}
		}
		// Written after the counts are known and before the fields that reference
		// it: a field row names its call, so the call has to exist first.
		callID := deps.writeCandidatePluginCall(ctx, call)
		for id, proposals := range kept {
			if len(proposals) == 0 {
				continue
			}
			merged[id] = keyProposals{CallID: callID, Proposals: proposals}
			ingestionLogger().DebugContext(ctx, "candidate plugin batch result: proposals", "plugin_id", c.PluginID, "batch_id", id, "instrument_description", batchItemDescByID(filtered, id), "proposals", identification.HintsSummary(proposedIdentifiers(proposals)))
		}
		if call.ItemsCompleted == 0 {
			ingestionLogger().DebugContext(ctx, "candidate plugin batch result: no hints", "plugin_id", c.PluginID, "batch_ids", batchItemIDs(filtered))
		}
	}
	if !anyPlugin {
		return nil, noPlugins(), nil
	}
	for _, item := range items {
		switch {
		case resolved[item.ID]:
			outcomes[item.ID] = db.TelemetryCandidateFieldsProposed
		case attempted[item.ID]:
			outcomes[item.ID] = db.TelemetryCandidateNothingProposed
		default:
			outcomes[item.ID] = db.TelemetryCandidateNotAttemptedTypeFilter
		}
	}
	if len(merged) > 0 {
		return merged, outcomes, nil
	}
	return nil, outcomes, nil
}

// keyProposals is what one candidate plugin offered for one resolution key, and
// the call it came from. The call travels with them because a proposed field
// names both its key and its call, and neither alone answers whether completion
// helped: the call covers many keys, and the key does not know which call spoke
// for it.
type keyProposals struct {
	CallID    string
	Proposals []candidate.Proposal
}

// proposedIdentifiers is the identifiers alone, for the resolver -- which ranks
// by value and has no use for the field a plugin filed it under.
func proposedIdentifiers(ps []candidate.Proposal) []identifier.Identifier {
	out := make([]identifier.Identifier, 0, len(ps))
	for _, p := range ps {
		out = append(out, p.Identifier)
	}
	return out
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

	// Path A: client supplied identifier_hints -- resolve by identifiers, and
	// bind to the instrument the description already names where that instrument
	// has no identity of its own.
	//
	// No persistent BROKER_DESCRIPTION identifier is created here either way. The
	// binding below passes one to EnsureInstrument only when the pre-pass found
	// the row already stored, so it matches rather than inserts: the client's
	// identifiers stay authoritative and no description-derived mapping is minted
	// to pollute later lookups (adr/0004). Whether one should be is 0106's
	// question.
	//
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
		// The identifiers named no instrument, but the description may already
		// name one that nothing has identified. Binding to it is what stops a
		// second instrument being minted beside the one already holding this
		// description's transactions -- and it is what carries the plugins'
		// identifiers on to it, since an instrument with no identity is completed
		// by what identifies it.
		descOnly := pre.descOnly[key]
		if descOnly != "" {
			ingestionLogger().InfoContext(ctx, "instrument resolution: binding to the instrument this description already names",
				"source", source, "instrument_description", instrumentDescription, "instrument_id", descOnly,
				"stated", identification.HintsSummary(identifierHints))
		}
		// No DB hit: call identifier plugins with hints.
		//
		// Any proposals are what the candidate plugins offered for the gap this
		// key's stated identifiers left -- a venue for an ISIN that named none.
		// They are passed apart from the stated ones and stay that way: they
		// choose between the listings the stated identifier produced, and
		// introduce none of their own. See adr/0057.
		return resolveWithIdentifierPlugins(ctx, database, registry, broker, source, instrumentDescription, identifier.Identity{Stated: identifierHints, Proposed: proposedIdentifiers(proposedHintsCache[key].Proposals), Hints: hints}, cache, key, rowIndex, descOnly != "", hintsValidAt, keys, db.TelemetryPurposePrimary)
	}

	// Path B: no client hints -- use pre-extracted description hints, then identifier plugins.
	// DB lookup by (source, description) and batch description extraction are handled by the
	// caller's pre-pass; DB hits are already in cache (caught above), misses proceed here.
	extractedHints := proposedIdentifiers(proposedHintsCache[key].Proposals)
	if len(extractedHints) == 0 {
		// Extraction failed: ensure broker-description-only and record error.
		// Identifier plugins are never called in this path, so no identification
		// attempt is opened and OpenFIGI is never reached.
		ingestionLogger().InfoContext(ctx, "instrument resolution: description extraction failed, using broker description only", "source", source, "instrument_description", instrumentDescription)
		// No claim: one description associates nothing with anything, and
		// nobody asserted an identity for it.
		instID, _, ensureErr := database.EnsureInstrument(ctx, "", "", "", instrumentDescription, "", "", []db.IdentifierInput{{
			Ref:       db.InstrumentRef{Type: "BROKER_DESCRIPTION", Value: instrumentDescription, Domain: source},
			Canonical: false,
		}}, nil, "", nil, nil, nil)
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
		resultByTicker, _ := resolveWithIdentifierPlugins(ctx, database, registry, broker, source, instrumentDescription, identifier.Identity{Proposed: tickerHints, Hints: hints}, nil, key, rowIndex, true, hintsValidAt, keys, db.TelemetryPurposeMismatchCheck)
		resultByFigi, _ := resolveWithIdentifierPlugins(ctx, database, registry, broker, source, instrumentDescription, identifier.Identity{Proposed: figiHints, Hints: hints}, nil, key, rowIndex, true, hintsValidAt, keys, db.TelemetryPurposeMismatchCheck)
		idByTicker := resultByTicker.InstrumentID
		idByFigi := resultByFigi.InstrumentID
		// Consider "unresolved" (broker-description-only) as empty for mismatch check
		if idByTicker != "" && idByFigi != "" && idByTicker != idByFigi {
			keys.mismatch(key, db.TelemetryMismatchFIGIvsTicker)
			ingestionLogger().ErrorContext(ctx, "MIC_TICKER and OPENFIGI_SHARE_CLASS resolved to different instruments; using MIC_TICKER",
				"source", source, "instrument_description", instrumentDescription,
				"instrument_id_by_ticker", idByTicker, "instrument_id_by_figi", idByFigi)
			hintsToUse = tickerHints
		}
	}

	// Resolve by the proposals, which the source did not state and which stay
	// marked as such: they are the only key here, so ResolveWithPlugins queries
	// them, and it is that same provenance that makes it refuse a result nothing
	// independent agrees with. See adr/0057 and adr/0059.
	//
	// The (source, description) binding is always stored when one is ensured,
	// which is exactly why the refusal matters: the binding is canonical and no
	// later upload re-examines it.
	return resolveWithIdentifierPlugins(ctx, database, registry, broker, source, instrumentDescription, identifier.Identity{Proposed: hintsToUse, Hints: hints}, cache, key, rowIndex, true, hintsValidAt, keys, db.TelemetryPurposePrimary)
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
func resolveWithIdentifierPlugins(ctx context.Context, database db.DB, registry *identifier.Registry, broker, source, instrumentDescription string, ident identifier.Identity, cache map[string]resolveResult, key string, rowIndex int32, bindSourceDescription bool, hintsValidAt *time.Time, keys *resolutionKeys, purpose string) (resolveResult, error) {
	// Ingestion-specific fallback: broker-description-only instrument.
	fallback := func(ctx context.Context, database db.DB) (string, error) {
		// The listing is dropped rather than returned: a broker description is
		// security-grain, so this instrument's line is its unknown one and
		// nothing here has learned a currency to name it with.
		id, _, err := database.EnsureInstrument(ctx, "", "", "", instrumentDescription, "", "",
			[]db.IdentifierInput{{
				Ref:       db.InstrumentRef{Type: "BROKER_DESCRIPTION", Value: instrumentDescription, Domain: source},
				Canonical: false,
			}},
			nil, "", nil, nil, nil)
		return id, err
	}

	result, err := identification.ResolveWithPlugins(ctx, database, registry, broker, source, instrumentDescription, ident, bindSourceDescription, fallback, keys.attempt(key, purpose), ingestionLogger(), 0, hintsValidAt)
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
	// Only the primary resolution's verdicts are kept. A mismatch-check probe
	// resolves the same key by half its identifiers to answer a different
	// question, and its view of a proposal is not what became of it.
	if purpose == db.TelemetryPurposePrimary {
		keys.fields(key, result.ProposalOutcomes)
	}

	r := resolveResult{InstrumentID: result.InstrumentID, ListingID: result.ListingID, FirstRowIndex: rowIndex}
	if !result.Identified {
		msg := MsgBrokerDescriptionOnly
		switch {
		case result.Unconfirmed:
			msg = MsgProposalUnconfirmed
		case result.HadTimeout:
			msg = MsgPluginTimeout
		case result.HadError:
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
