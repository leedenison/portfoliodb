package description

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/leedenison/portfoliodb/server/identifier"
	descpkg "github.com/leedenison/portfoliodb/server/identifier/description"
	"github.com/leedenison/portfoliodb/server/telemetry"
)

func TestIsOpenAIModelNotFound(t *testing.T) {
	tests := []struct {
		err  string
		want bool
	}{
		{"openai 404: {}", true},
		{`openai 404: {"error":{"code":"model_not_found"}}`, true},
		{"the model `gpt-5.2` was not found", true},
		{"openai 429: quota", false},
		{"openai 200: ok", false},
	}
	for _, tt := range tests {
		got := isOpenAIModelNotFound(tt.err)
		if got != tt.want {
			t.Errorf("isOpenAIModelNotFound(%q) = %v, want %v", tt.err, got, tt.want)
		}
	}
}

func TestIsOpenAIQuotaExceeded(t *testing.T) {
	tests := []struct {
		err  string
		want bool
	}{
		{"openai 429: {}", true},
		{`{"error":{"code":"insufficient_quota"}}`, true},
		{"You exceeded your current quota", true},
		{"openai 404: not found", false},
		{"openai 200: ok", false},
	}
	for _, tt := range tests {
		got := isOpenAIQuotaExceeded(tt.err)
		if got != tt.want {
			t.Errorf("isOpenAIQuotaExceeded(%q) = %v, want %v", tt.err, got, tt.want)
		}
	}
}

// recordingCounter records the last counter name passed to Incr.
type recordingCounter struct {
	last string
}

func (r *recordingCounter) Incr(_ context.Context, name string) { r.last = name }

func (r *recordingCounter) IncrBy(_ context.Context, name string, _ int64) { r.last = name }

func TestHandleOpenAIError_IncrementsCounters(t *testing.T) {
	ctx := context.Background()

	t.Run("model not found", func(t *testing.T) {
		c := &recordingCounter{}
		p := NewPlugin(c, slog.Default())
		p.handleOpenAIError(ctx, "INRG", &errWithMessage{"openai 404: model not found"})
		if c.last != CounterModelNotFound {
			t.Errorf("counter = %q, want %q", c.last, CounterModelNotFound)
		}
	})

	t.Run("quota exceeded", func(t *testing.T) {
		c := &recordingCounter{}
		p := NewPlugin(c, slog.Default())
		p.handleOpenAIError(ctx, "VUSA", &errWithMessage{"openai 429: insufficient_quota"})
		if c.last != CounterQuotaExceeded {
			t.Errorf("counter = %q, want %q", c.last, CounterQuotaExceeded)
		}
	})

	t.Run("nil counter no panic", func(t *testing.T) {
		p := NewPlugin(nil, slog.Default())
		p.handleOpenAIError(ctx, "X", &errWithMessage{"openai 404: not found"})
	})

	t.Run("other error no increment", func(t *testing.T) {
		c := &recordingCounter{}
		p := NewPlugin(c, slog.Default())
		p.handleOpenAIError(ctx, "X", &errWithMessage{"openai 500: internal server error"})
		if c.last != "" {
			t.Errorf("unexpected counter increment: %q", c.last)
		}
	})
}

type errWithMessage struct{ msg string }

func (e *errWithMessage) Error() string { return e.msg }

var _ telemetry.CounterIncrementer = (*recordingCounter)(nil)

func TestExtractBatch_TypeHintPassedToClient(t *testing.T) {
	// BatchItemForClient must include TypeHint from Hints.SecurityTypeHint; when server returns OCC, plugin emits OCC identifier.
	var receivedContent string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		// Capture user message to verify type is included
		var body struct {
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || len(body.Messages) < 2 {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		receivedContent = body.Messages[1].Content
		// Return OCC for the single item (id "ab12")
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"choices": []map[string]interface{}{
				{"message": map[string]string{"content": `{"ab12": {"OCC": "BRKB241115P00390000"}}`}},
			},
			"usage": map[string]int64{"prompt_tokens": 1, "completion_tokens": 1, "total_tokens": 2},
		})
	}))
	defer server.Close()

	config := []byte(`{"openai_api_key":"test","openai_base_url":"` + server.URL + `"}`)
	ctx := context.Background()
	p := NewPlugin(nil, nil)
	items := []descpkg.BatchItem{
		{ID: "ab12", InstrumentDescription: "BRKB 241115P00390000 BRK B 15NOV24 390 P", Hints: identifier.Hints{SecurityTypeHint: identifier.SecurityTypeHintOption}},
	}
	out, err := p.ExtractBatch(ctx, config, "IBKR", "IBKR:test:statement", items)
	if err != nil {
		t.Fatalf("ExtractBatch: %v", err)
	}
	if out == nil {
		t.Fatal("expected non-nil out")
	}
	ids, ok := out["ab12"]
	if !ok || len(ids) != 1 {
		t.Fatalf("out[ab12] = %v, want one OCC identifier", out)
	}
	if ids[0].Type != "OCC" || ids[0].Value != "BRKB241115P00390000" {
		t.Errorf("out[ab12] = %+v, want Type=OCC Value=BRKB241115P00390000", ids[0])
	}
	if receivedContent != "" && !strings.Contains(receivedContent, "OPTION") {
		t.Errorf("user content should include type OPTION, got: %s", receivedContent)
	}
}
