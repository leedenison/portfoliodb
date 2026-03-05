package identifier

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

const openAIBaseURL = "https://api.openai.com"

// OpenAIClient calls OpenAI Chat Completions to normalize broker descriptions.
type OpenAIClient struct {
	baseURL    string
	apiKey     string
	model      string
	httpClient *http.Client
}

// NewOpenAIClient creates a client. apiKey is required for calls.
func NewOpenAIClient(apiKey, model string) *OpenAIClient {
	return NewOpenAIClientWithBaseURL(apiKey, model, openAIBaseURL)
}

// NewOpenAIClientWithBaseURL creates a client with a custom base URL (for testing).
func NewOpenAIClientWithBaseURL(apiKey, model, baseURL string) *OpenAIClient {
	if model == "" {
		model = "gpt-4o-mini"
	}
	if baseURL == "" {
		baseURL = openAIBaseURL
	}
	return &OpenAIClient{
		baseURL: baseURL,
		apiKey:  apiKey,
		model:   model,
		httpClient: &http.Client{
			Timeout: 20 * time.Second,
		},
	}
}

// NormalizeDescription asks the model to return a ticker or short search phrase for OpenFIGI.
func (c *OpenAIClient) NormalizeDescription(ctx context.Context, brokerDescription string) (string, error) {
	if c.apiKey == "" {
		return "", fmt.Errorf("openai api key required")
	}
	reqBody := map[string]interface{}{
		"model": c.model,
		"messages": []map[string]string{
			{"role": "system", "content": "You are a helper that converts broker security descriptions into a single ticker symbol or a very short search phrase suitable for the OpenFIGI API. Reply with only the ticker or phrase, one line, no explanation."},
			{"role": "user", "content": brokerDescription},
		},
		"max_tokens": 30,
		"temperature": 0,
	}
	body, err := json.Marshal(reqBody)
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		slurp, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("openai %d: %s", resp.StatusCode, string(slurp))
	}
	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	if len(out.Choices) == 0 {
		return "", fmt.Errorf("openai: no choices")
	}
	return strings.TrimSpace(out.Choices[0].Message.Content), nil
}

// UnderlyingFromDerivative asks the model to return only the underlying ticker symbol for an option or other derivative identifier.
// Used when the derivative parsing library cannot interpret the ticker. Returns (symbol, nil) or ("", err).
func (c *OpenAIClient) UnderlyingFromDerivative(ctx context.Context, derivativeTicker string) (string, error) {
	if c.apiKey == "" {
		return "", fmt.Errorf("openai api key required")
	}
	reqBody := map[string]interface{}{
		"model": c.model,
		"messages": []map[string]string{
			{"role": "system", "content": "You are a helper that extracts the underlying stock or index ticker from an option or derivative symbol. Reply with only the underlying ticker, one line, no explanation. Example: for 'AAPL  250117C00150000' reply 'AAPL'."},
			{"role": "user", "content": derivativeTicker},
		},
		"max_tokens": 20,
		"temperature": 0,
	}
	body, err := json.Marshal(reqBody)
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		slurp, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("openai %d: %s", resp.StatusCode, string(slurp))
	}
	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	if len(out.Choices) == 0 {
		return "", fmt.Errorf("openai: no choices")
	}
	return strings.TrimSpace(out.Choices[0].Message.Content), nil
}
