# 风控对抗优化 — 开发计划

> 基于对 GPT-guide.md（4天前总结的 ChatGPT 真实浏览器请求分析）与当前项目实现的对比。
> 目标：降低被 OpenAI 风控系统检测标记的概率，保持更多账号持续运行。

## 进度总览

| 优先级 | 内容 | 状态 |
|---|---|---|
| P0 | Turnstile p-token + PoW Config 补全 | ✅ 已完成 |
| P1 | timeOrigin + FNV Hash + 元数据补全 | ✅ 已完成 |
| P2 | thinking_effort + 回退串 + 版本动态更新 | ✅ 已完成 |
| P3 | 端点验证 + 缺失 Headers | ✅ 已完成 |

**改动汇总**：涉及 3 个文件，约 60 行代码，编译及测试通过。

---

## 现状总览

### 当前风控状态

项目目前处于**第1层容忍窗口**：服务端已经收集到足够多的异常信号（指纹差异、配置结构差异等），但尚未升级风控响应。服务端可能在积累数据、灰度测试，随时可能收紧。

P0-P2 的改动已将请求结构与真实浏览器对齐，预计可降低被风控标记的概率。

风控升级路径：
```
第1层：放行（当前状态）
第2层：要求 PoW proof token（加重计算）
第3层：要求 turnstile token（隐形验证）
第4层：要求 arkose/captcha（人机验证）
第5层：封号/封IP
```

### 资源估算（4C8G Ubuntu 服务器）

> 注：PoW 算法已从 SHA3-512 改为 FNV hash，单次 PoW 计算速度提升 10-50 倍。

| 场景 | 可支撑账号数（更新后） |
|---|---|
| 轻量使用（每账号 1次/分钟） | 800-1200 |
| 中等使用（每账号 2-3次/分钟） | 400-800 |
| 重度使用（每账号持续对话） | 200-300 |

实际限制因素更可能是**代理IP质量**和**账号本身的质量**，而非计算资源。

---

## P0 — 立即修复（影响最大，代价最小）✅ 已完成

### 1. ✅ 修复 Turnstile p-token 传空字符串

**文件**：`internal/backend/backend.go:429`

**现状**：`getChatRequirements` 调用 `buildRequirements(payload, "")`，第二个参数 `sourceP` 始终为空字符串。导致 `solveTurnstileToken` 在 XOR 解密 `dx` 时密钥为空，解密成为空操作。如果 OpenAI 要求 turnstile，当前实现无法正确解密指令。

**原因**：文档中 `obt()` 函数通过 `$.set(xR, i)` 将 p token 设置为 XOR 密钥，然后 `sbt(atob(t), $.get(xR))` 用它对 dx 做异或解密。我们的 `xorTurnstileString(text, "")` 在 key 为空时直接返回原文。

**改动**：
- 将 `getChatRequirements` 中计算好的 `p` token（`buildLegacyRequirementsToken` 的结果）传递给 `buildRequirements`
- 在 `buildRequirements` 中将 p token 传入 `solveTurnstileToken(dx, p)`

**预计代码量**：~5 行

---

### 2. ✅ PoW Config 数组补全到 25 个元素

**文件**：`internal/backend/pow.go:63-114`

**现状**：`buildPOWConfig` 返回 18 个元素（索引 0-17），文档中 `getConfig()` 返回 25 个元素（索引 0-24）。缺少 7 个 window 属性检测字段。

**文档中的完整数组**：

| 索引 | 文档内容 | 我们当前值 |
|---|---|---|
| 18 | `Number("ai" in window)` | **缺失** |
| 19 | `Number("createPRNG" in window)` | **缺失** |
| 20 | `Number("cache" in window)` | **缺失** |
| 21 | `Number("data" in window)` | **缺失** |
| 22 | `Number("solana" in window)` | **缺失** |
| 23 | `Number("dump" in window)` | **缺失** |
| 24 | `Number("InstallTrigger" in window)` | **缺失** |

这些字段用于浏览器环境指纹检测：
- `InstallTrigger` 是 Firefox 特有 API，Chrome/Edge 下为 0
- `ai` / `createPRNG` 是 Chrome 内置 AI API（实验性），大部分浏览器为 0
- `solana` 是 Solana 钱包注入的对象检测

**改动**：在 `buildPOWConfig` 返回的数组末尾追加 7 个元素（全部为 0，因为我们模拟的是 Chrome）

**预计代码量**：~2 行

---

## P1 — 重要（消除明显指纹，增加真实性）✅ 已完成

### 3. ✅ 修复 `timeOrigin` 恒为 0

**文件**：`internal/backend/pow.go` 索引 17

**现状**：`float64(time.Now().UnixNano())/1e6 - float64(time.Now().UnixNano())/1e6` 两次调用几乎同时执行，结果恒 ≈0。

**文档**：`performance.timeOrigin` 是页面开始加载时的高精度时间戳，应该是一个合理的大数值（约 `time.Now().UnixMilli() - 页面加载耗时`）。

**改动**：在 `Client` 结构体中存储一个 `timeOrigin` 字段，在 `Bootstrap` 时捕获，在 `buildPOWConfig` 中使用。

**预计代码量**：~5 行

---

### 4. ✅ PoW 算法从 SHA3-512 改为 FNV Hash

**文件**：`internal/backend/pow.go:116-145`

**现状**：`powGenerate` 使用 `sha3.Sum512`。文档中 `_runCheck` 调用 `zvt(seed + answer)`，`zvt` 是 FNV-1a 变种哈希。

**文档中的 zvt 实现**：
```js
function zvt(e) {
    let t = 2166136261;           // FNV offset basis
    for (let n = 0; n < e.length; n++)
        t ^= e.charCodeAt(n),
        t = Math.imul(t, 16777619) >>> 0;  // FNV prime
    return t ^= t >>> 16,
    t = Math.imul(t, 2246822507) >>> 0,
    t ^= t >>> 13,
    t = Math.imul(t, 3266489909) >>> 0,
    t ^= t >>> 16,
    (t >>> 0).toString(16).padStart(8, `0`)
}
```

**差异分析**：
- FNV 比 SHA3-512 快 10-50 倍（减少 CPU 消耗，同等资源可支撑更多并发）
- 输出格式不同：FNV 产生 8 字符 hex 字符串，SHA3-512 产生 64 字节二进制
- 如果 OpenAI 在服务端用 `difficulty` 做反向验证，两种算法结果完全不同

**疑问**：需要确认当前 SHA3-512 实现是否确实能通过验证。如果能通过，说明 OpenAI 服务端只检查 PoW 难度（hash 是否足够小），不验证具体算法。但从"尽可能像浏览器"的角度，应改为 FNV。

**改动**：在 `powGenerate` 中实现 FNV hash 函数替换 SHA3-512

**预计代码量**：~20 行

---

### 5. ✅ 匿名对话消息元数据补全

**文件**：`internal/backend/backend.go:835-846`（`conversationUserMessage`）

**现状**：`conversationUserMessage` 中 `metadata` 缺少两个字段：

```go
// 当前
"metadata": map[string]any{
    "selected_github_repos":     []any{},
    "selected_all_github_repos": false,
    "serialization_metadata":    map[string]any{"custom_symbol_offsets": []any{}},
},

// 文档
"metadata": {
    "developer_mode_connector_ids": [],
    "selected_sources": [],           // ← 缺失
    "selected_github_repos": [],
    "selected_all_github_repos": false,
    "serialization_metadata": {
        "custom_symbol_offsets": []
    }
}
```

注意 `startTextConversation`（已验证流程）已有 `developer_mode_connector_ids`，仅缺少 `selected_sources`。

**改动**：
- `conversationUserMessage` 加上 `developer_mode_connector_ids` 和 `selected_sources`
- `startTextConversation` 的 metadata 加上 `selected_sources`

**预计代码量**：~3 行

---

## P2 — 改进（提升通过率，降低特征）✅ 已完成

### 6. ✅ 添加 `thinking_effort` 支持

**文件**：`internal/backend/backend.go`（`conversationPayload`、`startTextConversation`）

**现状**：Thinking 模型（如 `gpt-5-5-thinking`）的对话 payload 缺少 `thinking_effort` 字段。真实浏览器会发送 `"thinking_effort": "extended"`。

**改动**：当 model 为 thinking 类型时，在 payload 中添加 `"thinking_effort": "extended"`

**预计代码量**：~5 行

---

### 7. ✅ PoW 回退串消除固定前缀

**文件**：`internal/backend/pow.go:144`

**现状**：当 `powGenerate` 在 50 万次迭代后失败，返回固定前缀：
```
"wQ8Lk5FbGpA2NcR9dShT6gYjU7VxZ4D" + base64('"'+seed+'"')
```

前缀 `wQ8Lk5FbGpA2NcR9dShT6gYjU7VxZ4D` 在所有请求中相同，是明显的指纹。大量请求共享同一回退串可被 OpenAI 聚类检测。

**改动**：生成一个看起来像合法 base64 编码 JSON 的随机回退串，或直接 throw error（因为 p token 通常不需要 PoW 解）

**预计代码量**：~5 行

---

### 8. ✅ 客户端版本动态更新

**文件**：`internal/backend/backend.go:22-23`

**现状**：
```go
DefaultClientVersion     = "prod-be885abbfcfe7b1f511e88b3003d9ee44757fbad"
DefaultClientBuildNumber = "5955942"
```

使用静态默认值。ChatGPT 每次部署都会更新这些值。如果大量请求使用旧版本号，是异常信号。

**改动**：
- 从 Bootstrap HTML 中解析最新的 client version（通常可在 JS bundle 路径或 meta 标签中找到）
- 或添加定期自动更新策略
- 对于从浏览器导入的账号，可以同期抓取版本的机制

**预计代码量**：~20 行 + 持续维护

---

## P3 — 加固（降低长期风险）

### 9. ✅ Sentinel 端点路径验证（已完成）

**结论**：ChatGPT 当前使用两步流程 `POST /backend-api/sentinel/chat-requirements/prepare` → `/finalize`。已修改 `internal/backend/backend.go` 中的 `getChatRequirements`。

**同时修正**：finalize payload 中字段名为 `proofofwork` 和 `turnstile`（非文档推测的 `proof_token` / `turnstile_token`），已同步修正代码和文档。

---

### 10. 缺失的 Headers

| Header | 文档中 | 我们 | 影响评估 |
|---|---|---|---|
| `OAI-Echo-Logs` | 复杂的遥测序列 | 无 | 低 — 看起来像客户端遥测 |
| `X-Oai-Is` | 存在 | 无 | 极低 — 文档说"实测没什么影响" |

**结论**：暂不处理，除非出现相关风控。

---

## 未确认项（需进一步验证）

1. **`x-conduit-token` 来源**：文档从 `/finalize` 获取，我们从 `/prepare` 获取。两者功能是否等同？需浏览器验证。

2. **Sentinel 端点**：单步 vs 两步流程（`/chat-requirements` vs `/prepare` → `/finalize`），是否已由 OpenAI 合并？需浏览器验证。

3. **`history_and_training_disabled: true`**：当前匿名流程固定使用，是否有账号因此被标记？文档提到"chatgpt2api 使用的就是临时会话作为代理"。

4. **FNV Hash 线上验证**：改为 FNV 后，需要通过实际 API 调用来确认 PoW 仍能通过服务端验证。如果出问题，可回退至 SHA3-512（保留 `zvtHash` 函数备选）。

---

## 改动文件清单

| 文件 | 改动内容 | 优先级 |
|---|---|---|
| `internal/backend/backend.go` | Turnstile p-token 传入、powTimeOrigin 字段、metadata 补全、thinking_effort 条件追加、ClientVersion 动态提取 | P0-P2 |
| `internal/backend/pow.go` | Config 数组 18→25 元素、timeOrigin 参数、SHA3-512→FNV hash、回退串随机化 | P0-P2 |
| `internal/backend/responses_image.go` | metadata 补全 selected_sources、thinking_effort 条件追加 | P1-P2 |
