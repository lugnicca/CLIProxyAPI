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
