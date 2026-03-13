package chat_completions

import (
	"bytes"
	"context"
	"fmt"
	"time"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// junieStreamParams holds state that persists across streaming call invocations.
type junieStreamParams struct {
	// ChatID is used as the "id" field in OpenAI SSE chunks.
	ChatID string
	// AccumulatedContent holds all text for non-stream usage.
	AccumulatedContent string
	// UnixTimestamp is captured once for the lifetime of the stream.
	UnixTimestamp int64
}

// newJunieStreamParams constructs the initial streaming state.
func newJunieStreamParams() *junieStreamParams {
	return &junieStreamParams{
		ChatID:        fmt.Sprintf("chatcmpl-%d", time.Now().UnixNano()),
		UnixTimestamp: time.Now().Unix(),
	}
}

// buildContentChunk constructs a streaming OpenAI SSE chunk for a content delta.
func buildContentChunk(p *junieStreamParams, modelName, text string) string {
	base := `{"id":"","object":"chat.completion.chunk","created":0,"model":"","choices":[{"index":0,"delta":{"content":""},"finish_reason":null}]}`
	out, _ := sjson.Set(base, "id", p.ChatID)
	out, _ = sjson.Set(out, "created", p.UnixTimestamp)
	out, _ = sjson.Set(out, "model", modelName)
	out, _ = sjson.Set(out, "choices.0.delta.content", text)
	return out
}

// buildStopChunk constructs the final OpenAI SSE chunk signalling finish_reason=stop.
func buildStopChunk(p *junieStreamParams, modelName string) string {
	base := `{"id":"","object":"chat.completion.chunk","created":0,"model":"","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`
	out, _ := sjson.Set(base, "id", p.ChatID)
	out, _ = sjson.Set(out, "created", p.UnixTimestamp)
	out, _ = sjson.Set(out, "model", modelName)
	return out
}

// buildNonStreamResponse assembles a full OpenAI Chat Completions response from accumulated text.
func buildNonStreamResponse(p *junieStreamParams, modelName string) string {
	base := `{"id":"","object":"chat.completion","created":0,"model":"","choices":[{"index":0,"message":{"role":"assistant","content":""},"finish_reason":"stop"}]}`
	out, _ := sjson.Set(base, "id", p.ChatID)
	out, _ = sjson.Set(out, "created", p.UnixTimestamp)
	out, _ = sjson.Set(out, "model", modelName)
	out, _ = sjson.Set(out, "choices.0.message.content", p.AccumulatedContent)
	return out
}

// parseGrazieSSELine attempts to extract the JSON payload from a "data: {...}" SSE line.
// Returns nil if the line is not a data line or is the [DONE] sentinel.
func parseGrazieSSELine(rawJSON []byte) []byte {
	line := bytes.TrimSpace(rawJSON)
	if !bytes.HasPrefix(line, []byte("data:")) {
		return nil
	}
	payload := bytes.TrimSpace(line[5:])
	if bytes.Equal(payload, []byte("[DONE]")) {
		return nil
	}
	return payload
}

// ConvertJunieResponseToOpenAI translates a single streaming SSE line from the JetBrains
// Grazie API into one or more OpenAI-format SSE lines.
//
// Grazie streams JSON objects with a "type" discriminator:
//   - type=Content: carries {"text": "..."} — emit an OpenAI content delta chunk.
//   - type=QuotaMetadata: signals end-of-stream — emit a stop chunk then "data: [DONE]".
//
// Any unrecognised event types are silently dropped.
func ConvertJunieResponseToOpenAI(_ context.Context, modelName string, _, _, rawJSON []byte, param *any) []string {
	if *param == nil {
		*param = newJunieStreamParams()
	}
	p := (*param).(*junieStreamParams)

	payload := parseGrazieSSELine(rawJSON)
	if payload == nil {
		return nil
	}

	eventType := gjson.GetBytes(payload, "type").String()

	switch eventType {
	case "Content":
		text := gjson.GetBytes(payload, "text").String()
		chunk := buildContentChunk(p, modelName, text)
		return []string{"data: " + chunk}

	case "QuotaMetadata":
		stopChunk := buildStopChunk(p, modelName)
		return []string{
			"data: " + stopChunk,
			"data: [DONE]",
		}

	default:
		// Unknown / informational event — ignore.
		return nil
	}
}

// ConvertJunieResponseToOpenAINonStream accumulates all streaming Grazie SSE lines and
// returns a single OpenAI Chat Completions non-streaming response once the stream ends.
//
// On every call except the final one (QuotaMetadata) an empty string is returned.
// On the final call the assembled response is returned.
func ConvertJunieResponseToOpenAINonStream(_ context.Context, modelName string, _, _, rawJSON []byte, param *any) string {
	if *param == nil {
		*param = newJunieStreamParams()
	}
	p := (*param).(*junieStreamParams)

	payload := parseGrazieSSELine(rawJSON)
	if payload == nil {
		return ""
	}

	eventType := gjson.GetBytes(payload, "type").String()

	switch eventType {
	case "Content":
		p.AccumulatedContent += gjson.GetBytes(payload, "text").String()
		return ""

	case "QuotaMetadata":
		return buildNonStreamResponse(p, modelName)

	default:
		return ""
	}
}
