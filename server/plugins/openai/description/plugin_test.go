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

func TestHandleOpenAIError_Classifies(t *testing.T) {
	ctx := context.Background()
	p := NewPlugin(slog.Default(), http.DefaultClient)

	tests := []struct {
		name string
		err  string
		want descpkg.Outcome
	}{
		{name: "model not found", err: "openai 404: model not found", want: descpkg.OutcomeModelNotFound},
		{name: "quota exceeded", err: "openai 429: insufficient_quota", want: descpkg.OutcomeQuotaExceeded},
		// Both arrive as 429; only the body marker tells them apart.
		{name: "rate limited", err: "openai 429: rate_limit_exceeded", want: descpkg.OutcomeRateLimited},
		{name: "other", err: "openai 500: internal server error", want: descpkg.OutcomeError},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := p.handleOpenAIError(ctx, "INRG", &errWithMessage{tc.err})
			if got != tc.want {
				t.Errorf("handleOpenAIError(%q) = %q, want %q", tc.err, got, tc.want)
			}
		})
	}
}

func TestHandleOpenAIError_NilLogger(t *testing.T) {
	p := NewPlugin(nil, http.DefaultClient)
	if got := p.handleOpenAIError(context.Background(), "X", &errWithMessage{"openai 404: not found"}); got != descpkg.OutcomeModelNotFound {
		t.Errorf("outcome = %q, want %q", got, descpkg.OutcomeModelNotFound)
	}
}

type errWithMessage struct{ msg string }

func (e *errWithMessage) Error() string { return e.msg }

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
	p := NewPlugin(nil, http.DefaultClient)
	items := []descpkg.BatchItem{
		{ID: "ab12", InstrumentDescription: "BRKB 241115P00390000 BRK B 15NOV24 390 P", Hints: identifier.Hints{SecurityTypeHint: identifier.SecurityTypeHintOption}},
	}
	res, err := p.ExtractBatch(ctx, config, "IBKR", "IBKR:test:statement", items)
	if err != nil {
		t.Fatalf("ExtractBatch: %v", err)
	}
	if res.Telemetry.Outcome != descpkg.OutcomeHintsReturned {
		t.Errorf("Telemetry.Outcome = %q, want %q", res.Telemetry.Outcome, descpkg.OutcomeHintsReturned)
	}
	// The token cost of the call is what makes the cost of one import answerable.
	if res.Telemetry.Tokens == nil {
		t.Fatal("Telemetry.Tokens = nil, want the usage the API reported")
	}
	if res.Telemetry.Tokens.PromptTokens != 1 || res.Telemetry.Tokens.CompletionTokens != 1 || res.Telemetry.Tokens.TotalTokens != 2 {
		t.Errorf("Telemetry.Tokens = %+v, want {1 1 2}", *res.Telemetry.Tokens)
	}
	ids, ok := res.Hints["ab12"]
	if !ok || len(ids) != 1 {
		t.Fatalf("Hints[ab12] = %v, want one OCC identifier", res.Hints)
	}
	if ids[0].Type != "OCC" || ids[0].Value != "BRKB241115P00390000" {
		t.Errorf("out[ab12] = %+v, want Type=OCC Value=BRKB241115P00390000", ids[0])
	}
	if receivedContent != "" && !strings.Contains(receivedContent, "OPTION") {
		t.Errorf("user content should include type OPTION, got: %s", receivedContent)
	}
}

func TestPlugin_AcceptableSecurityTypes_IncludesETF(t *testing.T) {
	p := NewPlugin(nil, nil)
	types := p.AcceptableSecurityTypes()
	if !types[identifier.SecurityTypeHintETF] {
		t.Error("ETF is not acceptable; an ETF arriving with no identifiers gets no description plugin at all")
	}
	// The set stays exclusive: CASH belongs to the cash plugin.
	if types[identifier.SecurityTypeHintCash] {
		t.Error("CASH should not be acceptable to the OpenAI plugin")
	}
}
