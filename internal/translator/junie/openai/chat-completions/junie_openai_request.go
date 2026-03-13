// Package chat_completions provides request translation from OpenAI Chat Completions
// format to JetBrains Grazie (Junie) API format.
package chat_completions

import (
	"encoding/json"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// modelToGrazieProfile maps OpenAI-style model IDs to JetBrains Grazie profile identifiers.
var modelToGrazieProfile = map[string]string{
	"gpt-4o":             "openai-gpt-4o",
	"o1":                 "openai-o1",
	"o3":                 "openai-o3",
	"o3-mini":            "openai-o3-mini",
	"o4-mini":            "openai-o4-mini",
	"gpt4.1":             "openai-gpt4.1",
	"gpt4.1-mini":        "openai-gpt4.1-mini",
	"gpt4.1-nano":        "openai-gpt4.1-nano",
	"gemini-pro-2.5":     "google-chat-gemini-pro-2.5",
	"gemini-flash-2.0":   "google-chat-gemini-flash-2.0",
	"gemini-flash-2.5":   "google-chat-gemini-flash-2.5",
	"claude-3.5-haiku":   "anthropic-claude-3.5-haiku",
	"claude-3.5-sonnet":  "anthropic-claude-3.5-sonnet",
	"claude-3.7-sonnet":  "anthropic-claude-3.7-sonnet",
	"claude-4-sonnet":    "anthropic-claude-4-sonnet",
}

// roleToGrazieType maps OpenAI message roles to Grazie message type strings.
func roleToGrazieType(role string) string {
	switch role {
	case "system":
		return "system_message"
	case "assistant":
		return "assistant_message"
	default:
		// user, tool, function — default to user_message
		return "user_message"
	}
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
//	    ]
//	  }
//	}
//
// Tool calls and function messages are not natively supported by Grazie; they are
// serialised to JSON and embedded as user_message content (known limitation).
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

	type grazieMessage struct {
		Type    string `json:"type"`
		Content string `json:"content"`
	}

	var grazieMessages []grazieMessage

	for _, msg := range messagesResult.Array() {
		role := msg.Get("role").String()

		// Handle tool/function messages — embed as JSON string content.
		if role == "tool" || role == "function" {
			raw := msg.Raw
			grazieMessages = append(grazieMessages, grazieMessage{
				Type:    "user_message",
				Content: raw,
			})
			continue
		}

		// Handle assistant messages that carry tool_calls — embed as JSON string content.
		if role == "assistant" && msg.Get("tool_calls").Exists() {
			raw := msg.Raw
			grazieMessages = append(grazieMessages, grazieMessage{
				Type:    "assistant_message",
				Content: raw,
			})
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

		grazieMessages = append(grazieMessages, grazieMessage{
			Type:    roleToGrazieType(role),
			Content: content,
		})
	}

	// Serialise the messages via encoding/json so that special characters are escaped correctly.
	msgBytes, err := json.Marshal(grazieMessages)
	if err != nil {
		out, _ = sjson.SetBytes(out, "chat.messages", []interface{}{})
		return out
	}

	// sjson.SetRawBytes lets us embed the already-serialised JSON array verbatim.
	out, _ = sjson.SetRawBytes(out, "chat.messages", msgBytes)
	return out
}
