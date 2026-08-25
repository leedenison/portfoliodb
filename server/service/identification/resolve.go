package identification

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"time"

	backoff "github.com/cenkalti/backoff/v4"
	"github.com/leedenison/portfoliodb/server/currency"
	"github.com/leedenison/portfoliodb/server/db"
	"github.com/leedenison/portfoliodb/server/derivative"
	"github.com/leedenison/portfoliodb/server/identifier"
	"github.com/leedenison/portfoliodb/server/pluginutil"
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
	// ListingID is the currency line the resolution landed on, which is a
	// separate answer from which security it landed on: a security listed in GBP
	// and in USD is one security and two lines, and a holding in one is not
	// fungible with a holding in the other.
	//
	// Empty where nothing named a line. That is not a failure -- the security is
	// still resolved -- it says the currency is unknown, and a posting reaching
	// it names no line.
	ListingID  string
	HadTimeout bool                  // at least one plugin timed out
	HadError   bool                  // at least one plugin returned a non-ErrNotIdentified error
	Identified bool                  // a plugin successfully identified the instrument
	HintDiffs  []identifier.HintDiff // differences between supplied hints and resolved instrument
	// Unconfirmed reports that a result was found and then dropped: the whole
	// identity was a proposal and nothing independent agreed with what it
	// resolved to. The instrument is the broker-description-only fallback, so it
	// is not Identified either -- the two say different things, and a caller
	// reporting why a row has no ticker needs to tell "nobody answered" from "an
	// answer was found and not trusted".
	Unconfirmed bool
	// Confirmed names the fields the chosen result agreed with, among the things
	// the caller knew independently of any proposal: the currency and security
	// type the source stated, and the identifiers it stated.
	//
	// A proposal is deliberately not counted. Where a source stated nothing, the
	// proposal is the only key the plugins were given, so a provider echoing it
	// back confirms that the value names some security and not that it names
	// this one -- counting it would make every guess self-confirming. Empty
	// therefore means "nothing independent agreed", which is what
	// 0132 acts on. See adr/0057.
	Confirmed []string
	// ProposalOutcomes says what the resolution was able to establish about each
	// identifier the caller proposed, in the order they were given. It is what
	// 0134 records, and it says nothing about whether a guess was any good --
	// only whether anything checked it.
	ProposalOutcomes []ProposalOutcome
}

// ProposalOutcome is one proposed identifier and what became of it.
type ProposalOutcome struct {
	Identifier identifier.Identifier
	Outcome    string
}

// ResolvedInstrument holds an instrument ID plus the metadata needed for
// hint comparison, as returned by ResolveByHintsDBOnly.
//
// Currencies rather than a currency, because how many lines the identifier
// reached is what the grain decides: a ticker names one, an ISIN names every line
// of the security it identifies. A stated currency is checked for membership in
// the set, so a security quoted in two currencies contradicts a file naming
// either of them and contradicts a file naming a third.
//
// No exchange. A venue we do not hold is one nobody has told us about rather than
// a disagreement, so there is nothing here to check one against. See
// docs/adr/0077-a-venue-set-is-what-we-know-not-what-exists.md.
type ResolvedInstrument struct {
	ID         string
	AssetClass string
	Currencies []string
}

// isDerivativeClass reports whether an asset class is one the schema requires an
// underlying line for.
func isDerivativeClass(assetClass string) bool {
	return assetClass == db.AssetClassOption || assetClass == db.AssetClassFuture
}

// identifierTypesOf is the type of each identifier, for the vocabulary-level
// questions that do not care what any one of them spells.
func identifierTypesOf(ids []identifier.Identifier) []string {
	out := make([]string, len(ids))
	for i, idn := range ids {
		out[i] = idn.Type
	}
	return out
}

// FallbackFunc is called when no identifier plugin resolves the instrument.
// It must return an instrument ID, typically by calling EnsureInstrument.
type FallbackFunc func(ctx context.Context, database db.DB) (string, error)

// NewProposalFilter narrows a candidate plugin's output to what may be acted on.
//
// It is FilterIdentifierHints plus the checks that need reference data, which is
// why it takes a database and lives beside the resolver rather than in the
// plugin: a candidate plugin has no database access, deliberately, so nothing it
// returns has been checked against the exchange table.
//
// A MIC nothing recognises loses the domain and keeps the ticker. A good symbol
// under a venue that does not exist is still worth having, and dropping the pair
// would throw away the half that was right. A recognised one is normalised to
// its operating MIC so it is compared and stored at the grain everything else
// uses (adr/0003).
//
// It returns a function applied to one identifier at a time rather than to a
// slice, so a caller keeps whatever it has attached to each -- the field it was
// proposed for and the plugin's confidence in it, which telemetry records
// alongside the value and which a filtered slice would have separated from it.
// Lookups are memoised across the returned function's whole life: a batch
// proposes the same handful of venues over and over, and the exchange table does
// not change underneath it.
func NewProposalFilter(ctx context.Context, database db.InstrumentDB, logger *slog.Logger) func(identifier.Identifier) (identifier.Identifier, bool) {
	l := resolveLogger(logger)
	known := make(map[string]string)
	// operatingMIC returns the operating MIC for a domain, or "" when the MIC is
	// not one the exchange table carries.
	operatingMIC := func(mic string) string {
		if v, ok := known[mic]; ok {
			return v
		}
		v := ""
		if ok, err := database.ValidateMIC(ctx, mic); err == nil && ok {
			if op, err := database.LookupOperatingMIC(ctx, mic); err == nil && op != "" {
				v = op
			} else {
				v = mic
			}
		}
		known[mic] = v
		return v
	}
	return func(in identifier.Identifier) (identifier.Identifier, bool) {
		out := FilterIdentifierHints(ctx, []identifier.Identifier{in}, l)
		if len(out) == 0 {
			return identifier.Identifier{}, false
		}
		id := out[0]
		if id.Type != "MIC_TICKER" || id.Domain == "" {
			return id, true
		}
		op := operatingMIC(id.Domain)
		if op == "" {
			l.DebugContext(ctx, "candidate proposed an unknown exchange; keeping the ticker without it",
				"mic", id.Domain, "ticker", id.Value)
			id.Domain = ""
			return id, true
		}
		id.Domain = op
		return id, true
	}
}

// ResolveByHintsDBOnly looks up each hint by (type, domain, value) and returns unique instrument IDs (in order of first occurrence).
//
// Fallback when domain is empty: If the hint has domain == "" and the exact (type, domain, value) lookup finds nothing,
// we perform a second lookup by (type, value) only, ignoring domain. That allows e.g. (TICKER, "", "AAPL") to match
// a stored row (TICKER, "US", "AAPL"). We do this because:
//   - Empty domain means the user supplied only a ticker (no exchange), or we only extracted a ticker from the
//     instrument description (e.g. a candidate plugin returned "AAPL" with no exchange). In those cases the user
//     is effectively saying "resolve this to any valid ticker/exchange combo."
//   - In storage we persist TICKER with domain set to the exchange code (e.g. "US" for US exchanges). So
//     FindInstrumentByIdentifier(type, "", value) looks for domain IS NULL and does not match those rows. The
//     fallback FindInstrumentByTypeAndValue(type, value) matches any domain; if exactly one instrument has that
//     (type, value), we use it. If multiple instruments match (same ticker on different exchanges), the fallback
//     returns "" and we do not resolve (ambiguous).
//
// Only identifiers a source stated may be passed here. A lookup that a
// proposal satisfied would resolve the instrument without any plugin having
// confirmed the proposal, which is the one thing a proposal must never do.
// See adr/0057.
func ResolveByHintsDBOnly(ctx context.Context, database db.InstrumentDB, hints []identifier.Identifier) ([]ResolvedInstrument, error) {
	seen := make(map[string]bool)
	var resolved []ResolvedInstrument
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
		id, ac, curs, err := database.FindInstrumentWithMetaByIdentifier(ctx, h.Type, h.Domain, value)
		if err != nil {
			return nil, err
		}
		if id == "" && h.Domain == "" {
			id, err = database.FindInstrumentByTypeAndValue(ctx, h.Type, value)
			if err != nil {
				return nil, err
			}
			// An underlying hint built from an OCC root spells a multi-class
			// ticker without its separator, so the exact lookups above miss the
			// instrument that the same import resolved from its own posting
			// minutes earlier. Compare with the separator dropped on both sides
			// before concluding it is not there.
			if id == "" && h.Type == "MIC_TICKER" {
				id, err = database.FindInstrumentByTickerIgnoringSeparators(ctx, value)
				if err != nil {
					return nil, err
				}
			}
			// TypeAndValue fallback doesn't return metadata. Empty fields
			// cause CompareHints to skip the currency and assetClass checks,
			// so hint validation is not performed for instruments matched by
			// type+value without an exact domain match.
			ac, curs = "", nil
		}
		if id != "" && !seen[id] {
			seen[id] = true
			resolved = append(resolved, ResolvedInstrument{ID: id, AssetClass: ac, Currencies: curs})
		}
	}
	return resolved, nil
}

// ResolveIDsByHintsDBOnly is a lightweight variant of ResolveByHintsDBOnly that
// returns only instrument IDs (no metadata). It uses FindInstrumentByIdentifier
// (index-only lookup) instead of FindInstrumentWithMetaByIdentifier (JOIN),
// making it cheaper for callers that don't need hint comparison.
// Only identifiers a source stated may be passed here. A lookup that a
// proposal satisfied would resolve the instrument without any plugin having
// confirmed the proposal, which is the one thing a proposal must never do.
// See adr/0057.
func ResolveIDsByHintsDBOnly(ctx context.Context, database db.InstrumentDB, hints []identifier.Identifier) ([]string, error) {
	seen := make(map[string]bool)
	var ids []string
	for _, h := range hints {
		if h.Type == "" || h.Value == "" {
			continue
		}
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
			// An underlying hint built from an OCC root spells a multi-class
			// ticker without its separator, so the exact lookups above miss the
			// instrument that the same import resolved from its own posting
			// minutes earlier. Compare with the separator dropped on both sides
			// before concluding it is not there.
			if id == "" && h.Type == "MIC_TICKER" {
				id, err = database.FindInstrumentByTickerIgnoringSeparators(ctx, value)
				if err != nil {
					return nil, err
				}
			}
		}
		if id != "" && !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	return ids, nil
}

// FilterIdentifierHints keeps only hints whose Type is in the controlled vocabulary (identifier.Known).
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
		if identifier.Known(typ) {
			out = append(out, h)
		} else {
			l.DebugContext(ctx, "identifier hint discarded: type not in vocabulary", "type", typ, "value", h.Value)
		}
	}
	return out
}

// pluginResult holds a single plugin's identification output. tel is the half
// of the plugin-call record only the plugin can supply; the other half -- won,
// superseded or discarded as inconsistent -- is decided by winner selection
// below, once every plugin has returned.
type pluginResult struct {
	inst *identifier.Instrument
	ids  []identifier.Identifier
	// filtered is what the call constrained the provider to. It is graded with
	// ids when deciding what the result claimed, and is never stored: the value
	// was corroborated, not returned. See adr/0060.
	filtered []identifier.Identifier
	tel      identifier.Telemetry
	stats    callStats
	err      error
}

// claim is what one result asserted: the identifiers it named together, either
// by returning them or by strictly filtering on them.
func claim(r *pluginResult) db.IdentityClaim {
	c := db.IdentityClaim{Identifiers: make([]db.ClaimedIdentifier, 0, len(r.ids)+len(r.filtered))}
	for _, idn := range r.ids {
		c.Identifiers = append(c.Identifiers, claimed(idn, db.ClaimRoleReturned))
	}
	for _, idn := range r.filtered {
		c.Identifiers = append(c.Identifiers, claimed(idn, db.ClaimRoleFiltered))
	}
	return c
}

// claimed is the domain triple as the store names it. The two are separate types
// because db cannot import the package holding identifier.Identifier without a
// cycle, so the crossing is explicit and happens here.
func claimed(idn identifier.Identifier, role string) db.ClaimedIdentifier {
	return db.ClaimedIdentifier{
		Ref:  db.InstrumentRef{Type: idn.Type, Value: idn.Value, Domain: idn.Domain},
		Role: role,
	}
}

// flattenClaims is the lossy step, kept to one place so that what it loses is
// visible. For each subject it takes the value from the first claim carrying it
// -- the claims arrive in precedence-descending order -- and drops which result
// said what, which is exactly the distinction that makes an association a claim
// rather than a set. It is what the instrument is stored with, and 0140 is what
// stops the merge being decided on it.
//
// The subject and not the type, because a domain scopes a value rather than
// decorating it: MIC_TICKER/XNAS/VOD and MIC_TICKER/XLON/VOD are what the line
// is called at two venues, and keying on the type would keep whichever came
// first and silently lose the other. A line's venue set is derived from these
// rows (adr/0068), so the venue would go with it -- and it went whether the two
// arrived from two results or from one result naming both, which is a winner's
// own answer being trimmed on the way to the store. Domains normalize to
// operating MICs first, per adr/0003, so a segment and the venue that operates
// it are one subject here as they are in sameSubject.
//
// Only returned identifiers are flattened. A filtered value was corroborated
// rather than supplied, and writing it is a decision about what an admitted
// claim licenses, which is 0140's.
func flattenClaims(ctx context.Context, normalizeMIC MICNormalizer, claims []db.IdentityClaim) []identifier.Identifier {
	// What sameSubject compares, as a map key. Upper-cased because sameSubject
	// folds case and a key cannot.
	type subject struct{ typ, domain string }

	seen := make(map[subject]bool)
	var out []identifier.Identifier
	for _, c := range claims {
		for _, idn := range c.Identifiers {
			if idn.Role != db.ClaimRoleReturned {
				continue
			}
			id := identifier.Identifier{Type: idn.Ref.Type, Domain: idn.Ref.Domain, Value: idn.Ref.Value}
			s := subject{id.Type, strings.ToUpper(domainOf(ctx, normalizeMIC, id))}
			if seen[s] {
				continue
			}
			seen[s] = true
			out = append(out, id)
		}
	}
	return out
}

// Attempt is where one ResolveWithPlugins call records itself: the run it belongs
// to and the resolution key it resolves. Purpose distinguishes the primary call
// from the two the MIC_TICKER against OPENFIGI_SHARE_CLASS mismatch check makes
// and from underlying recursion, which are otherwise indistinguishable.
//
// Depth is not a field. It is already a parameter of the call, so holding it here
// too would let the two disagree.
//
// The zero Attempt records nothing, which is what a caller outside a run passes.
type Attempt struct {
	DB      db.TelemetryDB
	RunID   string
	KeyID   string
	Purpose string
}

// write records a finished attempt and returns its id, for the plugin calls
// written under it. The scope fills itself in, so the caller states only what it
// learned. An empty id is an attempt that was not recorded, and the writer
// already skips the children of one.
func (a Attempt) write(ctx context.Context, r db.TelemetryIdentificationAttempt) string {
	if a.DB == nil || a.KeyID == "" {
		return ""
	}
	r.RunID = a.RunID
	r.ResolutionKeyID = a.KeyID
	r.Purpose = a.Purpose
	return a.DB.WriteIdentificationAttempt(ctx, r)
}

// writeCall records one plugin invocation under an attempt, and the identifiers
// that call claimed under it.
//
// Every call that identified something gets claim rows, whichever outcome the
// orchestrator settled on: a result discarded as inconsistent still made a
// claim, and it is exactly the discarded ones a contradiction is read from. A
// call that got no answer claims nothing -- its filter matched nothing, so the
// provider asserted nothing.
func (a Attempt) writeCall(ctx context.Context, attemptID, pluginID, outcome string, stats callStats, r *pluginResult) {
	if a.DB == nil || attemptID == "" {
		return
	}
	callID := a.DB.WriteIdentifierPluginCall(ctx, db.TelemetryIdentifierPluginCall{
		RunID:     a.RunID,
		AttemptID: attemptID,
		PluginID:  pluginID,
		Outcome:   outcome,
		Retries:   stats.Retries,
		Duration:  stats.Duration,
	})
	if callID == "" || r == nil || r.inst == nil {
		return
	}
	for _, c := range claim(r).Identifiers {
		a.DB.WriteIdentifierClaim(ctx, db.TelemetryIdentifierClaim{
			RunID:  a.RunID,
			CallID: callID,
			Ref:    c.Ref,
			Role:   c.Role,
		})
	}
}

// underlying returns the scope an underlying resolution runs under: the same run
// and the same key, since it is the same description being resolved, but named as
// the recursion it is.
func (a Attempt) underlying() Attempt {
	a.Purpose = db.TelemetryPurposeUnderlying
	return a
}

// MICNormalizer maps a MIC to its operating MIC. Returns the input unchanged
// if normalization fails (unknown MIC, DB error, etc.).
type MICNormalizer func(ctx context.Context, mic string) string

// MICLookup is the subset of db.InstrumentDB needed to build a MICNormalizer
// and a country resolver.
type MICLookup interface {
	LookupOperatingMIC(ctx context.Context, mic string) (string, error)
	LookupMICCountry(ctx context.Context, mic string) (string, error)
}

// MICCountry resolves a MIC to the ISO 3166 country it is in, returning "" when
// the answer is unknown. It is what lets a provider that named a market be
// compared with one that named a venue.
type MICCountry func(mic string) string

// NewDBMICCountry returns a MICCountry backed by a database lookup. Results are
// memoised for the life of the resolver: one resolution asks about the same
// handful of MICs repeatedly, and the reference table does not change under it.
func NewDBMICCountry(ctx context.Context, db MICLookup) MICCountry {
	seen := make(map[string]string)
	return func(mic string) string {
		if mic == "" {
			return ""
		}
		if c, ok := seen[mic]; ok {
			return c
		}
		c, err := db.LookupMICCountry(ctx, mic)
		if err != nil {
			c = ""
		}
		seen[mic] = c
		return c
	}
}

// NewDBMICNormalizer returns a MICNormalizer backed by a database lookup.
func NewDBMICNormalizer(db MICLookup) MICNormalizer {
	return func(ctx context.Context, mic string) string {
		if mic == "" {
			return mic
		}
		op, err := db.LookupOperatingMIC(ctx, mic)
		if err != nil {
			return mic
		}
		return op
	}
}

// normalizeMICValue applies the normalizer if non-nil, otherwise returns mic unchanged.
func normalizeMICValue(ctx context.Context, normalizeMIC MICNormalizer, mic string) string {
	if normalizeMIC == nil || mic == "" {
		return mic
	}
	return normalizeMIC(ctx, mic)
}

// mismatch is what excluded a result from a merge, ready for the log line.
type mismatch struct{ field, winner, other string }

// lineMismatch reports whether two results described different lines, and what
// said so.
//
// The currency decides. A line is a currency of a security (adr/0068), so two
// results stating currencies of one family have described one line however many
// venues they name between them, and two stating different families have
// described two. One resolution answers about one line: it writes the
// identifiers it gathered against the line its winner named, so a result about
// another line has nothing to contribute to this one and its identifiers must
// not be filed under this currency. Unioning two results into two listings is
// adr/0071.
//
// The venue stands in where the currency is silent, and only there. A result
// naming a market or a venue and no currency has still said where it trades,
// and taking another plugin's names onto it is how a London ticker comes to sit
// on a New York instrument for a price plugin to fetch the wrong line's close
// against. Both sides normalize to operating MICs first, per adr/0003, so a
// segment and the venue that operates it are one answer.
//
// Strict rather than open-world because both sides are whole answers rather
// than partial records; see adr/0077.
func lineMismatch(ctx context.Context, normalizeMIC MICNormalizer, countryOf MICCountry, winner, other *identifier.Instrument) *mismatch {
	wc, oc := winner.Listing.Currency, other.Listing.Currency
	if wc != "" && oc != "" {
		if !sameCurrency(wc, oc) {
			return &mismatch{"Currency", wc, oc}
		}
		return nil
	}
	wv := normalizeVenue(ctx, normalizeMIC, winner.Listing.Venue)
	ov := normalizeVenue(ctx, normalizeMIC, other.Listing.Venue)
	if !wv.Agrees(ov, countryOf) {
		return &mismatch{"Venue", venueString(wv), venueString(ov)}
	}
	return nil
}

// idMismatch reports whether other's identifiers contradict winner's, and which
// pair did.
//
// A difference in the value under one subject is the whole of what an
// identifier can contradict. A listing-grain domain used to contradict on its
// own account, two venues being read as two lines; a line is a currency
// (adr/0068), and every plugin sets that domain from the venue it put on the
// answer, so the test restated lineMismatch's venue comparison on a field that
// does not key a line -- and restated it in a spelling, a composite's US
// against a venue's XLON, that no normalization reaches.
//
// Every pair, rather than a map keyed on the type: a result may name one type
// twice -- a ticker on each of two venues -- and keying would keep whichever
// came last and compare the other against nothing.
//
// An identifier the winner also named is agreement, and agreement anywhere in
// the winner's answer settles it. Reading the conflicting half alone would have
// a winner that named one subject twice reject a result naming either of its
// values, which is the winner disagreeing with itself.
func idMismatch(ctx context.Context, normalizeMIC MICNormalizer, winner, other []identifier.Identifier) *mismatch {
	for _, o := range other {
		var conflict identifier.Identifier
		agreed, found := false, false
		for _, w := range winner {
			if !sameSubject(ctx, normalizeMIC, w, o) {
				continue
			}
			if w.Value == o.Value {
				agreed = true
				break
			}
			if !found {
				conflict, found = w, true
			}
		}
		if !agreed && found {
			return &mismatch{"Identifier:" + o.Type, conflict.String(), o.String()}
		}
	}
	return nil
}

// consistentWith reports whether other's answer may be merged into winner's,
// and logs what excluded it when it may not.
//
// Merge admission asks two things, and they are different questions. Whether
// the two results described one line, which the currency decides. And whether
// they contradict each other about the security, which the identifiers do.
func consistentWith(ctx context.Context, l *slog.Logger, winnerPlugin, otherPlugin string, winner, other *pluginResult, normalizeMIC MICNormalizer, countryOf MICCountry) bool {
	m := lineMismatch(ctx, normalizeMIC, countryOf, winner.inst, other.inst)
	if m == nil {
		m = idMismatch(ctx, normalizeMIC, winner.ids, other.ids)
	}
	if m == nil {
		return true
	}
	resolveLogger(l).WarnContext(ctx, "identifier plugin mismatch, excluding from merge",
		"winner_plugin", winnerPlugin, "other_plugin", otherPlugin,
		"field", m.field, "winner_value", m.winner, "other_value", m.other)
	return false
}

// securityClaim is what a result said about which security it resolved: the
// identifiers of security grain it returned, and the ones the call was strictly
// filtered on.
//
// The two are one set here because they are one claim. A provider that answers
// "no identifier found" when its filter matches nothing has asserted that the
// filtered value denotes the security it described, so a value it filtered on
// and deliberately did not echo back says what a returned one says (adr/0060).
func securityClaim(r *pluginResult) []identifier.Identifier {
	var out []identifier.Identifier
	for _, idn := range r.ids {
		if idn.Value != "" && identifier.CorroboratesSecurity(idn.Type) {
			out = append(out, idn)
		}
	}
	for _, idn := range r.filtered {
		if idn.Value != "" && identifier.CorroboratesSecurity(idn.Type) {
			out = append(out, idn)
		}
	}
	return out
}

// corroborated reports whether something named the security both results
// describe, and logs what each offered when nothing did.
//
// Agreeing about the line is not evidence about the security. The identity a
// resolution starts from is routinely ambiguous -- a broker file names a symbol
// and a currency and no venue, and one symbol is quoted in one currency in more
// than one place -- so where two providers can disagree at all, each has picked
// one listing out of several the query admits. That they agree about the
// currency is the query restated rather than evidence they picked the same
// security. Only a value that names a security carries that, and only where both
// of them named it.
//
// So the loser crosses on one security-grain identifier the two share, and
// contributes nothing where there is none: not its identifiers of either grain,
// not the fields it would have filled. See adr/0078.
func corroborated(ctx context.Context, l *slog.Logger, winnerPlugin, otherPlugin string, winner, other *pluginResult, normalizeMIC MICNormalizer) bool {
	w, o := securityClaim(winner), securityClaim(other)
	for _, a := range w {
		for _, b := range o {
			if a.Value == b.Value && sameSubject(ctx, normalizeMIC, a, b) {
				return true
			}
		}
	}
	// Info rather than Warn. A mismatch is two providers disagreeing and worth
	// a reader's attention; this is the ordinary shape of two answers to an
	// ambiguous query, and with plugins whose identifier vocabularies do not
	// overlap it is most of them. Logging it as a warning would bury the
	// mismatches. The telemetry row carries it either way.
	resolveLogger(l).InfoContext(ctx, "identifier plugin named no security in common, excluding from merge",
		"winner_plugin", winnerPlugin, "other_plugin", otherPlugin,
		"winner_value", HintsSummary(w), "other_value", HintsSummary(o))
	return false
}

// fillBlanks copies fields src knows and dst does not. Only empty fields are
// written: adr/0004 makes the identifier the source of truth for an instrument,
// so a value already present is never replaced, and the first result to fill a
// field wins because they arrive in precedence order.
//
// The asset class is deliberately not among them. It decides which invariants
// the row must satisfy -- an OPTION needs an underlying and strike, expiry and
// put_call, all derived from the winner -- so adopting one plugin's class over
// another's structure is how a write comes to violate a constraint. Every
// plugin sets a class anyway, so there is nothing to fill.
func fillBlanks(ctx context.Context, normalizeMIC MICNormalizer, countryOf MICCountry, dst *identifier.Instrument, src *identifier.Instrument) {
	if src == nil {
		return
	}
	// A venue is adopted only where dst named no venue and permits this one. A
	// market-level answer is not an absence of opinion: taking a venue outside
	// the country it named would replace a real constraint with a value that
	// contradicts it. The market itself is kept when nothing better arrives.
	//
	// A result admitted on its currency may still name a venue this refuses, and
	// that is coherent rather than a gap. The currency said the two described one
	// line; this says a market cannot be restated as a venue outside it. Nothing
	// is lost either way: the venue arrived as the domain of a MIC_TICKER, and
	// recompute_listing_venues derives the line's venue set from those rows
	// rather than from this field.
	if dst.Listing.Venue.MIC == "" && src.Listing.Venue.MIC != "" &&
		dst.Listing.Venue.Permits(normalizeMICValue(ctx, normalizeMIC, src.Listing.Venue.MIC), countryOf) {
		dst.Listing.Venue.MIC = src.Listing.Venue.MIC
	}
	if dst.Listing.Venue.Country == "" {
		dst.Listing.Venue.Country = src.Listing.Venue.Country
	}
	// The currency travels like any other field the winner left blank. It used
	// to travel only from a result that named the same venue, because with one
	// row per instrument a currency learned from the London line would be
	// asserted of the New York one and the venue was the only thing available to
	// stop it. There is now a row per line, and the currency is what names the
	// line rather than something the venue scopes: two venues quoting one
	// currency are one listing (adr/0068), so conditioning this on the venue
	// would refuse a fact about the line from a result that named the line.
	//
	// What still guards it is consistentWith, which has already excluded any
	// result whose currency is of another family than the winner's, or -- where
	// one of them stated none -- whose venue the winner's answer does not permit.
	if dst.Listing.Currency == "" {
		dst.Listing.Currency = src.Listing.Currency
	}
	if dst.Name == "" {
		dst.Name = src.Name
	}
	if dst.CIK == "" {
		dst.CIK = src.CIK
	}
	if dst.SICCode == "" {
		dst.SICCode = src.SICCode
	}
}

// normalizeVenue maps a venue's MIC to its operating MIC so that two answers are
// compared at the same grain, per adr/0003. A market-level answer has no MIC and
// passes through unchanged.
func normalizeVenue(ctx context.Context, normalizeMIC MICNormalizer, v identifier.Venue) identifier.Venue {
	v.MIC = normalizeMICValue(ctx, normalizeMIC, v.MIC)
	return v
}

// venueString renders a venue for a log line.
func venueString(v identifier.Venue) string {
	switch {
	case v.MIC != "":
		return v.MIC
	case v.Country != "":
		return v.Country + " (market)"
	default:
		return ""
	}
}

// domainOf is an identifier's domain, at the grain comparison happens on.
//
// A MIC_TICKER's domain is a MIC and normalizes to its operating one, per
// adr/0003, so a segment and the venue that operates it are one domain rather
// than two. No other domain is a MIC -- an OPENFIGI_TICKER's is a composite
// exchange code, a description's is the source that wrote it -- and normalizing
// one would be a lookup that can only fail.
func domainOf(ctx context.Context, normalizeMIC MICNormalizer, id identifier.Identifier) string {
	if id.Type == "MIC_TICKER" {
		return normalizeMICValue(ctx, normalizeMIC, id.Domain)
	}
	return id.Domain
}

// sameSubject reports whether two identifiers are about the same thing, so that
// their values can be compared at all.
//
// The domain scopes the value rather than decorating it, so it is part of the
// subject: MIC_TICKER/XNAS/AAPL says what this security is called at NASDAQ and
// MIC_TICKER/XLON/AAPL says what it is called in London, and two answers have
// not agreed about either by both saying AAPL. Whether those two venues are one
// line is a question about the currency, and lineMismatch asks it (adr/0068).
// An identifier naming no domain names no particular venue, which is not the
// same subject as one that does -- comparing them would be reading a bare
// ticker as though it had said where.
func sameSubject(ctx context.Context, normalizeMIC MICNormalizer, a, b identifier.Identifier) bool {
	return a.Type == b.Type &&
		strings.EqualFold(domainOf(ctx, normalizeMIC, a), domainOf(ctx, normalizeMIC, b))
}

// CompareHints compares supplied hints and identifier hints against the
// resolved instrument and its identifiers, returning any differences.
// Fields are skipped when either side is empty or UNKNOWN. normalizeMIC
// (if non-nil) maps a MIC_TICKER's domain to its operating MIC while deciding
// whether two identifiers name the same subject, so that segment-vs-operating
// differences do not read as different names.
//
// This is the shape for a resolution that named one line -- a plugin result. What
// the database answers directly reaches lines at whichever grain the identifier
// had, and is compared by CompareDBMeta below.
func CompareHints(ctx context.Context, hints identifier.Hints, identifierHints []identifier.Identifier, inst *identifier.Instrument, resolvedIDs []identifier.Identifier, normalizeMIC MICNormalizer) []identifier.HintDiff {
	if inst == nil {
		return nil
	}
	var diffs []identifier.HintDiff

	// Currency.
	if hints.Currency != "" && inst.Listing.Currency != "" &&
		!sameCurrency(hints.Currency, inst.Listing.Currency) {
		diffs = append(diffs, identifier.HintDiff{Field: "Currency", HintValue: hints.Currency, ResolvedValue: inst.Listing.Currency})
	}

	// SecurityType (same vocabulary as AssetClass).
	if hints.SecurityTypeHint != "" && hints.SecurityTypeHint != identifier.SecurityTypeHintUnknown &&
		inst.AssetClass != "" && inst.AssetClass != identifier.SecurityTypeHintUnknown &&
		!strings.EqualFold(hints.SecurityTypeHint, inst.AssetClass) {
		diffs = append(diffs, identifier.HintDiff{Field: "SecurityType", HintValue: hints.SecurityTypeHint, ResolvedValue: inst.AssetClass})
	}

	// No Exchange difference is reported, and none can be. A venue set is what we
	// have been told about rather than what exists, so a source naming a venue we
	// do not hold has told us something new; and a source naming one we do hold
	// has agreed. Neither is a contradiction, which leaves nothing for the test to
	// find -- and a test that can only pass is not a test. Currency carries the
	// weight instead: a line is a currency, so a source stating the wrong one has
	// named the wrong line, which is what a difference here means. See
	// docs/adr/0077-a-venue-set-is-what-we-know-not-what-exists.md.

	// Identifier values: compare client-supplied hints against resolved
	// identifiers, on the subject they share rather than on the type alone.
	//
	// On the subject the two share, which for a ticker is the venue as well as
	// the type. A resolution that landed on another venue is not a difference at
	// all, for the reason given above, so there is nothing here to report about
	// it; a row naming the ticker type whose two values are the same string
	// would be a venue difference restated as noise.
	for _, h := range identifierHints {
		if h.Value == "" {
			continue
		}
		for _, id := range resolvedIDs {
			if id.Value == "" || !sameSubject(ctx, normalizeMIC, h, id) {
				continue
			}
			if id.Value != h.Value {
				diffs = append(diffs, identifier.HintDiff{Field: h.Type, HintValue: h.Value, ResolvedValue: id.Value})
			}
			break
		}
	}

	return diffs
}

// resultMatchesHints returns true when the plugin result has zero hint diffs
// and at least one hint field was actually confirmed (both sides non-empty).
// This prevents sparse results from vacuously "matching" because they lack data.
func resultMatchesHints(ctx context.Context, hints identifier.Hints, identifierHints []identifier.Identifier, r *pluginResult, normalizeMIC MICNormalizer) bool {
	if r.inst == nil {
		return false
	}
	diffs := CompareHints(ctx, hints, identifierHints, r.inst, r.ids, normalizeMIC)
	if len(diffs) != 0 {
		return false
	}
	// The venue is tested here although CompareHints reports no difference for
	// one, and the two are not in conflict. This ranks one answer against another
	// -- what a source named against what a plugin answered -- which is the strict
	// side of adr/0077, while a hint difference is a claim about our own partial
	// record and is the permissive side. A result naming a venue no stated
	// MIC_TICKER names has answered about a different line, and a guess must not
	// promote it.
	if r.inst.Listing.Venue.MIC != "" && venueStated(identifierHints) {
		return venueAgrees(ctx, normalizeMIC, r.inst.Listing.Venue.MIC, identifierHints)
	}
	// At least one hint field must have been compared (both sides non-empty).
	if hints.Currency != "" && r.inst.Listing.Currency != "" {
		return true
	}
	if hints.SecurityTypeHint != "" && hints.SecurityTypeHint != identifier.SecurityTypeHintUnknown &&
		r.inst.AssetClass != "" && r.inst.AssetClass != identifier.SecurityTypeHintUnknown {
		return true
	}
	// Same subject test CompareHints used, because this asks whether that
	// comparison happened at all. Read on the type alone it would answer yes for
	// a stated ticker and a resolved one on another venue -- a comparison the
	// rule above says was never made -- and a plugin would win the stated tier
	// on it.
	for _, h := range identifierHints {
		if h.Value == "" {
			continue
		}
		for _, id := range r.ids {
			if id.Value != "" && sameSubject(ctx, normalizeMIC, h, id) {
				return true
			}
		}
	}
	return false
}

// CompareDBMeta is CompareHints for the answer the database gives on its own,
// where no plugin ran and the metadata came off the row the identifier matched.
//
// It differs from CompareHints in the one way the grain forces. A plugin answers
// about one line and so has one currency to be compared against; the database
// answers about whatever lines the identifier reached, which is one line for a
// ticker and every line of the security for an ISIN. So the currency test is
// membership rather than equality, on currency family: a security quoted in GBP
// and USD contradicts a source stating CHF and contradicts neither of the two it
// holds, and GBX is GBP under a different unit prefix.
//
// A security holding no line says nothing about a currency, which is not the same
// as saying the stated one is wrong, so it reports nothing.
//
// No exchange, for the reason CompareHints gives: the database returns no venue
// because there is no venue claim it could contradict.
func CompareDBMeta(hints identifier.Hints, r ResolvedInstrument) []identifier.HintDiff {
	var diffs []identifier.HintDiff
	if hints.Currency != "" && len(r.Currencies) > 0 && !holdsCurrency(r.Currencies, hints.Currency) {
		diffs = append(diffs, identifier.HintDiff{
			Field:         "Currency",
			HintValue:     hints.Currency,
			ResolvedValue: strings.Join(r.Currencies, ", "),
		})
	}
	if hints.SecurityTypeHint != "" && hints.SecurityTypeHint != identifier.SecurityTypeHintUnknown &&
		r.AssetClass != "" && r.AssetClass != identifier.SecurityTypeHintUnknown &&
		!strings.EqualFold(hints.SecurityTypeHint, r.AssetClass) {
		diffs = append(diffs, identifier.HintDiff{Field: "SecurityType", HintValue: hints.SecurityTypeHint, ResolvedValue: r.AssetClass})
	}
	return diffs
}

// ConfirmedDBFields is confirmedFields for that same answer: what the database
// corroborated rather than what it contradicted. A stated currency naming one of
// the lines the identifier reached was checked and held.
func ConfirmedDBFields(hints identifier.Hints, r ResolvedInstrument) []string {
	var out []string
	if hints.Currency != "" && holdsCurrency(r.Currencies, hints.Currency) {
		out = append(out, "Currency")
	}
	if hints.SecurityTypeHint != "" && hints.SecurityTypeHint != identifier.SecurityTypeHintUnknown &&
		r.AssetClass != "" && r.AssetClass != identifier.SecurityTypeHintUnknown &&
		strings.EqualFold(hints.SecurityTypeHint, r.AssetClass) {
		out = append(out, "SecurityType")
	}
	return out
}

// sameCurrency reports whether two codes name one line's currency.
//
// On family and not on the code: GBX is GBP under a different unit prefix, so a
// line quoted in one is the line the other names and a security never holds
// both (adr/0068). Every currency comparison in this package goes through here,
// because a rule about what makes two lines cannot hold in one place and not
// another -- a source stating GBX would then contradict on the plugin path what
// it corroborates on the database path.
func sameCurrency(a, b string) bool {
	return currency.Family(strings.ToUpper(a)) == currency.Family(strings.ToUpper(b))
}

// holdsCurrency reports whether one of these lines is quoted in the currency
// stated.
func holdsCurrency(currencies []string, stated string) bool {
	for _, c := range currencies {
		if sameCurrency(c, stated) {
			return true
		}
	}
	return false
}

// venueStated reports whether any of these identifiers named a venue.
func venueStated(idns []identifier.Identifier) bool {
	for _, h := range idns {
		if h.Type == "MIC_TICKER" && h.Domain != "" {
			return true
		}
	}
	return false
}

// venueAgrees reports whether mic is one of the venues these identifiers named.
//
// Every one of them is consulted, not just the first: a source that named two
// listings has said the security trades on both, and an answer landing on either
// agrees with it. Stopping at the first would turn the order they arrived in into
// a verdict. Both sides are normalised to operating MICs, so a segment MIC and
// the operating MIC it belongs to are the same venue.
func venueAgrees(ctx context.Context, normalizeMIC MICNormalizer, mic string, idns []identifier.Identifier) bool {
	want := normalizeMICValue(ctx, normalizeMIC, mic)
	for _, h := range idns {
		if h.Type != "MIC_TICKER" || h.Domain == "" {
			continue
		}
		if strings.EqualFold(normalizeMICValue(ctx, normalizeMIC, h.Domain), want) {
			return true
		}
	}
	return false
}

// confirmedFields names what a result agreed with, among the things the caller
// knew independently of any proposal.
//
// It is CompareHints read the other way round. CompareHints reports what the
// result contradicted; this reports what it corroborated, and the two are not
// complements: a field neither side supplied appears in neither list. That gap
// is the whole point. A result that contradicts nothing because it says almost
// nothing has confirmed nothing, and 0132 needs to tell that apart from a result
// that was actually checked and held up.
//
// stated must be what a source stated and never what a plugin proposed. Where a
// source stated nothing, the proposal was the only key the identifier plugins
// were given, so it coming back is the provider agreeing with itself.
func confirmedFields(ctx context.Context, hints identifier.Hints, stated []identifier.Identifier, inst *identifier.Instrument, resolvedIDs []identifier.Identifier, normalizeMIC MICNormalizer) []string {
	if inst == nil {
		return nil
	}
	var out []string
	// The currency counts, and it is the strongest field here on the path where
	// the whole key was guessed.
	//
	// It counts because a provider that returns a currency has either measured it
	// or been made to agree with it. OpenFIGI is the second kind: its mapping call
	// filters on the currency it is given and answers "No identifier found" when
	// the security has no listing in it, so a non-empty response is the provider
	// asserting that this security trades in that currency, and the plugin records
	// the hint on that basis rather than echoing it. See adr/0059.
	//
	// That is what makes it a real test of a guessed ticker: the currency comes
	// off the transaction, not off the guess, so a ticker naming the wrong company
	// only survives if that company also trades in the currency the source stated.
	if hints.Currency != "" && inst.Listing.Currency != "" && sameCurrency(hints.Currency, inst.Listing.Currency) {
		out = append(out, "Currency")
	}
	if hints.SecurityTypeHint != "" && hints.SecurityTypeHint != identifier.SecurityTypeHintUnknown &&
		inst.AssetClass != "" && inst.AssetClass != identifier.SecurityTypeHintUnknown &&
		strings.EqualFold(hints.SecurityTypeHint, inst.AssetClass) {
		out = append(out, "SecurityType")
	}
	// Every stated MIC_TICKER: a source that named two listings corroborates the
	// resolution by either of them, and stopping at the first would turn the order
	// the hints arrived in into a verdict.
	//
	// Corroboration survives the open-world reading of a venue where contradiction
	// does not, which is why this arm stands and CompareHints' does not. A stated
	// venue appearing among the ones the result named is a thing that was checked
	// and held; a stated venue absent from them is a thing nobody has told us,
	// which is not a finding. Empty where the database answered on its own: it
	// returns no venue, so there was nothing to check. See
	// docs/adr/0077-a-venue-set-is-what-we-know-not-what-exists.md.
	if inst.Listing.Venue.MIC != "" && venueAgrees(ctx, normalizeMIC, inst.Listing.Venue.MIC, stated) {
		out = append(out, "Exchange")
	}
	// Corroboration is on the whole triple. A stated ticker is confirmed by a
	// result naming that ticker on that venue, and not by one naming the symbol
	// somewhere else: the venue is half of what was stated, so a result that
	// agreed with the other half has agreed with a thing nobody said.
	for _, h := range stated {
		if h.Value == "" {
			continue
		}
		for _, id := range resolvedIDs {
			if id.Value == h.Value && sameSubject(ctx, normalizeMIC, h, id) {
				out = append(out, h.Type)
				break
			}
		}
	}
	return out
}

// proposalOutcomes says what the resolution established about each proposal.
//
// The verdict is about the identifier as a whole, venue included, because that is
// what was proposed: a ticker offered on a named venue is one claim about one
// listing, and confirming the symbol while the venue disagrees is not a
// confirmation of what was said.
//
// inst nil means nothing was resolved, so nothing could be checked.
func proposalOutcomes(ctx context.Context, proposed []identifier.Identifier, inst *identifier.Instrument, resolvedIDs []identifier.Identifier, normalizeMIC MICNormalizer) []ProposalOutcome {
	if len(proposed) == 0 {
		return nil
	}
	out := make([]ProposalOutcome, 0, len(proposed))
	if inst == nil {
		for _, p := range proposed {
			out = append(out, ProposalOutcome{Identifier: p, Outcome: db.TelemetryCandidateFieldUnused})
		}
		return out
	}
	byType := make(map[string]string, len(resolvedIDs))
	for _, id := range resolvedIDs {
		if id.Value != "" {
			byType[id.Type] = id.Value
		}
	}
	for _, p := range proposed {
		out = append(out, ProposalOutcome{Identifier: p, Outcome: proposalOutcome(ctx, p, inst, byType, normalizeMIC)})
	}
	return out
}

func proposalOutcome(ctx context.Context, p identifier.Identifier, inst *identifier.Instrument, byType map[string]string, normalizeMIC MICNormalizer) string {
	if p.Type == "CURRENCY" {
		switch {
		case inst.Listing.Currency == "":
			return db.TelemetryCandidateFieldUntested
		case sameCurrency(inst.Listing.Currency, p.Value):
			return db.TelemetryCandidateFieldConfirmed
		default:
			return db.TelemetryCandidateFieldContradicted
		}
	}
	resolved, carried := byType[p.Type]
	if carried && !strings.EqualFold(resolved, p.Value) {
		return db.TelemetryCandidateFieldContradicted
	}
	// The venue half of the claim, checked whether or not the symbol was carried:
	// a proposal naming the wrong venue is contradicted even by a result that
	// says nothing about the symbol.
	if p.Domain != "" && inst.Listing.Venue.MIC != "" {
		if !strings.EqualFold(normalizeMICValue(ctx, normalizeMIC, p.Domain), normalizeMICValue(ctx, normalizeMIC, inst.Listing.Venue.MIC)) {
			return db.TelemetryCandidateFieldContradicted
		}
		if carried {
			return db.TelemetryCandidateFieldConfirmed
		}
	}
	if carried {
		return db.TelemetryCandidateFieldConfirmed
	}
	return db.TelemetryCandidateFieldUntested
}

// ResolveWithPlugins calls enabled identifier plugins with the given hints, merges results, and ensures the instrument.
// If no plugin identifies the instrument, fallback is called.
// depth tracks recursion for underlying resolution; callers pass 0.
//
// bindSourceDescription puts (source, instrumentDescription) among the identifiers
// the instrument is ensured with, as a non-canonical BROKER_DESCRIPTION. What that
// does depends on whether the row is already there, and both callers want one of
// the two: on the description path it is not, so the mapping is stored and every
// later upload of that description resolves by lookup; on the hinted path the
// caller only sets it having found the row already stored, so it matches instead
// and binds the result to the instrument that description already names. Either
// way it is the same act -- naming the description as one of the identifiers this
// instrument is known by.
func ResolveWithPlugins(
	ctx context.Context,
	database db.DB,
	registry *identifier.Registry,
	broker, source, instrumentDescription string,
	ident identifier.Identity,
	bindSourceDescription bool,
	fallback FallbackFunc,
	tel Attempt,
	logger *slog.Logger,
	depth int,
	hintsValidAt *time.Time,
) (ResolveResult, error) {
	l := resolveLogger(logger)
	normMIC := NewDBMICNormalizer(database)
	countryOf := NewDBMICCountry(ctx, database)
	// Stated is what a source said and is the only thing allowed to stand in for
	// a resolution: it satisfies the database lookup below, and it is the only
	// thing that may raise conflicting hints. A proposal is tested by the
	// plugins, never trusted ahead of them. See adr/0057.
	hints, identifierHints := ident.Hints, ident.Stated

	// Where a source stated nothing, the proposal is the only key resolution has,
	// so it is what gets queried and looked up -- refusing that would mean
	// refusing to identify the instrument at all, and refusing to look it up
	// would mean paying a plugin for a description whose ticker the database
	// already holds. The substitution happens once, here, and produces a separate
	// value: ident keeps the true provenance, so the scoring and the confirmation
	// below still know the key was guessed. See adr/0057.
	keyed := ident
	guessedKey := len(keyed.Stated) == 0 && len(keyed.Proposed) > 0
	if guessedKey {
		keyed.Stated, keyed.Proposed = keyed.Proposed, nil
	}
	// A guessed venue must not narrow the database lookup, though it is exactly
	// what the plugins want for ranking. A proposal is not evidence, so a key
	// carrying one has to find the instrument the same key without one would
	// have found: otherwise a guess about where a ticker trades forks a second
	// instrument alongside the one that ticker already names -- which is what a
	// price import, storing a ticker and no venue, leaves waiting. The plugins
	// still receive the venue; only this lookup is widened. See adr/0057.
	lookupIDs := keyed.Stated
	if guessedKey {
		lookupIDs = make([]identifier.Identifier, len(keyed.Stated))
		copy(lookupIDs, keyed.Stated)
		for i := range lookupIDs {
			if lookupIDs[i].Type == "MIC_TICKER" {
				lookupIDs[i].Domain = ""
			}
		}
	}
	// What the attempt row states about its inputs. Its outcome and the asset
	// class it landed on are filled in at each of the returns below.
	row := db.TelemetryIdentificationAttempt{
		Depth:              depth,
		SecurityTypeHint:   hints.SecurityTypeHint,
		HadIdentifierHints: len(keyed.Stated) > 0,
	}

	// If all hints already resolve to one instrument in DB, use it (avoids plugin call).
	resolved, err := ResolveByHintsDBOnly(ctx, database, lookupIDs)
	if err != nil {
		return ResolveResult{}, err
	}
	if len(resolved) == 1 {
		diffs := CompareDBMeta(hints, resolved[0])
		confirmed := ConfirmedDBFields(hints, resolved[0])
		// No plugin was called, which is why this is its own outcome rather than
		// an identification with no plugin-call rows under it. It is also why a
		// failure rate over all attempts falls as the instrument table fills.
		row.Outcome = db.TelemetryAttemptDBShortCircuit
		row.AssetClass = resolved[0].AssetClass
		tel.write(ctx, row)
		// The database answered before any plugin ran, so nothing consulted the
		// proposals -- which is what unused means and why it is not untested.
		return ResolveResult{InstrumentID: resolved[0].ID, Identified: true, HintDiffs: diffs, Confirmed: confirmed,
			ProposalOutcomes: proposalOutcomes(ctx, ident.Proposed, nil, nil, normMIC)}, nil
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
	if len(inputs) == 0 {
		l.DebugContext(ctx, "instrument resolution: no enabled identifier plugins", "source", source, "instrument_description", instrumentDescription)
	}

	// Winner selection relies on inputs being ordered by precedence (descending),
	// which is guaranteed by ListEnabledPluginConfigs. The first successful
	// result in iteration order wins.
	results := make([]pluginResult, len(inputs))
	var wg sync.WaitGroup
	for i := range inputs {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			in := inputs[idx]
			timeout := pluginutil.TimeoutFromConfig(in.config.Config, DefaultPluginTimeout)
			res, stats, err := callPluginWithRetry(ctx, in.plugin, in.config.Config, broker, source, instrumentDescription, keyed, timeout, PluginRetryBackoff)
			results[idx] = pluginResult{inst: res.Instrument, ids: res.Identifiers, filtered: res.Filtered, tel: res.Telemetry, stats: stats, err: err}
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

	// One outcome per plugin invocation, seeded with what the plugin reported and
	// refined below for the ones that succeeded: won, superseded and discarded are
	// the orchestrator's to decide, and it cannot decide any of them until every
	// plugin has returned.
	callOutcomes := make([]string, len(results))
	for i := range results {
		callOutcomes[i] = string(results[i].tel.Outcome)
	}

	// Whether there is anything to match against at all, checked separately for
	// each provenance so that a proposal cannot make a stated match look possible
	// where none was.
	hasStated := hints.Currency != "" ||
		(hints.SecurityTypeHint != "" && hints.SecurityTypeHint != identifier.SecurityTypeHintUnknown) ||
		len(identifierHints) > 0
	hasProposed := len(keyed.Proposed) > 0

	var winner *pluginResult
	var winnerIdx int
	firstSuccessIdx := -1
	firstStatedIdx := -1
	firstProposedIdx := -1
	var hadTimeout, hadOtherErr bool
	for i := range results {
		r := &results[i]
		if r.err == nil && r.inst != nil {
			if firstSuccessIdx < 0 {
				firstSuccessIdx = i
			}
			if hasStated && firstStatedIdx < 0 && resultMatchesHints(ctx, hints, identifierHints, r, normMIC) {
				firstStatedIdx = i
			}
			// A guess promotes only a result that does not argue with the source.
			// Matching a proposal is not enough on its own: a result contradicting
			// the stated currency would otherwise be lifted over one that merely
			// failed to confirm it, which is a proposal outranking a statement by
			// the back door. Contradicting nothing is a weaker test than the
			// stated tier's, which also requires something to have been confirmed.
			if hasProposed && firstProposedIdx < 0 &&
				len(CompareHints(ctx, hints, identifierHints, r.inst, r.ids, normMIC)) == 0 &&
				resultMatchesHints(ctx, identifier.Hints{}, keyed.Proposed, r, normMIC) {
				firstProposedIdx = i
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
	// Three tiers, in order. Agreeing with what a source stated outranks agreeing
	// with what a plugin proposed, which outranks precedence alone. A proposal can
	// break a tie among plugins that all answered; it can never outrank something
	// a source said, and it never removes a result from contention -- a
	// contradicted proposal costs a result its place in the middle tier and
	// nothing more. See adr/0057.
	decidedBy := ""
	switch {
	case firstStatedIdx >= 0:
		winnerIdx, decidedBy = firstStatedIdx, "stated"
	case firstProposedIdx >= 0:
		winnerIdx, decidedBy = firstProposedIdx, "proposed"
	case firstSuccessIdx >= 0:
		winnerIdx, decidedBy = firstSuccessIdx, "precedence"
	}
	if firstSuccessIdx >= 0 {
		winner = &results[winnerIdx]
	}
	if winner != nil && winnerIdx != firstSuccessIdx {
		l.InfoContext(ctx, "identifier plugin preferred over higher-precedence plugin",
			"chosen_plugin", inputs[winnerIdx].config.PluginID,
			"bypassed_plugin", inputs[firstSuccessIdx].config.PluginID,
			"decided_by", decidedBy,
			"instrument_description", instrumentDescription)
	}

	// writeAttempt records the attempt and the invocations under it. It is deferred
	// to the returns below rather than run here, because the winner path resolves
	// the underlying first and that recursion is a separate attempt: writing this
	// one now would nest the wrong way round in time.
	writeAttempt := func(outcome, assetClass string) {
		row.Outcome = outcome
		row.AssetClass = assetClass
		attemptID := tel.write(ctx, row)
		for i := range results {
			o := callOutcomes[i]
			if o == string(identifier.OutcomeIdentified) {
				// The plugin says it identified something the resolver did not
				// take as a success. The row records what the resolver saw: won,
				// superseded and discarded are its words, and this is none of them.
				o = string(identifier.OutcomeNotIdentified)
			}
			tel.writeCall(ctx, attemptID, inputs[i].config.PluginID, o, results[i].stats, &results[i])
		}
	}

	if winner != nil {
		l.DebugContext(ctx, "identifier plugin chosen", "plugin_id", inputs[winnerIdx].config.PluginID, "instrument_description", instrumentDescription, "instrument_name", winner.inst.Name)
		// One entry per result that contributed, in precedence-descending
		// order. Kept apart because what makes an association a claim is that
		// the identifiers arrived together in one answer: the same identifiers
		// gathered from two results are a set nobody asserted. See adr/0060.
		var claims []db.IdentityClaim
		var providerIDs []db.ProviderIdentifierInput
		// Consistent results that did not win, in precedence order. Collected
		// rather than applied in the loop because consistentWith compares
		// against the winner, and filling it mid-loop would change what later
		// results are checked against.
		var fills []*identifier.Instrument
		for i := range results {
			r := &results[i]
			if r.err != nil || r.inst == nil {
				continue
			}
			// Decided here rather than recomputed later: each predicate logs a
			// warning when it refuses, so asking twice would say it twice.
			//
			// The two refusals are kept apart because they are different
			// findings. consistentWith says the two results argued -- about the
			// line, or about a value under a subject they share. corroborated
			// says nothing tied them to one security in the first place, which is
			// the ordinary shape of two answers to an ambiguous query rather than
			// a disagreement. A reader asking why a plugin's answer did not reach
			// an instrument needs to know which happened.
			switch {
			case i == winnerIdx:
				callOutcomes[i] = db.TelemetryPluginCallWon
			case !consistentWith(ctx, l, inputs[winnerIdx].config.PluginID, inputs[i].config.PluginID, winner, r, normMIC, countryOf):
				callOutcomes[i] = db.TelemetryPluginCallDiscardedInconsistent
				continue
			case !corroborated(ctx, l, inputs[winnerIdx].config.PluginID, inputs[i].config.PluginID, winner, r, normMIC):
				callOutcomes[i] = db.TelemetryPluginCallDiscardedUncorroborated
				continue
			default:
				// Identified the same security and did not win it. Precedence or
				// a better hint match put another plugin ahead.
				callOutcomes[i] = db.TelemetryPluginCallSuperseded
			}
			claims = append(claims, claim(r))
			for _, pi := range r.inst.ProviderIdentifiers {
				providerIDs = append(providerIDs, db.ProviderIdentifierInput{
					Provider: pi.Provider, Type: pi.Type, Domain: pi.Domain, Value: pi.Value,
				})
			}
			if i != winnerIdx {
				fills = append(fills, r.inst)
			}
		}
		// The winner supplies the instrument, but a plugin that identified the
		// same security and merely lost on precedence still knows things the
		// winner left blank. Its identifiers are already merged in; taking the
		// fields it filled and the winner did not is the same trust, applied to
		// the same result.
		//
		// The venue is still worth filling although nothing stores one. A
		// provider answering with a composite names no single venue, so the
		// winner's is empty while a lower-precedence result names it outright,
		// and the filled venue is what picks the line out of the security's
		// listings below -- a security quoted at one venue in two currencies is
		// the case where naming the venue settles which line the result is
		// about. Note consistentWith skips the currency comparison where either
		// side stated none, and checks the venue in precisely that case, so a
		// field filled here was never contradicted; it is adopted because that
		// plugin agreed on everything it could be checked on, which is the same
		// basis its identifiers are merged on.
		mergedIds := flattenClaims(ctx, normMIC, claims)
		merged := *winner.inst
		for _, src := range fills {
			fillBlanks(ctx, normMIC, countryOf, &merged, src)
		}
		// A plugin answers about the instrument it was named, so the names it
		// gives back are only as current as the hint that named it: the vintage
		// of the file the hints came from, and now for a caller that states
		// none. That matters most for an OCC, which a split restates -- a
		// pre-split symbol resolves to the contract, and the name written for it
		// is correct as of that export rather than of today, which is what
		// leaves the option pending for the retroactive restatement.
		//
		// Only names this path writes carry it; a name already stored keeps the
		// valid_from it was written with, because when a name became correct is a
		// market fact and re-seeing it is not evidence of a new one. The
		// broker-description-only fallback below derives nothing from the market
		// and writes no dated name at all.
		namedAt := hintsValidAt
		if namedAt == nil {
			now := time.Now()
			namedAt = &now
		}
		nameFrom := db.VintageDate(namedAt)
		identifiers := make([]db.IdentifierInput, 0, len(mergedIds)+1)
		hasSource := false
		for _, idn := range mergedIds {
			identifiers = append(identifiers, db.IdentifierInput{
				Ref:       db.InstrumentRef{Type: idn.Type, Value: idn.Value, Domain: idn.Domain},
				Canonical: true,
				ValidFrom: nameFrom,
			})
			if idn.Type == "BROKER_DESCRIPTION" && idn.Domain == source && idn.Value == instrumentDescription {
				hasSource = true
			}
		}
		if bindSourceDescription && !hasSource {
			identifiers = append(identifiers, db.IdentifierInput{
				Ref:       db.InstrumentRef{Type: "BROKER_DESCRIPTION", Value: instrumentDescription, Domain: source},
				Canonical: false,
				ValidFrom: nameFrom,
			})
		}
		inst := &merged
		var underlyingListingID string
		if len(inst.UnderlyingIdentifiers) > 0 && depth < MaxResolveDepth {
			// The underlying's identity is its own. The plugin named these
			// identifiers from the derivative it resolved, so they are stated
			// rather than proposed, and no proposal about the derivative says
			// anything about what it is written on.
			uIdent := identifier.Identity{
				Stated: make([]identifier.Identifier, len(inst.UnderlyingIdentifiers)),
				Hints:  identifier.Hints{SecurityTypeHint: identifier.UnderlyingSecTypeHint(inst.AssetClass)},
			}
			copy(uIdent.Stated, inst.UnderlyingIdentifiers)
			// Underlying resolution: no source description, no fallback needed (use nil).
			uResult, uErr := ResolveWithPlugins(ctx, database, registry, broker, source, "", uIdent, false, nil, tel.underlying(), logger, depth+1, nil)
			if uErr != nil {
				l.WarnContext(ctx, "underlying resolution failed", "instrument_description", instrumentDescription, "err", uErr)
			} else if uResult.InstrumentID != "" {
				// The line the contract delivers is the one its strike is quoted
				// in, not whichever line the underlying's own resolution happened
				// to land on: an option struck in euros delivers the euro line
				// however the underlying was found. The currency is what this
				// result states, else what the contract's symbology implies.
				//
				// Ensure rather than find. A corroborated contract asserts its
				// own strike currency, and a strike is quoted against shares, so
				// it asserts that the underlying has a line there -- whatever
				// lines the underlying is already known to have, and whether or
				// not anything has said so before. That is an assertion by a
				// source rather than a guess, which is what lets it mint. See
				// adr/0074-an-options-underlying-is-the-line-its-strike-is-quoted-in.md.
				//
				// A contract that declares no currency asserts nothing and names
				// no line; the branch below refuses it.
				//
				// The symbology is read from what the source stated as well as
				// what the result carries: a broker naming a contract by its OCC
				// symbol is the assertion, and a plugin answering about that
				// contract need not repeat it.
				wearing := make([]identifier.Identifier, 0, len(ident.Stated)+len(mergedIds))
				wearing = append(wearing, ident.Stated...)
				wearing = append(wearing, mergedIds...)
				cur := identifier.StrikeCurrency(inst.Listing.Currency, identifierTypesOf(wearing))
				if cur != "" {
					line, lErr := database.EnsureListing(ctx, uResult.InstrumentID, cur)
					if lErr != nil {
						l.WarnContext(ctx, "underlying listing could not be ensured",
							"instrument_description", instrumentDescription, "underlying", uResult.InstrumentID,
							"currency", cur, "err", lErr)
					}
					underlyingListingID = line
				}
			}
		}
		var optFields *db.OptionFields
		if inst.AssetClass == db.AssetClassOption {
			optFields = optionFieldsFromIdentifiers(mergedIds)
		}
		diffs := CompareHints(ctx, hints, identifierHints, inst, mergedIds, normMIC)
		confirmed := confirmedFields(ctx, hints, identifierHints, inst, mergedIds, normMIC)
		// A key nobody stated has to earn its place before it decides which
		// instrument a transaction belongs to.
		//
		// This is the case where the whole identity was guessed: no source stated
		// an identifier, so the proposal was queried as the only key there was. A
		// provider answering about it proves the value names some security, not
		// that it names this one -- a plausible invented ISIN is usually a real
		// ISIN belonging to something else, and a plausible invented ticker is
		// usually another company's. So the result has to agree with something
		// nobody guessed: the currency the transaction stated, or the security
		// type. Agreeing with nothing at all is not a near miss, it is the
		// absence of a test, and the guess is dropped.
		//
		// Dropping it produces a broker-description-only instrument, which is the
		// better failure: it is visible in the UI, it is repairable, and it does
		// not propagate. A wrong attachment becomes a canonical, instance-global
		// (source, description) binding that every later upload hits and no
		// plugin ever re-examines. See adr/0059 and issues/0132.
		//
		// A source that stated an identifier is not subject to this. There the
		// proposal was never queried -- it only ranked among the listings the
		// stated key produced -- so there is no invention to round-trip.
		if len(ident.Stated) == 0 && len(ident.Proposed) > 0 && len(confirmed) == 0 && fallback != nil {
			l.InfoContext(ctx, "instrument resolution: proposed identifier confirmed nothing the source stated, using broker description only",
				"source", source, "instrument_description", instrumentDescription,
				"proposed", HintsSummary(ident.Proposed), "resolved", instrumentSummary(inst))
			fb, fbErr := fallback(ctx, database)
			if fbErr != nil {
				return ResolveResult{}, fbErr
			}
			writeAttempt(db.TelemetryAttemptProposalUnconfirmed, "")
			return ResolveResult{InstrumentID: fb, Unconfirmed: true, HintDiffs: diffs,
				ProposalOutcomes: proposalOutcomes(ctx, ident.Proposed, inst, mergedIds, normMIC)}, nil
		}
		// A derivative that declared no strike currency names no line of its
		// underlying, and a contract whose deliverable is denominated in nothing
		// is not stored as a derivative. It degrades the way an unresolvable
		// underlying already did -- to a broker-description-only instrument,
		// visible in the UI and repairable -- rather than failing the upload it
		// arrived in, because one contract nobody could place is not a reason to
		// reject a statement.
		//
		// Below the proposal check, which is the more specific finding: an
		// identity nobody corroborated is dropped as a guess before this asks
		// what it delivers, and it has no authority to mint a line either. See
		// adr/0074-an-options-underlying-is-the-line-its-strike-is-quoted-in.md.
		if isDerivativeClass(inst.AssetClass) && underlyingListingID == "" && fallback != nil {
			l.InfoContext(ctx, "instrument resolution: contract declares no strike currency, using broker description only",
				"source", source, "instrument_description", instrumentDescription,
				"asset_class", inst.AssetClass, "resolved", instrumentSummary(inst))
			fb, fbErr := fallback(ctx, database)
			if fbErr != nil {
				return ResolveResult{}, fbErr
			}
			writeAttempt(db.TelemetryAttemptUnderlyingLineUnknown, "")
			return ResolveResult{InstrumentID: fb, HintDiffs: diffs}, nil
		}
		// The listing is the line the result named, and it is where every
		// identifier of listing grain has just been written. The provider
		// identifiers follow the same routing, so it is passed on rather than
		// discarded; carrying it out of Resolve to the caller is 0147's third
		// step.
		id, listingID, err := database.EnsureInstrument(ctx, inst.AssetClass, inst.Listing.Currency, inst.Name, inst.CIK, inst.SICCode, identifiers, claims, underlyingListingID, optFields)
		if err != nil {
			return ResolveResult{}, err
		}
		if len(providerIDs) > 0 {
			if err := database.SaveProviderIdentifiers(ctx, id, listingID, providerIDs); err != nil {
				l.WarnContext(ctx, "save provider identifiers failed", "instrument_id", id, "err", err)
			}
		}
		// Where the result named a venue and no currency, the venue may still
		// pick the line out. It does so only when it picks out exactly one: the
		// LSE lists both the GBP and the USD line of the same ETC, and settling
		// that by taking either would attach a holding to a currency nobody
		// stated. Two matches leaves the resolution naming no line, which is the
		// honest answer and the one adr/0068 requires.
		if listingID == "" && inst.Listing.Venue.MIC != "" {
			mic := normalizeMICValue(ctx, normMIC, inst.Listing.Venue.MIC)
			byVenue, vErr := database.ListingForVenue(ctx, id, mic)
			if vErr != nil {
				l.WarnContext(ctx, "listing lookup by venue failed", "instrument_id", id, "mic", mic, "err", vErr)
			} else {
				listingID = byVenue
			}
		}
		writeAttempt(db.TelemetryAttemptIdentified, inst.AssetClass)
		return ResolveResult{InstrumentID: id, ListingID: listingID, Identified: true, HintDiffs: diffs, Confirmed: confirmed,
			ProposalOutcomes: proposalOutcomes(ctx, ident.Proposed, inst, mergedIds, normMIC)}, nil
	}

	// Nothing identified it. A plugin removed by the eligibility filter made no
	// call, so an attempt that had no eligible plugin left is not a failure to
	// identify -- it is the denominator a failure rate must exclude.
	switch {
	case len(inputs) == 0:
		writeAttempt(db.TelemetryAttemptNoEligiblePlugins, "")
	case hadTimeout:
		writeAttempt(db.TelemetryAttemptPluginTimeout, "")
	case hadOtherErr:
		writeAttempt(db.TelemetryAttemptPluginError, "")
	default:
		writeAttempt(db.TelemetryAttemptNotIdentified, "")
	}

	// Unresolved: call fallback if provided.
	if fallback != nil {
		id, err := fallback(ctx, database)
		if err != nil {
			return ResolveResult{}, err
		}
		// Nothing identified it, so nothing checked the proposals either.
		return ResolveResult{InstrumentID: id, HadTimeout: hadTimeout, HadError: hadOtherErr,
			ProposalOutcomes: proposalOutcomes(ctx, ident.Proposed, nil, nil, normMIC)}, nil
	}
	return ResolveResult{HadTimeout: hadTimeout, HadError: hadOtherErr,
		ProposalOutcomes: proposalOutcomes(ctx, ident.Proposed, nil, nil, normMIC)}, nil
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
	return inst.Name + " (" + inst.AssetClass + "/" + inst.Listing.Venue.MIC + ")"
}

// callStats is what only the caller of a plugin knows about the invocation: the
// retry loop and the clock belong to the resolver, not to the plugin. Duration
// spans the whole invocation including any retry, because that is what one
// identifier_plugin_call row describes.
type callStats struct {
	Retries  int
	Duration time.Duration
}

// callPluginWithRetry calls Identify with exponential backoff retry.
// ErrNotIdentified is treated as a permanent error (no retry). Each attempt gets its own
// context timeout derived from the parent so cancellation still propagates.
func callPluginWithRetry(ctx context.Context, p identifier.Plugin, config []byte, broker, source, instrumentDescription string, ident identifier.Identity, timeout, initialBackoff time.Duration) (identifier.Result, callStats, error) {
	var res identifier.Result
	var calls int
	started := time.Now()

	bo := backoff.NewExponentialBackOff()
	bo.InitialInterval = initialBackoff
	bo.MaxElapsedTime = 0 // controlled by MaxRetries, not elapsed time
	bCtx := backoff.WithContext(backoff.WithMaxRetries(bo, 1), ctx)

	err := backoff.Retry(func() error {
		calls++
		attemptCtx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		var attemptErr error
		res, attemptErr = p.Identify(attemptCtx, config, broker, source, instrumentDescription, ident)
		if attemptErr == nil {
			return nil
		}
		if errors.Is(attemptErr, identifier.ErrNotIdentified) {
			return backoff.Permanent(attemptErr)
		}
		return attemptErr
	}, bCtx)

	return res, callStats{Retries: calls - 1, Duration: time.Since(started)}, err
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
		if !parsed.Strike.IsPositive() || parsed.Expiry.IsZero() || parsed.PutCall == "" {
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
