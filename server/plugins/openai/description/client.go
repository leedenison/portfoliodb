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

// NormalizedIdentifier is the structured result of normalizing a broker description.
// Type is one of TICKER, ISIN, CUSIP, SHARE_CLASS_FIGI; Value is the identifier value.
type NormalizedIdentifier struct {
	Type  string // TICKER, ISIN, CUSIP, or SHARE_CLASS_FIGI
	Value string
}

// Allowed identifier types for mapping (must match OpenFIGI Mapping API idTypes).
var allowedTypes = map[string]bool{"TICKER": true, "ISIN": true, "CUSIP": true, "SHARE_CLASS_FIGI": true}

// parseStructuredResponse parses a line in the form "TYPE: VALUE" (e.g. "TICKER: AAPL").
// Returns (type, value, true) on success, or ("", "", false) if the format is invalid or type not allowed.
func parseStructuredResponse(line string) (idType, value string, ok bool) {
	line = strings.TrimSpace(line)
	if line == "" {
		return "", "", false
	}
	before, after, found := strings.Cut(line, ":")
	if !found {
		return "", "", false
	}
	idType = strings.TrimSpace(strings.ToUpper(before))
	value = strings.TrimSpace(after)
	if !allowedTypes[idType] || value == "" {
		return "", "", false
	}
	return idType, value, true
}

// NormalizeDescription asks the model to return a structured identifier (type + value) for instrument mapping.
// The prompt requires the format "TYPE: VALUE" where TYPE is TICKER, ISIN, CUSIP, or SHARE_CLASS_FIGI.
// Returns the parsed identifier or an error.
func (c *Client) NormalizeDescription(ctx context.Context, brokerDescription string) (*NormalizedIdentifier, error) {
	if c.apiKey == "" {
		return nil, fmt.Errorf("openai api key required")
	}
	systemPrompt := `You convert broker security descriptions into a single identifier for instrument mapping.
Reply with exactly one line in this format: TYPE: VALUE
- TYPE must be exactly one of: TICKER, ISIN, CUSIP, SHARE_CLASS_FIGI (use the one that best matches the description).
- VALUE: for TICKER use primary exchange symbol (e.g. AAPL); for ISIN use 12-character ISIN (e.g. US0378331005); for CUSIP use 9-character CUSIP; for SHARE_CLASS_FIGI use the 12-character OpenFIGI share-class FIGI (e.g. BBG001234567).
No other text, no explanation, no markdown. Example: TICKER: AAPL`
	reqBody := map[string]interface{}{
		"model": c.model,
		"messages": []map[string]string{
			{"role": "system", "content": systemPrompt},
			{"role": "user", "content": brokerDescription},
		},
		"max_tokens":  40,
		"temperature": 0,
	}
	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		slurp, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("openai %d: %s", resp.StatusCode, string(slurp))
	}
	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	if len(out.Choices) == 0 {
		return nil, fmt.Errorf("openai: no choices")
	}
	raw := strings.TrimSpace(out.Choices[0].Message.Content)
	// Take first line only in case the model adds extra lines
	if idx := strings.Index(raw, "\n"); idx >= 0 {
		raw = strings.TrimSpace(raw[:idx])
	}
	idType, value, ok := parseStructuredResponse(raw)
	if !ok {
		return nil, fmt.Errorf("openai: invalid response format (expected TYPE: VALUE), got %q", raw)
	}
	return &NormalizedIdentifier{Type: idType, Value: value}, nil
}
