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
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v6/sdk/translator"
	log "github.com/sirupsen/logrus"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// --------------------------------------------------------------------------
// Ingrazzio backend endpoints (per-provider, native format).
// Discovered via Junie CLI JAR decompilation (March 2026).
// --------------------------------------------------------------------------

const (
	ingrazzioBase = "https://ingrazzio-cloud-prod.labs.jb.gg"

	// OpenAI-format endpoint — accepts only OpenAI models.
	ingrazzioOpenAIEndpoint = ingrazzioBase + "/user/v5/llm/openai/v1/chat/completions"

	// Anthropic Messages-format endpoint — accepts only Claude models.
	ingrazzioAnthropicEndpoint = ingrazzioBase + "/user/v5/llm/anthropic/v1/messages"

	grazieUserAgent   = "ktor-client"
	grazieAgentHeader = `{"name":"junie:cli","version":"888.195"}`

	anthropicAPIVersion = "2023-06-01"
)

// --------------------------------------------------------------------------
// Provider detection — determines which Ingrazzio endpoint to use.
// --------------------------------------------------------------------------

type junieUpstream string

const (
	upstreamOpenAI    junieUpstream = "openai"
	upstreamAnthropic junieUpstream = "anthropic"
)

// junieUpstreamForModel returns the upstream provider for the given model name.
// Both the client-facing alias and the mapped real name are checked.
func junieUpstreamForModel(model string) junieUpstream {
	lower := strings.ToLower(model)
	if strings.HasPrefix(lower, "claude") {
		return upstreamAnthropic
	}
	// Default: OpenAI endpoint (handles gpt-*, o1, o3, o4-mini, etc.)
	return upstreamOpenAI
}

func junieEndpointForUpstream(up junieUpstream) string {
	switch up {
	case upstreamAnthropic:
		return ingrazzioAnthropicEndpoint
	default:
		return ingrazzioOpenAIEndpoint
	}
}

// --------------------------------------------------------------------------
// Model name mapping: client-facing alias → real Ingrazzio model name.
// --------------------------------------------------------------------------

var junieModelMapping = map[string]string{
	// OpenAI models
	"gpt4.1":      "gpt-4.1-2025-04-14",
	"gpt4.1-mini": "gpt-4.1-mini-2025-04-14",
	"gpt4.1-nano": "gpt-4.1-nano-2025-04-14",
	"gpt-5":       "gpt-5-2025-08-07",
	"gpt-5-mini":  "gpt-5-mini-2025-08-07",
	"gpt-5-nano":  "gpt-5-nano-2025-08-07",
	"gpt-5.1":     "gpt-5.1-2025-11-13",
	"gpt-5.2":     "gpt-5.2-2025-12-11",
	"gpt-5.4":     "gpt-5.4-2026-03-05",
	"o1":          "o1-2024-12-17",
	"o3":          "o3-2025-04-16",
	"o3-mini":     "o3-mini-2025-01-31",
	"o4-mini":     "o4-mini-2025-04-16",
	"gpt-4o":      "gpt-4o-2024-11-20",
	"gpt-4o-mini": "gpt-4o-mini-2024-07-18",

	// Anthropic models
	"claude-4-sonnet":   "claude-sonnet-4-20250514",
	"claude-4.1-opus":   "claude-opus-4-1-20250805",
	"claude-4.5-sonnet": "claude-sonnet-4-5-20250929",
	"claude-4.5-haiku":  "claude-haiku-4-5-20251001",
	"claude-4.5-opus":   "claude-opus-4-5-20251101",
	"claude-4.6-sonnet": "claude-sonnet-4-6",
	"claude-4.6-opus":   "claude-opus-4-6",

	// Gemini models (OpenAI endpoint may not accept — placeholder for future)
	"gemini-flash-2.0":      "gemini-2.0-flash",
	"gemini-flash-lite-2.0": "gemini-2.0-flash-lite",
	"gemini-pro-2.5":        "gemini-2.5-pro",
	"gemini-flash-2.5":      "gemini-2.5-flash",
	"gemini-flash-lite-2.5": "gemini-2.5-flash-lite",
}

func mapJunieModelName(clientModel string) string {
	if mapped, ok := junieModelMapping[clientModel]; ok {
		return mapped
	}
	return clientModel
}

// --------------------------------------------------------------------------
// JunieExecutor
// --------------------------------------------------------------------------

// JunieExecutor routes requests to the correct Ingrazzio per-provider endpoint.
// OpenAI models → OpenAI endpoint (pass-through).
// Anthropic models → Anthropic endpoint (OpenAI→Anthropic translation using existing translators).
type JunieExecutor struct {
	cfg *config.Config
}

func NewJunieExecutor(cfg *config.Config) *JunieExecutor {
	return &JunieExecutor{cfg: cfg}
}

func (e *JunieExecutor) Identifier() string { return "junie" }

// --------------------------------------------------------------------------
// Credentials
// --------------------------------------------------------------------------

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
		if v, ok := a.Metadata["access_token"].(string); ok {
			if trimmed := strings.TrimSpace(v); trimmed != "" {
				return trimmed
			}
		}
	}
	return ""
}

// --------------------------------------------------------------------------
// PrepareRequest / HttpRequest (interface compliance)
// --------------------------------------------------------------------------

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

// --------------------------------------------------------------------------
// Payload helpers
// --------------------------------------------------------------------------

// mapPayloadModel maps the model field in the JSON payload and returns (updated payload, mapped model name).
func mapPayloadModel(payload []byte) ([]byte, string) {
	clientModel := gjson.GetBytes(payload, "model").String()
	if clientModel == "" {
		return payload, ""
	}
	mapped := mapJunieModelName(clientModel)
	if mapped == clientModel {
		return payload, mapped
	}
	updated, err := sjson.SetBytes(payload, "model", mapped)
	if err != nil {
		return payload, mapped
	}
	return updated, mapped
}

// translateOpenAIToAnthropic converts an OpenAI chat completion request body
// into an Anthropic Messages API request body using the existing translator.
func translateOpenAIToAnthropic(modelName string, payload []byte) []byte {
	from := sdktranslator.FromString("openai")
	to := sdktranslator.FromString("claude")
	return sdktranslator.TranslateRequest(from, to, modelName, payload, false)
}

// anthropicResponseToOpenAI converts a native Anthropic Messages API JSON response
// into an OpenAI chat.completion JSON response. This is a lightweight conversion that
// handles the common case (text content, stop reason, usage) without needing the full
// translator machinery (which expects specific request context).
func anthropicResponseToOpenAI(responseBody []byte) []byte {
	// Parse Anthropic response fields.
	id := gjson.GetBytes(responseBody, "id").String()
	model := gjson.GetBytes(responseBody, "model").String()
	stopReason := gjson.GetBytes(responseBody, "stop_reason").String()

	// Extract text content from Anthropic content blocks.
	var textContent string
	contents := gjson.GetBytes(responseBody, "content")
	if contents.IsArray() {
		for _, block := range contents.Array() {
			if block.Get("type").String() == "text" {
				textContent += block.Get("text").String()
			}
		}
	}

	// Map Anthropic stop_reason to OpenAI finish_reason.
	finishReason := "stop"
	switch stopReason {
	case "end_turn", "stop_sequence":
		finishReason = "stop"
	case "max_tokens":
		finishReason = "length"
	case "tool_use":
		finishReason = "tool_calls"
	}

	// Extract tool_calls if present.
	var toolCallsJSON string
	if contents.IsArray() {
		var toolCalls []string
		for _, block := range contents.Array() {
			if block.Get("type").String() == "tool_use" {
				tc := fmt.Sprintf(`{"id":%q,"type":"function","function":{"name":%q,"arguments":%s}}`,
					block.Get("id").String(),
					block.Get("name").String(),
					block.Get("input").Raw)
				toolCalls = append(toolCalls, tc)
			}
		}
		if len(toolCalls) > 0 {
			toolCallsJSON = `,"tool_calls":[` + strings.Join(toolCalls, ",") + `]`
		}
	}

	// Build usage.
	inputTokens := gjson.GetBytes(responseBody, "usage.input_tokens").Int()
	outputTokens := gjson.GetBytes(responseBody, "usage.output_tokens").Int()

	// Build OpenAI response.
	out := fmt.Sprintf(`{"id":%q,"object":"chat.completion","created":%d,"model":%q,"choices":[{"index":0,"message":{"role":"assistant","content":%q%s},"finish_reason":%q}],"usage":{"prompt_tokens":%d,"completion_tokens":%d,"total_tokens":%d}}`,
		id, time.Now().Unix(), model,
		textContent, toolCallsJSON,
		finishReason,
		inputTokens, outputTokens, inputTokens+outputTokens)
	return []byte(out)
}

// translateAnthropicToOpenAIStream converts an Anthropic SSE streaming chunk
// into OpenAI SSE format using the existing translator.
func translateAnthropicToOpenAIStream(ctx context.Context, modelName string, originalReq, translatedReq, chunk []byte, param *any) []string {
	from := sdktranslator.FromString("claude")
	to := sdktranslator.FromString("openai")
	return sdktranslator.TranslateStream(ctx, from, to, modelName, originalReq, translatedReq, chunk, param)
}

// setJunieHeaders sets all required headers for an Ingrazzio request.
func setJunieHeaders(req *http.Request, jwtToken string, upstream junieUpstream) {
	req.Header.Set("Authorization", "Bearer "+jwtToken)
	req.Header.Set("User-Agent", grazieUserAgent)
	req.Header.Set("Grazie-Agent", grazieAgentHeader)
	req.Header.Set("Content-Type", "application/json")
	if upstream == upstreamAnthropic {
		req.Header.Set("anthropic-version", anthropicAPIVersion)
	}
}

// --------------------------------------------------------------------------
// Execute (non-streaming)
// --------------------------------------------------------------------------

func (e *JunieExecutor) Execute(ctx context.Context, auth *cliproxyauth.Auth, req cliproxyexecutor.Request, opts cliproxyexecutor.Options) (resp cliproxyexecutor.Response, err error) {
	jwtToken := junieCreds(auth)
	if strings.TrimSpace(jwtToken) == "" {
		return resp, fmt.Errorf("junie executor: missing JWT token")
	}

	// Map model name and determine upstream provider.
	body, mappedModel := mapPayloadModel(req.Payload)
	if mappedModel == "" {
		mappedModel = gjson.GetBytes(body, "model").String()
	}
	upstream := junieUpstreamForModel(mappedModel)
	endpoint := junieEndpointForUpstream(upstream)

	// For Anthropic upstream: translate OpenAI → Anthropic format.
	if upstream == upstreamAnthropic {
		body = translateOpenAIToAnthropic(mappedModel, body)
		body, _ = sjson.SetBytes(body, "model", mappedModel)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return resp, fmt.Errorf("junie executor: build request: %w", err)
	}
	setJunieHeaders(httpReq, jwtToken, upstream)

	recordAPIRequest(ctx, e.cfg, upstreamRequestLog{
		URL:      endpoint,
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

	// For Anthropic upstream: translate response back to OpenAI format.
	if upstream == upstreamAnthropic {
		data = anthropicResponseToOpenAI(data)
	}

	resp = cliproxyexecutor.Response{Payload: data, Headers: httpResp.Header.Clone()}
	return resp, nil
}

// --------------------------------------------------------------------------
// ExecuteStream (streaming)
// --------------------------------------------------------------------------

func (e *JunieExecutor) ExecuteStream(ctx context.Context, auth *cliproxyauth.Auth, req cliproxyexecutor.Request, opts cliproxyexecutor.Options) (_ *cliproxyexecutor.StreamResult, err error) {
	jwtToken := junieCreds(auth)
	if strings.TrimSpace(jwtToken) == "" {
		return nil, fmt.Errorf("junie executor: missing JWT token")
	}

	// Map model name and determine upstream provider.
	body, mappedModel := mapPayloadModel(req.Payload)
	if mappedModel == "" {
		mappedModel = gjson.GetBytes(body, "model").String()
	}
	upstream := junieUpstreamForModel(mappedModel)
	endpoint := junieEndpointForUpstream(upstream)

	// For Anthropic upstream: translate OpenAI → Anthropic format, force stream=true.
	originalPayload := body
	var translatedPayload []byte
	if upstream == upstreamAnthropic {
		body = translateOpenAIToAnthropic(mappedModel, body)
		body, _ = sjson.SetBytes(body, "model", mappedModel)
		body, _ = sjson.SetBytes(body, "stream", true)
		translatedPayload = body
	} else {
		// Ensure stream=true for OpenAI endpoint.
		body, _ = sjson.SetBytes(body, "stream", true)
		translatedPayload = body
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("junie executor: build request: %w", err)
	}
	setJunieHeaders(httpReq, jwtToken, upstream)

	recordAPIRequest(ctx, e.cfg, upstreamRequestLog{
		URL:      endpoint,
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

		var param any
		scanner := bufio.NewScanner(httpResp.Body)
		scanner.Buffer(nil, 52_428_800) // 50MB
		for scanner.Scan() {
			line := scanner.Bytes()
			appendAPIResponseChunk(ctx, e.cfg, line)

			trimmed := bytes.TrimSpace(line)
			if len(trimmed) == 0 {
				continue
			}

			if upstream == upstreamAnthropic {
				// Translate Anthropic SSE → OpenAI SSE format.
				translated := translateAnthropicToOpenAIStream(ctx, mappedModel, originalPayload, translatedPayload, line, &param)
				for _, chunk := range translated {
					if strings.TrimSpace(chunk) != "" {
						out <- cliproxyexecutor.StreamChunk{Payload: []byte(chunk)}
					}
				}
			} else {
				// OpenAI upstream: pass-through.
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

// --------------------------------------------------------------------------
// Refresh (OAuth token refresh)
// --------------------------------------------------------------------------

func (e *JunieExecutor) Refresh(ctx context.Context, auth *cliproxyauth.Auth) (*cliproxyauth.Auth, error) {
	if auth == nil {
		return nil, nil
	}

	var refreshToken string
	if auth.Metadata != nil {
		if v, ok := auth.Metadata["refresh_token"].(string); ok {
			refreshToken = strings.TrimSpace(v)
		}
	}

	if refreshToken == "" {
		log.Warnf("junie executor: no refresh_token in metadata; JWT tokens must be manually refreshed")
		return auth, nil
	}

	tokenData, err := junieauth.NewJunieAuth(e.cfg).RefreshTokens(ctx, refreshToken)
	if err != nil {
		return nil, fmt.Errorf("junie executor: token refresh failed: %w", err)
	}

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

// --------------------------------------------------------------------------
// CountTokens (not supported)
// --------------------------------------------------------------------------

func (e *JunieExecutor) CountTokens(_ context.Context, _ *cliproxyauth.Auth, _ cliproxyexecutor.Request, _ cliproxyexecutor.Options) (cliproxyexecutor.Response, error) {
	return cliproxyexecutor.Response{}, fmt.Errorf("junie executor: CountTokens not supported")
}
