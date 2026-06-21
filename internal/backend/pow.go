package backend

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math"
	"math/rand"
	"regexp"
	"strings"
	"time"
)

const defaultPOWScript = "https://chatgpt.com/backend-api/sentinel/sdk.js"

var (
	scriptSrcRE = regexp.MustCompile(`(?is)<script[^>]+src=["']([^"']+)["']`)
)

func parsePOWResources(html string) ([]string, string) {
	matches := scriptSrcRE.FindAllStringSubmatch(html, -1)
	sources := make([]string, 0, len(matches))
	dataBuild := ""
	for _, match := range matches {
		src := match[1]
		sources = append(sources, src)
		if dataBuild == "" {
			if hit := regexp.MustCompile(`c/[^/]*/_`).FindString(src); hit != "" {
				dataBuild = hit
			}
		}
	}
	if len(sources) == 0 {
		sources = []string{defaultPOWScript}
	}
	if dataBuild == "" {
		if match := regexp.MustCompile(`<html[^>]*data-build=["']([^"']*)["']`).FindStringSubmatch(html); len(match) > 1 {
			dataBuild = match[1]
		}
	}
	return sources, dataBuild
}

func buildLegacyRequirementsToken(userAgent string, scriptSources []string, dataBuild string, timeOrigin float64) string {
	seed := fmt.Sprintf("%f", rand.Float64())
	config := buildPOWConfig(userAgent, scriptSources, dataBuild, timeOrigin)
	answer, _ := powGenerate(seed, "0fffff", config, 500000)
	return "gAAAAAC" + answer
}

func buildProofToken(seed, difficulty, userAgent string, scriptSources []string, dataBuild string, timeOrigin float64) (string, error) {
	config := buildPOWConfig(userAgent, scriptSources, dataBuild, timeOrigin)
	answer, solved := powGenerate(seed, difficulty, config, 500000)
	if !solved {
		return "", fmt.Errorf("failed to solve proof token: difficulty=%s", difficulty)
	}
	return "gAAAAAB" + answer + "~S", nil
}

func buildPOWConfig(userAgent string, scriptSources []string, dataBuild string, timeOrigin float64) []any {
	if len(scriptSources) == 0 {
		scriptSources = []string{defaultPOWScript}
	}
	// Object.keys 采样池 — React 在 DOM 上注入的随机后缀属性 key
	objKeysPool := []string{
		"_reactListening8in7sfyhjvp",
		"_reactListeningo743lnnpvdg",
		"_reactListening" + randomHex(8),
		"__reactFiber$" + randomHex(8),
		"__reactProps$" + randomHex(8),
	}
	// Object.getOwnPropertyNames(window) 采样池 — 从 window 属性名中随机取
	winPropPool := []string{
		"onchange", "location", "closed", "postMessage", "queueMicrotask",
		"requestAnimationFrame", "setInterval", "setTimeout", "caches",
		"indexedDB", "sessionStorage", "localStorage", "performance",
		"crypto", "navigator", "screen", "fetch",
	}
	// Date().toString() — 使用太平洋时区（ChatGPT 匿名端点默认时区）
	loc, _ := time.LoadLocation("America/Los_Angeles")
	t := time.Now().In(loc)
	zoneName, offsetSec := t.Zone()
	offsetSign := "+"
	if offsetSec < 0 {
		offsetSign = "-"
		offsetSec = -offsetSec
	}
	dateStr := fmt.Sprintf("%s GMT%s%02d%02d (%s)",
		t.Format("Mon Jan 02 2006 15:04:05"),
		offsetSign, offsetSec/3600, (offsetSec%3600)/60, zoneName)

	return []any{
		fmt.Sprintf("%d", randomChoiceInt([]int{3000, 4000, 5000})), // [0]  screen.width+screen.height (string)
		dateStr,                          // [1]  Date().toString()
		"4294967296",                     // [2]  performance.memory.jsHeapSizeLimit
		0,                                // [3]  nonce (运行时写入)
		rand.Float64(),                   // [4]  Math.random()
		userAgent,                        // [5]  navigator.userAgent
		randomChoice(scriptSources),      // [6]  <script src> 随机 URL
		dataBuild,                        // [7]  c/.../_ script 目录
		"en-US",                          // [8]  navigator.language
		0,                                // [9]  elapsed ms (运行时写入)
		[]string{"en-US", "en"},          // [10] navigator.languages (数组)
		rand.Float64(),                   // [11] Math.random()
		randomChoice(objKeysPool),        // [12] Object.keys 随机键
		randomChoice(winPropPool),        // [13] Object.getOwnPropertyNames(window) 随机键
		float64(time.Now().UnixNano())/1e6, // [14] performance.now()
		"",                               // [15] sessionStorage.sid
		"",                               // [16] URLSearchParams(location.search)
		"Win32",                          // [17] navigator.platform
		timeOrigin,                       // [18] performance.timeOrigin
		0,                                // [19] Number("ai" in window)
		0,                                // [20] Number("InstallTrigger" in window) — Chrome=0
		0,                                // [21] Number("solana" in window)
		1,                                // [22] Number("TextEncoder" in window)
	}
}

// randomHex returns a random n-character lowercase hex string.
func randomHex(n int) string {
	const hexChars = "0123456789abcdef"
	b := make([]byte, n)
	for i := range b {
		b[i] = hexChars[rand.Intn(len(hexChars))]
	}
	return string(b)
}

func powGenerate(seed, difficulty string, config []any, limit int) (string, bool) {
	t0 := time.Now()
	for nonce := 0; nonce < limit; nonce++ {
		config[3] = nonce
		config[9] = int64(math.Round(float64(time.Since(t0)) / float64(time.Millisecond)))

		// Full JSON marshal each iteration — matches JS JSON.stringify (no HTML escaping)
		var buf bytes.Buffer
		enc := json.NewEncoder(&buf)
		enc.SetEscapeHTML(false)
		if err := enc.Encode(config); err != nil {
			continue
		}
		encoded := base64.StdEncoding.EncodeToString(bytes.TrimSpace(buf.Bytes()))

		hashStr := zvtHash(seed + encoded)
		if len(hashStr) >= len(difficulty) && hashStr[:len(difficulty)] <= difficulty {
			return encoded, true
		}
	}
	return randomBase64(24), false
}

// randomBase64 returns a random string of n base64-safe characters.
func randomBase64(n int) string {
	const charset = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"
	b := make([]byte, n)
	for i := range b {
		b[i] = charset[rand.Intn(len(charset))]
	}
	return string(b)
}

// zvtHash implements the FNV-1a variant used by ChatGPT's sentinel PoW verification.
// Mirrors the zvt(e) function in the sentinel SDK.
func zvtHash(input string) string {
	h := uint32(2166136261) // FNV offset basis
	for i := 0; i < len(input); i++ {
		h ^= uint32(input[i])
		h = uint32(uint64(h) * 16777619) // FNV prime (32-bit Math.imul)
	}
	h ^= h >> 16
	h = uint32(uint64(h) * 2246822507)
	h ^= h >> 13
	h = uint32(uint64(h) * 3266489909)
	h ^= h >> 16
	return fmt.Sprintf("%08x", h)
}

func randomChoice(items []string) string {
	if len(items) == 0 {
		return ""
	}
	return items[rand.Intn(len(items))]
}

func randomChoiceInt(items []int) int {
	if len(items) == 0 {
		return 0
	}
	return items[rand.Intn(len(items))]
}

// rawProofAnswer extracts the raw base64 PoW answer from a proof token.
// Format: "gAAAAAB" + <raw_answer> + "~S"  →  returns <raw_answer>.
// If the token doesn't match the expected format, returns it as-is.
func rawProofAnswer(proofToken string) string {
	const prefix = "gAAAAAB"
	const suffix = "~S"
	t := strings.TrimSpace(proofToken)
	if !strings.HasPrefix(t, prefix) || !strings.HasSuffix(t, suffix) {
		return t
	}
	return t[len(prefix) : len(t)-len(suffix)]
}
