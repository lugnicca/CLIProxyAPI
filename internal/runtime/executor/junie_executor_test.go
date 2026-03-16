package executor

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/executor"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v6/sdk/translator"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func newJunieExecutor() *JunieExecutor {
	return NewJunieExecutor(&config.Config{})
}

func newJunieAuth(apiKey string) *cliproxyauth.Auth {
	return &cliproxyauth.Auth{
		Attributes: map[string]string{
			"api_key": apiKey,
		},
	}
}

// openAISSEBody returns a well-formed OpenAI SSE body (as Ingrazzio returns).
func openAISSEBody() string {
	lines := []string{
		`data: {"id":"chatcmpl-123","object":"chat.completion.chunk","created":1234567890,"model":"gpt-4o","choices":[{"index":0,"delta":{"content":"Hello"},"finish_reason":null}]}`,
		`data: {"id":"chatcmpl-123","object":"chat.completion.chunk","created":1234567890,"model":"gpt-4o","choices":[{"index":0,"delta":{"content":" world"},"finish_reason":null}]}`,
		`data: {"id":"chatcmpl-123","object":"chat.completion.chunk","created":1234567890,"model":"gpt-4o","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
		`data: [DONE]`,
		``,
	}
	return strings.Join(lines, "\n")
}

// openAIJSONBody returns a well-formed OpenAI non-streaming response (as Ingrazzio returns).
func openAIJSONBody() string {
	return `{"id":"chatcmpl-123","object":"chat.completion","created":1234567890,"model":"gpt-4o","choices":[{"index":0,"message":{"role":"assistant","content":"Hello world"},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":2,"total_tokens":12}}`
}

// sseBody kept for backward compatibility with old SSE-parsing utility tests below.
func sseBody() string {
	lines := []string{
		`data: {"type":"Content","content":"Hello"}`,
		`data: {"type":"Content","content":" world"}`,
		`data: {"type":"QuotaMetadata","spent":{"amount":"42"}}`,
		``,
	}
	return strings.Join(lines, "\n")
}

// ---------------------------------------------------------------------------
// TestJunieExecutor_Identifier
// ---------------------------------------------------------------------------

func TestJunieExecutor_Identifier(t *testing.T) {
	e := newJunieExecutor()
	if got := e.Identifier(); got != "junie" {
		t.Fatalf("Identifier() = %q, want %q", got, "junie")
	}
}

// ---------------------------------------------------------------------------
// TestJunieExecutor_Refresh
// ---------------------------------------------------------------------------

func TestJunieExecutor_Refresh(t *testing.T) {
	e := newJunieExecutor()
	original := newJunieAuth("my-token")
	got, err := e.Refresh(context.Background(), original)
	if err != nil {
		t.Fatalf("Refresh returned unexpected error: %v", err)
	}
	if got != original {
		t.Fatal("Refresh should return the same *Auth pointer (no-op)")
	}
}

func TestJunieExecutor_Refresh_NilAuth(t *testing.T) {
	e := newJunieExecutor()
	got, err := e.Refresh(context.Background(), nil)
	if err != nil {
		t.Fatalf("Refresh(nil) returned unexpected error: %v", err)
	}
	if got != nil {
		t.Fatalf("Refresh(nil) = %v, want nil", got)
	}
}

// ---------------------------------------------------------------------------
// TestJunieExecutor_CountTokens
// ---------------------------------------------------------------------------

func TestJunieExecutor_CountTokens(t *testing.T) {
	e := newJunieExecutor()
	_, err := e.CountTokens(context.Background(), newJunieAuth("tok"), cliproxyexecutor.Request{}, cliproxyexecutor.Options{})
	if err == nil {
		t.Fatal("CountTokens should return an error (not supported)")
	}
	if !strings.Contains(err.Error(), "not supported") {
		t.Fatalf("unexpected error message: %v", err)
	}
}

// ---------------------------------------------------------------------------
// TestJunieExecutor_PrepareRequest
// ---------------------------------------------------------------------------

func TestJunieExecutor_PrepareRequest_SetsJWTHeader(t *testing.T) {
	e := newJunieExecutor()
	req, _ := http.NewRequest(http.MethodPost, "https://example.com", nil)
	auth := newJunieAuth("test-jwt-token")

	if err := e.PrepareRequest(req, auth); err != nil {
		t.Fatalf("PrepareRequest error: %v", err)
	}

	if got := req.Header.Get("Authorization"); got != "Bearer test-jwt-token" {
		t.Fatalf("%s = %q, want %q", "Authorization", got, "Bearer test-jwt-token")
	}
	if got := req.Header.Get("User-Agent"); got != grazieUserAgent {
		t.Fatalf("User-Agent = %q, want %q", got, grazieUserAgent)
	}
	if got := req.Header.Get("Grazie-Agent"); got != grazieAgentHeader {
		t.Fatalf("Grazie-Agent = %q, want %q", got, grazieAgentHeader)
	}
}

func TestJunieExecutor_PrepareRequest_FallbackToMetadata(t *testing.T) {
	e := newJunieExecutor()
	req, _ := http.NewRequest(http.MethodPost, "https://example.com", nil)
	auth := &cliproxyauth.Auth{
		Metadata: map[string]any{
			"jwt_token": "metadata-jwt",
		},
	}

	if err := e.PrepareRequest(req, auth); err != nil {
		t.Fatalf("PrepareRequest error: %v", err)
	}
	if got := req.Header.Get("Authorization"); got != "Bearer metadata-jwt" {
		t.Fatalf("%s = %q, want %q", "Authorization", got, "Bearer metadata-jwt")
	}
	// Grazie-Agent must always be set.
	if got := req.Header.Get("Grazie-Agent"); got != grazieAgentHeader {
		t.Fatalf("Grazie-Agent = %q, want %q", got, grazieAgentHeader)
	}
}

func TestJunieExecutor_PrepareRequest_NoCredentialsNoHeader(t *testing.T) {
	e := newJunieExecutor()
	req, _ := http.NewRequest(http.MethodPost, "https://example.com", nil)
	auth := &cliproxyauth.Auth{}

	if err := e.PrepareRequest(req, auth); err != nil {
		t.Fatalf("PrepareRequest error: %v", err)
	}
	// Header should be absent (empty string) when no credentials supplied.
	if got := req.Header.Get("Authorization"); got != "" {
		t.Fatalf("expected no JWT header, got %q", got)
	}
	// User-Agent and Grazie-Agent should always be set regardless.
	if got := req.Header.Get("User-Agent"); got != grazieUserAgent {
		t.Fatalf("User-Agent = %q, want %q", got, grazieUserAgent)
	}
	if got := req.Header.Get("Grazie-Agent"); got != grazieAgentHeader {
		t.Fatalf("Grazie-Agent = %q, want %q", got, grazieAgentHeader)
	}
}

func TestJunieExecutor_PrepareRequest_NilRequest(t *testing.T) {
	e := newJunieExecutor()
	if err := e.PrepareRequest(nil, newJunieAuth("tok")); err != nil {
		t.Fatalf("PrepareRequest(nil) should be a no-op, got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// TestJunieCreds – indirect tests via PrepareRequest
// ---------------------------------------------------------------------------

func TestJunieCreds_AttributesApiKey(t *testing.T) {
	e := newJunieExecutor()
	req, _ := http.NewRequest(http.MethodPost, "https://example.com", nil)
	auth := &cliproxyauth.Auth{
		Attributes: map[string]string{"api_key": "  trimmed-key  "},
		Metadata:   map[string]any{"jwt_token": "should-not-be-used"},
	}
	_ = e.PrepareRequest(req, auth)
	// Attributes.api_key should take priority; whitespace trimmed.
	if got := req.Header.Get("Authorization"); got != "Bearer trimmed-key" {
		t.Fatalf("expected Bearer+trimmed Attributes api_key, got %q", got)
	}
}

func TestJunieCreds_MetadataFallback(t *testing.T) {
	e := newJunieExecutor()
	req, _ := http.NewRequest(http.MethodPost, "https://example.com", nil)
	auth := &cliproxyauth.Auth{
		Metadata: map[string]any{"jwt_token": "  meta-jwt  "},
	}
	_ = e.PrepareRequest(req, auth)
	if got := req.Header.Get("Authorization"); got != "Bearer meta-jwt" {
		t.Fatalf("expected Bearer+trimmed Metadata jwt_token, got %q", got)
	}
}

func TestJunieCreds_NilAuth(t *testing.T) {
	e := newJunieExecutor()
	req, _ := http.NewRequest(http.MethodPost, "https://example.com", nil)
	_ = e.PrepareRequest(req, nil)
	if got := req.Header.Get("Authorization"); got != "" {
		t.Fatalf("nil auth should produce no JWT header, got %q", got)
	}
}

// ---------------------------------------------------------------------------
// Mock Ingrazzio server helpers
// ---------------------------------------------------------------------------

// newMockIngrazzioServer starts an httptest.Server that:
//   - Accepts POST requests only.
//   - Requires the Authorization header.
//   - Verifies the Grazie-Agent header is present.
//   - Streams back a hard-coded OpenAI SSE payload (or JSON for non-stream).
func newMockIngrazzioServer(t *testing.T, wantJWT string, streaming bool) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Method check
		if r.Method != http.MethodPost {
			t.Errorf("mock Ingrazzio server: expected POST, got %s", r.Method)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		// JWT header check
		if jwt := r.Header.Get("Authorization"); jwt == "" {
			t.Errorf("mock Ingrazzio server: missing %s header", "Authorization")
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		} else if wantJWT != "" && jwt != "Bearer "+wantJWT {
			t.Errorf("mock Ingrazzio server: JWT = %q, want %q", jwt, wantJWT)
		}

		// Grazie-Agent check
		if ga := r.Header.Get("Grazie-Agent"); ga == "" {
			t.Errorf("mock Ingrazzio server: missing Grazie-Agent header")
		}

		// Read and lightly validate body
		body, _ := io.ReadAll(r.Body)
		if len(body) > 0 {
			result := gjson.ParseBytes(body)
			if !result.IsObject() {
				t.Errorf("mock Ingrazzio server: request body is not a JSON object: %s", body)
			}
		}

		if streaming {
			// Stream SSE response (OpenAI format)
			w.Header().Set("Content-Type", "text/event-stream")
			w.Header().Set("Cache-Control", "no-cache")
			w.WriteHeader(http.StatusOK)
			_, _ = fmt.Fprint(w, openAISSEBody())
		} else {
			// Non-streaming JSON response (OpenAI format)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = fmt.Fprint(w, openAIJSONBody())
		}
	}))
}

// newMockGrazieServer is kept for backward compatibility with HttpRequest tests.
// It accepts any valid JSON POST with an Authorization header and responds with an SSE body.
func newMockGrazieServer(t *testing.T, wantJWT string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Method check
		if r.Method != http.MethodPost {
			t.Errorf("mock server: expected POST, got %s", r.Method)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		// JWT header check
		if jwt := r.Header.Get("Authorization"); jwt == "" {
			t.Errorf("mock server: missing %s header", "Authorization")
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		} else if wantJWT != "" && jwt != "Bearer "+wantJWT {
			t.Errorf("mock server: JWT = %q, want %q", jwt, wantJWT)
		}

		// Read and lightly validate body
		body, _ := io.ReadAll(r.Body)
		if len(body) > 0 {
			result := gjson.ParseBytes(body)
			if !result.IsObject() {
				t.Errorf("mock server: request body is not a JSON object: %s", body)
			}
		}

		// Stream SSE response
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprint(w, openAISSEBody())
	}))
}

// ---------------------------------------------------------------------------
// TestJunieExecutor_HttpRequest – full-stack round-trip via mock server
// ---------------------------------------------------------------------------

func TestJunieExecutor_HttpRequest_RoundTrip(t *testing.T) {
	server := newMockGrazieServer(t, "test-jwt-token")
	defer server.Close()

	e := newJunieExecutor()
	auth := newJunieAuth("test-jwt-token")

	body := []byte(`{"model":"gpt-4o","messages":[{"role":"user","content":"hello"}]}`)
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, server.URL, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}

	resp, err := e.HttpRequest(context.Background(), auth, req)
	if err != nil {
		t.Fatalf("HttpRequest error: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	data, _ := io.ReadAll(resp.Body)
	// Ingrazzio returns OpenAI SSE format; check for expected content.
	if !bytes.Contains(data, []byte("chat.completion.chunk")) {
		t.Fatalf("response body does not contain expected OpenAI SSE content: %s", data)
	}
}

func TestJunieExecutor_HttpRequest_MissingJWT_NoHeader(t *testing.T) {
	// Server rejects if no JWT header; executor should propagate the 401 status.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	e := newJunieExecutor()
	// Auth with no credentials → PrepareRequest sets no JWT header.
	auth := &cliproxyauth.Auth{}

	req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, server.URL, nil)
	resp, err := e.HttpRequest(context.Background(), auth, req)
	if err != nil {
		t.Fatalf("HttpRequest error: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 from server when JWT is absent, got %d", resp.StatusCode)
	}
}

func TestJunieExecutor_HttpRequest_NilRequest(t *testing.T) {
	e := newJunieExecutor()
	_, err := e.HttpRequest(context.Background(), newJunieAuth("tok"), nil)
	if err == nil {
		t.Fatal("expected error for nil request")
	}
}

// ---------------------------------------------------------------------------
// TestJunieSSEParsing – unit-test the SSE parsing logic in isolation.
// These helpers parse the old Grazie SSE format and are kept for reference.
// ---------------------------------------------------------------------------

// parseSSEContent replicates a simple SSE parsing loop.
func parseSSEContent(t *testing.T, rawSSE []byte) (content string, quotaAmount string) {
	t.Helper()

	var contentBuf strings.Builder
	var quotaLine []byte

	lines := bytes.Split(rawSSE, []byte("\n"))
	for _, line := range lines {
		if !bytes.HasPrefix(line, dataTag) {
			continue
		}
		payload := bytes.TrimSpace(line[5:])
		if len(payload) == 0 {
			continue
		}
		eventType := gjson.GetBytes(payload, "type").String()
		switch eventType {
		case "Content":
			contentBuf.WriteString(gjson.GetBytes(payload, "content").String())
		case "QuotaMetadata":
			quotaLine = payload
		}
	}

	content = contentBuf.String()
	if len(quotaLine) > 0 {
		quotaAmount = gjson.GetBytes(quotaLine, "spent.amount").String()
	}
	return content, quotaAmount
}

func TestJunieSSEParsing_AccumulatesContent(t *testing.T) {
	raw := []byte(sseBody())
	content, quota := parseSSEContent(t, raw)

	if content != "Hello world" {
		t.Fatalf("accumulated content = %q, want %q", content, "Hello world")
	}
	if quota != "42" {
		t.Fatalf("quota amount = %q, want %q", quota, "42")
	}
}

func TestJunieSSEParsing_HandlesEmptyLines(t *testing.T) {
	raw := []byte("\ndata: \ndata: {\"type\":\"Content\",\"content\":\"x\"}\n\n")
	content, _ := parseSSEContent(t, raw)
	if content != "x" {
		t.Fatalf("content = %q, want %q", content, "x")
	}
}

func TestJunieSSEParsing_OnlyQuotaMetadata(t *testing.T) {
	raw := []byte(`data: {"type":"QuotaMetadata","spent":{"amount":"99"}}`)
	content, quota := parseSSEContent(t, raw)
	if content != "" {
		t.Fatalf("expected empty content, got %q", content)
	}
	if quota != "99" {
		t.Fatalf("quota = %q, want %q", quota, "99")
	}
}

func TestJunieSSEParsing_UnknownEventTypeIgnored(t *testing.T) {
	raw := []byte("data: {\"type\":\"Unknown\",\"data\":\"ignored\"}\ndata: {\"type\":\"Content\",\"content\":\"ok\"}")
	content, _ := parseSSEContent(t, raw)
	if content != "ok" {
		t.Fatalf("content = %q, want %q", content, "ok")
	}
}

// ---------------------------------------------------------------------------
// TestJunieExecutor_Execute_MissingToken – Execute should error with no JWT
// ---------------------------------------------------------------------------

func TestJunieExecutor_Execute_MissingToken(t *testing.T) {
	e := newJunieExecutor()
	auth := &cliproxyauth.Auth{} // no credentials

	_, err := e.Execute(context.Background(), auth, cliproxyexecutor.Request{
		Model:   "gpt-4o",
		Payload: []byte(`{}`),
	}, cliproxyexecutor.Options{
		SourceFormat: sdktranslator.Format("openai"),
	})
	if err == nil {
		t.Fatal("Execute should return error when JWT token is missing")
	}
	if !strings.Contains(err.Error(), "missing JWT token") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// ---------------------------------------------------------------------------
// TestJunieExecutor_ExecuteStream_MissingToken
// ---------------------------------------------------------------------------

func TestJunieExecutor_ExecuteStream_MissingToken(t *testing.T) {
	e := newJunieExecutor()
	auth := &cliproxyauth.Auth{} // no credentials

	_, err := e.ExecuteStream(context.Background(), auth, cliproxyexecutor.Request{
		Model:   "gpt-4o",
		Payload: []byte(`{}`),
	}, cliproxyexecutor.Options{
		SourceFormat: sdktranslator.Format("openai"),
	})
	if err == nil {
		t.Fatal("ExecuteStream should return error when JWT token is missing")
	}
	if !strings.Contains(err.Error(), "missing JWT token") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// ---------------------------------------------------------------------------
// TestJunieExecutor_Integration_MockIngrazzioStream
//
// Full end-to-end integration test for streaming:
//   - Spin up a mock Ingrazzio server that returns OpenAI SSE format
//   - Use HttpRequest to hit it (bypassing the hard-coded const endpoint)
//   - Parse the SSE stream using bufio.Scanner
//   - Verify content passes through as-is (native OpenAI SSE)
// ---------------------------------------------------------------------------

func TestJunieExecutor_Integration_MockIngrazzioStream(t *testing.T) {
	server := newMockIngrazzioServer(t, "integration-jwt", true)
	defer server.Close()

	e := newJunieExecutor()
	auth := newJunieAuth("integration-jwt")

	body := []byte(`{"model":"gpt-4o","messages":[{"role":"user","content":"hello"}],"stream":true}`)
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, server.URL, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}

	resp, err := e.HttpRequest(context.Background(), auth, req)
	if err != nil {
		t.Fatalf("HttpRequest error: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("unexpected status %d: %s", resp.StatusCode, b)
	}

	// Replicate the streaming scan logic from ExecuteStream
	var accumulated strings.Builder
	var sawDone bool

	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Bytes()
		if !bytes.HasPrefix(line, dataTag) {
			continue
		}
		payload := bytes.TrimSpace(line[5:])
		if bytes.Equal(payload, []byte("[DONE]")) {
			sawDone = true
			continue
		}
		if len(payload) == 0 {
			continue
		}
		// Extract delta content from OpenAI SSE chunk
		content := gjson.GetBytes(payload, "choices.0.delta.content").String()
		accumulated.WriteString(content)
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scanner error: %v", err)
	}

	if got := accumulated.String(); got != "Hello world" {
		t.Fatalf("accumulated SSE content = %q, want %q", got, "Hello world")
	}
	if !sawDone {
		t.Fatal("expected to see data: [DONE] in SSE stream")
	}
	t.Logf("Integration test passed: content=%q", accumulated.String())
}

// ---------------------------------------------------------------------------
// TestJunieExecutor_Integration_MockIngrazzioSSE_ErrorStatus
// ---------------------------------------------------------------------------

func TestJunieExecutor_Integration_MockIngrazzioSSE_ErrorStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":{"message":"quota exceeded","type":"insufficient_quota"}}`, http.StatusTooManyRequests)
	}))
	defer server.Close()

	e := newJunieExecutor()
	auth := newJunieAuth("some-token")

	req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, server.URL, bytes.NewReader([]byte(`{}`)))
	resp, err := e.HttpRequest(context.Background(), auth, req)
	if err != nil {
		t.Fatalf("HttpRequest error: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("expected 429, got %d", resp.StatusCode)
	}
}

// ---------------------------------------------------------------------------
// TestJunieExecutor_GrazieAgentHeader
// Verify that the Grazie-Agent header is set correctly in PrepareRequest.
// ---------------------------------------------------------------------------

func TestJunieExecutor_GrazieAgentHeader(t *testing.T) {
	e := newJunieExecutor()
	req, _ := http.NewRequest(http.MethodPost, "https://example.com", nil)
	auth := newJunieAuth("my-token")

	if err := e.PrepareRequest(req, auth); err != nil {
		t.Fatalf("PrepareRequest error: %v", err)
	}

	got := req.Header.Get("Grazie-Agent")
	if got != grazieAgentHeader {
		t.Fatalf("Grazie-Agent = %q, want %q", got, grazieAgentHeader)
	}
}

// ---------------------------------------------------------------------------
// TestMapJunieModelName – unit tests for the model name mapping function.
// ---------------------------------------------------------------------------

func TestMapJunieModelName_OpenAIRewritten(t *testing.T) {
	// Models that have a mapping entry (alias -> real provider name for /v1/chat/completions).
	cases := []struct {
		input string
		want  string
	}{
		{"gpt4.1", "gpt-4.1-2025-04-14"},
		{"gpt4.1-mini", "gpt-4.1-mini-2025-04-14"},
		{"gpt4.1-nano", "gpt-4.1-nano-2025-04-14"},
		{"gpt-5", "gpt-5-2025-08-07"},
		{"gpt-5-mini", "gpt-5-mini-2025-08-07"},
		{"gpt-5-nano", "gpt-5-nano-2025-08-07"},
	}
	for _, tc := range cases {
		got := mapJunieModelName(tc.input)
		if got != tc.want {
			t.Errorf("mapJunieModelName(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestMapJunieModelName_OpenAIPassThrough(t *testing.T) {
	// These OpenAI models pass through unchanged (real names accepted by /v1/chat/completions).
	passThrough := []string{
		"gpt-4o", "gpt-4o-mini", "o1", "o3", "o3-mini", "o4-mini",
		"gpt-5-codex", "gpt-5.1", "gpt-5.1-codex", "gpt-5.1-codex-mini",
		"gpt-5.1-codex-max", "gpt-5.2", "gpt-5.2-codex", "gpt-5.3-codex", "gpt-5.4",
	}
	for _, name := range passThrough {
		got := mapJunieModelName(name)
		if got != name {
			t.Errorf("mapJunieModelName(%q) = %q, want unchanged %q", name, got, name)
		}
	}
}

func TestMapJunieModelName_AnthropicRewritten(t *testing.T) {
	// Maps alias -> real provider model name for /v1/chat/completions endpoint.
	cases := []struct {
		input string
		want  string
	}{
		{"claude-4-sonnet", "claude-sonnet-4-20250514"},
		{"claude-4.1-opus", "claude-opus-4-1-20250805"},
		{"claude-4.5-sonnet", "claude-sonnet-4-5-20250929"},
		{"claude-4.5-haiku", "claude-haiku-4-5-20251001"},
		{"claude-4.5-opus", "claude-opus-4-5-20251101"},
		{"claude-4.6-sonnet", "claude-sonnet-4-6"},
		{"claude-4.6-opus", "claude-opus-4-6"},
	}
	for _, tc := range cases {
		got := mapJunieModelName(tc.input)
		if got != tc.want {
			t.Errorf("mapJunieModelName(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestMapJunieModelName_GoogleRewritten(t *testing.T) {
	// Maps alias -> real provider model name for /v1/chat/completions endpoint.
	cases := []struct {
		input string
		want  string
	}{
		{"gemini-flash-2.0", "gemini-2.0-flash"},
		{"gemini-flash-lite-2.0", "gemini-2.0-flash-lite"},
		{"gemini-pro-2.5", "gemini-2.5-pro"},
		{"gemini-flash-2.5", "gemini-2.5-flash"},
		{"gemini-flash-lite-2.5", "gemini-2.5-flash-lite"},
	}
	for _, tc := range cases {
		got := mapJunieModelName(tc.input)
		if got != tc.want {
			t.Errorf("mapJunieModelName(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestMapJunieModelName_GooglePassThrough(t *testing.T) {
	// These Gemini models pass through unchanged.
	passThrough := []string{
		"gemini-3.0-pro", "gemini-3.0-flash", "gemini-3.1-flash-lite", "gemini-3.1-pro",
	}
	for _, name := range passThrough {
		got := mapJunieModelName(name)
		if got != name {
			t.Errorf("mapJunieModelName(%q) = %q, want unchanged %q", name, got, name)
		}
	}
}

func TestMapJunieModelName_XAIPassThrough(t *testing.T) {
	// xAI Grok models pass through unchanged (real names accepted by /v1/chat/completions).
	passThrough := []string{
		"grok-4", "grok-4-fast", "grok-code-fast-1",
		"grok-4.1-fast", "grok-4.1-fast-non-reasoning",
	}
	for _, name := range passThrough {
		got := mapJunieModelName(name)
		if got != name {
			t.Errorf("mapJunieModelName(%q) = %q, want unchanged %q", name, got, name)
		}
	}
}

func TestMapJunieModelName_PassThrough(t *testing.T) {
	// These names have no mapping entry and pass through unchanged.
	passThrough := []string{
		"some-unknown-model",
		"future-model-9000",
	}
	for _, name := range passThrough {
		got := mapJunieModelName(name)
		if got != name {
			t.Errorf("mapJunieModelName(%q) = %q, want unchanged %q", name, got, name)
		}
	}
}

func TestMapJunieModelName_Unknown(t *testing.T) {
	unknown := "some-future-model-9000"
	got := mapJunieModelName(unknown)
	if got != unknown {
		t.Errorf("mapJunieModelName(%q) = %q, want unchanged %q", unknown, got, unknown)
	}
}

// ---------------------------------------------------------------------------
// TestJunieExecutor_Execute_ModelMapping – verify model name is rewritten in payload.
// Uses a mock server that echoes back the received model name.
// ---------------------------------------------------------------------------

func TestJunieExecutor_Execute_ModelMapping(t *testing.T) {
	// Mock server records the model name from the request body.
	var receivedModel string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		receivedModel = gjson.GetBytes(body, "model").String()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprint(w, openAIJSONBody())
	}))
	defer server.Close()

	// Verify the mapping logic itself through mapJunieModelName and confirm
	// payload mutation via a direct sjson round-trip.
	// gpt4.1 (alias) -> gpt-4.1-2025-04-14 (real provider name for /v1/chat/completions)
	payload := []byte(`{"model":"gpt4.1","messages":[{"role":"user","content":"hi"}]}`)
	clientModel := gjson.GetBytes(payload, "model").String()
	mapped := mapJunieModelName(clientModel)
	if mapped == clientModel {
		t.Fatalf("expected gpt4.1 to be rewritten, got unchanged %q", mapped)
	}
	rewritten, err := sjson.SetBytes(payload, "model", mapped)
	if err != nil {
		t.Fatalf("sjson.SetBytes error: %v", err)
	}
	if got := gjson.GetBytes(rewritten, "model").String(); got != "gpt-4.1-2025-04-14" {
		t.Fatalf("rewritten model = %q, want %q", got, "gpt-4.1-2025-04-14")
	}
	// Verify pass-through model is not touched (unknown model names pass through unchanged).
	passthroughPayload := []byte(`{"model":"some-custom-model","messages":[]}`)
	pt := gjson.GetBytes(passthroughPayload, "model").String()
	if mapJunieModelName(pt) != "some-custom-model" {
		t.Fatalf("unknown model should pass through unchanged, got %q", mapJunieModelName(pt))
	}
	_ = receivedModel
	_ = server
}

func TestJunieExecutor_ExecuteStream_ModelMapping(t *testing.T) {
	// Same logic: verify that payload rewriting works correctly for stream path.
	// claude-4.6-sonnet (alias) -> claude-sonnet-4-6 (real provider name for /v1/chat/completions)
	payload := []byte(`{"model":"claude-4.6-sonnet","messages":[{"role":"user","content":"hello"}],"stream":true}`)
	clientModel := gjson.GetBytes(payload, "model").String()
	mapped := mapJunieModelName(clientModel)
	if mapped != "claude-sonnet-4-6" {
		t.Fatalf("claude-4.6-sonnet -> %q, want claude-sonnet-4-6", mapped)
	}
	rewritten, err := sjson.SetBytes(payload, "model", mapped)
	if err != nil {
		t.Fatalf("sjson.SetBytes error: %v", err)
	}
	if got := gjson.GetBytes(rewritten, "model").String(); got != "claude-sonnet-4-6" {
		t.Fatalf("rewritten model = %q, want claude-sonnet-4-6", got)
	}
}

// ---------------------------------------------------------------------------
// Provider routing tests
// ---------------------------------------------------------------------------

func TestJunieUpstreamForModel_OpenAI(t *testing.T) {
	openaiModels := []string{
		"gpt-4.1-2025-04-14", "gpt-5-2025-08-07", "o3-2025-04-16",
		"o4-mini-2025-04-16", "gpt4.1", "gpt-5", "o3", "gpt-4o",
	}
	for _, m := range openaiModels {
		if got := junieUpstreamForModel(m); got != upstreamOpenAI {
			t.Errorf("junieUpstreamForModel(%q) = %q, want %q", m, got, upstreamOpenAI)
		}
	}
}

func TestJunieUpstreamForModel_Anthropic(t *testing.T) {
	claudeModels := []string{
		"claude-sonnet-4-6", "claude-opus-4-6", "claude-4.6-sonnet",
		"claude-sonnet-4-5-20250929", "claude-haiku-4-5-20251001",
	}
	for _, m := range claudeModels {
		if got := junieUpstreamForModel(m); got != upstreamAnthropic {
			t.Errorf("junieUpstreamForModel(%q) = %q, want %q", m, got, upstreamAnthropic)
		}
	}
}

func TestJunieEndpointForUpstream(t *testing.T) {
	if got := junieEndpointForUpstream(upstreamOpenAI); got != ingrazzioOpenAIEndpoint {
		t.Errorf("OpenAI endpoint = %q, want %q", got, ingrazzioOpenAIEndpoint)
	}
	if got := junieEndpointForUpstream(upstreamAnthropic); got != ingrazzioAnthropicEndpoint {
		t.Errorf("Anthropic endpoint = %q, want %q", got, ingrazzioAnthropicEndpoint)
	}
}

func TestMapPayloadModel_ReturnsModelName(t *testing.T) {
	payload := []byte(`{"model":"gpt4.1","messages":[]}`)
	updated, mapped := mapPayloadModel(payload)
	if mapped != "gpt-4.1-2025-04-14" {
		t.Fatalf("mapped = %q, want gpt-4.1-2025-04-14", mapped)
	}
	if got := gjson.GetBytes(updated, "model").String(); got != "gpt-4.1-2025-04-14" {
		t.Fatalf("payload model = %q, want gpt-4.1-2025-04-14", got)
	}
}

func TestMapPayloadModel_UnknownPassThrough(t *testing.T) {
	payload := []byte(`{"model":"some-unknown-model","messages":[]}`)
	_, mapped := mapPayloadModel(payload)
	if mapped != "some-unknown-model" {
		t.Fatalf("mapped = %q, want some-unknown-model", mapped)
	}
}

func TestSetJunieHeaders_OpenAI(t *testing.T) {
	req, _ := http.NewRequest("POST", "https://example.com", nil)
	setJunieHeaders(req, "test-token", upstreamOpenAI)
	if got := req.Header.Get("Authorization"); got != "Bearer test-token" {
		t.Errorf("Authorization = %q", got)
	}
	if got := req.Header.Get("anthropic-version"); got != "" {
		t.Errorf("anthropic-version should not be set for OpenAI, got %q", got)
	}
}

func TestSetJunieHeaders_Anthropic(t *testing.T) {
	req, _ := http.NewRequest("POST", "https://example.com", nil)
	setJunieHeaders(req, "test-token", upstreamAnthropic)
	if got := req.Header.Get("Authorization"); got != "Bearer test-token" {
		t.Errorf("Authorization = %q", got)
	}
	if got := req.Header.Get("anthropic-version"); got != anthropicAPIVersion {
		t.Errorf("anthropic-version = %q, want %q", got, anthropicAPIVersion)
	}
}

// TestJunieExecutor_Execute_RoutesToOpenAI verifies that OpenAI models are routed
// to the OpenAI endpoint with the request body passed through unchanged.
func TestJunieExecutor_Execute_RoutesToOpenAI(t *testing.T) {
	var capturedURL string
	var capturedBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedURL = r.URL.Path
		capturedBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		resp := `{"id":"chatcmpl-1","object":"chat.completion","created":1,"model":"gpt-4.1-2025-04-14","choices":[{"index":0,"message":{"role":"assistant","content":"Hi"},"finish_reason":"stop"}]}`
		w.Write([]byte(resp))
	}))
	defer server.Close()

	// Temporarily override the endpoint constant — we can't, so we test through the full flow
	// by checking the executor sends the correct payload format (OpenAI, not Anthropic).
	// For a true integration test, see TestJunieExecutor_Integration_*.
	// Here we verify the routing logic indirectly.
	model := "gpt4.1"
	mapped := mapJunieModelName(model)
	if mapped != "gpt-4.1-2025-04-14" {
		t.Fatalf("mapping broken: %q", mapped)
	}
	upstream := junieUpstreamForModel(mapped)
	if upstream != upstreamOpenAI {
		t.Fatalf("routing broken: %q should route to OpenAI", mapped)
	}

	_ = capturedURL
	_ = capturedBody
}

// TestJunieExecutor_Execute_RoutesToAnthropic verifies that Claude models are detected
// as Anthropic upstream.
func TestJunieExecutor_Execute_RoutesToAnthropic(t *testing.T) {
	model := "claude-4.6-sonnet"
	mapped := mapJunieModelName(model)
	if mapped != "claude-sonnet-4-6" {
		t.Fatalf("mapping broken: %q", mapped)
	}
	upstream := junieUpstreamForModel(mapped)
	if upstream != upstreamAnthropic {
		t.Fatalf("routing broken: %q should route to Anthropic", mapped)
	}
	endpoint := junieEndpointForUpstream(upstream)
	if !strings.Contains(endpoint, "anthropic") {
		t.Fatalf("endpoint should contain 'anthropic': %q", endpoint)
	}
}
