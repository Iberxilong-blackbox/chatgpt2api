# Sentinel dx VM 实现计划

> 基于 `docs/riskM/dx-Pow.md` 逆向分析，实现 Sentinel dx VM 以处理 `so.collector_dx` 挑战。
> 计划日期：2026-06-20

## 背景

OpenAI 的 ChatGPT SentinelSDK 包含两套 VM 机制：

| | Turnstile VM（已有） | Sentinel dx VM（缺失） |
|---|---|---|
| XOR 密钥 | legacy p token (`gAAAAAC...`) | PoW proof token (`gAAAAAB...`) |
| 指令来源 | `turnstile.dx` | `so.collector_dx` |
| 触发 | prepare 响应 | prepare 响应（但延迟到 PoW 解完后处理） |
| 目的 | 收集浏览器指纹生成 token | 深层环境检测（自动化工具识别） |
| 结果用途 | `"turnstile"` 字段发往 finalize | `"so"` 字段发往 finalize |

根据 `dx-Pow.md`，dx 的解密密钥与 PoW Token 强绑定——SDK 在 PoW 计算完成后将 proof token 存入 WeakMap，后续 `Pn` 函数从中取出作为 XOR 密钥解密 dx。只有真正完成算力证明的客户端才能解开 dx 挑战。

**当前 gap**：
- prepare 响应中的 `so.collector_dx` **被完全忽略**
- finalize 请求中**没有 `so` 字段**
- 多账号运行对话和生图时，OpenAI 可能通过此缺口检测异常

## 改动总览

| 文件 | 改动 | 说明 | 状态 |
|------|------|------|------|
| `internal/backend/sentinel_dx.go` | **新建** | Sentinel dx VM 实现，约 220 行（实际 281 行） | ✅ 完成 |
| `internal/backend/pow.go` | **新增函数** | `rawProofAnswer` — 从 proof token 提取裸 base64，约 10 行 | ✅ 完成 |
| `internal/backend/backend.go` | **修改** | `ChatRequirements` + `buildRequirements` + `getChatRequirements`，约 25 行 | ✅ 完成 |
| `internal/backend/backend_test.go` | **修改** | 4 个新测试（含边界测试），约 45 行 | ✅ 完成（超出计划） |

---

## Step 1: 提取裸 Proof Answer（`pow.go`） ✅ 完成

**问题**：`buildProofToken` 返回 `"gAAAAAB" + answer + "~S"`，dx 解密需要中间的裸 base64 `answer`。

**改动**：在 `pow.go` 新增辅助函数：

```go
func rawProofAnswer(proofToken string) string {
    const prefix = "gAAAAAB"
    const suffix = "~S"
    t := strings.TrimSpace(proofToken)
    if !strings.HasPrefix(t, prefix) || !strings.HasSuffix(t, suffix) {
        return t
    }
    return t[len(prefix) : len(t)-len(suffix)]
}
```

---

## Step 2: 实现 Sentinel dx VM（新建 `sentinel_dx.go`） ✅ 完成

**架构**：与 Turnstile VM 同模式。复用同包（`backend`）的：
- `xorTurnstileString` — XOR 解密函数
- `turnstileKey` — 寄存器键值转换
- `turnstileToString` — JS 类型转字符串
- `turnstileFunc` — opcode 函数类型
- `turnstileOrderedMap` — 有序 map 类型

**核心函数**：

```go
func solveSentinelDxToken(dx, proofKey string) string
```

**处理管道**：

```
base64_decode(dx)
    → xor_decrypt(decoded, proofKey)
    → json_parse → 指令数组 [][]any
    → VM 执行（寄存器 map + opcode dispatch）
    → base64_encode(result)
    → 返回
```

**与 Turnstile VM 的差异**：

| 项 | Turnstile VM | Sentinel dx VM |
|----|-------------|---------------|
| XOR 密钥 | `process[16] = p` (legacy token) | `process[16] = proofKey` (裸 PoW answer) |
| 指令来源 | prepare 响应 `turnstile.dx` | prepare 响应 `so.collector_dx` |
| 结果用途 | `"turnstile"` 字段发往 finalize | `"so"` 字段发往 finalize |

**初始 opcode 表**：复用 Turnstile VM 的完整 opcode 1-24。两者共用 SentinelSDK 指令集，已知 opcode 6（属性读取）和 opcode 3（Resolve 返回）在两套 VM 中语义相同。

**未知 opcode 处理**：遇到未定义 opcode 时 log 输出 opcode 号和完整 instruction，不 panic，继续执行后续指令。

**空 key 防御**：`proofKey` 为空时直接返回空字符串。

---

## Step 3: 修改 `buildRequirements`（`backend.go`） ✅ 完成

**函数签名扩展**：

```go
// 旧
func (c *Client) buildRequirements(data map[string]any, sourceP string) (proofToken, turnstileToken string, err error)

// 新
func (c *Client) buildRequirements(data map[string]any, sourceP string) (proofToken, turnstileToken, dxToken string, err error)
```

**插入位置**：在 Turnstile 处理块之后、函数 return 之前：

```go
// 处理 so.collector_dx
dxToken = ""
so := util.StringMap(data["so"])
if util.ToBool(so["required"]) && util.Clean(so["collector_dx"]) != "" {
    rawKey := rawProofAnswer(proofToken)
    if rawKey != "" {
        dxToken = solveSentinelDxToken(util.Clean(so["collector_dx"]), rawKey)
    }
}
```

**顺序保证**：dx 处理在 PoW 解完之后（`proofToken` 已有值），与 dx-Pow.md 描述的 "PoW Token → WeakMap → dx 解密" 时序一致。

---

## Step 4: 修改 `getChatRequirements` 加入 finalize（`backend.go`） ✅ 完成

**更新调用**：

```go
proofToken, turnstileToken, dxToken, err := c.buildRequirements(preparePayload, p)
```

**finalize payload 增加 `so` 字段**：

```go
finalizePayload := map[string]any{
    "prepare_token": prepareToken,
    "proofofwork":   proofToken,
    "turnstile":     turnstileToken,
}
if dxToken != "" {
    finalizePayload["so"] = dxToken
}
```

---

## Step 5: 扩展 `ChatRequirements` 结构体（`backend.go`） ✅ 完成

```go
type ChatRequirements struct {
    Token          string
    ProofToken     string
    TurnstileToken string
    SOToken        string
    DxToken        string         // NEW: Sentinel dx VM 结果（客户端→服务端）
    Raw            map[string]any
}
```

`DxToken`（客户端计算、发往服务端）与 `SOToken`（服务端在 finalize 响应中返回）是不同值，分开存储。

---

## 覆盖范围

三个入口函数都经过同一个 `getChatRequirements`：

| 入口 | 文件:行 | 场景 |
|------|---------|------|
| `StreamConversation` | `backend.go:148` | 文本对话 |
| `StreamMultimodalConversation` | `backend.go:765` | 视觉对话 |
| `streamOfficialResponsesImage` | `responses_image.go:184` | 生图 |

改 `getChatRequirements` 一处，三个入口全部覆盖。

---

## 边界情况

| 场景 | 处理 |
|------|------|
| PoW 非 required 但 so required | `rawProofAnswer` 返回空，跳过 dx 处理 |
| `so.collector_dx` 为空字符串 | `util.Clean` 后为空，不触发 |
| dx 解密或 JSON 解析失败 | `solveSentinelDxToken` 返回 `""`，不阻断主流程 |
| 遇到未知 opcode | log 输出 opcode 号和指令内容，继续执行不 panic |
| proofToken 格式异常（无前缀/后缀） | `rawProofAnswer` 兜底：返回原始字符串 |
| finalize 被服务端拒绝 | 已有 `upstreamHTTPError` 错误路径覆盖 |

---

## 数据流全貌

```
Bootstrap (解析 HTML)
    ↓
buildLegacyRequirementsToken() → "gAAAAAC..." (legacy p token)
    ↓
POST /prepare {"p": "gAAAAAC..."}
    ↓
prepare 响应:
    ├── prepare_token
    ├── proofofwork: {seed, difficulty}
    ├── turnstile: {required, dx}      → solveTurnstileToken(dx, p) → turnstileToken
    └── so: {required, collector_dx}   → solveSentinelDxToken(dx, rawProofAnswer) → dxToken  ← NEW
    ↓
POST /finalize {
    prepare_token,
    proofofwork: "gAAAAAB...~S",
    turnstile: turnstileToken,
    so: dxToken                        ← NEW
}
    ↓
finalize 响应:
    ├── token → OpenAI-Sentinel-Chat-Requirements-Token
    ├── so_token → OpenAI-Sentinel-SO-Token
    └── ... → ChatRequirements.Raw
```

---

## 不在此次范围 ✅ 已确认跳过

### 1. 注册流程（`internal/service/register.go`）— 已跳过

（以下复用思路保留供后续启用时参考）

注册 sentinel 端点（`sentinel.openai.com/backend-api/sentinel/req`）的 PoW/VM 机制与聊天流程是同一套 SentinelSDK，算法相同。但当前项目基本不使用注册流程，因此本次不改。如果后续需要启用，复用思路如下：

**现状**：`buildSentinelToken`（`register.go:774`）向单一端点 POST，响应中包含 `proofofwork`、可能也包含 `turnstile.dx` 和 `so.collector_dx`。

**已有基础**：
- PoW 已正确实现（`generateToken` 有 `~S` 后缀、真实 elapsed ms、完整 JSON marshal）
- XOR 函数（`xorTurnstileString`）和 VM opcode 定义在 `backend` 包中，需改为可导出或复制

**改动点**（后续启用时）：

```go
// register.go buildSentinelToken 中，第一次 POST 后：
payload := ... // 响应体 map[string]any

// 1. 检查 turnstile（当前 "t" 硬编码为空）
turnstile := util.StringMap(payload["turnstile"])
if util.ToBool(turnstile["required"]) && util.Clean(turnstile["dx"]) != "" {
    tValue = solveTurnstileToken(util.Clean(turnstile["dx"]), pToken)
}

// 2. 检查 so.collector_dx（当前完全缺失）
so := util.StringMap(payload["so"])
var soValue string
if util.ToBool(so["required"]) && util.Clean(so["collector_dx"]) != "" {
    rawKey := rawProofAnswer(pValue) // pValue 是解完 PoW 的 token
    soValue = solveSentinelDxToken(util.Clean(so["collector_dx"]), rawKey)
}

// 3. 第二次 POST 的 payload 增加字段
tokenPayload := map[string]any{
    "p":  pValue,
    "t":  tValue,       // 改为动态值
    "so": soValue,      // 新增
    "c":  challengeToken,
    "id": w.deviceID,
    "flow": flow,
}
```

核心复用：`solveSentinelDxToken`、`rawProofAnswer`、`solveTurnstileToken`。如果这些函数在 `backend` 包中（未导出），需改为大写导出或抽取到共享包。

### 2. finalize 响应中的 dx — 已跳过

如果 finalize 响应出现新的 `dx` 字段，目前通过 `ChatRequirements.Raw` 保留全量数据可供后续分析。

### 3. 抓包验证 — 已跳过

需要从真实浏览器抓取 `so.collector_dx` 样例，交叉验证 VM 执行结果与浏览器一致。

---

## 完成总结

| 类别 | 状态 |
|------|------|
| Step 1: `rawProofAnswer` | ✅ 完成 |
| Step 2: Sentinel dx VM | ✅ 完成（281 行，19 个 opcode 与 Turnstile VM 完全对齐） |
| Step 3: `buildRequirements` 修改 | ✅ 完成 |
| Step 4: `getChatRequirements` finalize | ✅ 完成 |
| Step 5: `ChatRequirements` 结构体 | ✅ 完成 |
| 诊断日志 | ✅ 完成 |
| 编译 + 测试 | ✅ 通过（4 个新测试 + 所有已有测试） |
| 注册流程 | ⏭️ 明确跳过（不在范围） |
| 抓包验证 | ⏳ 待真实流量 |

---

## 设计抉择（已确认）

| # | 抉择点 | 选择 | 理由 |
|---|--------|------|------|
| 1 | VM opcode 表 | **方案 A：照搬 Turnstile 全部 24 个 opcode** | 两套 VM 共用 SentinelSDK 指令集 |
| 2 | 代码组织 | **新建 `sentinel_dx.go`，从 `turnstile.go` 复制结构** | 低风险，不改动已验证的 Turnstile VM |
| 3 | `snapshot_dx` | **暂不处理，等抓到真实包再确认用途** | 当前无信息判断其作用 |
| 4 | 诊断日志 | **在 prepare 响应处理中加入 `so` 字段 dump 日志** | 便于确认 OpenAI 是否下发了 dx 挑战 |

## 诊断日志 ✅ 完成

在 `buildRequirements` 处理 `so` 时加入日志：（已实现，略有简化）

```go
so := util.StringMap(data["so"])
if util.ToBool(so["required"]) {
    log.Printf("sentinel_dx: so.required=true, collector_dx_present=%v, dxToken_produced=%v",
        util.Clean(so["collector_dx"]) != "",
        dxToken != "")
}
```

---

## 验证方法

1. ✅ `go build ./...` — 全项目编译通过
2. ✅ `go test ./internal/backend/...` — 已有测试全绿（含 4 个新测试）
3. ✅ 新增 `TestSolveSentinelDxTokenInterpretsEncodedProgram`：构造最小 dx 程序 `[[3,"hello"]]`，用 proofKey XOR 加密后 → 验证 VM 输出 `base64("hello")`
4. ✅ 新增 `TestRawProofAnswer`：验证 `"gAAAAABabc123~S"` → `"abc123"`，异常输入 → 返回原值
5. ⏳ 线上观察诊断日志，确认 `so.collector_dx` 是否被下发、VM 是否正常执行 — **需真实流量**

**额外完成的测试**（超出计划）：
- `TestSolveSentinelDxTokenEmptyKey` — 空 key 防御测试
- `TestSolveSentinelDxTokenInvalidBase64` — 非法 base64 输入测试

**Opcode 覆盖确认**：Sentinel dx VM 与 Turnstile VM 的 opcode 表完全对齐（1,2,3,5,6,7,8,9,10,14,15,16,17,18,19,20,21,23,24），两套 VM 共用 SentinelSDK 指令集。
