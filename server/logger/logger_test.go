package logger

import (
	"log/slog"
	"os"
	"testing"
)

func TestParseLOGLevel_global(t *testing.T) {
	global, prefixMap, defaultLevel := ParseLOGLevel("debug")
	if global == nil || *global != slog.LevelDebug {
		t.Fatalf("expected global debug, got %v", global)
	}
	if prefixMap != nil {
		t.Fatalf("expected nil prefixMap for global, got %v", prefixMap)
	}
	if defaultLevel != slog.LevelInfo {
		t.Fatalf("defaultLevel want info got %v", defaultLevel)
	}

	global, _, _ = ParseLOGLevel("info")
	if global == nil || *global != slog.LevelInfo {
		t.Fatalf("expected global info, got %v", global)
	}
}

func TestParseLOGLevel_json(t *testing.T) {
	env := `{"server/plugins": "debug", "default": "warn"}`
	global, prefixMap, defaultLevel := ParseLOGLevel(env)
	if global != nil {
		t.Fatalf("expected nil global for JSON, got %v", global)
	}
	if prefixMap == nil {
		t.Fatal("expected non-nil prefixMap")
	}
	if l := prefixMap["server/plugins"]; l != slog.LevelDebug {
		t.Fatalf("server/plugins want debug got %v", l)
	}
	if defaultLevel != slog.LevelWarn {
		t.Fatalf("default want warn got %v", defaultLevel)
	}
}

func TestSummary(t *testing.T) {
	if got := Summary("debug"); got != "global=DEBUG" {
		t.Fatalf("Summary(debug) = %q want global=DEBUG", got)
	}
	if got := Summary("info"); got != "global=INFO" {
		t.Fatalf("Summary(info) = %q want global=INFO", got)
	}
	env := `{"server/service/ingestion": "debug", "server/plugins": "debug", "default": "info"}`
	got := Summary(env)
	// Keys sorted: default, server/plugins, server/service/ingestion
	want := "default=INFO server/plugins=DEBUG server/service/ingestion=DEBUG"
	if got != want {
		t.Fatalf("Summary(json) = %q want %q", got, want)
	}
}

func TestParseLOGLevel_quotedJSON(t *testing.T) {
	// .env with LOG_LEVEL='{"...}' can leave quotes in the value; stripping allows JSON parse.
	env := `'{"server/plugins": "debug", "server/service/ingestion": "debug", "default": "info"}'`
	global, prefixMap, defaultLevel := ParseLOGLevel(env)
	if global != nil {
		t.Fatalf("expected nil global for quoted JSON, got %v", global)
	}
	if prefixMap == nil {
		t.Fatal("expected non-nil prefixMap for quoted JSON")
	}
	if l := prefixMap["server/plugins"]; l != slog.LevelDebug {
		t.Fatalf("server/plugins want debug got %v", l)
	}
	if l := prefixMap["server/service/ingestion"]; l != slog.LevelDebug {
		t.Fatalf("server/service/ingestion want debug got %v", l)
	}
	if defaultLevel != slog.LevelInfo {
		t.Fatalf("default want info got %v", defaultLevel)
	}
}

func TestWithCategory(t *testing.T) {
	inner := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))
	l := WithCategory(inner, "server/plugins/openfigi")
	if l == nil {
		t.Fatal("WithCategory returned nil")
	}
	// WithCategory is just l.With(categoryKey, category); no way to inspect without logging
	if WithCategory(nil, "x") != nil {
		t.Fatal("WithCategory(nil, x) should return nil")
	}
}
