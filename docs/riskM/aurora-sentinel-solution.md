# Aurora 项目 — ChatGPT Sentinel 完整解决方案

> **日期**：2026-06-25
> **背景**：基于 aurora 项目（ChatGPT API 反向代理）的源码分析，总结其如何完整通过 ChatGPT Sentinel 风控系统（turnstile + PoW + session observer）的全部校验。
> **参考文档**：[[sentinel-dx-expert-questions]] — 记录了逆向过程中遇到的核心未知问题，本文档即是对这些问题的逐一回答。

---

## 目录

1. [架构总览](#1-架构总览)
2. [第一层：TLS/HTTP 指纹模拟](#2-第一层tlshttp-指纹模拟)
3. [第二层：Proof of Work (PoW)](#3-第二层proof-of-work-pow)
4. [第三层：Turnstile VM 挑战求解](#4-第三层turnstile-vm-挑战求解)
5. [第四层：SO (Session Observer) VM](#5-第四层so-session-observer-vm)
6. [第五层：浏览器指纹数据池](#6-第五层浏览器指纹数据池)
7. [第六层：请求编排 (Orchestration)](#7-第六层请求编排-orchestration)
8. [关键设计决策](#8-关键设计决策)
9. [代码索引](#9-代码索引)

---

## 1. 架构总览

```
┌─────────────────────────────────────────────────────────────┐
│                    用户请求 (OpenAI API)                      │
└──────────────────────────┬──────────────────────────────────┘
                           ▼
┌─────────────────────────────────────────────────────────────┐
│  Layer 1: TLS 指纹                                          │
│  httpclient/bogdanfinn/tls_client.go                        │
│  → Chrome 146 JA3 指纹 + HTTP/2                              │
├─────────────────────────────────────────────────────────────┤
│  Layer 2: Proof of Work                                     │
│  internal/prooftoken/prooftoken.go                          │
│  → FNV-1a 32-bit hash + 25 元素 fingerprint config           │
├─────────────────────────────────────────────────────────────┤
│  Layer 3: Turnstile VM                                      │
│  internal/turnstile/turnstile.go                            │
│  → 35 opcode 纯 Go VM，执行 dx 字节码产出 turnstile token     │
├─────────────────────────────────────────────────────────────┤
│  Layer 4: SO (Session Observer) VM                          │
│  internal/so/so.go                                          │
│  → 独立的 VM 实例，处理 collector_dx + snapshot_dx            │
├─────────────────────────────────────────────────────────────┤
│  Layer 5: Browser Fingerprint Pool                          │
│  internal/browserfp/browserfp.go + data.go                  │
│  internal/fingerprint/fingerprint.go                        │
│  → 真实设备指纹池（WebGL/Navigator/Screen/Network）           │
├─────────────────────────────────────────────────────────────┤
│  Layer 6: Request Orchestration                             │
│  internal/chatgpt/request.go                                │
│  → sentinel/req → prepare → turnstile → ping → finalize      │
└─────────────────────────────────────────────────────────────┘
```

**核心思路**：不是 "Hook 浏览器截获数据"，而是 **在 Go 中完美复刻浏览器的每一层行为**——从 TLS 握手到 JavaScript VM 执行。

---

## 2. 第一层：TLS/HTTP 指纹模拟

### 2.1 问题

ChatGPT 服务端检查 TLS ClientHello 的 JA3 指纹。如果用 Go 默认 `net/http`，JA3 指纹是 `Go-http-client`，直接被识别为非浏览器。

### 2.2 解决方案

**文件：`httpclient/bogdanfinn/tls_client.go`**

```go
// 使用 bogdanfinn/tls-client 库，强制模拟 Chrome 146 的 TLS 指纹
client, _ := tls_client.NewHttpClient(tls_client.NewNoopLogger(), []tls_client.HttpClientOption{
    tls_client.WithCookieJar(tls_client.NewCookieJar()),
    tls_client.WithTimeoutSeconds(600),
    tls_client.WithClientProfile(profiles.Chrome_146),  // ← JA3 = Chrome 146
}...)
```

**为什么选择 Chrome 146**：
- 与 `browserfp.UserAgents` 池中的 UA 版本一致
- TLS 握手特征（密码套件顺序、扩展列表、椭圆曲线）与声明 UA 互相印证
- 服务端可以交叉校验 TLS ClientHello 中的 `ALPN`、`supported_versions` 等扩展

### 2.3 补充：Sec-CH-UA 头

在 `jshook/scripts/image_gen_full_flow.py` 中还有 `sec-ch-ua` 头的配合：

```python
FINGERPRINT = {
    "sec-ch-ua": '"Microsoft Edge";v="143", "Chromium";v="143", "Not A(Brand";v="24"',
    "sec-ch-ua-mobile": "?0",
    "sec-ch-ua-platform": '"Windows"',
}
```

TLS Layer + HTTP Header 层的 UA/平台/版本信息**必须自洽**，否则被风控识别为不一致。

---

## 3. 第二层：Proof of Work (PoW)

### 3.1 问题

Sentinel 服务端下发的 prepare 响应中包含 PoW 挑战：

```json
{
  "proofofwork": {
    "required": true,
    "seed": "xxx",
    "difficulty": "0000ffff"
  }
}
```

客户端必须找到一个 nonce 使得 `FNV1a(seed + base64(config))` 的前缀 ≤ difficulty。

### 3.2 解决方案

**文件：`internal/prooftoken/prooftoken.go`**

#### 3.2.1 25 元素 Fingerprint Config

对齐 2026-06 浏览器抓包，config 是 **25 元素数组**（不是 23 项设备指纹——原文档有误）：

```go
// fingerprint.go:152-178
config := []any{
    screenSum,       // [0]  screen.width + screen.height
    dateStr,         // [1]  Date().toString() — 带时区名，如 GMT-0800 (Pacific Daylight Time)
    jsHeap,          // [2]  jsHeapSizeLimit
    0,               // [3]  nonce (PoW 迭代中覆盖)
    ua,              // [4]  navigator.userAgent
    sdkScript,       // [5]  SDK 脚本 URL — NOT null!
    buildID,         // [6]  data-build 属性
    primaryLang,     // [7]  navigator.language
    langsStr,        // [8]  navigator.languages.join(",")
    r4,              // [9]  Math.random() / elapsedMs (PoW 迭代中覆盖)
    navigatorProbe,  // [10] "X in navigator" 探测字符串
    docKey,          // [11] document 随机 key
    winKey,          // [12] window 随机 key
    perfNow,         // [13] performance.now()
    deviceID,        // [14] device_id (UUID v4)
    searchJoined,    // [15] location.search
    hwConc,          // [16] hardwareConcurrency
    timeOrigin,      // [17] performance.timeOrigin
    0, 0, 0, 0,      // [18-24] "X in window" 检查 × 7
    0, 0, 0,
}
```

**关键点**：
- [5] SDK 脚本 URL **不是 null**——浏览器侧是 `currentScript.src`，Go 侧必须填有效 URL
- [6] buildID 来自 chatgpt.com 页面的 `data-build` 属性
- [13] 与 [17] 必须时间自洽：`timeOrigin + performanceNow ≈ Date.now()`
- [14] deviceID 必须非空 UUID，否则被路由到 "mini 池"（低权限）

#### 3.2.2 FNV-1a Hash PoW

```go
// prooftoken.go:262-284
func (c *Config) SolveProofOfWork(seed, difficulty string) string {
    // 最多 500k 次迭代
    for i := 0; i < 500_000; i++ {
        nonce := i
        elapsed := time.Since(startTime).Milliseconds()
        config := c.buildConfig(rng, &nonce, &elapsed)
        encoded := EncodeConfig(config)           // base64(JSON.stringify(config))
        hashResult := FNV1aHash(seed + encoded)   // FNV-1a 32-bit → 8 hex chars
        if hashResult[:len(difficulty)] <= difficulty {
            return "gAAAAAB" + encoded + "~S"     // ← 成功
        }
    }
    // 失败 fallback: gAAAAAB + ErrorPrefix + base64(error) + ~S
    return PrefixProof + ErrorPrefix + DefaultErrorPayload + Suffix
}
```

**Token 格式**：

| Token 类型 | 前缀 | 用途 |
|---|---|---|
| Requirements | `gAAAAAC` | 首次 sentinel/req，不含 PoW，固定 [3]=1 |
| Proof | `gAAAAAB` | prepare + finalize，含 PoW 迭代结果 |
| Error fallback | `gAAAAABwQ8Lk5FbGpA2NcR9dShT6gYjU7VxZ4DImUi~S` | PoW 超时/失败 |

#### 3.2.3 时间自洽性

```go
// PoW 迭代中：
config[3] = nonce          // int: 0, 1, 2, ...
config[9] = elapsedMs      // int64: 实际耗时毫秒数

// 服务端可以反推:
// serverTime ≈ timeOrigin + elapsedMs + nonceOverhead
// 如果 elapsedMs 与实际请求到达时间的偏差过大 → 识别为非浏览器
```

---

## 4. 第三层：Turnstile VM 挑战求解

### 4.1 问题

服务端下发 `turnstile.dx`（Base64 密文），客户端必须产出 `turnstile` token。这是 Sentinel 最核心的挑战——只有**正确执行 VM 字节码**才能产出被接受的 token。

### 4.2 解决方案

**文件：`internal/turnstile/turnstile.go`**（1641 行，aurora 项目中最核心、最复杂的文件）

#### 4.2.1 VM 架构

```
dx (Base64) → latin1Base64Decode → XOR(requirementsToken) → JSON.parse → [[op, args...], ...]
                                                                              ↓
                                                                    VM 指令队列 (reg 9)
                                                                              ↓
                                                               runQueue() 逐条执行
                                                                              ↓
                                                              opcode 3 (success) 触发
                                                                              ↓
                                                         latin1Base64Encode(result) → turnstile token
```

#### 4.2.2 35 个 Opcode 完整实现

| Opcode | 语义 | 对应 JS 操作 |
|--------|------|-------------|
| 0 | 递归调用 SolveDX | 子 turnstile 挑战 |
| 1 | XOR 加密/解密 | `reg[a] ^= reg[b]` |
| 2 | 赋值 | `reg[a] = b` |
| 3 | 成功退出 | `btoa(result)` |
| 4 | 失败退出 | `btoa(error)` |
| 5 | 拼接/加法 | `reg[a] += reg[b]`, 数组 append, 字符串 concat |
| 6 | 属性读取 | `reg[a] = reg[b][reg[c]]` |
| 7 | 函数调用（无返回值） | `reg[fn](args...)` |
| 8 | 移动 | `reg[a] = reg[b]` |
| 9 | 指令队列指针 | (保留寄存器) |
| 10 | 全局对象引用 | (保留寄存器) |
| 11 | 正则匹配 script src | `document.scripts[i].src.match(pattern)` |
| 12 | 寄存器映射引用 | `reg[a] = regMapRef` |
| 13 | Try-Catch | `try { reg[fn](args) } catch(e) { reg[a] = e.message }` |
| 14 | JSON.parse | `reg[a] = JSON.parse(reg[b])` |
| 15 | JSON.stringify | `reg[a] = JSON.stringify(reg[b])` |
| 16 | XOR 密钥 | (保留寄存器 = requirementsToken) |
| 17 | 函数调用（有返回值） | `reg[a] = reg[fn](args...)` |
| 18 | atob | `reg[a] = atob(reg[a])` |
| 19 | btoa | `reg[a] = btoa(reg[a])` |
| 20 | 条件跳转（相等） | `if (reg[a] === reg[b]) call(reg[fn])` |
| 21 | 条件跳转（差值） | `if (abs(reg[a]-reg[b]) > threshold) call(reg[fn])` |
| 22 | 子 VM 调用（替换队列） | `push queue; run subQueue; pop queue` |
| 23 | 条件跳转（非空） | `if (reg[a] != null) call(reg[fn])` |
| 24 | 方法绑定 | `reg[a] = reg[b][reg[c]].bind(reg[b])` |
| 25 | NOP | (空操作) |
| 26 | NOP | (空操作) |
| 27 | 减法/移除 | `reg[a] -= reg[b]`, 数组 remove |
| 28 | NOP | (空操作) |
| 29 | 小于比较 | `reg[a] = reg[b] < reg[c]` |
| 30 | DEF_FUNC | 创建子 VM 函数 |
| 31-32 | (未使用) | |
| 33 | 乘法 | `reg[a] = reg[b] * reg[c]` |
| 34 | Promise.resolved | `reg[a] = reg[b]` (同步简化) |
| 35 | 除法 | `reg[a] = reg[b] / reg[c]` |

#### 4.2.3 opcode 30 (DEF_FUNC) —— 文档中最大的谜题

**问题回放**：文档中记录 4 个 op30 函数（186 条"从不执行"的指令），困惑它们到底做什么。

**Aurora 的答案** (`turnstile.go:208-246`)：

```go
s.setReg(callbackReg, vmFunc(func(args ...any) (any, error) {
    targetReg := args[0]    // 目标寄存器编号
    returnReg := args[1]    // 返回值寄存器编号
    argRegs := args[2]      // 参数映射 [paramReg1, paramReg2, ...]
    innerQueue := args[3]   // 函数体指令队列 (186 条指令在这里!)

    s.setReg(targetReg, vmFunc(func(callArgs ...any) (any, error) {
        prevQ := s.copyQueue()              // 保存当前执行上下文
        for i, regID := range mappedArgRegs {
            s.setReg(regID, callArgs[i])     // 绑定实参到寄存器
        }
        s.setReg(pcReg, innerQueue)          // 切换到子函数指令队列
        s.runQueue()                         // 执行子函数体
        s.setReg(pcReg, prevQ)              // 恢复调用方队列
        return s.getReg(returnReg), nil      // 返回结果
    }))
}))
```

**关键洞察**：

1. **op30 创建的是"闭包"——可复用的子 VM 函数**
2. 子函数体 (`innerQueue`) 是完整独立的指令队列，**由调用方在需要时显式触发**
3. 调用方通过 opcode 7/17 + 目标寄存器编号触发执行
4. 这 186 条指令**不是"不执行"**——它们在以下时机执行：
   - `__oai_so_h` 函数被设为 `window` 属性后，被 VM 后续指令链调用
   - 作为 `addEventListener` 回调注册后，被模拟的"事件"触发
5. 每次调用**覆写固定寄存器**（不是追加），所以累加器大小不随调用次数增长

**这直接解释了为什么 T2（更多交互）反而比 T1 更小**：不同交互触发了不同的子 VM 函数，它们写入的寄存器目标不同。

#### 4.2.4 XOR 密钥派生 —— 另一个核心谜题

**问题**：turnstile 的 XOR key 是什么？与 PoW key 是什么关系？

**Aurora 的答案** (贯穿整个 VM 实现)：

```
XOR key = requirementsToken = "gAAAAAC" + base64(fingerprintConfig25) + "~S"
```

| 用途 | 密钥 | 位置 |
|------|------|------|
| VM 指令解密 | requirementsToken | `xorString(base64decode(dx), requirementsToken)` |
| VM 内部 XOR (opcode 1) | requirementsToken (reg 16) | `xorString(reg[target], reg[key])` |
| 每次 finalize | 新 requirementsToken | 新 session → 新 config → 新 token |

**因为每次 key 不同 → 每次 turnstile 输出完全不同 → 0 字节公共前缀完全正常。**

#### 4.2.5 浏览器 Window Mock —— 为什么输出 2000+ bytes

**文件中最关键的部分** — `turnstile.go:buildWindow()`（第 561-1014 行，450+ 行代码）：

这个函数构造了一个**完整的浏览器环境 mock**，包含 `authWindowKeyOrder` 中列出的全部 ~200 个属性：

```go
// 核心 mock 对象及其数据源：

// 1. navigator — turnstile.go:784-834
navigator := {
    userAgent, vendor, platform, hardwareConcurrency, deviceMemory,
    maxTouchPoints, language, languages, webdriver, clipboard, xr,
    storage, userAgentData, connection (effectiveType, rtt, downlink),
    // + prototype 上的 80+ 个属性 (authNavigatorPrototypeKeys)
}

// 2. screen — turnstile.go:687-696
screen := { width, height, availWidth, availHeight, colorDepth, pixelDepth, ... }

// 3. document — turnstile.go:697-783
document := {
    scripts, location, body, head, documentElement,
    createElement (含 canvas → WebGL context),
    getElementById, querySelector,
    cookie, referrer, readyState,
}

// 4. localStorage — turnstile.go:624-686
localStorage := {
    "statsig.stable_id.444584300": deviceID,
    "statsig.session_id.444584300": { sessionID, startTime, lastUpdate },
    "statsig.network_fallback.2742193661": urlConfig,
}

// 5. performance — turnstile.go:914-922
performance := {
    now(): 单调递增时间戳,
    timeOrigin,
    memory: { jsHeapSizeLimit },
}

// 6. WebGL — turnstile.go:754-779
canvas.getContext("webgl2") → getParameter(UNMASKED_VENDOR_WEBGL) → vendor string
canvas.getContext("webgl2") → getParameter(UNMASKED_RENDERER_WEBGL) → renderer string

// 7. React Router 上下文 — turnstile.go:970-999
__reactRouterContext, $RB, $RV, $RC, $RT, __reactRouterManifest,
__STATSIG__, __reactRouterVersion, __REACT_INTL_CONTEXT__,
DD_RUM, __SEGMENT_INSPECTOR__

// 8. 其他关键 window 属性
innerWidth, innerHeight, outerWidth, outerHeight,
screenX, screenY, scrollX, scrollY, devicePixelRatio,
isSecureContext, crossOriginIsolated,
atob, btoa, Math.random, JSON.parse, JSON.stringify,
Reflect.set, Object.keys, Object.create, Object.getPrototypeOf,
Array.from
```

**这就是 "~2000 bytes vs ~24 bytes" 差距的根本原因**：浏览器/aurora 的 VM 从 ~200 个属性中读取数据并混入 XOR 流，而原文档的 Go VM 只读取了 36 个 `__oai_so_*` 标量字段。

#### 4.2.6 JSON Key 顺序

**文件中最精细的设计** — `turnstile.go:1362-1417`：

```go
// withOrderedKeys 保证 JSON.stringify 输出时 key 顺序与浏览器一致
func withOrderedKeys(value map[string]any, keys []string) map[string]any {
    // 内部使用 orderedKeysMeta ("__ordered_keys__") 存储顺序
    // jsJSONStringify 按此顺序输出
}
```

**为什么重要**：Go 的 `map[string]any` key 顺序是随机的。但浏览器中 `Object.keys()` 和 `for...in` 有确定顺序（按属性插入顺序）。如果 JSON 的 key 顺序与浏览器不一致，服务端可能检测到异常。

---

## 5. 第四层：SO (Session Observer) VM

### 5.1 问题

Sentinel 还有一套独立的 "Session Observer" 系统，通过 `/sentinel/req` 下发 `collector_dx` 和 `snapshot_dx`。结果放入 `openai-sentinel-so-token` header。

### 5.2 解决方案

**文件：`internal/so/so.go`**（1069 行）

#### 5.2.1 与 Turnstile VM 的关键区别

| 维度 | Turnstile VM (`turnstile.go`) | SO VM (`so.go`) |
|------|-------------------------------|-----------------|
| 触发方式 | 同步，prepare→finalize 之间 | 异步：collector 后台跑，snapshot 按需读 |
| 执行模式 | 一次跑完整个指令队列 | collector 模式（填 regs），snapshot 模式（读 regs） |
| 退出方式 | opcode 3/4 (success/error) | snapshot 模式才有 success/error |
| regs 复用 | 无 | collector 的 regs 传给 snapshot 复用 |
| 浏览器 mock | 完整（450 行 buildWindow） | 简化版（~150 行 buildWindow） |

#### 5.2.2 Collector + Snapshot 两阶段

```go
// so.go:57-105

// Phase 1: Collector（异步，fire-and-forget）
func (s *Session) Start() <-chan struct{} {
    go func() {
        _, err := s.collector.run(s.reqToken, s.dx, true /* collector mode */)
        // collector mode: 不设 success/error，跑完指令链后保留 regs
    }()
}

// Phase 2: Snapshot（同步，复用 collector 的 regs）
func (s *Session) Snapshot(snapshotDX string) (string, error) {
    return collector.run(s.reqToken, snapshotDX, false /* snapshot mode */)
    // snapshot mode: 设 success/error，读取 collector 阶段写入的 regs
}
```

**为什么需要复用 regs**：snapshot_dx 字节码中只有 "读 reg[42]" 这样的寄存器编号，字段含义在 collector 阶段动态确定。不复用 regs 会导致 snapshot 读到错误的值。

#### 5.2.3 SO Token 构造

```go
// so.go:112-127
func BuildToken(soResult, chatReqToken, deviceID, flow string) (string, error) {
    payload := map[string]string{
        "so":   soResult,     // snapshot 产出
        "c":    chatReqToken, // collector 阶段的 token
        "id":   deviceID,     // 设备 ID
        "flow": flow,         // 流程标识
    }
    return base64.StdEncoding.EncodeToString(json.Marshal(payload))
}
```

---

## 6. 第五层：浏览器指纹数据池

### 6.1 问题

每次请求需要使用不同的浏览器指纹（随机但真实），避免被聚合分析识别为同一设备。

### 6.2 解决方案

**文件：`internal/browserfp/browserfp.go` + `internal/browserfp/data.go`**

#### 6.2.1 Profile 结构

```go
// browserfp.go:121-138
type Profile struct {
    WebGLUnmaskedRenderer string   // 从 92 个真实 GPU 中随机选
    WebGLUnmaskedVendor   string   // 从 34 个真实 vendor 中随机选
    Language              string   // 8 种语言组合
    BuildID               string   // chatgpt.com 的 data-build
    Platform              string   // 6 种平台
    ScreenWidth           int      // 从 20+ 分辨率 + 随机抖动中选
    ScreenHeight          int
    ScreenAvailHeight     int      // height - taskbar(20-60px)
    ScreenColorDepth      int      // 固定 24
    HardwareConcurrency   int      // {4, 8, 12, 16, 20, 24, 32}
    DeviceMemory          int      // {4, 8, 16, 32}
    JSHeapSizeLimit       int64    // ~2GB-17GB
    NetworkDownlink       float64  // 从 175+ 个真实值中选
    NetworkRTT            int      // 从 21 个真实值中选
    DevicePixelRatio      float64  // {1.0, 1.25, 1.5, 2.0, 2.5, 3.0}
}
```

#### 6.2.2 数据池规模

| 数据池 | 条目数 | 示例 |
|--------|--------|------|
| WebGL Renderers | 92 | `ANGLE (NVIDIA, NVIDIA GeForce RTX 3090 Ti ...)` |
| WebGL Vendors | 34 | `Google Inc. (NVIDIA)`, `Apple Inc.` |
| Languages | 8 | `en-US,en`, `zh-CN,zh`, `ja,en` |
| UserAgents | 5 | Chrome 146/147/148 + Mac |
| Screen Resolutions | 26 | 1920×1080, 2560×1440, 3840×2160, ... |
| Network Downlinks | 175+ | 0.25-10 Mbps 的各种真实组合 |
| Network RTTs | 21 | 50ms, 100ms, ..., 3000ms |
| Device Pixel Ratios | 14 | 1.0 (×4), 1.25 (×2), 1.5 (×3), 2.0 (×4), ... |

#### 6.2.3 指纹生成流程

```go
// browserfp.go:151-191
func Generate(rng *rand.Rand) *Profile {
    lang := Languages[rng.Intn(len(Languages))]        // 随机语言
    platform := Platforms[rng.Intn(len(Platforms))]     // 随机平台
    sr := screenResolutions[rng.Intn(len(screenResolutions))]
    sw, sh := sr[0]+rng.Intn(101)-50, sr[1]+rng.Intn(101)-50  // +-50px 抖动
    return &Profile{
        WebGLUnmaskedRenderer: webglUnmaskedRenderers[随机],
        WebGLUnmaskedVendor:   webglUnmaskedVendors[随机],
        Language:     lang.Code,
        Platform:     platform,
        ScreenWidth:  sw,
        ScreenHeight: sh,
        // ... 每个字段都从真实池中随机选择
    }
}
```

**关键设计**：
- **进程级单例** (`browserfp.Init()` → `Get()`)：同一进程内指纹固定，避免同一个 client 的请求被识别为不同设备
- **每次启动随机**：不同部署/重启生成不同指纹，天然防关联
- **+-50px 分辨率抖动**：模拟真实设备的小幅差异

---

## 7. 第六层：请求编排 (Orchestration)

### 7.1 完整流程

**文件：`internal/chatgpt/request.go`**

```
┌──────────────────────────────────────────────────────────────┐
│ Step 1: sentinel/req                                          │
│ POST /backend-api/sentinel/req                                │
│ Body: { p: "gAAAAAC...", id: deviceID, flow: "chatgpt" }     │
│ → 获取 oai-sc cookie + token                                  │
├──────────────────────────────────────────────────────────────┤
│ Step 2: chat-requirements/prepare                             │
│ POST /backend-api/sentinel/chat-requirements/prepare          │
│ Body: { p: "gAAAAAC...", id: deviceID, flow: "chatgpt" }     │
│ → 获取 proof.seed/difficulty, turnstile.dx, so.collector_dx   │
│   so.snapshot_dx, prepare_token                               │
├──────────────────────────────────────────────────────────────┤
│ Step 3: 本地计算                                              │
│ a) PoW: SolveProofOfWork(seed, difficulty) → proofToken       │
│ b) Turnstile: SolveDX(requirementsToken, dx) → turnstileToken │
│ c) SO Collector: soSession.Start() (异步)                     │
├──────────────────────────────────────────────────────────────┤
│ Step 4: chat-requirements/finalize                            │
│ POST /backend-api/sentinel/chat-requirements/finalize         │
│ Body: { prepare_token, p: proofToken, turnstile: turnstile }  │
│ → 获取 chat-requirements token                                │
├──────────────────────────────────────────────────────────────┤
│ Step 5: sentinel/ping (可选)                                  │
│ POST /backend-api/sentinel/ping                               │
│ Headers: openai-sentinel-token, openai-sentinel-extra-data    │
│ → 心跳/风控汇报                                                │
├──────────────────────────────────────────────────────────────┤
│ Step 6: SO Snapshot (业务请求时)                               │
│ soSession.Snapshot(snapshotDX) → soResult                      │
│ BuildToken(soResult, chatToken, deviceID, flow) → soToken     │
│ 注入 openai-sentinel-so-token header                          │
└──────────────────────────────────────────────────────────────┘
```

### 7.2 关键实现细节

#### 7.2.1 Sentinel-Extra-Data header

```go
// request.go:381-400
func buildSentinelExtraData(...) string {
    signals := sentinelExtraSignals{
        TurnstileTokenPresent:       boolToStr(turnstileToken != ""),
        ProofTokenPresent:           boolToStr(proofToken != ""),
        SOTokenPresent:              boolToStr(soTokenPresent),
    }
    // → JSON → base64 → openai-sentinel-extra-data header
}
```

**这是 `so_token_present=false` 的直接来源**——服务端通过这个 header 判断客户端的 token 是否存在。如果 header 声称 `so_token_present=true` 但 token 无效 → 风控降权。

#### 7.2.2 Conduit Token 三态

```go
// request.go:1229-1238
// conduit token 在 sentinel 上下文中签发,服务器据此判定客户端可信度与模型路由
//  1. /sentinel/req          → oai-sc cookie (会话级)
//  2. /sentinel/chat-requirements/prepare → prepare_token
//  3. /sentinel/ping         → 风控汇报
//  4. /sentinel/chat-requirements/finalize → chat-requirements token
//  5. /f/conversation/prepare (none→sent→success) → conduit tokens (带 sentinel 头)
```

#### 7.2.3 错误重试策略

```go
// request.go:237-255
// Free token 401 → 换 token 重试 (最多 2 次)
// Paid token 401 → 标记禁用，换下一个
// ForceLogin → Free token 重试 (最多 2 次)，否则 403
```

---

## 8. 关键设计决策

### 8.1 为什么纯 Go VM 而不是 CDP Hook 浏览器？

| 方案 | 优点 | 缺点 |
|------|------|------|
| CDP Hook (文档原方案) | 数据真实 | btoa Hook → VM 崩溃；SDK 缓存原生引用 → Hook 太晚；需要真实浏览器 |
| **纯 Go VM (aurora)** | **无外部依赖；性能高；可精确控制每个字节** | 需要完整逆向所有 opcode；需要构建浏览器 mock |

**aurora 的结论**：**CDP Hook 路径是死胡同**——SDK 缓存原生引用太早，Hook 注入必然打破 `this` 绑定导致 VM 崩溃。纯 Go 实现是唯一可行的生产方案。

### 8.2 为什么 Turnstile VM 和 SO VM 要分开实现？

虽然两者共享 0-35 opcode 表，但：
- Turnstile VM 需要**完整的浏览器 mock**（450 行 buildWindow），因为 VM 指令链会深度遍历 navigator/document/screen/localStorage
- SO VM 需要**寄存器跨阶段复用**（collector → snapshot），Turnstile VM 不需要
- 两者的退出语义不同（success/error 回调的参数格式不同）

### 8.3 为什么 Key 顺序这么重要？

`turnstile.go` 中 `withOrderedKeys` / `appendOrderedKey` / `orderedKeysMeta` 机制贯穿整个实现。原因：

1. JavaScript 中 `for...in` 和 `Object.keys()` 有确定顺序（插入顺序，数字键优先）
2. Go 的 `map[string]any` 迭代顺序是**随机的**
3. 如果 JSON.stringify 输出的 key 顺序与浏览器不一致：
   - 直接后果：XOR 密文不同 → 服务端解密失败
   - 间接后果：如果 XOR key 涉及 JSON 内容，不同的 key 顺序导致不同的 XOR key

### 8.4 为什么 `latin1Base64Encode` 而不是标准 base64？

```go
// turnstile.go:1602
func latin1Base64Encode(value string) string {
    return base64.StdEncoding.EncodeToString(latin1StringToBytes(value))
    // 每个 rune 截断到 byte: rune(0x0100) → byte(0x00)
}
```

浏览器 `btoa` 对输入字符串的处理是 **Latin-1 编码**（每个字符取低 8 位），不是 UTF-8。如果不做这个转换：
- 非 ASCII 字符会被 `json.Marshal` 转成 `\uXXXX` → base64 输出完全不同
- 例如 `"uoxy"` 重复 5 次特征：这些字符在 Latin-1 中是 4 个字节，而不是 Unicode 转义序列

### 8.5 数据一致性链

整个系统中最强的约束是**数据一致性链**：

```
TLS ClientHello (JA3 = Chrome 146)
    ↕ 必须一致
HTTP User-Agent header
    ↕ 必须一致
fingerprint config[4] (navigator.userAgent)
    ↕ 必须一致
fingerprint config[5] (SDK script URL — 版本号 "20260423af3c")
    ↕ 必须一致
TLS ALPN (h2/http1.1)
    ↕ 必须一致
buildWindow() 中的 sentinel SDK URL 版本
```

任何一环不一致 → 服务端检测为 "伪造浏览器" → `so_token_present=false`。

---

## 9. 代码索引

### 9.1 核心文件

| 文件 | 行数 | 职责 |
|------|------|------|
| `internal/turnstile/turnstile.go` | 1641 | Turnstile VM：35 opcode + 完整浏览器 mock |
| `internal/so/so.go` | 1069 | SO VM：collector/snapshot 两阶段 |
| `internal/prooftoken/prooftoken.go` | 348 | PoW：FNV-1a hash + 25 元素 config |
| `internal/fingerprint/fingerprint.go` | 232 | Build25：25 元素指纹数组生成 |
| `internal/browserfp/browserfp.go` | 198 | Profile 生成 + 全局单例 |
| `internal/browserfp/data.go` | 158 | 指纹数据池（92 GPU, 26 分辨率, 175+ 网速） |
| `internal/chatgpt/request.go` | ~3200 | 请求编排：prepare→turnstile→ping→finalize |
| `httpclient/bogdanfinn/tls_client.go` | ~80 | TLS 指纹模拟（Chrome 146） |

### 9.2 辅助文件

| 文件 | 职责 |
|------|------|
| `httpclient/Iaurorahttpclient.go` | HTTP 客户端接口定义 |
| `initialize/handlers.go` | API 路由 + turnstile 重试逻辑 |
| `initialize/session_manager.go` | WebSocket 会话管理 |
| `internal/chatgpt/cookie_bootstrap.go` | Cookie bootstrap 流程 |
| `internal/chatgpt/request_test.go` | 测试 |
| `util/useragent.go` | UA 管理 |

### 9.3 关键常量定义

| 常量 | 文件 | 值 |
|------|------|-----|
| `PrefixRequirements` | `prooftoken.go:26` | `gAAAAAC` |
| `PrefixProof` | `prooftoken.go:28` | `gAAAAAB` |
| `Suffix` | `prooftoken.go:30` | `~S` |
| `ErrorPrefix` | `prooftoken.go:34` | `wQ8Lk5FbGpA2NcR9dShT6gYjU7VxZ4D` |
| `DefaultFlow` | `prooftoken.go:41` | `chatgpt` |
| `fingerprintSize` | `prooftoken.go:44` | `25` |
| `DefaultBuildID` | `browserfp.go:108` | `prod-2e2e6a...` |
| `turnstileCallbackReg` (= 30) | `turnstile.go:27` | DEF_FUNC |
| `turnstileKeyReg` (= 16) | `turnstile.go:26` | XOR 密钥寄存器 |
| `turnstileQueueReg` (= 9) | `turnstile.go:24` | 指令队列寄存器 |

---

## 附录：与原逆向文档的问题对照

| 原文档问题 | Aurora 答案 | 关键代码 |
|------------|------------|----------|
| Q1.1: btoa 输入 `t` 是什么？ | VM 逐字节构建的字符串，含 XOR+拼接+属性读取 | `turnstile.go:177-271` |
| Q1.2: XOR 在哪里发生？ | VM 内部 opcode 1，key=requirementsToken(reg 16) | `turnstile.go:284-291` |
| Q1.3: turnstile 独立吗？ | 是，每次 requirementsToken 不同 | `prooftoken.go:244-252` |
| Q2.1: op30 做什么？ | 创建可复用子 VM 函数（闭包），覆写固定寄存器 | `turnstile.go:208-246` |
| Q2.2: ~2000 bytes 来源？ | 完整浏览器 mock：navigator/document/screen/localStorage/WebGL/React | `turnstile.go:561-1014` |
| Q3.1: 服务端校验什么？ | 解密正确性 + fingerprint 一致性 + PoW + TLS/HTTP 一致性 | `request.go:381-400` |
| Q3.2: 如何区分失败原因？ | XOR preview 日志 → 乱码=解密失败 vs JSON 合法=内容验证失败 | `request.go` log.Printf 链 |
| Q4.1: 完整数据源？ | ~200 个属性：参见 `authWindowKeyOrder` 和 `authNavigatorPrototypeKeys` | `turnstile.go:89-142` |
| Q4.2: undefined vs null？ | nil → JSON null，key ordering 保证一致性 | `turnstile.go:1362-1417` |
| Q5.1: XOR key 是什么？ | requirementsToken (gAAAAAC...)，指令解密+内部 XOR 都用它 | `turnstile.go:247` |
| Q5.2: 为什么 0 公共字节？ | 每次 session 新 key → 完全不同密文输出 | `prooftoken.go:245` |
| Q6.1: ~24 vs ~2000 bytes？ | 你的 VM 只读了 36 个标量字段，浏览器读了 ~200 个对象属性 | `turnstile.go:561-1014` |
| Q7.1: 如何获取 ground truth？ | 不需要 CDP — 用服务端返回值做 Oracle，纯 Go 复刻 | 全部 |

---

**结论**：Aurora 通过"在 Go 中完美复刻浏览器行为"的策略，构建了一套 **6 层防御体系**——从 TLS 指纹到 JavaScript VM 执行——每一层都精确对齐真实浏览器的行为。这不是"绕过"Sentinel，而是**正确实现** Sentinel 协议所要求的每一个步骤。
