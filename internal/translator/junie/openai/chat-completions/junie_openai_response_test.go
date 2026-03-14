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

// --- Tool call response tests ---

func TestStreamToolCallBegin(t *testing.T) {
	// ToolCallBegin should emit an OpenAI chunk with a tool_calls delta containing
	// the tool call id, type=function, and function.name.
	ctx := context.Background()
	var param any

	line := makeDataLine(`{"type":"ToolCallBegin","tool_call_id":"call_123","name":"get_weather"}`)
	out := ConvertJunieResponseToOpenAI(ctx, "gpt-4o", nil, nil, line, &param)

	if len(out) != 1 {
		t.Fatalf("expected 1 SSE line for ToolCallBegin, got %d", len(out))
	}

	jsonPart := strings.TrimPrefix(out[0], "data: ")
	if obj := gjson.Get(jsonPart, "object").String(); obj != "chat.completion.chunk" {
		t.Errorf("expected object=chat.completion.chunk, got %q", obj)
	}

	tc := gjson.Get(jsonPart, "choices.0.delta.tool_calls.0")
	if !tc.Exists() {
		t.Fatal("expected choices[0].delta.tool_calls[0] to exist")
	}
	if index := tc.Get("index").Int(); index != 0 {
		t.Errorf("expected tool_call index=0, got %d", index)
	}
	if id := tc.Get("id").String(); id != "call_123" {
		t.Errorf("expected tool_call id=call_123, got %q", id)
	}
	if tcType := tc.Get("type").String(); tcType != "function" {
		t.Errorf("expected tool_call type=function, got %q", tcType)
	}
	if name := tc.Get("function.name").String(); name != "get_weather" {
		t.Errorf("expected function.name=get_weather, got %q", name)
	}

	// finish_reason should be null for begin event
	if fr := gjson.Get(jsonPart, "choices.0.finish_reason"); fr.Type != gjson.Null {
		t.Errorf("expected finish_reason=null for ToolCallBegin, got %v", fr)
	}
}

func TestStreamToolCallArgumentsDelta(t *testing.T) {
	// ToolCallArgumentsDelta should emit an OpenAI chunk with function.arguments delta.
	ctx := context.Background()
	var param any

	// Initialize with a ToolCallBegin first.
	beginLine := makeDataLine(`{"type":"ToolCallBegin","tool_call_id":"call_123","name":"get_weather"}`)
	ConvertJunieResponseToOpenAI(ctx, "gpt-4o", nil, nil, beginLine, &param)

	deltaLine := makeDataLine(`{"type":"ToolCallArgumentsDelta","content":"{\"location\":"}`)
	out := ConvertJunieResponseToOpenAI(ctx, "gpt-4o", nil, nil, deltaLine, &param)

	if len(out) != 1 {
		t.Fatalf("expected 1 SSE line for ToolCallArgumentsDelta, got %d", len(out))
	}

	jsonPart := strings.TrimPrefix(out[0], "data: ")
	tc := gjson.Get(jsonPart, "choices.0.delta.tool_calls.0")
	if !tc.Exists() {
		t.Fatal("expected choices[0].delta.tool_calls[0] to exist")
	}
	if args := tc.Get("function.arguments").String(); args != `{"location":` {
		t.Errorf("expected function.arguments=%q, got %q", `{"location":`, args)
	}
	// index should still be 0 (current tool call index)
	if index := tc.Get("index").Int(); index != 0 {
		t.Errorf("expected tool_call index=0, got %d", index)
	}
}

func TestStreamToolCallEnd(t *testing.T) {
	// ToolCallEnd by itself should not emit a chunk (the stream continues).
	// The finish chunk is emitted by QuotaMetadata.
	ctx := context.Background()
	var param any

	beginLine := makeDataLine(`{"type":"ToolCallBegin","tool_call_id":"call_123","name":"get_weather"}`)
	ConvertJunieResponseToOpenAI(ctx, "gpt-4o", nil, nil, beginLine, &param)

	endLine := makeDataLine(`{"type":"ToolCallEnd"}`)
	out := ConvertJunieResponseToOpenAI(ctx, "gpt-4o", nil, nil, endLine, &param)

	// ToolCallEnd itself doesn't need to emit anything, or may emit nothing.
	// The key is it should not cause a panic and processing continues.
	_ = out

	// After ToolCallEnd, QuotaMetadata should emit finish_reason=tool_calls.
	quotaLine := makeDataLine(`{"type":"QuotaMetadata"}`)
	finalOut := ConvertJunieResponseToOpenAI(ctx, "gpt-4o", nil, nil, quotaLine, &param)

	if len(finalOut) < 2 {
		t.Fatalf("expected at least 2 lines from QuotaMetadata after tool call, got %d", len(finalOut))
	}

	stopJSON := strings.TrimPrefix(finalOut[0], "data: ")
	if reason := gjson.Get(stopJSON, "choices.0.finish_reason").String(); reason != "tool_calls" {
		t.Errorf("expected finish_reason=tool_calls after tool call stream, got %q", reason)
	}
	if finalOut[len(finalOut)-1] != "data: [DONE]" {
		t.Errorf("expected last line to be 'data: [DONE]', got %q", finalOut[len(finalOut)-1])
	}
}

func TestStreamMultipleToolCalls(t *testing.T) {
	// Two sequential tool calls should each get their own index.
	ctx := context.Background()
	var param any

	// First tool call
	ConvertJunieResponseToOpenAI(ctx, "gpt-4o", nil, nil,
		makeDataLine(`{"type":"ToolCallBegin","tool_call_id":"call_1","name":"tool_a"}`), &param)
	ConvertJunieResponseToOpenAI(ctx, "gpt-4o", nil, nil,
		makeDataLine(`{"type":"ToolCallArgumentsDelta","content":"{}"}`), &param)
	ConvertJunieResponseToOpenAI(ctx, "gpt-4o", nil, nil,
		makeDataLine(`{"type":"ToolCallEnd"}`), &param)

	// Second tool call
	out2 := ConvertJunieResponseToOpenAI(ctx, "gpt-4o", nil, nil,
		makeDataLine(`{"type":"ToolCallBegin","tool_call_id":"call_2","name":"tool_b"}`), &param)

	if len(out2) != 1 {
		t.Fatalf("expected 1 SSE line for second ToolCallBegin, got %d", len(out2))
	}

	jsonPart := strings.TrimPrefix(out2[0], "data: ")
	tc := gjson.Get(jsonPart, "choices.0.delta.tool_calls.0")
	if !tc.Exists() {
		t.Fatal("expected choices[0].delta.tool_calls[0] to exist for second tool call")
	}
	// The second tool call should have index=1
	if index := tc.Get("index").Int(); index != 1 {
		t.Errorf("expected second tool_call index=1, got %d", index)
	}
	if id := tc.Get("id").String(); id != "call_2" {
		t.Errorf("expected second tool_call id=call_2, got %q", id)
	}
	if name := tc.Get("function.name").String(); name != "tool_b" {
		t.Errorf("expected second tool_call function.name=tool_b, got %q", name)
	}
}

func TestNonStreamToolCallResponse(t *testing.T) {
	// Tool calls accumulated in non-stream mode should appear in choices[0].message.tool_calls
	// with finish_reason=tool_calls.
	ctx := context.Background()
	var param any

	ConvertJunieResponseToOpenAINonStream(ctx, "gpt-4o", nil, nil,
		makeDataLine(`{"type":"ToolCallBegin","tool_call_id":"call_abc","name":"get_weather"}`), &param)
	ConvertJunieResponseToOpenAINonStream(ctx, "gpt-4o", nil, nil,
		makeDataLine(`{"type":"ToolCallArgumentsDelta","content":"{\"location\":"}`), &param)
	ConvertJunieResponseToOpenAINonStream(ctx, "gpt-4o", nil, nil,
		makeDataLine(`{"type":"ToolCallArgumentsDelta","content":"\"Paris\"}"}`), &param)
	ConvertJunieResponseToOpenAINonStream(ctx, "gpt-4o", nil, nil,
		makeDataLine(`{"type":"ToolCallEnd"}`), &param)

	// Finalize
	result := ConvertJunieResponseToOpenAINonStream(ctx, "gpt-4o", nil, nil,
		makeDataLine(`{"type":"QuotaMetadata"}`), &param)

	if result == "" {
		t.Fatal("expected non-empty response after QuotaMetadata with tool calls")
	}

	if obj := gjson.Get(result, "object").String(); obj != "chat.completion" {
		t.Errorf("expected object=chat.completion, got %q", obj)
	}
	if reason := gjson.Get(result, "choices.0.finish_reason").String(); reason != "tool_calls" {
		t.Errorf("expected finish_reason=tool_calls, got %q", reason)
	}

	tcs := gjson.Get(result, "choices.0.message.tool_calls").Array()
	if len(tcs) != 1 {
		t.Fatalf("expected 1 tool_call in message, got %d", len(tcs))
	}

	tc := tcs[0]
	if id := tc.Get("id").String(); id != "call_abc" {
		t.Errorf("expected tool_call id=call_abc, got %q", id)
	}
	if tcType := tc.Get("type").String(); tcType != "function" {
		t.Errorf("expected tool_call type=function, got %q", tcType)
	}
	if name := tc.Get("function.name").String(); name != "get_weather" {
		t.Errorf("expected function.name=get_weather, got %q", name)
	}
	// arguments should be the concatenated deltas
	expectedArgs := `{"location":"Paris"}`
	if args := tc.Get("function.arguments").String(); args != expectedArgs {
		t.Errorf("expected function.arguments=%q, got %q", expectedArgs, args)
	}
}

func TestStreamMixedContentAndToolCalls(t *testing.T) {
	// Content chunks followed by a tool call — verify both are handled correctly.
	ctx := context.Background()
	var param any

	// Content first
	out1 := ConvertJunieResponseToOpenAI(ctx, "gpt-4o", nil, nil,
		makeDataLine(`{"type":"Content","text":"Let me check that for you."}`), &param)
	if len(out1) != 1 {
		t.Fatalf("expected 1 SSE line for content, got %d", len(out1))
	}
	jsonPart1 := strings.TrimPrefix(out1[0], "data: ")
	if content := gjson.Get(jsonPart1, "choices.0.delta.content").String(); content != "Let me check that for you." {
		t.Errorf("expected content delta, got %q", content)
	}

	// Then a tool call begins
	out2 := ConvertJunieResponseToOpenAI(ctx, "gpt-4o", nil, nil,
		makeDataLine(`{"type":"ToolCallBegin","tool_call_id":"call_99","name":"search"}`), &param)
	if len(out2) != 1 {
		t.Fatalf("expected 1 SSE line for ToolCallBegin, got %d", len(out2))
	}
	jsonPart2 := strings.TrimPrefix(out2[0], "data: ")
	if !gjson.Get(jsonPart2, "choices.0.delta.tool_calls.0").Exists() {
		t.Error("expected tool_calls delta in ToolCallBegin chunk")
	}

	// End stream
	ConvertJunieResponseToOpenAI(ctx, "gpt-4o", nil, nil,
		makeDataLine(`{"type":"ToolCallEnd"}`), &param)
	finalOut := ConvertJunieResponseToOpenAI(ctx, "gpt-4o", nil, nil,
		makeDataLine(`{"type":"QuotaMetadata"}`), &param)

	stopJSON := strings.TrimPrefix(finalOut[0], "data: ")
	if reason := gjson.Get(stopJSON, "choices.0.finish_reason").String(); reason != "tool_calls" {
		t.Errorf("expected finish_reason=tool_calls when stream had tool calls, got %q", reason)
	}
}
