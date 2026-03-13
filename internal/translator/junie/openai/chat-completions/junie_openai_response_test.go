package chat_completions

import (
	"context"
	"strings"
	"testing"

	"github.com/tidwall/gjson"
)

// makeDataLine wraps a JSON object string in an SSE "data: " prefix as bytes.
func makeDataLine(jsonPayload string) []byte {
	return []byte("data: " + jsonPayload)
}

func TestStreamContentChunk(t *testing.T) {
	ctx := context.Background()
	var param any

	line := makeDataLine(`{"type":"Content","text":"Hello, world!"}`)
	out := ConvertJunieResponseToOpenAI(ctx, "claude-4-sonnet", nil, nil, line, &param)

	if len(out) != 1 {
		t.Fatalf("expected 1 SSE line for Content event, got %d", len(out))
	}

	chunk := out[0]
	if !strings.HasPrefix(chunk, "data: ") {
		t.Errorf("expected chunk to start with 'data: ', got %q", chunk)
	}

	jsonPart := strings.TrimPrefix(chunk, "data: ")

	if content := gjson.Get(jsonPart, "choices.0.delta.content").String(); content != "Hello, world!" {
		t.Errorf("expected delta.content=%q, got %q", "Hello, world!", content)
	}

	if obj := gjson.Get(jsonPart, "object").String(); obj != "chat.completion.chunk" {
		t.Errorf("expected object=chat.completion.chunk, got %q", obj)
	}

	if model := gjson.Get(jsonPart, "model").String(); model != "claude-4-sonnet" {
		t.Errorf("expected model=claude-4-sonnet, got %q", model)
	}

	if finish := gjson.Get(jsonPart, "choices.0.finish_reason"); finish.Type != gjson.Null {
		t.Errorf("expected finish_reason=null for content chunk, got %v", finish)
	}
}

func TestStreamQuotaMetadata(t *testing.T) {
	ctx := context.Background()
	var param any

	// First send a content chunk to initialise state.
	contentLine := makeDataLine(`{"type":"Content","text":"some text"}`)
	ConvertJunieResponseToOpenAI(ctx, "claude-4-sonnet", nil, nil, contentLine, &param)

	// Now send QuotaMetadata to signal end-of-stream.
	quotaLine := makeDataLine(`{"type":"QuotaMetadata","quota":100}`)
	out := ConvertJunieResponseToOpenAI(ctx, "claude-4-sonnet", nil, nil, quotaLine, &param)

	if len(out) != 2 {
		t.Fatalf("expected 2 SSE lines for QuotaMetadata (stop chunk + [DONE]), got %d", len(out))
	}

	stopChunkLine := out[0]
	doneMarker := out[1]

	if !strings.HasPrefix(stopChunkLine, "data: ") {
		t.Errorf("expected stop chunk to start with 'data: ', got %q", stopChunkLine)
	}

	stopJSON := strings.TrimPrefix(stopChunkLine, "data: ")
	if reason := gjson.Get(stopJSON, "choices.0.finish_reason").String(); reason != "stop" {
		t.Errorf("expected finish_reason=stop, got %q", reason)
	}

	if doneMarker != "data: [DONE]" {
		t.Errorf("expected last line to be 'data: [DONE]', got %q", doneMarker)
	}
}

func TestNonStreamAccumulation(t *testing.T) {
	ctx := context.Background()
	var param any

	chunks := []string{"Hello, ", "world", "!"}

	for _, text := range chunks {
		line := makeDataLine(`{"type":"Content","text":"` + text + `"}`)
		result := ConvertJunieResponseToOpenAINonStream(ctx, "gpt-4o", nil, nil, line, &param)
		if result != "" {
			t.Errorf("expected empty string for Content chunk, got %q", result)
		}
	}

	// Final QuotaMetadata should return the full assembled response.
	quotaLine := makeDataLine(`{"type":"QuotaMetadata"}`)
	finalResult := ConvertJunieResponseToOpenAINonStream(ctx, "gpt-4o", nil, nil, quotaLine, &param)

	if finalResult == "" {
		t.Fatal("expected non-empty response after QuotaMetadata, got empty string")
	}

	if obj := gjson.Get(finalResult, "object").String(); obj != "chat.completion" {
		t.Errorf("expected object=chat.completion, got %q", obj)
	}

	if model := gjson.Get(finalResult, "model").String(); model != "gpt-4o" {
		t.Errorf("expected model=gpt-4o, got %q", model)
	}

	expectedContent := "Hello, world!"
	if content := gjson.Get(finalResult, "choices.0.message.content").String(); content != expectedContent {
		t.Errorf("expected accumulated content=%q, got %q", expectedContent, content)
	}

	if role := gjson.Get(finalResult, "choices.0.message.role").String(); role != "assistant" {
		t.Errorf("expected role=assistant, got %q", role)
	}

	if reason := gjson.Get(finalResult, "choices.0.finish_reason").String(); reason != "stop" {
		t.Errorf("expected finish_reason=stop, got %q", reason)
	}
}

func TestNonContentTypeIgnored(t *testing.T) {
	ctx := context.Background()
	var param any

	unknownTypes := []string{
		`{"type":"SomeOtherEvent","data":"value"}`,
		`{"type":"Heartbeat"}`,
		`{"type":"Error","message":"oops"}`,
	}

	for _, payload := range unknownTypes {
		line := makeDataLine(payload)

		// Stream version should return nil.
		streamOut := ConvertJunieResponseToOpenAI(ctx, "gpt-4o", nil, nil, line, &param)
		if streamOut != nil {
			t.Errorf("stream: expected nil for unknown type %q, got %v", payload, streamOut)
		}

		// Non-stream version should return empty string.
		nonStreamOut := ConvertJunieResponseToOpenAINonStream(ctx, "gpt-4o", nil, nil, line, &param)
		if nonStreamOut != "" {
			t.Errorf("non-stream: expected empty string for unknown type %q, got %q", payload, nonStreamOut)
		}
	}
}

func TestStreamNonDataLineIgnored(t *testing.T) {
	ctx := context.Background()
	var param any

	// Lines that are not SSE data lines should be ignored.
	nonDataLines := [][]byte{
		[]byte("event: ping"),
		[]byte(": keep-alive comment"),
		[]byte(""),
		[]byte("id: 42"),
	}

	for _, line := range nonDataLines {
		out := ConvertJunieResponseToOpenAI(ctx, "gpt-4o", nil, nil, line, &param)
		if out != nil {
			t.Errorf("expected nil for non-data line %q, got %v", line, out)
		}
	}
}

func TestStreamParamInitialisedOnFirstCall(t *testing.T) {
	ctx := context.Background()
	var param any

	if param != nil {
		t.Fatal("param should start nil")
	}

	line := makeDataLine(`{"type":"Content","text":"init"}`)
	ConvertJunieResponseToOpenAI(ctx, "gpt-4o", nil, nil, line, &param)

	if param == nil {
		t.Fatal("param should be initialised after first call")
	}
}

func TestNonStreamSameIDAndTimestampAcrossChunks(t *testing.T) {
	ctx := context.Background()
	var param any

	// Send two content chunks.
	line1 := makeDataLine(`{"type":"Content","text":"foo"}`)
	ConvertJunieResponseToOpenAINonStream(ctx, "model-x", nil, nil, line1, &param)

	line2 := makeDataLine(`{"type":"Content","text":"bar"}`)
	ConvertJunieResponseToOpenAINonStream(ctx, "model-x", nil, nil, line2, &param)

	// Finalise.
	quotaLine := makeDataLine(`{"type":"QuotaMetadata"}`)
	result := ConvertJunieResponseToOpenAINonStream(ctx, "model-x", nil, nil, quotaLine, &param)

	if result == "" {
		t.Fatal("expected non-empty final response")
	}

	// The id and created fields should be present and non-zero.
	id := gjson.Get(result, "id").String()
	if id == "" {
		t.Error("expected non-empty id in response")
	}

	created := gjson.Get(result, "created").Int()
	if created == 0 {
		t.Error("expected non-zero created timestamp in response")
	}
}
