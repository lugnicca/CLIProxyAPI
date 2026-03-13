package executor

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/util"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/executor"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v6/sdk/translator"
	log "github.com/sirupsen/logrus"
	"github.com/tidwall/gjson"
)

const (
	grazieStreamEndpoint = "https://api.jetbrains.ai/user/v5/llm/chat/stream/v7"
	grazieJWTHeader      = "grazie-authenticate-jwt"
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

// junieCreds extracts the JWT token from the auth record.
// It first checks Attributes["api_key"], then falls back to Metadata["jwt_token"].
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
	}
	return ""
}

// PrepareRequest injects Grazie JWT credentials and User-Agent into the outgoing HTTP request.
func (e *JunieExecutor) PrepareRequest(req *http.Request, auth *cliproxyauth.Auth) error {
	if req == nil {
		return nil
	}
	jwtToken := junieCreds(auth)
	if strings.TrimSpace(jwtToken) != "" {
		req.Header.Set(grazieJWTHeader, jwtToken)
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

	from := sdktranslator.Format("junie")
	to := opts.SourceFormat

	originalPayload := opts.OriginalRequest
	if len(originalPayload) == 0 {
		originalPayload = req.Payload
	}

	body := req.Payload

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, grazieStreamEndpoint, bytes.NewReader(body))
	if err != nil {
		return resp, fmt.Errorf("junie executor: build request: %w", err)
	}
	httpReq.Header.Set(grazieJWTHeader, jwtToken)
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

	// Parse SSE lines from Grazie response.
	// Accumulate Content chunks and find the final QuotaMetadata event.
	var contentBuf strings.Builder
	var quotaLine []byte

	lines := bytes.Split(data, []byte("\n"))
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

	// Build the raw JSON to pass through the translator.
	// If we have a QuotaMetadata line, use it as the base; otherwise build a minimal response.
	var rawJSON []byte
	if len(quotaLine) > 0 {
		rawJSON = quotaLine
	} else {
		// Synthesise a minimal response so the translator has something to work with.
		rawJSON = []byte(fmt.Sprintf(`{"type":"Content","content":%q}`, contentBuf.String()))
	}

	var param any
	out := sdktranslator.TranslateNonStream(ctx, from, to, req.Model, originalPayload, body, rawJSON, &param)
	resp = cliproxyexecutor.Response{Payload: []byte(out), Headers: httpResp.Header.Clone()}
	return resp, nil
}

// ExecuteStream performs a streaming request to the Grazie API.
// It pipes SSE lines from Grazie through the translator and sends them on the returned channel.
func (e *JunieExecutor) ExecuteStream(ctx context.Context, auth *cliproxyauth.Auth, req cliproxyexecutor.Request, opts cliproxyexecutor.Options) (_ *cliproxyexecutor.StreamResult, err error) {
	jwtToken := junieCreds(auth)
	if strings.TrimSpace(jwtToken) == "" {
		return nil, fmt.Errorf("junie executor: missing JWT token")
	}

	from := sdktranslator.Format("junie")
	to := opts.SourceFormat

	originalPayload := opts.OriginalRequest
	if len(originalPayload) == 0 {
		originalPayload = req.Payload
	}

	body := req.Payload

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, grazieStreamEndpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("junie executor: build request: %w", err)
	}
	httpReq.Header.Set(grazieJWTHeader, jwtToken)
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

			chunks := sdktranslator.TranslateStream(ctx, from, to, req.Model, originalPayload, body, bytes.Clone(line), &param)
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

// Refresh is a no-op for Junie: JWT tokens must be manually rotated.
func (e *JunieExecutor) Refresh(ctx context.Context, auth *cliproxyauth.Auth) (*cliproxyauth.Auth, error) {
	log.Warnf("junie executor: JWT tokens must be manually refreshed; automatic refresh is not supported")
	return auth, nil
}

// CountTokens is not supported for the Junie provider.
func (e *JunieExecutor) CountTokens(_ context.Context, _ *cliproxyauth.Auth, _ cliproxyexecutor.Request, _ cliproxyexecutor.Options) (cliproxyexecutor.Response, error) {
	return cliproxyexecutor.Response{}, fmt.Errorf("junie executor: CountTokens not supported")
}
