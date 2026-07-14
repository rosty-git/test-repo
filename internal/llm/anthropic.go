package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

const defaultModel = "claude-sonnet-4-5-20250929"

type AnthropicClient struct {
	apiKey     string
	model      string
	baseURL    string
	httpClient *http.Client
}

// NewAnthropicClient reads ANTHROPIC_API_KEY (required), and optionally
// ANTHROPIC_MODEL / ANTHROPIC_BASE_URL, from the environment.
func NewAnthropicClient() (*AnthropicClient, error) {
	key := os.Getenv("ANTHROPIC_API_KEY")
	if key == "" {
		return nil, fmt.Errorf("ANTHROPIC_API_KEY is not set")
	}
	model := os.Getenv("ANTHROPIC_MODEL")
	if model == "" {
		model = defaultModel
	}
	baseURL := os.Getenv("ANTHROPIC_BASE_URL")
	if baseURL == "" {
		baseURL = "https://api.anthropic.com"
	}
	return &AnthropicClient{
		apiKey:     key,
		model:      model,
		baseURL:    baseURL,
		httpClient: &http.Client{Timeout: 90 * time.Second},
	}, nil
}

type messagesRequest struct {
	Model       string     `json:"model"`
	MaxTokens   int        `json:"max_tokens"`
	Temperature float64    `json:"temperature"`
	System      string     `json:"system,omitempty"`
	Messages    []message  `json:"messages"`
	Tools       []tool     `json:"tools"`
	ToolChoice  toolChoice `json:"tool_choice"`
}

type message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type tool struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	InputSchema map[string]any `json:"input_schema"`
}

type toolChoice struct {
	Type string `json:"type"`
	Name string `json:"name"`
}

type messagesResponse struct {
	Content []contentBlock `json:"content"`
	Error   *apiError      `json:"error"`
}

type contentBlock struct {
	Type  string          `json:"type"`
	Text  string          `json:"text,omitempty"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`
}

type apiError struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

func (c *AnthropicClient) CallTool(ctx context.Context, req ToolRequest) (json.RawMessage, error) {
	maxTokens := req.MaxTokens
	if maxTokens == 0 {
		maxTokens = 1024
	}

	body := messagesRequest{
		Model:       c.model,
		MaxTokens:   maxTokens,
		Temperature: req.Temperature,
		System:      req.System,
		Messages:    []message{{Role: "user", Content: req.User}},
		Tools: []tool{{
			Name:        req.ToolName,
			Description: req.ToolDesc,
			InputSchema: req.ToolSchema,
		}},
		ToolChoice: toolChoice{Type: "tool", Name: req.ToolName},
	}

	var lastErr error
	for attempt := 0; attempt < 4; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(time.Duration(attempt) * 2 * time.Second):
			}
		}

		out, retriable, err := c.doCall(ctx, body)
		if err == nil {
			return out, nil
		}
		lastErr = err
		if !retriable {
			return nil, err
		}
	}
	return nil, fmt.Errorf("anthropic call failed after retries: %w", lastErr)
}

func (c *AnthropicClient) doCall(ctx context.Context, body messagesRequest) (json.RawMessage, bool, error) {
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, false, fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/messages", bytes.NewReader(payload))
	if err != nil {
		return nil, false, fmt.Errorf("build request: %w", err)
	}
	httpReq.Header.Set("content-type", "application/json")
	httpReq.Header.Set("x-api-key", c.apiKey)
	httpReq.Header.Set("anthropic-version", "2023-06-01")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, true, fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, true, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
		return nil, true, fmt.Errorf("anthropic API status %d: %s", resp.StatusCode, string(respBody))
	}
	if resp.StatusCode != http.StatusOK {
		return nil, false, fmt.Errorf("anthropic API status %d: %s", resp.StatusCode, string(respBody))
	}

	var parsed messagesResponse
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return nil, false, fmt.Errorf("parse response: %w", err)
	}
	if parsed.Error != nil {
		return nil, false, fmt.Errorf("anthropic API error: %s", parsed.Error.Message)
	}

	for _, block := range parsed.Content {
		if block.Type == "tool_use" {
			return block.Input, false, nil
		}
	}
	return nil, false, fmt.Errorf("no tool_use block in response")
}
