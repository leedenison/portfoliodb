package description

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

const defaultBaseURL = "https://api.openai.com"
const defaultModel = "gpt-4o-mini"

// Client calls OpenAI Chat Completions to normalize broker descriptions to a specific identifier (e.g. ticker) for mapping.
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

// NormalizedIdentifiers is the structured result of normalizing a broker description (JSON response).
type NormalizedIdentifiers struct {
	Ticker string // primary exchange symbol (e.g. EQQQ)
	OCC    string // OCC option symbol (21-character), when type is Option
}

// openAIJSONResponse is the shape of the model's JSON response (TICKER and/or OCC).
type openAIJSONResponse struct {
	TICKER string `json:"TICKER"`
	OCC    string `json:"OCC"`
}

// stripJSONFromContent removes markdown code fences if present and returns trimmed JSON string.
func stripJSONFromContent(raw string) string {
	raw = strings.TrimSpace(raw)
	// Remove ```json ... ``` or ``` ... ```
	for _, prefix := range []string{"```json", "```"} {
		if strings.HasPrefix(raw, prefix) {
			raw = raw[len(prefix):]
			if idx := strings.Index(raw, "```"); idx >= 0 {
				raw = raw[:idx]
			}
			break
		}
	}
	return strings.TrimSpace(raw)
}

const defaultBatchChunkSize = 50
const defaultMaxCompletionTokens = 4000

// BatchItemForClient is the input for NormalizeDescriptionsBatch (id, description, and security type hint).
type BatchItemForClient struct {
	ID          string
	Description string
	TypeHint    string // SecurityTypeHint from the request (e.g. Option, Stock); used to choose TICKER vs OCC
}

// NormalizeDescriptionsBatch asks the model to identify multiple descriptions in one (or chunked) request(s).
// Each item has an ID (short hash); the model must return a JSON object keyed by that id. Chunks into groups of batchChunkSize.
// Returns map keyed by ID; usage is merged across chunks.
func (c *Client) NormalizeDescriptionsBatch(ctx context.Context, items []BatchItemForClient) (map[string]*NormalizedIdentifiers, *Usage, error) {
	if c.apiKey == "" {
		return nil, nil, fmt.Errorf("openai api key required")
	}
	if len(items) == 0 {
		return nil, nil, nil
	}
	systemPrompt := `Here is a list of broker instrument descriptions. Each has an id (short hash) and a type (security type). Return a JSON object whose keys are exactly those ids and whose values are objects. If type is "OPTION", extract and return the OCC symbol (21-character standard option symbol) in "OCC". Otherwise extract and return the primary exchange ticker in "TICKER". You must quote each id in the response. If you cannot identify a security, use an empty object for that id. Do not include any other text. Example: { "a1b2c3d4": { "TICKER": "AAPL" }, "e5f6g7h8": { "OCC": "AAPL250117C00150000" } }`
	merged := make(map[string]*NormalizedIdentifiers)
	var totalUsage *Usage
	for start := 0; start < len(items); start += c.batchChunkSize {
		end := start + c.batchChunkSize
		if end > len(items) {
			end = len(items)
		}
		chunk := items[start:end]
		var sb strings.Builder
		for _, it := range chunk {
			sb.WriteString("id: ")
			sb.WriteString(it.ID)
			sb.WriteString("\ntype: ")
			sb.WriteString(it.TypeHint)
			sb.WriteString("\nDescription: ")
			sb.WriteString(it.Description)
			sb.WriteString("\n\n")
		}
		userContent := strings.TrimSpace(sb.String())
		reqBody := map[string]interface{}{
			"model": c.model,
			"messages": []map[string]string{
				{"role": "system", "content": systemPrompt},
				{"role": "user", "content": userContent},
			},
			"max_completion_tokens": c.maxCompletionTokens,
			"temperature":            0,
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
			return nil, nil, fmt.Errorf("openai %d: %s", resp.StatusCode, string(slurp))
		}
		var out struct {
			Choices []struct {
				Message struct {
					Content string `json:"content"`
				} `json:"message"`
			} `json:"choices"`
			Usage *struct {
				PromptTokens     int64 `json:"prompt_tokens"`
				CompletionTokens int64 `json:"completion_tokens"`
				TotalTokens      int64 `json:"total_tokens"`
			} `json:"usage"`
		}
		if err := json.Unmarshal(slurp, &out); err != nil {
			return nil, nil, fmt.Errorf("openai batch decode: %w", err)
		}
		if len(out.Choices) == 0 {
			return nil, nil, fmt.Errorf("openai: no choices")
		}
		raw := stripJSONFromContent(out.Choices[0].Message.Content)
		var parsed map[string]openAIJSONResponse
		if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
			return nil, nil, fmt.Errorf("openai batch: invalid JSON response: %w", err)
		}
		for id, v := range parsed {
			ticker := strings.TrimSpace(v.TICKER)
			occ := strings.TrimSpace(v.OCC)
			if occ != "" || ticker != "" {
				merged[id] = &NormalizedIdentifiers{Ticker: ticker, OCC: occ}
			}
		}
		if out.Usage != nil {
			if totalUsage == nil {
				totalUsage = &Usage{}
			}
			totalUsage.PromptTokens += out.Usage.PromptTokens
			totalUsage.CompletionTokens += out.Usage.CompletionTokens
			totalUsage.TotalTokens += out.Usage.TotalTokens
		}
	}
	return merged, totalUsage, nil
}

// Usage holds token counts from an OpenAI completion response.
type Usage struct {
	PromptTokens     int64
	CompletionTokens int64
	TotalTokens      int64
}
