package description

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const defaultBaseURL = "https://api.openai.com"
const defaultModel = "gpt-4o-mini"

// Client calls OpenAI Chat Completions to normalize broker descriptions to a specific identifier (e.g. ticker) for mapping.
type Client struct {
	baseURL    string
	apiKey     string
	model      string
	httpClient *http.Client
}

// NewClient creates a client. apiKey is required for calls.
func NewClient(apiKey, model, baseURL string) *Client {
	if model == "" {
		model = defaultModel
	}
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	return &Client{
		baseURL: baseURL,
		apiKey:  apiKey,
		model:   model,
		httpClient: &http.Client{
			Timeout: 20 * time.Second,
		},
	}
}

// NormalizedIdentifiers is the structured result of normalizing a broker description (JSON response).
// At least one of Ticker or ShareClassFIGI should be set for the result to be useful.
type NormalizedIdentifiers struct {
	Ticker        string // primary exchange symbol (e.g. EQQQ)
	ShareClassFIGI string // OpenFIGI share-class FIGI (12-char, e.g. BBG...)
}

// openAIJSONResponse is the shape of the model's JSON response.
type openAIJSONResponse struct {
	TICKER          string `json:"TICKER"`
	SHARE_CLASS_FIGI string `json:"SHARE_CLASS_FIGI"`
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

// NormalizeDescription asks the model to return JSON with TICKER and SHARE_CLASS_FIGI.
// Returns identifiers, optional usage (token counts), and an error. Usage may be nil if the response omitted it.
func (c *Client) NormalizeDescription(ctx context.Context, brokerDescription string) (*NormalizedIdentifiers, *Usage, error) {
	if c.apiKey == "" {
		return nil, nil, fmt.Errorf("openai api key required")
	}
	systemPrompt := `Here is a description of a financial security provided by a broker.  Identify it and provide a response in the following JSON format:
	{
		"TICKER": "<TICKER>",
		"SHARE_CLASS_FIGI": "<SHARE_CLASS_FIGI>"
	}
	Do not include any other text in your response.  If you cannot identify the security, return an empty object: {}.  If you do not know the value of a field, leave it empty.  Example: { "TICKER": "AAPL", "SHARE_CLASS_FIGI": "" }`
	reqBody := map[string]interface{}{
		"model": c.model,
		"messages": []map[string]string{
			{"role": "system", "content": systemPrompt},
			{"role": "user", "content": brokerDescription},
		},
		"max_completion_tokens": 80,
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
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		slurp, _ := io.ReadAll(resp.Body)
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
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, nil, err
	}
	if len(out.Choices) == 0 {
		return nil, nil, fmt.Errorf("openai: no choices")
	}
	raw := stripJSONFromContent(out.Choices[0].Message.Content)
	var parsed openAIJSONResponse
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		return nil, nil, fmt.Errorf("openai: invalid JSON response: %w", err)
	}
	ticker := strings.TrimSpace(parsed.TICKER)
	shareClassFIGI := strings.TrimSpace(parsed.SHARE_CLASS_FIGI)
	if ticker == "" && shareClassFIGI == "" {
		return nil, nil, fmt.Errorf("openai: response has no TICKER or SHARE_CLASS_FIGI")
	}
	var usage *Usage
	if out.Usage != nil {
		usage = &Usage{
			PromptTokens:     out.Usage.PromptTokens,
			CompletionTokens: out.Usage.CompletionTokens,
			TotalTokens:      out.Usage.TotalTokens,
		}
	}
	return &NormalizedIdentifiers{Ticker: ticker, ShareClassFIGI: shareClassFIGI}, usage, nil
}

// Usage holds token counts from an OpenAI completion response.
type Usage struct {
	PromptTokens     int64
	CompletionTokens int64
	TotalTokens      int64
}
