package protocol

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"chatgpt2api/internal/util"
)

// ResponseFormat is the structured-output contract requested by the caller.
// Upstream ChatGPT web has no native structured output, so it is emulated by
// prompt instructions plus validation of the final text.
type ResponseFormat struct {
	Type   string
	Name   string
	Schema map[string]any
	Strict bool
}

// ChatResponseFormat reads Chat Completions `response_format`.
func ChatResponseFormat(body map[string]any) (*ResponseFormat, error) {
	raw, ok := body["response_format"].(map[string]any)
	if !ok {
		if body["response_format"] != nil {
			return nil, HTTPError{Status: 400, Message: "response_format must be an object"}
		}
		return nil, nil
	}
	spec, _ := raw["json_schema"].(map[string]any)
	return parseResponseFormat(util.Clean(raw["type"]), spec)
}

// ResponsesTextFormat reads Responses API `text.format`.
func ResponsesTextFormat(body map[string]any) (*ResponseFormat, error) {
	text, _ := body["text"].(map[string]any)
	raw, ok := text["format"].(map[string]any)
	if !ok {
		return nil, nil
	}
	return parseResponseFormat(util.Clean(raw["type"]), raw)
}

func parseResponseFormat(kind string, spec map[string]any) (*ResponseFormat, error) {
	switch kind {
	case "", "text":
		return nil, nil
	case "json_object":
		return &ResponseFormat{Type: kind}, nil
	case "json_schema":
		schema, ok := spec["schema"].(map[string]any)
		if !ok {
			return nil, HTTPError{Status: 400, Message: "response_format json_schema requires a schema object"}
		}
		return &ResponseFormat{Type: kind, Name: util.Clean(spec["name"]), Schema: schema, Strict: util.ToBool(spec["strict"])}, nil
	default:
		return nil, HTTPError{Status: 400, Message: "unsupported response_format type: " + kind}
	}
}

// Prompt returns the instruction that asks the model for JSON-only output.
func (f *ResponseFormat) Prompt() string {
	if f == nil {
		return ""
	}
	prompt := "Output format (from the API caller, mandatory): reply with a single valid JSON value only. No markdown code fences, no explanations before or after the JSON."
	if f.Type == "json_schema" {
		schemaJSON, _ := json.Marshal(f.Schema)
		prompt += "\nThe JSON must conform to this JSON Schema"
		if f.Name != "" {
			prompt += " (" + f.Name + ")"
		}
		prompt += ": " + string(schemaJSON)
		if f.Strict {
			prompt += "\nInclude every required property and no properties outside the schema."
		}
	}
	return prompt
}

// Apply extracts the JSON value from model text and validates it against the
// requested format, returning the cleaned JSON text.
func (f *ResponseFormat) Apply(text string) (string, error) {
	if f == nil {
		return text, nil
	}
	jsonText, value, ok := extractJSONValue(text)
	if !ok {
		return "", HTTPError{Status: 502, Message: "model output is not valid JSON for response_format " + f.Type}
	}
	if f.Type == "json_object" {
		if _, ok := value.(map[string]any); !ok {
			return "", HTTPError{Status: 502, Message: "model output is not a JSON object for response_format json_object"}
		}
		return jsonText, nil
	}
	if err := validateJSONSchema(value, f.Schema, "$", f.Strict); err != nil {
		return "", HTTPError{Status: 502, Message: "model output does not match response_format json_schema: " + err.Error()}
	}
	return jsonText, nil
}

func extractJSONValue(text string) (string, any, bool) {
	candidate := strings.TrimSpace(text)
	if strings.HasPrefix(candidate, "```") {
		candidate = strings.TrimPrefix(candidate, "```")
		if newline := strings.IndexByte(candidate, '\n'); newline >= 0 {
			candidate = candidate[newline+1:]
		}
		candidate = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(candidate), "```"))
	}
	var value any
	if err := json.Unmarshal([]byte(candidate), &value); err == nil {
		return candidate, value, true
	}
	for _, pair := range [][2]string{{"{", "}"}, {"[", "]"}} {
		start := strings.Index(candidate, pair[0])
		end := strings.LastIndex(candidate, pair[1])
		if start < 0 || end <= start {
			continue
		}
		slice := candidate[start : end+1]
		if err := json.Unmarshal([]byte(slice), &value); err == nil {
			return slice, value, true
		}
	}
	return "", nil, false
}

// validateJSONSchema checks the commonly used JSON Schema keywords: type,
// enum, const, required, properties, additionalProperties and items.
func validateJSONSchema(value any, schema map[string]any, path string, strict bool) error {
	if schema == nil {
		return nil
	}
	if options, ok := schema["anyOf"].([]any); ok && len(options) > 0 {
		for _, option := range options {
			if optionSchema, ok := option.(map[string]any); ok && validateJSONSchema(value, optionSchema, path, strict) == nil {
				return nil
			}
		}
		return fmt.Errorf("%s matches none of anyOf", path)
	}
	if types := schemaTypes(schema["type"]); len(types) > 0 {
		matched := false
		for _, name := range types {
			if jsonValueHasType(value, name) {
				matched = true
				break
			}
		}
		if !matched {
			return fmt.Errorf("%s must be %s", path, strings.Join(types, " or "))
		}
	}
	if enumValues, ok := schema["enum"].([]any); ok {
		found := false
		for _, item := range enumValues {
			if jsonEqual(item, value) {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("%s is not one of the enum values", path)
		}
	}
	if constValue, ok := schema["const"]; ok && !jsonEqual(constValue, value) {
		return fmt.Errorf("%s must equal const value", path)
	}
	switch typed := value.(type) {
	case map[string]any:
		props, _ := schema["properties"].(map[string]any)
		for _, name := range anyList(schema["required"]) {
			key := util.Clean(name)
			if _, ok := typed[key]; !ok {
				return fmt.Errorf("%s.%s is required", path, key)
			}
		}
		for key, item := range typed {
			propSchema, declared := props[key].(map[string]any)
			if !declared {
				if additional, ok := schema["additionalProperties"].(bool); (ok && !additional) || (strict && props != nil && !ok) {
					return fmt.Errorf("%s.%s is not allowed", path, key)
				}
				continue
			}
			if err := validateJSONSchema(item, propSchema, path+"."+key, strict); err != nil {
				return err
			}
		}
	case []any:
		if itemSchema, ok := schema["items"].(map[string]any); ok {
			for index, item := range typed {
				if err := validateJSONSchema(item, itemSchema, fmt.Sprintf("%s[%d]", path, index), strict); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func schemaTypes(raw any) []string {
	switch v := raw.(type) {
	case string:
		return []string{v}
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			if name, ok := item.(string); ok {
				out = append(out, name)
			}
		}
		return out
	}
	return nil
}

func jsonValueHasType(value any, name string) bool {
	switch name {
	case "object":
		_, ok := value.(map[string]any)
		return ok
	case "array":
		_, ok := value.([]any)
		return ok
	case "string":
		_, ok := value.(string)
		return ok
	case "number":
		_, ok := value.(float64)
		return ok
	case "integer":
		number, ok := value.(float64)
		return ok && number == float64(int64(number))
	case "boolean":
		_, ok := value.(bool)
		return ok
	case "null":
		return value == nil
	}
	return true
}

func jsonEqual(a, b any) bool {
	left, errLeft := json.Marshal(a)
	right, errRight := json.Marshal(b)
	return errLeft == nil && errRight == nil && string(left) == string(right)
}

// ChatInstructions combines tool and output-format instructions into the
// caller system prompt injected ahead of the conversation.
func ChatInstructions(body map[string]any, format *ResponseFormat) string {
	parts := make([]string, 0, 2)
	if prompt := ChatToolPrompt(body); prompt != "" {
		parts = append(parts, prompt)
	}
	if prompt := format.Prompt(); prompt != "" {
		parts = append(parts, prompt)
	}
	return strings.Join(parts, "\n\n")
}

// UsageMap builds an OpenAI usage object. Token counts are local estimates:
// upstream ChatGPT web exposes neither token usage nor prompt cache hits.
func UsageMap(promptTokens, completionTokens int) map[string]any {
	return map[string]any{
		"prompt_tokens":         promptTokens,
		"completion_tokens":     completionTokens,
		"total_tokens":          promptTokens + completionTokens,
		"prompt_tokens_details": map[string]any{"cached_tokens": 0},
	}
}

const textModelCacheTTL = 10 * time.Minute

// ValidateTextModel rejects model ids that /v1/models does not list, so a
// typo is not silently served by the upstream default model.
func (e *Engine) ValidateTextModel(ctx context.Context, model string) error {
	model = strings.TrimSpace(model)
	if model == "" || model == util.ImageModelAuto {
		return nil
	}
	known, ok := e.knownTextModels(ctx)
	if !ok {
		return nil
	}
	if _, exists := known[model]; exists {
		return nil
	}
	return HTTPError{Status: 404, Message: fmt.Sprintf("The model `%s` does not exist or is not available. See /v1/models for supported models.", model)}
}

func (e *Engine) knownTextModels(ctx context.Context) (map[string]struct{}, bool) {
	e.modelCacheMu.Lock()
	defer e.modelCacheMu.Unlock()
	if e.modelCache != nil && time.Since(e.modelCacheAt) < textModelCacheTTL {
		return e.modelCache, true
	}
	listCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	result, err := e.ListModels(listCtx)
	if err != nil {
		// Without a model list the id cannot be judged; let upstream decide.
		return nil, false
	}
	known := map[string]struct{}{}
	for _, item := range util.AsMapSlice(result["data"]) {
		if id := util.Clean(item["id"]); id != "" {
			known[id] = struct{}{}
		}
	}
	e.modelCache = known
	e.modelCacheAt = time.Now()
	return known, true
}

type chatOutputOptions struct {
	Format       *ResponseFormat
	IncludeUsage bool
	PromptTokens int
}

// wrapChatStream post-processes OpenAI chat chunks: it buffers and validates
// structured output and appends the final usage chunk when requested.
func wrapChatStream(ctx context.Context, model string, items <-chan map[string]any, upstreamErr <-chan error, opts chatOutputOptions) (<-chan map[string]any, <-chan error) {
	if opts.Format == nil && !opts.IncludeUsage {
		return items, upstreamErr
	}
	out := make(chan map[string]any)
	errOut := make(chan error, 1)
	go func() {
		defer close(out)
		defer close(errOut)
		send := func(chunk map[string]any) bool {
			select {
			case out <- chunk:
				return true
			case <-ctx.Done():
				errOut <- ctx.Err()
				return false
			}
		}
		var completion strings.Builder
		var buffered strings.Builder
		id, created := "", int64(0)
		for chunk := range items {
			if id == "" {
				id = util.Clean(chunk["id"])
				created, _ = chunk["created"].(int64)
			}
			choices, _ := chunk["choices"].([]map[string]any)
			if len(choices) == 0 {
				if !send(chunk) {
					return
				}
				continue
			}
			delta, _ := choices[0]["delta"].(map[string]any)
			finish := choices[0]["finish_reason"]
			if content, ok := delta["content"].(string); ok {
				completion.WriteString(content)
				if opts.Format != nil {
					buffered.WriteString(content)
					if delta["role"] == nil {
						continue
					}
					delta["content"] = ""
				}
			}
			if calls, ok := delta["tool_calls"]; ok && calls != nil {
				completion.WriteString(util.Clean(calls))
			}
			if opts.Format != nil && finish == "stop" {
				text, err := opts.Format.Apply(buffered.String())
				if err != nil {
					// Drain so the producer goroutine can exit.
					for range items {
					}
					<-upstreamErr
					errOut <- err
					return
				}
				if !send(CompletionChunk(model, map[string]any{"content": text}, nil, id, created)) {
					return
				}
			}
			if !send(chunk) {
				return
			}
		}
		if err := <-upstreamErr; err != nil {
			errOut <- err
			return
		}
		if opts.IncludeUsage {
			usage := UsageMap(opts.PromptTokens, CountTextTokens(completion.String(), model))
			if !send(map[string]any{"id": id, "object": "chat.completion.chunk", "created": created, "model": model, "choices": []map[string]any{}, "usage": usage}) {
				return
			}
		}
		errOut <- nil
	}()
	return out, errOut
}
