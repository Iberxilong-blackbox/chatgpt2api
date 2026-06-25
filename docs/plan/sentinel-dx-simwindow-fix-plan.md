# sentinel dx — simWindow 数据修复计划

> 基于 Round 8 CDP Hook 实验捕获的浏览器真实 `__oai_so_*` 值，修复 Go VM 的 simWindow 初始化。

**状态**：🔄 数据修复已部署，服务端仍拒绝 — 进入差异排查阶段
**日期**：2026-06-23

---

## 背景

Round 8 在真实浏览器中通过 CDP Hook `Reflect.set` 捕获了 36 个 `__oai_so_*` 字段的加密前原始值。对比发现 Go VM 的 simWindow 存在三类问题：

1. **类型语义错误**：12 个字段 Go VM 输出 `0`/`""`/随机值，浏览器为 `null`
2. **字段缺失**：5 个字段浏览器有但 Go VM 未初始化
3. **值全为零**：17 个交互数据字段在 Go VM 中全是 `0`/`nil`，浏览器有真实时间戳和计数

服务端校验会检查这些字段的**存在性、类型、合理范围**。当前全零数据被识别为非浏览器环境。

---

## 核心设计决策：随机 vs 人类模拟

### 结论：**第一阶段用随机值，保留贝塞尔轨迹作为备选**

理由：
1. sentinel 服务端的主要检查是"字段是否存在 + 类型是否正确 + 值是否在合理范围"——不是深度轨迹分析
2. 服务端对每个 finalize 请求做统计建模的成本太高（需要存储历史轨迹做对比）
3. 浏览器数据中 `_sx0 ≈ _lx`（鼠标起始=结束=9,607），说明用户几乎没移动鼠标——服务端也没拒绝这个 session
4. 先用随机值验证假设，如果仍被拒绝再引入鼠标模拟

`C:\My_project\PRM-mail\mouse-controller\src` 提供了贝塞尔曲线轨迹生成器，如果后续需要真实鼠标移动模拟可以移植到 Go。

---

## 修改清单

### 修改 1：simWindow 预填充函数（`sentinel_dx.go`）

新增 `initSimWindow()` 函数，在 VM 执行循环之前调用，用合理初始值填充 simWindow。

#### 1a. 字段分类与初始化策略

##### 类型 A：null 字段（事件未发生，共 17 个）

这些字段在浏览器中被 SDK 初始化为 `null`，表示该类型事件监听器注册了但从未触发。Go VM 应设为 `nil`（会被 `json.Marshal` 序列化为 `null`）。

| 字段 | 浏览器值 | Go VM 当前 | 含义推测 |
|------|---------|-----------|---------|
| `__oai_so_h` | null | `""` | hash 事件 |
| `__oai_so_hi` | null | `""` | hash 事件（input?） |
| `__oai_so_hp` | null | `""` | hash 位置 |
| `__oai_so_hw` | null | `""` | hash 宽度 |
| `__oai_so_ht` | null | `0` | hashchange/touch |
| `__oai_so_hc` | null | `0` | hashchange count |
| `__oai_so_s` | null | `rand.Float64()` | **随机种子 — 应是 null！** |
| `__oai_so_t0` | null | `time.Now().UnixMilli()` | **时间基准 — 应是 null！** |
| `__oai_so_k` | null | `0` | 键盘事件 |
| `__oai_so_kp` | null | `0` | 按键事件 |
| `__oai_so_p` | null | `0` | 指针事件（pointer） |
| `__oai_so_pc` | null | `0` | 指针事件计数 |
| `__oai_so_fs` | null | ❌缺失 | 字体检测 |
| `__oai_so_fs2` | null | ❌缺失 | 字体检测（Date.now） |
| `__oai_so_fn` | null | ❌缺失 | 字体检测计数 |
| `__oai_so_bc` | null | ❌缺失 | 电池充电状态 |
| `__oai_so_bm` | null | ❌缺失 | 电池电量 |

**注意**：`_s` 和 `_t0` 在浏览器端是 `null`！这意味着浏览器 SDK 通过 `Reflect.set` 将它们设为 null 后，VM 内部计算时才从其他来源（如 opcode 17 调用 `Math.random`/`Date.now`）获取真实值并存到寄存器。Go VM 的 opcode 17 已经正确实现了 `Math.random` 和 `Date.now`，但 simWindow 中的 `_s`/`_t0` 不应被预填充——应保持 nil，让 VM 指令链自然填充。

##### 类型 B：交互数据字段（需要非零值，共 17 个）

这些字段在浏览器中有真实值，表示用户交互已发生。Go VM 需要合成合理值。

| 字段 | 浏览器值 | 含义 | 合成策略 |
|------|---------|------|---------|
| `__oai_so_wl` | 280865.40 | window load perf.now | `500 + rand(0, 2000)` ms |
| `__oai_so_m` | 281122.30 | mouse move perf.now | `_wl + rand(5000, 300000)` ms |
| `__oai_so_ss` | 113393.75 | scroll perf.now | `_wl + rand(1000, _m-_wl)` ms |
| `__oai_so_ss2` | 497145897.84 | scroll Date.now | `nowUnixMs - _wl + _ss` |
| `__oai_so_sn` | 63 | scroll 事件计数 | `rand(10, 200)` |
| `__oai_so_cs` | 17533.40 | click perf.now | `_wl + rand(500, _ss-_wl)` ms |
| `__oai_so_cs2` | 198020421.14 | click Date.now | `nowUnixMs - _wl + _cs` |
| `__oai_so_cn` | 63 | click 事件计数 | `rand(3, 100)` |
| `__oai_so_st` | 3 | scrollTop | `rand(0, 1000)` |
| `__oai_so_sw` | 7 | scrollWidth | `rand(0, 100)` |
| `__oai_so_sp` | 0 | scrollParent top | `0`（浏览器也是 0） |
| `__oai_so_spt` | 1 | scrollParent top | `rand(0, 5)` |
| `__oai_so_sx0` | 9 | 鼠标起始 x | `rand(0, 1920)` |
| `__oai_so_sy0` | 607 | 鼠标起始 y | `rand(0, 1080)` |
| `__oai_so_lx` | 9 | 最后鼠标 x | `_sx0 + rand(-100, 100)` |
| `__oai_so_ly` | 607 | 最后鼠标 y | `_sy0 + rand(-100, 100)` |
| `__oai_so_i` | 103 | 输入事件总数 | `_sn + _cn + rand(5, 50)` |
| `__oai_so_we` | 6 | window 事件计数 | `rand(1, 20)` |
| `__oai_so_wb` | 1 | blur 事件计数 | `rand(0, 3)` |

##### 时间戳一致性约束

`_ss2` 和 `_cs2` 是 `Date.now()` 值（Unix 毫秒），`_ss` 和 `_cs` 是 `performance.now()` 值（页面加载后的毫秒）。它们之间的关系：

```
_ss2 ≈ (time.Now().UnixMilli() - _wl) + _ss
_cs2 ≈ (time.Now().UnixMilli() - _wl) + _cs
```

即两个时间基准相差约等于页面加载时的 `Date.now()` 值。

更简单的公式（误差在秒级可以接受）：
```go
pageLoadDateNow := float64(time.Now().UnixMilli()) - wl
ss2 := pageLoadDateNow + ss
cs2 := pageLoadDateNow + cs
```

---

### 修改 2：`sentinel_dx.go` — 新增 `initSimWindow` 函数

```go
// initSimWindow pre-populates the simulated browser window object with
// realistic __oai_so_* values before the VM starts executing.
// This mimics the work done by sentinel SDK event listeners in a real browser.
func initSimWindow(w *turnstileOrderedMap) {
    // Type A: Null fields — event listeners registered but never triggered
    nullFields := []string{
        "__oai_so_h", "__oai_so_hi", "__oai_so_hp", "__oai_so_hw",
        "__oai_so_ht", "__oai_so_hc",
        "__oai_so_s", "__oai_so_t0",
        "__oai_so_k", "__oai_so_kp",
        "__oai_so_p", "__oai_so_pc",
        "__oai_so_fs", "__oai_so_fs2", "__oai_so_fn",
        "__oai_so_bc", "__oai_so_bm",
    }
    for _, f := range nullFields {
        w.add(f, nil)
    }

    // Type B: Interaction data fields — synthesize realistic values
    wl := 500.0 + rand.Float64()*2000.0  // window load: 0.5-2.5s
    w.add("__oai_so_wl", wl)

    m := wl + 5000.0 + rand.Float64()*295000.0  // mouse move: 5-300s after load
    w.add("__oai_so_m", m)

    ss := wl + 1000.0 + rand.Float64()*(m-wl-1000.0)  // scroll: 1s to mouse-time
    w.add("__oai_so_ss", ss)

    pageLoadDateNow := float64(time.Now().UnixMilli()) - wl
    w.add("__oai_so_ss2", pageLoadDateNow+ss)

    sn := float64(10 + rand.Intn(191))  // scroll count: 10-200
    w.add("__oai_so_sn", sn)

    cs := wl + 500.0 + rand.Float64()*(ss-wl-500.0)  // click: 0.5s to scroll-time
    w.add("__oai_so_cs", cs)
    w.add("__oai_so_cs2", pageLoadDateNow+cs)

    cn := float64(3 + rand.Intn(98))  // click count: 3-100
    w.add("__oai_so_cn", cn)

    w.add("__oai_so_st", float64(rand.Intn(1001)))     // scrollTop: 0-1000
    w.add("__oai_so_sw", float64(rand.Intn(100)))      // scrollWidth: 0-99
    w.add("__oai_so_sp", float64(0))                    // scrollParent: always 0
    w.add("__oai_so_spt", float64(rand.Intn(5)))       // scrollParentTop: 0-4

    sx0 := float64(rand.Intn(1920))  // start mouse x: 0-1919
    sy0 := float64(rand.Intn(1080))  // start mouse y: 0-1079
    w.add("__oai_so_sx0", sx0)
    w.add("__oai_so_sy0", sy0)
    w.add("__oai_so_lx", sx0+rand.Float64()*200.0-100.0)  // last x: start ± 100
    w.add("__oai_so_ly", sy0+rand.Float64()*200.0-100.0)  // last y: start ± 100

    w.add("__oai_so_i", sn+cn+float64(5+rand.Intn(46)))  // input total: sn+cn+5~50
    w.add("__oai_so_we", float64(1+rand.Intn(20)))       // window events: 1-20
    w.add("__oai_so_wb", float64(rand.Intn(4)))           // blur: 0-3
}
```

### 修改 3：`sentinel_dx.go` — 调用 `initSimWindow`

在 `simWindow := &turnstileOrderedMap{}` 之后、VM 执行循环之前添加：

```go
initSimWindow(simWindow)
```

### 修改 4：移除 opcode 17 中的错误 `_s`/`_t0` 填充

当前 opcode 17 中的 `Math.random` 和 `Date.now` 处理器是正确的 — 它们把值写入**寄存器**，不是写入 simWindow。浏览器的 `_s` 和 `_t0` 为 null 是因为这些值不是在 Reflect.set 阶段设置的，而是在 VM 执行阶段通过 opcode 17 计算的。

**因此 opcode 17 代码无需修改。**

simWindow 中的 `_s` 和 `_t0` 设为 null 后，VM 读取这些 null 值会 fall back 到 opcode 计算出的实际值。

---

## 不需要修改的项

| 项目 | 原因 |
|------|------|
| opcode 17 `Math.random` | 正确：值写入寄存器，不是 simWindow |
| opcode 17 `Date.now` | 正确：同上 |
| opcode 17 `performance.now` | 正确：返回 elapsed + jitter |
| opcode 7 `Reflect.set` 处理器 | 正确：已正确处理 `"window"` 字符串目标 |
| `toJSON()` 方法 | 正确：保持插入顺序，`nil` 值会被 `json.Marshal` 序列化为 `null` |
| `turnstileToString(nil)` | ⚠️ 当前返回 `"undefined"`，但 `toJSON()` 使用 `json.Marshal` 直接序列化值，不经过 `turnstileToString`。无影响。 |

---

## 风险与缓解

| 风险 | 概率 | 缓解 |
|------|:---:|------|
| 纯随机值被服务端检测为异常 | 中 | 值范围基于浏览器捕获数据；如被拒则引入贝塞尔轨迹（mouse-controller） |
| `_ss2`/`_cs2` 时间戳不一致 | 低 | 已用 `pageLoadDateNow` 统一基准 |
| 鼠标坐标超出屏幕 | 低 | 已限定 1920×1080 范围 + 微调 |
| `nil` vs `0` 序列化差异 | 低 | `json.Marshal(nil) → null`，`json.Marshal(0) → 0`，已验证 |
| simWindow 预填充值被 VM Reflect.set 覆盖 | 无 | 这正是期望行为 — simWindow 只是初值，VM 可以执行 Reflect.set 覆盖 |

---

## 验证步骤

1. 修改 `sentinel_dx.go`（修改 1-3）
2. `go build` 编译
3. 部署到服务器
4. 触发一次对话请求
5. 检查日志：`dxToken output` 的 base64 解码后 JSON 应包含 36 个字段，类型与浏览器一致
6. 检查日志：`so_token_present` 是否为 `true`

---

## 备选方案（如本计划被拒）

### 备选 A：贝塞尔轨迹模拟

从 `C:\My_project\PRM-mail\mouse-controller\src` 移植轨迹生成器到 Go：
- `trajectory.js` 的 `MouseTrajectoryGenerator` → Go 版本
- 生成真实鼠标移动路径（贝塞尔曲线 + 人类抖动）
- 对应填充 `_sx0, _sy0, _lx, _ly` 为轨迹的起止点
- 填充 `_m` 为轨迹时间戳

### 备选 B：直接借用浏览器 so token

如果浏览器 session 成功拿到有效的 `turnstile` token，可尝试通过 `OpenAI-Sentinel-SO-Token` header 注入。风险：token 可能绑定 device/session。

---

## 实施记录

### Round 1：initSimWindow 预填充（2026-06-23）🔴 被覆盖

**部署内容**：
- `sentinel_dx.go`：新增 `initSimWindow()` 函数（17 个 null 字段 + 19 个交互数据字段）
- `sentinel_dx.go`：在 `simWindow := &turnstileOrderedMap{}` 之后调用 `initSimWindow(simWindow)`

**结果**：`so_token_present=false`

**解码 dxToken 分析**：所有交互数据字段仍是 `0` 或 `null`，init 值被 VM 的 Reflect.set 指令全部覆盖。`_h/_hi/_hp/_hw` 字段值为函数对象，`json.Marshal` 返回空字节导致 JSON 无效（`"__oai_so_h":,`）。

**根因**：`initSimWindow` 在 VM 循环**之前**运行。VM 指令中的 Reflect.set 调用是 SDK 初始化阶段（写入 0/nil/fn），必然覆盖预填充值。真实浏览器中事件监听器在初始化**之后**触发并更新这些值，但 Go VM 没有事件循环。

### Round 2：Reflect.set 保护 + toJSON 修复（2026-06-23）🔴 仍未通过

**新增修改**：

1. **Reflect.set 保护**（`sentinel_dx.go` opcode 7 handler）— 添加 `isZeroValue` guard：
   - non-zero init 值被 VM 写入 0/nil → **拒绝覆盖**，保留 init 值
   - nil init 值被 VM 写入 0 → **拒绝覆盖**，保留 null 语义
   - nil init 值被 VM 写入非零（如 `_s`=Math.random）→ **放行**

2. **toJSON 容错**（`turnstile.go`）— `json.Marshal` 对不可序列化类型返回空字节时，回退为 `null`

**结果**：dxToken 长度从 832 → 1060 字节。解码验证：

```
修复前: {"__oai_so_h":,"__oai_so_wl":null,"__oai_so_ss":0,...}  // 无效JSON + 全零
修复后: {"__oai_so_h":null,"__oai_so_wl":1869.99,"__oai_so_ss":184151.12,...}  // 有效JSON + 合理值
```

| 字段类别 | 修复前 | 修复后 | 状态 |
|---------|--------|--------|:---:|
| `_h/_hi/_hp/_hw` | 空值（无效JSON） | `null` | ✅ |
| `_ht/_hc/_k/_kp/_p/_pc` | `0` | `null` | ✅ |
| `_wl` | `null` | `1869.99` | ✅ |
| `_m` | `null` | `213539.06` | ✅ |
| `_ss/_sn` | `0` / `0` | `184151.12` / `59` | ✅ |
| `_cs/_cn` | `0` / `0` | `36997.35` / `83` | ✅ |
| `_sx0/_sy0/_lx/_ly` | 全 `0` | `655,988 → 651,944` | ✅ |
| `_i/_we/_wb` | `0` / `0` / `0` | `153` / `3` / `1` | ✅ |
| `_fs/_fs2/_fn/_bc/_bm` | ❌缺失 | `null` | ✅ |
| `_s/_t0` | VM 填充 | VM 填充 | ✅ 不变 |

**但仍然 `so_token_present=false`** — 服务端继续拒绝。

### 当前状态总览

| 项目 | 状态 | 说明 |
|------|:---:|------|
| XOR 密钥 (sourceP) | ✅ | 解密正确，0 unknown opcode |
| Opcode 表 0-35 | ✅ | 全部实现 |
| 寄存器系统 | ✅ | `map[any]any`，浮点 key 原样 |
| PoW hash 碰撞 | ✅ | `token_present=true` |
| VM Reflect.set 写入 simWindow | ✅ | 正确写入所有 30+ 字段 |
| simWindow 字段完整性 | ✅ | 36 个字段全部出现 |
| simWindow null vs 0 语义 | ✅ | 与浏览器一致（null 保留为 null） |
| simWindow 交互数据非零 | ✅ | 时间戳、计数、坐标均有合理随机值 |
| JSON 结构有效 | ✅ | 无序列化错误 |
| **服务端接受证明** | 🔴 | `so_token_present=false` |

---

## 下一步排查方向

既然 simWindow 数据的**存在性、类型、值范围**都合理了，但服务端仍拒绝，问题可能出在以下维度：

### 方向 1：字段顺序（key order）不匹配

浏览器 SDK 按 Reflect.set **执行顺序**插入 key。Go VM 的 simWindow 顺序是 `initSimWindow` 写入（先 null 后数据），与浏览器不同。

`toJSON()` 保留插入顺序。如果服务端对 key 顺序做哈希校验，顺序不对会导致不匹配。

**验证方式**：
- 从 Round 8 CDP Hook 日志中提取浏览器 Reflect.set 的 key 写入顺序
- 使 Go simWindow 的 key 顺序与浏览器一致

### 方向 2：`_s` / `_t0` 在 simWindow vs 寄存器中的差异

浏览器端 `_s` 和 `_t0` 在 simWindow 中为 `null`。VM 通过 opcode 17（`Math.random`/`Date.now`）将实际值写入**寄存器**而非 simWindow。最终加密函数从寄存器读取而非从 simWindow 读取。

Go VM 将这些值写入了 simWindow。如果最终加密函数从 simWindow 读取 `_s`/`_t0` 而非从寄存器读取，值会不同（寄存器有真值，浏览器 simWindow 为 null）。

**验证方式**：
- 对比浏览器和 Go VM 的最终加密结果（不仅是 JSON 结构）
- 将 `_s`/`_t0` 从 simWindow 中移除（保持 nil），观察加密输出是否变化

### 方向 3：finalize 请求体的其他差异

服务端校验可能不止看 `turnstile` 字段内容：
- **Header 完整度**：`OpenAI-Sentinel-SO-Token` 之外的 headers
- **请求时序**：prepare → finalize 间隔
- **IP/会话一致性**：同一 IP 的 prepare 和 finalize
- **turnstile 字段格式**：是否加密 + 如何嵌入请求体

**验证方式**：
- CDP Hook 捕获完整 finalize 请求（headers + body）
- 与 Go 发出的 finalize 请求逐字段对比

### 方向 4：XOR 密钥版本漂移

当前使用 `sourceP`（prepare 响应中的 `p` 字段）作为 XOR 密钥。如果 SDK 实际使用新计算的 `proofToken`（PoW answer，`gAAAAAB...~S`），解密结果虽然能通过 JSON parse，但内容可能略有差异（某些常数字段值不同）。

**验证方式**：
- 用 `sourceP` 和 `rawProofAnswer` 分别解密同一 dx 指令，diff 对比 JSON 差异

### 优先级建议

1. **方向 1（key order）** — 成本最低，直接查 CDP 日志即可确认
2. **方向 3（finalize 对比）** — 影响面最大，能发现其他结构性问题
3. **方向 2（`_s`/`_t0` 位置）** — 需要理解 SDK 加密函数的数据流
4. **方向 4（XOR 密钥）** — 低概率（已验证解密正确），但值得快速验证

---

## 相关文档

- [sentinel-dx-vm-verification.md](../riskM/sentinel-dx-vm-verification.md) — 实验日志
- [sentinel-dx-diagnostic-journal.md](../riskM/sentinel-dx-diagnostic-journal.md) — Phase 1 诊断
- [PoW-618-new.md](../riskM/PoW-618-new.md) — PoW 指纹格式
- [dx-Pow.md](../riskM/dx-Pow.md) — dx + PoW 联动机制
