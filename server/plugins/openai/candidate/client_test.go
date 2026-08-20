package candidate

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/leedenison/portfoliodb/server/identifier"
	candpkg "github.com/leedenison/portfoliodb/server/identifier/candidate"
)

// completionJSON renders one item's reply in the shape the schema requires:
// every field present, every value nullable.
func completionJSON(id, ticker, exchange, currency, occ string) string {
	f := func(v string, conf float64) string {
		if v == "" {
			return `{"value":null,"confidence":0}`
		}
		return `{"value":"` + v + `","confidence":` + strings.TrimRight(strings.TrimRight(formatFloat(conf), "0"), ".") + `}`
	}
	return `{"id":"` + id + `","ticker":` + f(ticker, 0.9) +
		`,"exchange":` + f(exchange, 0.7) +
		`,"currency":` + f(currency, 0.8) +
		`,"occ":` + f(occ, 0.95) + `}`
}

func formatFloat(f float64) string {
	b, _ := json.Marshal(f)
	return string(b)
}

// chatBody wraps content in the envelope the API returns.
func chatBody(content string) map[string]any {
	return map[string]any{
		"choices": []map[string]any{
			{"finish_reason": "stop", "message": map[string]any{"content": content}},
		},
		"usage": map[string]int64{"prompt_tokens": 10, "completion_tokens": 20, "total_tokens": 30},
	}
}

// The request carries a strict schema, a zero temperature and a seed. Without
// the schema the model is free to answer in any shape, which is what the
// fence-stripping parser this replaced existed to cope with.
func TestPostChunk_SendsAStrictSchema(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&got)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(chatBody(`{"items":[]}`))
	}))
	defer srv.Close()

	c := NewClient("k", "", srv.URL, 0, 0, http.DefaultClient)
	if _, _, err := c.CompleteBatch(context.Background(), []BatchItemForClient{{ID: "a", Description: "X"}}); err != nil {
		t.Fatalf("CompleteBatch: %v", err)
	}
	rf, ok := got["response_format"].(map[string]any)
	if !ok {
		t.Fatalf("no response_format in request: %v", got)
	}
	if rf["type"] != "json_schema" {
		t.Errorf("response_format.type = %v, want json_schema", rf["type"])
	}
	js, _ := rf["json_schema"].(map[string]any)
	if js["strict"] != true {
		t.Errorf("json_schema.strict = %v, want true", js["strict"])
	}
	if got["temperature"] != float64(0) {
		t.Errorf("temperature = %v, want 0", got["temperature"])
	}
	if got["seed"] != float64(completionSeed) {
		t.Errorf("seed = %v, want %d", got["seed"], completionSeed)
	}
}

// Only the fields a source supplied are written into the prompt. A field the
// model is not shown is one it cannot hand back as though it had recalled it.
func TestUserContent_SendsOnlyWhatIsKnown(t *testing.T) {
	out := userContent([]BatchItemForClient{{
		ID:          "a1",
		Description: "BERKSHIRE HATHAWAY INC-CL B",
		TypeHint:    "STOCK",
		Known:       Known{ISIN: "US0846707026", Currency: "USD"},
	}})
	for _, want := range []string{"id: a1", "asset class: STOCK", "known isin: US0846707026", "known currency: USD"} {
		if !strings.Contains(out, want) {
			t.Errorf("prompt missing %q:\n%s", want, out)
		}
	}
	for _, unwanted := range []string{"known ticker", "known exchange", "known cusip", "known sedol"} {
		if strings.Contains(out, unwanted) {
			t.Errorf("prompt offered a blank for %q:\n%s", unwanted, out)
		}
	}
}

// A 429 is retried once. The orchestrator calls ProposeBatch once per plugin and
// cannot re-run a chunk, so without this a single rate limit costs the whole
// batch its proposals.
func TestCompleteChunk_RetriesOnRateLimit(t *testing.T) {
	old := RetryBackoff
	RetryBackoff = time.Millisecond
	defer func() { RetryBackoff = old }()

	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			http.Error(w, `{"error":{"code":"rate_limit_exceeded"}}`, http.StatusTooManyRequests)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(chatBody(`{"items":[` + completionJSON("a", "AAPL", "XNAS", "USD", "") + `]}`))
	}))
	defer srv.Close()

	c := NewClient("k", "", srv.URL, 0, 0, http.DefaultClient)
	got, _, err := c.CompleteBatch(context.Background(), []BatchItemForClient{{ID: "a", Description: "APPLE"}})
	if err != nil {
		t.Fatalf("CompleteBatch: %v", err)
	}
	if calls != 2 {
		t.Errorf("calls = %d, want 2", calls)
	}
	if got["a"] == nil || got["a"].Ticker.Value != "AAPL" {
		t.Errorf("got = %+v, want the retry's answer", got["a"])
	}
}

// A 400 is the request being wrong. Sending it again would only be wrong twice.
func TestCompleteChunk_DoesNotRetryABadRequest(t *testing.T) {
	old := RetryBackoff
	RetryBackoff = time.Millisecond
	defer func() { RetryBackoff = old }()

	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		http.Error(w, `{"error":{"code":"invalid_request_error"}}`, http.StatusBadRequest)
	}))
	defer srv.Close()

	c := NewClient("k", "", srv.URL, 0, 0, http.DefaultClient)
	if _, _, err := c.CompleteBatch(context.Background(), []BatchItemForClient{{ID: "a"}}); err == nil {
		t.Fatal("expected an error")
	}
	if calls != 1 {
		t.Errorf("calls = %d, want 1", calls)
	}
}

// A reply cut off at the token budget is named for what it is. It used to
// surface as invalid JSON, which points at the wrong fix.
func TestPostChunk_TruncationIsNamed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{
				{"finish_reason": "length", "message": map[string]any{"content": `{"items":[{"id":"a"`}},
			},
		})
	}))
	defer srv.Close()

	c := NewClient("k", "", srv.URL, 0, 0, http.DefaultClient)
	_, _, err := c.CompleteBatch(context.Background(), []BatchItemForClient{{ID: "a"}})
	if err == nil || !strings.Contains(err.Error(), "truncated") {
		t.Fatalf("err = %v, want a truncation error", err)
	}
}

// A refusal arrives as its own field rather than as prose in the content, so it
// has to be read as one or it parses as an empty answer.
func TestPostChunk_RefusalIsAnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{
				{"finish_reason": "stop", "message": map[string]any{"refusal": "no"}},
			},
		})
	}))
	defer srv.Close()

	c := NewClient("k", "", srv.URL, 0, 0, http.DefaultClient)
	_, _, err := c.CompleteBatch(context.Background(), []BatchItemForClient{{ID: "a"}})
	if err == nil || !strings.Contains(err.Error(), "refused") {
		t.Fatalf("err = %v, want a refusal error", err)
	}
}

// The venue travels on the ticker, because a MIC_TICKER is one identifier: an
// exchange with no symbol to qualify names nothing the resolver can look up.
func TestProposals_VenueTravelsOnTheTicker(t *testing.T) {
	got := proposals(&Completion{
		Ticker:   Field{Value: "BRK.B", Confidence: 0.95},
		Exchange: Field{Value: "XNYS", Confidence: 0.6},
		Currency: Field{Value: "USD", Confidence: 0.9},
	}, BatchItemForClient{TypeHint: identifier.SecurityTypeHintStock})
	if len(got) != 2 {
		t.Fatalf("proposals = %v, want a ticker and a currency", got)
	}
	if got[0].Identifier.Type != "MIC_TICKER" || got[0].Identifier.Domain != "XNYS" || got[0].Identifier.Value != "BRK.B" {
		t.Errorf("first proposal = %+v, want MIC_TICKER XNYS:BRK.B", got[0].Identifier)
	}
	// The weaker of the two claims names the field and carries the confidence:
	// a right ticker on a wrong venue is a different failure from both wrong.
	if got[0].Field != candpkg.FieldExchange || got[0].Confidence != 0.6 {
		t.Errorf("first proposal = %s/%v, want the exchange's weaker claim", got[0].Field, got[0].Confidence)
	}
	if got[1].Field != candpkg.FieldCurrency || got[1].Identifier.Type != "CURRENCY" {
		t.Errorf("second proposal = %+v, want a CURRENCY", got[1])
	}
}

// An OCC symbol is offered alone: it names the contract, its underlying, its
// expiry and its strike at once.
func TestProposals_AnOCCSymbolStandsAlone(t *testing.T) {
	got := proposals(&Completion{
		OCC:    Field{Value: "AAPL  251219C00200000", Confidence: 0.9},
		Ticker: Field{Value: "AAPL", Confidence: 0.9},
	}, BatchItemForClient{TypeHint: identifier.SecurityTypeHintOption})
	if len(got) != 1 || got[0].Identifier.Type != "OCC" {
		t.Fatalf("proposals = %v, want the OCC symbol alone", got)
	}
	// Normalised to the compact form the database stores.
	if got[0].Identifier.Value != "AAPL251219C00200000" {
		t.Errorf("Value = %q, want the compact form", got[0].Identifier.Value)
	}
	if got[0].Field != candpkg.FieldKey {
		t.Errorf("Field = %q, want %q", got[0].Field, candpkg.FieldKey)
	}
}

// What a source stated reaches the prompt; what a plugin proposed does not.
func TestKnownFrom_ReadsOnlyWhatTheSourceStated(t *testing.T) {
	got := knownFrom(candpkg.BatchItem{
		Hints: identifier.Hints{Currency: "GBX"},
		Stated: []identifier.Identifier{
			{Type: "ISIN", Value: "GB00B10RZP78"},
			{Type: "MIC_TICKER", Domain: "XLON", Value: "ULVR"},
		},
	})
	want := Known{Ticker: "ULVR", Exchange: "XLON", Currency: "GBX", ISIN: "GB00B10RZP78"}
	if got != want {
		t.Errorf("knownFrom = %+v, want %+v", got, want)
	}
}

// An option whose symbol will not parse is offered nothing. The ticker beside it
// is the underlying, and proposing that would resolve the contract to the share.
func TestProposals_AnUnparseableOptionSymbolIsDropped(t *testing.T) {
	got := proposals(&Completion{
		OCC:    Field{Value: "AAPL", Confidence: 0.9},
		Ticker: Field{Value: "AAPL", Confidence: 0.9},
	}, BatchItemForClient{TypeHint: identifier.SecurityTypeHintOption})
	if len(got) != 0 {
		t.Errorf("proposals = %v, want nothing for an unparseable OCC symbol", got)
	}
}

// The model returns the fields the source already supplied however firmly it is
// asked not to, so they are dropped in code rather than in prose.
func TestProposals_AlreadyKnownFieldsAreDropped(t *testing.T) {
	got := proposals(&Completion{
		Ticker:   Field{Value: "ULVR", Confidence: 0.9},
		Exchange: Field{Value: "XLON", Confidence: 0.9},
		Currency: Field{Value: "GBX", Confidence: 1},
	}, BatchItemForClient{
		TypeHint: identifier.SecurityTypeHintStock,
		Known:    Known{Currency: "GBX", Ticker: "ULVR", Exchange: "XLON"},
	})
	if len(got) != 0 {
		t.Errorf("proposals = %v, want nothing: the source supplied all of it", got)
	}
}
