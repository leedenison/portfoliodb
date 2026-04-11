package pluginutil

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/leedenison/portfoliodb/server/db"
)

// PluginAccepts checks whether an instrument matches the given asset class,
// exchange, and currency filter maps. Empty or nil maps accept all values.
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

// FilterIdentifiers returns identifiers whose type is in the supported set.
func FilterIdentifiers(supported []string, ids []db.IdentifierInput) []db.IdentifierInput {
	set := make(map[string]bool, len(supported))
	for _, t := range supported {
		set[t] = true
	}
	var out []db.IdentifierInput
	for _, id := range ids {
		if set[id.Type] {
			out = append(out, id)
		}
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
