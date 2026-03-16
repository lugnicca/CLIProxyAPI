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

	junieauth "github.com/router-for-me/CLIProxyAPI/v6/internal/auth/junie"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/util"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/executor"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
	log "github.com/sirupsen/logrus"
)

const (
	// ingrazzioEndpoint is the JetBrains Ingrazzio OpenAI-compatible chat completions endpoint.
	// It accepts standard OpenAI /v1/chat/completions format and returns OpenAI-format responses.
	ingrazzioEndpoint = "https://ingrazzio-cloud-prod.labs.jb.gg/v1/chat/completions"
	grazieUserAgent   = "ktor-client"
	// grazieAgentHeader is required by Ingrazzio to identify the Junie CLI client.
	grazieAgentHeader = `{"name":"junie:cli","version":"888.195"}`
)

// junieModelMapping maps client-facing model aliases to the real provider model names
// accepted by the Ingrazzio /v1/chat/completions endpoint.
// Derived from mitmproxy traffic captures of the Junie CLI (March 2026).
// Models not listed here pass through unchanged.
var junieModelMapping = map[string]string{
	// OpenAI models — client alias -> real provider model name
	"gpt4.1":              "gpt-4.1-2025-04-14",
	"gpt4.1-mini":         "gpt-4.1-mini-2025-04-14",
	"gpt4.1-nano":         "gpt-4.1-nano-2025-04-14",
	"gpt-5":               "gpt-5-2025-08-07",
	"gpt-5-mini":          "gpt-5-mini-2025-08-07",
	"gpt-5-nano":          "gpt-5-nano-2025-08-07",

	// Anthropic models
	"claude-4-sonnet":   "claude-sonnet-4-20250514",
	"claude-4.1-opus":   "claude-opus-4-1-20250805",
	"claude-4.5-sonnet": "claude-sonnet-4-5-20250929",
	"claude-4.5-haiku":  "claude-haiku-4-5-20251001",
	"claude-4.5-opus":   "claude-opus-4-5-20251101",
	"claude-4.6-sonnet": "claude-sonnet-4-6",
	"claude-4.6-opus":   "claude-opus-4-6",

	// Google Gemini models
	"gemini-flash-2.0":      "gemini-2.0-flash",
	"gemini-flash-lite-2.0": "gemini-2.0-flash-lite",
	"gemini-pro-2.5":        "gemini-2.5-pro",
	"gemini-flash-2.5":      "gemini-2.5-flash",
	"gemini-flash-lite-2.5": "gemini-2.5-flash-lite",
}

// mapJunieModelName returns the real provider model name for the given client-facing
// model alias. If the name is not in the mapping it is returned unchanged.
func mapJunieModelName(clientModel string) string {
	if mapped, ok := junieModelMapping[clientModel]; ok {
		return mapped
	}
	return clientModel
}

// JunieExecutor is a stateless executor for the Junie (JetBrains Ingrazzio) provider.
// Requests are forwarded directly to the Ingrazzio /v1/chat/completions endpoint in
// OpenAI format — no format translation is needed. Only the model name is mapped from
// the client-facing alias to the real provider name accepted by the endpoint.
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

// PrepareRequest injects Junie credentials, Grazie-Agent, and User-Agent into the outgoing HTTP request.
func (e *JunieExecutor) PrepareRequest(req *http.Request, auth *cliproxyauth.Auth) error {
	if req == nil {
		return nil
	}
	jwtToken := junieCreds(auth)
	if jwtToken != "" {
		req.Header.Set("Authorization", "Bearer "+jwtToken)
	}
	req.Header.Set("User-Agent", grazieUserAgent)
	req.Header.Set("Grazie-Agent", grazieAgentHeader)
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

// mapPayloadModel reads the model field from payload, maps it via mapJunieModelName,
// and returns the updated payload. If the model is unchanged, the original payload is returned.
func mapPayloadModel(payload []byte) []byte {
	clientModel := gjson.GetBytes(payload, "model").String()
	if clientModel == "" {
		return payload
	}
	mapped := mapJunieModelName(clientModel)
	if mapped == clientModel {
		return payload
	}
	updated, err := sjson.SetBytes(payload, "model", mapped)
	if err != nil {
		return payload
	}
	return updated
}

// Execute performs a non-streaming request to the Ingrazzio /v1/chat/completions endpoint.
// The request is forwarded as-is (OpenAI format) after model name mapping.
// The JSON response is returned directly without any translation.
func (e *JunieExecutor) Execute(ctx context.Context, auth *cliproxyauth.Auth, req cliproxyexecutor.Request, opts cliproxyexecutor.Options) (resp cliproxyexecutor.Response, err error) {
	jwtToken := junieCreds(auth)
	if strings.TrimSpace(jwtToken) == "" {
		return resp, fmt.Errorf("junie executor: missing JWT token")
	}

	// Apply model name mapping to the payload.
	body := mapPayloadModel(req.Payload)

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, ingrazzioEndpoint, bytes.NewReader(body))
	if err != nil {
		return resp, fmt.Errorf("junie executor: build request: %w", err)
	}
	httpReq.Header.Set("Authorization", "Bearer "+jwtToken)
	httpReq.Header.Set("User-Agent", grazieUserAgent)
	httpReq.Header.Set("Grazie-Agent", grazieAgentHeader)
	httpReq.Header.Set("Content-Type", "application/json")

	recordAPIRequest(ctx, e.cfg, upstreamRequestLog{
		URL:      ingrazzioEndpoint,
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

	data, errRead := io.ReadAll(httpResp.Body)
	if errRead != nil {
		recordAPIResponseError(ctx, e.cfg, errRead)
		return resp, errRead
	}
	appendAPIResponseChunk(ctx, e.cfg, data)

	if httpResp.StatusCode < 200 || httpResp.StatusCode >= 300 {
		logWithRequestID(ctx).Debugf("junie executor: request error, status: %d, body: %s", httpResp.StatusCode, summarizeErrorBody(httpResp.Header.Get("Content-Type"), data))
		return resp, statusErr{code: httpResp.StatusCode, msg: string(data)}
	}

	// Return the OpenAI-format JSON response directly — no translation needed.
	resp = cliproxyexecutor.Response{Payload: data, Headers: httpResp.Header.Clone()}
	return resp, nil
}

// ExecuteStream performs a streaming request to the Ingrazzio /v1/chat/completions endpoint.
// SSE lines are piped directly to the output channel — no translation is needed since
// Ingrazzio returns native OpenAI SSE format.
func (e *JunieExecutor) ExecuteStream(ctx context.Context, auth *cliproxyauth.Auth, req cliproxyexecutor.Request, opts cliproxyexecutor.Options) (_ *cliproxyexecutor.StreamResult, err error) {
	jwtToken := junieCreds(auth)
	if strings.TrimSpace(jwtToken) == "" {
		return nil, fmt.Errorf("junie executor: missing JWT token")
	}

	// Apply model name mapping to the payload.
	body := mapPayloadModel(req.Payload)

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, ingrazzioEndpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("junie executor: build request: %w", err)
	}
	httpReq.Header.Set("Authorization", "Bearer "+jwtToken)
	httpReq.Header.Set("User-Agent", grazieUserAgent)
	httpReq.Header.Set("Grazie-Agent", grazieAgentHeader)
	httpReq.Header.Set("Content-Type", "application/json")

	recordAPIRequest(ctx, e.cfg, upstreamRequestLog{
		URL:      ingrazzioEndpoint,
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
		for scanner.Scan() {
			line := scanner.Bytes()
			appendAPIResponseChunk(ctx, e.cfg, line)

			// Pass through non-empty SSE lines directly — Ingrazzio returns native
			// OpenAI SSE format so no translation is required.
			trimmed := bytes.TrimSpace(line)
			if len(trimmed) > 0 {
				out <- cliproxyexecutor.StreamChunk{Payload: bytes.Clone(line)}
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
