# PoW（Proof of Work）计算机制

## 概述

项目中有 **两套独立的 PoW 实现**，分别服务于不同的 OpenAI sentinel 接口。两套实现算法同构（FNV-1a 变种 hash + 暴力枚举），但 config 结构和 hash 细节略有不同。

---

## 一、聊天/生图 PoW

**核心文件**：`internal/backend/pow.go` | `internal/backend/backend.go` | `internal/backend/responses_image.go`

### 1.1 触发时机

以下三个入口在**每次请求前**都会执行完整的 PoW 流程：

| 入口函数 | 文件:行 | 场景 |
|---|---|---|
| `StreamConversation` | `backend.go:148` | 文本对话（匿名/登录） |
| `StreamMultimodalConversation` | `backend.go:765` | 多模态/视觉对话（需登录） |
| `streamOfficialResponsesImage` | `responses_image.go:184` | 生图（responses_image） |

### 1.2 完整流程

#### Step 0 — Bootstrap 初始化

```
Bootstrap() → GET chatgpt.com/
    → parsePOWResources(html) → 提取 <script> 标签的 sentinel SDK URL
    → 提取 <html data-build="..."> 中的 build hash
    → 时间戳 powTimeOrigin = now UnixMilli
```

产物存储到 Client 结构体的 `powSources` / `powDataBuild` / `powTimeOrigin` 字段。

`parsePOWResources` (`pow.go:21`)：用正则匹配 `<script src="...">` 标签，优先匹配 `c/{hash}/_` 格式的 dataBuild。

#### Step 1 — POST /prepare

```
GET /backend-anon/sentinel/chat-requirements/prepare  (匿名)
GET /backend-api/sentinel/chat-requirements/prepare    (登录)

请求体: {"p": "<legacy_requirements_token>"}
```

其中 `legacy_requirements_token` = `"gAAAAAC" + base64(json_config)`，difficulty 固定 `0fffff`（极低难度，几乎不过滤）。

#### Step 2 — 解 PoW 挑战

服务端返回 `prepare` 响应中包含 `proofofwork` 字段：

```json
{
  "proofofwork": {
    "required": true,
    "seed": "...",
    "difficulty": "..."
  }
}
```

客户端调用 `buildProofToken(seed, difficulty, ...)` → `powGenerate(seed, difficulty, config, 500000)`。

`buildRequirements()` (`backend.go:479`) 是入口，同时处理 PoW + Turnstile 两种挑战。

#### Step 3 — POST /finalize

```json
{
  "prepare_token": "<step1_token>",
  "proofofwork": "<poW_token>",
  "turnstile": "<turnstile_token>"
}
```

服务端返回最终的 `token`（`OpenAI-Sentinel-Chat-Requirements-Token`），随后注入请求头。

### 1.3 PoW 算法

`powGenerate` (`pow.go:121-145`)：

```
fn powGenerate(seed, difficulty, config[25], limit=500000):
    part1 = json([config[0], config[1], config[2]])[:-1]  // 末尾逗号
    part2 = json(config[4:9])[1:-1]                         // 前后去括号
    part3 = json(config[10:])[1:]                            // 前加逗号
    for i in 0..limit:
        json = part1 + i + part2 + (i>>1) + part3
        encoded = base64(json)
        hash = zvtHash(seed + encoded)
        if hash[:len(difficulty)] <= difficulty:
            return encoded    // 成功
    return random_base64(24)  // 失败兜底
```

- `i` 填入 config[3] 的位置，`i>>1` 填入 config[9] 的位置
- 这两个位置分别对应：`i` = PoW 计数器（浏览器侧通常是 `Math.random()` 重试次数），`i>>1` = 经过时间（ms）
- limit=500000（最大 50 万次尝试）

### 1.4 Hash 函数 — zvtHash

`zvtHash` (`pow.go:159-171`)：**FNV-1a 32-bit 变种**，匹配 ChatGPT sentinel SDK 的 `zvt()` 函数。

```
fn zvtHash(input):
    h = 2166136261            // FNV offset basis
    for c in input:
        h ^= uint32(c)
        h = h * 16777619      // FNV prime (32-bit imul)
    h ^= h >> 16
    h = h * 2246822507        // 额外混淆 (murmurhash finalizer)
    h ^= h >> 13
    h = h * 3266489909
    h ^= h >> 16
    return sprintf("%08x", h)  // 8 hex 字符 → 32-bit
```

### 1.5 PoW Config 结构（25 元素）

`buildPOWConfig` (`pow.go:61-118`) 返回：

| 索引 | 内容 | 含义 |
|------|------|------|
| 0 | 3000/4000/5000 | 随机屏幕宽度 |
| 1 | `"Mon Jan 02 2006 15:04:05 GMT-0500"` | 当前 EST 时间 |
| 2 | `4294705152` | 固定值（类似 feature flags） |
| 3 | `0` | 占位，由 powGenerate 替换为 i |
| 4 | userAgent | 浏览器 UA |
| 5 | scriptSrc | 随机 sentinel SDK 脚本 URL |
| 6 | dataBuild | `c/{hash}/_` |
| 7 | `"en-US"` | 语言 |
| 8 | `"en-US,es-US,en,es"` | Accept-Language |
| 9 | `0` | 占位，由 powGenerate 替换为 i>>1 |
| 10 | navigatorKey | 随机 navigator 属性 fingerprint |
| 11 | documentKey | 随机 document 属性 |
| 12 | windowKey | 随机 window 属性 |
| 13 | `performance.now()` | 页面已运行毫秒数 |
| 14 | UUID | 会话 ID |
| 15 | `""` | 空 |
| 16 | 8/16/24/32 | 随机 CPU 核心数 |
| 17 | timeOrigin | `performance.timeOrigin`（毫秒） |
| 18-24 | `0` | window 特性检测位（ai/createPRNG/cache/data/solana/dump/InstallTrigger） |

### 1.6 Token 前缀约定

| 前缀 | 含义 | 生成函数 |
|------|------|------|
| `gAAAAAC` | legacy requirements token（低难度 `0fffff`） | `buildLegacyRequirementsToken` |
| `gAAAAAB` | proof token（已解 PoW） | `buildProofToken` |

---

## 二、注册流程 PoW

**核心文件**：`internal/service/register.go`

### 2.1 触发时机

账号注册时，调用 `buildSentinelToken()` (`register.go:774`) 向 `sentinel.openai.com/backend-api/sentinel/req` 发起 POST。如果响应中 `proofofwork.required == true`，则解 PoW。

### 2.2 流程

```
buildSentinelToken()
    → POST sentinel.openai.com/backend-api/sentinel/req
        body: {"p": generateRequirementsToken(), "id": deviceID, "flow": flow}
    → 响应中获取 proofofwork.seed + proofofwork.difficulty
    → 如果 required:
        generateToken(seed, difficulty)  → 解 PoW
    → POST sentinel req (再次)
        body: {"p": proofToken, "t": turnstile, "c": challengeToken, ...}
    → 返回最终 sentinel token
```

### 2.3 算法

`generateToken` (`register.go:1397-1413`)：

```
fn generateToken(seed, difficulty):
    start = time.Now()
    data = config()             // 17 元素
    for i in 0..500000:         // registerSentinelMaxAttempts
        data[3] = i
        data[9] = elapsed_ms   // 实际经过毫秒
        payload = registerBase64JSON(data)
        hash = registerFNV1A32(seed + payload)
        if hash[:len(difficulty)] <= difficulty:
            return "gAAAAAB" + payload + "~S"    // 成功（后缀 ~S）
    return "gAAAAAB" + error_prefix + base64("None")  // 失败
```

核心差异：
- Config 只有 **17 个元素**（vs 聊天的 25 个）
- Hash 函数 `registerFNV1A32` 是**简化版** FNV-1a（无额外混淆步骤，仅 FNV 循环 + finalizer）
- 成功后追加 `~S` 后缀

### 2.4 注册 PoW Config 结构（17 元素）

`config()` (`register.go:1366-1387`)：

| 索引 | 内容 | 含义 |
|------|------|------|
| 0 | `"1920x1080"` | 固定屏幕分辨率 |
| 1 | UTC 时间 | `Mon Jan 02 2006 15:04:05 GMT+0000` |
| 2 | `4294705152` | 固定值 |
| 3 | `mathrand.Float64()` / `i` | 占位，被替换 |
| 4 | userAgent | 浏览器 UA |
| 5 | registerSentinelSDK | SDK URL |
| 6 | nil | — |
| 7 | nil | — |
| 8 | `"en-US"` | 语言 |
| 9 | `mathrand.Float64()` / elapsed | 占位，被替换 |
| 10 | navigator 属性 | 随机 fingerprint |
| 11 | document 属性 | 随机 |
| 12 | window 属性 | 随机 |
| 13 | `performance.now()` | 模拟页面运行时间 |
| 14 | UUID | 会话 SID |
| 15 | `""` | — |
| 16 | 4/8/12/16 | 随机 CPU 核心数 |

### 2.5 注册 Hash 函数 — registerFNV1A32

`registerFNV1A32` (`register.go:1434-1446`)：标准 FNV-1a 32-bit + Murmur3 finalizer，与 `zvtHash` 流程相同但实现略有差异。

### 2.6 常量

| 常量 | 值 | 位置 |
|------|-----|------|
| `registerSentinelMaxAttempts` | 500000 | `register.go:42` |
| `registerSentinelErrorPrefix` | `wQ8Lk5FbGpA2NcR9dShT6gYjU7VxZ4D` | `register.go:43` |

---

## 三、请求头注入

两个流程的 PoW token 最终都通过请求头发送给 OpenAI：

| 请求头 | 来源 | 值 |
|--------|------|-----|
| `OpenAI-Sentinel-Chat-Requirements-Token` | getChatRequirements 的 finalize 响应 | token |
| `OpenAI-Sentinel-Proof-Token` | buildProofToken 计算结果 | `gAAAAAB...` |
| `OpenAI-Sentinel-Turnstile-Token` | solveTurnstileToken | turnstile token |
| `openai-sentinel-token` | buildSentinelToken | 注册 sentinel token |

注入位置：
- 聊天请求：`conversationHeaders()` (`backend.go:898-910`)
- 生图请求：`responses_image.go:687-688`
- 注册请求：`register.go` 多处

---

## 四、FAQ / 关键决策

### 为什么从 SHA3-512 改为 FNV hash？

> 参见 `docs/riskM/todo.md:140`。原实现使用 `sha3.Sum512`，但 ChatGPT sentinel SDK 的 `_runCheck()` 实际调用 `zvt()`（FNV-1a 变种）。为"尽可能像浏览器"，改为 FNV hash。单次计算速度提升 10-50 倍。

### PoW 难度如何确定？

服务端在 `prepare` 响应中下发 `difficulty` 字段（如 `"0fffff"`），客户端计算 hash 前缀 ≤ difficulty 即通过。难度由 OpenAI 风控动态调整。

### 如果 PoW 失败会怎样？

`powGenerate` 返回 `randomBase64(24)` 作为兜底值（`solved=false`），`buildProofToken` 会返回 error，导致整个请求失败。

---

## 五、文件索引

| 文件 | 内容 |
|------|------|
| `internal/backend/pow.go` | 聊天/生图 PoW 核心（parsePOWResources, buildProofToken, powGenerate, zvtHash, buildPOWConfig） |
| `internal/backend/backend.go` | 聊天流程调用（Bootstrap, getChatRequirements, buildRequirements, conversationHeaders） |
| `internal/backend/responses_image.go` | 生图流程调用（streamOfficialResponsesImage） |
| `internal/service/register.go` | 注册 PoW 核心（registerSentinelTokenGenerator, registerFNV1A32, buildSentinelToken） |
