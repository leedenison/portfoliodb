// Package logger provides a category-based slog handler so LOG_LEVEL can be
// a global level (e.g. "debug") or a JSON object mapping category prefixes to
// levels (e.g. {"server/plugins": "debug", "default": "info"}).
// Callers use WithCategory to get a logger that attaches a category to every log.
package logger

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
)

const categoryKey = "category"

// normalizePath trims, splits on "/", filters empty segments, and lowercases each segment.
// Tolerant of leading "/" or "//".
func normalizePath(s string) []string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	parts := strings.Split(s, "/")
	var out []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, strings.ToLower(p))
		}
	}
	return out
}

// pathSegmentsMatchPrefix returns true if prefix is a segment-wise prefix of category
// (both normalized). Empty prefix matches nothing.
func pathSegmentsMatchPrefix(categorySegs, prefixSegs []string) bool {
	if len(prefixSegs) == 0 || len(prefixSegs) > len(categorySegs) {
		return false
	}
	for i := range prefixSegs {
		if categorySegs[i] != prefixSegs[i] {
			return false
		}
	}
	return true
}

// config holds either a global level (prefixMap nil) or prefix map + default level.
type config struct {
	globalLevel  *slog.Level
	prefixMap     map[string]slog.Level // key is normalized path joined by "/"
	defaultLevel  slog.Level
	prefixSegments map[string][]string // key -> normalized segments for longest-match
}

// parseLevel returns slog.Level for a string (info, warn, error, debug).
func parseLevel(s string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "info":
		return slog.LevelInfo
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	case "debug":
		return slog.LevelDebug
	default:
		return slog.LevelInfo
	}
}

// ParseLOGLevel parses the LOG_LEVEL env value. If it is a single word (no "{"),
// returns global set and prefixMap nil. If it starts with "{", parses as JSON:
// keys are category prefixes, values are level names; optional "default" key.
// On parse error (JSON invalid), returns global info and empty prefixMap.
func ParseLOGLevel(env string) (global *slog.Level, prefixMap map[string]slog.Level, defaultLevel slog.Level) {
	env = strings.TrimSpace(env)
	defaultLevel = slog.LevelInfo
	if env == "" {
		l := slog.LevelInfo
		return &l, nil, defaultLevel
	}
	if !strings.HasPrefix(env, "{") {
		l := parseLevel(env)
		return &l, nil, defaultLevel
	}
	var raw map[string]string
	if err := json.Unmarshal([]byte(env), &raw); err != nil {
		l := slog.LevelInfo
		return &l, nil, defaultLevel
	}
	prefixMap = make(map[string]slog.Level)
	for k, v := range raw {
		k = strings.TrimSpace(k)
		if k == "" {
			continue
		}
		if strings.ToLower(k) == "default" {
			defaultLevel = parseLevel(v)
			continue
		}
		segments := normalizePath(k)
		if len(segments) == 0 {
			continue
		}
		normalizedKey := strings.Join(segments, "/")
		prefixMap[normalizedKey] = parseLevel(v)
	}
	return nil, prefixMap, defaultLevel
}

// levelForCategory returns the effective level for the given category using
// longest matching prefix. category is normalized for comparison.
func levelForCategory(category string, prefixMap map[string]slog.Level, prefixSegments map[string][]string, defaultLevel slog.Level) slog.Level {
	if len(prefixMap) == 0 {
		return defaultLevel
	}
	catSegs := normalizePath(category)
	if len(catSegs) == 0 {
		return defaultLevel
	}
	var bestKey string
	bestLen := -1
	for key, segs := range prefixSegments {
		if pathSegmentsMatchPrefix(catSegs, segs) && len(segs) > bestLen {
			bestLen = len(segs)
			bestKey = key
		}
	}
	if bestKey == "" {
		return defaultLevel
	}
	return prefixMap[bestKey]
}

// categoryHandler wraps an inner handler and filters by category-based level.
type categoryHandler struct {
	inner  slog.Handler
	config *config
}

func (h *categoryHandler) Enabled(ctx context.Context, level slog.Level) bool {
	// We cannot see the record in Enabled, so accept all and filter in Handle.
	return true
}

func (h *categoryHandler) Handle(ctx context.Context, r slog.Record) error {
	var category string
	r.Attrs(func(a slog.Attr) bool {
		if a.Key == categoryKey && a.Value.Kind() == slog.KindString {
			category = a.Value.String()
			return false
		}
		return true
	})
	var effective slog.Level
	if h.config.globalLevel != nil {
		effective = *h.config.globalLevel
	} else {
		effective = levelForCategory(category, h.config.prefixMap, h.config.prefixSegments, h.config.defaultLevel)
	}
	if r.Level < effective {
		return nil
	}
	return h.inner.Handle(ctx, r)
}

func (h *categoryHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &categoryHandler{inner: h.inner.WithAttrs(attrs), config: h.config}
}

func (h *categoryHandler) WithGroup(name string) slog.Handler {
	return &categoryHandler{inner: h.inner.WithGroup(name), config: h.config}
}

// NewHandler returns a handler that filters records by category-based level.
// env is the LOG_LEVEL value: either a single level ("debug", "info", etc.) or
// a JSON object (e.g. `{"server/plugins": "debug", "default": "info"}`).
// The inner handler should use LevelDebug so it does not filter; this handler does the filtering.
func NewHandler(inner slog.Handler, env string) slog.Handler {
	global, prefixMap, defaultLevel := ParseLOGLevel(env)
	cfg := &config{
		globalLevel:  global,
		prefixMap:    prefixMap,
		defaultLevel: defaultLevel,
	}
	if len(prefixMap) > 0 {
		cfg.prefixSegments = make(map[string][]string)
		for key := range prefixMap {
			cfg.prefixSegments[key] = normalizePath(key)
		}
	}
	return &categoryHandler{inner: inner, config: cfg}
}

// WithCategory returns a logger that adds the given category to every log record,
// so the category-based level from LOG_LEVEL (JSON) applies. If l is nil, returns nil.
func WithCategory(l *slog.Logger, category string) *slog.Logger {
	if l == nil {
		return nil
	}
	return l.With(categoryKey, category)
}

