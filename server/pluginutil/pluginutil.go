package pluginutil

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/leedenison/portfoliodb/server/db"
	"github.com/leedenison/portfoliodb/server/identifier"
)

// PluginAccepts checks whether an instrument matches the given asset class,
// exchange, and currency filter maps. Empty or nil maps accept all values.
//
// This is the security-grain test, for the corporate event fetcher: a corporate
// event is an action on the security. The price fetcher uses PluginAcceptsListing
// below, its unit of work being one currency line.
func PluginAccepts(ac, ex, cu map[string]bool, inst *db.InstrumentRow) bool {
	if len(ac) > 0 && inst.AssetClass != nil && *inst.AssetClass != "" {
		if !ac[*inst.AssetClass] {
			return false
		}
	}
	if len(ex) > 0 && inst.ExchangeMIC != nil && *inst.ExchangeMIC != "" {
		if !ex[*inst.ExchangeMIC] {
			return false
		}
	}
	if len(cu) > 0 && inst.Currency != nil && *inst.Currency != "" {
		if !cu[strings.ToUpper(*inst.Currency)] {
			return false
		}
	}
	return true
}

// PluginAcceptsListing is the same test at the grain a price is quoted at: the
// asset class still comes from the security, while the currency and the venues
// are the listing's own. Empty or nil maps accept all values.
//
// A listing with no venue passes the exchange filter, as a null exchange does
// above: nothing named a venue, so there is nothing to fail on -- a composite
// identifier names a market and stores no MIC. Where a line is admitted to
// several venues, carrying any one of them is enough, the venues of one listing
// quoting one line differing by a spread rather than by anything a provider
// would hold separate data for.
//
// A listing with no currency does not reach here: it is not priceable, so it is
// never in a gap.
func PluginAcceptsListing(ac, ex, cu map[string]bool, assetClass *string, lst *db.Listing) bool {
	if len(ac) > 0 && assetClass != nil && *assetClass != "" {
		if !ac[*assetClass] {
			return false
		}
	}
	if len(ex) > 0 && len(lst.Venues) > 0 {
		matched := false
		for _, mic := range lst.Venues {
			if ex[mic] {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	if len(cu) > 0 && lst.Currency != nil && *lst.Currency != "" {
		if !cu[strings.ToUpper(*lst.Currency)] {
			return false
		}
	}
	return true
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
