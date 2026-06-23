package backend

import (
	"encoding/base64"
	"encoding/json"
	"log"
	"math"
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

	// Diagnostic: preview the XOR-decrypted text to distinguish "valid JSON instructions"
	// (starts with "[[") from XOR key mismatch garbage (binary noise like "kc0\x17...").
	// This is the single most important diagnostic for the XOR stability problem.
	preview := xorResult
	if len(preview) > 120 {
		preview = preview[:120]
	}
	log.Printf("sentinel_dx: xorResult preview: %q", preview)

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

	// [4] Reject / error — log and output error as btoa
	process[4] = turnstileFunc(func(args ...any) {
		if len(args) == 0 {
			return
		}
		errVal := turnstileToString(args[0])
		log.Printf("sentinel_dx: opcode 4 Reject — %s", errVal)
		result = base64.StdEncoding.EncodeToString([]byte(errVal))
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

	// [11] document.scripts regex match — search for script src matching pattern
	process[11] = turnstileFunc(func(args ...any) {
		if len(args) < 2 {
			return
		}
		// Simulated: return nil (no matching script) in VM context
		set(args[0], nil)
	})

	// [12] Map self-reference — store the process map itself
	process[12] = turnstileFunc(func(args ...any) {
		set(args[0], process)
	})

	// [13] Void function call with try/catch — error goes to target, raw args
	process[13] = turnstileFunc(func(args ...any) {
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

	// [21] Distance threshold conditional call: if |a-b| > threshold → call fn
	process[21] = turnstileFunc(func(args ...any) {
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

	// [0] Recursive Sentinel entry — called via opcode 7 to decrypt+execute a sub-program
	process[0] = turnstileFunc(func(args ...any) {
		// Takes a base64-encoded encrypted string, decrypts with key from reg 16,
		// and executes as a sub-program.
		if len(args) == 0 {
			return
		}
		encrypted := turnstileToString(args[0])
		key := turnstileToString(process[16])
		decoded, err := base64.StdEncoding.DecodeString(encrypted)
		if err != nil {
			return
		}
		xorResult := xorTurnstileString(string(decoded), key)
		var subTokens [][]any
		if err := json.Unmarshal([]byte(xorResult), &subTokens); err != nil {
			return
		}
		// Save state and run sub-VM
		savedTokens := process[9]
		savedResult := result
		result = ""
		process[9] = subTokens
		for _, token := range subTokens {
			if len(token) == 0 {
				continue
			}
			key := turnstileKey(token[0])
			if fn, exists := process[key]; exists {
				if f, ok := fn.(turnstileFunc); ok {
					f(token[1:]...)
				}
			}
		}
		// Store sub-result in the next register slot (caller reads via return path)
		subResult := result
		result = savedResult
		process[9] = savedTokens
		// Store result where caller expects it
		set(args[0], subResult)
	})

	// [22] Sub-VM execution — push new instruction queue, execute, restore
	process[22] = turnstileFunc(func(args ...any) {
		if len(args) < 2 {
			return
		}
		destReg := turnstileKey(args[0])
		subInstructions, ok := args[1].([]any)
		if !ok {
			return
		}
		// Convert []any → [][]any
		subTokens := make([][]any, 0, len(subInstructions))
		for _, inst := range subInstructions {
			if arr, ok := inst.([]any); ok {
				subTokens = append(subTokens, arr)
			}
		}
		// Save state
		savedTokens := process[9]
		savedResult := result
		// Run sub-VM
		result = ""
		process[9] = subTokens
		for _, token := range subTokens {
			if len(token) == 0 {
				continue
			}
			key := turnstileKey(token[0])
			if fn, exists := process[key]; exists {
				if f, ok := fn.(turnstileFunc); ok {
					f(token[1:]...)
				}
			}
		}
		// Store sub-result, restore state
		set(destReg, result)
		result = savedResult
		process[9] = savedTokens
	})

	// [25] Noop (mt)
	process[25] = turnstileFunc(func(args ...any) {})

	// [26] Noop (wt)
	process[26] = turnstileFunc(func(args ...any) {})

	// [27] Array splice or numeric subtraction
	process[27] = turnstileFunc(func(args ...any) {
		if len(args) < 2 {
			return
		}
		target := get(args[0])
		value := get(args[1])
		if list, ok := target.([]any); ok {
			// Array splice: remove first occurrence of value
			for i, item := range list {
				if reflect.DeepEqual(item, value) {
					set(args[0], append(list[:i], list[i+1:]...))
					return
				}
			}
			return
		}
		// Numeric subtraction
		a := turnstileToFloat(target)
		b := turnstileToFloat(value)
		set(args[0], a-b)
	})

	// [28] Noop (gt)
	process[28] = turnstileFunc(func(args ...any) {})

	// [29] Less than comparison: a < b → boolean
	process[29] = turnstileFunc(func(args ...any) {
		if len(args) < 3 {
			return
		}
		a := turnstileToFloat(get(args[1]))
		b := turnstileToFloat(get(args[2]))
		set(args[0], a < b)
	})

	// [30] Function definition — create a dynamic callable with param bindings
	process[30] = turnstileFunc(func(args ...any) {
		// Forms: (destReg, returnReg, body) or (destReg, returnReg, bindings, body)
		if len(args) < 3 {
			return
		}
		destReg := turnstileKey(args[0])
		returnReg := turnstileKey(args[1])

		var bindings []int
		var body []any

		// Detect 3-arg vs 4-arg form: if args[3] exists, it's the body in 4-arg form
		if len(args) >= 4 {
			if bindingsRaw, ok := args[2].([]any); ok {
				bodyRaw, _ := args[3].([]any)
				bindings = make([]int, 0, len(bindingsRaw))
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

		// Capture current process map for the closure
		capturedProcess := process

		// Create the callable
		createdFn := turnstileFunc(func(callArgs ...any) {
			// Save state
			savedTokens := capturedProcess[9]
			savedResult := result

			// Bind arguments to registers
			for i, reg := range bindings {
				if i < len(callArgs) {
					capturedProcess[reg] = callArgs[i]
				}
			}

			// Convert body to [][]any
			subTokens := make([][]any, 0, len(body))
			for _, inst := range body {
				if arr, ok := inst.([]any); ok {
					subTokens = append(subTokens, arr)
				}
			}

			// Run sub-VM
			result = ""
			capturedProcess[9] = subTokens
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

			// Restore state — result from sub-VM is already in 'result'
			// or stored via opcode 3 in the return register
			subResult := result
			result = savedResult
			capturedProcess[9] = savedTokens

			// If sub-VM produced a result via opcode 3, store in returnReg
			if subResult != "" {
				capturedProcess[returnReg] = subResult
			}
		})

		process[destReg] = createdFn
	})

	// [33] Multiplication: a * b
	process[33] = turnstileFunc(func(args ...any) {
		if len(args) < 3 {
			return
		}
		a := turnstileToFloat(get(args[1]))
		b := turnstileToFloat(get(args[2]))
		set(args[0], a*b)
	})

	// [34] Promise resolve — synchronously resolve and store value
	process[34] = turnstileFunc(func(args ...any) {
		if len(args) < 2 {
			return
		}
		value := get(args[1])
		// Check if value is a callable that we need to await
		if fn, ok := value.(turnstileFunc); ok {
			// Call it and store the result (synchronous in Go)
			fn()
			// Result would have been stored in some register by the fn
			return
		}
		set(args[0], value)
	})

	// [35] Division: a / b (division-by-zero → 0)
	process[35] = turnstileFunc(func(args ...any) {
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
	} else {
		log.Printf("sentinel_dx: dxToken output (len=%d): %s", len(result), result)
	}
	return result
}
