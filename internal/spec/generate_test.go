package spec

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// mockClient records every call and returns queued responses. Lets tests
// drive the retry loop without touching the network.
type mockClient struct {
	responses []string
	errors    []error
	calls     []struct{ system, user string }
	idx       int
}

func (m *mockClient) Complete(_ context.Context, system, user string) (string, error) {
	m.calls = append(m.calls, struct{ system, user string }{system, user})
	i := m.idx
	m.idx++
	if i >= len(m.responses) {
		return "", errors.New("mock: no more responses queued")
	}
	if i < len(m.errors) && m.errors[i] != nil {
		return "", m.errors[i]
	}
	return m.responses[i], nil
}

// A realistic Spec JSON that will actually match the fixture sample.
const goodSpecJSON = `{
  "pattern": "^Payment for GHS\\s*(?P<amount>[0-9.]+) to MTN\\s+\\.\\.Current Balance: GHS\\s*(?P<balance>[0-9.]+)\\. Transaction Id: (?P<reference>\\d+)\\.",
  "fields": {
    "amount": {"kind": "amount"},
    "balance": {"kind": "balance"},
    "reference": {"kind": "reference"}
  },
  "direction": "debit"
}`

const sampleBody = "Payment for GHS3.00 to MTN  ..Current Balance: GHS 13.52. Transaction Id: 50000000001. Fee charged: GHS0.00,Tax Charged 0."

func TestGenerateSuccessFirstTry(t *testing.T) {
	m := &mockClient{responses: []string{goodSpecJSON}}
	s, err := Generate(context.Background(), GenerateOptions{
		Fingerprint: "abc123",
		Skeleton:    "mobilemoney: payment for <amt> to mtn ..<name>: <amt>. <name>: <ref>.",
		Sample:      sampleBody,
		Sender:      "MobileMoney",
		Client:      m,
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}
	if s.Fingerprint != "abc123" || s.Sender != "MobileMoney" {
		t.Errorf("Generate did not attach fingerprint/sender: %+v", s)
	}
	if s.Direction != DirectionDebit {
		t.Errorf("expected debit direction, got %q", s.Direction)
	}
	if s.Version != 1 {
		t.Errorf("expected version 1 default, got %d", s.Version)
	}
	if len(m.calls) != 1 {
		t.Errorf("expected 1 call, got %d", len(m.calls))
	}
	if !strings.Contains(m.calls[0].user, sampleBody) {
		t.Error("user message should contain the sample body")
	}
}

func TestGenerateRetriesOnBadRegex(t *testing.T) {
	// First response has a broken regex, second is good. Generate should
	// call twice, feed the compile error back on retry, and succeed.
	badPattern := `{"pattern": "^(?P<amount>[0-9", "fields": {"amount": {"kind":"amount"}}, "direction": "debit"}`
	m := &mockClient{responses: []string{badPattern, goodSpecJSON}}

	s, err := Generate(context.Background(), GenerateOptions{
		Fingerprint: "abc",
		Sample:      sampleBody,
		Sender:      "MobileMoney",
		Client:      m,
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}
	if len(m.calls) != 2 {
		t.Fatalf("expected 2 calls (retry), got %d", len(m.calls))
	}
	if !strings.Contains(m.calls[1].user, "previous attempt failed") {
		t.Error("retry user message should reference the previous failure")
	}
	if !strings.Contains(m.calls[1].user, "(?P<amount>[0-9") {
		t.Error("retry should include the previous pattern for context")
	}
	if s.Pattern == "" {
		t.Error("expected non-empty pattern on success")
	}
}

func TestGenerateRetriesOnMissingMatch(t *testing.T) {
	// Well-formed JSON, compiles, but pattern doesn't match sample.
	nonMatching := `{"pattern": "^completely wrong (?P<amount>\\d+)$", "fields": {"amount": {"kind":"amount"}}, "direction":"debit"}`
	m := &mockClient{responses: []string{nonMatching, goodSpecJSON}}

	_, err := Generate(context.Background(), GenerateOptions{
		Sample: sampleBody,
		Client: m,
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}
	if len(m.calls) != 2 {
		t.Fatalf("expected retry, got %d calls", len(m.calls))
	}
}

func TestGenerateGivesUp(t *testing.T) {
	// Three bad responses in a row → error after MaxAttempts=3.
	bad := `{"pattern": "does not match", "fields": {}, "direction": "debit"}`
	m := &mockClient{responses: []string{bad, bad, bad}}

	_, err := Generate(context.Background(), GenerateOptions{
		Sample:      sampleBody,
		MaxAttempts: 3,
		Client:      m,
	})
	if err == nil {
		t.Fatal("expected error after all attempts fail")
	}
	if !strings.Contains(err.Error(), "gave up after 3") {
		t.Errorf("error should mention attempt count: %v", err)
	}
	if len(m.calls) != 3 {
		t.Errorf("expected 3 calls, got %d", len(m.calls))
	}
}

func TestGenerateHandlesMarkdownFences(t *testing.T) {
	wrapped := "```json\n" + goodSpecJSON + "\n```"
	m := &mockClient{responses: []string{wrapped}}
	_, err := Generate(context.Background(), GenerateOptions{
		Sample: sampleBody,
		Client: m,
	})
	if err != nil {
		t.Fatalf("expected fence-stripping to work: %v", err)
	}
}

func TestGenerateTransportErrorNoRetry(t *testing.T) {
	// A client error (e.g. network) should NOT retry — it's not something
	// the LLM can fix by trying again.
	m := &mockClient{
		responses: []string{""},
		errors:    []error{errors.New("network down")},
	}
	_, err := Generate(context.Background(), GenerateOptions{
		Sample: sampleBody,
		Client: m,
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if len(m.calls) != 1 {
		t.Errorf("transport error should fail fast, got %d calls", len(m.calls))
	}
}

func TestParseSpecJSONExtractsFromProse(t *testing.T) {
	// Model prepends chatty prose despite instructions.
	messy := "Here you go!\n\n" + goodSpecJSON + "\n\nHope that helps!"
	s, err := parseSpecJSON(messy)
	if err != nil {
		t.Fatalf("should extract JSON from prose: %v", err)
	}
	if s.Direction != "debit" {
		t.Errorf("wrong direction extracted: %q", s.Direction)
	}
}

// ---- AnthropicClient (HTTP) ----

func TestAnthropicClientHappyPath(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-api-key") != "test-key" {
			t.Errorf("missing api key header")
		}
		if r.Header.Get("anthropic-version") == "" {
			t.Error("missing version header")
		}
		body, _ := io.ReadAll(r.Body)
		var req messagesRequest
		if err := json.Unmarshal(body, &req); err != nil {
			t.Fatalf("bad request json: %v", err)
		}
		if req.Model != "claude-haiku-4-5-20251001" {
			t.Errorf("wrong model: %q", req.Model)
		}
		if len(req.System) != 1 || req.System[0].CacheControl == nil {
			t.Error("expected system block with cache_control")
		}
		if req.System[0].CacheControl.Type != "ephemeral" {
			t.Errorf("expected ephemeral cache, got %q", req.System[0].CacheControl.Type)
		}
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(`{"content":[{"type":"text","text":"hello back"}],"stop_reason":"end_turn"}`))
	}))
	defer server.Close()

	c := &AnthropicClient{
		APIKey:     "test-key",
		Model:      "claude-haiku-4-5-20251001",
		HTTPClient: server.Client(),
		Endpoint:   server.URL,
		MaxTokens:  100,
	}
	got, err := c.Complete(context.Background(), "sys", "user")
	if err != nil {
		t.Fatal(err)
	}
	if got != "hello back" {
		t.Errorf("unexpected reply: %q", got)
	}
}

func TestAnthropicClientAPIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"type":"authentication_error","message":"invalid x-api-key"}}`))
	}))
	defer server.Close()

	c := &AnthropicClient{
		APIKey:     "bad",
		HTTPClient: server.Client(),
		Endpoint:   server.URL,
		MaxTokens:  100,
	}
	_, err := c.Complete(context.Background(), "sys", "user")
	if err == nil {
		t.Fatal("expected auth error")
	}
	if !strings.Contains(err.Error(), "authentication_error") {
		t.Errorf("error should surface API error type: %v", err)
	}
}
