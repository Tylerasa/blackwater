package spec

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

// Client is the LLM abstraction the generator sits on top of. Tests inject
// a mock; production uses AnthropicClient. Keeping it a one-method interface
// means we could swap providers later without touching Generate.
type Client interface {
	Complete(ctx context.Context, systemPrompt, userMessage string) (string, error)
}

// DefaultModel is Haiku 4.5 — cheap, fast, and easily strong enough to write
// regex for a few dozen SMS templates. Overridable via LEDGER_MODEL.
const DefaultModel = "claude-haiku-4-5-20251001"

// AnthropicClient hits the Messages API directly with net/http. The system
// prompt is marked for ephemeral prompt caching so a batch of Generate calls
// within ~5 minutes only pays for it once.
type AnthropicClient struct {
	APIKey     string
	Model      string
	HTTPClient *http.Client
	Endpoint   string // override for tests
	MaxTokens  int
}

// NewAnthropicClient reads ANTHROPIC_API_KEY and LEDGER_MODEL from the env.
// Returns an error if the key is missing so callers can fail cleanly rather
// than hitting a 401 mid-batch.
func NewAnthropicClient() (*AnthropicClient, error) {
	key := os.Getenv("ANTHROPIC_API_KEY")
	if key == "" {
		return nil, errors.New("spec: ANTHROPIC_API_KEY not set")
	}
	model := os.Getenv("LEDGER_MODEL")
	if model == "" {
		model = DefaultModel
	}
	return &AnthropicClient{
		APIKey:     key,
		Model:      model,
		HTTPClient: &http.Client{Timeout: 60 * time.Second},
		Endpoint:   "https://api.anthropic.com/v1/messages",
		MaxTokens:  2048,
	}, nil
}

type systemBlock struct {
	Type         string            `json:"type"`
	Text         string            `json:"text"`
	CacheControl *cacheControlBody `json:"cache_control,omitempty"`
}

type cacheControlBody struct {
	Type string `json:"type"`
}

type messagesRequest struct {
	Model     string          `json:"model"`
	MaxTokens int             `json:"max_tokens"`
	System    []systemBlock   `json:"system,omitempty"`
	Messages  []messageInput  `json:"messages"`
}

type messageInput struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type messagesResponse struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	StopReason string `json:"stop_reason"`
	Model      string `json:"model"`
	Usage      struct {
		InputTokens              int `json:"input_tokens"`
		OutputTokens             int `json:"output_tokens"`
		CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
		CacheReadInputTokens     int `json:"cache_read_input_tokens"`
	} `json:"usage"`
	Error *struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// Complete sends one system+user turn and returns the assistant's text.
// The system block is marked ephemeral so repeated Generate calls in the
// same short window read from cache.
func (c *AnthropicClient) Complete(ctx context.Context, systemPrompt, userMessage string) (string, error) {
	reqBody := messagesRequest{
		Model:     c.Model,
		MaxTokens: c.MaxTokens,
		System: []systemBlock{{
			Type:         "text",
			Text:         systemPrompt,
			CacheControl: &cacheControlBody{Type: "ephemeral"},
		}},
		Messages: []messageInput{{Role: "user", Content: userMessage}},
	}
	buf, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("spec: marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.Endpoint, bytes.NewReader(buf))
	if err != nil {
		return "", fmt.Errorf("spec: build request: %w", err)
	}
	httpReq.Header.Set("x-api-key", c.APIKey)
	httpReq.Header.Set("anthropic-version", "2023-06-01")
	httpReq.Header.Set("content-type", "application/json")

	httpResp, err := c.HTTPClient.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("spec: http: %w", err)
	}
	defer httpResp.Body.Close()

	respBytes, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return "", fmt.Errorf("spec: read response: %w", err)
	}

	var resp messagesResponse
	if err := json.Unmarshal(respBytes, &resp); err != nil {
		return "", fmt.Errorf("spec: decode response (status %d): %w: %s",
			httpResp.StatusCode, err, truncate(string(respBytes), 200))
	}
	if resp.Error != nil {
		return "", fmt.Errorf("spec: api error (%s): %s", resp.Error.Type, resp.Error.Message)
	}
	if httpResp.StatusCode >= 400 {
		return "", fmt.Errorf("spec: http status %d: %s", httpResp.StatusCode, truncate(string(respBytes), 200))
	}
	if len(resp.Content) == 0 {
		return "", errors.New("spec: empty response content")
	}
	// Concatenate all text blocks (Haiku typically returns one).
	var out bytes.Buffer
	for _, c := range resp.Content {
		if c.Type == "text" {
			out.WriteString(c.Text)
		}
	}
	return out.String(), nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
