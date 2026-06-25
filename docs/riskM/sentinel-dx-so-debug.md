# Sentinel SO VM — 诊断与修复记录

> **日期**：2026-06-25
> **状态**：Fix #7 实施中 — 补齐 36 个 `__oai_so_*` 传感器字段 + `jsSetProp` guard
> **参考**：[[aurora-sentinel-solution]] — SO VM 两阶段 (collector + snapshot) 设计
> **关联**：[[sentinel-dx-simwindow-fix-plan]] — `__oai_so_*` 字段语义和合理值范围

---

## 最新运行日志 (2026-06-25 14:05) — 含指令级 dump

### Snapshot 前 10 条指令 + 逐条解析

```
ins[0] = [8, 41.27, 8]               op8(move):  reg[41.27] = reg[8]          → 41.27 = opcode 8 (move)
ins[1] = [41.27, 61.28, 2]           op8(move):  reg[61.28] = reg[2]          → 61.28 = opcode 2 (assign)
ins[2] = [61.28, 97.56, "Reflect"]   op2(assign): reg[97.56] = "Reflect"      → 97.56 = "Reflect"
ins[3] = [41.27, 98.62, 6]           op8(move):  reg[98.62] = reg[6]          → 98.62 = opcode 6 (getProp)
ins[4] = [98.62, 97.56, 10, 97.56]   op6(getProp): reg[97.56] = window[reg[97.56]]
                                      → reg[97.56] = window["Reflect"]        ← 🔴 nil! (SO mock 无 Reflect)
ins[5] = [41.27, 93.19, 61.28]       op8(move):  reg[93.19] = reg[61.28]     → 93.19 = opcode 2
ins[6] = [61.28, 11.66, 60.09]       op2(assign): reg[11.66] = 60.09
ins[7] = [93.19, 3.37, "set"]        op2(assign): reg[3.37] = "set"
ins[8] = [41.27, 1.91, 98.62]        op8(move):  reg[1.91] = reg[98.62]      → 1.91 = opcode 6
ins[9] = [98.62, 3.37, 97.56, 3.37]  op6(getProp): reg[3.37] = reg[97.56][reg[3.37]]
                                      → reg[3.37] = nil["set"] = nil         ← 🔴 级联塌陷
```

### 级联失败链

```
window["Reflect"] = nil                    ← 根因: SO buildWindow() 缺少 Reflect
  → nil["set"] = nil                       ← ins[9] 覆盖了 ins[7] 写入的 "set"
  → 后续 505 条指令全在 nil 上运算
  → 最终产出 4 byte XOR 碎片 "\\LBX"
```

### 三次运行对比

| 时间 | Snapshot 结果 | 第 1 条指令 |
|------|-------------|------------|
| 11:54 | `ZCB^` | `[8 24.68 8]` |
| 13:57 | `ZMBU` | `[8 35.95 8]` |
| 14:05 | `\LBX` | `[8 41.27 8]` |

每次的寄存器编号都不同（由 collector 动态分配），但**指令模式完全一致**：先 move opcode 引用到高位寄存器，然后 getProp 去 window mock 读属性。

---

## 根因确认

### 🔴 SO 版 `buildWindow()` 缺少的全局对象

| 属性 | Turnstile buildWindow (~450行) | SO buildWindow (~150行) |
|------|-------------------------------|------------------------|
| `Reflect` (含 `Reflect.set`) | ✅ | ❌ |
| `Object.keys` | ✅ | ❌ |
| `Object.create` | ✅ | ❌ |
| `Object.getPrototypeOf` | ✅ | ❌ |
| `Array.from` | ✅ | ❌ |
| `Math.random` | ✅ | ❌ |
| `JSON.parse` | ✅ | ❌ |
| `JSON.stringify` | ✅ | ❌ |
| `navigator` prototype (80+ 属性) | ✅ | ❌ |
| WebGL context | ✅ | ❌ |
| React Router 上下文 | ✅ | ❌ |
| Statsig | ✅ | ❌ |

### 为什么 "ZCB^" 不是 "null"

指令链没有直接产出 nil。VM 内部有多轮 XOR、拼接、条件分支——即便输入是 nil，经过 `jsToString(nil) → "undefined"`、XOR 后截断等处理，最终产物是 4 字节的密文碎片。4 字节 → Latin-1 base64 = 8 字符 → 日志中看到的 `result len=8`。

---

## 修复方向

### 方向 1（根本修复）：补齐 SO 版 `buildWindow()` 缺失的全局对象

从 turnstile 版 `buildWindow()` 移植以下到 SO 版：
1. **JS 全局对象**：`Reflect.set`、`Object.keys/create/getPrototypeOf`、`Array.from`、`Math.random`、`JSON.parse/stringify`
2. **navigator prototype** (`authNavigatorPrototypeKeys` — 80+ 属性)
3. **WebGL context**（如果 snapshot 后续指令会读）
4. **React Router 上下文**（`__reactRouterContext` 等）
5. **Statsig**（`__STATSIG__` 等）

### 方向 2（更快但可能反复）：按需逐步补

每补一个属性 → 部署 → 看下一条失败的指令 → 再补下一个。但指令中有 515 条，可能补 10+ 轮。

### 建议

**先按方向 1 批量补齐**。Turnstile 版的 `buildWindow()` 是经过验证的（产出 1212B token），可以把 SO 版缺失的属性一次性补上，减少反复部署。

---

## 尝试过的修复

| # | 改动 | 预期效果 | 实际结果 |
|---|------|---------|---------|
| 1 | `so.go:155` — collector 模式才清空 regs | snapshot 读到 collector 数据 | ❌ 仍为 8 bytes |
| 2 | 添加 base64 解码诊断日志 (D0) | 看到 snapshot 原始输出 | ✅ 确认为 4 byte 碎片 |
| 3 | 添加 D1/D2/D3 诊断日志 | 看到 collector 寄存器 + 回调参数 | ✅ collector 存标签名, 回调类型 string |
| 4 | 添加 snapshot 前 10 条指令 dump (D4) | 精确定位失败点 | ✅ **根因确认**: `window.Reflect` 返回 nil |
| 5 | 补齐 SO buildWindow 缺失的 JS 全局对象 | Reflect/Object 正常，后续指令仍有缺失 | ❌ 仍为 4 bytes ("_ABT") |
| 6 | 添加 nil-property 追踪 (D5) | 精确定位全部缺失属性 | ✅ **定位 31 个缺失 key** — 全部是 `__oai_so_*` 传感器字段 |
| 7 | 补齐 36 个 `__oai_so_*` 字段 + `jsSetProp` guard | 消灭全部 nil-props，产出完整 SO token | ⏳ 待部署 |

---

## 修复 #5：补齐 SO buildWindow() 缺失的 JS 全局对象 (2026-06-25)

### 部署结果 (14:12)

```
snapshot ins[2] = [64.76 56.87 Reflect]
snapshot ins[4] = [55.8 56.87 10 56.87]    ← window["Reflect"] 现在返回正确对象 ✅
snapshot ins[8] = [55.8 32.22 56.87 32.22]  ← Reflect["set"] 现在也正确 ✅
snapshot ins[9] = [64.76 80.47 Object]      ← 开始读取下一个全局对象
snapshot success callback — arg type=string, toStr="_ABT", len=4  ← 仍为 4 bytes ❌
```

**Reflect 和 Object 都已修复**（指令 0-9 全部正常），但后续 496 条指令仍有别的缺失属性导致级联塌陷。

### 结论

逐个补齐属性的方式（whack-a-mole）效率太低——504 条指令中可能有 10+ 个缺失点。需要一次性定位**全部**缺失属性。

---

## 修复 #6：nil-property 追踪 (2026-06-25)

### 改动

在 `jsGetProp()` 中新增：当 snapshot 模式从 `map[string]any` 查找属性返回 nil 时，记录缺失的 key 和次数。snapshot 成功回调时 dump top-20 缺失属性。

### 预期日志

```
so: snapshot nil-props: 12 unique missing keys:
so: snapshot nil-prop #1: "someProperty" (×45)
so: snapshot nil-prop #2: "anotherProp" (×12)
...
```

这会告诉我们 snapshot 字节码在 window mock 中查找了哪些不存在的属性——一次性定位所有缺失点，然后批量补齐。

### 改动内容

从 turnstile `buildWindow()` 移植以下到 `internal/so/so.go`：

| 新增 | 说明 |
|------|------|
| `Reflect.set` | 属性设置（snapshot ins[4] 最先读取的全局对象） |
| `Object.keys` / `Object.getPrototypeOf` / `Object.create` | 对象遍历和原型读取 |
| `Math.random` / `Math.abs` | 数学函数 |
| `JSON.parse` / `JSON.stringify` | JSON 编解码 |
| `Array.from` | 数组转换 |
| `navigator.__proto__` (52 个属性名) | 浏览器原型链探测 |
| `window["0"]` | 自引用（`window[0] === window`） |

### 新增辅助函数

- `(*soSolver).jsSetProp(obj, prop, value)` — 简化版 Reflect.set
- `soObjectKeys(value)` — 简化版 Object.keys（跳过 `__prototype__` 内部 key）
- `toStrSlice(values)` — `[]any` → `[]string` 转换

---

## 修复 #7：补齐 36 个 `__oai_so_*` 字段 + `jsSetProp` guard (2026-06-25)

### 背景

Fix #6 的 nil-property 追踪揭示了 31 个缺失的 window 属性，几乎全部是 `__oai_so_*` 传感器字段。这些字段在真实浏览器中由 collector 阶段的事件监听器通过 `Reflect.set` 动态写入。Aurora 原始 `so.go` 的 `buildWindow()` 也不包含这些字段——这是 SO VM 的**通用缺陷**，不仅限于本项目。

参考 `sentinel-dx-simwindow-fix-plan.md`（Round 8 CDP Hook 捕获的真实浏览器数据），这些字段分为两类：

### 改动 1：添加 17 个 null 字段到 `buildWindow()`

SDK 注册了事件监听器但这些事件从未触发（如键盘、鼠标、hashchange）。浏览器端这些字段为 `null`。

```go
// so.go buildWindow() — Type A: null fields
nullSOFields := []string{
    "__oai_so_h", "__oai_so_hi", "__oai_so_hp", "__oai_so_hw",  // hash 事件
    "__oai_so_ht", "__oai_so_hc",                                 // hashchange/touch
    "__oai_so_s", "__oai_so_t0",                                  // 随机种子/时间基准 (VM opcode 填充)
    "__oai_so_k", "__oai_so_kp",                                  // 键盘事件
    "__oai_so_p", "__oai_so_pc",                                  // 指针事件
    "__oai_so_fs", "__oai_so_fs2", "__oai_so_fn",                // 字体检测
    "__oai_so_bc", "__oai_so_bm",                                 // 电池状态
}
```

### 改动 2：添加 19 个交互数据字段到 `buildWindow()`

这些字段在浏览器中有真实值（时间戳、计数、坐标），Go VM 需要合成合理值。

| 字段 | 含义 | 合成策略 |
|------|------|---------|
| `__oai_so_wl` | window load perf.now | `500 + rand(0, 2000)` ms |
| `__oai_so_m` | mouse move perf.now | `_wl + rand(5000, 300000)` ms |
| `__oai_so_ss` | scroll perf.now | `_wl + rand(1000, _m-_wl)` ms |
| `__oai_so_ss2` | scroll Date.now | `nowUnixMs - _wl + _ss` |
| `__oai_so_sn` | scroll 事件计数 | `rand(10, 200)` |
| `__oai_so_cs` | click perf.now | `_wl + rand(500, _ss-_wl)` ms |
| `__oai_so_cs2` | click Date.now | `nowUnixMs - _wl + _cs` |
| `__oai_so_cn` | click 事件计数 | `rand(3, 100)` |
| `__oai_so_st` | scrollTop | `rand(0, 1000)` |
| `__oai_so_sw` | scrollWidth | `rand(0, 100)` |
| `__oai_so_sp` | scrollParent top | `0`（浏览器也是 0） |
| `__oai_so_spt` | scrollParent top | `rand(0, 5)` |
| `__oai_so_sx0` | 鼠标起始 x | `rand(0, 1920)` |
| `__oai_so_sy0` | 鼠标起始 y | `rand(0, 1080)` |
| `__oai_so_lx` | 最后鼠标 x | `_sx0 + rand(-100, 100)` |
| `__oai_so_ly` | 最后鼠标 y | `_sy0 + rand(-100, 100)` |
| `__oai_so_i` | 输入事件总数 | `_sn + _cn + rand(5, 50)` |
| `__oai_so_we` | window 事件计数 | `rand(1, 20)` |
| `__oai_so_wb` | blur 事件计数 | `rand(0, 3)` |

**时间戳一致性约束**：`_ss2` 和 `_cs2` 是 `Date.now()` 值，`_ss` 和 `_cs` 是 `performance.now()` 值。两者相差约等于页面加载时的 `Date.now()` 值（`pageLoadDateNow`）。

### 改动 3：添加 3 个缺失的工具对象

| 属性 | 实现 | 说明 |
|------|------|------|
| `removeEventListener` | `vmFunc` no-op | DOM 方法，mock 中无需实际操作 |
| `Date` | `vmFunc` → JS 格式日期字符串 | `"Thu Jun 25 2026 14:17:15 GMT+0000 (Coordinated Universal Time)"` |
| `Math.sqrt` | `vmFunc` → `math.Sqrt(n)` | 加到现有 Math mock 中 |

### 改动 4：`jsSetProp` guard — 保护预填充值不被 VM 覆盖

**核心问题**：VM 在 collector 初始化阶段会通过 `Reflect.set` 将所有 `__oai_so_*` 字段设为 `0` 或 `null`。如果我们的预填充值被覆盖，snapshot 读到的又会是全零。

**解决**：在 `jsSetProp()` 中加 guard——当 VM 试图用 0/nil 覆盖已存在的**非零值**时，拒绝写入。

```go
// so.go jsSetProp() — guard logic
case map[string]any:
    key := toStr(prop)
    if isZeroValue(value) {
        if existing, ok := target[key]; ok && !isZeroValue(existing) {
            return true // reject: keep existing non-zero value
        }
    }
    target[key] = value
    return true
```

`isZeroValue()` 定义：`nil`, `float64(0)`, `0` (int), `""` (空字符串), `false` 均为零值。

**设计意图**：
- nil init 值被 VM 写入非零值（如 `_s` = Math.random）→ **放行**（VM 计算的值是正确的）
- 非零 init 值被 VM 写入 0/nil → **拒绝**（保留我们的合成值）
- nil init 值被 VM 写入 0/nil → **放行**（本来就是 nil）

### 与旧 turnstile 方案的对比

| 维度 | 旧方案 (simwindow fix plan Round 2) | 新方案 (本修复) |
|------|-----------------------------------|----------------|
| 目标文件 | `sentinel_dx.go` turnstile VM | `so.go` SO VM |
| 数据模型 | `turnstileOrderedMap` (有序) | `map[string]any` (无序) |
| 问题 | key 顺序不一致 + `_h` 函数序列化失败 | key 顺序不重要（SO 不走 JSON.stringify） |
| guard 位置 | opcode 7 handler 内联判断 | `jsSetProp` 集中 guard |

### 预期效果

部署后 snapshot 的 nil-props 应从 31 → 0，snapshot 输出从 ~275 bytes 增长到 >= 700 bytes +。最终目标是 OpenAI 服务端接受 SO token（`so_token_present` 在 extra-data header 为 true 时服务端通过校验）。

### 风险

| 风险 | 概率 | 缓解 |
|------|:---:|------|
| 纯随机值被服务端检测异常 | 中 | 值范围基于 Round 8 CDP 真实数据 |
| guard 阻止了 VM 有意义的覆盖 | 低 | guard 只阻止非零→零覆盖，不阻止零→非零 |
| 时间戳不一致被检测 | 低 | `pageLoadDateNow` 统一基准 |

---

## 相关文件

| 文件 | 作用 |
|------|------|
| `internal/so/so.go` | SO VM 核心 + D1/D2/D3/D4 诊断日志 |
| `internal/turnstile/turnstile.go` | Turnstile VM — `buildWindow()` 参考源 (~450 行) |
| `internal/backend/sentinel_dx.go` | SO 桥接（sosession + buildSOToken） |
| `internal/backend/backend.go` | 请求编排（调用 SO） |
| `aurora/internal/so/so.go` | Aurora 原版 SO VM（对比基准，同样缺失属性） |
| `docs/plan/aurora-code-improvement-plan.md` | 整体改进计划 |
| `docs/riskM/aurora-sentinel-solution.md` | Aurora 标准答案 |
