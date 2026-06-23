package backend

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math"
	"math/rand"
	"reflect"
	"strconv"
	"strings"
	"time"
)

type turnstileFunc func(args ...any)

type turnstileOrderedMap struct {
	keys   []string
	values map[string]any
}

func (m *turnstileOrderedMap) add(key string, value any) {
	if m.values == nil {
		m.values = map[string]any{}
	}
	if _, ok := m.values[key]; !ok {
		m.keys = append(m.keys, key)
	}
	m.values[key] = value
}

// toJSON serializes the ordered map to a JSON object string,
// preserving insertion order of keys.
func (m *turnstileOrderedMap) toJSON() string {
	if m == nil || len(m.keys) == 0 {
		return "{}"
	}
	// Simple ordered JSON — build manually to preserve key order
	out := "{"
	for i, k := range m.keys {
		if i > 0 {
			out += ","
		}
		v := m.values[k]
		vBytes, err := json.Marshal(v)
		if err != nil || len(vBytes) == 0 {
			vBytes = []byte("null")
		}
		kBytes, _ := json.Marshal(k)
		out += string(kBytes) + ":" + string(vBytes)
	}
	out += "}"
	return out
}

func solveTurnstileToken(dx, p string) string {
	decoded, err := base64.StdEncoding.DecodeString(dx)
	if err != nil {
		return ""
	}
	var tokenList [][]any
	if err := json.Unmarshal([]byte(xorTurnstileString(string(decoded), p)), &tokenList); err != nil {
		return ""
	}

	process := map[any]any{}
	start := time.Now()
	result := ""
	get := func(value any) any {
		return process[value]
	}
	set := func(key any, value any) {
		process[key] = value
	}
	call := func(value any, args ...any) {
		if fn, ok := value.(turnstileFunc); ok {
			fn(args...)
		}
	}

	process[float64(1)] = turnstileFunc(func(args ...any) {
		if len(args) < 2 {
			return
		}
		set(args[0], xorTurnstileString(turnstileToString(get(args[0])), turnstileToString(get(args[1]))))
	})
	process[float64(2)] = turnstileFunc(func(args ...any) {
		if len(args) < 2 {
			return
		}
		set(args[0], args[1])
	})
	process[float64(3)] = turnstileFunc(func(args ...any) {
		if len(args) == 0 {
			return
		}
		result = base64.StdEncoding.EncodeToString([]byte(turnstileToString(args[0])))
	})
	// [4] Reject / error
	process[float64(4)] = turnstileFunc(func(args ...any) {
		if len(args) == 0 {
			return
		}
		result = base64.StdEncoding.EncodeToString([]byte(turnstileToString(args[0])))
	})
	process[float64(5)] = turnstileFunc(func(args ...any) {
		if len(args) < 2 {
			return
		}
		current := get(args[0])
		incoming := get(args[1])
		if list, ok := current.([]any); ok {
			set(args[0], append(list, incoming))
			return
		}
		if _, ok := current.(string); ok {
			set(args[0], turnstileToString(current)+turnstileToString(incoming))
			return
		}
		if _, ok := current.(float64); ok {
			set(args[0], turnstileToString(current)+turnstileToString(incoming))
			return
		}
		if _, ok := incoming.(string); ok {
			set(args[0], turnstileToString(current)+turnstileToString(incoming))
			return
		}
		if _, ok := incoming.(float64); ok {
			set(args[0], turnstileToString(current)+turnstileToString(incoming))
			return
		}
		set(args[0], "NaN")
	})
	process[float64(6)] = turnstileFunc(func(args ...any) {
		if len(args) < 3 {
			return
		}
		left, leftOK := get(args[1]).(string)
		right, rightOK := get(args[2]).(string)
		if !leftOK || !rightOK {
			return
		}
		value := left + "." + right
		if value == "window.document.location" {
			value = "https://chatgpt.com/"
		}
		set(args[0], value)
	})
	process[float64(7)] = turnstileFunc(func(args ...any) {
		if len(args) < 1 {
			return
		}
		target := get(args[0])
		values := make([]any, 0, len(args)-1)
		for _, arg := range args[1:] {
			values = append(values, get(arg))
		}
		if target == "window.Reflect.set" && len(values) >= 3 {
			if obj, ok := values[0].(*turnstileOrderedMap); ok {
				obj.add(turnstileToString(values[1]), values[2])
			}
			return
		}
		call(target, values...)
	})
	process[float64(8)] = turnstileFunc(func(args ...any) {
		if len(args) < 2 {
			return
		}
		set(args[0], get(args[1]))
	})
	process[float64(9)] = tokenList
	process[float64(10)] = "window"
	// [11] document.scripts regex match
	process[float64(11)] = turnstileFunc(func(args ...any) {
		if len(args) < 2 {
			return
		}
		set(args[0], nil)
	})
	// [12] Map self-reference
	process[float64(12)] = turnstileFunc(func(args ...any) {
		set(args[0], process)
	})
	// [13] Void function call with try/catch
	process[float64(13)] = turnstileFunc(func(args ...any) {
		if len(args) < 2 {
			return
		}
		fn := get(args[1])
		if fn, ok := fn.(turnstileFunc); ok {
			func() {
				defer func() {
					if r := recover(); r != nil {
						set(args[0], turnstileToString(r))
					}
				}()
				fn(args[2:]...)
			}()
		}
	})
	process[float64(14)] = turnstileFunc(func(args ...any) {
		if len(args) < 2 {
			return
		}
		var value any
		if json.Unmarshal([]byte(turnstileToString(get(args[1]))), &value) == nil {
			set(args[0], value)
		}
	})
	process[float64(15)] = turnstileFunc(func(args ...any) {
		if len(args) < 2 {
			return
		}
		data, err := json.Marshal(get(args[1]))
		if err == nil {
			set(args[0], string(data))
		}
	})
	process[float64(16)] = p
	process[float64(17)] = turnstileFunc(func(args ...any) {
		if len(args) < 2 {
			return
		}
		callArgs := make([]any, 0, len(args)-2)
		for _, arg := range args[2:] {
			callArgs = append(callArgs, get(arg))
		}
		switch get(args[1]) {
		case "window.performance.now":
			elapsed := float64(time.Since(start).Nanoseconds()) + rand.Float64()
			set(args[0], elapsed/1e6)
		case "window.Object.create":
			set(args[0], &turnstileOrderedMap{})
		case "window.Object.keys":
			if len(callArgs) > 0 && callArgs[0] == "window.localStorage" {
				set(args[0], []string{
					"STATSIG_LOCAL_STORAGE_INTERNAL_STORE_V4",
					"STATSIG_LOCAL_STORAGE_STABLE_ID",
					"client-correlated-secret",
					"oai/apps/capExpiresAt",
					"oai-did",
					"STATSIG_LOCAL_STORAGE_LOGGING_REQUEST",
					"UiState.isNavigationCollapsed.1",
				})
			}
		case "window.Math.random":
			set(args[0], rand.Float64())
		default:
			call(get(args[1]), callArgs...)
		}
	})
	process[float64(18)] = turnstileFunc(func(args ...any) {
		if len(args) < 1 {
			return
		}
		data, err := base64.StdEncoding.DecodeString(turnstileToString(get(args[0])))
		if err == nil {
			set(args[0], string(data))
		}
	})
	process[float64(19)] = turnstileFunc(func(args ...any) {
		if len(args) < 1 {
			return
		}
		set(args[0], base64.StdEncoding.EncodeToString([]byte(turnstileToString(get(args[0])))))
	})
	process[float64(20)] = turnstileFunc(func(args ...any) {
		if len(args) < 3 || !reflect.DeepEqual(get(args[0]), get(args[1])) {
			return
		}
		callArgs := make([]any, 0, len(args)-3)
		for _, arg := range args[3:] {
			callArgs = append(callArgs, get(arg))
		}
		call(get(args[2]), callArgs...)
	})
	// [21] Distance threshold conditional call
	process[float64(21)] = turnstileFunc(func(args ...any) {
		if len(args) < 4 {
			return
		}
		a := turnstileToFloat(get(args[0]))
		b := turnstileToFloat(get(args[1]))
		threshold := turnstileToFloat(get(args[2]))
		if math.Abs(a-b) > threshold {
			callArgs := make([]any, 0, len(args)-4)
			for _, arg := range args[4:] {
				callArgs = append(callArgs, get(arg))
			}
			call(get(args[3]), callArgs...)
		}
	})
	process[float64(23)] = turnstileFunc(func(args ...any) {
		if len(args) < 2 || get(args[0]) == nil {
			return
		}
		call(get(args[1]), args[2:]...)
	})
	process[float64(24)] = turnstileFunc(func(args ...any) {
		if len(args) < 3 {
			return
		}
		left, leftOK := get(args[1]).(string)
		right, rightOK := get(args[2]).(string)
		if leftOK && rightOK {
			set(args[0], left+"."+right)
		}
	})

	// [0] Recursive Sentinel entry
	process[float64(0)] = turnstileFunc(func(args ...any) {
		if len(args) == 0 {
			return
		}
		encrypted := turnstileToString(args[0])
		key := turnstileToString(process[float64(16)])
		decoded, err := base64.StdEncoding.DecodeString(encrypted)
		if err != nil {
			return
		}
		xorResult := xorTurnstileString(string(decoded), key)
		var subTokens [][]any
		if err := json.Unmarshal([]byte(xorResult), &subTokens); err != nil {
			return
		}
		savedTokens := process[float64(9)]
		savedResult := result
		result = ""
		process[float64(9)] = subTokens
		for _, token := range subTokens {
			if len(token) == 0 {
				continue
			}
			if fn, ok := process[token[0]].(turnstileFunc); ok {
				fn(token[1:]...)
			}
		}
		set(args[0], result)
		result = savedResult
		process[float64(9)] = savedTokens
	})

	// [22] Sub-VM execution
	process[float64(22)] = turnstileFunc(func(args ...any) {
		if len(args) < 2 {
			return
		}
		destReg := args[0]
		subInstructions, ok := args[1].([]any)
		if !ok {
			return
		}
		subTokens := make([][]any, 0, len(subInstructions))
		for _, inst := range subInstructions {
			if arr, ok := inst.([]any); ok {
				subTokens = append(subTokens, arr)
			}
		}
		savedTokens := process[float64(9)]
		savedResult := result
		result = ""
		process[float64(9)] = subTokens
		for _, token := range subTokens {
			if len(token) == 0 {
				continue
			}
			if fn, ok := process[token[0]].(turnstileFunc); ok {
				fn(token[1:]...)
			}
		}
		set(destReg, result)
		result = savedResult
		process[float64(9)] = savedTokens
	})

	// [25] Noop (mt)
	process[float64(25)] = turnstileFunc(func(args ...any) {})

	// [26] Noop (wt)
	process[float64(26)] = turnstileFunc(func(args ...any) {})

	// [27] Array splice or numeric subtraction
	process[float64(27)] = turnstileFunc(func(args ...any) {
		if len(args) < 2 {
			return
		}
		target := get(args[0])
		value := get(args[1])
		if list, ok := target.([]any); ok {
			for i, item := range list {
				if reflect.DeepEqual(item, value) {
					set(args[0], append(list[:i], list[i+1:]...))
					return
				}
			}
			return
		}
		a := turnstileToFloat(target)
		b := turnstileToFloat(value)
		set(args[0], a-b)
	})

	// [28] Noop (gt)
	process[float64(28)] = turnstileFunc(func(args ...any) {})

	// [29] Less than comparison
	process[float64(29)] = turnstileFunc(func(args ...any) {
		if len(args) < 3 {
			return
		}
		a := turnstileToFloat(get(args[1]))
		b := turnstileToFloat(get(args[2]))
		set(args[0], a < b)
	})

	// [30] Function definition
	process[float64(30)] = turnstileFunc(func(args ...any) {
		if len(args) < 3 {
			return
		}
		destReg := turnstileKey(args[0])
		returnReg := turnstileKey(args[1])

		var bindings []any
		var body []any

		if len(args) >= 4 {
			if bindingsRaw, ok := args[2].([]any); ok {
				bodyRaw, _ := args[3].([]any)
				bindings = make([]any, 0, len(bindingsRaw))
				for _, b := range bindingsRaw {
					bindings = append(bindings, turnstileKey(b))
				}
				body = bodyRaw
			} else if bodyRaw, ok := args[2].([]any); ok {
				body = bodyRaw
			}
		} else if bodyRaw, ok := args[2].([]any); ok {
			body = bodyRaw
		}

		if body == nil {
			return
		}

		capturedProcess := process

		createdFn := turnstileFunc(func(callArgs ...any) {
			savedTokens := capturedProcess[float64(9)]
			savedResult := result

			for i, reg := range bindings {
				if i < len(callArgs) {
					capturedProcess[reg] = callArgs[i]
				}
			}

			subTokens := make([][]any, 0, len(body))
			for _, inst := range body {
				if arr, ok := inst.([]any); ok {
					subTokens = append(subTokens, arr)
				}
			}

			result = ""
			capturedProcess[float64(9)] = subTokens
			for _, token := range subTokens {
				if len(token) == 0 {
					continue
				}
				key := turnstileKey(token[0])
				if fn, exists := capturedProcess[key]; exists {
					if f, ok := fn.(turnstileFunc); ok {
						f(token[1:]...)
					}
				}
			}

			subResult := result
			result = savedResult
			capturedProcess[float64(9)] = savedTokens

			if subResult != "" {
				capturedProcess[returnReg] = subResult
			}
		})

		process[destReg] = createdFn
	})

	// [33] Multiplication: a * b
	process[float64(33)] = turnstileFunc(func(args ...any) {
		if len(args) < 3 {
			return
		}
		a := turnstileToFloat(get(args[1]))
		b := turnstileToFloat(get(args[2]))
		set(args[0], a*b)
	})

	// [34] Promise resolve (synchronous in Go)
	process[float64(34)] = turnstileFunc(func(args ...any) {
		if len(args) < 2 {
			return
		}
		value := get(args[1])
		if fn, ok := value.(turnstileFunc); ok {
			fn()
			return
		}
		set(args[0], value)
	})

	// [35] Division: a / b (zero → 0)
	process[float64(35)] = turnstileFunc(func(args ...any) {
		if len(args) < 3 {
			return
		}
		a := turnstileToFloat(get(args[1]))
		b := turnstileToFloat(get(args[2]))
		if b == 0 {
			set(args[0], float64(0))
		} else {
			set(args[0], a/b)
		}
	})

	for _, token := range tokenList {
		if len(token) == 0 {
			continue
		}
		if fn, ok := process[token[0]].(turnstileFunc); ok {
			fn(token[1:]...)
		}
	}
	return result
}

// turnstileKey normalizes a VM register key for lookup in the process map.
// SDK's At Map uses JavaScript numbers as keys — integer opcodes (0-35) and
// floating-point register addresses (90.67, 95.87, …) coexist in the same Map.
// In Go, json.Unmarshal into []any produces float64 for all numbers, so we
// preserve float64 keys exactly. Integer-valued floats in the opcode range
// (0-255) are normalized to float64(int(v)) for consistency with Go int→float64.
func turnstileKey(value any) any {
	switch v := value.(type) {
	case float64:
		if v == float64(int(v)) && int(v) >= 0 && int(v) <= 255 {
			return float64(int(v))
		}
		return v
	case int:
		return float64(v)
	case json.Number:
		f, _ := strconv.ParseFloat(v.String(), 64)
		if f == float64(int(f)) && int(f) >= 0 && int(f) <= 255 {
			return float64(int(f))
		}
		return f
	default:
		return value
	}
}

func turnstileToString(value any) string {
	switch v := value.(type) {
	case nil:
		return "undefined"
	case float64:
		if math.Trunc(v) == v {
			return strconv.FormatFloat(v, 'f', 1, 64)
		}
		return strconv.FormatFloat(v, 'f', -1, 64)
	case string:
		switch v {
		case "window.Math":
			return "[object Math]"
		case "window.Reflect":
			return "[object Reflect]"
		case "window.performance":
			return "[object Performance]"
		case "window.localStorage":
			return "[object Storage]"
		case "window.Object":
			return "function Object() { [native code] }"
		case "window.Reflect.set":
			return "function set() { [native code] }"
		case "window.performance.now":
			return "function () { [native code] }"
		case "window.Object.create":
			return "function create() { [native code] }"
		case "window.Object.keys":
			return "function keys() { [native code] }"
		case "window.Math.random":
			return "function random() { [native code] }"
		default:
			return v
		}
	case []string:
		return strings.Join(v, ",")
	case []any:
		parts := make([]string, 0, len(v))
		for _, item := range v {
			text, ok := item.(string)
			if !ok {
				return fmt.Sprint(value)
			}
			parts = append(parts, text)
		}
		return strings.Join(parts, ",")
	default:
		return fmt.Sprint(value)
	}
}

func turnstileToFloat(value any) float64 {
	switch v := value.(type) {
	case float64:
		return v
	case int:
		return float64(v)
	case json.Number:
		f, _ := strconv.ParseFloat(v.String(), 64)
		return f
	case string:
		f, _ := strconv.ParseFloat(v, 64)
		return f
	case bool:
		if v {
			return 1
		}
		return 0
	default:
		return 0
	}
}

func xorTurnstileString(text, key string) string {
	if key == "" {
		return text
	}
	var out strings.Builder
	keyRunes := []rune(key)
	for index, ch := range text {
		out.WriteRune(ch ^ keyRunes[index%len(keyRunes)])
	}
	return out.String()
}
