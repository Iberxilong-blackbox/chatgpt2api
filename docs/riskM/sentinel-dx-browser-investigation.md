# Sentinel dx 浏览器侧逆向日志

> **Phase 3**：通过 anything-analyzer CDP 在真实浏览器中逆向 sentinel SDK VM 的加密链路。
> Phase 2（Go VM trace + 修 bug）见 [`sentinel-dx-vm-verification`](sentinel-dx-vm-verification.md)

**状态**：🔄 进行中 — Round 1 (方向 A) 完成，进入方向 B1

---

## 背景总结（来自 Phase 1 & 2）

### 架构

```
┌─ 主窗口 (chatgpt.com) ──────────────────────────────┐
│  浏览器事件（鼠标、键盘、滚动、窗口...）               │
│  ↓                                                   │
│  Reflect.set(window, "__oai_so_m", <perf.now时间戳>) │
│  Reflect.set(window, "__oai_so_ss", <滚动时间戳>)     │
│  Reflect.set(window, "__oai_so_k", null)             │
│  ... 共 36 个字段                                    │
└─────────────────────┬────────────────────────────────┘
                      │ 数据通过 window 对象跨 frame 共享
                      ↓
┌─ iframe (frame.html?sv=20260423af3c) ────────────────┐
│  SentinelSDK.init()                                  │
│  ↓                                                   │
│  VM 读取主窗口 window.__oai_so_* → 寄存器输入         │
│  ↓                                                   │
│  VM 执行 ~265 条主指令流（初始化层）                    │
│  ↓                                                   │
│  注册 4 个事件回调（op30 函数，共 186 条指令）          │
│  ↓ 用户交互触发                                       │
│  事件回调执行 XOR 迭代 → 累加器增长                    │
│  ↓                                                   │
│  VM 最终指令（opcode 3）：btoa(累加器)                 │
│  ↓                                                   │
│  HTTP POST /finalize                                 │
│  Body: { "turnstile": "<Base64密文>" }                │
│                                                      │
└──────────────────────────────────────────────────────┘
```

### 已确知

| 项目 | 结论 | 证据 |
|------|------|------|
| VM 输入：36 个 `__oai_so_*` 字段 | 语义、类型、合理范围已明确（Round 8/10/11） | CDP Reflect.set Hook ×3 |
| VM 主指令流结构 | 262-287 条指令，opcode 表 0-35 全覆盖 | Go VM trace ×多次 |
| VM 两层架构 | 初始化层（注册回调 + 2-3 轮 XOR）+ 加密层（4 个 op30 回调，186 条指令从不执行） | Go VM trace Round 17 |
| opcode 3 实现 | `btoa(t)` — 纯 base64，无 XOR（sdk.js 第 651-653 行） | sdk.js 逆向 Round 15 |
| 浏览器 turnstile 特征 | 3264 chars base64 → 2447 bytes 二进制密文（非明文 JSON） | HTTP 抓包 Round 12 |
| Go VM 输出规模 | ~12-32 chars base64 → ~9-24 bytes（vs 浏览器 2447 bytes，差 ~100x） | Go VM trace Round 18-19 |
| Hook btoa/JSON.stringify 会崩溃 VM | `this` 绑定丢失导致 "Illegal invocation"（Round 10） | CDP 三 Hook 实验 |
| 单独的 Reflect.set Hook 安全 | 不会崩溃 VM（Round 8/10/11） | CDP 多次验证 |
| `Page.addScriptToEvaluateOnNewDocument` 可在 SDK 前注入 | both 主窗口 + iframe 生效 | CDP ×多次 |
| `Runtime.evaluate` 手动注入 Hook "太晚" | SDK 已缓存原生函数引用，绕过 Hook（Round 9） | CDP 验证 |

### 当前瓶颈

> **浏览器 produce 2447 bytes 密文，Go VM produce 24 bytes 密文。差距 ~100x。**
>
> ~~根因假设：4 个事件回调（op30 函数，186 条指令）在浏览器中因用户交互被调用了数百次，每次都做 XOR 迭代追加到累加器。Go VM 没有事件系统，回调永远不触发。~~ ❌ 已排除
>
> **Round 1 修正**：turnstile 大小与交互量无关（T2 1641B < T1 2303B < T3 2452B），排除了回调累加器模型。~100x 差距的真正原因是 **Go VM 的 simWindow 数据量远远不够**——浏览器侧采集了大量浏览器指纹数据（cookie、navigator、header 等），不仅仅是 36 个 `__oai_so_*` 字段。
>
> **待验证**：
> 1. ~~回调函数体里到底是不是加密逻辑？~~ → 方向 A 已排除"回调追加"模型，不需要验证
> 2. 浏览器 turnstile 的内部结构是什么？→ **下一步 B1：CDP 断点截获 btoa 输入 T**
> 3. XOR 密钥从哪来？是 session-random 还是派生自 simWindow 字段？

---

## 方向 A：HTTP 抓包 — 多次触发对比 turnstile 增长规律

### 核心问题

> 用户交互量对 turnstile 长度和内容的影响是什么？

### 实验设计

用干净的 anything-analyzer session，**不注入任何 Hook**（避免 VM 崩溃），发送 3 条对话：

| 轮次 | 交互模式 | 目的 |
|------|---------|------|
| **T1：基线** | 页面加载后立刻发送（< 3 秒，最小化鼠标移动） | 最小交互 → 最小 turnstile |
| **T2：中等** | 移动鼠标 ~10 秒后发送 | 中等交互 → 中等 turnstile |
| **T3：大量** | 大量鼠标移动 + 滚动 + 键盘输入后发送 | 最大交互 → 最大 turnstile |

每次捕获：
- HTTP finalize 请求中的 `turnstile` 字段（base64）
- `atob(turnstile)` → 二进制 bytes + 长度
- 首/尾 bytes 的 hex dump（对比结构）

### 可排除的假设

| 实验结果 | 排除什么 |
|----------|---------|
| 3 次 turnstile **长度相同** | 排除"回调追加累加器"模型 — 加密在初始化层一次性完成 |
| **长度递增**但**前缀相同** | 确认追加模型；前缀 = 初始化层产出，后缀 = 回调追加 |
| **长度递增**但内容**完全不同** | 排除追加模型 — 每次整体重算（可能含时间戳） |
| **长度相同**但**内容不同** | 加密结果含每次随机的元素（如 Math.random 推导的密钥） |

### 推断能力

从增长幅度可以推算**每次回调追加的 bytes 数**：
```
单次回调产出 = (T3_len - T1_len) / (T3交互数 - T1交互数)
```
如果这个数匹配 Go VM trace 中单次 XOR+btoa 迭代的产出量 → 确认回调就是 XOR 迭代。

---

## 方向 B：CDP 断点 — 截获 btoa 的输入

### 核心问题

> 浏览器 VM 传给 `btoa()` 的原始字符串 T 是什么？是二进制密文还是 base64 字符串？

### 实验设计

**B1：`Debugger.setBreakpointByUrl` 打在 sdk.js 的 btoa 调用**

根据 sdk.js 逆向（Round 15），opcode 3 实现：
```js
At[o(16)](H, (t => {
    s(btoa("" + t))   // ← 断点打在这里
}))
```

步骤：
1. 在 anything-analyzer 中创建新 session
2. CDP `Page.addScriptToEvaluateOnNewDocument` 注入**仅 Reflect.set Hook**（安全的，已验证）
3. 刷新 chatgpt.com
4. CDP `Debugger.setBreakpointByUrl` 在 iframe 的 sdk.js 中对应的行号处设断点
5. 发送对话 + 交互 → 等待断点触发
6. 断点触发后 `Runtime.evaluate` 读取局部变量 `t`——即 btoa 的输入

一次断点就能回答：
- `t` 的长度（是否为 ~2447 bytes 原始密文？）
- `t` 的内容类型（二进制 string 还是 base64 字符串？）
- `t` 的前缀/后缀是否有固定结构？

### B2（备选）：如果断点定位困难

sdk.js 是混淆后的代码，行号可能不稳定。备选方案：

**CDP `Debugger.setBreakpoint` 打在 `btoa` 自身调用上**（不依赖 sdk.js 行号）：
1. 用 `Runtime.evaluate` 在 iframe context 中获取 `btoa` 函数对象
2. 用 `Debugger.setBreakpoint` 针对该函数对象打断点
3. 如果 SDK 缓存了原生 `btoa` 引用，断点可能绑定到缓存的引用（需要验证）

注意：Round 10 中三 Hook（Reflect.set + JSON.stringify + btoa）导致 VM 崩溃。但 **CDP 断点不是 Hook**——它不修改 `this` 绑定，只是在调用前暂停。应该不会崩溃 VM。

### 可排除的假设

| 实验结果 | 排除什么 |
|----------|---------|
| `t` 是二进制密文（含大量不可打印字符） | 排除 "opcode 3 之前还有 btoa" — 加密输出是原始 XOR 结果 |
| `t` 是 base64 字符串 | 排除 "单次 XOR" — VM 先 XOR 再 btoa 多轮，最终给 opcode 3 的是 base64 |
| `t` 包含 `__oai_so` 或 `window` | 排除"加密是 XOR(JSON, key)" — opcode 3 可能直接读了 simWindow |
| `t` 不包含 `__oai_so` | 操作的数据是加密片段，非原始字段 |

---

## 方向 C（Phase 2 备选）：dump op30 函数指令体

如果方向 A+B 因故无法推进，回到 Go VM 侧：

### C1：静态 dump

在 `opcode 30 DEFINE` 时打印**全部 body 指令**（目前只打印第一条 + body 指令数）。

### C2：模拟回调调用

主指令流结束后，构造 fake event 对象，手动 `call(callback_fn, fakeEvent)` 调用 4 个 op30 函数。观察累加器是否增长。

### 为什么这是备选？

- 每轮需要部署 → 反馈慢
- 只能看到 Go VM 的模拟行为，看不到浏览器 ground truth
- 但如果 A+B 都失败，这是唯一可行路径

---

## 方向 D（存疑）：追踪 opcode 2 和 opcode 15

Go VM trace 中这些 opcode 使用频率较低，但 sdk.js 中它们可能存在。如果 A/B/C 都未能解，排查是否有未被正确实现的 opcode。

---

## 实验记录

### Round 1 — T1/T2/T3 turnstile 对比 ✅ 已完成

**实验执行**：在一个 anything-analyzer session 中发送 3 条对话，捕获 3 次 `/finalize` 请求。

| | T1 (seq 106) | T2 (seq 407) | T3 (seq 517) |
|---|---|---|---|
| **交互** | 最小 (< 3s) | 中等 (~10s) | 大量 (+12min) |
| **Base64 chars** | 3072 | 2188 | 3272 |
| **解码 bytes** | 2303 | **1641** ←最小! | 2452 |

**关键发现**：

1. **❌ 排除了"回调累加器"模型**：T2 (1641B) < T1 (2303B)，更多交互反而产生更小的 turnstile。

2. **3 个 turnstile 完全不同**：0 bytes 公共前缀，0 bytes 公共后缀。每次 finalize 独立生成全新密文。

3. **内部存在 XOR 重复特征**：
   - `75 6F 78 79` ("uoxy") 在 T1 中出现 5 次
   - 大量 8-20 字节序列恰好出现 2 次 → XOR 重复密钥的经典特征
   
4. **字节分布呈双峰，非均匀随机**：高频落在 `0x00-0x1F`（控制字符）和 `0x60-0x7F`（小写 ASCII 区），说明明文中有大量小写字符串（JSON 字段名？），XOR 后保留了结构偏差。

**修正后的模型**：

```
原假设 ❌:
  simWindow → 初始化 XOR (~24B) → 事件回调追加 (~2000B) → turnstile

修正 ✅:
  simWindow (完整快照) → XOR(plaintext, key) → btoa → turnstile
  每次独立生成；大小差异来自采集的浏览器指纹字段数量不同
  ~100x 差距不是因为回调，而是 Go VM 的 simWindow 数据量不够
```

**下一步**：方向 B1 — CDP 断点截获 btoa 输入 T，确认 XOR 前的明文结构和数据来源。

---

## 执行记录

| 方向 | 状态 | 说明 |
|------|:---:|------|
| **A** HTTP 对比 turnstile | ✅ | 3 轮对比完成。排除累加器模型，确认独立快照 + XOR 模型 |
| **B1** CDP 断点 btoa | ⏳ | 下一步优先 — 截获 btoa 输入确认明文结构 |
| **B2** CDP 断点 btoa 函数对象 | ⏳ | B1 失败后 |
| **C** dump op30 + 模拟调用 | 💤 | 回调追加模型已排除，优先级降低 |
| **D** opcode 2/15 排查 | 💤 | C 失败后 |

---

## 参考

- Phase 2: [`sentinel-dx-vm-verification`](sentinel-dx-vm-verification.md) — Go VM trace 分析 + 19 轮改代码记录
- Phase 1: [`sentinel-dx-diagnostic-journal`](sentinel-dx-diagnostic-journal.md) — 初始诊断
- simWindow fix plan: [`../plan/sentinel-dx-simwindow-fix-plan.md`](../plan/sentinel-dx-simwindow-fix-plan.md)
