package chat_completions

import (
	"encoding/json"
	"testing"

	"github.com/tidwall/gjson"
)

// buildOpenAIRequest is a helper that constructs a minimal OpenAI chat request body as JSON bytes.
func buildOpenAIRequest(model string, messages []map[string]any) []byte {
	body := map[string]any{
		"model":    model,
		"messages": messages,
	}
	b, _ := json.Marshal(body)
	return b
}

func TestBasicUserMessage(t *testing.T) {
	input := buildOpenAIRequest("claude-4-sonnet", []map[string]any{
		{"role": "user", "content": "hello"},
	})

	out := ConvertOpenAIRequestToJunie("claude-4-sonnet", input, false)

	if prompt := gjson.GetBytes(out, "prompt").String(); prompt != "ij.chat.request.new-chat" {
		t.Errorf("expected prompt=ij.chat.request.new-chat, got %q", prompt)
	}

	if profile := gjson.GetBytes(out, "profile").String(); profile != "anthropic-claude-4-sonnet" {
		t.Errorf("expected profile=anthropic-claude-4-sonnet, got %q", profile)
	}

	msgs := gjson.GetBytes(out, "chat.messages").Array()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}

	if msgType := msgs[0].Get("type").String(); msgType != "user_message" {
		t.Errorf("expected type=user_message, got %q", msgType)
	}

	if content := msgs[0].Get("content").String(); content != "hello" {
		t.Errorf("expected content=hello, got %q", content)
	}
}

func TestSystemMessage(t *testing.T) {
	input := buildOpenAIRequest("gpt-4o", []map[string]any{
		{"role": "system", "content": "You are a helpful assistant."},
	})

	out := ConvertOpenAIRequestToJunie("gpt-4o", input, false)

	msgs := gjson.GetBytes(out, "chat.messages").Array()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}

	if msgType := msgs[0].Get("type").String(); msgType != "system_message" {
		t.Errorf("expected type=system_message, got %q", msgType)
	}

	if content := msgs[0].Get("content").String(); content != "You are a helpful assistant." {
		t.Errorf("unexpected content: %q", content)
	}
}

func TestAssistantMessage(t *testing.T) {
	input := buildOpenAIRequest("gpt-4o", []map[string]any{
		{"role": "assistant", "content": "I can help with that."},
	})

	out := ConvertOpenAIRequestToJunie("gpt-4o", input, false)

	msgs := gjson.GetBytes(out, "chat.messages").Array()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}

	if msgType := msgs[0].Get("type").String(); msgType != "assistant_message" {
		t.Errorf("expected type=assistant_message, got %q", msgType)
	}

	if content := msgs[0].Get("content").String(); content != "I can help with that." {
		t.Errorf("unexpected content: %q", content)
	}
}

func TestMultipleMessages(t *testing.T) {
	input := buildOpenAIRequest("claude-4-sonnet", []map[string]any{
		{"role": "system", "content": "Be concise."},
		{"role": "user", "content": "What is 2+2?"},
		{"role": "assistant", "content": "4."},
		{"role": "user", "content": "Are you sure?"},
	})

	out := ConvertOpenAIRequestToJunie("claude-4-sonnet", input, false)

	msgs := gjson.GetBytes(out, "chat.messages").Array()
	if len(msgs) != 4 {
		t.Fatalf("expected 4 messages, got %d", len(msgs))
	}

	expected := []struct {
		typ     string
		content string
	}{
		{"system_message", "Be concise."},
		{"user_message", "What is 2+2?"},
		{"assistant_message", "4."},
		{"user_message", "Are you sure?"},
	}

	for i, exp := range expected {
		if got := msgs[i].Get("type").String(); got != exp.typ {
			t.Errorf("msg[%d]: expected type=%q, got %q", i, exp.typ, got)
		}
		if got := msgs[i].Get("content").String(); got != exp.content {
			t.Errorf("msg[%d]: expected content=%q, got %q", i, exp.content, got)
		}
	}
}

func TestModelMapping(t *testing.T) {
	cases := []struct {
		model           string
		expectedProfile string
	}{
		{"gpt-4o", "openai-gpt-4o"},
		{"claude-4-sonnet", "anthropic-claude-4-sonnet"},
		{"gemini-pro-2.5", "google-chat-gemini-pro-2.5"},
		{"claude-4.5-haiku", "anthropic-claude-4-5-haiku"},
		{"claude-4.6-sonnet", "anthropic-claude-4-6-sonnet"},
		{"gemini-flash-2.0", "google-chat-gemini-flash-2.0"},
		{"o1", "openai-o1"},
		{"o3-mini", "openai-o3-mini"},
	}

	for _, tc := range cases {
		input := buildOpenAIRequest(tc.model, []map[string]any{
			{"role": "user", "content": "hi"},
		})
		out := ConvertOpenAIRequestToJunie(tc.model, input, false)
		profile := gjson.GetBytes(out, "profile").String()
		if profile != tc.expectedProfile {
			t.Errorf("model %q: expected profile=%q, got %q", tc.model, tc.expectedProfile, profile)
		}
	}
}

func TestUnknownModel(t *testing.T) {
	unknownModel := "my-custom-model-v99"
	input := buildOpenAIRequest(unknownModel, []map[string]any{
		{"role": "user", "content": "test"},
	})

	out := ConvertOpenAIRequestToJunie(unknownModel, input, false)

	profile := gjson.GetBytes(out, "profile").String()
	if profile != unknownModel {
		t.Errorf("expected unknown model to fall back to model name as profile, got %q", profile)
	}
}

func TestNoMessages(t *testing.T) {
	// Input with no messages array — should produce empty chat.messages.
	input := []byte(`{"model":"gpt-4o"}`)
	out := ConvertOpenAIRequestToJunie("gpt-4o", input, false)

	msgs := gjson.GetBytes(out, "chat.messages").Array()
	if len(msgs) != 0 {
		t.Errorf("expected empty messages array, got %d", len(msgs))
	}
}

func TestContentArrayFlattened(t *testing.T) {
	// OpenAI content as array of parts — text parts should be concatenated.
	body := map[string]any{
		"model": "gpt-4o",
		"messages": []map[string]any{
			{
				"role": "user",
				"content": []map[string]any{
					{"type": "text", "text": "Hello, "},
					{"type": "text", "text": "world!"},
					{"type": "image_url", "image_url": map[string]any{"url": "http://example.com/img.png"}},
				},
			},
		},
	}
	input, _ := json.Marshal(body)
	out := ConvertOpenAIRequestToJunie("gpt-4o", input, false)

	msgs := gjson.GetBytes(out, "chat.messages").Array()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}

	content := msgs[0].Get("content").String()
	if content != "Hello, world!" {
		t.Errorf("expected flattened text content=%q, got %q", "Hello, world!", content)
	}
}

// buildOpenAIRequestWithTools constructs an OpenAI chat request body with tools and tool_choice.
func buildOpenAIRequestWithTools(model string, messages []map[string]any, tools []map[string]any, toolChoice any) []byte {
	body := map[string]any{
		"model":    model,
		"messages": messages,
	}
	if tools != nil {
		body["tools"] = tools
	}
	if toolChoice != nil {
		body["tool_choice"] = toolChoice
	}
	b, _ := json.Marshal(body)
	return b
}

func TestToolsPassedToGrazie(t *testing.T) {
	// OpenAI tools array should be unwrapped from the "function" wrapper and placed in chat.tools.
	tools := []map[string]any{
		{
			"type": "function",
			"function": map[string]any{
				"name":        "get_weather",
				"description": "Get weather for a location",
				"parameters": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"location": map[string]any{"type": "string"},
					},
					"required": []string{"location"},
				},
			},
		},
	}
	input := buildOpenAIRequestWithTools("claude-4-sonnet", []map[string]any{
		{"role": "user", "content": "What's the weather?"},
	}, tools, nil)

	out := ConvertOpenAIRequestToJunie("claude-4-sonnet", input, false)

	grazieTools := gjson.GetBytes(out, "chat.tools").Array()
	if len(grazieTools) != 1 {
		t.Fatalf("expected 1 tool in chat.tools, got %d", len(grazieTools))
	}

	tool := grazieTools[0]
	if name := tool.Get("name").String(); name != "get_weather" {
		t.Errorf("expected tool name=get_weather, got %q", name)
	}
	if desc := tool.Get("description").String(); desc != "Get weather for a location" {
		t.Errorf("expected tool description, got %q", desc)
	}
	// The "function" wrapper should NOT be present.
	if tool.Get("function").Exists() {
		t.Error("expected no 'function' wrapper in Grazie tool format")
	}
	// "type" field (from OpenAI) should NOT be carried over.
	if tool.Get("type").Exists() {
		t.Error("expected no 'type' field in Grazie tool format")
	}
	// parameters should be present.
	if !tool.Get("parameters").Exists() {
		t.Error("expected parameters to be present in Grazie tool")
	}
	if propType := tool.Get("parameters.properties.location.type").String(); propType != "string" {
		t.Errorf("expected location parameter type=string, got %q", propType)
	}
}

func TestToolChoicePassedToGrazie(t *testing.T) {
	tools := []map[string]any{
		{
			"type": "function",
			"function": map[string]any{
				"name":        "my_tool",
				"description": "A test tool",
				"parameters":  map[string]any{"type": "object", "properties": map[string]any{}},
			},
		},
	}
	input := buildOpenAIRequestWithTools("gpt-4o", []map[string]any{
		{"role": "user", "content": "do something"},
	}, tools, "auto")

	out := ConvertOpenAIRequestToJunie("gpt-4o", input, false)

	if toolChoice := gjson.GetBytes(out, "chat.tool_choice").String(); toolChoice != "auto" {
		t.Errorf("expected chat.tool_choice=auto, got %q", toolChoice)
	}
}

func TestToolResultMessage(t *testing.T) {
	// A "tool" role message should be converted to Grazie tool_result format.
	input := buildOpenAIRequest("gpt-4o", []map[string]any{
		{
			"role":         "tool",
			"tool_call_id": "call_abc123",
			"content":      "The weather is sunny and 25°C",
		},
	})

	out := ConvertOpenAIRequestToJunie("gpt-4o", input, false)

	msgs := gjson.GetBytes(out, "chat.messages").Array()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}

	msg := msgs[0]
	if msgType := msg.Get("type").String(); msgType != "tool_result" {
		t.Errorf("expected type=tool_result, got %q", msgType)
	}
	if id := msg.Get("tool_call_id").String(); id != "call_abc123" {
		t.Errorf("expected tool_call_id=call_abc123, got %q", id)
	}
	if content := msg.Get("content").String(); content != "The weather is sunny and 25°C" {
		t.Errorf("expected content to be the tool result text, got %q", content)
	}
}

func TestAssistantToolCallMessage(t *testing.T) {
	// An assistant message with tool_calls should be converted to Grazie format
	// with a tool_calls array (not just raw JSON string).
	input := buildOpenAIRequest("gpt-4o", []map[string]any{
		{
			"role":    "assistant",
			"content": "",
			"tool_calls": []map[string]any{
				{
					"id":   "call_xyz",
					"type": "function",
					"function": map[string]any{
						"name":      "get_weather",
						"arguments": `{"location":"Paris"}`,
					},
				},
			},
		},
	})

	out := ConvertOpenAIRequestToJunie("gpt-4o", input, false)

	msgs := gjson.GetBytes(out, "chat.messages").Array()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}

	msg := msgs[0]
	if msgType := msg.Get("type").String(); msgType != "assistant_message" {
		t.Errorf("expected type=assistant_message, got %q", msgType)
	}

	// tool_calls should be a proper array, not a raw JSON string embedded in content.
	toolCalls := msg.Get("tool_calls").Array()
	if len(toolCalls) != 1 {
		t.Fatalf("expected 1 tool_call in Grazie assistant_message, got %d", len(toolCalls))
	}

	tc := toolCalls[0]
	if id := tc.Get("id").String(); id != "call_xyz" {
		t.Errorf("expected tool_call id=call_xyz, got %q", id)
	}
	if name := tc.Get("name").String(); name != "get_weather" {
		t.Errorf("expected tool_call name=get_weather, got %q", name)
	}
	if args := tc.Get("arguments").String(); args != `{"location":"Paris"}` {
		t.Errorf("expected tool_call arguments=%q, got %q", `{"location":"Paris"}`, args)
	}
}

func TestMixedConversationWithTools(t *testing.T) {
	// Full conversation: system, user, assistant with tool_calls, tool result.
	tools := []map[string]any{
		{
			"type": "function",
			"function": map[string]any{
				"name":        "get_weather",
				"description": "Get weather",
				"parameters":  map[string]any{"type": "object", "properties": map[string]any{}},
			},
		},
	}
	messages := []map[string]any{
		{"role": "system", "content": "You are helpful."},
		{"role": "user", "content": "What's the weather in Paris?"},
		{
			"role":    "assistant",
			"content": "",
			"tool_calls": []map[string]any{
				{
					"id":   "call_1",
					"type": "function",
					"function": map[string]any{
						"name":      "get_weather",
						"arguments": `{"location":"Paris"}`,
					},
				},
			},
		},
		{
			"role":         "tool",
			"tool_call_id": "call_1",
			"content":      "Sunny, 22°C",
		},
		{"role": "user", "content": "Thanks!"},
	}
	input := buildOpenAIRequestWithTools("claude-4-sonnet", messages, tools, "auto")

	out := ConvertOpenAIRequestToJunie("claude-4-sonnet", input, false)

	msgs := gjson.GetBytes(out, "chat.messages").Array()
	if len(msgs) != 5 {
		t.Fatalf("expected 5 messages, got %d", len(msgs))
	}

	// [0] system
	if msgs[0].Get("type").String() != "system_message" {
		t.Errorf("msg[0]: expected system_message, got %q", msgs[0].Get("type").String())
	}
	// [1] user
	if msgs[1].Get("type").String() != "user_message" {
		t.Errorf("msg[1]: expected user_message, got %q", msgs[1].Get("type").String())
	}
	// [2] assistant with tool_calls
	if msgs[2].Get("type").String() != "assistant_message" {
		t.Errorf("msg[2]: expected assistant_message, got %q", msgs[2].Get("type").String())
	}
	if len(msgs[2].Get("tool_calls").Array()) != 1 {
		t.Errorf("msg[2]: expected 1 tool_call, got %d", len(msgs[2].Get("tool_calls").Array()))
	}
	// [3] tool result
	if msgs[3].Get("type").String() != "tool_result" {
		t.Errorf("msg[3]: expected tool_result, got %q", msgs[3].Get("type").String())
	}
	if msgs[3].Get("tool_call_id").String() != "call_1" {
		t.Errorf("msg[3]: expected tool_call_id=call_1, got %q", msgs[3].Get("tool_call_id").String())
	}
	// [4] user
	if msgs[4].Get("type").String() != "user_message" {
		t.Errorf("msg[4]: expected user_message, got %q", msgs[4].Get("type").String())
	}

	// Tools should be in chat.tools
	grazieTools := gjson.GetBytes(out, "chat.tools").Array()
	if len(grazieTools) != 1 {
		t.Errorf("expected 1 tool in chat.tools, got %d", len(grazieTools))
	}
	// tool_choice should be passed through
	if tc := gjson.GetBytes(out, "chat.tool_choice").String(); tc != "auto" {
		t.Errorf("expected chat.tool_choice=auto, got %q", tc)
	}
}
