# Sentinel dx VM 验证日志

> Phase 2：浏览器 SDK vs Go VM 逐指令对比，定位 opcode handler 语义差异。
> Phase 1 总结见 [`sentinel-dx-diagnostic-journal`](sentinel-dx-diagnostic-journal.md)

**状态**：⏸️ 暂停 — Round 19 验证了 cryptoWrittenRegs 追踪（正确排除 proofKey），但 XOR 片段过短（< 20 chars）。**Phase 3 启动**：转移到浏览器侧 CDP 逆向，见 [`sentinel-dx-browser-investigation`](sentinel-dx-browser-investigation.md)。

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

### Round 6：部署验证 + 新现象 (2026-06-23) 🔴

**操作**：部署 Round 5 修复到服务器，触发对话请求。

**结果**：dxToken 不再是 `"dW5kZWZpbmVk"`。VM 成功产出结构完整的 JSON 证明。

**日志**（`journalctl`）：

```
sentinel_dx: VM start — 276 instructions, proofKey len=591
sentinel_dx: [276] RESULT set: str("eyJfX29haV9zb19oIjosIl9fb2FpX3NvX2hpIjos"...)
sentinel_dx: dxToken output (len=704): eyJfX29haV9zb19oIjos...
sentinel_dx: so.required=true, collector_dx_present=true, pow_required=true, proofToken_empty=false, dxToken_produced=true, sourceP="gAAAAACWyI0MDAw"...(len=591)
sentinel_dx: finalize response — status=200, token_present=true, so_token_present=false
```

🔴 **`so_token_present=false` — 服务端拒绝了我们的 dxToken 证明。**

**解码 dxToken**（base64 → JSON）：

```json
{
  "__oai_so_h": "",
  "__oai_so_hi": "",
  "__oai_so_hp": "",
  "__oai_so_hw": "",
  "__oai_so_s": 0.11378412743006085,
  "__oai_so_k": 0,
  "__oai_so_kp": 0,
  "__oai_so_we": 0,
  "__oai_so_wb": 0,
  "__oai_so_wl": null,
  "__oai_so_t0": 1782209588664,
  "__oai_so_p": 0,
  "__oai_so_pc": 0,
  "__oai_so_m": null,
  "__oai_so_i": 0,
  "__oai_so_ht": 0,
  "__oai_so_hc": 0,
  "__oai_so_ss": 0,
  "__oai_so_ss2": 0,
  "__oai_so_sn": 0,
  "__oai_so_cs": 0,
  "__oai_so_cs2": 0,
  "__oai_so_cn": 0,
  "__oai_so_st": 0,
  "__oai_so_sw": 0,
  "__oai_so_sp": 0,
  "__oai_so_spt": 0,
  "__oai_so_sx0": 0,
  "__oai_so_sy0": 0,
  "__oai_so_lx": 0,
  "__oai_so_ly": 0
}
```

#### 分析

**好消息**：
- 所有 30 个 `__oai_so_*` 字段全部出现 — simWindow 的 Reflect.set 修复生效
- JSON 结构正确，没有缺失字段
- 仅有的 2 个非零值来源正确：`__oai_so_s`（Math.random 种子）和 `__oai_so_t0`（Date.now 时间戳）

**坏消息**：
- 其余 28 个字段全是 `0`、`""` 或 `null`
- `__oai_so_m`（鼠标事件数组）为 null — 应为真实的鼠标移动/点击记录
- `__oai_so_k`（键盘事件）、`__oai_so_kp`（按键）、`__oai_so_we`（窗口事件）等全部为 0

**根因**：VM 不是数据采集器，而是计算引擎。sentinel SDK 在浏览器中的工作方式是：
1. **浏览器事件监听器**收集真实交互数据（鼠标轨迹、键盘事件、窗口变换等）
2. 数据存入 VM 寄存器作为**输入**
3. **VM 指令**对输入做加密/混淆计算，产出最终证明

我们的 Go VM 正确执行了步骤 3（加密计算），但步骤 2 的输入数据全部是零——因为我们不是在真实浏览器中运行，没有事件监听器来填充这些值。服务端校验时发现交互指标为空，拒绝证明。

**用户影响**：首次因为 dx 挑战导致请求失败（之前是直接拿不到有效 dxToken，现在是拿到了但被拒）。错误表现为 `"模型没有返回文本内容"` — 因为 sentinel 未通过，实际的生图响应从未返回。

---

### Round 7：回顾参考文档 (2026-06-23) 📖

回顾两篇原始参考文档，与当前代码做差异分析。

---

#### 文档 1: `dx-Pow.md` — Sentinel dx + PoW 联动机制

**核心观点回顾**：

1. **dx XOR 密钥与 PoW 强绑定**：SDK 中 PoW 计算结果 (`cachedProof`) 存入 WeakMap，随后 dx 解密时取出作为 XOR 密钥。只有完成算力证明的客户端才能解出后续 VM 挑战。

2. **解密三步**：atob(Base64) → XOR(PoW token 循环密钥) → JSON.parse → VM 指令队列（寄存器 9）

3. **VM 最终目的**："访问 window、navigator 等对象，进行更深层次的环境检测（如检测自动化工具特征），最终将收集到的数据通过 btoa 编码后返回给服务器。"

**与当前代码对比**：

| 步骤 | 文档描述 | 当前代码 | 一致？ |
|------|---------|---------|:---:|
| XOR 密钥 | PoW token (`cachedProof`) | `sourceP` (legacy p token) | ⚠️ 不同，但能解出正确 JSON |
| Base64 解码 | `atob(dx)` | `base64.StdEncoding.DecodeString` | ✅ |
| XOR 算法 | 逐字符 `^`，模长循环 | `xorTurnstileString` | ✅ |
| JSON 解析 | `JSON.parse` → 寄存器 9 | `json.Unmarshal` → `process[9]` | ✅ |
| VM 执行 | 遍历指令队列直到返回 | break on `result != ""` | ✅ |

**关键差异分析**：

文档说 XOR 密钥是 PoW token，但代码用的是 `sourceP`（prepare 响应中的 `p` 字段）。从诊断日志的证据链看：
- 代码注释指出 SDK 时序中 `GNt(r)` (dx 解密) 在 PoW 开始**之前**调用
- 因此解密时 PoW 还未完成，不可能用 proofToken 作为密钥
- 实际效果：用 `sourceP` XOR 解密能产生正确 JSON（Round 6 的 276-277 条指令均合法，0 unknown opcode）→ **密钥正确**

**结论**：文档和代码的差异可能是因为 SDK 版本不同或分析角度不同。XOR 解密结果已验证正确，此步骤无需改动。

---

#### 文档 2: `PoW-618-new.md` — PoW 指纹格式

**核心观点回顾**：

1. **PoW 算法**：FNV-1a 32-bit 哈希（不是 MD5/SHA），碰撞格式 `gAAAAAB` + base64(fp) + `~S`
2. **设备指纹**：固定的 23 项数组，用于 hash 碰撞。下标 3（nonce）和下标 9（elapsed ms）运行时覆盖
3. **从浏览器抓取的示例指纹**（有真实值）：
   - `[0]`: `"3000"` — screen.width+height 字符串
   - `[1]`: 完整 `Date().toString()` 含太平洋时区
   - `[5]`: 完整 `navigator.userAgent`
   - `[14]`: `6574898.6` — performance.now() 毫秒
   - `[18]`: `1781862547037.9` — timeOrigin

**与当前代码对比**：

当前 `pow.go` 的 `buildPOWConfig` 已经完整实现了 23 项指纹，且值均来自真实环境数据或合理随机范围：

| 指纹下标 | 文档示例值 | 当前代码 | 合理性 |
|---------|-----------|---------|:---:|
| `[0]` screen | `"3000"` | `"3000"/"4000"/"5000"` 随机 | ✅ |
| `[1]` Date | GMT 时区完整串 | 动态生成太平洋时区 | ✅ |
| `[5]` UA | 完整 UA | 来自请求 Header | ✅ |
| `[8]` lang | `"en-US"` | `"en-US"` | ✅ |
| `[14]` perf.now | `6574898.6` | `time.Now().UnixNano()/1e6` | ✅ |
| `[17]` platform | `"Win32"` | `"Win32"` | ✅ |
| `[19-22]` feature检测 | `0,0,0,1` | `0,0,0,1` | ✅ |

**结论**：PoW 指纹实现与文档一致，且已经足够真实。PoW 本身已验证通过（`token_present=true`）。

---

#### 文档给我们的真正启示

两份文档解释了**机制**：
- dx 怎么解密 ✅（已验证正确）
- PoW 怎么计算 ✅（已验证正确）
- VM 是干什么的：**环境检测**（探测自动化工具特征）

但没有解释**服务端如何验证 VM 输出**。这个问题需要我们自己去逆向。

**核心矛盾**：

```
我们的 VM 执行：   正确的算法 + 零值的环境数据 → 被拒
真实浏览器的 VM：  正确的算法 + 真实的浏览器环境数据 → 通过
```

两份文档的原始作者解决了"算法"层面（解密、PoW），但没有触及"数据"层面（VM 输入数据的真实性）。

**新认知**：方向 A 不是抓 HTTP 包，而是 Hook JS 运行时。Round 1 的失败是因为我们 Hook 错了东西 — 应该在 `Reflect.set(target, key, value)` 拦截 `__oai_so_*` 属性写入，而非在网络请求中找 `so`。不过方向 A 的前提仍然是要有一个会触发 dx 的浏览器 session。

---

### Round 8：CDP Hook 浏览器实验 (2026-06-23) ✅ 完成

**目标**：通过 anything-analyzer 的 CDP capabilities 在真实浏览器中 Hook sentinel SDK 的 `Reflect.set` 调用，捕获 `__oai_so_*` 字段在加密**之前**的原始值。

**实验过程**：
1. `Page.addScriptToEvaluateOnNewDocument` 注入 Hook 脚本（identifier: 2）
2. 刷新 chatgpt.com 页面，发送 2 条普通对话
3. sentinel SDK 确认触发：HTTP 抓包到 `prepare → sdk.js → req(dx) → finalize` 完整链路
4. Hook 捕获到 **11084 个 `Reflect.set` 事件，覆盖 36 个 `__oai_so_*` 字段**

**注意**：`finalize_so` 未通过 fetch/XHR hook 捕获到（SDK 可能使用其他传输方式），但 HTTP 抓包确认 finalize 请求中有 `turnstile` 字段（加密后的 dx 证明）。

---

#### 实验结果：浏览器 so_raw 完整数据

在 11084 次 Reflect.set 调用后，`window.__so_dump.so_raw` 的最终状态：

| 字段 | 浏览器值 | 类型 | Go VM 当前值 | 差异类型 |
|------|---------|------|-------------|:---:|
| `__oai_so_m` | **281122.30** | number | `null` | 🔴 缺鼠标时间戳 |
| `__oai_so_ss` | **113393.75** | number | `0` | 🔴 缺滚动时间戳 |
| `__oai_so_ss2` | **497145897.84** | number | `0` | 🔴 缺滚动 Date.now |
| `__oai_so_sn` | **63** | number | `0` | 🔴 缺滚动计数 |
| `__oai_so_cs` | **17533.40** | number | `0` | 🔴 缺点击时间戳 |
| `__oai_so_cs2` | **198020421.14** | number | `0` | 🔴 缺点击 Date.now |
| `__oai_so_cn` | **63** | number | `0` | 🔴 缺点击计数 |
| `__oai_so_st` | **3** | number | `0` | 🔴 缺 scrollTop |
| `__oai_so_sw` | **7** | number | `0` | 🔴 缺 scrollWidth |
| `__oai_so_sp` | **0** | number | `0` | ✅ |
| `__oai_so_spt` | **1** | number | `0` | 🔴 缺 scroll 父级 top |
| `__oai_so_sx0` | **9** | number | `0` | 🔴 缺鼠标起始 x |
| `__oai_so_sy0` | **607** | number | `0` | 🔴 缺鼠标起始 y |
| `__oai_so_lx` | **9** | number | `0` | 🔴 缺最后鼠标 x |
| `__oai_so_ly` | **607** | number | `0` | 🔴 缺最后鼠标 y |
| `__oai_so_i` | **103** | number | `0` | 🔴 缺输入事件计数 |
| `__oai_so_we` | **6** | number | `0` | 🔴 缺窗口事件计数 |
| `__oai_so_wb` | **1** | number | `0` | 🔴 缺 blur 计数 |
| `__oai_so_wl` | **280865.40** | number | `null` | 🔴 缺 window load 时间 |
| `__oai_so_ht` | **null** | null | `0` | 🔴 应为 null |
| `__oai_so_hc` | **null** | null | `0` | 🔴 应为 null |
| `__oai_so_p` | **null** | null | `0` | 🔴 应为 null |
| `__oai_so_pc` | **null** | null | `0` | 🔴 应为 null |
| `__oai_so_fs` | **null** | null | ❌缺失 | 🔴 缺整个字段 |
| `__oai_so_fs2` | **null** | null | ❌缺失 | 🔴 缺整个字段 |
| `__oai_so_fn` | **null** | null | ❌缺失 | 🔴 缺整个字段 |
| `__oai_so_k` | **null** | null | `0` | 🔴 应为 null |
| `__oai_so_bc` | **null** | null | ❌缺失 | 🔴 缺整个字段 |
| `__oai_so_bm` | **null** | null | ❌缺失 | 🔴 缺整个字段 |
| `__oai_so_h` | **null** | null | `""` | 🔴 应为 null |
| `__oai_so_hi` | **null** | null | `""` | 🔴 应为 null |
| `__oai_so_hp` | **null** | null | `""` | 🔴 应为 null |
| `__oai_so_hw` | **null** | null | `""` | 🔴 应为 null |
| `__oai_so_s` | **null** | null | `0.1137...` | 🔴 应为 null！ |
| `__oai_so_kp` | **null** | null | `0` | 🔴 应为 null |
| `__oai_so_t0` | **null** | null | `1782209588664` | 🔴 应为 null！ |

#### 核心发现

##### 发现 1：`__oai_so_m` 不是鼠标事件数组，是 `performance.now()` 时间戳

之前完全理解错了这个字段的含义。`_m` 是鼠标事件发生时的 `performance.now()` 值，是一个单一浮点数（毫秒），不是事件数组。同样的模式适用于 `_ss`（scroll）、`_cs`（click）。

##### 发现 2：每个交互维度有 3 个字段的命名模式

```
_ss  = performance.now() 时间戳（秒，浮点）→ 测量时间差
_ss2 = Date.now() 时间戳（毫秒）         → 提供绝对时间
_sn  = 事件计数                           → 提供事件密度
```

##### 发现 3：`null` vs `0` vs `""` 有严格语义

| 浏览器类型 | Go VM 当前 | 含义 |
|-----------|-----------|------|
| `null` | `0` 或 `""` | 字段存在但无数据/事件未发生 |
| `0` | `0` | 数值为零（如 `_sp` = scroll position 0） |
| 非零浮点 | `0` 或 `null` | 真实交互发生，时间戳/计数被设置 |

服务端校验时会检查 `null` → 表示"该字段类型被 SDK 识别但事件未发生" vs `0` → "事件发生但数值为 0"。混淆这两者会被识别为模拟。

##### 发现 4：缺失 5 个字段

浏览器有但我们 VM 没有的字段：`__oai_so_fs`, `__oai_so_fs2`, `__oai_so_fn`, `__oai_so_bc`, `__oai_so_bm`。全部为 `null`（事件未发生）。这些是 fingerprinting 相关的字段（`_fs` = font size?, `_bc` = battery charging?, `_bm` = battery level?）。

##### 发现 5：`_s` 和 `_t0` 在浏览器端被设为 null

Round 5 我们添加的 `Math.random`（`_s`）和 `Date.now`（`_t0`）是错误的。浏览器 SDK 通过 `Reflect.set` 将它们初始化为 `null`，VM 计算时再根据 null 值做特殊处理（生成真正的随机数和时间戳），或者这些值在 Reflect.set 之后被 VM 的 op30 子函数覆写。

##### 发现 6：finalize 请求中没有独立的 `so` 字段

与 Round 1 结论一致 — finalize 请求体只有 `prepare_token` + `proofofwork` + `turnstile`。`so` 数据嵌入在 `turnstile` 字段中。这意味着 Go 后端需要把 `dxToken` 放入 `OpenAI-Sentinel-SO-Token` header 或 `turnstile` 字段中（取决于前端 sendBeacon vs XHR 的具体方式）。

---

### 方向 A 结论：`Reflect.set` Hook 路径验证成功 ✅

**Hook `Reflect.set` 是研究 sentinel SDK 输入侧的正确方式**。我们拿到了所有 36 个字段的加密前原始值。

但 **Round 8 只覆盖了 SDK 链路的前半段（输入侧）**：事件监听器 → Reflect.set → so 对象。后半段（VM 计算 → JSON.stringify → btoa → finalize 请求）尚未捕获。

**`finalize_so` 未捕获**：我们的 fetch/XHR hook 没能拦截到最终请求中的 so 密文。原因分析见 Round 9。

---

### 方向 B：基于 Hook 数据修复 Go VM simWindow 🔧

基于 Round 8 数据修复 Go VM 的 simWindow 初始化，详见 [`docs/plan/sentinel-dx-simwindow-fix-plan.md`](../../plan/sentinel-dx-simwindow-fix-plan.md)。核心变更：

1. **字段补齐**：添加缺失的 `_fs`, `_fs2`, `_fn`, `_bc`, `_bm` 字段（初始值 `nil`/null）
2. **null 修复**：将 `_h/_hi/_hp/_hw/_s/_t0/_k/_kp/_ht/_hc/_p/_pc` 从 `0`/`""`/随机值改为 `nil`（null）
3. **交互数据合成**：用随机但合理的值填充时间戳和计数字段

---

### Round 9：Hook `btoa` 捕获浏览器 VM 最终输出 (2026-06-23) ❌ 路径关闭

**目标**：拿到浏览器 sentinel SDK 的**完整 VM 输出**（即 `btoa(JSON.stringify(so对象))` 的输入），作为 Go VM 输出的逐字段对照基准。

#### 发现：SDK 主窗口/iframe 分离架构

通过 Round 8 和 Round 9 的逐步排查，确认了 sentinel SDK 在浏览器中的架构是**分离的**：

```
┌─ 主窗口 (chatgpt.com) ──────────────────────────┐
│                                                 │
│  事件监听器（mousemove, scroll, click, ...）      │
│  ↓                                              │
│  Reflect.set(window, "__oai_so_m", 281122.30)   │  ← Round 8 Hook 这里 ✅
│  Reflect.set(window, "__oai_so_ss", 113393.75)  │
│  Reflect.set(window, "__oai_so_k", null)        │
│  ...                                            │
│                                                 │
└─────────────────────┬───────────────────────────┘
                      │ 数据通过 window 对象共享
                      ↓
┌─ iframe (frame.html?sv=20260423af3c) ───────────┐
│                                                 │
│  SentinelSDK.init()                             │
│  ↓                                              │
│  VM 读取主窗口 __oai_so_* → 寄存器               │
│  ↓                                              │
│  VM 执行加密计算（265-276 条指令）                 │
│  ↓                                              │
│  JSON.stringify(so对象)                          │
│  ↓                                              │
│  btoa(json) = dxToken                           │  ← Round 9 需要 Hook 这里！
│  ↓                                              │
│  HTTP POST /finalize  { turnstile: dxToken }     │
│                                                 │
└─────────────────────────────────────────────────┘
```

**证据链**：

| 观察 | 结论 |
|------|------|
| 主窗口 `__so_dump.events` = 11084，iframe `__so_dump.events` = 0 | Reflect.set 发生在主窗口 |
| iframe `__so_dump` 存在（来自 `addScriptToEvaluateOnNewDocument`） | Hook 脚本可注入到 iframe |
| iframe btoa Hook 初始为 `false`（Runtime.evaluate 只注入主窗口） | CDP Runtime.evaluate 需要显式指定 iframe 上下文 |
| HTTP 抓包有 `GET /sentinel/frame.html` + `GET /sentinel/20260423af3c/sdk.js` | SDK 在 iframe 中加载和执行 |

#### Round 9a：CDP 注入 iframe btoa Hook

**操作**：通过 CDP `Runtime.evaluate` 注入 btoa Hook 到 iframe 的 `contentWindow`。

```javascript
const f = document.querySelectorAll('iframe')[1];
const w = f.contentWindow;
const origBtoa = w.btoa;
w.btoa = function(input) {
    const result = origBtoa.call(this, input);
    if (typeof input === 'string' && input.includes('__oai_so_')) {
        w.__so_dump.finalize_so = result;
        w.__so_dump.finalize_so_raw = input;
        console.log('[HOOK-iframe] btoa so — raw len:', input.length);
    }
    return result;
};
```

**结果**：注入成功（返回 `HOOKED_iframe_btoa`）。随后发了一条对话，但 `finalize_so` **仍未捕获**。

**推测原因**：SDK 脚本在 iframe 中已经加载并执行过了。如果 SDK 在初始化时缓存了 `window.btoa` 的引用（`const btoa = window.btoa`），后续调用会通过缓存的引用绕过我们的 Hook。

#### Round 9 结论：手动 Console 注入路径关闭

**根因确认**：SDK 在 iframe 加载时缓存了 `window.btoa` 和 `JSON.stringify` 的原生引用（类似 `const _btoa = window.btoa`），后续 VM 调用通过缓存引用绕过所有手动 Console 注入的 Hook。手动注入"太晚"——页面 JS 已经执行完毕。

**正确方案**：必须通过 CDP `Page.addScriptToEvaluateOnNewDocument` 在页面加载**之前**注入 Hook，使 SDK 缓存的就是被 Hook 过的函数引用。

---

### Round 10：CDP 预注入三 Hook 实验 (2026-06-23) 🔬

**目标**：通过 CDP `Page.addScriptToEvaluateOnNewDocument` 在页面加载前同时注入 Reflect.set + JSON.stringify + btoa 三个 Hook，一次性捕获从输入到输出的完整数据链路。

#### 操作流程

1. 在 dx-phase2 session 中通过 CDP 注入组合 Hook 脚本（identifier: 2），覆盖 `Reflect.set`、`JSON.stringify`、`btoa`
2. 刷新 chatgpt.com 页面使 Hook 在 SDK 加载前生效
3. 在主窗口 Console 验证 3 个 Hook 均已注入：`Reflect.set` ✅、`JSON.stringify` ✅、`btoa` ✅
4. 发送对话，观察结果

#### 结果：VM 崩溃

**浏览器 so_raw 数据（CDP 读取）**：✅ 成功捕获 32 个字段，6072 次 Reflect.set 事件

```json
{
  "__oai_so_m": 118517.89999985695,     // 鼠标 perf.now 时间戳
  "__oai_so_ss": 49107.005517150916,    // 滚动 perf.now
  "__oai_so_ss2": 89839386.116615,      // 滚动 Date.now
  "__oai_so_sn": 78,                    // 滚动次数
  "__oai_so_cs": 22295.099999427795,    // 点击 perf.now
  "__oai_so_cs2": 339845281.2281493,    // 点击 Date.now
  "__oai_so_cn": 78,                    // 点击次数
  "__oai_so_i": 87,                     // 输入事件计数
  "__oai_so_s": 95038.19999980927,      // 随机种子 (perf.now)
  "__oai_so_t0": 1782216677717,         // Date.now 初始时间戳
  "__oai_so_sp": 796.8776293387937,     // scrollTop 位置
  "__oai_so_spt": 7,                    // scroll 父级 top
  "__oai_so_st": 3, "_sw": 8,           // scrollTop/scrollWidth
  "__oai_so_sx0": 620, "_sy0": 751,     // 起始鼠标坐标
  "__oai_so_lx": 9, "_ly": 364,         // 最后鼠标坐标
  "__oai_so_we": 0, "_wb": 0,           // 窗口事件/blur（未发生）
  "__oai_so_k": 0, "_kp": 0,            // 键盘事件（未发生）
  "__oai_so_fs": null, "_fs2": null, "_fn": null,  // 字体检测（未触发）
  "__oai_so_bc": null, "_bm": null,     // 电池（未触发）
  "__oai_so_wl": null,                   // window load 时间（已完成）
  "__oai_so_h": ?, "_hi": ?, "_hp": ?, "_hw": ?  // 未在 JSON 中出现（undefined 被跳过）
}
```

**但 finalize_so_raw 未捕获**：iframe 中 `finalize_so_raw` = `undefined`，JSON.stringify/btoa Hook 在 iframe 中未触发。

**关键发现 —— `turnstile` 字段解码**：

HTTP 抓包（seq 430）捕获到 finalize 请求体：
```json
{
  "prepare_token": "gAAAAABqOnfk...",
  "proofofwork": "gAAAAABWzMwMDAs...",
  "turnstile": "NDA5OiBUeXBlRXJyb3I6IElsbGVnYWwgaW52b2NhdGlvbg=="
}
```

Base64 解码 `turnstile`：
```
409: TypeError: Illegal invocation
```

**结论**：三项 Hook 的组合干扰了 sentinel SDK VM 的执行。VM 在第 409 步（可能是 `btoa` 或 `Reflect.set` 调用时）因为原生函数的 `this` 上下文丢失而抛出 "Illegal invocation"。SDK 将错误信息 base64 编码后作为 `turnstile` 发送。

#### 关键认知：`turnstile` 与 `so` / `__oai_so_*` 的关系

这是我们首次从 HTTP 层面确认 sentinel SDK 的完整数据流：

```
┌─ 主窗口 (chatgpt.com) ────────────────────────────────┐
│                                                        │
│  浏览器事件（鼠标、键盘、滚动、窗口...）                  │
│  ↓                                                     │
│  Reflect.set(window, "__oai_so_m", <perf.now时间戳>)   │
│  Reflect.set(window, "__oai_so_ss", <滚动时间戳>)       │
│  Reflect.set(window, "__oai_so_k", null)               │
│  ... 共 36 个字段                                       │
│                                                        │
│  这些字段统称为 "so 原始数据"（so_raw）                   │
│                                                        │
└───────────────────────┬────────────────────────────────┘
                        │ 通过 window 对象跨 frame 共享
                        ↓
┌─ iframe (frame.html) ──────────────────────────────────┐
│                                                        │
│  SentinelSDK.init()                                    │
│  ↓                                                     │
│  VM 读取主窗口 window.__oai_so_* → 寄存器输入            │
│  ↓                                                     │
│  VM 执行加密计算（265-276 条指令），产出 so 对象          │
│  ↓                                                     │
│  JSON.stringify(so对象) → JSON 字符串                   │
│  ↓                                                     │
│  btoa(JSON字符串) → Base64 密文                         │
│  ↓                                                     │
│  HTTP POST /finalize                                   │
│  Body: { "turnstile": "<Base64密文>" }                  │
│                                                        │
└────────────────────────────────────────────────────────┘
```

**核心关系**：

| 概念 | 位置 | 格式 | 说明 |
|------|------|------|------|
| `__oai_so_*` 字段 | 主窗口 `window` 对象 | 原始 JS 值（number, null, string） | 事件监听器通过 `Reflect.set` 写入的原始交互数据 |
| `so_raw` | 我们的 Hook 捕获 | `{__oai_so_m: 118517.9, ...}` | 加密**前**的明文，即 VM 的输入 |
| `so` 对象 | iframe VM 内部 | 经过 VM 指令计算后的对象 | 与 `so_raw` 字段相同但值被 VM 变换（加密/混淆） |
| `turnstile` | HTTP finalize 请求体 | Base64 字符串 | `btoa(JSON.stringify(so对象))`，即 VM 的**最终输出** |
| Go `dxToken` | 我们的后端 | Base64 字符串 | 我们的 Go VM 产出的等价物，需与浏览器的 `turnstile` 语义一致 |

**之前为什么找不到 `so` 字段**（Round 1）：我们以为 finalize 请求中有一个独立的 `so` 字段，但实际上 so 数据**嵌入在 `turnstile` 字段中**——经过 VM 计算 → JSON.stringify → btoa 三层转换后才发出。不存在明文的 `so` 请求字段。

**本轮 `turnstile` 为什么是错误信息**：我们的 Reflect.set + JSON.stringify + btoa 三 Hook 组合干扰了 VM 的正常执行（原生函数 `this` 绑定被破坏），VM 崩溃于 "Illegal invocation"，SDK 把错误信息 base64 后当作 turnstile 发出。这也反向证明：
- btoa Hook **确实在 iframe 中生效了**（否则 SDK 不会因为 `this` 丢失而崩溃）
- 问题出在 Hook 的 `this` 转发方式上，而非注入时机
- **单独使用 Reflect.set Hook（Round 8）不会导致崩溃**——说明 JSON.stringify 或 btoa Hook 是干扰源

---

### Round 11：纯净 Reflect.set Hook 数据捕获 (2026-06-23) ✅ 完成，无新信息

**目标**：用纯净 Reflect.set-only Hook 捕获新鲜 so_raw，作为 simWindow 数据的对照基准。

**操作**：
1. 新 session `dx-Round11`，无残留 Hook
2. CDP `Page.addScriptToEvaluateOnNewDocument` 注入 **仅 Reflect.set Hook**（identifier: 2）
3. 刷新 chatgpt.com，验证登录态 ✅ + Hook 生效 ✅
4. 发送对话，sentinel SDK 触发

**结果**：✅ 成功捕获 —— **3352 次 Reflect.set 事件，36 个字段**

#### 捕获数据

| 字段 | 浏览器值 | 字段 | 浏览器值 |
|------|---------|------|---------|
| `__oai_so_s` | 35874.90 | `__oai_so_ss` | 88681.15 |
| `__oai_so_t0` | 1782219136586 | `__oai_so_ss2` | 377335128.80 |
| `__oai_so_m` | 50921.60 | `__oai_so_sn` | 77 |
| `__oai_so_wl` | 51049.10 | `__oai_so_cs` | 14793.10 |
| `__oai_so_we` | 21 | `__oai_so_cs2` | 144626602.71 |
| `__oai_so_wb` | 3 | `__oai_so_cn` | 77 |
| `__oai_so_i` | 132 | `__oai_so_st` | 1 |
| `__oai_so_sx0` | 779 | `__oai_so_sw` | 8 |
| `__oai_so_sy0` | 489 | `__oai_so_sp` | 9.95 |
| `__oai_so_lx` | 773 | `__oai_so_spt` | 6 |
| `__oai_so_ly` | 495 | | |

| 零值字段 | `_k`=0, `_kp`=0, `_p`=0, `_pc`=0, `_ht`=0, `_hc`=0 |
| null 字段 | `_fs`=null, `_fs2`=null, `_fn`=null, `_bc`=null, `_bm`=null |
| **undefined 字段** | **`_h`, `_hi`, `_hp`, `_hw` — JSON.stringify 跳过，不出现** |

#### 结论：与 Round 8/10 数据一致，输入侧研究已达信息饱和

这是第三次拿到同样的数据。36 个字段的语义、类型、合理范围已充分了解。**继续在输入侧抓数据不会带来新突破。**

真正的瓶颈在 **VM 执行到输出的黑盒**：
- 浏览器 VM 产出的 JSON key 顺序 —— 未知
- `_h/_hi/_hp/_hw` 在 JSON.stringify 中是**被跳过**（undefined）还是 SDK 另有处理 —— 未知
- Go VM 产出的 JSON 与浏览器 VM 产出是否逐字段一致 —— **从未对比过**

---

### Round 12：`this`-safe JSON.stringify Hook + turnstile 解码 (2026-06-23) 🔴 关键突破

**目标**：用 `.bind(JSON)` 方式注入纯净的 JSON.stringify Hook（不接触 Reflect.set 和 btoa），捕获浏览器 VM 输出的 JSON 明文。

**操作**：
1. 在 `dx-Round11` session 中通过 `Page.addScriptToEvaluateOnNewDocument` 注入 Hook（identifier: 3）
2. Hook 使用 `JSON.stringify.bind(JSON)` 保留原生 this，只钩 JSON.stringify
3. 刷新页面，发送对话

**结果**：
- Reflect.set Hook：✅ 捕获 3215 次事件（正常）
- JSON.stringify Hook：❌ `finalize_json` 未捕获——Hook 在 iframe 中已安装（验证确认），但未触发匹配
- VM 未崩溃（4 次 finalize 请求均成功，turnstile 长度 3264，非错误信息）

#### 关键发现：解码 turnstile → 二进制密文！

从 HTTP 抓包（seq 871）提取 turnstile 字段并 base64 解码：

```json
// 期望（Go VM 产出）：
btoa({"__oai_so_h":null,"__oai_so_hi":null,...})  → "eyJfX29haV9zb19oIjpudWxs..."

// 实际（浏览器产出）：
btoa(<二进制密文>)  → "TRoYBhYCCQwMGnRAd1R6..."
atob("TRoYBhY...") → [77, 26, 24, 6, 22, 2, 9, 12, 12, 26, 116, 64, ...]
                                    ↑ 非可打印字符，不是 JSON 文本
```

**正确的数据流**：

```
修正前：so_raw → VM → so对象 → JSON.stringify → btoa → turnstile
修正后：so_raw → VM → so对象 → JSON.stringify → 🔐 加密 → btoa → turnstile
```

#### 根因定位

**Go VM 的 opcode 3（最终产出）直接做 `btoa(JSON.stringify(so))`，跳过了加密步骤。** 浏览器 SDK 在 JSON.stringify 和 btoa 之间插入了一个加密层（可能是 opcode 1 XOR 操作，使用 proofKey 作为密钥，或者另有专门的加密 opcode）。

这就是 `so_token_present=false` 的根本原因——不是 simWindow 数据不对，不是 key 顺序不对，而是 **整个输出缺少加密层**，服务端解密失败直接拒。

#### 下一步

Go VM trace 重点观察：opcode 3 之前是否有 XOR（opcode 1）或 base64 encode（opcode 19）对最终数据进行加密。浏览器 SDK 一定在 VM 的最后几步执行了加密操作。

---

### Round 13：Go VM 加密层 trace (2026-06-23) ✅ 定位到缺失步骤

**目标**：通过逐指令 trace，找到 opcode 3 之前加密步骤在 VM 中的实际执行路径。

**操作**：
- 在 Go VM 添加循环 trace buffer（50 条指令）+ crypto op（1/3/15/19）专项日志
- 添加 dxToken 自检：产出后判断是 BINARY（加密）还是 PLAIN TEXT（未加密）

**结果**：✅ Trace 准确捕获到最后 50 条指令，定位到关键问题。

#### Trace 关键指令分析

```
[236] op1(XOR): reg[24.7] = XOR(nil, nil) = "\x00\x00\x00\x00\x00"
[237] op19(btoa): reg[24.7] = "AAAAAAA="
[239] op1(XOR): reg[62.96] = XOR(?, ?) = "uwoyr"
[240] op19(btoa): reg[62.96] = "dXdveXI="
...
[259] op1(XOR): reg[11.9] = XOR(reg[11.9], reg[24.7]) = 含 \x00 和 | 的混合串
[260] op19(btoa): reg[11.9] = "AAAAAAAAAHwAAAAAAAAAfAAAAAAAAHwA"
[267] op1(XOR): reg[62.96] = XOR(reg[62.96], reg[11.9]) = "\\mJ@P`|\x13"
[268] op19(btoa): reg[62.96] = "XG1KQFBgfBM="
     ↑ 加密片段完成，但留在 reg[62.96] 中...

[269-273] op52.75 (Reflect.set) ×5 — 继续写入 simWindow
[274] op99.51 dispatch(3, 76.89)    ← op7 间接调用 opcode 3
     ↓
     opcode 3: get(76.89) = simWindow.toJSON() = 明文 JSON
     → btoa(明文JSON) = "eyJfX29haV9zb19oIjpudWxs..."
     → RESULT = 明文 JSON 的 base64
```

**自检结果**：`SELF-CHECK: dxToken decodes to PLAIN TEXT — NOT ENCRYPTED!`

#### 结论

1. VM **确实**计算了加密片段（op1 XOR + op19 btoa 多轮迭代），说明加密逻辑已存在
2. 但加密结果（reg[62.96]="XG1KQFBgfBM="）是**孤儿数据**——从未被 opcode 3 引用
3. opcode 3 最终用的是 `simWindow.toJSON()` 的明文，直接 btoa 输出
4. **缺失步骤**：在 btoa 之前没有对 JSON 字符串做 `XOR(JSON, proofKey)`

浏览器 SDK 的真实数据流应该是：
```
simWindow → JSON.stringify → XOR(JSON字符串, proofKey) → btoa → turnstile (二进制密文)
```

我们的 Go VM 少了一步 XOR。

---

### Round 14：opcode 3 添加 XOR 加密 (2026-06-23) 🔧 格式正确但仍被拒

**目标**：在 opcode 3 的 `btoa` 之前加 `XOR(JSON, proofKey)`，使输出格式与浏览器一致（二进制密文）。

**修改**：`sentinel_dx.go` opcode 3 handler，在 `base64.Encode` 之前插入：
```go
jsonStr := turnstileToString(v)
encrypted := xorTurnstileString(jsonStr, proofKey)
result = base64.StdEncoding.EncodeToString([]byte(encrypted))
```

**结果**（两次请求均一致）：

| 指标 | Round 13 (修改前) | Round 14 (修改后) |
|------|:---:|:---:|
| 输出格式 | `eyJfX29haV9z...` 明文 JSON | `HGMeHi4gKggKJm8...` 二进制密文 |
| SELF-CHECK | PLAIN TEXT | **BINARY — encrypted ✓** |
| `so_token_present` | false | **false** — 仍被拒 |

#### 规模对比暴露问题

| 来源 | atob 后大小 |
|------|------|
| 浏览器 turnstile (Round 12) | **2447 bytes** |
| Go VM dxToken (Round 14) | **794 bytes** |

我们的输出只有浏览器的 **1/3**。简单 `XOR(JSON, proofKey)` 格式对了但内容不够。浏览器 VM 的加密流程显然更复杂——trace 中看到的多轮 XOR+btoa 迭代构建加密片段才是真实路径。

#### 结论

加密方向正确 ✅，但实现方式不对。需要从 sdk.js 源码中精确定位 opcode 3 和加密逻辑的完整实现。

---

### Round 15 方案：逆向 sdk.js 中 opcode 3 和加密逻辑

**目标**：从之前提取的 sdk.js 源码（65KB, 1903 行）中精确定位 opcode 3 handler 的实现，找到完整的加密流程。

**关键搜索点**：
- `At.set(<opcode_3_key>, ...)` — opcode 3 的 handler 注册
- `btoa` / `JSON.stringify` 调用点附近
- XOR 加密相关的字符串/函数

---

### Round 15：逆向 sdk.js opcode 3 真实实现 (2026-06-23) ✅ 关键突破

**目标**：从 sdk.js 源码中精确定位 opcode 3 handler 的真实实现。

**发现**：

#### opcode 3 的真实实现（第 651-653 行）

```js
At[o(16)](H, (t => {
    s(btoa("" + t))   // 就是 btoa(t)，没有 XOR！
}))
```

opcode 3 **只做 btoa**，没有 XOR 加密逻辑。XOR 加密发生在 VM 指令链中，加密后的值已经存在寄存器里，opcode 3 只是读出来 btoa。

#### 完整数据流对比

**浏览器 SDK 真实流程**：
```
simWindow → VM指令链逐XOR构建加密串 → 存入 reg[X] → op7(op3, reg[X]) → op3: btoa(加密串) → turnstile
```

**Go VM 当前流程**：
```
simWindow → VM指令链算XOR片段但孤儿化 → reg[X] = nil → op7(op3, reg[X]) → op3: get(nil) → 回退 simWindow.toJSON()
                                                                              → Round 14: btoa(XOR(JSON, proofKey)) ✓格式 ✗内容
```

#### 结论

- Round 14 的 XOR 改动方向错误 — 不应该在 opcode 3 里加 XOR
- 问题本质：**VM 的 XOR 链加密片段没有传到 opcode 3 读取的寄存器**
- 正确方向：回退 opcode 3 + 追踪寄存器写入，找到 XOR 链断裂点

---

### Round 16：opcode 3 回退 + 寄存器写入追踪 (2026-06-23) 🔧

**目标**：回退 Round 14 XOR 改动，新增寄存器级 trace，定位 XOR 链断裂点。

**修改内容**（`sentinel_dx.go`）：

1. **opcode 3 回退**（行 218-244）：
   - 移除 `xorTurnstileString(jsonStr, proofKey)` XOR 加密
   - 移除 `simWindow.toJSON()` nil 回退
   - 回归纯 `btoa(turnstileToString(v))` — 匹配 sdk.js 真实行为

2. **寄存器写入追踪** `regWriteLog`（行 164-180）：
   - 新增 `regWriteLog map[any][]regWriteEntry` 记录每次 `set()` 调用
   - 每条记录包含指令序号 `instrIdx` 和写入的值
   - 所有寄存器写入（包括 op30 参数绑定、返回值）均经过 `set()` 被追踪

3. **opcode 3 诊断日志**（行 231-243）：
   - 触发时打印 `regKey` 和当前值
   - 转储该寄存器的完整写入历史（谁写的、第几条指令、写了什么值）
   - 如果寄存器从未被写入 → 明确标记 "NEVER written"

4. **预扫描**（行 810-825）：
   - VM 执行前扫描所有指令，找出引用 opcode 3 的位置
   - 区分 DIRECT（`[3, regKey]`）和 INDIRECT（`[op7_key, 3, regKey]`）调用

5. **opcode 30 写入追踪修复**（行 688, 723）：
   - `capturedProcess[reg] = callArgs[i]` → `set(reg, callArgs[i])`
   - `capturedProcess[returnReg] = subResult` → `set(returnReg, subResult)`

**预期 trace 输出回答的问题**：
- opcode 3 读的是哪个寄存器？（如 68.85 / 76.89）
- 该寄存器被谁写过？（指令序号 + 写入值）
- 如果从未被写 → XOR 链的输出去了哪个寄存器？
- XOR 链构建加密片段后是否存入了"错误的"寄存器？

**部署结果**（3 次请求，每次不同寄存器但模式一致）：
- 第 1 次：250 指令，目标 `77.9`，仅 [25] 设 nil，XOR 片段在 `46.54`/`91.84`
- 第 2 次：238 指令，目标 `15.58`，仅 [25] 设 nil，XOR 片段在 `31.76`/`11.28`
- 第 3 次：287 指令，目标 `55.69`，仅 [30] 设 nil，XOR 片段在 `30.08`/`42.21`

**关键发现**：
1. opcode 3 目标寄存器**每次都被初始化为 nil，之后从未被写入**
2. XOR 链产出在**其他**寄存器中（碎片化的 base64 小片段）
3. 4 个 opcode 30 函数被注册为事件回调（`addEventListener`），但**从未被调用**（0 条 `op30-fn CALL` 日志）
4. 寄存器扫描显示 74 个含字符串的寄存器，全部是字段名/事件类型/按键名 — 0 个完整加密结果

**结论**：opcode 3 目标寄存器为 nil 不是"写错寄存器"的 bug，而是**加密逻辑在事件回调函数中，回调不触发 → 加密永不执行**。

---

### Round 17：opcode 30 函数追踪 + 事件回调发现 (2026-06-23) ✅ 关键突破

**目标**：追踪 opcode 30 函数定义和调用，理解 XOR 链为何不完整。

**修改内容**：
1. opcode 30 定义时打印 `destReg`、`returnReg`、bindings 数、body 指令数、第一条指令
2. createdFn 调用时打印 `CALL destReg=%v` 和参数
3. createdFn 返回时打印 `RETURN` 和 returnReg 的值

**部署结果**（287 条指令的请求）：

```
op30 DEFINE — destReg=44.7  returnReg=61.6  bindings=1 body=56  first_inst=[44.51 67.9 19.83]
op30 DEFINE — destReg=14.82 returnReg=65.85 bindings=1 body=113 first_inst=[44.51 24.7 19.83]
op30 DEFINE — destReg=15.77 returnReg=29.49 bindings=1 body=3   first_inst=[5 52.79 19.17]
op30 DEFINE — destReg=68.68 returnReg=79.07 bindings=1 body=14  first_inst=[44.51 75.55 19.83]
```

**但 0 条 `op30-fn CALL` 日志** — 这 4 个函数被传给 `addEventListener` 和 `Reflect.set` 作为回调，但 Go VM 没有事件系统，它们永远不被调用。

**发现这些 op30 函数的用途**：

| destReg | body | 被用作 |
|---------|------|--------|
| `44.7` | 56 指令 | `Reflect.set(window, "__oai_so_h", fn_44.7)` |
| `14.82` | 113 指令 | `addEventListener("pointermove"/"click"/"scroll", fn_14.82)` |
| `15.77` | 3 指令 | `addEventListener("paste", fn_15.77)` |
| `68.68` | 14 指令 | `addEventListener("wheel", fn_68.68)` |

**核心认知**：
- SDK 架构分两层：① **初始化层**（主 VM 指令流）注册事件监听器；② **加密层**（事件回调函数）在用户交互时被触发，执行 XOR 迭代
- 在浏览器中：用户交互 → 事件回调执行 → 每次回调追加 XOR 片段到累加器 → 最终累加器包含完整密文
- 在 Go VM 中：初始化层正常执行，但加密层（113+56+14+3=186 条指令）完全跳过

**XOR 累加器机制**：

观察 XOR+btoa 对的执行模式：
```
reg[30.08] = XOR(30.08, 42.21)  → 累加器 XOR 密钥片段
reg[30.08] = btoa(30.08)        → base64 编码
reg[42.21] = XOR(42.21, 42.21)  → 密钥片段自 XOR 清零（重置）
reg[42.21] = btoa(42.21)        → "AAAAAAA="
```

累加器 `30.08` 在每次迭代中增长，密钥 `42.21` 在每次使用后被重置。但总共只有 8 次 XOR 迭代（初始化层），远不够构建完整密文。

---

### Round 18：XOR 累加器自动捕获 + 多级 fallback (2026-06-23) 🔧

**目标**：不依赖 `lastCryptoDest`（指向最后 XOR 目标，恰好是清零后的 pad），而是扫描所有寄存器找到真正的加密累加器。

**发现**：`lastCryptoDest` 指向 `42.21`（pad 寄存器，最后被自 XOR 清零 = `"AAAAAAA="`），而非累加器 `30.08`。

**累加器 reg 30.08 的增长过程**：
```
"AAAAAAA="  → "AAAAAAAAfAA="  → "eXdvd3d5d29Qd3kL"  → "XW5KQFILUhtSBAFnSgVddA=="  → "YWEbfmB/f2JgWU1lbHR3V2VJY1VddxMI"
 (7 chars)     (12 chars)          (20 chars)               (24 chars)                    (32 chars ≈ 24 bytes 密文)
```

**修改内容**（`sentinel_dx.go` opcode 3 handler）：

3 级 fallback 链：

| 优先级 | 数据源 | 实现 |
|--------|--------|------|
| **Tier 1** | 目标寄存器（`args[0]`） | 指令流预期的寄存器 |
| **Tier 2** | 最长 base64 字符串（≥20 chars） | 扫描 `process` map 中所有 string 值，取最长者（累加器） |
| **Tier 3** | `XOR(simWindow.toJSON(), proofKey)` | 兜底：手动构建 XOR 加密的 JSON |

**预期**：Tier 2 应命中 reg 30.08 的 32 字符累加器输出 `"YWEbfmB/f2JgWU1lbHR3V2VJY1VddxMI"`（~24 bytes 密文）。
虽然远小于浏览器的 2447 bytes，但这是 VM **实际计算出的加密片段**，不是我们手动构造的。

**状态**：🔴 部署完成，Tier 2 误选 proofKey（见 Round 19）

---

### Round 19：Tier 2 fallback 修复 — cryptoWrittenRegs 追踪 (2026-06-23) 🔧

**部署结果**：Tier 2 "最长base64字符串" 策略失败。

**根因**：Tier 2 扫描所有寄存器取最长字符串（≥20 chars），但 `proofKey` 存储在 `reg[16]`（591 chars，fernet token `gAAAAAC...`），远长于 XOR 累加器（~32 chars）。结果 `btoa(proofKey)` → 输出 fernet token 明文 → 服务器拒。

**修复内容**（`sentinel_dx.go`）：

1. **新增 `cryptoWrittenRegs` 追踪**（map[any]bool）：
   - VM 执行循环中，opcode 1（XOR）或 opcode 19（btoa）写入寄存器后，标记该寄存器为"crypto-written"
   - XOR 累加器经过多次 XOR+btoa 迭代，必在集合中
   - proofKey 作为静态输入只被 set 一次，永不被 XOR/btoa 触碰 → 不在集合中

2. **Tier 2 改为两级**：
   - **Tier 2a**：仅扫描 `cryptoWrittenRegs` 中的字符串（≥20 chars），取最长者
   - **Tier 2b**：若 2a 无结果，扫描全部寄存器但排除 fernet tokens（`gAAAAAC` 前缀 + >200 chars）
   - **Tier 3**（不变）：`XOR(simWindow.toJSON(), proofKey)` 兜底

3. **新增诊断日志**：
   - opcode 3 触发时打印 `cryptoWrittenRegs` 的条目列表
   - 明确标注命中的 Tier（2a / 2b / 3）

**预期**：Tier 2a 应命中 XOR 累加器（~32 chars base64），而非 proofKey（591 chars fernet token）。累加器大小仍远小于浏览器 2447 bytes，但它是 VM **实际计算出的加密片段**。

**状态**：⏳ 待部署验证

---

## 当前状态

| 项目 | 状态 | 说明 |
|------|:---:|------|
| XOR 密钥 (sourceP) | ✅ | Round 7 确认：能解出正确 JSON，密钥正确 |
| Opcode 表 0-35 | ✅ | 全部实现，0 unknown opcode |
| 寄存器系统 | ✅ | `map[any]any`，浮点 key 原样保留 |
| PoW 指纹 (23 项) | ✅ | Round 7 确认：与 PoW-618-new.md 一致，值合理 |
| PoW hash 碰撞 | ✅ | `token_present=true`，已验证通过 |
| 浏览器交叉验证 (HTTP) | ❌ | 否决 — 网络请求中不包含独立 so 字段，so 嵌入在 turnstile 中 |
| Node.js 本地 SDK VM | 💤 | 降级为备选，sdk.js 源码已提取 |
| Go VM trace 日志 | ✅ | Round 4 分析完成，定位到 2+1 个 bug |
| **Bug 1: Reflect.set 类型不匹配** | ✅ | Round 5 修复 |
| **Bug 2: Date.now 未实现** | ✅ | Round 5 修复 |
| **Bug 3: opcode 3 间接调用 nil** | ✅ | Round 5 修复 |
| **Window 对象模拟** | ✅ | simWindow + toJSON() 已实现 |
| **dxToken 产出** | ✅ | 不再是 "dW5kZWZpbmVk" |
| **服务端接受证明** | 🔴 | `so_token_present=false` — 证明被拒 |
| **CDP Hook — Reflect.set** | ✅ | Round 8/10/11：三次捕获 36 字段真实浏览器数据 |
| **`__oai_so_*` 字段语义** | ✅ | 已明确：perf.now/Date.now/计数 命名模式，null/0/undefined 语义 |
| **`turnstile` 与 `so` 的关系** | ✅ | Round 12：`turnstile` = `btoa(加密串)`，加密串由 VM 指令链逐 XOR 构建 |
| **opcode 3 真实实现 (sdk.js)** | ✅ | Round 15：`s(btoa("" + t))` — 纯 btoa，无 XOR |
| **opcode 3 回退 + 寄存器追踪** | ✅ | Round 16：回退 Round 14 XOR，新增 regWriteLog、预扫描 |
| **opcode 30 函数追踪** | ✅ | Round 17：发现 4 个 op30 函数是事件回调，从未触发 |
| **XOR 累加器定位** | ✅ | Round 18：reg 30.08 是加密累加器，32 chars ≈ 24 bytes |
| **XOR 累加器自动捕获** | 🔴 | Round 19 部署：Tier 2 "最长字符串"误选 proofKey（591 chars fernet token），已修复为 cryptoWrittenRegs 追踪 |
| **Tier 2 — cryptoWrittenRegs 追踪** | 🔧 | Round 19：新增 XOR/btoa 写入目的地追踪，Tier 2a 仅扫描 crypto-written 寄存器 |
| **Go VM 输出 vs 浏览器输出** | 🔴 | 规模差距 ~100x（24 vs 2447 bytes），根因：事件回调未触发，XOR 迭代不足 |
| **下一步** | ⏳ | 部署 Round 19 → 验证 Tier 2a 命中 XOR 累加器 → 确认不再误选 proofKey

---

## 实施记录：simWindow 数据修复

### Round 1：initSimWindow 预填充（2026-06-23 19:19）🔴 被覆盖

根据 [`sentinel-dx-simwindow-fix-plan.md`](../../plan/sentinel-dx-simwindow-fix-plan.md) 实施：
- 新增 `initSimWindow()` 函数，用合理随机值预填充 36 个字段
- 在 VM 循环前调用 `initSimWindow(simWindow)`

**结果**：`so_token_present=false`，dxToken 长度 832。解码后发现所有交互字段仍为 `0`/`null`——VM Reflect.set 指令覆盖了 init 值。`_h/_hi/_hp/_hw` 为函数值，JSON 序列化为空（无效 JSON `"__oai_so_h":,`）。

### Round 2：Reflect.set 保护 + toJSON 修复（2026-06-23 20:19）🔴 仍未通过

**新增修改**：
1. **Reflect.set guard**（`sentinel_dx.go`）：non-zero init 值不被 0/nil 覆盖；nil init 不被 0 覆盖（保持 null）；nil→非零放行（如 `_s`=Math.random）
2. **toJSON 容错**（`turnstile.go`）：不可序列化类型 → `null`

**结果**：dxToken 长度 1060。解码验证所有字段正确：
- Type A null 字段：全部 `null`（16 个）
- Type B 交互字段：全部非零合理值（`_wl=1869`, `_m=213539`, `_ss=184151`, `_sn=59`, `_cs=36997`, `_cn=83`, `_sx0=655`, `_sy0=988`, `_lx≈651`, `_ly≈944`, `_i=153`, `_we=3`, `_wb=1`...）
- `_s`/`_t0`：VM 正常填充
- `_fs/_fs2/_fn/_bc/_bm`：存在且为 `null`

**但仍然 `so_token_present=false`** — 数据层面已合理，问题不在 simWindow 值本身。详见计划文档中的下一步排查方向。
