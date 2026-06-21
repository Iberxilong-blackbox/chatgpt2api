OpenAI Chat 最新 POW 逆向分析（2API 仓库算法滞后风控问题）

近期复盘各类 2API 开源仓库实现，发现绝大多数项目的 Proof-of-Work 算法仍沿用老旧版本，极易触发平台风控、接口降智限流。恰逢端午假期逆向解析 OpenAI Chat 现行最新 POW 完整流程，完整调用链路与源码拆解如下：

一、整体调用链路
getEnforcementToken → _getAnswer → _runCheck 完整执行流程

二、分层源码拆解
1. 入口函数：getEnforcementToken
async getEnforcementToken(t, n) {
  return this._getAnswer(t, n.forceSync);
}
2. 调度器：_getAnswer
_getAnswer(t, n = !1) {
  const r = "gAAAAAB";
  // 未开启POW校验直接返回空
  if (!t.proofofwork.required) return null;
  const { seed: o, difficulty: c } = t.proofofwork;
  // seed、difficulty 字段非法直接返回空
  if ((typeof o !== "string") || (typeof c !== "string")) return null;

  const i = this.answers.get(o);
  // ① 缓存命中：直接返回缓存结果
  if (typeof i === "string") return i;

  // ② 同步计算分支（forceSync = true）
  if (n) {
    const t = this._generateAnswerSync(o, c);
    const n = r + t;
    return this.answers.set(o, n), n;
  }

  // ③ 异步分支：并发去重，避免重复计算同一seed
  if (!this.answers.has(o))
    this.answers.set(o, this._generateAnswerAsync(o, c));
  return Promise.resolve().then(async () => (
    r + await this.answers.get(o)
  )).then(t => (this.answers.set(o, t), t));
}
内存缓存复用已求解的 seed 结果，减少重复哈希计算
区分同步 / 异步两种计算模式
结果统一拼接前缀 gAAAAAB
3. 同步求解器：_generateAnswerSync
_generateAnswerSync(t, n) {
  const r = performance.now();
  try {
    const o = this.getConfig();          // 获取23项设备指纹数组
    // 最大尝试次数：maxAttempts = 500000
    for (let c = 0; c < this.maxAttempts; c++) {
      const i = this._runCheck(r, t, n, o, c);
      if (i) return i;
    }
  } catch (t) {
    return this.buildGenerateFailMessage(t);
  }
  return this.buildGenerateFailMessage();
}
参数说明：

t：pow seed
n：difficulty 难度串
o：设备指纹数组（固定 25 个字段）
循环遍历 nonce，最大重试 500000 次，命中合法哈希直接返回结果，超限 / 异常返回失败信息
4. 核心校验函数：_runCheck（哈希判定核心）
_runCheck = function (t0, seed, difficulty, fp, nonce) {
  fp[3]  = nonce;
  fp[9]  = Math.round(performance.now() - t0);
  const base64fp = N(fp);                 // N = base64(JSON.stringify(fp))
  const h = fnv1a_hex(seed + base64fp);
  // 前N位哈希值对比难度值，满足难度则返回fp编码串，否则返回null
  return h.substring(0, difficulty.length) <= difficulty
         ? (base64fp + "~S")
         : null;
};
哈希算法：fnv1a_hex，不再是MD5/SHA 系列
设备指纹：固定 25 项数组，修改下标 3、9 用于遍历 nonce 碰撞
结果格式：最终对外 token = gAAAAAB + base64fp~S


-------------

贴个go版本实现：
```
package main

import (
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"math/rand"
	"os"
	"time"
)

// -----------------------------------------------------------------------------
// 1. FNV-1a 32-bit 哈希 (与 JS 端 Math.imul 行为对齐, 低 32 位无符号)
// -----------------------------------------------------------------------------
func fnv1a32(s string) uint32 {
	const (
		offset uint32 = 2166136261
		prime  uint32 = 16777619
	)
	h := offset
	for i := 0; i < len(s); i++ {
		h ^= uint32(s[i])
		// uint32 乘法天然取低 32 位, 等价于 Math.imul
		h *= prime
	}
	h ^= h >> 16
	h *= 2246822507
	h ^= h >> 13
	h *= 3266489909
	h ^= h >> 16
	return h
}

func fnv1aHex(s string) string {
	return fmt.Sprintf("%08x", fnv1a32(s))
}

// -----------------------------------------------------------------------------
// 2. N(t) = base64(JSON.stringify(t))
// -----------------------------------------------------------------------------
func N(arr []interface{}) (string, error) {
	b, err := json.Marshal(arr)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(b), nil
}

// -----------------------------------------------------------------------------
// 3. _runCheck
// -----------------------------------------------------------------------------
func runCheck(t0 time.Time, seed, difficulty string, fp []interface{}, nonce int) (*string, error) {
	fp[3] = nonce
	elapsedMs := int64(math.Round(float64(time.Since(t0)) / float64(time.Millisecond)))
	fp[9] = elapsedMs
	base64Fp, err := N(fp)
	if err != nil {
		return nil, err
	}
	h := fnv1aHex(seed + base64Fp)
	if len(h) >= len(difficulty) && h[:len(difficulty)] <= difficulty {
		ans := base64Fp + "~S"
		return &ans, nil
	}
	return nil, nil
}

// -----------------------------------------------------------------------------
// 4. _generateAnswerSync
// -----------------------------------------------------------------------------
func generateAnswerSync(seed, difficulty string, fp []interface{}, maxAttempts int) (*string, error) {
	t0 := time.Now()
	for nonce := 0; nonce < maxAttempts; nonce++ {
		ans, err := runCheck(t0, seed, difficulty, fp, nonce)
		if err != nil {
			return nil, err
		}
		if ans != nil {
			return ans, nil
		}
	}
	return nil, nil
}

// -----------------------------------------------------------------------------
// 5. _getAnswer
// -----------------------------------------------------------------------------
type Challenge struct {
	ProofOfWork struct {
		Required   bool   `json:"required"`
		Seed       string `json:"seed"`
		Difficulty string `json:"difficulty"`
	} `json:"proofofwork"`
}

func getAnswer(ch Challenge, forceSync bool, maxAttempts int, fp []interface{}) (*string, error) {
	if !ch.ProofOfWork.Required {
		return nil, nil
	}
	if ch.ProofOfWork.Seed == "" || ch.ProofOfWork.Difficulty == "" {
		return nil, nil
	}
	if fp == nil {
		fp = buildFingerprint()
	}
	if forceSync {
		ans, err := generateAnswerSync(ch.ProofOfWork.Seed, ch.ProofOfWork.Difficulty, fp, maxAttempts)
		if err != nil || ans == nil {
			return ans, err
		}
		token := "gAAAAAB" + *ans
		return &token, nil
	}
	// 异步分支: JS 用 requestIdleCallback 分片, Go 这里简化为同步
	ans, err := generateAnswerSync(ch.ProofOfWork.Seed, ch.ProofOfWork.Difficulty, fp, maxAttempts)
	if err != nil || ans == nil {
		return ans, err
	}
	token := "gAAAAAB" + *ans
	return &token, nil
}

// -----------------------------------------------------------------------------
// 6. 占位指纹, 真实使用请用浏览器真实环境数据替换
// -----------------------------------------------------------------------------
func P[T any](arr []T) T {
	var zero T
	if len(arr) == 0 {
		return zero
	}
	return arr[rand.Intn(len(arr))]
}

func buildFingerprint() []interface{} {
	// 顺序与 sdk.deob.js:259-262 一致
	// [3] = nonce, [9] = elapsed ms  (运行时由 runCheck 写入)
	return []interface{}{
		"0",     // [0]  screen.width+screen.height
		"",      // [1]  Date.toString()
		"0",     // [2]  performance.memory.jsHeapSizeLimit
		0,       // [3]  nonce (运行时写入)
		0.0,     // [4]  Math.random()
		"",      // [5]  navigator.userAgent
		"",      // [6]  <script src> 随机
		"",      // [7]  含 c/.../_ 的 script 目录
		"en-US", // [8]  navigator.language
		0,       // [9]  elapsed ms (运行时写入)
		"en-US", // [10] navigator.languages
		0.0,     // [11] Math.random()
		"",      // [12] Object.keys 随机键
		"",      // [13] Object.getOwnPropertyNames(window) 随机键
		0.0,     // [14] performance.now()
		"",      // [15] sessionStorage.sid
		"",      // [16] URLSearchParams(location.search) joined
		"",      // [17] navigator.platform
		0.0,     // [18] performance.timeOrigin
		0,       // [19] "ai" in window
		0,       // [20] "InstallTrigger" in window
		0,       // [21] "solana" in window
		0,       // [22] "TextEncoder" in window
	}
}

// -----------------------------------------------------------------------------
// 7. CLI
// -----------------------------------------------------------------------------
func main() {
	seed := flag.String("seed", "", "服务端下发的 seed")
	difficulty := flag.String("difficulty", "0", "难度十六进制前缀")
	max := flag.Int("max", 500000, "最大尝试次数")
	fpFile := flag.String("fingerprint-json", "", "23 项指纹 JSON 文件路径, 缺省用占位")
	flag.Parse()

	if *seed == "" {
		fmt.Fprintln(os.Stderr, "需要 -seed")
		os.Exit(2)
	}

	var fp []interface{}
	if *fpFile != "" {
		b, err := os.ReadFile(*fpFile)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		if err := json.Unmarshal(b, &fp); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	} else {
		fp = buildFingerprint()
	}

	var ch Challenge
	ch.ProofOfWork.Required = true
	ch.ProofOfWork.Seed = *seed
	ch.ProofOfWork.Difficulty = *difficulty

	token, err := getAnswer(ch, true, *max, fp)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if token == nil {
		fmt.Println("FAILED")
		os.Exit(1)
	}
	fmt.Println(*token)
}
```

```
package main

import (
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"math/rand"
	"os"
	"time"
)

// -----------------------------------------------------------------------------
// 1. FNV-1a 32-bit 哈希 (与 JS 端 Math.imul 行为对齐, 低 32 位无符号)
// -----------------------------------------------------------------------------
func fnv1a32(s string) uint32 {
	const (
		offset uint32 = 2166136261
		prime  uint32 = 16777619
	)
	h := offset
	for i := 0; i < len(s); i++ {
		h ^= uint32(s[i])
		// uint32 乘法天然取低 32 位, 等价于 Math.imul
		h *= prime
	}
	h ^= h >> 16
	h *= 2246822507
	h ^= h >> 13
	h *= 3266489909
	h ^= h >> 16
	return h
}

func fnv1aHex(s string) string {
	return fmt.Sprintf("%08x", fnv1a32(s))
}

// -----------------------------------------------------------------------------
// 2. N(t) = base64(JSON.stringify(t))
// -----------------------------------------------------------------------------
func N(arr []interface{}) (string, error) {
	b, err := json.Marshal(arr)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(b), nil
}

// -----------------------------------------------------------------------------
// 3. _runCheck
// -----------------------------------------------------------------------------
func runCheck(t0 time.Time, seed, difficulty string, fp []interface{}, nonce int) (*string, error) {
	fp[3] = nonce
	elapsedMs := int64(math.Round(float64(time.Since(t0)) / float64(time.Millisecond)))
	fp[9] = elapsedMs
	base64Fp, err := N(fp)
	if err != nil {
		return nil, err
	}
	h := fnv1aHex(seed + base64Fp)
	if len(h) >= len(difficulty) && h[:len(difficulty)] <= difficulty {
		ans := base64Fp + "~S"
		return &ans, nil
	}
	return nil, nil
}

// -----------------------------------------------------------------------------
// 4. _generateAnswerSync
// -----------------------------------------------------------------------------
func generateAnswerSync(seed, difficulty string, fp []interface{}, maxAttempts int) (*string, error) {
	t0 := time.Now()
	for nonce := 0; nonce < maxAttempts; nonce++ {
		ans, err := runCheck(t0, seed, difficulty, fp, nonce)
		if err != nil {
			return nil, err
		}
		if ans != nil {
			return ans, nil
		}
	}
	return nil, nil
}

// -----------------------------------------------------------------------------
// 5. _getAnswer
// -----------------------------------------------------------------------------
type Challenge struct {
	ProofOfWork struct {
		Required   bool   `json:"required"`
		Seed       string `json:"seed"`
		Difficulty string `json:"difficulty"`
	} `json:"proofofwork"`
}

func getAnswer(ch Challenge, forceSync bool, maxAttempts int, fp []interface{}) (*string, error) {
	if !ch.ProofOfWork.Required {
		return nil, nil
	}
	if ch.ProofOfWork.Seed == "" || ch.ProofOfWork.Difficulty == "" {
		return nil, nil
	}
	if fp == nil {
		fp = buildFingerprint()
	}
	if forceSync {
		ans, err := generateAnswerSync(ch.ProofOfWork.Seed, ch.ProofOfWork.Difficulty, fp, maxAttempts)
		if err != nil || ans == nil {
			return ans, err
		}
		token := "gAAAAAB" + *ans
		return &token, nil
	}
	// 异步分支: JS 用 requestIdleCallback 分片, Go 这里简化为同步
	ans, err := generateAnswerSync(ch.ProofOfWork.Seed, ch.ProofOfWork.Difficulty, fp, maxAttempts)
	if err != nil || ans == nil {
		return ans, err
	}
	token := "gAAAAAB" + *ans
	return &token, nil
}

// -----------------------------------------------------------------------------
// 6. 占位指纹, 真实使用请用浏览器真实环境数据替换
// -----------------------------------------------------------------------------
func P[T any](arr []T) T {
	var zero T
	if len(arr) == 0 {
		return zero
	}
	return arr[rand.Intn(len(arr))]
}

func buildFingerprint() []interface{} {
	// 顺序与 sdk.deob.js:259-262 一致
	// [3] = nonce, [9] = elapsed ms  (运行时由 runCheck 写入)
	return []interface{}{
		"0",     // [0]  screen.width+screen.height
		"",      // [1]  Date.toString()
		"0",     // [2]  performance.memory.jsHeapSizeLimit
		0,       // [3]  nonce (运行时写入)
		0.0,     // [4]  Math.random()
		"",      // [5]  navigator.userAgent
		"",      // [6]  <script src> 随机
		"",      // [7]  含 c/.../_ 的 script 目录
		"en-US", // [8]  navigator.language
		0,       // [9]  elapsed ms (运行时写入)
		"en-US", // [10] navigator.languages
		0.0,     // [11] Math.random()
		"",      // [12] Object.keys 随机键
		"",      // [13] Object.getOwnPropertyNames(window) 随机键
		0.0,     // [14] performance.now()
		"",      // [15] sessionStorage.sid
		"",      // [16] URLSearchParams(location.search) joined
		"",      // [17] navigator.platform
		0.0,     // [18] performance.timeOrigin
		0,       // [19] "ai" in window
		0,       // [20] "InstallTrigger" in window
		0,       // [21] "solana" in window
		0,       // [22] "TextEncoder" in window
	}
}

// -----------------------------------------------------------------------------
// 7. CLI
// -----------------------------------------------------------------------------
func main() {
	seed := flag.String("seed", "", "服务端下发的 seed")
	difficulty := flag.String("difficulty", "0", "难度十六进制前缀")
	max := flag.Int("max", 500000, "最大尝试次数")
	fpFile := flag.String("fingerprint-json", "", "23 项指纹 JSON 文件路径, 缺省用占位")
	flag.Parse()

	if *seed == "" {
		fmt.Fprintln(os.Stderr, "需要 -seed")
		os.Exit(2)
	}

	var fp []interface{}
	if *fpFile != "" {
		b, err := os.ReadFile(*fpFile)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		if err := json.Unmarshal(b, &fp); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	} else {
		fp = buildFingerprint()
	}

	var ch Challenge
	ch.ProofOfWork.Required = true
	ch.ProofOfWork.Seed = *seed
	ch.ProofOfWork.Difficulty = *difficulty

	token, err := getAnswer(ch, true, *max, fp)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if token == nil {
		fmt.Println("FAILED")
		os.Exit(1)
	}
	fmt.Println(*token)
}
```



-----

大概从抓取了一组指纹，形如

```

[
    "3000",
    "Fri Jun 19 2026 04:38:41 GMT-0700 (Pacific Daylight Time)",
    "4294967296",
    0,
    0.37582884894942703,
    "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/148.0.0.0 Safari/537.36",
    "https://connect.facebook.net/en_US/fbevents.js",
    "prod-ab8a6348980a3e1d771c463b9f4f3e4e584f2769",
    "en-US",
    0,
    [
        "en-US",
        "en"
    ],
    0.941807406176301,
    "_reactListening8in7sfyhjvp",
    "onchange",
    6574898.6000000015,
    "",
    "",
    "Win32",
    1781862547037.9,
    0,
    0,
    0,
    1
]
```
