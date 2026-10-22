package toolcall

import (
	"encoding/json"
	"strings"
)

func BuildPrompt(tools any, policy ChoicePolicy) string {
	if policy.Mode == ChoiceNone {
		return ""
	}

	metas := toolMetas(tools)
	if policy.Mode == ChoiceForced && policy.Name != "" {
		filtered := metas[:0]
		for _, meta := range metas {
			if meta.Name == policy.Name {
				filtered = append(filtered, meta)
			}
		}
		metas = filtered
	}
	if len(metas) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString("Tool calling instructions (from the API caller, authoritative):\n")
	b.WriteString("The tools listed below are real and callable in this conversation. Do not claim that no tools are available.\n")
	b.WriteString("To call tools, reply with ONLY this XML, without prose or markdown fences:\n")
	b.WriteString(`<tool_calls><tool_call><tool_name>NAME</tool_name><parameters>{"arg":"value"}</parameters></tool_call></tool_calls>` + "\n")
	b.WriteString("<parameters> must hold a JSON object of argument VALUES that satisfies the tool's parameters schema. Never copy the schema itself (type/properties/required) into <parameters>.\n")
	b.WriteString("For several calls, put multiple <tool_call> elements inside one <tool_calls>.\n")
	switch policy.Mode {
	case ChoiceForced:
		b.WriteString("You must call the tool " + policy.Name + " now.\n")
	case ChoiceRequired:
		b.WriteString("You must call at least one tool now.\n")
	default:
		b.WriteString("If no tool is needed, answer normally without any XML.\n")
	}
	b.WriteString("Tool results come back as \"Tool result\" lines; use them to continue.\n")
	b.WriteString("Available tools:\n")
	for _, meta := range metas {
		b.WriteString("Tool: ")
		b.WriteString(meta.Name)
		b.WriteByte('\n')
		if meta.Description != "" {
			b.WriteString("Description: ")
			b.WriteString(meta.Description)
			b.WriteByte('\n')
		}
		if schemaJSON := compactJSON(meta.Schema); schemaJSON != "" {
			b.WriteString("Parameters schema: ")
			b.WriteString(schemaJSON)
			b.WriteByte('\n')
		}
		b.WriteString("Example call: <tool_calls><tool_call><tool_name>")
		b.WriteString(meta.Name)
		b.WriteString("</tool_name><parameters>")
		b.WriteString(compactJSON(exampleArguments(meta.Schema)))
		b.WriteString("</parameters></tool_call></tool_calls>\n")
	}
	return strings.TrimSpace(b.String())
}

// FormatCallsXML renders calls in the same XML format the prompt teaches, used
// to replay assistant tool calls from conversation history.
func FormatCallsXML(calls []ParsedCall) string {
	if len(calls) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("<tool_calls>")
	for _, call := range calls {
		b.WriteString("<tool_call><tool_name>")
		b.WriteString(call.Name)
		b.WriteString("</tool_name><parameters>")
		b.WriteString(compactJSON(cloneMap(call.Input)))
		b.WriteString("</parameters></tool_call>")
	}
	b.WriteString("</tool_calls>")
	return b.String()
}

// exampleArguments builds a placeholder argument object shaped by the schema,
// so the model sees argument values rather than the schema structure.
func exampleArguments(schema any) map[string]any {
	out := map[string]any{}
	schemaMap, _ := schema.(map[string]any)
	props, _ := schemaMap["properties"].(map[string]any)
	for key, prop := range props {
		propSchema, _ := prop.(map[string]any)
		out[key] = exampleValue(propSchema)
	}
	return out
}

func exampleValue(schema map[string]any) any {
	if enumValues, ok := schema["enum"].([]any); ok && len(enumValues) > 0 {
		return enumValues[0]
	}
	if constValue, ok := schema["const"]; ok {
		return constValue
	}
	switch schemaTypeName(schema) {
	case "integer", "number":
		return 0
	case "boolean":
		return false
	case "array":
		return []any{}
	case "object":
		return exampleArguments(schema)
	default:
		return "..."
	}
}

func schemaTypeName(schema map[string]any) string {
	switch v := schema["type"].(type) {
	case string:
		return v
	case []any:
		for _, item := range v {
			if name, ok := item.(string); ok && name != "null" {
				return name
			}
		}
	}
	return ""
}

func ExtractToolMeta(tool map[string]any) (string, string, any) {
	if fn, ok := tool["function"].(map[string]any); ok {
		return strings.TrimSpace(asString(fn["name"])), strings.TrimSpace(asString(fn["description"])), firstNonNil(fn["parameters"], fn["inputSchema"], fn["schema"])
	}
	return strings.TrimSpace(asString(tool["name"])), strings.TrimSpace(asString(tool["description"])), firstNonNil(tool["input_schema"], tool["inputSchema"], tool["schema"], tool["parameters"])
}

func ToolNames(tools any) []string {
	metas := toolMetas(tools)
	out := make([]string, 0, len(metas))
	for _, meta := range metas {
		if meta.Name != "" {
			out = append(out, meta.Name)
		}
	}
	return out
}

type ToolMeta struct {
	Name        string
	Description string
	Schema      any
}

func toolMetas(tools any) []ToolMeta {
	items := toToolMaps(tools)
	out := make([]ToolMeta, 0, len(items))
	for _, item := range items {
		name, description, schema := ExtractToolMeta(item)
		if name == "" {
			continue
		}
		out = append(out, ToolMeta{Name: name, Description: description, Schema: schema})
	}
	return out
}

func toToolMaps(tools any) []map[string]any {
	switch v := tools.(type) {
	case []map[string]any:
		return v
	case []any:
		out := make([]map[string]any, 0, len(v))
		for _, item := range v {
			if m, ok := item.(map[string]any); ok {
				out = append(out, m)
			}
		}
		return out
	case map[string]any:
		return []map[string]any{v}
	default:
		return nil
	}
}

func compactJSON(v any) string {
	if v == nil {
		return ""
	}
	buf, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return string(buf)
}

func firstNonNil(values ...any) any {
	for _, value := range values {
		if value != nil {
			return value
		}
	}
	return nil
}
