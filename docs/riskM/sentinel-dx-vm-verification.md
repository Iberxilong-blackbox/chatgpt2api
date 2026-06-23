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

指令 [265] `dispatch key=57.69 args=[3 55.31]` — **非直接调 opcode 3！**

**执行路径追踪**：
```
[265] dispatch key=57.69 args=[3 55.31]
      ↓ reg 57.69 = opcode 7 handler (由 [216] set 57.69=fn 设定)
      ↓ opcode 7: target = get(3) = opcode 3 handler (fn)
      ↓           values = [get(55.31)] = [nil]
      ↓           → log "op7 call target=fn"
      ↓           → call(opcode_3_handler, [nil])
      ↓ opcode 3: args = [nil  (← 已被 opcode 7 解析为 nil，不是 55.31！)]
      ↓           turnstileToString(nil) = "undefined"
      ↓           base64("undefined") = "dW5kZWZpbmVk"
```

⚠️ **Round 4 初版分析错误**：曾认为 opcode 3 直接用 `args[0]=55.31`，实际上是通过 opcode 7 间接调用，`args[0]` 已是解析后的 `nil`。这解释了为什么 `turnstileToString(args[0])`（现行代码）能产出 `"dW5kZWZpbmVd"` —— 不是服务器二进制不一致，而是间接调用路径。

**根因链**：
1. `window.Reflect.set` 全空操作 → simWindow 无数据 → 子 VM 函数无有效输出
2. Register 55.31 在 [33] 设为 nil 后再未被写入
3. [265] 通过 opcode 7 间接调 opcode 3，传入 nil
4. opcode 3 收到 nil 产出 `"undefined"` → 无有效 dxToken

```
turnstileToString(nil) → case nil: return "undefined"
base64("undefined") = "dW5kZWZpbmVk"
```

✅ **trace 日志方案验证成功**：从 265 条指令中精确锁定了 2 个具体 bug + 1 个间接调度路径 bug。

---

### Round 5 修复 (2026-06-23) 🔧

针对 Round 4 发现的 2 个 bug + 间接调用路径，实施以下修改：

#### 修改 1: simWindow + Reflect.set 修复 (`sentinel_dx.go`)

- 新增 `simWindow := &turnstileOrderedMap{}` 全局模拟窗口对象
- opcode 7 的 `window.Reflect.set` 处理器：增加 `values[0] == "window"` 分支，写入 simWindow
- 增加 Reflect.set 写入的 trace 日志

#### 修改 2: Date.now 补全 (`sentinel_dx.go`)

- opcode 17 switch 新增 `"window.Date.now"` case，返回 `time.Now().UnixMilli()`

#### 修改 3: opcode 3 nil 回退 (`sentinel_dx.go`)

- opcode 3 改用 `get(args[0])` 解析寄存器引用
- 当 `v == nil` 且 `args[0]` 是 float64（寄存器 key）或 nil（已被 opcode 7 解析）→ 回退到 `simWindow.toJSON()`
- 当 `v == nil` 且 `args[0]` 是 string → 当作字面量使用（兼容测试用例）

#### 修改 4: toJSON 方法 (`turnstile.go`)

- `turnstileOrderedMap` 新增 `toJSON()` 方法，按插入顺序序列化为 JSON 字符串

---

### 下一步修复方向

1. **部署验证**：部署新代码到服务器，触发对话，检查 dxToken 是否不再是 `"dW5kZWZpbmVk"`
2. **如果 simWindow JSON 不是正确格式**：需要对照 sdk.js 确认 so 对象的序列化方式
3. **如果 dxToken 格式正确但被服务端拒绝**：可能需要精确模拟更多浏览器 API（如 `window.Date.now` 的确定性时间戳）

---

## 当前状态

| 项目 | 状态 | 说明 |
|------|:---:|------|
| XOR 密钥 | ✅ | sourceP |
| Opcode 表 0-35 | ✅ | 全部实现 |
| 寄存器系统 | ✅ | `map[any]any` |
| 浏览器交叉验证 | ❌ | 否决 — 该账号 prepare 不下发 collector_dx |
| Node.js 本地 SDK VM | 💤 | 降级为备选，sdk.js 源码已提取用于逐 opcode 对比 |
| Go VM trace 日志 | ✅ | Round 4 分析完成，定位到 2+1 个 bug |
| **Bug 1: Reflect.set 类型不匹配** | ✅ | Round 5 修复 — opcode 7 支持 string "window" target |
| **Bug 2: Date.now 未实现** | ✅ | Round 5 修复 — opcode 17 新增 case |
| **Bug 3: opcode 3 间接调用 nil** | ✅ | Round 5 修复 — nil 回退到 simWindow.toJSON() |
| **Window 对象模拟** | ✅ | simWindow + toJSON() 已实现 |
| Opcode handler 语义修复 | 🔄 | Round 5 代码完成，待部署验证 |
