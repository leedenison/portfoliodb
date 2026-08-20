package candidate

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const defaultBaseURL = "https://api.openai.com"
const defaultModel = "gpt-4o-mini"

const defaultBatchChunkSize = 50

// defaultMaxCompletionTokens bounds one chunk's reply. A structured answer costs
// far more output than the bare ticker the old prompt asked for -- four fields
// per item, each a value and a confidence -- so a budget sized for that prompt
// truncates this one. A truncated reply is not partial data: the JSON is cut
// mid-object and the whole chunk is lost, which is why finishReasonLength is
// reported as its own failure rather than surfacing as a parse error.
const defaultMaxCompletionTokens = 12000

// completionSeed is sent on every request. It does not make the model
// deterministic -- OpenAI documents seed as best-effort -- but it removes one
// source of variation between two runs of the same batch, which is what the
// measurement in 0134 needs and what keeps a re-recorded cassette comparable
// with the one it replaced.
const completionSeed = 20260401

// RetryBackoff is the delay before retrying a chunk OpenAI refused with a 429 or
// a 5xx. Variable, not const, so tests can shorten it.
var RetryBackoff = 2 * time.Second

// Client calls OpenAI Chat Completions to complete a partial instrument
// identity: given a broker description and whatever the source already stated,
// it asks for the fields that are missing.
type Client struct {
	baseURL             string
	apiKey              string
	model               string
	batchChunkSize      int
	maxCompletionTokens int
	httpClient          *http.Client
}

// NewClient creates a client. apiKey is required for calls.
func NewClient(apiKey, model, baseURL string, batchChunkSize, maxCompletionTokens int, httpClient *http.Client) *Client {
	if model == "" {
		model = defaultModel
	}
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	if batchChunkSize <= 0 {
		batchChunkSize = defaultBatchChunkSize
	}
	if maxCompletionTokens <= 0 {
		maxCompletionTokens = defaultMaxCompletionTokens
	}
	return &Client{
		baseURL:             baseURL,
		apiKey:              apiKey,
		model:               model,
		batchChunkSize:      batchChunkSize,
		maxCompletionTokens: maxCompletionTokens,
		httpClient:          httpClient,
	}
}

// Field is one thing the model was asked to complete: the value it offered, or
// empty when it declined, and what it says about its own answer.
//
// Confidence is recorded and never gated on. See candidate.Proposal.
type Field struct {
	Value      string
	Confidence float64
}

// Completion is what the model returned for one item. A field it would not guess
// comes back with an empty Value, which is a decision it was made to state
// rather than a key it happened to omit: every field is required by the schema
// and nullable, so declining is explicit.
type Completion struct {
	Ticker   Field
	Exchange Field
	Currency Field
	OCC      Field
}

// Known is what the source already said about an instrument, and what separates
// this prompt from the one it replaced. Only the fields that are set are sent:
// a blank line invites the model to fill it with something, and a field it is
// not shown is one it cannot echo back as though it had recalled it.
type Known struct {
	Ticker   string
	Exchange string
	Currency string
	ISIN     string
	CUSIP    string
	SEDOL    string
}

// BatchItemForClient is one instrument to complete.
type BatchItemForClient struct {
	ID          string
	Description string
	// TypeHint is the asset class the source stated, which decides whether an
	// OCC symbol is worth asking for.
	TypeHint string
	Known    Known
}

// systemPrompt states the task and the vocabularies. It asks for the fields that
// are missing rather than for an identifier, which is the whole difference
// between a candidate plugin and the extraction step it grew out of.
//
// Two things it deliberately does not do. It does not offer a list of valid
// answers to choose from: an answer that is valid by construction tells the
// resolution nothing when it checks validity afterwards. And it does not ask for
// an ISIN, CUSIP or SEDOL. A ticker is usually present in the description and a
// model reading one is reading; an ISIN is twelve check-digited characters it
// can only recall or confabulate, and a plausible confabulated ISIN is a real
// ISIN belonging to another company -- the failure adr/0059 is built around.
const systemPrompt = `You complete partial instrument identities for a portfolio system.

For each item you are given a broker's free-text description, the asset class the source stated, and any fields the source already supplied. Fill in every field you can determine.

Fields:
- ticker: the exchange symbol the instrument is quoted under.
- exchange: the ISO 10383 operating MIC of its primary listing venue. NASDAQ is XNAS, the New York Stock Exchange is XNYS, the London Stock Exchange is XLON, Xetra is XETR, Euronext Paris is XPAR, Toronto is XTSE, the Australian Securities Exchange is XASX, Tokyo is XJPX. If you can name the venue, give its MIC.
- currency: the ISO 4217 code it trades in. London pence is GBX.
- occ: the 21-character OCC option symbol, and only when the asset class is OPTION.
- confidence: your own estimate for that field, between 0 and 1.

Return null for a field the source already supplied, and null for one you genuinely do not know. Set confidence to 0 whenever the value is null.

Return one entry per item, with the id exactly as given.`

// candidateSchema is the JSON schema the response is validated against by
// OpenAI before it is returned, which is why nothing here parses defensively.
//
// The shape is an array of items rather than an object keyed by id, because a
// strict schema cannot describe an object whose keys are not known in advance.
// Every property is required and every value is nullable, so "I do not know" is
// a thing the model has to say rather than a key it can leave out.
func candidateSchema() map[string]any {
	field := func(desc string) map[string]any {
		return map[string]any{
			"type": "object",
			"properties": map[string]any{
				"value":      map[string]any{"type": []string{"string", "null"}, "description": desc},
				"confidence": map[string]any{"type": "number"},
			},
			"required":             []string{"value", "confidence"},
			"additionalProperties": false,
		}
	}
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"items": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"id":       map[string]any{"type": "string"},
						"ticker":   field("exchange symbol"),
						"exchange": field("ISO 10383 operating MIC"),
						"currency": field("ISO 4217 code"),
						"occ":      field("21-character OCC option symbol, options only"),
					},
					"required":             []string{"id", "ticker", "exchange", "currency", "occ"},
					"additionalProperties": false,
				},
			},
		},
		"required":             []string{"items"},
		"additionalProperties": false,
	}
}

// userContent renders one chunk. Only the fields a source actually supplied are
// written, so the model is never shown a blank to fill.
func userContent(chunk []BatchItemForClient) string {
	var sb strings.Builder
	for _, it := range chunk {
		fmt.Fprintf(&sb, "id: %s\n", it.ID)
		if it.TypeHint != "" {
			fmt.Fprintf(&sb, "asset class: %s\n", it.TypeHint)
		}
		fmt.Fprintf(&sb, "description: %s\n", it.Description)
		for _, kv := range []struct{ k, v string }{
			{"ticker", it.Known.Ticker},
			{"exchange", it.Known.Exchange},
			{"currency", it.Known.Currency},
			{"isin", it.Known.ISIN},
			{"cusip", it.Known.CUSIP},
			{"sedol", it.Known.SEDOL},
		} {
			if kv.v != "" {
				fmt.Fprintf(&sb, "known %s: %s\n", kv.k, kv.v)
			}
		}
		sb.WriteString("\n")
	}
	return strings.TrimSpace(sb.String())
}

// completionField mirrors one field of the schema.
type completionField struct {
	Value      *string `json:"value"`
	Confidence float64 `json:"confidence"`
}

func (f completionField) field() Field {
	if f.Value == nil {
		return Field{}
	}
	return Field{Value: strings.TrimSpace(*f.Value), Confidence: f.Confidence}
}

type completionItem struct {
	ID       string          `json:"id"`
	Ticker   completionField `json:"ticker"`
	Exchange completionField `json:"exchange"`
	Currency completionField `json:"currency"`
	OCC      completionField `json:"occ"`
}

type completionResponse struct {
	Items []completionItem `json:"items"`
}

// chatResponse is the envelope. Refusal is the model declining the request
// outright, which structured outputs report as a field rather than as prose in
// the content, and FinishReason names a reply that was cut short.
type chatResponse struct {
	Choices []struct {
		FinishReason string `json:"finish_reason"`
		Message      struct {
			Content string  `json:"content"`
			Refusal *string `json:"refusal"`
		} `json:"message"`
	} `json:"choices"`
	Usage *struct {
		PromptTokens     int64 `json:"prompt_tokens"`
		CompletionTokens int64 `json:"completion_tokens"`
		TotalTokens      int64 `json:"total_tokens"`
	} `json:"usage"`
}

// CompleteBatch asks the model to complete each item's identity, in chunks.
// Returns a map keyed by BatchItemForClient.ID; usage is merged across chunks.
func (c *Client) CompleteBatch(ctx context.Context, items []BatchItemForClient) (map[string]*Completion, *Usage, error) {
	if c.apiKey == "" {
		return nil, nil, fmt.Errorf("openai api key required")
	}
	if len(items) == 0 {
		return nil, nil, nil
	}
	merged := make(map[string]*Completion)
	var totalUsage *Usage
	for start := 0; start < len(items); start += c.batchChunkSize {
		end := min(start+c.batchChunkSize, len(items))
		parsed, usage, err := c.completeChunk(ctx, items[start:end])
		if err != nil {
			return nil, nil, err
		}
		for i := range parsed.Items {
			it := &parsed.Items[i]
			// First entry per id wins. The model emits a second, all-null entry
			// for the same id often enough to matter -- the schema permits it,
			// since an array cannot express "one per id" -- and taking the last
			// would throw away the answer in favour of the blank that follows it.
			if _, seen := merged[it.ID]; seen {
				continue
			}
			merged[it.ID] = &Completion{
				Ticker:   it.Ticker.field(),
				Exchange: it.Exchange.field(),
				Currency: it.Currency.field(),
				OCC:      it.OCC.field(),
			}
		}
		if usage != nil {
			if totalUsage == nil {
				totalUsage = &Usage{}
			}
			totalUsage.PromptTokens += usage.PromptTokens
			totalUsage.CompletionTokens += usage.CompletionTokens
			totalUsage.TotalTokens += usage.TotalTokens
		}
	}
	return merged, totalUsage, nil
}

// completeChunk sends one chunk, retrying once on a status worth retrying.
//
// The retry lives here because nothing above can do it: the orchestrator calls
// ProposeBatch once per plugin and has no way to re-run a chunk, so a single 429
// used to cost the whole batch its proposals.
func (c *Client) completeChunk(ctx context.Context, chunk []BatchItemForClient) (*completionResponse, *Usage, error) {
	parsed, usage, err := c.postChunk(ctx, chunk)
	if err == nil || !retryable(err) {
		return parsed, usage, err
	}
	select {
	case <-ctx.Done():
		return nil, nil, ctx.Err()
	case <-time.After(RetryBackoff):
	}
	return c.postChunk(ctx, chunk)
}

// statusError is an HTTP status OpenAI returned, kept as a type so the retry
// decision reads the code rather than the message.
type statusError struct {
	code int
	body string
}

func (e *statusError) Error() string { return fmt.Sprintf("openai %d: %s", e.code, e.body) }

// retryable reports whether an error is worth one more attempt: a rate limit or
// a server-side failure. A 4xx that is not 429 is the request being wrong, and
// sending it again would only be wrong twice.
func retryable(err error) bool {
	var se *statusError
	if !errors.As(err, &se) {
		return false
	}
	return se.code == http.StatusTooManyRequests || se.code >= 500
}

func (c *Client) postChunk(ctx context.Context, chunk []BatchItemForClient) (*completionResponse, *Usage, error) {
	reqBody := map[string]any{
		"model": c.model,
		"messages": []map[string]string{
			{"role": "system", "content": systemPrompt},
			{"role": "user", "content": userContent(chunk)},
		},
		"max_completion_tokens": c.maxCompletionTokens,
		"temperature":           0,
		"seed":                  completionSeed,
		"response_format": map[string]any{
			"type": "json_schema",
			"json_schema": map[string]any{
				"name":   "instrument_candidates",
				"strict": true,
				"schema": candidateSchema(),
			},
		},
	}
	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, nil, err
	}
	slurp, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, nil, &statusError{code: resp.StatusCode, body: string(slurp)}
	}
	var out chatResponse
	if err := json.Unmarshal(slurp, &out); err != nil {
		return nil, nil, fmt.Errorf("openai batch decode: %w", err)
	}
	if len(out.Choices) == 0 {
		return nil, nil, fmt.Errorf("openai: no choices")
	}
	choice := out.Choices[0]
	if choice.Message.Refusal != nil && *choice.Message.Refusal != "" {
		return nil, nil, fmt.Errorf("openai refused the request: %s", *choice.Message.Refusal)
	}
	if choice.FinishReason == "length" {
		// Named rather than left to fail as a parse error, because the fix is a
		// larger max_completion_tokens or a smaller chunk and neither is
		// suggested by "invalid JSON".
		return nil, nil, fmt.Errorf("openai: reply truncated at %d completion tokens; lower batch_chunk_size or raise max_completion_tokens", c.maxCompletionTokens)
	}
	// Parsed strictly. The schema was enforced upstream, so anything that fails
	// here is a contract change worth failing on rather than working around --
	// which is why the markdown-fence stripping this replaced is gone rather
	// than kept as a fallback.
	var parsed completionResponse
	if err := json.Unmarshal([]byte(choice.Message.Content), &parsed); err != nil {
		return nil, nil, fmt.Errorf("openai batch: response did not match the schema: %w", err)
	}
	var usage *Usage
	if out.Usage != nil {
		usage = &Usage{
			PromptTokens:     out.Usage.PromptTokens,
			CompletionTokens: out.Usage.CompletionTokens,
			TotalTokens:      out.Usage.TotalTokens,
		}
	}
	return &parsed, usage, nil
}

// Usage holds token counts from an OpenAI completion response.
type Usage struct {
	PromptTokens     int64
	CompletionTokens int64
	TotalTokens      int64
}
