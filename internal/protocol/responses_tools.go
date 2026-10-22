package protocol

import (
	"context"
	"time"

	tooladapter "chatgpt2api/internal/toolcall"
	"chatgpt2api/internal/util"
)

// responseTextOptions carries the caller contract for a Responses text turn.
type responseTextOptions struct {
	Tools        any
	ToolChoice   any
	Format       *ResponseFormat
	PromptTokens int
}

// ResponsesFunctionTools keeps only function tools; built-in tools such as
// image_generation take other paths.
func ResponsesFunctionTools(tools any) []any {
	var out []any
	for _, raw := range anyList(tools) {
		if tool, ok := raw.(map[string]any); ok && util.Clean(tool["type"]) == "function" {
			out = append(out, tool)
		}
	}
	return out
}

// ResponsesInstructions renders the tool and output-format instructions for a
// Responses text turn.
func ResponsesInstructions(opts responseTextOptions) string {
	return ChatInstructions(map[string]any{"tools": opts.Tools, "tool_choice": opts.ToolChoice}, opts.Format)
}

// FunctionCallOutputItem renders a parsed call as a Responses function_call item.
func FunctionCallOutputItem(call tooladapter.ParsedCall, status string) map[string]any {
	formatted := tooladapter.FormatOpenAI([]tooladapter.ParsedCall{call})[0]
	fn := formatted["function"].(map[string]any)
	return map[string]any{
		"id":        "fc_" + util.NewHex(32),
		"type":      "function_call",
		"status":    status,
		"call_id":   formatted["id"],
		"name":      call.Name,
		"arguments": fn["arguments"],
	}
}

func responseUsage(promptTokens, outputTokens int) map[string]any {
	return map[string]any{
		"input_tokens":          promptTokens,
		"output_tokens":         outputTokens,
		"total_tokens":          promptTokens + outputTokens,
		"input_tokens_details":  map[string]any{"cached_tokens": 0},
		"output_tokens_details": map[string]any{"reasoning_tokens": 0},
	}
}

func streamTextResponseEvents(ctx context.Context, model string, deltas <-chan string, upstreamErr <-chan error, opts responseTextOptions) (<-chan map[string]any, <-chan error) {
	policy := tooladapter.PolicyFromToolChoice(opts.ToolChoice)
	toolNames := tooladapter.ToolNames(opts.Tools)
	if policy.Mode != tooladapter.ChoiceNone {
		if err := validateToolChoice(policy, toolNames); err != nil {
			return streamHTTPError(err)
		}
	}
	toolMode := len(toolNames) > 0 && policy.Mode != tooladapter.ChoiceNone
	out := make(chan map[string]any)
	errOut := make(chan error, 1)
	go func() {
		defer close(out)
		defer close(errOut)
		responseID := "resp_" + util.NewHex(32)
		itemID := "msg_" + util.NewHex(32)
		created := time.Now().Unix()
		full := ""
		streamedLen := 0
		textItemOpen := false
		send := func(item map[string]any) bool {
			select {
			case out <- item:
				return true
			case <-ctx.Done():
				errOut <- ctx.Err()
				return false
			}
		}
		sendText := func(text string) bool {
			if text == "" {
				return true
			}
			if !textItemOpen {
				textItemOpen = true
				if !send(map[string]any{"type": "response.output_item.added", "output_index": 0, "item": TextOutputItem("", itemID, "in_progress")}) {
					return false
				}
			}
			return send(map[string]any{"type": "response.output_text.delta", "item_id": itemID, "output_index": 0, "content_index": 0, "delta": text})
		}
		if !send(ResponseCreated(responseID, model, created)) {
			return
		}
		for delta := range deltas {
			full += delta
			if opts.Format != nil {
				continue
			}
			visible := full
			if toolMode {
				visible = safeRawToolVisiblePrefix(full)
			}
			if len(visible) > streamedLen {
				if !sendText(visible[streamedLen:]) {
					return
				}
				streamedLen = len(visible)
			}
		}
		if upstreamErr != nil {
			if err := <-upstreamErr; err != nil {
				errOut <- err
				return
			}
		}

		text := full
		var calls []tooladapter.ParsedCall
		if toolMode {
			parsed, _, err := tooladapter.Parse(full, toolNames, policy)
			if err != nil {
				errOut <- HTTPError{Status: 400, Message: err.Error()}
				return
			}
			calls = tooladapter.NormalizeForSchemas(parsed, opts.Tools)
			if len(calls) > 0 {
				// Only the prose streamed before the tool markup stays visible.
				text = full[:streamedLen]
			}
		}
		if len(calls) == 0 {
			if opts.Format != nil {
				formatted, err := opts.Format.Apply(full)
				if err != nil {
					errOut <- err
					return
				}
				text = formatted
			}
			// Flush text held back while waiting for tool markup or format validation.
			if len(text) > streamedLen && !sendText(text[streamedLen:]) {
				return
			}
		}

		var output []map[string]any
		if textItemOpen {
			if !send(map[string]any{"type": "response.output_text.done", "item_id": itemID, "output_index": 0, "content_index": 0, "text": text}) {
				return
			}
			item := TextOutputItem(text, itemID, "completed")
			if !send(map[string]any{"type": "response.output_item.done", "output_index": 0, "item": item}) {
				return
			}
			output = append(output, item)
		} else if len(calls) == 0 {
			// Empty answer still yields one message item.
			item := TextOutputItem("", itemID, "completed")
			if !send(map[string]any{"type": "response.output_item.added", "output_index": 0, "item": TextOutputItem("", itemID, "in_progress")}) {
				return
			}
			if !send(map[string]any{"type": "response.output_item.done", "output_index": 0, "item": item}) {
				return
			}
			output = append(output, item)
		}
		for _, call := range calls {
			index := len(output)
			item := FunctionCallOutputItem(call, "completed")
			pending := util.CopyMap(item)
			pending["status"] = "in_progress"
			pending["arguments"] = ""
			if !send(map[string]any{"type": "response.output_item.added", "output_index": index, "item": pending}) {
				return
			}
			if !send(map[string]any{"type": "response.function_call_arguments.delta", "item_id": item["id"], "output_index": index, "delta": item["arguments"]}) {
				return
			}
			if !send(map[string]any{"type": "response.function_call_arguments.done", "item_id": item["id"], "output_index": index, "arguments": item["arguments"]}) {
				return
			}
			if !send(map[string]any{"type": "response.output_item.done", "output_index": index, "item": item}) {
				return
			}
			output = append(output, item)
		}
		completed := ResponseCompleted(responseID, model, created, output)
		response := completed["response"].(map[string]any)
		response["usage"] = responseUsage(opts.PromptTokens, CountTextTokens(full, model))
		if !send(completed) {
			return
		}
		errOut <- nil
	}()
	return out, errOut
}
