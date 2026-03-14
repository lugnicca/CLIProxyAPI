package chat_completions

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// junieToolCallState tracks state for a single in-progress tool call during streaming.
type junieToolCallState struct {
	Index     int
	ID        string
	Name      string
	Arguments string
}

// junieStreamParams holds state that persists across streaming call invocations.
type junieStreamParams struct {
	// ChatID is used as the "id" field in OpenAI SSE chunks.
	ChatID string
	// AccumulatedContent holds all text for non-stream usage.
	AccumulatedContent string
	// UnixTimestamp is captured once for the lifetime of the stream.
	UnixTimestamp int64
	// ToolCalls accumulates tool calls (for non-streaming mode).
	ToolCalls []junieToolCallState
	// CurrentToolCall is the in-progress tool call during streaming.
	CurrentToolCall *junieToolCallState
	// ToolCallCount tracks how many tool calls have been started (for indexing).
	ToolCallCount int
	// HadToolCalls indicates at least one tool call was seen in this stream.
	HadToolCalls bool
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

// buildToolCallBeginChunk constructs an OpenAI SSE chunk for a tool call begin event.
func buildToolCallBeginChunk(p *junieStreamParams, modelName, toolCallID, name string, index int) string {
	base := `{"id":"","object":"chat.completion.chunk","created":0,"model":"","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"","type":"function","function":{"name":"","arguments":""}}]},"finish_reason":null}]}`
	out, _ := sjson.Set(base, "id", p.ChatID)
	out, _ = sjson.Set(out, "created", p.UnixTimestamp)
	out, _ = sjson.Set(out, "model", modelName)
	out, _ = sjson.Set(out, "choices.0.delta.tool_calls.0.index", index)
	out, _ = sjson.Set(out, "choices.0.delta.tool_calls.0.id", toolCallID)
	out, _ = sjson.Set(out, "choices.0.delta.tool_calls.0.function.name", name)
	return out
}

// buildToolCallArgumentsDeltaChunk constructs an OpenAI SSE chunk for streaming tool call arguments.
func buildToolCallArgumentsDeltaChunk(p *junieStreamParams, modelName, args string, index int) string {
	base := `{"id":"","object":"chat.completion.chunk","created":0,"model":"","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":""}}]},"finish_reason":null}]}`
	out, _ := sjson.Set(base, "id", p.ChatID)
	out, _ = sjson.Set(out, "created", p.UnixTimestamp)
	out, _ = sjson.Set(out, "model", modelName)
	out, _ = sjson.Set(out, "choices.0.delta.tool_calls.0.index", index)
	out, _ = sjson.Set(out, "choices.0.delta.tool_calls.0.function.arguments", args)
	return out
}

// buildStopChunk constructs the final OpenAI SSE chunk signalling finish_reason=stop or tool_calls.
func buildStopChunk(p *junieStreamParams, modelName string) string {
	finishReason := "stop"
	if p.HadToolCalls {
		finishReason = "tool_calls"
	}
	base := `{"id":"","object":"chat.completion.chunk","created":0,"model":"","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`
	out, _ := sjson.Set(base, "id", p.ChatID)
	out, _ = sjson.Set(out, "created", p.UnixTimestamp)
	out, _ = sjson.Set(out, "model", modelName)
	out, _ = sjson.Set(out, "choices.0.finish_reason", finishReason)
	return out
}

// buildNonStreamResponse assembles a full OpenAI Chat Completions response from accumulated data.
func buildNonStreamResponse(p *junieStreamParams, modelName string) string {
	if p.HadToolCalls && len(p.ToolCalls) > 0 {
		// Build response with tool_calls in message
		type openAIFunction struct {
			Name      string `json:"name"`
			Arguments string `json:"arguments"`
		}
		type openAIToolCall struct {
			ID       string         `json:"id"`
			Type     string         `json:"type"`
			Function openAIFunction `json:"function"`
		}
		type openAIMessage struct {
			Role      string           `json:"role"`
			Content   string           `json:"content"`
			ToolCalls []openAIToolCall `json:"tool_calls"`
		}
		type openAIChoice struct {
			Index        int           `json:"index"`
			Message      openAIMessage `json:"message"`
			FinishReason string        `json:"finish_reason"`
		}
		type openAIResponse struct {
			ID      string         `json:"id"`
			Object  string         `json:"object"`
			Created int64          `json:"created"`
			Model   string         `json:"model"`
			Choices []openAIChoice `json:"choices"`
		}

		toolCalls := make([]openAIToolCall, len(p.ToolCalls))
		for i, tc := range p.ToolCalls {
			toolCalls[i] = openAIToolCall{
				ID:   tc.ID,
				Type: "function",
				Function: openAIFunction{
					Name:      tc.Name,
					Arguments: tc.Arguments,
				},
			}
		}

		resp := openAIResponse{
			ID:      p.ChatID,
			Object:  "chat.completion",
			Created: p.UnixTimestamp,
			Model:   modelName,
			Choices: []openAIChoice{
				{
					Index: 0,
					Message: openAIMessage{
						Role:      "assistant",
						Content:   p.AccumulatedContent,
						ToolCalls: toolCalls,
					},
					FinishReason: "tool_calls",
				},
			},
		}
		b, err := json.Marshal(resp)
		if err != nil {
			return ""
		}
		return string(b)
	}

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
// Note: This function is kept for reference but is no longer registered as the active
// translator for the junie format. The JunieExecutor now uses Ingrazzio's native
// OpenAI pass-through endpoint, so no translation is needed.
//
// Grazie streams JSON objects with a "type" discriminator:
//   - type=Content: carries {"text": "..."} — emit an OpenAI content delta chunk.
//   - type=ToolCallBegin: carries tool call id and name — emit an OpenAI tool call begin chunk.
//   - type=ToolCallArgumentsDelta: carries partial arguments — emit an OpenAI tool call delta chunk.
//   - type=ToolCallEnd: signals end of a tool call — no chunk emitted.
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
		// Grazie uses "text" field for content
		text := gjson.GetBytes(payload, "text").String()
		chunk := buildContentChunk(p, modelName, text)
		return []string{"data: " + chunk}

	case "ToolCallBegin":
		toolCallID := gjson.GetBytes(payload, "tool_call_id").String()
		name := gjson.GetBytes(payload, "name").String()
		index := p.ToolCallCount
		p.ToolCallCount++
		p.HadToolCalls = true
		p.CurrentToolCall = &junieToolCallState{
			Index: index,
			ID:    toolCallID,
			Name:  name,
		}
		chunk := buildToolCallBeginChunk(p, modelName, toolCallID, name, index)
		return []string{"data: " + chunk}

	case "ToolCallArgumentsDelta":
		content := gjson.GetBytes(payload, "content").String()
		index := 0
		if p.CurrentToolCall != nil {
			index = p.CurrentToolCall.Index
			p.CurrentToolCall.Arguments += content
		}
		chunk := buildToolCallArgumentsDeltaChunk(p, modelName, content, index)
		return []string{"data: " + chunk}

	case "ToolCallEnd":
		// Finalize the current tool call
		if p.CurrentToolCall != nil {
			p.ToolCalls = append(p.ToolCalls, *p.CurrentToolCall)
			p.CurrentToolCall = nil
		}
		// No chunk emitted for ToolCallEnd itself
		return nil

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
// Note: This function is kept for reference but is no longer registered as the active
// translator for the junie format. The JunieExecutor now uses Ingrazzio's native
// OpenAI pass-through endpoint, so no translation is needed.
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
		// Grazie uses "text" field for content
		p.AccumulatedContent += gjson.GetBytes(payload, "text").String()
		return ""

	case "ToolCallBegin":
		toolCallID := gjson.GetBytes(payload, "tool_call_id").String()
		name := gjson.GetBytes(payload, "name").String()
		p.HadToolCalls = true
		p.CurrentToolCall = &junieToolCallState{
			Index: p.ToolCallCount,
			ID:    toolCallID,
			Name:  name,
		}
		p.ToolCallCount++
		return ""

	case "ToolCallArgumentsDelta":
		content := gjson.GetBytes(payload, "content").String()
		if p.CurrentToolCall != nil {
			p.CurrentToolCall.Arguments += content
		}
		return ""

	case "ToolCallEnd":
		if p.CurrentToolCall != nil {
			p.ToolCalls = append(p.ToolCalls, *p.CurrentToolCall)
			p.CurrentToolCall = nil
		}
		return ""

	case "QuotaMetadata":
		return buildNonStreamResponse(p, modelName)

	default:
		return ""
	}
}
