package backend

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"math/rand"
	"os"
	"reflect"
	"time"
)

// logSOEvent appends a structured JSON event line to data/logs/so_events.log.
// Used to persist sparse but critical so-related events (so_token_present,
// unknown_opcode, dx_token_anomaly) that would otherwise scroll out of journalctl.
// Write failures are silently ignored — this is best-effort diagnostics.
func logSOEvent(event string, detail map[string]any) {
	os.MkdirAll("data/logs", 0755)
	f, err := os.OpenFile("data/logs/so_events.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer f.Close()
	entry := map[string]any{
		"ts":     time.Now().UTC().Format(time.RFC3339),
		"event":  event,
		"detail": detail,
	}
	data, _ := json.Marshal(entry)
	f.Write(append(data, '\n'))
}

// initSimWindow pre-populates the simulated browser window object with
// realistic __oai_so_* values before the VM starts executing.
// This mimics the work done by sentinel SDK event listeners in a real browser.
func initSimWindow(w *turnstileOrderedMap) {
	// Type A: Null fields — event listeners registered but never triggered
	nullFields := []string{
		"__oai_so_h", "__oai_so_hi", "__oai_so_hp", "__oai_so_hw",
		"__oai_so_ht", "__oai_so_hc",
		"__oai_so_s", "__oai_so_t0",
		"__oai_so_k", "__oai_so_kp",
		"__oai_so_p", "__oai_so_pc",
		"__oai_so_fs", "__oai_so_fs2", "__oai_so_fn",
		"__oai_so_bc", "__oai_so_bm",
	}
	for _, f := range nullFields {
		w.add(f, nil)
	}

	// Type B: Interaction data fields — synthesize realistic values
	wl := 500.0 + rand.Float64()*2000.0 // window load perf.now: 0.5-2.5s
	w.add("__oai_so_wl", wl)

	m := wl + 5000.0 + rand.Float64()*295000.0 // mouse move: 5-300s after load
	w.add("__oai_so_m", m)

	ss := wl + 1000.0 + rand.Float64()*(m-wl-1000.0) // scroll: 1s to mouse-time
	w.add("__oai_so_ss", ss)

	pageLoadDateNow := float64(time.Now().UnixMilli()) - wl
	w.add("__oai_so_ss2", pageLoadDateNow+ss)

	sn := float64(10 + rand.Intn(191)) // scroll count: 10-200
	w.add("__oai_so_sn", sn)

	cs := wl + 500.0 + rand.Float64()*(ss-wl-500.0) // click: 0.5s to scroll-time
	w.add("__oai_so_cs", cs)
	w.add("__oai_so_cs2", pageLoadDateNow+cs)

	cn := float64(3 + rand.Intn(98)) // click count: 3-100
	w.add("__oai_so_cn", cn)

	w.add("__oai_so_st", float64(rand.Intn(1001))) // scrollTop: 0-1000
	w.add("__oai_so_sw", float64(rand.Intn(100)))  // scrollWidth: 0-99
	w.add("__oai_so_sp", float64(0))               // scrollParent: always 0
	w.add("__oai_so_spt", float64(rand.Intn(5)))   // scrollParentTop: 0-4

	sx0 := float64(rand.Intn(1920)) // start mouse x: 0-1919
	sy0 := float64(rand.Intn(1080)) // start mouse y: 0-1079
	w.add("__oai_so_sx0", sx0)
	w.add("__oai_so_sy0", sy0)
	w.add("__oai_so_lx", sx0+rand.Float64()*200.0-100.0) // last x: start ± 100
	w.add("__oai_so_ly", sy0+rand.Float64()*200.0-100.0) // last y: start ± 100

	w.add("__oai_so_i", sn+cn+float64(5+rand.Intn(46))) // input total: sn+cn+5~50
	w.add("__oai_so_we", float64(1+rand.Intn(20)))      // window events: 1-20
	w.add("__oai_so_wb", float64(rand.Intn(4)))          // blur: 0-3
}

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

	process := map[any]any{}
	simWindow := &turnstileOrderedMap{} // simulated browser window for Reflect.set
	initSimWindow(simWindow)
	start := time.Now()
	result := ""
	instrIdx := 0

	// traceValue returns a compact one-line summary of a VM value for trace logging.
	traceValue := func(v any) string {
		if v == nil {
			return "nil"
		}
		switch x := v.(type) {
		case turnstileFunc:
			return "fn"
		case string:
			if len(x) > 40 {
				return fmt.Sprintf("str(%q...)", x[:40])
			}
			return fmt.Sprintf("str(%q)", x)
		case float64:
			return fmt.Sprintf("num(%v)", x)
		case bool:
			return fmt.Sprintf("bool(%v)", x)
		case []any:
			return fmt.Sprintf("arr(%d)", len(x))
		case []string:
			return fmt.Sprintf("strs(%d)", len(x))
		case *turnstileOrderedMap:
			return fmt.Sprintf("orderedMap(keys=%d)", len(x.keys))
		case map[any]any:
			return "map[self]"
		default:
			return fmt.Sprintf("%T", v)
		}
	}

	get := func(value any) any {
		return process[turnstileKey(value)]
	}
	set := func(key any, value any) {
		k := turnstileKey(key)
		process[k] = value
	}
	call := func(value any, args ...any) {
		if fn, ok := value.(turnstileFunc); ok {
			fn(args...)
		}
	}

	// Opcode table — mirrors the Turnstile VM (shared SentinelSDK instruction set).
	// Opcode 16 uses proofKey (PoW answer) instead of the legacy p token.

	// [1] XOR operation: set(dest, xor(get(srcA), get(srcB)))
	process[float64(1)] = turnstileFunc(func(args ...any) {
		if len(args) < 2 {
			return
		}
		set(args[0], xorTurnstileString(turnstileToString(get(args[0])), turnstileToString(get(args[1]))))
	})

	// [2] Set literal value
	process[float64(2)] = turnstileFunc(func(args ...any) {
		if len(args) < 2 {
			return
		}
		set(args[0], args[1])
	})

	// [3] Resolve / finalize: base64-encode value as result
	process[float64(3)] = turnstileFunc(func(args ...any) {
		if len(args) == 0 {
			return
		}
		// Resolve register reference. args[0] may be:
		// - float64 register key (direct dispatch) → get() resolves it
		// - nil (already resolved by opcode 7 indirection) → skip get()
		// - string literal (edge case) → skip get()
		v := get(args[0])
		if v == nil {
			// Register is uninitialized — fall back to simulated window
			if _, isRegKey := args[0].(float64); isRegKey || args[0] == nil {
				v = simWindow.toJSON()
			} else {
				v = args[0] // literal string value
			}
		}
		result = base64.StdEncoding.EncodeToString([]byte(turnstileToString(v)))
	})

	// [4] Reject / error — log and output error as btoa
	process[float64(4)] = turnstileFunc(func(args ...any) {
		if len(args) == 0 {
			return
		}
		errVal := turnstileToString(args[0])
		log.Printf("sentinel_dx: opcode 4 Reject — %s", errVal)
		result = base64.StdEncoding.EncodeToString([]byte(errVal))
	})

	// [5] Concatenate / append
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

	// [6] Browser property access via dot notation
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

	// [7] Call with resolved arguments
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
			// Browser Reflect.set(target, key, value) — target may be:
			// - the string "window" (register 10 value) → write to simWindow
			// - an *turnstileOrderedMap (Object.create result) → write to that map
			if obj, ok := values[0].(*turnstileOrderedMap); ok {
				obj.add(turnstileToString(values[1]), values[2])
			} else if values[0] == "window" {
				simWindow.add(turnstileToString(values[1]), values[2])
			}
			return
		}
		call(target, values...)
	})

	// [8] Copy register
	process[float64(8)] = turnstileFunc(func(args ...any) {
		if len(args) < 2 {
			return
		}
		set(args[0], get(args[1]))
	})

	// [9] Instruction queue — the full decoded instruction list
	process[float64(9)] = tokenList

	// [10] Constant string "window"
	process[float64(10)] = "window"

	// [11] document.scripts regex match — search for script src matching pattern
	process[float64(11)] = turnstileFunc(func(args ...any) {
		if len(args) < 2 {
			return
		}
		// Simulated: return nil (no matching script) in VM context
		set(args[0], nil)
	})

	// [12] Map self-reference — store the process map itself
	process[float64(12)] = turnstileFunc(func(args ...any) {
		set(args[0], process)
	})

	// [13] Void function call with try/catch — error goes to target, raw args
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

	// [14] JSON.parse
	process[float64(14)] = turnstileFunc(func(args ...any) {
		if len(args) < 2 {
			return
		}
		var value any
		if json.Unmarshal([]byte(turnstileToString(get(args[1]))), &value) == nil {
			set(args[0], value)
		}
	})

	// [15] JSON.stringify
	process[float64(15)] = turnstileFunc(func(args ...any) {
		if len(args) < 2 {
			return
		}
		data, err := json.Marshal(get(args[1]))
		if err == nil {
			set(args[0], string(data))
		}
	})

	// [16] XOR key — PoW proof answer (NOT the legacy p token)
	process[float64(16)] = proofKey

	// [17] Simulated browser / JS runtime API calls
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
		case "window.Date.now":
			set(args[0], float64(time.Now().UnixMilli()))
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
	process[float64(18)] = turnstileFunc(func(args ...any) {
		if len(args) < 1 {
			return
		}
		data, err := base64.StdEncoding.DecodeString(turnstileToString(get(args[0])))
		if err == nil {
			set(args[0], string(data))
		}
	})

	// [19] Base64 encode (btoa)
	process[float64(19)] = turnstileFunc(func(args ...any) {
		if len(args) < 1 {
			return
		}
		set(args[0], base64.StdEncoding.EncodeToString([]byte(turnstileToString(get(args[0])))))
	})

	// [20] Conditional call (equality check)
	process[float64(20)] = turnstileFunc(func(args ...any) {
		a := get(args[0])
		b := get(args[1])
		if len(args) < 3 || !reflect.DeepEqual(a, b) {
			return
		}
		callArgs := make([]any, 0, len(args)-3)
		for _, arg := range args[3:] {
			callArgs = append(callArgs, get(arg))
		}
		call(get(args[2]), callArgs...)
	})

	// [21] Distance threshold conditional call: if |a-b| > threshold → call fn
	process[float64(21)] = turnstileFunc(func(args ...any) {
		if len(args) < 4 {
			return
		}
		a := turnstileToFloat(get(args[0]))
		b := turnstileToFloat(get(args[1]))
		threshold := turnstileToFloat(get(args[2]))
		diff := math.Abs(a - b)
		if diff > threshold {
			callArgs := make([]any, 0, len(args)-4)
			for _, arg := range args[4:] {
				callArgs = append(callArgs, get(arg))
			}
			call(get(args[3]), callArgs...)
		} else {
		}
	})

	// [23] Call with raw (unresolved) arguments
	process[float64(23)] = turnstileFunc(func(args ...any) {
		v := get(args[0])
		if len(args) < 2 || v == nil {
			return
		}
		call(get(args[1]), args[2:]...)
	})

	// [24] Dot-join two strings
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

	// [0] Recursive Sentinel entry — called via opcode 7 to decrypt+execute a sub-program
	process[float64(0)] = turnstileFunc(func(args ...any) {
		// Takes a base64-encoded encrypted string, decrypts with key from reg 16,
		// and executes as a sub-program.
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
		// Save state and run sub-VM
		savedTokens := process[float64(9)]
		savedResult := result
		result = ""
		process[float64(9)] = subTokens
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
		process[float64(9)] = savedTokens
		// Store result where caller expects it
		set(args[0], subResult)
	})

	// [22] Sub-VM execution — push new instruction queue, execute, restore
	process[float64(22)] = turnstileFunc(func(args ...any) {
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
		savedTokens := process[float64(9)]
		savedResult := result
		// Run sub-VM
		result = ""
		process[float64(9)] = subTokens
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
	process[float64(28)] = turnstileFunc(func(args ...any) {})

	// [29] Less than comparison: a < b → boolean
	process[float64(29)] = turnstileFunc(func(args ...any) {
		if len(args) < 3 {
			return
		}
		a := turnstileToFloat(get(args[1]))
		b := turnstileToFloat(get(args[2]))
		set(args[0], a < b)
	})

	// [30] Function definition — create a dynamic callable with param bindings
	process[float64(30)] = turnstileFunc(func(args ...any) {
		// Forms: (destReg, returnReg, body) or (destReg, returnReg, bindings, body)
		if len(args) < 3 {
			return
		}
		destReg := turnstileKey(args[0])
		returnReg := turnstileKey(args[1])

		var bindings []any
		var body []any

		// Detect 3-arg vs 4-arg form: if args[3] exists, it's the body in 4-arg form
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

		// Capture current process map for the closure
		capturedProcess := process

		// Create the callable
		createdFn := turnstileFunc(func(callArgs ...any) {
			// Save state
			savedTokens := capturedProcess[float64(9)]
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

			// Restore state — result from sub-VM is already in 'result'
			// or stored via opcode 3 in the return register
			subResult := result
			result = savedResult
			capturedProcess[float64(9)] = savedTokens

			// If sub-VM produced a result via opcode 3, store in returnReg
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

	// [34] Promise resolve — synchronously resolve and store value
	process[float64(34)] = turnstileFunc(func(args ...any) {
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

	// Execution loop
	unknownOps := map[any]bool{}
	for _, token := range tokenList {
		if len(token) == 0 {
			continue
		}
		instrIdx++
		key := turnstileKey(token[0])
		if _, exists := process[key]; !exists {
			if !unknownOps[key] {
				unknownOps[key] = true
				log.Printf("sentinel_dx: [%d] UNKNOWN key=%v args=%v", instrIdx, key, token[1:])
				// Persist to so_events.log for later analysis
				argsStr := fmt.Sprint(token[1:])
				if len(argsStr) > 200 {
					argsStr = argsStr[:200]
				}
				dxPreview := result
				if len(dxPreview) > 80 {
					dxPreview = dxPreview[:80]
				}
				pkPrefix := proofKey
				if len(pkPrefix) > 50 {
					pkPrefix = pkPrefix[:50]
				}
				logSOEvent("unknown_opcode", map[string]any{
					"key":                key,
					"args":               argsStr,
					"instruction_index":  instrIdx,
					"dx_token_preview":   dxPreview,
					"proof_key_prefix":   pkPrefix,
				})
			}
			continue
		}
		call(process[key], token[1:]...)
		if result != "" {
			log.Printf("sentinel_dx: [%d] RESULT set: %s", instrIdx, traceValue(result))
			break
		}
	}
	if result == "" {
		log.Printf("sentinel_dx: VM executed %d instructions but result is EMPTY (no opcode 3 Resolve?)", len(tokenList))
	} else {
		log.Printf("sentinel_dx: dxToken output (len=%d): %s", len(result), result)
	}
	return result
}
