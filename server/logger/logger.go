// Package logger provides a category-based slog handler so LOG_LEVEL can be
// a global level (e.g. "debug") or a JSON object mapping category prefixes to
// levels (e.g. {"server/plugins": "debug", "default": "info"}).
// Callers use WithCategory to get a logger that attaches a category to every log.
package logger

import (
	"context"
	"encoding/json"
	"log/slog"
	"sort"
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

// stripQuoted removes one layer of surrounding single or double quotes so that
// values like '{"key": "value"}' from .env (where Docker Compose can leave the
// quotes in the value) are parsed as JSON.
func stripQuoted(s string) string {
	s = strings.TrimSpace(s)
	if len(s) < 2 {
		return s
	}
	if (s[0] == '\'' && s[len(s)-1] == '\'') || (s[0] == '"' && s[len(s)-1] == '"') {
		return s[1 : len(s)-1]
	}
	return s
}

// ParseLOGLevel parses the LOG_LEVEL env value. If it is a single word (no "{"),
// returns global set and prefixMap nil. If it starts with "{", parses as JSON:
// keys are category prefixes, values are level names; optional "default" key.
// On parse error (JSON invalid), returns global info and empty prefixMap.
// Surrounding single or double quotes are stripped so .env values like
// LOG_LEVEL='{"server/service/ingestion": "debug"}' work when the quotes are
// passed through into the container.
func ParseLOGLevel(env string) (global *slog.Level, prefixMap map[string]slog.Level, defaultLevel slog.Level) {
	env = stripQuoted(strings.TrimSpace(env))
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

// Summary returns a short summary of the configured log levels for startup logging,
// e.g. "global=DEBUG" or "default=INFO server/plugins=DEBUG server/service/ingestion=DEBUG".
// Category keys are sorted for deterministic output.
func Summary(env string) string {
	global, prefixMap, defaultLevel := ParseLOGLevel(env)
	if global != nil {
		return "global=" + levelString(*global)
	}
	var b strings.Builder
	b.WriteString("default=")
	b.WriteString(levelString(defaultLevel))
	keys := make([]string, 0, len(prefixMap))
	for k := range prefixMap {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		b.WriteString(" ")
		b.WriteString(k)
		b.WriteString("=")
		b.WriteString(levelString(prefixMap[k]))
	}
	return b.String()
}

func levelString(l slog.Level) string {
	switch l {
	case slog.LevelDebug:
		return "DEBUG"
	case slog.LevelInfo:
		return "INFO"
	case slog.LevelWarn:
		return "WARN"
	case slog.LevelError:
		return "ERROR"
	default:
		return "INFO"
	}
}

// EffectiveLevel returns the effective slog level for the given category when using
// the same parsing as NewHandler. It is used for startup logging so we can verify
// LOG_LEVEL is applied (e.g. that "server/service/ingestion" gets debug when configured).
func EffectiveLevel(env string, category string) slog.Level {
	global, prefixMap, defaultLevel := ParseLOGLevel(env)
	if global != nil {
		return *global
	}
	if len(prefixMap) == 0 {
		return defaultLevel
	}
	prefixSegments := make(map[string][]string)
	for key := range prefixMap {
		prefixSegments[key] = normalizePath(key)
	}
	return levelForCategory(category, prefixMap, prefixSegments, defaultLevel)
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
	attrs  []slog.Attr // from WithAttrs (Logger.With); Record in Handle does not include these
}

func (h *categoryHandler) Enabled(ctx context.Context, level slog.Level) bool {
	// We cannot see the record in Enabled, so accept all and filter in Handle.
	return true
}

func (h *categoryHandler) Handle(ctx context.Context, r slog.Record) error {
	category := findCategoryInAttrs(h.attrs)
	if category == "" {
		r.Attrs(func(a slog.Attr) bool {
			if a.Key == categoryKey && a.Value.Kind() == slog.KindString {
				category = a.Value.String()
				return false
			}
			return true
		})
	}
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

func findCategoryInAttrs(attrs []slog.Attr) string {
	for _, a := range attrs {
		if a.Key == categoryKey && a.Value.Kind() == slog.KindString {
			return a.Value.String()
		}
	}
	return ""
}

func (h *categoryHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	merged := make([]slog.Attr, 0, len(h.attrs)+len(attrs))
	merged = append(merged, h.attrs...)
	merged = append(merged, attrs...)
	return &categoryHandler{
		inner:  h.inner.WithAttrs(attrs),
		config: h.config,
		attrs:  merged,
	}
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

