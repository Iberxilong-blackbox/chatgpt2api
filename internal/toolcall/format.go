package toolcall

import (
	"crypto/rand"
	"encoding/hex"
)

// NewCallID returns a random tool call id with the given prefix so ids stay
// unique across turns of the same conversation.
func NewCallID(prefix string) string {
	buf := make([]byte, 12)
	_, _ = rand.Read(buf)
	return prefix + hex.EncodeToString(buf)
}

func FormatOpenAI(calls []ParsedCall) []map[string]any {
	out := make([]map[string]any, 0, len(calls))
	for _, call := range calls {
		out = append(out, map[string]any{
			"id":   NewCallID("call_"),
			"type": "function",
			"function": map[string]any{
				"name":      call.Name,
				"arguments": compactJSON(call.Input),
			},
		})
	}
	return out
}

func FormatOpenAIStream(calls []ParsedCall) []map[string]any {
	out := make([]map[string]any, 0, len(calls))
	for i, call := range calls {
		out = append(out, map[string]any{
			"index": i,
			"id":    NewCallID("call_"),
			"type":  "function",
			"function": map[string]any{
				"name":      call.Name,
				"arguments": compactJSON(call.Input),
			},
		})
	}
	return out
}

func FormatAnthropic(calls []ParsedCall) []map[string]any {
	out := make([]map[string]any, 0, len(calls))
	for _, call := range calls {
		out = append(out, map[string]any{
			"type":  "tool_use",
			"id":    NewCallID("toolu_"),
			"name":  call.Name,
			"input": cloneMap(call.Input),
		})
	}
	return out
}
