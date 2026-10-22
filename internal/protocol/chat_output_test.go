package protocol

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestNormalizeMessagesReplaysToolHistory(t *testing.T) {
	messages := []map[string]any{
		{"role": "user", "content": "weather in Paris?"},
		{"role": "assistant", "content": nil, "tool_calls": []any{map[string]any{
			"id": "call_abc", "type": "function",
			"function": map[string]any{"name": "get_weather", "arguments": `{"city":"Paris"}`},
		}}},
		{"role": "tool", "tool_call_id": "call_abc", "content": "sunny"},
	}
	got := NormalizeMessages(messages, nil)
	if len(got) != 3 {
		t.Fatalf("len = %d, want 3", len(got))
	}
	if want := `<tool_calls><tool_call><tool_name>get_weather</tool_name><parameters>{"city":"Paris"}</parameters></tool_call></tool_calls>`; got[1]["content"] != want {
		t.Fatalf("assistant content = %q, want %q", got[1]["content"], want)
	}
	if got[2]["role"] != "tool" || got[2]["content"] != "Tool result call_abc: sunny" {
		t.Fatalf("tool message = %#v", got[2])
	}
	// Stored contexts are normalized again; the result must be stable.
	again := NormalizeMessages(got, nil)
	if again[2]["content"] != got[2]["content"] || again[1]["content"] != got[1]["content"] {
		t.Fatalf("re-normalization changed messages: %#v", again)
	}
}

func TestChatResponseFormatParsing(t *testing.T) {
	if format, err := ChatResponseFormat(map[string]any{}); format != nil || err != nil {
		t.Fatalf("missing response_format = %#v, %v", format, err)
	}
	if format, err := ChatResponseFormat(map[string]any{"response_format": map[string]any{"type": "text"}}); format != nil || err != nil {
		t.Fatalf("text response_format = %#v, %v", format, err)
	}
	if _, err := ChatResponseFormat(map[string]any{"response_format": map[string]any{"type": "xml"}}); err == nil {
		t.Fatal("unknown response_format type should error")
	}
	if _, err := ChatResponseFormat(map[string]any{"response_format": map[string]any{"type": "json_schema", "json_schema": map[string]any{"name": "x"}}}); err == nil {
		t.Fatal("json_schema without schema should error")
	}
}

func personFormat() *ResponseFormat {
	return &ResponseFormat{Type: "json_schema", Name: "person", Strict: true, Schema: map[string]any{
		"type":                 "object",
		"required":             []any{"name", "age"},
		"additionalProperties": false,
		"properties": map[string]any{
			"name": map[string]any{"type": "string"},
			"age":  map[string]any{"type": "integer"},
		},
	}}
}

func TestResponseFormatApply(t *testing.T) {
	format := personFormat()
	got, err := format.Apply("```json\n{\"name\":\"Ann\",\"age\":3}\n```")
	if err != nil || got != `{"name":"Ann","age":3}` {
		t.Fatalf("Apply(fenced) = %q, %v", got, err)
	}
	got, err = format.Apply(`Sure! {"name":"Ann","age":3} hope it helps`)
	if err != nil || got != `{"name":"Ann","age":3}` {
		t.Fatalf("Apply(prose) = %q, %v", got, err)
	}
	for _, bad := range []string{`{"name":"Ann"}`, `{"name":"Ann","age":"3"}`, `{"name":"Ann","age":3,"x":1}`, `not json`} {
		if _, err := format.Apply(bad); err == nil {
			t.Fatalf("Apply(%q) should fail", bad)
		}
	}
	if _, err := (&ResponseFormat{Type: "json_object"}).Apply(`[1,2]`); err == nil {
		t.Fatal("json_object must reject arrays")
	}
	if !strings.Contains(format.Prompt(), `"required":["name","age"]`) {
		t.Fatalf("Prompt() = %s", format.Prompt())
	}
}

func TestValidateTextModel(t *testing.T) {
	engine := &Engine{ListModelsFunc: func(context.Context) (map[string]any, error) {
		return map[string]any{"data": []map[string]any{{"id": "gpt-6"}}}, nil
	}}
	if err := engine.ValidateTextModel(context.Background(), "gpt-6"); err != nil {
		t.Fatalf("known model error = %v", err)
	}
	if err := engine.ValidateTextModel(context.Background(), "auto"); err != nil {
		t.Fatalf("auto error = %v", err)
	}
	err := engine.ValidateTextModel(context.Background(), "gpt-typo")
	var httpErr HTTPError
	if !errors.As(err, &httpErr) || httpErr.Status != 404 {
		t.Fatalf("unknown model error = %#v, want 404", err)
	}

	failing := &Engine{ListModelsFunc: func(context.Context) (map[string]any, error) { return nil, errors.New("down") }}
	if err := failing.ValidateTextModel(context.Background(), "gpt-typo"); err != nil {
		t.Fatalf("model list failure should not block, got %v", err)
	}
}

func collectChunks(t *testing.T, items <-chan map[string]any, errCh <-chan error) ([]map[string]any, error) {
	t.Helper()
	var chunks []map[string]any
	for item := range items {
		chunks = append(chunks, item)
	}
	return chunks, <-errCh
}

func feedDeltas(parts ...string) (<-chan string, <-chan error) {
	deltas := make(chan string, len(parts))
	errs := make(chan error, 1)
	for _, part := range parts {
		deltas <- part
	}
	close(deltas)
	errs <- nil
	close(errs)
	return deltas, errs
}

func TestWrapChatStreamValidatesFormatAndAppendsUsage(t *testing.T) {
	deltas, errs := feedDeltas("```json\n{\"name\":", "\"Ann\",\"age\":3}\n```")
	items, errCh := streamChatCompletionEvents(context.Background(), "gpt-5", deltas, errs, nil, nil)
	items, errCh = wrapChatStream(context.Background(), "gpt-5", items, errCh, chatOutputOptions{Format: personFormat(), IncludeUsage: true, PromptTokens: 7})
	chunks, err := collectChunks(t, items, errCh)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	var content strings.Builder
	for _, chunk := range chunks {
		for _, choice := range chunk["choices"].([]map[string]any) {
			if text, ok := choice["delta"].(map[string]any)["content"].(string); ok {
				content.WriteString(text)
			}
		}
	}
	if content.String() != `{"name":"Ann","age":3}` {
		t.Fatalf("content = %q", content.String())
	}
	last := chunks[len(chunks)-1]
	usage, _ := last["usage"].(map[string]any)
	if usage["prompt_tokens"] != 7 || usage["prompt_tokens_details"].(map[string]any)["cached_tokens"] != 0 {
		t.Fatalf("last chunk = %#v, want usage chunk", last)
	}
}

func TestWrapChatStreamRejectsInvalidFormat(t *testing.T) {
	deltas, errs := feedDeltas("I cannot do that")
	items, errCh := streamChatCompletionEvents(context.Background(), "gpt-5", deltas, errs, nil, nil)
	items, errCh = wrapChatStream(context.Background(), "gpt-5", items, errCh, chatOutputOptions{Format: personFormat()})
	if _, err := collectChunks(t, items, errCh); err == nil {
		t.Fatal("invalid structured output should error")
	}
}

func TestStreamTextResponseEventsEmitsFunctionCall(t *testing.T) {
	tools := ResponsesFunctionTools([]any{
		map[string]any{"type": "function", "name": "get_weather", "parameters": map[string]any{"type": "object", "properties": map[string]any{"city": map[string]any{"type": "string"}}}},
		map[string]any{"type": "web_search"},
	})
	if len(tools) != 1 {
		t.Fatalf("ResponsesFunctionTools() = %#v", tools)
	}
	deltas, errs := feedDeltas(`Checking. <tool_calls><tool_call><tool_name>get_weather</tool_name><parameters>{"properties":{"city":"Paris"}}</parameters></tool_call></tool_calls>`)
	events, errCh := streamTextResponseEvents(context.Background(), "gpt-5", deltas, errs, responseTextOptions{Tools: tools, PromptTokens: 5})
	chunks, err := collectChunks(t, events, errCh)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	var types []string
	for _, chunk := range chunks {
		types = append(types, chunk["type"].(string))
	}
	joined := strings.Join(types, ",")
	if !strings.Contains(joined, "response.function_call_arguments.done") {
		t.Fatalf("events = %s", joined)
	}
	response := chunks[len(chunks)-1]["response"].(map[string]any)
	output := response["output"].([]map[string]any)
	if len(output) != 2 || output[0]["type"] != "message" || output[1]["type"] != "function_call" {
		t.Fatalf("output = %#v", output)
	}
	if output[1]["arguments"] != `{"city":"Paris"}` || output[1]["name"] != "get_weather" {
		t.Fatalf("function_call = %#v", output[1])
	}
	if text := responseContentText(output[0]["content"]); text != "Checking." {
		t.Fatalf("message text = %q", text)
	}
	if response["usage"].(map[string]any)["input_tokens"] != 5 {
		t.Fatalf("usage = %#v", response["usage"])
	}
}

func TestMessagesFromInputConvertsFunctionCallItems(t *testing.T) {
	input := []any{
		map[string]any{"type": "function_call", "call_id": "call_1", "name": "get_weather", "arguments": `{"city":"Paris"}`},
		map[string]any{"type": "function_call_output", "call_id": "call_1", "output": "sunny"},
	}
	got := MessagesFromInput(input, "be brief")
	if len(got) != 3 {
		t.Fatalf("messages = %#v", got)
	}
	if !strings.Contains(got[1]["content"].(string), "<tool_name>get_weather</tool_name>") {
		t.Fatalf("function_call message = %#v", got[1])
	}
	if got[2]["content"] != "Tool result call_1: sunny" {
		t.Fatalf("function_call_output message = %#v", got[2])
	}
}
