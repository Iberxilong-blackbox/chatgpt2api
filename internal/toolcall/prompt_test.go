package toolcall

import (
	"reflect"
	"strings"
	"testing"
)

func weatherTools() []map[string]any {
	return []map[string]any{{
		"type": "function",
		"function": map[string]any{
			"name": "get_weather",
			"parameters": map[string]any{
				"type":     "object",
				"required": []any{"city"},
				"properties": map[string]any{
					"city": map[string]any{"type": "string"},
					"days": map[string]any{"type": "integer"},
					"unit": map[string]any{"type": "string", "enum": []any{"c", "f"}},
				},
			},
		},
	}}
}

func TestBuildPromptSeparatesSchemaFromExampleArguments(t *testing.T) {
	prompt := BuildPrompt(weatherTools(), ChoicePolicy{Mode: ChoiceAuto})
	for _, want := range []string{
		`Parameters schema: {"properties":`,
		`<tool_name>get_weather</tool_name><parameters>{"city":"...","days":0,"unit":"c"}</parameters>`,
		"Never copy the schema itself",
		"Do not claim that no tools are available",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("BuildPrompt() missing %q in:\n%s", want, prompt)
		}
	}
	if strings.Contains(prompt, `<parameters>{"properties"`) || strings.Contains(prompt, `<parameters>{"type"`) {
		t.Fatalf("BuildPrompt() must not put the schema inside <parameters>:\n%s", prompt)
	}
}

func TestBuildPromptForcedChoiceNamesTool(t *testing.T) {
	prompt := BuildPrompt(weatherTools(), ChoicePolicy{Mode: ChoiceForced, Name: "get_weather"})
	if !strings.Contains(prompt, "You must call the tool get_weather now.") {
		t.Fatalf("forced prompt = %s", prompt)
	}
}

func TestNormalizeForSchemasUnwrapsSchemaEcho(t *testing.T) {
	for _, input := range []map[string]any{
		{"properties": map[string]any{"city": "Paris"}},
		{"type": "object", "properties": map[string]any{"city": "Paris"}},
	} {
		got := NormalizeForSchemas([]ParsedCall{{Name: "get_weather", Input: input}}, weatherTools())
		if !reflect.DeepEqual(got[0].Input, map[string]any{"city": "Paris"}) {
			t.Fatalf("NormalizeForSchemas(%#v) = %#v, want unwrapped arguments", input, got[0].Input)
		}
	}
}

func TestNormalizeForSchemasKeepsDeclaredPropertiesField(t *testing.T) {
	tools := []map[string]any{{
		"name": "set_props",
		"input_schema": map[string]any{
			"type":       "object",
			"properties": map[string]any{"properties": map[string]any{"type": "object"}},
		},
	}}
	input := map[string]any{"properties": map[string]any{"a": float64(1)}}
	got := NormalizeForSchemas([]ParsedCall{{Name: "set_props", Input: input}}, tools)
	if !reflect.DeepEqual(got[0].Input, input) {
		t.Fatalf("Input = %#v, want untouched %#v", got[0].Input, input)
	}
}

func TestFormatOpenAIUsesUniqueCallIDs(t *testing.T) {
	calls := []ParsedCall{{Name: "a", Input: map[string]any{}}, {Name: "a", Input: map[string]any{}}}
	first := FormatOpenAI(calls)
	second := FormatOpenAI(calls)
	seen := map[any]bool{}
	for _, item := range append(first, second...) {
		if seen[item["id"]] {
			t.Fatalf("duplicate call id %v", item["id"])
		}
		seen[item["id"]] = true
	}
}

func TestPolicyFromResponsesFunctionChoice(t *testing.T) {
	got := PolicyFromToolChoice(map[string]any{"type": "function", "name": "get_weather"})
	if got != (ChoicePolicy{Mode: ChoiceForced, Name: "get_weather"}) {
		t.Fatalf("PolicyFromToolChoice() = %#v", got)
	}
}

func TestFormatCallsXMLRoundTripsThroughParse(t *testing.T) {
	xml := FormatCallsXML([]ParsedCall{{Name: "get_weather", Input: map[string]any{"city": "Paris"}}})
	calls, _, err := Parse(xml, []string{"get_weather"}, ChoicePolicy{Mode: ChoiceAuto})
	if err != nil || len(calls) != 1 || calls[0].Input["city"] != "Paris" {
		t.Fatalf("Parse(FormatCallsXML()) = %#v, %v", calls, err)
	}
}
