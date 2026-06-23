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

## 当前状态

| 项目 | 状态 | 说明 |
|------|:---:|------|
| XOR 密钥 | ✅ | sourceP |
| Opcode 表 0-35 | ✅ | 全部实现 |
| 寄存器系统 | ✅ | `map[any]any` |
| 浏览器交叉验证 | ❌ | 否决 — 该账号 prepare 不下发 collector_dx |
| Node.js 本地 SDK VM | 💤 | 降级为备选，sdk.js 源码已提取用于逐 opcode 对比 |
| **Go VM trace 日志** | 🔄 | 当前方案 |
| Opcode handler 语义修复 | ⏳ | 待 trace 定位后修复
