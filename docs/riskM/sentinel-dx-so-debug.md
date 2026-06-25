# Sentinel SO VM — 诊断与修复记录

> **日期**：2026-06-25
> **状态**：进行中 — snapshot result 仅 8 bytes，`so_token_present=false`
> **参考**：[[aurora-sentinel-solution]] — SO VM 两阶段 (collector + snapshot) 设计

---

## 最新运行日志 (2026-06-25 13:57)

```
2026/06/25 13:57:47 sentinel: /req returned 401 — continuing to prepare   ← 第一次 401(已自动重试)
2026/06/25 13:57:48 sentinel: /req OK — status=200                        ✅
2026/06/25 13:57:48 sentinel: PoW OK — token len=613                      ✅
2026/06/25 13:57:48 turnstile: SolveDX OK — result len=1212               ✅
2026/06/25 13:57:48 sentinel: SO — required=true, collector_dx_present=true, snapshot_dx_present=true
2026/06/25 13:57:48 sentinel_dx: SO collector started — collector_dx len=17428, snapshot_dx len=18904

── D1: Collector 指令预览 ──
2026/06/25 13:57:48 so: collector queue len=269, plain_len=13071, first=[8 52.36 8], last=[74.35 3 12.94]

── D3: Collector 寄存器 dump ──
2026/06/25 13:57:48 so: collector regs dump — total_regs=165, non_opcode=134
2026/06/25 13:57:48 so: collector reg [n:0.47] = <nil>
2026/06/25 13:57:48 so: collector reg [n:0.91] = 0
2026/06/25 13:57:48 so: collector reg [n:1.35] = __oai_so_fn         ← 属性标签
2026/06/25 13:57:48 so: collector reg [n:10.67] = 1
2026/06/25 13:57:48 so: collector reg [n:10.99] = <nil>
2026/06/25 13:57:48 so: collector reg [n:11.65] = __oai_so_t0        ← 属性标签
2026/06/25 13:57:48 so: collector reg [n:12.64] = Alt                 ← 键盘键名
2026/06/25 13:57:48 so: collector reg [n:12.94] = <nil>
2026/06/25 13:57:48 so: collector reg [n:13.87] = <nil>
2026/06/25 13:57:48 so: collector reg [n:14.96] = __oai_so_sn        ← 属性标签
2026/06/25 13:57:48 so: collector reg [n:16.22] = __oai_so_i         ← 属性标签
2026/06/25 13:57:48 so: collector reg [n:17.63] = __oai_so_bc        ← 属性标签
2026/06/25 13:57:48 so: collector reg [n:18.39] = __oai_so_bm        ← 属性标签
2026/06/25 13:57:48 so: collector reg [n:19.66] = __oai_so_hp        ← 属性标签
2026/06/25 13:57:48 so: collector reg [n:19.76] = 0
2026/06/25 13:57:48 so: collector reg [n:19.97] = <nil>
2026/06/25 13:57:48 so: collector reg [n:20.72] = <nil>
2026/06/25 13:57:48 so: collector reg [n:21.04] = type                ← 事件属性名
2026/06/25 13:57:48 so: collector reg [n:21.19] = 0
2026/06/25 13:57:48 so: collector reg [n:21.62] = 0
2026/06/25 13:57:48 so: collector reg [n:22.12] = Shift               ← 键盘键名
2026/06/25 13:57:48 so: collector reg [n:22.26] = <nil>
2026/06/25 13:57:48 so: collector reg [n:22.54] = key                 ← 事件属性名
2026/06/25 13:57:48 so: collector reg [n:25.68] = <nil>
2026/06/25 13:57:48 so: collector reg [n:28.04] = 0
2026/06/25 13:57:48 so: collector reg [n:28.86] = 0xc613a0            ← 内存地址(Promise ref)
2026/06/25 13:57:48 so: collector reg [n:29.95] = cVV3d3NkQRM=        ← base64 数据
2026/06/25 13:57:48 so: collector reg [n:3.16] = 80.83
2026/06/25 13:57:48 so: collector reg [n:3.3] = 0
2026/06/25 13:57:48 so: collector reg [n:3.46] = <nil>
2026/06/25 13:57:48 so: collector reg ... +104 more entries omitted

── D2: Snapshot 成功回调参数 ──
2026/06/25 13:57:48 so: snapshot success callback — arg type=string, toStr="ZMBU", len=4

2026/06/25 13:57:48 sentinel_dx: SO snapshot OK — result len=8
2026/06/25 13:57:48 sentinel_dx: SO snapshot decoded — raw="ZMBU", hex=5a4d4255
2026/06/25 13:57:48 sentinel_dx: SO token built — len=3884
2026/06/25 13:57:48 sentinel: finalize response — status=200, token_present=true, so_token_present=false  ❌

(第二次 snapshot 调用结果相同: "ZMBU")
```

---

## 诊断关键发现

### 🔴 核心结论：Collector 存的是标签名，不是数据值

| 寄存器 | 值 | 含义 |
|--------|-----|------|
| n:1.35 | `__oai_so_fn` | session observer 函数名标签 |
| n:11.65 | `__oai_so_t0` | 时间戳标签 |
| n:14.96 | `__oai_so_sn` | 屏幕名称标签 |
| n:16.22 | `__oai_so_i` | 索引标签 |
| n:17.63 | `__oai_so_bc` | 浏览器特征标签 |
| n:18.39 | `__oai_so_bm` | 浏览器指标标签 |
| n:19.66 | `__oai_so_hp` | 硬件属性标签 |
| n:12.64 | `Alt` | 键盘修饰键名 |
| n:22.12 | `Shift` | 键盘修饰键名 |
| n:21.04 | `type` | 事件 type 属性名 |
| n:22.54 | `key` | 事件 key 属性名 |

**这些是属性 LABEL，不是属性 VALUE。** Collector 的职责是告诉 snapshot "去读这些属性"，snapshot 拿着标签去 `s.window` mock 中执行 `window["__oai_so_hp"]` 或 `event["key"]` 等查找操作。

### Snapshot 结果对比

| 时间 | Snapshot 结果 | Hex |
|------|-------------|-----|
| 11:54 | `ZCB^` | 5a43425e |
| 13:57 | `ZMBU` | 5a4d4255 |

每次不同（因为 requirementsToken 不同 → XOR key 不同），但都是 4 字节 ASCII 大写字母。类型是 `string`（不是 nil）。

### 🔴 根因推断

```
collector_dx 字节码执行
  → 填充 regs: 标签名(__oai_so_hp)、属性名(Alt, type, key) 等
  → 返回(regs 保留在 s.collector 中)

snapshot_dx 字节码执行(运行在 collector 一样的 s.collector 上)
  → 从 regs 读取标签 → 用 opcode 6 去 s.window mock 查找属性值
  → 但 SO 版 buildWindow() 缺少 navigator.prototype 80+ 属性
  → 缺少 WebGL、React Router、Statsig 等上下文
  → 大量 jsGetProp 返回 nil
  → 整个计算链最终产出 4-byte XOR 碎片
```

**类比**：collector 写了一张购物清单（`__oai_so_hp`、`Alt`、`key`），snapshot 拿着清单去 `window` mock 这个"超市"里找货，但超市货架半空（SO mock 缺 300 行属性），提回来的篮子里只有 4 字节。

---

## 现象

### 之前 (11:54 日志)
```
sentinel_dx: SO snapshot decoded — raw="ZCB^", hex=5a43425e
```

### 现在 (13:57 日志 — 含新诊断)
```
so: collector reg [n:1.35] = __oai_so_fn      ← collector 存的是标签名！
so: collector reg [n:14.96] = __oai_so_sn
so: collector reg [n:16.22] = __oai_so_i
so: snapshot success callback — arg type=string, toStr="ZMBU", len=4
```

---

## 尝试过的修复

| # | 改动 | 预期效果 | 实际结果 |
|---|------|---------|---------|
| 1 | `so.go:155` — collector 模式才清空 regs | snapshot 读到 collector 数据 | ❌ 仍为 8 bytes |
| 2 | 添加 base64 解码诊断日志 | 看到 snapshot 原始输出 | ✅ 确认为 "ZCB^" (4 bytes) |
| 3 | 添加 D1/D2/D3 诊断日志 | 看到 collector 寄存器内容 + snapshot 回调参数 | ✅ collector 存标签名, success callback 类型 string |
| 4 | 添加 snapshot 前 10 条指令 dump | 精确定位 snapshot 在读哪些 window 属性 | ⏳ 待部署 |

---

## 下一步

1. **部署当前版本**，从 `so: snapshot ins[0..9]` 日志确认 snapshot 字节码在读哪些属性
2. 根据指令 trace，判断是否需要方向 1（升级 SO 版 `buildWindow()` 对齐 turnstile 版 ~450 行）
3. 如果指令显示只读基础属性（navigator UA、screen 尺寸等），则问题不在 mock；如果涉及 navigator prototype、WebGL、React Router，则需要补齐

---

## 相关文件

| 文件 | 作用 |
|------|------|
| `internal/so/so.go` | SO VM 核心 + D1/D2/D3/D4 诊断日志 |
| `internal/backend/sentinel_dx.go` | SO 桥接（sosession + buildSOToken） |
| `internal/backend/backend.go` | 请求编排（调用 SO） |
| `internal/turnstile/turnstile.go` | Turnstile VM（完整版 buildWindow ~450 行，供对比） |
| `aurora/internal/so/so.go` | Aurora 原版（对比基准） |
| `aurora/internal/chatgpt/request.go` | Aurora 参考请求编排 |
| `docs/plan/aurora-code-improvement-plan.md` | 整体改进计划 |
| `docs/riskM/aurora-sentinel-solution.md` | Aurora 标准答案 |
