// Package chat_completions provides request translation from OpenAI Chat Completions
// format to JetBrains Grazie (Junie) API format.
package chat_completions

import (
	"encoding/json"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// modelToGrazieProfile maps user-facing model IDs to JetBrains Grazie/Ingrazzio profile identifiers.
// Derived from the Ingrazzio API's supported profiles list (March 2026).
var modelToGrazieProfile = map[string]string{
	// OpenAI models
	"gpt-4o":              "openai-gpt-4o",
	"gpt-4o-mini":         "openai-gpt-4o-mini",
	"o1":                  "openai-o1",
	"o3":                  "openai-o3",
	"o3-mini":             "openai-o3-mini",
	"o4-mini":             "openai-o4-mini",
	"gpt4.1":              "openai-gpt4.1",
	"gpt4.1-mini":         "openai-gpt4.1-mini",
	"gpt4.1-nano":         "openai-gpt4.1-nano",
	"gpt-5":               "openai-gpt-5",
	"gpt-5-mini":          "openai-gpt-5-mini",
	"gpt-5-nano":          "openai-gpt-5-nano",
	"gpt-5-codex":         "openai-gpt-5-codex",
	"gpt-5.1":             "openai-gpt-5-1",
	"gpt-5.1-codex":       "openai-gpt-5-1-codex",
	"gpt-5.1-codex-mini":  "openai-gpt-5-1-codex-mini",
	"gpt-5.1-codex-max":   "openai-gpt-5-1-codex-max",
	"gpt-5.2":             "openai-gpt-5-2",
	"gpt-5.2-codex":       "openai-gpt-5-2-codex",
	"gpt-5.3-codex":       "openai-gpt-5-3-codex",
	"gpt-5.4":             "openai-gpt-5-4",
	// Anthropic models
	"claude-4-sonnet":   "anthropic-claude-4-sonnet",
	"claude-4.1-opus":   "anthropic-claude-4.1-opus",
	"claude-4.5-sonnet": "anthropic-claude-4-5-sonnet",
	"claude-4.5-haiku":  "anthropic-claude-4-5-haiku",
	"claude-4.5-opus":   "anthropic-claude-4-5-opus",
	"claude-4.6-opus":   "anthropic-claude-4-6-opus",
	"claude-4.6-sonnet": "anthropic-claude-4-6-sonnet",
	// Google models
	"gemini-flash-2.0":      "google-chat-gemini-flash-2.0",
	"gemini-flash-lite-2.0": "google-chat-gemini-flash-lite-2.0",
	"gemini-pro-2.5":        "google-chat-gemini-pro-2.5",
	"gemini-flash-2.5":      "google-chat-gemini-flash-2.5",
	"gemini-flash-lite-2.5": "google-chat-gemini-flash-lite-2.5",
	"gemini-3.0-pro":        "google-gemini-3-0-pro",
	"gemini-3.0-flash":      "google-gemini-3-0-flash",
	"gemini-3.1-flash-lite": "google-gemini-3-1-flash-lite",
	"gemini-3.1-pro":        "google-gemini-3-1-pro",
	// xAI models
	"grok-4":                      "xai-grok-4",
	"grok-4-fast":                 "xai-grok-4-fast",
	"grok-code-fast-1":            "xai-grok-code-fast-1",
	"grok-4.1-fast":               "xai-grok-4-1-fast",
	"grok-4.1-fast-non-reasoning": "xai-grok-4-1-fast-non-reasoning",
}

// roleToGrazieType maps OpenAI message roles to Grazie message type strings.
func roleToGrazieType(role string) string {
	switch role {
	case "system":
		return "system_message"
	case "assistant":
		return "assistant_message"
	default:
		// user — default to user_message
		return "user_message"
	}
}

// grazieToolCall represents a single tool call in Grazie assistant_message format.
type grazieToolCall struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// grazieAssistantMessage represents an assistant message in Grazie format, optionally
// with tool calls.
type grazieAssistantMessage struct {
	Type      string           `json:"type"`
	Content   string           `json:"content"`
	ToolCalls []grazieToolCall `json:"tool_calls,omitempty"`
}

// grazieToolResultMessage represents a tool result message in Grazie format.
type grazieToolResultMessage struct {
	Type       string `json:"type"`
	ToolCallID string `json:"tool_call_id"`
	Content    string `json:"content"`
}

// grazieMessage is used for regular (non-tool, non-assistant-with-toolcalls) messages.
type grazieMessage struct {
	Type    string `json:"type"`
	Content string `json:"content"`
}

// ConvertOpenAIRequestToJunie converts an OpenAI Chat Completions request body to
// the JetBrains Grazie API format.
//
// Grazie format:
//
//	{
//	  "prompt": "ij.chat.request.new-chat",
//	  "profile": "<grazie-profile>",
//	  "chat": {
//	    "messages": [
//	      {"type": "system_message", "content": "..."},
//	      {"type": "user_message",   "content": "..."}
//	    ],
//	    "tools": [
//	      {"name": "...", "description": "...", "parameters": {...}}
//	    ],
//	    "tool_choice": "auto"
//	  }
//	}
func ConvertOpenAIRequestToJunie(modelName string, inputRawJSON []byte, _ bool) []byte {
	// Resolve Grazie profile from model name; fall back to the model name itself.
	profile, ok := modelToGrazieProfile[modelName]
	if !ok {
		profile = modelName
	}

	// Build skeleton output.
	out := []byte(`{}`)
	out, _ = sjson.SetBytes(out, "prompt", "ij.chat.request.new-chat")
	out, _ = sjson.SetBytes(out, "profile", profile)

	// Convert messages array.
	messagesResult := gjson.GetBytes(inputRawJSON, "messages")
	if !messagesResult.Exists() || !messagesResult.IsArray() {
		out, _ = sjson.SetBytes(out, "chat.messages", []interface{}{})
		return out
	}

	// Use a slice of json.RawMessage so we can mix differently-shaped message structs.
	var grazieMessages []json.RawMessage

	for _, msg := range messagesResult.Array() {
		role := msg.Get("role").String()

		// Handle tool result messages — convert to Grazie tool_result format.
		if role == "tool" || role == "function" {
			toolMsg := grazieToolResultMessage{
				Type:       "tool_result",
				ToolCallID: msg.Get("tool_call_id").String(),
				Content:    msg.Get("content").String(),
			}
			b, err := json.Marshal(toolMsg)
			if err != nil {
				continue
			}
			grazieMessages = append(grazieMessages, json.RawMessage(b))
			continue
		}

		// Handle assistant messages that carry tool_calls — convert to Grazie format
		// with a proper tool_calls array.
		if role == "assistant" && msg.Get("tool_calls").Exists() {
			var toolCalls []grazieToolCall
			for _, tc := range msg.Get("tool_calls").Array() {
				toolCalls = append(toolCalls, grazieToolCall{
					ID:        tc.Get("id").String(),
					Name:      tc.Get("function.name").String(),
					Arguments: tc.Get("function.arguments").String(),
				})
			}
			assistantMsg := grazieAssistantMessage{
				Type:      "assistant_message",
				Content:   msg.Get("content").String(),
				ToolCalls: toolCalls,
			}
			b, err := json.Marshal(assistantMsg)
			if err != nil {
				continue
			}
			grazieMessages = append(grazieMessages, json.RawMessage(b))
			continue
		}

		// Normal text content — may be a string or an array of content parts.
		contentResult := msg.Get("content")
		var content string
		if contentResult.IsArray() {
			// Flatten content parts: collect all "text" type parts.
			for _, part := range contentResult.Array() {
				if part.Get("type").String() == "text" {
					content += part.Get("text").String()
				}
			}
		} else {
			content = contentResult.String()
		}

		plainMsg := grazieMessage{
			Type:    roleToGrazieType(role),
			Content: content,
		}
		b, err := json.Marshal(plainMsg)
		if err != nil {
			continue
		}
		grazieMessages = append(grazieMessages, json.RawMessage(b))
	}

	// Serialise the messages via encoding/json so that special characters are escaped correctly.
	msgBytes, err := json.Marshal(grazieMessages)
	if err != nil {
		out, _ = sjson.SetBytes(out, "chat.messages", []interface{}{})
		return out
	}

	// sjson.SetRawBytes lets us embed the already-serialised JSON array verbatim.
	out, _ = sjson.SetRawBytes(out, "chat.messages", msgBytes)

	// Convert tools array: unwrap the "function" wrapper from each OpenAI tool.
	toolsResult := gjson.GetBytes(inputRawJSON, "tools")
	if toolsResult.Exists() && toolsResult.IsArray() {
		type grazieTool struct {
			Name        string          `json:"name"`
			Description string          `json:"description"`
			Parameters  json.RawMessage `json:"parameters"`
		}
		var grazieTools []grazieTool
		for _, tool := range toolsResult.Array() {
			fn := tool.Get("function")
			if !fn.Exists() {
				continue
			}
			gt := grazieTool{
				Name:        fn.Get("name").String(),
				Description: fn.Get("description").String(),
				Parameters:  json.RawMessage(fn.Get("parameters").Raw),
			}
			grazieTools = append(grazieTools, gt)
		}
		if len(grazieTools) > 0 {
			toolsBytes, err := json.Marshal(grazieTools)
			if err == nil {
				out, _ = sjson.SetRawBytes(out, "chat.tools", toolsBytes)
			}
		}
	}

	// Pass tool_choice through if present.
	toolChoiceResult := gjson.GetBytes(inputRawJSON, "tool_choice")
	if toolChoiceResult.Exists() {
		out, _ = sjson.SetRawBytes(out, "chat.tool_choice", []byte(toolChoiceResult.Raw))
	}

	return out
}
