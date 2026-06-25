# 基于 Aurora 方案的代码改进计划

> **日期**：2026-06-25
> **参考**：[[aurora-sentinel-solution]] — aurora 项目完整 6 层防御体系
> **目标**：将当前项目的 sentinel 风控对抗能力对齐 aurora 参考实现，解决 `so_token_present=false` 问题
> **策略**：能直接用 aurora 代码的就直接引用（aurora 目录已存在于项目中），需要适配的在现有代码上修改

---

## 现状 vs Aurora 差距总览

| 层 | 当前项目 | Aurora | 差距 |
|---|---------|--------|------|
| L1: TLS 指纹 | `surf` 库 Chrome impersonation | `bogdanfinn/tls-client` Chrome_146 JA3 | JA3 不一致 |
| L2: PoW Config | 23 元素，类型/顺序有误 | 25 元素，严格对齐浏览器 | 2 元素缺失 + 类型错误 |
| L3: Turnstile VM | 仅模拟 36 个 `__oai_so_*` 标量，无浏览器 mock | 完整浏览器 mock：navigator/screen/document/localStorage/WebGL/React ~200 属性 | **根本原因：输出 ~24B vs ~2000B** |
| L4: SO VM | **无** | 完整 collector/snapshot 两阶段 VM | 整层缺失 |
| L5: 浏览器指纹池 | 硬编码散落各处 | 92 GPU + 34 vendor + 26 分辨率 + 175 网速数据池 | 指纹单一，易被聚合 |
| L6: 请求编排 | prepare→PoW→turnstile→finalize | sentinel/req→prepare→PoW+turnstile+SO→finalize→ping→SO snapshot | 缺 3 步 |

---

## 📊 实施进度

> 更新时间：2026-06-25

### ✅ Phase 1 完成 — 复制 aurora 内部包

- ✅ 复制 `aurora/internal/turnstile/` → `internal/turnstile/`（1641 行，35-opcode VM + 完整浏览器 mock）
- ✅ 复制 `aurora/internal/so/` → `internal/so/`（1069 行，collector/snapshot 两阶段 SO VM）
- ✅ 复制 `aurora/internal/prooftoken/` → `internal/prooftoken/`（348 行，25 元素 config + FNV-1a PoW）
- ✅ 复制 `aurora/internal/fingerprint/` → `internal/fingerprint/`（232 行，Build25 算法）
- ✅ 复制 `aurora/internal/browserfp/` → `internal/browserfp/`（198+158 行，数据池 + Profile 生成）
- ✅ 所有 import 路径从 `aurora/internal/...` 改为 `chatgpt2api/internal/...`
- ✅ 移除 `aurora/util` 依赖（内联 `fixedUserAgent` 常量）
- ✅ `browserfp.Get()` 添加自动初始化（防止 nil pointer）
- ✅ 编译通过，测试通过（30 个测试全部绿）

### ✅ Phase 2 完成 — 替换后端 VM

- ✅ `pow.go` — 重写
  - 删除 `buildLegacyRequirementsToken()`、`buildPOWConfig()`（23 元素）
  - 新增 `buildRequirementsToken()`（使用 `prooftoken.Config.GenerateRequirementsToken()`，25 元素）
  - 新增 `buildProofToken()`（使用 `prooftoken.Config.SolveProofOfWork()`）
  - 新增 `buildSentinelReqBody()`、`buildSentinelTokenHeader()` 辅助函数
  - 保留 `parsePOWResources()`、`zvtHash()`、`rawProofAnswer()`

- ✅ `turnstile.go` — 重写（从 650 行缩减到 27 行）
  - 删除自研的 650 行 VM（opcode 0-35, turnstileFunc, turnstileOrderedMap, xorTurnstileString 等）
  - 替换为 `turnstile.SolveDX()` 单调用（完整 35-opcode VM + buildWindow ~200 属性）

- ✅ `sentinel_dx.go` — 重写（从 1040 行缩减到 110 行）
  - 删除自研的 1040 行 VM（solveSentinelDxToken, initSimWindow, 36 个 __oai_so_* 字段）
  - 替换为 SO 桥接：`startSOCollector()` + `sosession.buildSOToken()`
  - 保留 `logSOEvent()` 诊断日志

- ✅ `backend.go` — 关键改动
  - `Client` 结构体新增 `soSess *sosession` 字段
  - `NewClient()` 添加 `browserfp.Init()` 初始化（sync.Once 保护）
  - `getChatRequirements()` 使用新 `buildRequirementsToken()` 生成 25 元素 p token
  - `buildRequirements()` 重写：PoW→prooftoken, Turnstile→aurora VM, SO→异步 collector 启动
  - `conversationHeaders()` 添加动态 SO snapshot token 注入
  - 移除旧的 `so` 字段从 finalize payload

- ✅ `responses_image.go` — `officialHeaders()` 添加 SO snapshot token 注入

- ✅ `backend_test.go` — 更新测试
  - 替换旧 VM 测试为新的空输入/边界测试
  - 所有 30 个测试通过

### ✅ Phase 3 完成 — L6 Headers

- ✅ L6: 添加 `openai-sentinel-token` header（JSON: {p, t, c, id, flow}）
  - `buildSentinelTokenHeader()` 已在 `pow.go` 中，现已在 `conversationHeaders()` 和 `officialHeaders()` 中调用
- ✅ L6: 添加 `openai-sentinel-extra-data` header（base64 JSON: {turnstile_present, proof_present, so_present}）
  - 新增 `buildSentinelExtraData()` 在 `pow.go` 中，已在两个 header 函数中调用
- ✅ 测试更新：`TestOfficialImageHeadersIncludeSentinelAndConduitTokens` 验证新 header 的格式和内容

### ⏳ 待完成

- [ ] L1: TLS 客户端切换（surf → bogdanfinn/tls-client Chrome 146）→ **暂缓，当前 surf + Chrome 145 已自洽**
- [ ] L5: 数据一致性链验证（UA ↔ TLS JA3 ↔ config[4] 自洽）→ **Chrome 145 已自洽**
- [ ] 部署到服务器 + 日志诊断验证

### ✅ Phase 4 完成 — L6 请求编排

- ✅ L6: 添加 `POST /sentinel/req` 步骤（prepare 之前，best-effort，测试环境自动跳过）
- ✅ L6: 添加 `POST /sentinel/ping` 步骤（finalize 之后，fire-and-forget goroutine）
- ✅ L6: 添加 `openai-sentinel-token` header（JSON: {p, t, c, id, flow}）
- ✅ L6: 添加 `openai-sentinel-extra-data` header（base64 JSON: {turnstile_present, proof_present, so_present}）
- ✅ 测试更新：新 header 格式和内容验证

### 🎯 下一步：部署验证

所有代码改动已完成。当前 sentinel 请求编排完整对齐 aurora 6 层体系：

```
sentinel/req → prepare → PoW+Turnstile+SO → finalize → ping
                                                          ↓
                                              conversation (含 SO snapshot + 6 个 sentinel headers)
```

Headers 注入清单：
| Header | 状态 |
|--------|------|
| `OpenAI-Sentinel-Chat-Requirements-Token` | ✅ |
| `OpenAI-Sentinel-Proof-Token` | ✅ |
| `OpenAI-Sentinel-Turnstile-Token` | ✅ |
| `OpenAI-Sentinel-SO-Token` | ✅ |
| `OpenAI-Sentinel-Token` | ✅ 新增 |
| `OpenAI-Sentinel-Extra-Data` | ✅ 新增 |



### 📁 新增文件

```
internal/
├── browserfp/
│   ├── browserfp.go   ← aurora (198 行，Profile + 数据池生成)
│   └── data.go        ← aurora (158 行，92 GPU, 34 vendor, 26 分辨率, 175+ 网速)
├── fingerprint/
│   └── fingerprint.go ← aurora (232 行，Build25 算法)
├── prooftoken/
│   └── prooftoken.go  ← aurora (348 行，25 元素 config + FNV-1a PoW)
├── turnstile/
│   └── turnstile.go   ← aurora (1641 行，35-opcode VM + buildWindow)
└── so/
    └── so.go          ← aurora (1069 行，collector/snapshot 两阶段 VM)
```

### 📁 修改文件

```
internal/backend/
├── backend.go         — 重写 getChatRequirements + buildRequirements
├── backend_test.go    — 更新测试
├── turnstile.go       — 替换为 aurora bridge (27 行)
├── sentinel_dx.go     — 替换为 SO bridge (110 行)
├── pow.go             — 重写 PoW 配置生成
└── responses_image.go — officialHeaders 添加 SO token
```

---

## TODO: Layer 1 — TLS/HTTP 指纹模拟

### 1.1 替换 HTTP 客户端为 bogdanfinn/tls-client

**文件**：`internal/service/proxy.go`

**现状**：使用 `enetx/surf` 库做浏览器 impersonation，TLS 指纹不可控
**目标**：使用 `bogdanfinn/tls-client` + `profiles.Chrome_146`，精确控制 JA3 指纹

**具体修改**：
- [ ] 1.1.1 在 `proxy.go` 中新增 `BrowserHTTPClientWithTLSProfile(profile string, timeout)` 方法，内部使用 `bogdanfinn/tls-client`
- [ ] 1.1.2 将默认 TLS profile 从 `surf` 的 `Impersonate().Windows().Chrome()` 改为 `tls_client.WithClientProfile(profiles.Chrome_146)`
- [ ] 1.1.3 在 `go.mod` 中添加依赖：
  ```
  github.com/bogdanfinn/tls-client
  github.com/bogdanfinn/fhttp
  ```
- [ ] 1.1.4 确保所有 sentinel 相关请求（sentinel/req, prepare, finalize, ping, conversation）都走新 TLS 客户端
- [ ] 1.1.5 验证 JA3 指纹为 Chrome 146（可用 Wireshark 或 ja3.me 验证）

**参考代码**：`aurora/httpclient/bogdanfinn/tls_client.go`

---

## TODO: Layer 2 — Proof of Work (25 元素 Fingerprint Config)

### ✅ 2.1 升级 fingerprint config 从 23 到 25 元素

**文件**：`internal/backend/pow.go`

**现状**：`buildPOWConfig()` 生成 23 元素 config，类型和顺序有多处偏差
**目标**：25 元素 config，严格对齐 aurora `fingerprint.Build25()`

**具体修改**：
- [x] 2.1.1 创建 `internal/fingerprint/`，移植 aurora `fingerprint.Build25()` 逻辑
  - 直接 import aurora 包：~~`import "aurora/internal/fingerprint"`~~（已改为复制到 `internal/fingerprint/`）
  - ✅ 已复制 `aurora/internal/fingerprint/fingerprint.go` 到 `internal/fingerprint/` 并适配 import
- [x] 2.1.2 修复 config 元素差异（对照表）：

| 索引 | 当前值 | 应为 | 修复 |
|------|--------|------|------|
| [0] | `string("3000")` | `int(3000)` | 类型改为 number |
| [5] | 可能为 null | `"https://chatgpt.com/backend-api/sentinel/sdk.js"` | 必须非空 URL |
| [10] | `[]string{"en-US","en"}` (array) | `"en-US,en"` (逗号分隔 string) | 类型改为 string |
| [11] | `rand.Float64()` | `"X in navigator"` 探测字符串 | 改为 navigator probe |
| [14] | `""`（空字符串） | UUID v4 | 必须非空 UUID |
| [17] | `"Win32"` (platform) | `float64(timeOrigin)` | 移到 [17] 正确位置 |
| [18-24] | **缺失** | 7 个 `"X in window"` 检查值 | 新增 7 个元素 |

- [x] 2.1.3 修复 `buildProofToken()` 使用正确的 25 元素 config ✅
- [x] 2.1.4 确保 `buildRequirementsToken()` 也使用 25 元素 config ✅

### ✅ 2.2 创建浏览器指纹数据池

**文件**：已创建 `internal/browserfp/`

- [x] 2.2.1 移植 `aurora/internal/browserfp/browserfp.go` 的 Profile 结构和 Generate 逻辑 ✅
- [x] 2.2.2 移植 `aurora/internal/browserfp/data.go` 的数据池 ✅
- [x] 2.2.3 在 `Backend.Client` 初始化时调用 `browserfp.Init()` 建立进程级单例 ✅

---

## TODO: Layer 3 — Turnstile VM（核心修复）✅ Phase 2 已完成

### 3.1 添加完整浏览器 Window Mock（最关键！）

**文件**：`internal/backend/sentinel_dx.go`

**现状**：VM 只从 36 个 `__oai_so_*` 标量字段读取，输出 ~24 bytes
**必须**：VM 需要完整的 `buildWindow()` 包含 ~200 个浏览器属性，输出 ~2000 bytes

**具体修改**：
- [ ] 3.1.1 **直接使用 aurora turnstile VM**：将 `aurora/internal/turnstile/turnstile.go` 作为新的 sentinel_dx 引擎
  - 在 `internal/backend/sentinel_dx.go` 中 import `aurora/internal/turnstile` 并调用 `turnstile.SolveDX(requirementsToken, dx)`
  - 移除当前自研的 1040 行 VM（`solveSentinelDxToken` 函数及其辅助函数）
  - 保留诊断日志（so_events 日志记录）

- [ ] 3.1.2 如果选择自己修改（不直接引用 aurora），至少需要添加以下到 `initSimWindow()`：

```
必须 mock 的对象和属性：
├── navigator: userAgent, vendor, platform, hardwareConcurrency, deviceMemory,
│   maxTouchPoints, language, languages, webdriver, clipboard, xr, storage,
│   userAgentData, connection (effectiveType, rtt, downlink),
│   + prototype 上 80+ 属性 (authNavigatorPrototypeKeys)
├── screen: width, height, availWidth, availHeight, colorDepth, pixelDepth
├── document: scripts[].src, location, body, head, documentElement,
│   createElement (含 canvas → WebGL context), getElementById, querySelector,
│   cookie, referrer, readyState
├── localStorage: statsig.stable_id, statsig.session_id, statsig.network_fallback
├── performance: now(), timeOrigin, memory.jsHeapSizeLimit
├── WebGL: UNMASKED_VENDOR_WEBGL, UNMASKED_RENDERER_WEBGL
├── React Router: __reactRouterContext, $RB, $RV, $RC, $RT
├── Statsig: __STATSIG__, __reactRouterVersion
├── 其他: innerWidth, innerHeight, outerWidth, outerHeight,
│   screenX, screenY, scrollX, scrollY, devicePixelRatio,
│   isSecureContext, crossOriginIsolated, atob, btoa,
│   Math.random, JSON.parse/stringify, Object.keys/create/getPrototypeOf,
│   Reflect.set, Array.from
```

- [ ] 3.1.3 添加 `withOrderedKeys` 机制 — JSON key 顺序必须与浏览器一致

### 3.2 修复 XOR 密钥

**文件**：`internal/backend/sentinel_dx.go` + `internal/backend/turnstile.go`

**现状**：`solveSentinelDxToken(dx, proofKey)` — XOR 密钥是 PoW answer
**必须**：XOR 密钥 = requirementsToken（`gAAAAAC` + base64(25-element-config) + `~S`）

**具体修改**：
- [ ] 3.2.1 `solveSentinelDxToken` 的第二个参数从 `proofKey` 改为 `requirementsToken`（即 `sourceP` / `p`）
- [ ] 3.2.2 在 `buildRequirements()` 中调用 `solveSentinelDxToken(dx, sourceP)` 而不是 `solveSentinelDxToken(dx, proofKey)`
- [ ] 3.2.3 确认 opcode 16 (XOR key register) 在 VM 中设置为 requirementsToken

### 3.3 添加 Latin-1 Base64 编解码

**文件**：`internal/backend/turnstile.go`

- [ ] 3.3.1 添加 `latin1StringToBytes(value string) []byte` — 每个 rune 截断到 byte
- [ ] 3.3.2 添加 `latin1Base64Encode(value string) string` — Latin-1 → base64
- [ ] 3.3.3 在 opcode 3 (success) 和 opcode 19 (btoa) 中使用 Latin-1 编码
- [ ] 3.3.4 在 opcode 18 (atob) 中使用 Latin-1 解码

### 3.4 修复 opcode 6 (属性读取)

**现状**：`left + "." + right` → 返回字符串路径而非实际属性值
**必须**：`reg[a] = reg[b][reg[c]]` — 读取实际对象属性

- [ ] 3.4.1 在 VM 中维护完整的 window 对象树（使用 aurora 的 `buildWindow()`）
- [ ] 3.4.2 opcode 6 实现真正的属性查找：`obj[key]`

---

## TODO: Layer 4 — SO (Session Observer) VM ✅ Phase 2 已完成

### 4.1 添加 SO VM 完整实现

**状态**：当前项目**完全没有** SO VM

**具体修改**：
- [ ] 4.1.1 **直接使用 aurora SO VM**：import `aurora/internal/so` 
  - 在 `internal/backend/` 中创建 `so_bridge.go`，封装对 aurora SO 包的调用
  - 暴露 `CreateSOSession(reqToken, collectorDX)` 和 `GetSOToken(session, snapshotDX, deviceID, flow)` 接口

- [ ] 4.1.2 如果选择自研，创建 `internal/backend/so.go`，实现：
  - `soSolver` 结构体 + VM
  - `run(reqToken, dx, isCollector bool)` — collector/snapshot 两模式
  - `BuildToken(soResult, chatReqToken, deviceID, flow)` — 构造 SO token
  - Collector 模式：异步 fire-and-forget，初始化 VM 寄存器
  - Snapshot 模式：同步，复用 collector 的 regs，输出结果

- [ ] 4.1.3 SO VM 需要简化版 `buildWindow()`（约 150 行，不需要 turnstile 的完整 mock）

---

## TODO: Layer 5 — 浏览器指纹数据池 ✅ Phase 1 已完成

### 5.1 建立指纹数据池

**文件**：新建或引用 aurora

- [ ] 5.1.1 **推荐**：直接 import `aurora/internal/browserfp` 使用其数据池和 Profile 生成
- [ ] 5.1.2 在 `Backend.Client` 初始化时：
  ```go
  import "aurora/internal/browserfp"
  
  func init() {
      browserfp.Init() // 进程级单例，每次启动随机
  }
  ```
- [ ] 5.1.3 在 `buildPOWConfig` / fingerprint 生成中引用 `browserfp.Get()` 获取当前指纹

### 5.2 数据一致性链检查

- [ ] 5.2.1 确保 TLS JA3 (Chrome 146) ↔ User-Agent header ↔ config[4] UA ↔ buildWindow UA 一致
- [ ] 5.2.2 确保 SDK 版本 `20260423af3c` 在所有地方一致（config[5], buildWindow sentinel URL, sec-ch-ua）
- [ ] 5.2.3 确保 timeOrigin + performanceNow ≈ Date.now() 时间自洽

---

## TODO: Layer 6 — 请求编排

### 6.1 添加 sentinel/req 步骤

**文件**：`internal/backend/backend.go`

**现状**：直接 POST prepare，缺少前置 sentinel/req
**目标**：完整 6 步编排

- [ ] 6.1.1 在 `getChatRequirements()` 中，prepare 之前添加 sentinel/req：
  ```go
  // Step 0: POST /backend-api/sentinel/req
  // Body: { p: requirementsToken, id: deviceID, flow: "chatgpt" }
  // → 获取 oai-sc cookie + token
  ```
- [ ] 6.1.2 正确处理 sentinel/req 响应中的 cookies（特别是 `oai-sc`）

### 6.2 添加 SO Collector 异步启动

- [ ] 6.2.1 在 `buildRequirements()` 中，prepare 响应返回 `so.collector_dx` 后：
  ```go
  soSession := so.NewSession(sourceP, collectorDX)
  soSession.Start() // 异步 fire-and-forget
  c.soSession = soSession // 保存到 client 状态
  ```

### 6.3 添加 SO Snapshot + Header 注入

- [ ] 6.3.1 在每次业务请求（conversation）前调用：
  ```go
  soResult, err := c.soSession.Snapshot(snapshotDX)
  soToken := so.BuildToken(soResult, chatToken, deviceID, flow)
  ```
- [ ] 6.3.2 在 conversation 请求中添加 `openai-sentinel-so-token` header
- [ ] 6.3.3 添加 `openai-sentinel-token` header（JSON 格式：`{p, t, c, id, flow}`）
- [ ] 6.3.4 添加 `openai-sentinel-extra-data` header（base64 JSON：turnstile/proof/so token present 标志）

### 6.4 添加 sentinel/ping

- [ ] 6.4.1 在 finalize 之后、业务请求之前，可选发送 sentinel/ping：
  ```go
  // POST /backend-api/sentinel/ping
  // Headers: openai-sentinel-token, openai-sentinel-extra-data
  ```

### 6.5 修复 conversation 请求的 sentinel headers

**文件**：`internal/backend/backend.go` — `conversationHeaders()` / `officialHeaders()`

- [ ] 6.5.1 在 `officialHeaders()` 中添加：
  - `openai-sentinel-token`: JSON `{p, t, c, id, flow}`
  - `openai-sentinel-so-token`: base64 JSON `{so, c, id, flow}`
  - `openai-sentinel-extra-data`: base64 JSON `{turnstile_present, proof_present, so_present}`

---

## TODO: 代码清理

### 7.1 删除废弃代码

- [ ] 7.1.1 如果成功切换到 aurora turnstile VM，删除 `internal/backend/sentinel_dx.go` 中自研的 1040 行 VM（或保留作为 fallback 并加 deprecated 注释）
- [ ] 7.1.2 如果成功切换 TLS 客户端，标记 `proxy.go` 中 surf 相关代码为 deprecated

### 7.2 日志改进

- [ ] 7.2.1 保留 `so_events.log` 诊断日志机制
- [ ] 7.2.2 在关键路径添加 aurora 风格的 diagnostic log（XOR preview, VM trace, finalize response）
- [ ] 7.2.3 添加 sentinel 整体流程的 timing log（每一步耗时）

---

## TODO: 部署和验证

### 8.1 编译和部署

- [ ] 8.1.1 `go build` 确保所有新依赖正确引入
- [ ] 8.1.2 部署到服务器
- [ ] 8.1.3 确保 `data/logs/` 目录存在且可写

### 8.2 日志诊断验证

- [ ] 8.2.1 发送测试对话请求
- [ ] 8.2.2 检查 finalize 响应中的 `so_token_present` 字段——**目标：从 false 变为 true**
- [ ] 8.2.3 如果仍为 false，检查以下诊断点：
  - XOR preview 是否为合法 JSON（`[[` 开头）→ 确认 XOR key 正确
  - Turnstile 输出大小是否 ~2000 bytes（非 ~24 bytes）→ 确认 buildWindow 生效
  - SO snapshot 是否正确复用 collector regs → 检查 SO 日志
  - TLS JA3 是否为 Chrome 146 → 检查服务端是否检测到 TLS 指纹异常
- [ ] 8.2.4 检查 `so_events.log` 中的事件，确认无 `unknown_opcode`、`dx_token_anomaly` 事件

### 8.3 回归测试

- [ ] 8.3.1 验证对话功能正常（streaming 响应OK）
- [ ] 8.3.2 验证图片生成功能正常
- [ ] 8.3.3 验证模型列表功能正常
- [ ] 8.3.4 验证匿名模式 + 登录模式均正常

---

## 执行优先级

```
P0 (阻塞 — 直接导致 so_token_present=false):
  ├── 3.1 添加完整浏览器 Window Mock ⭐ 最关键
  ├── 3.2 修复 XOR 密钥为 requirementsToken
  ├── 3.3 添加 Latin-1 Base64 编解码
  └── 3.4 修复 opcode 6 属性读取

P1 (缺失层 — 没有这些层服务端可能仍拒):
  ├── 4.1 添加 SO VM 完整实现
  ├── 2.1 升级 fingerprint config 23→25 元素
  ├── 6.1 添加 sentinel/req 步骤
  └── 6.2 添加 SO Collector 异步启动

P2 (加固 — 提升通过率):
  ├── 1.1 替换 TLS 客户端为 Chrome 146 JA3
  ├── 5.1 建立浏览器指纹数据池
  ├── 6.3 添加 SO Snapshot + Header 注入
  └── 6.5 修复 conversation 请求的 sentinel headers

P3 (完善):
  ├── 6.4 添加 sentinel/ping
  └── 7.x 代码清理
```

---

## 快速方案：直接引用 Aurora 代码

如果追求最快修复速度，可以在 `internal/backend/` 中直接 import aurora 包：

```go
import (
    "aurora/internal/turnstile"    // 替代 sentinel_dx.go
    "aurora/internal/so"           // 新增 SO VM
    "aurora/internal/prooftoken"   // 替代 pow.go PoW 部分
    "aurora/internal/fingerprint"  // 替代 buildPOWConfig
    "aurora/internal/browserfp"    // 替代硬编码指纹
)
```

然后在 `buildRequirements()` 中：
```go
// 1. Requirements token
cfg := prooftoken.NewConfig(c.userAgent)
requirementsToken := cfg.GenerateRequirementsToken()

// 2. PoW
proofToken := cfg.SolveProofOfWork(seed, difficulty)

// 3. Turnstile
turnstileToken, _ := turnstile.SolveDX(requirementsToken, dx)

// 4. SO
soSession := so.NewSession(requirementsToken, collectorDX)
soSession.Start()
soResult, _ := soSession.Snapshot(snapshotDX)
soToken, _ := so.BuildToken(soResult, chatToken, c.deviceID, "chatgpt")
```

**注意**：直接引用需要解决 import 路径问题。当前项目的 module 是 `chatgpt2api`，aurora 是独立 module。需要在 `go.mod` 中添加 replace 指令或调整 import 路径。

---

## 预期结果

改完后：
1. **`so_token_present` 从 `false` 变为 `true`** — 服务端接受 turnstile 和 SO token
2. Turnstile 输出从 ~24 bytes 增长到 ~2000 bytes（对齐浏览器）
3. SO token 正确构造并注入 conversation 请求
4. 对话功能正常，无 403/风控拦截
5. 日志中 `so_events.log` 出现 `so_token_present` 事件（表示服务端接受了我们的 token）
