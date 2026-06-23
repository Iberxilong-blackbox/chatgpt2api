# Sentinel dx VM 验证日志

> Phase 2：浏览器 SDK vs Go VM 逐指令对比，定位 opcode handler 语义差异。
> Phase 1 总结见 [`sentinel-dx-diagnostic-journal`](sentinel-dx-diagnostic-journal.md)

**状态**：🔄 进行中

---

## 背景

### 已知

- XOR 密钥 = `sourceP`（legacy p token，`gAAAAAC...`）
- 完整 opcode 表 0-35（31,32 是 SDK 空白），Go VM 已全部实现
- 指令格式：`[opcode|regKey, ...args]`，At 是单 Map 双用途（opcode handler + 寄存器）
- Go VM 寄存器系统已修复：`map[any]any`，浮点 key 原样保留

### 未知（当前瓶颈）

- opcode handler **语义** 与浏览器 SDK 不完全一致
- 寄存器派发正确、零 unknown opcode，但仍然 3/3 `result EMPTY`
- 具体哪个 opcode 的行为有差异 → 未知

### 目标

通过浏览器端 dump SDK VM 执行时的**寄存器写入序列**（key → value 类型 + 摘要），与 Go VM 的同输入执行序列对比，找到第一个行为分歧点。

---

## 实验记录

### Round 1：浏览器 finalize 流量检查 (2026-06-23) ❌ 路径被否决

**操作**：anything-analyzer session `dx-phase2`，chatgpt.com 发送对话 "h"，捕获 HTTP 流量。

**发现**：
- `POST /sentinel/chat-requirements/prepare` 响应中**没有 `so.collector_dx`**（只有 `persona` + `prepare_token`）
- `POST /sentinel/chat-requirements/finalize` 请求中**没有 `so` 字段**（只有 `prepare_token` + `proofofwork` + `turnstile`）
- 该账号为 chatgpt-freeaccount，此类型请求未触发 dx 挑战

**结论**：**交叉验证路径（浏览器 so vs 我们的 dxToken）走不通。** 不是所有账号/请求都会触发 dx，浏览器端抓不到带 `so` 的 finalize。

---

### Round 2 方案：Node.js 本地运行 SDK VM → 降级为备选

**sdk.js 分析结果**（65KB, 1903 行，高度混淆）：
- VM 核心位于第 508-635 行：`Pt()` 循环 + 全部 opcode handler `At.set(...)` 调用
- 直接 Node.js 运行需要大量 deobfuscation + browser API mock，工作量大
- **降级为备选方案**——保留 sdk.js 源码用于逐 opcode 精确对比

---

### Round 3 方案：Go VM 逐指令 trace 日志（当前方案 ✅）

**思路**：与其花几小时搭 Node.js 环境，不如给 Go VM 加超详细的 trace 日志——打印每条执行的指令、寄存器读写、条件跳转决策。部署后跑一次，trace 直接告诉你执行路径在哪里出了问题。

**优势**：
- 即时可用，不需要任何环境搭建
- 一次性看清整个执行路径
- 定位到具体 opcode 后，再去 sdk.js 源码（已提取）里精确定位差异

**需要加的 trace 信息**：
- 每条指令的 `token[0]`（opcode/regKey）和参数列表
- 每次 `get(key)` 的返回值类型和摘要
- 每次 `set(key, value)` 的 key 和 value 摘要
- 条件跳转（opcode 20/21/23）的判断结果（true/false 及原因）
- 函数调用（opcode 7/17）的 target 名称
- sub-VM（opcode 22）和函数定义（opcode 30）的进入/退出

---

## 交互模式

| 角色 | 职责 |
|------|------|
| **人工** | 部署新版本到服务器，触发请求，复制 trace 日志给 Claude |
| **人工** | 在 DevTools Console 运行 JS 验证特定 API 的浏览器返回值（按需） |
| **Claude** | 分析 trace 日志，定位执行路径分歧点 |
| **Claude** | 对照 sdk.js 源码修复 Go VM opcode handler |
| **人工** | 在 anything-analyzer 中提供 sdk.js 源码、运行 CDP 查询（按需） |

### 工作循环

```
Go VM 加 trace → 部署 → 跑一次 → Claude 分析 trace → 定位差异 → 修 VM → 再部署 → 验证
```

---

### Round 4：Go VM trace 日志分析 (2026-06-23) 🔬

**操作**：部署 trace 版 Go VM 到服务器，触发一次对话请求，获取完整 265 条指令执行 trace。

**trace 日志**：`server-log.md`（541 行，完整覆盖所有 265 条指令）

#### 正常部分 ✅

- XOR 解密成功，265 条指令全部 JSON parse 通过
- 0 个 unknown opcode — 所有指令都匹配到了 handler
- 条件跳转（op20/21/23）：trace 显示决策路径正常
- 子 VM（op22/op30）：函数定义和嵌套执行正常
- `window.performance.now`（op17）：正确返回模拟时间戳
- `window.Object.create`（op17）：正确创建 `orderedMap`
- 寄存器读写、base64 编解码（op18/op19）、算术运算均正常

#### 发现 —— 两个关键 Bug

##### Bug 1: `window.Reflect.set` 目标对象类型不匹配（opcode 7）

**位置**: `sentinel_dx.go` 第 197-202 行

```go
if target == "window.Reflect.set" && len(values) >= 3 {
    if obj, ok := values[0].(*turnstileOrderedMap); ok {  // ← BUG: 永不匹配
        obj.add(turnstileToString(values[1]), values[2])
    }
    return
}
```

**根因**: `values[0]` 是 register 10 = 字符串 `"window"`，不是 `*turnstileOrderedMap`。`ok` 恒为 `false`，`obj.add()` 永不执行。

**后果**: VM 中 ~36 次 `window.Reflect.set(window, "__oai_so_*", value)` 调用全部是空操作。浏览器 so 对象的 30+ 个属性一个都没被写入。后续的计算链（op30 定义的加密函数）读不到中间值，全部拿到 nil。

**Trace 证据**:
- 指令 229: `op7 call target=str("window.Reflect.set")` — 无寄存器写入
- 指令 230-264: 同样模式，所有 Reflect.set 调用都无副作用
- 指令 238: `get 28.69 → nil (UNINITIALIZED)` — 本该被 Reflect.set 写入的值仍是 nil

##### Bug 2: `window.Date.now` 未实现（opcode 17）

**位置**: `sentinel_dx.go` opcode 17 switch 语句（第 286-308 行）

已实现的 browser API: `performance.now`, `Object.create`, `Object.keys`, `Math.random`。**缺 `Date.now`**。

**后果**: 指令 228 调用 `window.Date.now()` 落入 `default` 分支 → `call("window.Date.now")` 因目标非 `turnstileFunc` 而静默失败 → register 15.46 保持 nil。

**Trace 证据**:
- 指令 228: `dispatch key=77.59 args=[15.46 17.24]` → 无后续 set 日志
- 指令 239: `get 15.46 → nil (UNINITIALIZED)` — Date.now 结果未被写入

#### 关键寄存器追踪

| 寄存器 | 初始值 | 最终状态 | 根因 |
|--------|--------|----------|------|
| **55.31** | [33] `set = nil` | nil | 从未被写入；最终 opcode 3 读到 nil → `turnstileToString(nil)` = `"undefined"` |
| **15.46** | [228] Date.now 调用 | nil | Bug 2：Date.now 未实现，结果未写入 |
| **28.69** | [159] `set = nil` | nil | Bug 1：Reflect.set 空操作，值丢失 |
| **29.2** | [141] `set = nil` | nil | Bug 1：同上 |

#### 最终结果

指令 [265] `opcode 3` 读 register 55.31 → nil → `turnstileToString(nil)` = `"undefined"` → base64 → `dW5kZWZpbmVk`

```
turnstileToString(nil) → case nil: return "undefined"
base64("undefined") = "dW5kZWZpbmVk"
```

✅ **trace 日志方案验证成功**：从 265 条指令中精确锁定了 2 个具体 bug，不需要 Node.js 本地 SDK VM。

---

### 下一步修复方向

1. **模拟 `window` 对象**：创建一个 `*turnstileOrderedMap` 作为 simulated global window。当 opcode 7 遇到 `window.Reflect.set` 且 target 对象是 `"window"`（register 10）时，写入 simulated window 而非检查类型。
2. **补充 `window.Date.now`**：在 opcode 17 switch 中增加 `"window.Date.now"` case，返回当前 Unix 毫秒时间戳。
3. **审计其他浏览器 API**：检查 trace 中出现的所有 op7 call target 是否都已有 handler，确保无遗漏。

---

## 当前状态

| 项目 | 状态 | 说明 |
|------|:---:|------|
| XOR 密钥 | ✅ | sourceP |
| Opcode 表 0-35 | ✅ | 全部实现 |
| 寄存器系统 | ✅ | `map[any]any` |
| 浏览器交叉验证 | ❌ | 否决 — 该账号 prepare 不下发 collector_dx |
| Node.js 本地 SDK VM | 💤 | 降级为备选，sdk.js 源码已提取用于逐 opcode 对比 |
| Go VM trace 日志 | ✅ | Round 4 分析完成，定位到 2 个具体 bug |
| **Bug 1: Reflect.set 类型不匹配** | 🐛 | opcode 7 — `values[0]` 是 string 非 orderedMap |
| **Bug 2: Date.now 未实现** | 🐛 | opcode 17 — switch 缺 case |
| **Window 对象模拟** | 🔜 | 修复 Bug 1 的前提 |
| Opcode handler 语义修复 | 🔄 | Round 5 修复中 |
