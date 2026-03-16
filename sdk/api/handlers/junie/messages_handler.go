// Package junie provides HTTP handlers for the Junie (JetBrains Ingrazzio) provider.
// It exposes an Anthropic Messages-compatible endpoint that routes requests to the
// correct Ingrazzio backend: Claude models go to the Anthropic endpoint (pass-through),
// while GPT/Grok/Gemini models go to the OpenAI endpoint (with format translation).
// This enables Claude Code to use ALL JetBrains AI models transparently.
package junie

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

const (
	ingrazzioBase            = "https://ingrazzio-cloud-prod.labs.jb.gg"
	ingrazzioAnthropicMsgURL = ingrazzioBase + "/user/v5/llm/anthropic/v1/messages"
	ingrazzioOpenAIChatURL   = ingrazzioBase + "/user/v5/llm/openai/v1/chat/completions"
	grazieUserAgent          = "ktor-client"
	grazieAgentHeader        = `{"name":"junie:cli","version":"888.195"}`
	anthropicAPIVersion      = "2023-06-01"
	junieTokenFile           = ".cli-proxy-api/junie-account.json"
)

// ---------------------------------------------------------------------------
// Model name mapping (same as executor)
// ---------------------------------------------------------------------------

var modelMapping = map[string]string{
	"gpt4.1": "gpt-4.1-2025-04-14", "gpt4.1-mini": "gpt-4.1-mini-2025-04-14",
	"gpt4.1-nano": "gpt-4.1-nano-2025-04-14", "gpt-5": "gpt-5-2025-08-07",
	"gpt-5-mini": "gpt-5-mini-2025-08-07", "gpt-5-nano": "gpt-5-nano-2025-08-07",
	"gpt-5.1": "gpt-5.1-2025-11-13", "gpt-5.2": "gpt-5.2-2025-12-11",
	"gpt-5.4": "gpt-5.4-2026-03-05", "o1": "o1-2024-12-17",
	"o3": "o3-2025-04-16", "o3-mini": "o3-mini-2025-01-31",
	"o4-mini": "o4-mini-2025-04-16", "gpt-4o": "gpt-4o-2024-11-20",
	"gpt-4o-mini": "gpt-4o-mini-2024-07-18",
	"claude-4-sonnet": "claude-sonnet-4-20250514", "claude-4.1-opus": "claude-opus-4-1-20250805",
	"claude-4.5-sonnet": "claude-sonnet-4-5-20250929", "claude-4.5-haiku": "claude-haiku-4-5-20251001",
	"claude-4.5-opus": "claude-opus-4-5-20251101", "claude-4.6-sonnet": "claude-sonnet-4-6",
	"claude-4.6-opus": "claude-opus-4-6",
}

func mapModel(name string) string {
	if m, ok := modelMapping[name]; ok {
		return m
	}
	return name
}

func isClaudeModel(model string) bool {
	return strings.HasPrefix(strings.ToLower(model), "claude")
}

// ---------------------------------------------------------------------------
// Handler
// ---------------------------------------------------------------------------

type JunieMessagesHandler struct{}

func NewJunieMessagesHandler() *JunieMessagesHandler { return &JunieMessagesHandler{} }

func loadJunieToken() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot determine home dir: %w", err)
	}
	data, err := os.ReadFile(home + "/" + junieTokenFile)
	if err != nil {
		return "", fmt.Errorf("cannot read junie token file: %w", err)
	}
	token := gjson.GetBytes(data, "access_token").String()
	if token == "" {
		return "", fmt.Errorf("no access_token in junie token file")
	}
	return token, nil
}

func authError(c *gin.Context, msg string) {
	c.JSON(http.StatusUnauthorized, gin.H{
		"type": "error", "error": gin.H{"type": "authentication_error", "message": msg},
	})
}

func apiError(c *gin.Context, status int, msg string) {
	c.JSON(status, gin.H{
		"type": "error", "error": gin.H{"type": "api_error", "message": msg},
	})
}

// Messages handles POST /junie/v1/messages.
// Routes Claude models to Ingrazzio Anthropic (pass-through).
// Routes GPT/Grok/other models to Ingrazzio OpenAI (with Anthropic↔OpenAI translation).
func (h *JunieMessagesHandler) Messages(c *gin.Context) {
	rawJSON, err := c.GetRawData()
	if err != nil {
		apiError(c, http.StatusBadRequest, fmt.Sprintf("Invalid request: %v", err))
		return
	}

	token, err := loadJunieToken()
	if err != nil {
		log.Errorf("junie messages handler: %v", err)
		authError(c, "Junie authentication not configured. Run cli-proxy-api --junie-login first.")
		return
	}

	model := mapModel(gjson.GetBytes(rawJSON, "model").String())
	isStream := gjson.GetBytes(rawJSON, "stream").Bool()

	if isClaudeModel(model) {
		// Claude → pass-through to Ingrazzio Anthropic endpoint
		if isStream {
			h.proxyAnthropicStream(c, rawJSON, token)
		} else {
			h.proxyAnthropic(c, rawJSON, token)
		}
	} else {
		// GPT/Grok/etc → translate Anthropic→OpenAI, forward, translate back
		if isStream {
			h.translateAndProxyOpenAIStream(c, rawJSON, model, token)
		} else {
			h.translateAndProxyOpenAI(c, rawJSON, model, token)
		}
	}
}

// ---------------------------------------------------------------------------
// Anthropic pass-through (Claude models)
// ---------------------------------------------------------------------------

func (h *JunieMessagesHandler) proxyAnthropic(c *gin.Context, body []byte, token string) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Minute)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, ingrazzioAnthropicMsgURL, bytes.NewReader(body))
	if err != nil {
		apiError(c, http.StatusInternalServerError, err.Error())
		return
	}
	setAnthropicHeaders(req, token, c)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		apiError(c, http.StatusBadGateway, err.Error())
		return
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	forwardResponseHeaders(c, resp)
	c.Data(resp.StatusCode, resp.Header.Get("Content-Type"), data)
}

func (h *JunieMessagesHandler) proxyAnthropicStream(c *gin.Context, body []byte, token string) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Minute)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, ingrazzioAnthropicMsgURL, bytes.NewReader(body))
	if err != nil {
		apiError(c, http.StatusInternalServerError, err.Error())
		return
	}
	setAnthropicHeaders(req, token, c)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		apiError(c, http.StatusBadGateway, err.Error())
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		data, _ := io.ReadAll(resp.Body)
		c.Data(resp.StatusCode, resp.Header.Get("Content-Type"), data)
		return
	}

	streamSSE(c, resp)
}

// ---------------------------------------------------------------------------
// OpenAI with translation (GPT/Grok models)
// ---------------------------------------------------------------------------

func (h *JunieMessagesHandler) translateAndProxyOpenAI(c *gin.Context, anthropicBody []byte, model string, token string) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Minute)
	defer cancel()

	// Translate Anthropic request → OpenAI request
	openaiBody := anthropicToOpenAIRequest(anthropicBody, model)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, ingrazzioOpenAIChatURL, bytes.NewReader(openaiBody))
	if err != nil {
		apiError(c, http.StatusInternalServerError, err.Error())
		return
	}
	setOpenAIHeaders(req, token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		apiError(c, http.StatusBadGateway, err.Error())
		return
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// Wrap OpenAI error in Anthropic error format
		errMsg := gjson.GetBytes(data, "error.message").String()
		if errMsg == "" {
			errMsg = string(data)
		}
		c.JSON(resp.StatusCode, gin.H{
			"type": "error", "error": gin.H{"type": "api_error", "message": errMsg},
		})
		return
	}

	// Translate OpenAI response → Anthropic response
	anthropicResp := openAIToAnthropicResponse(data, model)
	c.Data(http.StatusOK, "application/json", anthropicResp)
}

func (h *JunieMessagesHandler) translateAndProxyOpenAIStream(c *gin.Context, anthropicBody []byte, model string, token string) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Minute)
	defer cancel()

	// Translate Anthropic request → OpenAI request (with stream=true)
	openaiBody := anthropicToOpenAIRequest(anthropicBody, model)
	openaiBody, _ = sjson.SetBytes(openaiBody, "stream", true)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, ingrazzioOpenAIChatURL, bytes.NewReader(openaiBody))
	if err != nil {
		apiError(c, http.StatusInternalServerError, err.Error())
		return
	}
	setOpenAIHeaders(req, token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		apiError(c, http.StatusBadGateway, err.Error())
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		data, _ := io.ReadAll(resp.Body)
		errMsg := gjson.GetBytes(data, "error.message").String()
		if errMsg == "" {
			errMsg = string(data)
		}
		c.JSON(resp.StatusCode, gin.H{
			"type": "error", "error": gin.H{"type": "api_error", "message": errMsg},
		})
		return
	}

	// Stream: translate each OpenAI SSE chunk → Anthropic SSE chunk
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Status(http.StatusOK)

	flusher, _ := c.Writer.(http.Flusher)

	// Send Anthropic message_start event
	msgID := fmt.Sprintf("msg_%d", time.Now().UnixNano())
	fmt.Fprintf(c.Writer, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":%q,\"type\":\"message\",\"role\":\"assistant\",\"content\":[],\"model\":%q,\"stop_reason\":null,\"stop_sequence\":null,\"usage\":{\"input_tokens\":0,\"output_tokens\":0}}}\n\n", msgID, model)
	if flusher != nil {
		flusher.Flush()
	}

	// Send content_block_start
	fmt.Fprintf(c.Writer, "event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n")
	if flusher != nil {
		flusher.Flush()
	}

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(nil, 52_428_800)
	var totalContent string
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		payload := line[6:]
		if payload == "[DONE]" {
			break
		}

		// Extract content delta from OpenAI chunk
		delta := gjson.Get(payload, "choices.0.delta.content").String()
		if delta != "" {
			totalContent += delta
			// Emit Anthropic content_block_delta
			escaped, _ := sjson.Set("{}", "x", delta)
			deltaJSON := gjson.Get(escaped, "x").Raw // properly escaped string
			fmt.Fprintf(c.Writer, "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":%s}}\n\n", deltaJSON)
			if flusher != nil {
				flusher.Flush()
			}
		}

		// Check for finish
		finishReason := gjson.Get(payload, "choices.0.finish_reason").String()
		if finishReason != "" && finishReason != "null" {
			break
		}
	}

	// Send content_block_stop
	fmt.Fprintf(c.Writer, "event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n")
	// Send message_delta with stop_reason
	fmt.Fprintf(c.Writer, "event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\",\"stop_sequence\":null},\"usage\":{\"output_tokens\":0}}\n\n")
	// Send message_stop
	fmt.Fprintf(c.Writer, "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
	if flusher != nil {
		flusher.Flush()
	}
}

// ---------------------------------------------------------------------------
// Anthropic ↔ OpenAI format translation
// ---------------------------------------------------------------------------

// anthropicToOpenAIRequest converts an Anthropic Messages request to OpenAI chat completions format.
func anthropicToOpenAIRequest(body []byte, model string) []byte {
	var messages []map[string]any

	// Convert system prompt
	sys := gjson.GetBytes(body, "system")
	if sys.Exists() && sys.String() != "" {
		messages = append(messages, map[string]any{"role": "system", "content": sys.String()})
	}

	// Convert messages
	gjson.GetBytes(body, "messages").ForEach(func(_, msg gjson.Result) bool {
		role := msg.Get("role").String()
		content := msg.Get("content")

		if content.Type == gjson.String {
			messages = append(messages, map[string]any{"role": role, "content": content.String()})
		} else if content.IsArray() {
			// Anthropic content blocks → flatten text parts
			var text string
			content.ForEach(func(_, block gjson.Result) bool {
				if block.Get("type").String() == "text" {
					text += block.Get("text").String()
				}
				return true
			})
			if text != "" {
				messages = append(messages, map[string]any{"role": role, "content": text})
			}
		}
		return true
	})

	result := map[string]any{
		"model":    model,
		"messages": messages,
	}

	// Map max_tokens → max_completion_tokens for newer models
	if mt := gjson.GetBytes(body, "max_tokens"); mt.Exists() {
		result["max_completion_tokens"] = mt.Int()
	}
	if temp := gjson.GetBytes(body, "temperature"); temp.Exists() {
		result["temperature"] = temp.Float()
	}
	if topP := gjson.GetBytes(body, "top_p"); topP.Exists() {
		result["top_p"] = topP.Float()
	}

	// Convert tools if present
	tools := gjson.GetBytes(body, "tools")
	if tools.Exists() && tools.IsArray() {
		var openaiTools []map[string]any
		tools.ForEach(func(_, tool gjson.Result) bool {
			openaiTools = append(openaiTools, map[string]any{
				"type": "function",
				"function": map[string]any{
					"name":        tool.Get("name").String(),
					"description": tool.Get("description").String(),
					"parameters":  tool.Get("input_schema").Value(),
				},
			})
			return true
		})
		result["tools"] = openaiTools
	}

	out, _ := json.MarshalIndent(result, "", "  ")
	return out
}

// openAIToAnthropicResponse converts an OpenAI chat completion response to Anthropic Messages format.
func openAIToAnthropicResponse(body []byte, model string) []byte {
	id := gjson.GetBytes(body, "id").String()
	content := gjson.GetBytes(body, "choices.0.message.content").String()
	finishReason := gjson.GetBytes(body, "choices.0.finish_reason").String()
	inputTokens := gjson.GetBytes(body, "usage.prompt_tokens").Int()
	outputTokens := gjson.GetBytes(body, "usage.completion_tokens").Int()

	stopReason := "end_turn"
	switch finishReason {
	case "stop":
		stopReason = "end_turn"
	case "length":
		stopReason = "max_tokens"
	case "tool_calls":
		stopReason = "tool_use"
	}

	// Build content blocks
	var contentBlocks []map[string]any

	// Check for tool calls
	toolCalls := gjson.GetBytes(body, "choices.0.message.tool_calls")
	if toolCalls.Exists() && toolCalls.IsArray() {
		if content != "" {
			contentBlocks = append(contentBlocks, map[string]any{"type": "text", "text": content})
		}
		toolCalls.ForEach(func(_, tc gjson.Result) bool {
			contentBlocks = append(contentBlocks, map[string]any{
				"type":  "tool_use",
				"id":    tc.Get("id").String(),
				"name":  tc.Get("function.name").String(),
				"input": json.RawMessage(tc.Get("function.arguments").String()),
			})
			return true
		})
	} else {
		contentBlocks = append(contentBlocks, map[string]any{"type": "text", "text": content})
	}

	resp := map[string]any{
		"id":            id,
		"type":          "message",
		"role":          "assistant",
		"model":         model,
		"content":       contentBlocks,
		"stop_reason":   stopReason,
		"stop_sequence": nil,
		"usage": map[string]any{
			"input_tokens":  inputTokens,
			"output_tokens": outputTokens,
		},
	}

	out, _ := json.Marshal(resp)
	return out
}

// ---------------------------------------------------------------------------
// Models endpoint
// ---------------------------------------------------------------------------

func (h *JunieMessagesHandler) Models(c *gin.Context) {
	models := []map[string]any{
		// Claude
		modelEntry("claude-sonnet-4-6", "Claude Sonnet 4.6 (JetBrains)"),
		modelEntry("claude-opus-4-6", "Claude Opus 4.6 (JetBrains)"),
		modelEntry("claude-sonnet-4-5-20250929", "Claude Sonnet 4.5 (JetBrains)"),
		modelEntry("claude-haiku-4-5-20251001", "Claude Haiku 4.5 (JetBrains)"),
		// GPT
		modelEntry("gpt4.1", "GPT-4.1 (JetBrains)"),
		modelEntry("gpt-5", "GPT-5 (JetBrains)"),
		modelEntry("gpt-5.4", "GPT-5.4 (JetBrains)"),
		modelEntry("o3", "O3 (JetBrains)"),
		modelEntry("o4-mini", "O4 Mini (JetBrains)"),
		// Grok
		modelEntry("grok-4", "Grok 4 (JetBrains)"),
		modelEntry("grok-4-fast", "Grok 4 Fast (JetBrains)"),
	}
	c.JSON(http.StatusOK, gin.H{"data": models})
}

func modelEntry(id, name string) map[string]any {
	return map[string]any{"id": id, "display_name": name, "type": "model", "created_at": "2025-01-01T00:00:00Z"}
}

// ---------------------------------------------------------------------------
// Headers
// ---------------------------------------------------------------------------

func setAnthropicHeaders(req *http.Request, token string, c *gin.Context) {
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("User-Agent", grazieUserAgent)
	req.Header.Set("Grazie-Agent", grazieAgentHeader)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("anthropic-version", anthropicAPIVersion)
	if v := c.GetHeader("anthropic-version"); v != "" {
		req.Header.Set("anthropic-version", v)
	}
	if v := c.GetHeader("anthropic-beta"); v != "" {
		req.Header.Set("anthropic-beta", v)
	}
}

func setOpenAIHeaders(req *http.Request, token string) {
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("User-Agent", grazieUserAgent)
	req.Header.Set("Grazie-Agent", grazieAgentHeader)
	req.Header.Set("Content-Type", "application/json")
}

func forwardResponseHeaders(c *gin.Context, resp *http.Response) {
	for _, hdr := range []string{"Content-Type", "X-Request-Id", "Request-Id"} {
		if v := resp.Header.Get(hdr); v != "" {
			c.Header(hdr, v)
		}
	}
}

func streamSSE(c *gin.Context, resp *http.Response) {
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Status(resp.StatusCode)
	flusher, _ := c.Writer.(http.Flusher)
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(nil, 52_428_800)
	for scanner.Scan() {
		fmt.Fprintln(c.Writer, scanner.Text())
		if flusher != nil {
			flusher.Flush()
		}
	}
}


