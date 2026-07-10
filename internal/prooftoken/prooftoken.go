package prooftoken

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	mathrand "math/rand"
	"strconv"
	"strings"
	"time"

	"chatgpt2api/internal/browserfp"
	"chatgpt2api/internal/fingerprint"
)

// mathRandNew 是 math/rand.New 的本地别名 (避免类型混淆)。
type mathRand = mathrand.Rand

func mathRandNew(seed int64) *mathRand {
	return mathrand.New(mathrand.NewSource(seed))
}

const (
	// PrefixRequirements RequirementsToken 前缀
	PrefixRequirements = "gAAAAAC"
	// PrefixProof ProofToken 前缀
	PrefixProof = "gAAAAAB"
	// Suffix 附加在 base64(config) 后面的分隔符
	Suffix = "~S"
	// ErrorPrefix PoW 失败时的占位指纹前缀 (对齐 sdk.deob.pretty.js:292 /
	// 4813494d-lryin3horwb01cb5.js class constructor)。
	// 最终 token 格式: PrefixProof + ErrorPrefix + base64(JSON.stringify("e" or error)) + Suffix
	ErrorPrefix = "wQ8Lk5FbGpA2NcR9dShT6gYjU7VxZ4D"
	// DefaultErrorPayload 失败 fallback 默认 base64 内容 (= base64(JSON.stringify("e")) = "ImUi")。
	// 对齐 buildGenerateFailMessage(e) 里 String(e ?? "e") → NM("e")。
	DefaultErrorPayload = "ImUi"
)

// DefaultFlow 是 sentinel prepare/finalize 流程标识。
const DefaultFlow = "chatgpt"

// fingerprintSize 25 元素 config, 对齐新版 SDK 算法 (conversation.txt 2026-06 样本)。
const fingerprintSize = 25

// powSentinelNonce / powSentinelElapsed 是 PoW 模板化的哨兵值——在
// json.Marshal 输出中定位 nonce([3]) 和 elapsed([9]) 的位置。
// 取负数保证不会与真实数据冲突。
const (
	powSentinelNonce   = -777777
	powSentinelElapsed = -888888
)

// DebugLog 是 PoW 诊断日志的输出通道。由上层(backend)注入 sentinelLog。
// 为 nil 时不输出任何诊断日志。
var DebugLog func(format string, args ...any)

func powLog(format string, args ...any) {
	if DebugLog != nil {
		DebugLog(format, args...)
	}
}

// windowKeys 候选 [13] (Object.getOwnPropertyNames(window) 随机键)
var windowKeys = []string{
	"requestIdleCallback", "webkitRequestAnimationFrame", "onfocus", "onblur",
}

// reactLetters 是 [12] 随机 suffix 字符表
var reactLetters = []rune("abcdefghijklmnopqrstuvwxyz0123456789")

// Config 持有 p 字段生成所需的全部上下文 (对齐新版 BrowserSession 字段)。
type Config struct {
	DeviceID  string
	UserAgent string
	Language  string
	Languages string
	// Timezone IANA 时区名(如 "America/Los_Angeles"),传给
	// fingerprint.Options.Timezone。如果为空,fingerprint 用 Go 本地时区。
	Timezone            string
	ScreenWidth         int
	ScreenHeight        int
	HardwareConcurrency int
	SentinelSV          string // SDK 版本, e.g. "20260423af3c"
	BuildID             string // 来自 chatgpt.com 页面的 data-build
	// 可选:固定 Math.random (用于测试)
	FixedRandom *float64
}

// fixedUserAgent 默认浏览器 UA，对齐 aurora 指纹池中的 Chrome 148。
const fixedUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/148.0.0.0 Safari/537.36"

// NewConfig 构造默认配置。指纹随机化由 fingerprint.Build25 内部完成。
// userAgent 为空时使用 fixedUserAgent。
func NewConfig(userAgent string) *Config {
	if userAgent == "" {
		userAgent = fixedUserAgent
	}
	fp := browserfp.Get()
	return &Config{
		DeviceID:            randomUUID(),
		UserAgent:           userAgent,
		Language:            fp.Language,
		Languages:           browserfp.LanguageJoin(fp.Language),
		Timezone:            "America/Los_Angeles",
		ScreenWidth:         fp.ScreenWidth,
		ScreenHeight:        fp.ScreenHeight,
		HardwareConcurrency: fp.HardwareConcurrency,
		SentinelSV:          "20260423af3c",
		BuildID:             fp.BuildID,
	}
}

// randomUUID 生成 v4 UUID (与 crypto/rand 区分)。
func randomUUID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// EncodeConfig 把 config 数组编码为 base64 字符串 (对齐 base64.b64encode(json_str.encode('utf-8')))。
func EncodeConfig(config []any) string {
	b, err := json.Marshal(config)
	if err != nil {
		return ""
	}
	return base64.StdEncoding.EncodeToString(b)
}

// EncodeString 把任意字符串 JSON 序列化后 base64 编码(对齐 SDK 的
// NM(String(e)) 行为:先把字符串当 JSON 值序列化,再 btoa + UTF-8)。
// 用在 PoW 失败 fallback 拼 base64(error)。
func EncodeString(s string) string {
	b, err := json.Marshal(s)
	if err != nil {
		return ""
	}
	return base64.StdEncoding.EncodeToString(b)
}

// FNV1aHash FNV-1a 32 位哈希, 返回 8 位 hex (小写)。
// 对齐 Python fnv1a_hash + _imul。
func FNV1aHash(text string) string {
	const (
		fnvOffset = 2166136261
		fnvPrime  = 16777619
	)
	h := uint32(fnvOffset)
	for _, ch := range text {
		h ^= uint32(ch)
		h = imul32(h, fnvPrime)
	}
	h ^= h >> 16
	h = imul32(h, 2246822507)
	h ^= h >> 13
	h = imul32(h, 3266489909)
	h ^= h >> 16
	return fmt.Sprintf("%08x", h)
}

// FNV1aHashBytes 是 FNV1aHash 的零分配版本——对多个 []byte 片段顺序计算
// FNV-1a 哈希。用于 PoW 热循环中避免 seed + base64 的字符串拼接分配。
func FNV1aHashBytes(parts ...[]byte) string {
	const (
		fnvOffset = 2166136261
		fnvPrime  = 16777619
	)
	h := uint32(fnvOffset)
	for _, part := range parts {
		for _, b := range part {
			h ^= uint32(b)
			h = imul32(h, fnvPrime)
		}
	}
	h ^= h >> 16
	h = imul32(h, 2246822507)
	h ^= h >> 13
	h = imul32(h, 3266489909)
	h ^= h >> 16
	return fmt.Sprintf("%08x", h)
}

// imul32 模拟 JavaScript Math.imul (32 位整数乘法)。
func imul32(a, b uint32) uint32 {
	return (a * b) & 0xFFFFFFFF
}

// rngFloat 返回 Math.random() (若 FixedRandom 非空则返回 fixed 值)。
func (c *Config) rngFloat(rng *mathRand) float64 {
	if c.FixedRandom != nil {
		return *c.FixedRandom
	}
	return rng.Float64()
}

// envFlags 返回 [19-22] 的环境探测值 (Number("X" in window))。
// 当前 aurora 不是真浏览器,默认全部 0;后续如需模拟,可在 Config 里加字段。
func (c *Config) envFlags() [4]int {
	return [4]int{0, 0, 0, 0}
}

// buildConfig 构造 25 元素 fingerprint config (覆盖 [3]/[9]/[14])。
//
// 走 internal/fingerprint.Build25 拿到真实浏览器形态(对齐 2026-06-24 chatgpt.com 抓包),
// 然后覆盖 PoW 的 [3] nonce、[9] elapsed, 以及 [14] device_id。
func (c *Config) buildConfig(rng *mathRand, attempt *int, elapsedMs *int64) []any {
	// 把 c 的字段映射成 fingerprint.Options;rng 注入保持 deterministic
	opts := fingerprint.Options{
		UserAgent:           c.UserAgent,
		Platform:            "Win32",
		ScreenWidth:         c.ScreenWidth,
		ScreenHeight:        c.ScreenHeight,
		HardwareConcurrency: c.HardwareConcurrency,
		JSHeapSizeLimit:     4294967296,
		BuildID:             c.BuildID,
		Timezone:            c.Timezone,
		Rand:                rng,
	}
	// 如果 Languages 是 "en-US,en" 字符串形式,拆成 []string
	if c.Languages != "" {
		opts.Languages = splitLangList(c.Languages)
	} else {
		opts.Languages = []string{c.Language, "en"}
	}

	config := fingerprint.Build25(opts)

	// [3] / [9] 覆盖:PoW 阶段用 nonce(int) 和 elapsedMs(int64)
	if attempt != nil {
		config[3] = *attempt
	} else {
		// requirements 阶段:对齐 sdk.deob.pretty.js:413 `n[3] = 1`(固定 int 1,
		// 不是 Math.random)。requirements token 不跑 PoW,只是固定一个
		// 标记让服务端验设备形态。
		config[3] = 1
	}
	if elapsedMs != nil {
		config[9] = *elapsedMs
	} else {
		// requirements 阶段:[9] 是 performance.now() - t0 的一次性测时;
		// 这里不传 elapsedMs 时沿用 fingerprint 的 Math.random()(float),
		// 跟 SDK 行为一致(SDK 也不传)。
		config[9] = c.rngFloat(rng)
	}
	// [14] device_id — 浏览器 SDK 用 localStorage 中存储的 device_id,
	// 必须是非空 UUID, 否则服务器识别为空设备 → mini 池。
	if c.DeviceID != "" {
		config[14] = c.DeviceID
	}
	return config
}

// splitLangList 把 "en-US,en" 拆成 []string。
func splitLangList(s string) []string {
	out := []string{}
	cur := ""
	for _, r := range s {
		if r == ',' {
			if cur != "" {
				out = append(out, cur)
				cur = ""
			}
			continue
		}
		cur += string(r)
	}
	if cur != "" {
		out = append(out, cur)
	}
	return out
}

// GenerateRequirementsToken 生成首次 sentinel/req 的 p 字段值 (gAAAAAC 前缀)。
//
// 对齐 sdk.deob.pretty.js:407-418 _generateRequirementsTokenAnswerBlocking:
//  1. 拿 fingerprint config(本包 buildConfig)
//  2. [3] = 1 (固定)
//  3. [9] = performance.now() - t0 (一次性测时,不循环)
//  4. base64(JSON.stringify(config)) → 返回
//  5. 失败 → errorPrefix + base64(error)  (errorPrefix = "wQ8Lk5FbGpA2NcR9dShT6gYjU7VxZ4D")
//
// 注意:requirements token **不跑 PoW**(proof token 才跑);这里只是
// 一次性拼一份带时间戳的 config 让服务端验设备形态。
func (c *Config) GenerateRequirementsToken() string {
	rng := mathRandNew(time.Now().UnixNano())
	// 对齐浏览器: requirements 阶段 [9] = Math.random(), 不是 elapsed
	// (elapsed 只在 PoW 迭代阶段使用)
	config := c.buildConfig(rng, nil, nil)
	config[3] = 1
	encoded := EncodeConfig(config)
	return PrefixRequirements + encoded + Suffix
}

// SolveProofOfWork 按服务端挑战求解 proof token (gAAAAAB 前缀 + FNV-1a 哈希)。
//
// 使用模板化优化: 25 元素 config 中只有 [3] nonce 和 [9] elapsed 每次迭代变化,
// 其余 23 个元素在循环前一次性 JSON 序列化, 循环内只做整数→字符串拼接。
// 相比旧实现省掉了 500k 次 json.Marshal + Build25 调用。
//
// 失败 fallback 格式(对齐 sdk.deob.pretty.js:329 + buildGenerateFailMessage:364-366):
//
//	"gAAAAAB" + ErrorPrefix + base64(JSON.stringify("e" or error)) + "~S"
//
// 500k 次未命中时 err=nil → 用 DefaultErrorPayload ("ImUi" = base64('"e"'))。
func (c *Config) SolveProofOfWork(seed, difficulty string) string {
	if seed == "" || difficulty == "" {
		return PrefixProof + Suffix
	}

	startTime := time.Now()
	rng := mathRandNew(time.Now().UnixNano())
	diffLen := len(difficulty)
	const maxIter = 500_000

	// —— 阶段 1: 构建 JSON 模板 (一次性) ——
	// 用唯一的负数哨兵标记 nonce 和 elapsed 在 JSON 输出中的位置。
	sentinelNonce := powSentinelNonce
	sentinelElapsed := int64(powSentinelElapsed)

	tmplCfg := c.buildConfig(rng, &sentinelNonce, &sentinelElapsed)
	tmplJSON, err := json.Marshal(tmplCfg)
	if err != nil {
		powLog("poW: template marshal FAILED — %v; falling back to legacy", err)
		return c.solveProofOfWorkLegacy(seed, difficulty, rng)
	}
	tmplStr := string(tmplJSON)

	// 定位哨兵在 JSON 串中的位置
	nonceSentinel := strconv.Itoa(sentinelNonce)
	elapsedSentinel := strconv.FormatInt(sentinelElapsed, 10)
	noncePos := strings.Index(tmplStr, nonceSentinel)
	elapsedPos := strings.Index(tmplStr, elapsedSentinel)

	if noncePos < 0 || elapsedPos < 0 {
		powLog("poW: sentinel not found in JSON — noncePos=%d elapsedPos=%d; falling back to legacy",
			noncePos, elapsedPos)
		return c.solveProofOfWorkLegacy(seed, difficulty, rng)
	}

	jsonPrefix := tmplStr[:noncePos]
	jsonMiddle := tmplStr[noncePos+len(nonceSentinel) : elapsedPos]
	jsonSuffix := tmplStr[elapsedPos+len(elapsedSentinel):]

	powLog("poW: template built — json=%dB prefix=%dB middle=%dB suffix=%dB",
		len(tmplStr), len(jsonPrefix), len(jsonMiddle), len(jsonSuffix))

	// —— 阶段 2: 诊断验证(首次迭代, 仅 debug 模式) ——
	if !verifyPoWTemplate(c, rng, tmplStr, jsonPrefix, jsonMiddle, jsonSuffix) {
		powLog("poW: template verification FAILED — falling back to legacy method")
		return c.solveProofOfWorkLegacy(seed, difficulty, rng)
	}

	// —— 阶段 3: 主求解循环 ——
	// 预分配所有缓冲区, 循环内零堆分配。
	preCap := len(jsonPrefix) + len(jsonMiddle) + len(jsonSuffix) + 20
	jsonBuf := make([]byte, 0, preCap)
	encBuf := make([]byte, base64.StdEncoding.EncodedLen(preCap))
	seedBytes := []byte(seed)

	var elapsed int64
	for i := 0; i < maxIter; i++ {
		// 每 1024 次迭代更新一次 elapsed, 避免 time.Since 系统调用开销
		if i&1023 == 0 {
			elapsed = time.Since(startTime).Milliseconds()
		}

		// 组装 JSON: prefix + nonce + middle + elapsed + suffix
		jsonBuf = jsonBuf[:0]
		jsonBuf = append(jsonBuf, jsonPrefix...)
		jsonBuf = strconv.AppendInt(jsonBuf, int64(i), 10)
		jsonBuf = append(jsonBuf, jsonMiddle...)
		jsonBuf = strconv.AppendInt(jsonBuf, elapsed, 10)
		jsonBuf = append(jsonBuf, jsonSuffix...)

		// Base64 编码(写入预分配缓冲区, 零分配)
		encLen := base64.StdEncoding.EncodedLen(len(jsonBuf))
		base64.StdEncoding.Encode(encBuf, jsonBuf)

		// FNV-1a 哈希: seed + base64(json), 直接走 []byte 避免字符串拼接
		hashResult := FNV1aHashBytes(seedBytes, encBuf[:encLen])
		if len(hashResult) >= diffLen && hashResult[:diffLen] <= difficulty {
			elapsedMs := time.Since(startTime).Milliseconds()
			powLog("poW: SOLVED — iter=%d elapsed=%dms speed=%.0f/ms diff=%q",
				i, elapsedMs, float64(i)/float64(max64(1, elapsedMs)), difficulty)
			return PrefixProof + string(encBuf[:encLen]) + Suffix
		}

		// 进度日志 (仅 debug 模式, 每 10 万次)
		if DebugLog != nil && i > 0 && i%100000 == 0 {
			powLog("poW: progress — iter=%d/%d elapsed=%dms", i, maxIter, time.Since(startTime).Milliseconds())
		}
	}

	elapsedMs := time.Since(startTime).Milliseconds()
	powLog("poW: EXHAUSTED — maxIter=%d elapsed=%dms diff=%q", maxIter, elapsedMs, difficulty)
	return PrefixProof + ErrorPrefix + DefaultErrorPayload + Suffix
}

// max64 returns the larger of a and b (int64-safe max, avoid Go 1.21+ builtin dependency).
func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

// verifyPoWTemplate 对比模板方案与旧方案的首次迭代输出, 确认两者一致。
// 仅在 DebugLog 启用时执行; 返回 false 表示验证失败。
func verifyPoWTemplate(c *Config, rng *mathRand, tmplStr string, jsonPrefix, jsonMiddle, jsonSuffix string) bool {
	if DebugLog == nil {
		return true // 未启用诊断日志时跳过验证
	}

	verifyNonce := 0
	verifyElapsed := int64(0)

	// 旧方案: buildConfig + json.Marshal
	legacyCfg := c.buildConfig(rng, &verifyNonce, &verifyElapsed)
	legacyJSON, err := json.Marshal(legacyCfg)
	if err != nil {
		powLog("poW: verify — legacy marshal failed: %v", err)
		return false
	}

	// 模板方案: prefix + "0" + middle + "0" + suffix
	var verifyBuf strings.Builder
	verifyBuf.Grow(len(jsonPrefix) + len(jsonMiddle) + len(jsonSuffix) + 10)
	verifyBuf.WriteString(jsonPrefix)
	verifyBuf.WriteString("0")
	verifyBuf.WriteString(jsonMiddle)
	verifyBuf.WriteString("0")
	verifyBuf.WriteString(jsonSuffix)
	templateStr := verifyBuf.String()

	if string(legacyJSON) != templateStr {
		powLog("poW: verify — JSON MISMATCH")
		powLog("poW:   legacy_json   (%dB): %s", len(legacyJSON), legacyJSON)
		powLog("poW:   template_json (%dB): %s", len(templateStr), templateStr)
		powLog("poW:   template_full (%dB): %s", len(tmplStr), tmplStr)
		return false
	}

	legacyB64 := base64.StdEncoding.EncodeToString(legacyJSON)
	templateB64 := base64.StdEncoding.EncodeToString([]byte(templateStr))
	if legacyB64 != templateB64 {
		powLog("poW: verify — BASE64 MISMATCH")
		powLog("poW:   legacy_b64:   %s", legacyB64)
		powLog("poW:   template_b64: %s", templateB64)
		return false
	}

	powLog("poW: verify — PASSED (legacy == template, %d bytes JSON)", len(legacyJSON))
	return true
}

// solveProofOfWorkLegacy 是旧的逐次 Build25 + Marshal 实现。
// 保留作为模板方案的 fallback: 当哨兵定位失败或诊断验证不通过时自动切换。
func (c *Config) solveProofOfWorkLegacy(seed, difficulty string, rng *mathRand) string {
	startTime := time.Now()
	diffLen := len(difficulty)
	const maxIter = 500_000

	powLog("poW: legacy — starting fallback solver")

	for i := 0; i < maxIter; i++ {
		nonce := i
		elapsed := time.Since(startTime).Milliseconds()
		config := c.buildConfig(rng, &nonce, &elapsed)
		encoded := EncodeConfig(config)
		hashInput := seed + encoded
		hashResult := FNV1aHash(hashInput)
		if hashResult[:diffLen] <= difficulty {
			elapsedMs := time.Since(startTime).Milliseconds()
			powLog("poW: legacy — SOLVED iter=%d elapsed=%dms", i, elapsedMs)
			return PrefixProof + encoded + Suffix
		}
	}

	powLog("poW: legacy — EXHAUSTED maxIter=%d", maxIter)
	return PrefixProof + ErrorPrefix + DefaultErrorPayload + Suffix
}

// BuildFailToken 构造 PoW 失败 fallback token。
// errMessage 为空时使用 SDK 默认 "e"。
func BuildFailToken(errMessage string) string {
	if errMessage == "" {
		errMessage = "e"
	}
	return PrefixProof + ErrorPrefix + EncodeString(errMessage) + Suffix
}

// String 辅助:float64 → string (避免重复写 strconv.FormatFloat)。
func String(v float64) string {
	return strconv.FormatFloat(v, 'f', -1, 64)
}

// BackwardCompat 旧 API 兼容 (client-go 旧版代码依赖这些)。

// (c *Config).RequirementsToken 兼容 client-go 旧名 (返回 gAAAAAC + base64 + ~S)。
func (c *Config) RequirementsToken() string {
	return c.GenerateRequirementsToken()
}

// SolveProofToken 兼容 client-go 旧名 (接受 userAgent 参数)。
func SolveProofToken(seed, difficulty, userAgent string) string {
	c := NewConfig(userAgent)
	return c.SolveProofOfWork(seed, difficulty)
}

// BuildSentinelRequestBody 构造 sentinel/req 请求体 (JSON 字符串)。
func BuildSentinelRequestBody(p, deviceID, flow string) string {
	if flow == "" {
		flow = DefaultFlow
	}
	body := map[string]string{"p": p, "id": deviceID, "flow": flow}
	b, _ := json.Marshal(body)
	return string(b)
}

// SentinelTokenHeader 是 f/conversation 头里 openai-sentinel-token 的值 (JSON)。
// 字段含义:
//
//	p: prepare 阶段用的 p (config 编码)
//	t: turnstile token (无则空)
//	c: sentinel token (服务端返回)
//	id: deviceID
//	flow: 流程标识
type SentinelTokenHeader struct {
	P    string `json:"p"`
	T    string `json:"t"`
	C    string `json:"c"`
	ID   string `json:"id"`
	Flow string `json:"flow"`
}

// BuildSentinelTokenHeader 构造 openai-sentinel-token 请求头值 (JSON 字符串)。
func BuildSentinelTokenHeader(p, turnstileToken, sentinelToken, deviceID, flow string) string {
	if flow == "" {
		flow = DefaultFlow
	}
	h := SentinelTokenHeader{P: p, T: turnstileToken, C: sentinelToken, ID: deviceID, Flow: flow}
	b, _ := json.Marshal(h)
	return string(b)
}
