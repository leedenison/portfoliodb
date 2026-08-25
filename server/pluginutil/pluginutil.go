package pluginutil

import (
	"encoding/json"
	"time"

	"github.com/leedenison/portfoliodb/server/assetclass"
	"github.com/leedenison/portfoliodb/server/currency"
	"github.com/leedenison/portfoliodb/server/db"
	"github.com/leedenison/portfoliodb/server/identifier"
)

// Plugin admission is four predicates, one per thing a plugin can be offered.
// They live together because they are one question -- may this plugin be called
// about this? -- asked of four different subjects. Spread over three packages
// under four spellings, they were last found to disagree by someone reading all
// four; together, a rule stated of one is visibly stated of the others.
//
// Each names its subject and nothing else:
//
//   - Accepts, a stored security. The corporate event fetcher's unit of work,
//     an event being an action on the security.
//   - AcceptsListing, a stored currency line. The price fetcher's unit of work,
//     a price being quoted in one currency.
//   - AcceptsSecurityType, a security type a source stated. Identification's
//     routing, run before anything is stored and so with no row to read.
//   - AcceptsCurrency, a currency code. The inflation fetcher's unit of work,
//     an index being published per currency.
//
// The first three take what a plugin declared acceptable and are permissive in
// the same way: an empty declaration takes everything, and a subject that
// carries nothing to test has nothing to fail on. Every currency comparison
// among them is on the family, as every currency comparison is (adr/0068).
//
// The asset class comparisons are not uniform, and should not be. The first two
// test set membership of a stored class, which is a class the system wrote; the
// third asks assetclass.MayBe of a class a source stated, which may be a node
// above anything a plugin declares -- a statement of EQUITY reaches a plugin
// declaring STOCK, and a stored EQUITY is not a stored STOCK.

// Accepts checks whether an instrument matches the given asset class,
// exchange, and currency filter maps. Empty or nil maps accept all values.
//
// This is the security-grain test. AcceptsListing below is the same test at the
// grain a price is quoted at.
//
// The asset class is the security's own. The currency and the venues are not --
// a security carries neither -- so both are read over its lines and pass on any
// one of them. A plugin covering the GBP line of a dual-listed security can
// answer about the split that security declared, splits being actions on the
// security and applying to every line of it, so refusing the fetch because
// another line is in a currency the plugin does not carry would lose the events
// for a line it does.
//
// Permissive in the venue, by the same rule AcceptsListing is: the set is
// what we have been told about rather than what exists, so a security no line of
// which records a venue has nothing to fail on. See
// docs/adr/0077-a-venue-set-is-what-we-know-not-what-exists.md.
//
// The currency is compared on the family, as every currency comparison is: a
// plugin declaring GBP carries the London line whether the line is quoted in
// pounds or in pence, the two being one currency under a different unit prefix
// (adr/0068).
func Accepts(ac, ex, cu map[string]bool, inst *db.InstrumentRow) bool {
	if len(ac) > 0 && inst.AssetClass != nil && *inst.AssetClass != "" {
		if !ac[*inst.AssetClass] {
			return false
		}
	}
	if len(ex) > 0 && anyLineHasVenue(inst) {
		matched := false
		for _, l := range inst.Listings {
			for _, v := range l.Venues {
				if ex[v.MIC] {
					matched = true
				}
			}
		}
		if !matched {
			return false
		}
	}
	if len(cu) > 0 && anyLineHasCurrency(inst) {
		matched := false
		for _, l := range inst.Listings {
			if l.Currency != "" && currency.SameAny(cu, l.Currency) {
				matched = true
			}
		}
		if !matched {
			return false
		}
	}
	return true
}

// anyLineHasVenue reports whether any line of the security records a venue,
// which is what makes the exchange filter applicable at all.
func anyLineHasVenue(inst *db.InstrumentRow) bool {
	for _, l := range inst.Listings {
		if len(l.Venues) > 0 {
			return true
		}
	}
	return false
}

// anyLineHasCurrency reports whether any line of the security carries a currency.
func anyLineHasCurrency(inst *db.InstrumentRow) bool {
	for _, l := range inst.Listings {
		if l.Currency != "" {
			return true
		}
	}
	return false
}

// AcceptsListing is the same test at the grain a price is quoted at: the
// asset class still comes from the security, while the currency and the venues
// are the listing's own. Empty or nil maps accept all values.
//
// The venue test is permissive, because a venue set is what we know and not what
// exists (adr/0077). A listing with no venue passes, as a null exchange did
// before: nothing named a venue, so there is nothing to fail on -- a composite
// identifier names a market and stores no MIC. Where a line is admitted to
// several venues, carrying any one of them is enough, the venues of one listing
// quoting one line differing by a spread rather than by anything a provider
// would hold separate data for. The question here is whether this plugin can
// plausibly price this line, and a plugin that covers any venue we have heard of
// can.
//
// A listing with no currency does not reach here: it is not priceable, so it is
// never in a gap. One that has a currency is matched on the family, as
// Accepts matches it: the line is the family and not the code.
func AcceptsListing(ac, ex, cu map[string]bool, assetClass *string, lst *db.Listing) bool {
	if len(ac) > 0 && assetClass != nil && *assetClass != "" {
		if !ac[*assetClass] {
			return false
		}
	}
	if len(ex) > 0 && len(lst.Venues) > 0 {
		matched := false
		for _, v := range lst.Venues {
			if ex[v.MIC] {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	if len(cu) > 0 && lst.Currency != "" {
		if !currency.SameAny(cu, lst.Currency) {
			return false
		}
	}
	return true
}

// AcceptsSecurityType reports whether a plugin should be tried for a row whose
// source stated secType. A plugin declaring no acceptable types takes anything,
// and a row whose source stated nothing is offered to every plugin.
//
// The subject is a statement rather than a stored row, which is what separates
// this from Accepts above: identification runs before anything is stored, so
// there is no asset class the system wrote and no line to read a currency off.
//
// The permissive question: a plugin is tried when what the source said and what
// the plugin covers could describe one security. Excluding a row because its
// source could not be specific loses the row, where trying a plugin that turns
// out not to cover it costs a call -- which is why a statement of EQUITY
// reaches a plugin declaring STOCK, and why a source that said only SECURITY
// reaches all of them.
//
// Cash and securities stay apart under the same rule rather than a second gate
// above it: a cash plugin declares CASH, which no security class lies under or
// over, so only a row whose source stated cash can reach one.
func AcceptsSecurityType(acceptable map[string]bool, secType string) bool {
	if len(acceptable) == 0 || secType == "" {
		return true
	}
	stated := db.StrToAssetClass(secType)
	for t := range acceptable {
		if assetclass.MayBe(stated, db.StrToAssetClass(t)) {
			return true
		}
	}
	return false
}

// AcceptsCurrency reports whether a plugin publishing for these currencies
// publishes for code.
//
// A slice rather than a set, because the inflation plugins declare one, and
// on the family like every other currency comparison here: a plugin publishing
// an index for GBP publishes it for the line quoted in pence, the two being one
// currency under a different unit prefix (adr/0068).
func AcceptsCurrency(supported []string, code string) bool {
	for _, c := range supported {
		if currency.Same(c, code) {
			return true
		}
	}
	return false
}

// FilterIdentifiers returns identifiers whose type is in the supported set.
func FilterIdentifiers(supported []string, ids []db.IdentifierInput) []db.IdentifierInput {
	set := make(map[string]bool, len(supported))
	for _, t := range supported {
		set[t] = true
	}
	var out []db.IdentifierInput
	for _, id := range ids {
		if set[id.Ref.Type] {
			out = append(out, id)
		}
	}
	return out
}

// ToIdentifiers narrows stored identifiers to the triple a plugin is given.
//
// The orchestrators carry [db.IdentifierInput] because that is what the store
// hands back, and drop Canonical and the validity interval here, at the last
// point before the call. Both are the store's business: a plugin asked to look
// up a price has no use for whether we consider a name canonical, and a plugin
// that could read it could act on it.
func ToIdentifiers(ids []db.IdentifierInput) []identifier.Identifier {
	out := make([]identifier.Identifier, len(ids))
	for i, id := range ids {
		out[i] = identifier.Identifier{Type: id.Ref.Type, Domain: id.Ref.Domain, Value: id.Ref.Value}
	}
	return out
}

type pluginConfigJSON struct {
	TimeoutSeconds *int `json:"timeout_seconds"`
}

// TimeoutFromConfig parses timeout_seconds from plugin config JSON and falls
// back to defaultTimeout when missing or invalid.
func TimeoutFromConfig(config []byte, defaultTimeout time.Duration) time.Duration {
	if len(config) == 0 {
		return defaultTimeout
	}
	var c pluginConfigJSON
	if err := json.Unmarshal(config, &c); err != nil {
		return defaultTimeout
	}
	if c.TimeoutSeconds == nil || *c.TimeoutSeconds <= 0 {
		return defaultTimeout
	}
	return time.Duration(*c.TimeoutSeconds) * time.Second
}

// Trigger sends a non-blocking signal on a trigger channel. Nil-safe.
func Trigger(ch chan<- struct{}) {
	if ch == nil {
		return
	}
	select {
	case ch <- struct{}{}:
	default:
	}
}
