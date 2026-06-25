# Sentinel SO VM — 诊断与修复记录

> **日期**：2026-06-25
> **状态**：进行中 — snapshot result 仅 8 bytes，`so_token_present=false`
> **参考**：[[aurora-sentinel-solution]] — SO VM 两阶段 (collector + snapshot) 设计

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

### 诊断方向

已添加诊断日志到 `sentinel_dx.go`，base64 解码 snapshot result 查看原始内容。预期下次部署后能看到 snapshot 产出的真实值（例如 "null"、"error" 或实际数据片段）。

---

## 尝试过的修复

| # | 改动 | 预期效果 | 实际结果 |
|---|------|---------|---------|
| 1 | `so.go:155` — collector 模式才清空 regs | snapshot 读到 collector 数据 | ❌ 仍为 8 bytes |
| 2 | 添加 base64 解码诊断日志 | 看到 snapshot 原始输出 | ⏳ 待部署 |

---

## 下一步

1. **部署诊断日志版本**，从 `sentinel_dx: SO snapshot decoded` 日志确认 snapshot 实际产出内容
2. 如果产出是 `"null"` → 说明 VM 成功回调被调用但参数为 nil → 追踪是哪个寄存器未被 collector 填充
3. 如果产出是错误信息 → 说明 VM 执行过程中出错
4. 考虑在 so.go 中添加 VM 指令级 trace 日志（受 feature flag 控制）
5. 对比 aurora `internal/so/so.go` 原版与本项目版本的差异（排除复制过程中的遗漏）

---

## 相关文件

| 文件 | 作用 |
|------|------|
| `internal/so/so.go` | SO VM 核心（collector/snapshot 两阶段） |
| `internal/backend/sentinel_dx.go` | SO 桥接（sosession + buildSOToken） |
| `internal/backend/backend.go` | 请求编排（调用 SO） |
| `docs/plan/aurora-code-improvement-plan.md` | 整体改进计划 |
| `docs/riskM/aurora-sentinel-solution.md` | Aurora 标准答案 |
