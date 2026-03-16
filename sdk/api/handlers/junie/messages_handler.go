// Package junie provides HTTP handlers for the Junie (JetBrains Ingrazzio) provider.
// It exposes Anthropic Messages-compatible endpoints that forward requests directly
// to the Ingrazzio Anthropic backend, enabling Claude Code to use JetBrains AI
// subscriptions transparently.
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
)

const (
	ingrazzioBase             = "https://ingrazzio-cloud-prod.labs.jb.gg"
	ingrazzioAnthropicMsgURL  = ingrazzioBase + "/user/v5/llm/anthropic/v1/messages"
	grazieUserAgent           = "ktor-client"
	grazieAgentHeader         = `{"name":"junie:cli","version":"888.195"}`
	anthropicAPIVersion       = "2023-06-01"
	junieTokenFile            = ".cli-proxy-api/junie-account.json"
)

// JunieMessagesHandler handles Anthropic Messages-format requests by forwarding
// them directly to the Ingrazzio Anthropic endpoint. This is a lightweight
// reverse-proxy that only adds JetBrains authentication headers.
type JunieMessagesHandler struct{}

func NewJunieMessagesHandler() *JunieMessagesHandler {
	return &JunieMessagesHandler{}
}

// loadJunieToken reads the OAuth access token from the junie-account.json file.
func loadJunieToken() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot determine home dir: %w", err)
	}
	path := home + "/" + junieTokenFile
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("cannot read junie token file %s: %w", path, err)
	}
	token := gjson.GetBytes(data, "access_token").String()
	if token == "" {
		return "", fmt.Errorf("no access_token in %s", path)
	}
	return token, nil
}

// Messages handles POST /junie/v1/messages — Anthropic Messages format.
// It forwards the request as-is to Ingrazzio's Anthropic endpoint.
func (h *JunieMessagesHandler) Messages(c *gin.Context) {
	rawJSON, err := c.GetRawData()
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"type": "error",
			"error": gin.H{
				"type":    "invalid_request_error",
				"message": fmt.Sprintf("Invalid request: %v", err),
			},
		})
		return
	}

	token, err := loadJunieToken()
	if err != nil {
		log.Errorf("junie messages handler: %v", err)
		c.JSON(http.StatusUnauthorized, gin.H{
			"type": "error",
			"error": gin.H{
				"type":    "authentication_error",
				"message": "Junie authentication not configured. Run cliproxyapi --junie-login first.",
			},
		})
		return
	}

	stream := gjson.GetBytes(rawJSON, "stream")
	isStreaming := stream.Exists() && stream.Type != gjson.False

	if isStreaming {
		h.handleStreaming(c, rawJSON, token)
	} else {
		h.handleNonStreaming(c, rawJSON, token)
	}
}

func (h *JunieMessagesHandler) handleNonStreaming(c *gin.Context, body []byte, token string) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Minute)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, ingrazzioAnthropicMsgURL, bytes.NewReader(body))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"type": "error", "error": gin.H{"type": "api_error", "message": err.Error()}})
		return
	}
	setHeaders(req, token)
	// Forward anthropic-version and anthropic-beta from client if present
	if v := c.GetHeader("anthropic-version"); v != "" {
		req.Header.Set("anthropic-version", v)
	}
	if v := c.GetHeader("anthropic-beta"); v != "" {
		req.Header.Set("anthropic-beta", v)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"type": "error", "error": gin.H{"type": "api_error", "message": err.Error()}})
		return
	}
	defer resp.Body.Close()

	data, _ := io.ReadAll(resp.Body)

	// Forward relevant headers
	for _, hdr := range []string{"Content-Type", "X-Request-Id", "Request-Id"} {
		if v := resp.Header.Get(hdr); v != "" {
			c.Header(hdr, v)
		}
	}
	c.Data(resp.StatusCode, resp.Header.Get("Content-Type"), data)
}

func (h *JunieMessagesHandler) handleStreaming(c *gin.Context, body []byte, token string) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Minute)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, ingrazzioAnthropicMsgURL, bytes.NewReader(body))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"type": "error", "error": gin.H{"type": "api_error", "message": err.Error()}})
		return
	}
	setHeaders(req, token)
	if v := c.GetHeader("anthropic-version"); v != "" {
		req.Header.Set("anthropic-version", v)
	}
	if v := c.GetHeader("anthropic-beta"); v != "" {
		req.Header.Set("anthropic-beta", v)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"type": "error", "error": gin.H{"type": "api_error", "message": err.Error()}})
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		data, _ := io.ReadAll(resp.Body)
		c.Data(resp.StatusCode, resp.Header.Get("Content-Type"), data)
		return
	}

	// Stream SSE directly to client
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Status(resp.StatusCode)

	flusher, _ := c.Writer.(http.Flusher)
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(nil, 52_428_800)
	for scanner.Scan() {
		line := scanner.Text()
		fmt.Fprintln(c.Writer, line)
		if flusher != nil {
			flusher.Flush()
		}
	}
}

// Models returns the list of Junie models available via the Anthropic endpoint.
func (h *JunieMessagesHandler) Models(c *gin.Context) {
	// Return Claude models available via Junie in Anthropic format
	models := []map[string]any{
		modelEntry("claude-sonnet-4-6", "Claude Sonnet 4.6 (JetBrains)", 200000),
		modelEntry("claude-opus-4-6", "Claude Opus 4.6 (JetBrains)", 200000),
		modelEntry("claude-sonnet-4-5-20250929", "Claude Sonnet 4.5 (JetBrains)", 200000),
		modelEntry("claude-haiku-4-5-20251001", "Claude Haiku 4.5 (JetBrains)", 200000),
		modelEntry("claude-opus-4-5-20251101", "Claude Opus 4.5 (JetBrains)", 200000),
		modelEntry("claude-opus-4-1-20250805", "Claude Opus 4.1 (JetBrains)", 200000),
		modelEntry("claude-sonnet-4-20250514", "Claude Sonnet 4 (JetBrains)", 200000),
	}
	c.JSON(http.StatusOK, gin.H{"data": models})
}

func modelEntry(id, name string, contextLength int) map[string]any {
	return map[string]any{
		"id":             id,
		"display_name":   name,
		"type":           "model",
		"context_window": contextLength,
		"created_at":     "2025-01-01T00:00:00Z",
	}
}

func setHeaders(req *http.Request, token string) {
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("User-Agent", grazieUserAgent)
	req.Header.Set("Grazie-Agent", grazieAgentHeader)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("anthropic-version", anthropicAPIVersion)
}

// Ensure unused imports don't cause errors
var _ = strings.TrimSpace
var _ json.RawMessage
