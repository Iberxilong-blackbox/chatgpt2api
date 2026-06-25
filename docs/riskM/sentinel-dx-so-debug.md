# Sentinel SO VM — 诊断与修复记录

> **日期**：2026-06-25
> **状态**：进行中 — snapshot result 仅 8 bytes，`so_token_present=false`
> **参考**：[[aurora-sentinel-solution]] — SO VM 两阶段 (collector + snapshot) 设计

---

## 最新运行日志 (2026-06-25 11:54)

```
2026/06/25 11:53:59 sentinel: /req OK — status=200                          ✅
2026/06/25 11:53:59 sentinel: PoW OK — token len=609                        ✅
2026/06/25 11:53:59 turnstile: SolveDX OK — result len=1212                 ✅ (was 24B, now 1212B)
2026/06/25 11:53:59 sentinel: SO — required=true, collector_dx_present=true, snapshot_dx_present=true
2026/06/25 11:53:59 sentinel_dx: SO collector started — collector_dx len=17140, snapshot_dx len=19196
2026/06/25 11:53:59 sentinel_dx: SO collector finished                     ✅
2026/06/25 11:54:00 sentinel: finalize response — status=200, token_present=true, so_token_present=false  ❌
2026/06/25 11:54:00 sentinel: /ping — status=200                           ✅
2026/06/25 11:54:00 so: snapshot queue len=528, plain_len=14396, first=[8 24.68 8], last=[87.77 3 97.69]
2026/06/25 11:54:00 sentinel_dx: SO snapshot OK — result len=8             ❌ 仅 8 bytes
2026/06/25 11:54:00 sentinel_dx: SO snapshot decoded — raw="ZCB^", hex=5a43425e
2026/06/25 11:54:00 sentinel_dx: SO token built — len=3832
2026/06/25 11:54:01 so: snapshot queue len=528, plain_len=14396, first=[8 24.68 8], last=[87.77 3 97.69]
2026/06/25 11:54:01 sentinel_dx: SO snapshot OK — result len=8
2026/06/25 11:54:01 sentinel_dx: SO snapshot decoded — raw="ZCB^", hex=5a43425e
2026/06/25 11:54:01 sentinel_dx: SO token built — len=3832
```

### 关键观察

1. **Turnstile 大幅改善**：24B → 1212B（aurora VM + buildWindow mock 生效 ✅）
2. **Snapshot 被执行 2 次**（11:54:00 和 11:54:01），每次都产出相同的 "ZCB^"
3. **Snapshot 有 528 条指令**，plain_len=14396（解密成功），说明 XOR key 正确
4. **`so_token_present=false`** — 服务端不接收 SO token

---

## 现象

服务端日志：

```
sentinel: PoW OK — token len=617              ← ✅ 正常
turnstile: SolveDX OK — result len=1232       ← ✅ 正常 (之前 ~24B → 现在 1232B)
SO collector started — collector_dx len=17132 ← ✅ 正常
SO collector finished                          ← ✅ collector 跑完
finalize response — so_token_present=false     ← ❌ 服务端不认
SO snapshot OK — result len=8                  ← ❌ 仅 8 bytes (预期 100+)
SO token built — len=3860                      ← token 封装大 (含 chatToken/deviceID)，但 so 字段仅 8B
```

Turnstile 大幅改善（24B → 1232B），说明 aurora VM + buildWindow mock 生效。但 SO 仍失败。

---

## 根因分析

### 可疑点 1: 寄存器清空 `so.go:155`

**文件**：`internal/so/so.go:155`

```go
func (s *soSolver) run(reqToken, dx string, collector bool) (string, error) {
    s.regs = map[string]any{}  // ← BUG: 每次 run() 都清空寄存器！
```

**分析**：collector 异步跑完 `run(reqToken, collectorDX, true)` 后，`s.regs` 被填满采集数据。但 snapshot 调用 `run(reqToken, snapshotDX, false)` 时，第一行就把 `s.regs` 重置为空 map。snapshot 读不到 collector 存的寄存器值，VM 执行后只能产出 8 bytes（即 "null" → base64 "bnVsbA=="）。

**修复 (2026-06-25)**：
```go
if collector {
    s.regs = map[string]any{}  // 仅 collector 清空，snapshot 复用 regs
}
```

**部署后结果**：snapshot result 仍然是 8 bytes。**此修复未解决问题。**

### 可疑点 2: `initRuntime()` 覆写低编号寄存器

**文件**：`internal/so/so.go:262-545`

snapshot 调用 `run()` 时，虽然不再清空 `s.regs`，但仍会调用 `s.initRuntime()`，它向 regs 0-35 写入 opcode 函数。如果 collector 在低编号寄存器存了数据，就会被覆盖。

但正常设计下，opcode 函数在 0-35，collector 数据在更高编号。且 opcode 函数相同，覆写应为 no-op。

**状态**：不太可能是根因，待验证。

### 可疑点 3: `s.window` 重建

snapshot 调用 `run()` → `s.window = s.buildWindow()` 创建**新的** window map。虽然内容相同（同一 Profile），但 collector 可能将旧 window 子对象的引用存入了某些 regs，snapshot 读到的引用指向旧 window 而非新 window。不过新旧 window 字段值相同，不应对结果产生质变。

**状态**：不太可能是根因。

### 🔴 可疑点 4: Aurora 原版的 SO VM 也存在同样的矛盾 (2026-06-25 新发现)

**对比 `aurora/internal/so/so.go` (原版) 和 `internal/so/so.go` (本项目)**：

| 位置 | Aurora 原版 | 本项目 |
|------|------------|--------|
| `run():155` | `s.regs = map[string]any{}` **无条件清空** | `if collector { s.regs = map[string]any{} }` (仅 collector 清空) |
| 其他所有代码 | — | **完全一致** (diagnostic log 除外) |

**Aurora 文档** (`aurora-sentinel-solution.md:437`) 说：
> "snapshot mode: 设 success/error，**读取 collector 阶段写入的 regs**"

但 **Aurora 代码在 `run()` 中无条件清空 `s.regs`**，这意味着即使 aurora 原版，snapshot 也无法复用 collector 的 regs。文档和代码存在直接矛盾。

**两种可能**：
- **A**: Aurora 的 SO VM 本身就有这个 bug（文档写了理想设计但代码没跟上），aurora 的 SO token 可能也没真正通过
- **B**: Snapshot 实际上不依赖 collector regs，它从 `buildWindow()` 读取数据，但 SO 版 `buildWindow()` (~150 行) 相比 turnstile 版 (~450 行) 缺少关键属性

### 🔴 可疑点 5: SO 版 buildWindow 过于简化

SO VM 的 `buildWindow()` (~150 行) vs Turnstile VM 的 `buildWindow()` (~450 行)：

**SO 版缺少的关键属性**（turnstile 有但 SO 没有）：
- `navigator` prototype 上的 80+ 属性 (`authNavigatorPrototypeKeys`)
- WebGL context (`canvas.getContext("webgl2")`)
- React Router 上下文 (`__reactRouterContext`, `$RB`, `$RV`, `$RC`, `$RT`)
- Statsig (`__STATSIG__`, `__reactRouterVersion`)
- `__REACT_INTL_CONTEXT__`, `DD_RUM`, `__SEGMENT_INSPECTOR__`
- 更多 window 全局属性 (`innerWidth`, `outerWidth`, `screenX`, `screenY` 等)

如果 snapshot_dx 字节码会遍历 navigator prototype 或读取 WebGL 属性，SO 版 mock 会返回 nil。

---

## 新增诊断日志 (2026-06-25)

已添加以下诊断点到 `internal/so/so.go`：

### D1: Collector 指令预览
```go
// 在 run() 中，collector 模式下也输出字节码指令数量和首尾指令
so: collector queue len=%d, plain_len=%d, first=%v, last=%v
```

### D2: Snapshot 成功回调参数类型
```go
// 在 success callback (reg 3) 中，记录传入参数的类型、值和长度
so: snapshot success callback — arg type=%T, toStr=%q, len=%d
```

### D3: Collector 寄存器 dump
```go
// collector 跑完后，dump 所有非 opcode 的寄存器（排除 0-35 的 opcode handler）
// 输出：total_regs、non_opcode 数量、前 30 个非 opcode 寄存器的 key 和 value（截断到 150 字符）
so: collector regs dump — total_regs=%d, non_opcode=%d
so: collector reg [key] = value
```

这些诊断日志帮助回答以下问题：
- **D1**: collector 字节码是否成功解密和执行？（指令数量和首尾指令）
- **D2**: snapshot 成功回调拿到了什么类型的参数？如果是 string，是什么内容？长度多少？
- **D3**: collector 到底在哪些寄存器里存了什么数据？（最关键的线索）

---

## 尝试过的修复

| # | 改动 | 预期效果 | 实际结果 |
|---|------|---------|---------|
| 1 | `so.go:155` — collector 模式才清空 regs | snapshot 读到 collector 数据 | ❌ 仍为 8 bytes |
| 2 | 添加 base64 解码诊断日志 | 看到 snapshot 原始输出 | ✅ 确认为 "ZCB^" (4 bytes) |
| 3 | 添加 D1/D2/D3 诊断日志 | 看到 collector 寄存器内容 + snapshot 回调参数 | ⏳ 待部署 |

---

## 下一步

1. **部署当前版本**，观察新增的诊断日志：
   - `D1`: collector 指令预览 → 确认 collector 字节码正常
   - `D2`: snapshot success callback arg → 确认传入的是什么类型/值
   - `D3`: collector regs dump → **最关键**，确认 collector 到底填充了什么数据
2. 如果 `D3` 显示 collector 根本没填充非 opcode 寄存器 → 说明 collector 字节码执行有问题
3. 如果 `D3` 显示 collector 填充了大量数据但 "ZCB^" 仍然出现 → 说明 snapshot 字节码没有读取这些数据（或者读取了但计算结果仍是 "ZCB^"）
4. 考虑对比 aurora `turnstile.go` 和 `so.go` 的 `buildWindow()` 差异，评估是否需要将 SO 版升级为完整版
5. 如果线索仍然不明确，考虑 dump snapshot 字节码的前 10 条指令（需要修改 log 输出完整指令而非首尾）

---

## 相关文件

| 文件 | 作用 |
|------|------|
| `internal/so/so.go` | SO VM 核心（collector/snapshot 两阶段）+ 新增诊断日志 |
| `internal/backend/sentinel_dx.go` | SO 桥接（sosession + buildSOToken） |
| `internal/backend/backend.go` | 请求编排（调用 SO） |
| `aurora/internal/so/so.go` | Aurora 原版（对比基准） |
| `aurora/internal/chatgpt/request.go` | Aurora 参考请求编排 |
| `docs/plan/aurora-code-improvement-plan.md` | 整体改进计划 |
| `docs/riskM/aurora-sentinel-solution.md` | Aurora 标准答案 |
