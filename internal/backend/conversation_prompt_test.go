package backend

import (
	"strings"
	"testing"
)

func TestConversationPromptLeadsWithCallerInstructions(t *testing.T) {
	prompt := conversationPrompt([]map[string]any{
		{"role": "system", "content": "Tool calling instructions: use get_weather"},
		{"role": "user", "content": "weather?"},
	})
	if !strings.HasPrefix(prompt, "Instructions from the API caller") {
		t.Fatalf("prompt should lead with instructions: %s", prompt)
	}
	if strings.Contains(prompt, "System:") || strings.Contains(prompt, "Conversation history") {
		t.Fatalf("instructions must not be buried in history: %s", prompt)
	}
	if !strings.HasSuffix(prompt, "Current user message:\nweather?") {
		t.Fatalf("prompt should end with current user message: %s", prompt)
	}
}

func TestConversationPromptContinuesAfterToolResult(t *testing.T) {
	prompt := conversationPrompt([]map[string]any{
		{"role": "user", "content": "weather in Paris?"},
		{"role": "assistant", "content": "<tool_calls>...</tool_calls>"},
		{"role": "tool", "content": "Tool result call_1: sunny"},
	})
	userPos := strings.Index(prompt, "User: weather in Paris?")
	toolPos := strings.Index(prompt, "Tool: Tool result call_1: sunny")
	if userPos < 0 || toolPos < 0 || userPos > toolPos {
		t.Fatalf("transcript order lost: %s", prompt)
	}
	if !strings.Contains(prompt, "Continue the conversation below as the Assistant") {
		t.Fatalf("prompt should ask to continue after tool result: %s", prompt)
	}
}
