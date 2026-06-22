package backend

import (
	"encoding/base64"
	"encoding/json"
	"log"
	"math/rand"
	"reflect"
	"time"
)

// solveSentinelDxToken decrypts and executes a Sentinel dx VM challenge.
// dx is the encrypted VM bytecode from the prepare response (so.collector_dx).
// proofKey is the raw PoW answer (stripped of "gAAAAAB" prefix and "~S" suffix),
// used as the XOR decryption key — matching the SDK's WeakMap binding of cachedProof.
//
// Returns the base64-encoded VM execution result, or "" on failure.
func solveSentinelDxToken(dx, proofKey string) string {
	decoded, err := base64.StdEncoding.DecodeString(dx)
	if err != nil {
		log.Printf("sentinel_dx: base64 decode FAILED — %v (dx len=%d, proofKey len=%d)", err, len(dx), len(proofKey))
		return ""
	}
	xorResult := xorTurnstileString(string(decoded), proofKey)
	var tokenList [][]any
	if err := json.Unmarshal([]byte(xorResult), &tokenList); err != nil {
		preview := xorResult
		if len(preview) > 200 {
			preview = preview[:200]
		}
		log.Printf("sentinel_dx: JSON parse FAILED — %v (xorResult preview: %q)", err, preview)
		return ""
	}
	log.Printf("sentinel_dx: VM start — %d instructions, proofKey len=%d", len(tokenList), len(proofKey))

	process := map[int]any{}
	start := time.Now()
	result := ""
	get := func(value any) any {
		return process[turnstileKey(value)]
	}
	set := func(key any, value any) {
		process[turnstileKey(key)] = value
	}
	call := func(value any, args ...any) {
		if fn, ok := value.(turnstileFunc); ok {
			fn(args...)
		}
	}

	// Opcode table — mirrors the Turnstile VM (shared SentinelSDK instruction set).
	// Opcode 16 uses proofKey (PoW answer) instead of the legacy p token.

	// [1] XOR operation: set(dest, xor(get(srcA), get(srcB)))
	process[1] = turnstileFunc(func(args ...any) {
		if len(args) < 2 {
			return
		}
		set(args[0], xorTurnstileString(turnstileToString(get(args[0])), turnstileToString(get(args[1]))))
	})

	// [2] Set literal value
	process[2] = turnstileFunc(func(args ...any) {
		if len(args) < 2 {
			return
		}
		set(args[0], args[1])
	})

	// [3] Resolve / finalize: base64-encode value as result
	process[3] = turnstileFunc(func(args ...any) {
		if len(args) == 0 {
			return
		}
		result = base64.StdEncoding.EncodeToString([]byte(turnstileToString(args[0])))
	})

	// [5] Concatenate / append
	process[5] = turnstileFunc(func(args ...any) {
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

	// [6] Browser property access via dot notation
	process[6] = turnstileFunc(func(args ...any) {
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

	// [7] Call with resolved arguments
	process[7] = turnstileFunc(func(args ...any) {
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

	// [8] Copy register
	process[8] = turnstileFunc(func(args ...any) {
		if len(args) < 2 {
			return
		}
		set(args[0], get(args[1]))
	})

	// [9] Instruction queue — the full decoded instruction list
	process[9] = tokenList

	// [10] Constant string "window"
	process[10] = "window"

	// [14] JSON.parse
	process[14] = turnstileFunc(func(args ...any) {
		if len(args) < 2 {
			return
		}
		var value any
		if json.Unmarshal([]byte(turnstileToString(get(args[1]))), &value) == nil {
			set(args[0], value)
		}
	})

	// [15] JSON.stringify
	process[15] = turnstileFunc(func(args ...any) {
		if len(args) < 2 {
			return
		}
		data, err := json.Marshal(get(args[1]))
		if err == nil {
			set(args[0], string(data))
		}
	})

	// [16] XOR key — PoW proof answer (NOT the legacy p token)
	process[16] = proofKey

	// [17] Simulated browser / JS runtime API calls
	process[17] = turnstileFunc(func(args ...any) {
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

	// [18] Base64 decode (atob)
	process[18] = turnstileFunc(func(args ...any) {
		if len(args) < 1 {
			return
		}
		data, err := base64.StdEncoding.DecodeString(turnstileToString(get(args[0])))
		if err == nil {
			set(args[0], string(data))
		}
	})

	// [19] Base64 encode (btoa)
	process[19] = turnstileFunc(func(args ...any) {
		if len(args) < 1 {
			return
		}
		set(args[0], base64.StdEncoding.EncodeToString([]byte(turnstileToString(get(args[0])))))
	})

	// [20] Conditional call (equality check)
	process[20] = turnstileFunc(func(args ...any) {
		if len(args) < 3 || !reflect.DeepEqual(get(args[0]), get(args[1])) {
			return
		}
		callArgs := make([]any, 0, len(args)-3)
		for _, arg := range args[3:] {
			callArgs = append(callArgs, get(arg))
		}
		call(get(args[2]), callArgs...)
	})

	// [21] No-op
	process[21] = turnstileFunc(func(args ...any) {})

	// [23] Call with raw (unresolved) arguments
	process[23] = turnstileFunc(func(args ...any) {
		if len(args) < 2 || get(args[0]) == nil {
			return
		}
		call(get(args[1]), args[2:]...)
	})

	// [24] Dot-join two strings
	process[24] = turnstileFunc(func(args ...any) {
		if len(args) < 3 {
			return
		}
		left, leftOK := get(args[1]).(string)
		right, rightOK := get(args[2]).(string)
		if leftOK && rightOK {
			set(args[0], left+"."+right)
		}
	})

	// Execution loop
	unknownOps := map[int]bool{}
	for _, token := range tokenList {
		if len(token) == 0 {
			continue
		}
		key := turnstileKey(token[0])
		if _, exists := process[key]; !exists {
			if !unknownOps[key] {
				unknownOps[key] = true
				log.Printf("sentinel_dx: unknown opcode %d (instruction: %v)", key, token)
			}
			continue
		}
		call(process[key], token[1:]...)
	}
	if result == "" {
		log.Printf("sentinel_dx: VM executed %d instructions but result is EMPTY (no opcode 3 Resolve?)", len(tokenList))
	}
	return result
}
