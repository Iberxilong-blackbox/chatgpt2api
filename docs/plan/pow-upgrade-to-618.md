# PoW 算法升级：对齐 ChatGPT 最新 Sentinel SDK（618 新版）

> 基于 `docs/riskM/PoW-618-new.md` 逆向分析，将聊天 PoW 从旧版 25 元素指纹升级为 OpenAI 现行 23 元素指纹。
> 改动日期：2026-06-20

## 背景

OpenAI 在 2026 年端午前后更新了 ChatGPT Sentinel SDK 的 PoW 实现，几乎所有开源 2API 项目仍沿用旧版算法，极易触发风控降智。

新版 Sentinel SDK 的核心调用链：

```
getEnforcementToken → _getAnswer → _generateAnswerSync → _runCheck
```

## 改动范围

| 文件 | 改动类型 | 说明 |
|------|---------|------|
| `internal/backend/pow.go` | 重写 | buildPOWConfig / powGenerate / buildProofToken |

## 逐项差异与改动

### 一、Fingerprint Config 结构：25 → 23 元素

新版 `getConfig()` 返回 23 个元素，与旧版 25 元素的语义完全不同。以下按索引对齐：

| 索引 | 旧版 (25) | 新版 (23) | 类型变化 |
|------|----------|----------|---------|
| 0 | `int` (3000/4000/5000) | `string` ("3000") | int→string |
| 1 | EST 时间 | `Date().toString()` (PDT) | 时区变更 |
| 2 | `4294705152` (feature flags) | `"4294967296"` (jsHeapSizeLimit) | int64→string |
| 3 | nonce 占位 (运行时写入) | nonce 占位 (运行时写入) | 相同 |
| 4 | userAgent | **Math.random()** | 含义完全变更 |
| 5 | script src URL | **userAgent** | 含义完全变更 |
| 6 | dataBuild | **script src URL** | 含义完全变更 |
| 7 | `"en-US"` | **dataBuild** | 含义完全变更 |
| 8 | `"en-US,es-US,en,es"` (Accept-Lang) | `"en-US"` (navigator.language) | 简化 |
| 9 | i>>1 占位 (运行时写入) | elapsed ms 占位 (运行时写入) | 计算方式变更 |
| 10 | navigator key string | **`[]string{"en-US","en"}`** (navigator.languages) | string→[]string |
| 11 | document key | **Math.random()** | 含义完全变更 |
| 12 | window key | **Object.keys 随机键** | 含义完全变更 |
| 13 | performance.now() | **Object.getOwnPropertyNames(window)** | 含义完全变更 |
| 14 | UUID (session SID) | **performance.now()** | 含义完全变更 |
| 15 | `""` (空) | **sessionStorage.sid** | 含义完全变更 |
| 16 | CPU 核心数 | **URLSearchParams** | 含义完全变更 |
| 17 | timeOrigin | **navigator.platform** ("Win32") | 含义完全变更 |
| 18 | `"ai" in window` | **timeOrigin** | 含义完全变更 |
| 19 | `"createPRNG" in window` | `"ai" in window` | 检测项移位 |
| 20 | `"cache" in window` | `"InstallTrigger" in window` | 检测项移位 |
| 21 | `"data" in window` | `"solana" in window` | 检测项移位 |
| 22 | `"solana" in window` | **`"TextEncoder" in window`** | 去旧增新 |
| 23 | `"dump" in window` | — | 移除 |
| 24 | `"InstallTrigger" in window` | — | 移到索引20 |

**关键变化**：
- 去掉了 `createPRNG`、`cache`、`data`、`dump` 4 个检测位
- 新增 `TextEncoder` 检测位（Chrome 下 = 1）
- `jsHeapSizeLimit` 从 feature-flag 变为真实内存限制值
- 时间戳改用浏览器真实时区 (`America/Los_Angeles`)

### 二、`_runCheck` 算法修正

#### 2.1 `config[9]` 值：i>>1 → 实际耗时

```go
// 旧：无意义的 nonce 右移
config[9] = i >> 1

// 新：实际经过毫秒数（与 JS performance.now() 语义对齐）
config[9] = int64(math.Round(float64(time.Since(t0)) / float64(time.Millisecond)))
```

旧版 `i>>1` 是一个完全脱离浏览器语义的值——真实浏览器的 `performance.now()` 返回的是页面加载后的毫秒数，而非 nonce 的位运算结果。新版使用 `time.Since(t0)` 计算实际耗时，与浏览器行为对齐。

#### 2.2 JSON 序列化：分片拼接 → 完整 Marshal

```go
// 旧：预计算 JSON 片段，在循环中拼接
part1 := mustMarshal(config[:3])
part2 := mustMarshal(config[4:9])
part3 := mustMarshal(config[10:])
for i := 0; i < limit; i++ {
    finalJSON := bytes.Join([][]byte{part1, []byte(fmt.Sprint(i)), part2, ...}, nil)
    ...
}

// 新：每次迭代完整 json.Marshal，匹配 JS JSON.stringify 行为
for nonce := 0; nonce < limit; nonce++ {
    var buf bytes.Buffer
    enc := json.NewEncoder(&buf)
    enc.SetEscapeHTML(false)  // 对齐 JS JSON.stringify（不转义 HTML）
    enc.Encode(config)
    encoded := base64.StdEncoding.EncodeToString(bytes.TrimSpace(buf.Bytes()))
    ...
}
```

旧版的分片拼接是一种性能优化，但它对 config 数组结构敏感（依赖索引边界），且 `json.Marshal` 默认转义 HTML 字符 (`<`→`<`)，与 JS `JSON.stringify` 行为不一致。新版通过 `SetEscapeHTML(false)` 消除此差异。

#### 2.3 Result 后缀：追加 `~S`

```go
// 旧: "gAAAAAB" + answer
// 新: "gAAAAAB" + answer + "~S"
```

新版 SDK 的 `_runCheck` 成功时返回 `base64fp + "~S"`，`_getAnswer` 再拼前缀 `gAAAAAB`。

`~S` 只加在 **proof token** (`gAAAAAB`)，**legacy requirements token** (`gAAAAAC`) 不加。这与注册流程的实现一致。

#### 2.4 失败兜底：固定前缀 → 随机字符串

```go
// 旧：所有请求共享同一固定前缀 — 明显指纹
"wQ8Lk5FbGpA2NcR9dShT6gYjU7VxZ4D" + base64('"'+seed+'"')

// 新：随机 base64
randomBase64(24)
```

### 三、未变更项

| 项目 | 说明 |
|------|------|
| Hash 算法 (`zvtHash`) | FNV-1a 32-bit，已与新版一致 |
| `gAAAAAB` 前缀 | 保持 |
| `gAAAAAC` 前缀 | 保持，legacy requirements token 不加 `~S` |
| `parsePOWResources` | Bootstrap 中提取 script src / dataBuild 的逻辑不变 |
| `turnstile.go` | Turnstile VM 与 PoW config 结构无关 |
| `internal/service/register.go` | 注册 PoW 已正确（有 `~S`、真实 elapsed、完整 JSON marshal） |

### 四、向后兼容

- `buildLegacyRequirementsToken` 和 `buildProofToken` 的函数签名未变，调用方（`backend.go`）无需修改
- 所有已有测试通过
- 新旧 config 的 `powGenerate` 返回值格式不变（raw base64，不含前缀/后缀），前缀/后缀由各自调用方处理

### 五、Object Keys 采样池

新版 `buildPOWConfig` 内置了 Object.keys 和 Object.getOwnPropertyNames(window) 的随机采样池：

**Object.keys**（React DOM 注入属性）：
- `_reactListening8in7sfyhjvp`
- `_reactListeningo743lnnpvdg`
- `_reactListening` + 随机 8 位 hex
- `__reactFiber$` + 随机 8 位 hex
- `__reactProps$` + 随机 8 位 hex

**Object.getOwnPropertyNames(window)**：
- `onchange`, `location`, `closed`, `postMessage`, `queueMicrotask`
- `requestAnimationFrame`, `setInterval`, `setTimeout`, `caches`
- `indexedDB`, `sessionStorage`, `localStorage`, `performance`
- `crypto`, `navigator`, `screen`, `fetch`

每次请求从池中随机采样，模拟浏览器中 `Object.keys` 和 `Object.getOwnPropertyNames` 的遍历不确定顺序。
