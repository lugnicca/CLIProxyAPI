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

// sseBody returns a well-formed SSE body that the mock Grazie server sends.
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
	// User-Agent should always be set regardless.
	if got := req.Header.Get("User-Agent"); got != grazieUserAgent {
		t.Fatalf("User-Agent = %q, want %q", got, grazieUserAgent)
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
// Mock Grazie SSE server helpers
// ---------------------------------------------------------------------------

// newMockGrazieServer starts an httptest.Server that:
//   - Accepts POST requests only.
//   - Requires the grazie-authenticate-jwt header.
//   - Optionally verifies the request body contains expected JSON fields.
//   - Streams back a hard-coded SSE payload.
func newMockGrazieServer(t *testing.T, wantJWT string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Method check
		if r.Method != http.MethodPost {
			t.Errorf("mock Grazie server: expected POST, got %s", r.Method)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		// JWT header check
		if jwt := r.Header.Get("Authorization"); jwt == "" {
			t.Errorf("mock Grazie server: missing %s header", "Authorization")
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		} else if wantJWT != "" && jwt != "Bearer "+wantJWT {
			t.Errorf("mock Grazie server: JWT = %q, want %q", jwt, wantJWT)
		}

		// Read and lightly validate body
		body, _ := io.ReadAll(r.Body)
		if len(body) > 0 {
			// If it's a non-empty body, it must be valid JSON and expected
			// to contain at least one of the Grazie format fields.
			result := gjson.ParseBytes(body)
			if !result.IsObject() {
				t.Errorf("mock Grazie server: request body is not a JSON object: %s", body)
			}
		}

		// Stream SSE response
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprint(w, sseBody())
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

	body := []byte(`{"prompt":"hello","profile":"junie","chat":[]}`)
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
	if !bytes.Contains(data, []byte("Content")) {
		t.Fatalf("response body does not contain expected SSE content: %s", data)
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
// TestJunieSSEParsing – unit-test the SSE accumulation logic in isolation
// ---------------------------------------------------------------------------

// parseSSEContent replicates the SSE parsing loop from Execute so we can test
// it without needing a real network call.
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
		Model:   "junie-model",
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
		Model:   "junie-model",
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
// TestJunieExecutor_Integration_MockGrazieSSE
//
// Full end-to-end integration test:
//   - Spin up a mock Grazie SSE server
//   - Use HttpRequest to hit it (bypassing the hard-coded const endpoint)
//   - Parse the SSE stream using bufio.Scanner (same as ExecuteStream)
//   - Verify accumulated content matches expected
// ---------------------------------------------------------------------------

func TestJunieExecutor_Integration_MockGrazieSSE(t *testing.T) {
	server := newMockGrazieServer(t, "integration-jwt")
	defer server.Close()

	e := newJunieExecutor()
	auth := newJunieAuth("integration-jwt")

	body := []byte(`{"prompt":"test","profile":"junie","chat":[{"role":"user","content":"hello"}]}`)
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
	var quotaAmount string

	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Bytes()
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
			accumulated.WriteString(gjson.GetBytes(payload, "content").String())
		case "QuotaMetadata":
			quotaAmount = gjson.GetBytes(payload, "spent.amount").String()
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scanner error: %v", err)
	}

	if got := accumulated.String(); got != "Hello world" {
		t.Fatalf("accumulated SSE content = %q, want %q", got, "Hello world")
	}
	if quotaAmount != "42" {
		t.Fatalf("quota amount = %q, want %q", quotaAmount, "42")
	}
	t.Logf("Integration test passed: content=%q quota=%q", accumulated.String(), quotaAmount)
}

// ---------------------------------------------------------------------------
// TestJunieExecutor_Integration_MockGrazieSSE_ErrorStatus
// ---------------------------------------------------------------------------

func TestJunieExecutor_Integration_MockGrazieSSE_ErrorStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"quota exceeded"}`, http.StatusTooManyRequests)
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
