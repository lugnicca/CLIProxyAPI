package executor

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/util"
	junieauth "github.com/router-for-me/CLIProxyAPI/v6/internal/auth/junie"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/executor"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v6/sdk/translator"
	log "github.com/sirupsen/logrus"
)

const (
	grazieStreamEndpoint = "https://ingrazzio-cloud-prod.labs.jb.gg/user/v5/llm/chat/stream/v7"
	grazieUserAgent      = "ktor-client"
)

// JunieExecutor is a stateless executor for the Junie (JetBrains Grazie) provider.
type JunieExecutor struct {
	cfg *config.Config
}

// NewJunieExecutor constructs a new JunieExecutor.
func NewJunieExecutor(cfg *config.Config) *JunieExecutor {
	return &JunieExecutor{cfg: cfg}
}

// Identifier returns the provider key.
func (e *JunieExecutor) Identifier() string { return "junie" }

// junieCreds extracts the token from the auth record.
// Priority order:
//  1. Attributes["api_key"] - manually supplied JWT or API key
//  2. Metadata["jwt_token"] - legacy JWT token from stored credentials
//  3. Metadata["access_token"] - OAuth access token from JetBrains Account flow
func junieCreds(a *cliproxyauth.Auth) (jwtToken string) {
	if a == nil {
		return ""
	}
	if a.Attributes != nil {
		if v := strings.TrimSpace(a.Attributes["api_key"]); v != "" {
			return v
		}
	}
	if a.Metadata != nil {
		if v, ok := a.Metadata["jwt_token"].(string); ok {
			if trimmed := strings.TrimSpace(v); trimmed != "" {
				return trimmed
			}
		}
		// OAuth access_token from JetBrains Account PKCE flow
		if v, ok := a.Metadata["access_token"].(string); ok {
			if trimmed := strings.TrimSpace(v); trimmed != "" {
				return trimmed
			}
		}
	}
	return ""
}

// PrepareRequest injects Grazie JWT credentials and User-Agent into the outgoing HTTP request.
func (e *JunieExecutor) PrepareRequest(req *http.Request, auth *cliproxyauth.Auth) error {
	if req == nil {
		return nil
	}
	jwtToken := junieCreds(auth)
	if jwtToken != "" {
		req.Header.Set("Authorization", "Bearer "+jwtToken)
	}
	req.Header.Set("User-Agent", grazieUserAgent)
	return nil
}

// HttpRequest injects Junie credentials into the request and executes it.
func (e *JunieExecutor) HttpRequest(ctx context.Context, auth *cliproxyauth.Auth, req *http.Request) (*http.Response, error) {
	if req == nil {
		return nil, fmt.Errorf("junie executor: request is nil")
	}
	if ctx == nil {
		ctx = req.Context()
	}
	httpReq := req.WithContext(ctx)
	if err := e.PrepareRequest(httpReq, auth); err != nil {
		return nil, err
	}
	client := util.SetProxy(&e.cfg.SDKConfig, &http.Client{})
	return client.Do(httpReq)
}

// Execute performs a non-streaming request to the Grazie API.
// Grazie always returns an SSE stream; this method accumulates the full stream
// and returns the translated non-streaming response.
func (e *JunieExecutor) Execute(ctx context.Context, auth *cliproxyauth.Auth, req cliproxyexecutor.Request, opts cliproxyexecutor.Options) (resp cliproxyexecutor.Response, err error) {
	jwtToken := junieCreds(auth)
	if strings.TrimSpace(jwtToken) == "" {
		return resp, fmt.Errorf("junie executor: missing JWT token")
	}

	from := opts.SourceFormat
	to := sdktranslator.Format("junie")

	originalPayload := opts.OriginalRequest
	if len(originalPayload) == 0 {
		originalPayload = req.Payload
	}

	// Translate from OpenAI format to Grazie format.
	body := sdktranslator.TranslateRequest(from, to, req.Model, req.Payload, false)

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, grazieStreamEndpoint, bytes.NewReader(body))
	if err != nil {
		return resp, fmt.Errorf("junie executor: build request: %w", err)
	}
	httpReq.Header.Set("Authorization", "Bearer "+jwtToken)
	httpReq.Header.Set("User-Agent", grazieUserAgent)
	httpReq.Header.Set("Content-Type", "application/json")

	recordAPIRequest(ctx, e.cfg, upstreamRequestLog{
		URL:      grazieStreamEndpoint,
		Method:   http.MethodPost,
		Headers:  httpReq.Header.Clone(),
		Body:     body,
		Provider: e.Identifier(),
	})

	client := util.SetProxy(&e.cfg.SDKConfig, &http.Client{})
	httpResp, err := client.Do(httpReq)
	if err != nil {
		recordAPIResponseError(ctx, e.cfg, err)
		return resp, err
	}
	defer func() {
		if errClose := httpResp.Body.Close(); errClose != nil {
			log.Errorf("junie executor: close response body error: %v", errClose)
		}
	}()

	recordAPIResponseMetadata(ctx, e.cfg, httpResp.StatusCode, httpResp.Header.Clone())

	if httpResp.StatusCode < 200 || httpResp.StatusCode >= 300 {
		b, _ := io.ReadAll(httpResp.Body)
		appendAPIResponseChunk(ctx, e.cfg, b)
		logWithRequestID(ctx).Debugf("junie executor: request error, status: %d, body: %s", httpResp.StatusCode, summarizeErrorBody(httpResp.Header.Get("Content-Type"), b))
		return resp, statusErr{code: httpResp.StatusCode, msg: string(b)}
	}

	data, err := io.ReadAll(httpResp.Body)
	if err != nil {
		recordAPIResponseError(ctx, e.cfg, err)
		return resp, err
	}
	appendAPIResponseChunk(ctx, e.cfg, data)

	// Feed each SSE line through the translator. The NonStream translator
	// accumulates Content chunks and returns the full response on QuotaMetadata.
	var param any
	var result string
	lines := bytes.Split(data, []byte("\n"))
	for _, line := range lines {
		trimmed := bytes.TrimSpace(line)
		if len(trimmed) == 0 {
			continue
		}
		out := sdktranslator.TranslateNonStream(ctx, to, from, req.Model, originalPayload, body, trimmed, &param)
		if out != "" {
			result = out
		}
	}

	// If the stream didn't contain a QuotaMetadata terminator (unusual), we may
	// still have accumulated content in the translator param. Build a synthetic
	// stop so the translator flushes.
	if result == "" {
		syntheticStop := []byte(`data: {"type":"QuotaMetadata"}`)
		out := sdktranslator.TranslateNonStream(ctx, to, from, req.Model, originalPayload, body, syntheticStop, &param)
		if out != "" {
			result = out
		}
	}

	resp = cliproxyexecutor.Response{Payload: []byte(result), Headers: httpResp.Header.Clone()}
	return resp, nil
}

// ExecuteStream performs a streaming request to the Grazie API.
// It pipes SSE lines from Grazie through the translator and sends them on the returned channel.
func (e *JunieExecutor) ExecuteStream(ctx context.Context, auth *cliproxyauth.Auth, req cliproxyexecutor.Request, opts cliproxyexecutor.Options) (_ *cliproxyexecutor.StreamResult, err error) {
	jwtToken := junieCreds(auth)
	if strings.TrimSpace(jwtToken) == "" {
		return nil, fmt.Errorf("junie executor: missing JWT token")
	}

	from := opts.SourceFormat
	to := sdktranslator.Format("junie")

	originalPayload := opts.OriginalRequest
	if len(originalPayload) == 0 {
		originalPayload = req.Payload
	}

	// Translate from OpenAI format to Grazie format.
	body := sdktranslator.TranslateRequest(from, to, req.Model, req.Payload, true)

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, grazieStreamEndpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("junie executor: build request: %w", err)
	}
	httpReq.Header.Set("Authorization", "Bearer "+jwtToken)
	httpReq.Header.Set("User-Agent", grazieUserAgent)
	httpReq.Header.Set("Content-Type", "application/json")

	recordAPIRequest(ctx, e.cfg, upstreamRequestLog{
		URL:      grazieStreamEndpoint,
		Method:   http.MethodPost,
		Headers:  httpReq.Header.Clone(),
		Body:     body,
		Provider: e.Identifier(),
	})

	client := util.SetProxy(&e.cfg.SDKConfig, &http.Client{})
	httpResp, err := client.Do(httpReq)
	if err != nil {
		recordAPIResponseError(ctx, e.cfg, err)
		return nil, err
	}

	recordAPIResponseMetadata(ctx, e.cfg, httpResp.StatusCode, httpResp.Header.Clone())

	if httpResp.StatusCode < 200 || httpResp.StatusCode >= 300 {
		data, _ := io.ReadAll(httpResp.Body)
		if errClose := httpResp.Body.Close(); errClose != nil {
			log.Errorf("junie executor: close response body error: %v", errClose)
		}
		appendAPIResponseChunk(ctx, e.cfg, data)
		logWithRequestID(ctx).Debugf("junie executor: request error, status: %d, body: %s", httpResp.StatusCode, summarizeErrorBody(httpResp.Header.Get("Content-Type"), data))
		return nil, statusErr{code: httpResp.StatusCode, msg: string(data)}
	}

	out := make(chan cliproxyexecutor.StreamChunk, 64)
	go func() {
		defer close(out)
		defer func() {
			if errClose := httpResp.Body.Close(); errClose != nil {
				log.Errorf("junie executor: close response body error: %v", errClose)
			}
		}()

		scanner := bufio.NewScanner(httpResp.Body)
		scanner.Buffer(nil, 52_428_800) // 50MB
		var param any
		for scanner.Scan() {
			line := scanner.Bytes()
			appendAPIResponseChunk(ctx, e.cfg, line)

			chunks := sdktranslator.TranslateStream(ctx, to, from, req.Model, originalPayload, body, bytes.Clone(line), &param)
			for i := range chunks {
				out <- cliproxyexecutor.StreamChunk{Payload: []byte(chunks[i])}
			}
		}
		if errScan := scanner.Err(); errScan != nil {
			recordAPIResponseError(ctx, e.cfg, errScan)
			out <- cliproxyexecutor.StreamChunk{Err: errScan}
		}
	}()

	return &cliproxyexecutor.StreamResult{Headers: httpResp.Header.Clone(), Chunks: out}, nil
}

// Refresh attempts to refresh the Junie auth tokens.
// For OAuth PKCE mode: uses the refresh_token from auth.Metadata to obtain new tokens.
// For legacy JWT manual mode (no refresh_token in metadata): returns auth unchanged.
// Nil auth is returned as-is without error.
func (e *JunieExecutor) Refresh(ctx context.Context, auth *cliproxyauth.Auth) (*cliproxyauth.Auth, error) {
	if auth == nil {
		return nil, nil
	}

	// Check for OAuth refresh token in metadata
	var refreshToken string
	if auth.Metadata != nil {
		if v, ok := auth.Metadata["refresh_token"].(string); ok {
			refreshToken = strings.TrimSpace(v)
		}
	}

	if refreshToken == "" {
		// No refresh token available - legacy JWT manual mode, return as-is
		log.Warnf("junie executor: no refresh_token in metadata; JWT tokens must be manually refreshed")
		return auth, nil
	}

	// OAuth mode: exchange refresh token for new tokens
	tokenData, err := junieauth.NewJunieAuth(e.cfg).RefreshTokens(ctx, refreshToken)
	if err != nil {
		return nil, fmt.Errorf("junie executor: token refresh failed: %w", err)
	}

	// Update metadata with new token values
	if auth.Metadata == nil {
		auth.Metadata = make(map[string]any)
	}
	auth.Metadata["access_token"] = tokenData.AccessToken
	if tokenData.RefreshToken != "" {
		auth.Metadata["refresh_token"] = tokenData.RefreshToken
	}
	if tokenData.IDToken != "" {
		auth.Metadata["id_token"] = tokenData.IDToken
	}
	auth.Metadata["expired"] = tokenData.Expire
	auth.Metadata["last_refresh"] = time.Now().Format(time.RFC3339)
	auth.Metadata["type"] = "junie"

	log.Debugf("junie executor: tokens refreshed successfully")
	return auth, nil
}

// CountTokens is not supported for the Junie provider.
func (e *JunieExecutor) CountTokens(_ context.Context, _ *cliproxyauth.Auth, _ cliproxyexecutor.Request, _ cliproxyexecutor.Options) (cliproxyexecutor.Response, error) {
	return cliproxyexecutor.Response{}, fmt.Errorf("junie executor: CountTokens not supported")
}
