# Sentinel dx 诊断日志

> 持续更新的实验诊断记录。每次拿到新日志/抓包数据后追加一轮。
> 实现方案：[[sentinel-dx-vm-plan]]
> 抓包方法：[[sentinel-capture-verification-guide]]
> 逆向分析：[[dx-Pow]]

---

## 第一轮：线上日志观察 (2026-06-22)

### 现象

服务器 `journalctl` 中观察到 12 条 sentinel_dx 日志，时间跨度 11:12 ~ 15:03，**全部同一模式**：

```
sentinel_dx: so.required=true, collector_dx_present=true, dxToken_produced=false
```

### 分析

日志来自 `internal/backend/backend.go` `buildRequirements` 的诊断输出。`dxToken_produced=false` 表示 `solveSentinelDxToken` 没有产出有效 token。可能原因：

| # | 可能原因 | 可能性 | 判断依据 |
|---|---------|--------|---------|
| A | PoW 非 required → proofToken 为空 → rawProofAnswer 返回 "" → 跳过 dx 解密 | **最高** | dx 的 XOR 密钥来自 PoW proof token |
| B | XOR 解密失败或 JSON 解析失败 | 中 | proofKey 格式不匹配，或指令格式变化 |
| C | VM 执行完成但无 opcode 3 (Resolve) | 低 | 指令集中没有结束指令 |

**关键盲点**：当前日志没有区分这三种情况。

### 行动

- [x] 分析日志，列出可能原因
- [x] **第二轮**：加细化诊断日志

---

## 第二轮：加细化日志 (2026-06-22)

### 改动

**`internal/backend/backend.go`** — `buildRequirements`：
- 新增：`collector_dx` 存在但 `rawKey` 为空时，单独的警告日志
- 现有日志扩展：增加 `pow_required` 和 `proofToken_empty` 字段

**`internal/backend/sentinel_dx.go`** — `solveSentinelDxToken`：
- Base64 decode 失败时 log
- JSON parse 失败时 log（含 XOR 解密后文本前 200 字符）
- VM 开始执行时 log 指令数量
- VM 执行完成但 result 为空时 log

编译 ✅ 测试 ✅

### 新日志 (2026-06-22 17:54)

```
sentinel_dx: JSON parse FAILED — invalid character 'k' looking for beginning of value
  (xorResult preview: "kc0\x17,0,\x11\x04\f-\x1cM:ng\x15\x1c(Q\x19p\x00\x03&.F7\x047mS\x00^\f\a%\x14QU+...")

sentinel_dx: so.required=true, collector_dx_present=true, pow_required=true, proofToken_empty=false, dxToken_produced=false
```

### 分析

**场景 A（PoW 不下发）和场景 C（VM 无输出）已排除。**

确定了问题：**XOR 密钥不匹配**。证据链：

```
pow_required=true  →  PoW 确实被要求了           ✅
proofToken_empty=false → proofToken 成功生成     ✅
但 XOR 解密结果 →  二进制垃圾（非法 JSON）          ❌
```

XOR 解密后的文本 `kc0\x17,0,\x11\x04...` 是典型的**密钥错误导致的乱码**——字符级 XOR 如果密钥对了，解密出来就是可读 JSON；密钥差一个字符，全部错位。

### 根因推断

真实浏览器 SDK 的密钥链路：

```
PoW 计算 → cachedProof → WeakMap.set(context, cachedProof) → Pn() 取出 → XOR 密钥
```

我们的密钥链路：

```
PoW 计算 → answer → "gAAAAAB"+answer+"~S" → rawProofAnswer() 剥掉包装 → XOR 密钥
                                                              ↑
                                                    这一层提取逻辑可能与 SDK 不一致
```

可能的差异点：

| # | 假设 | 说明 |
|---|------|------|
| 1 | **SDK 存进 WeakMap 的不是裸 answer** | `cachedProof` 可能是完整 token（含前后缀）或其他结构，不是 `rawProofAnswer` 提取的裸 base64 |
| 2 | **我们 PoW 的 answer 格式与 SDK 不同** | 即使密钥提取方式正确，如果 PoW 生成的 answer 本身跟浏览器的不一样，XOR 结果也是错的 |
| 3 | **加密 key 根本不是 proof answer** | `dx-Pow.md` 的分析可能有遗漏——SDK 可能用了别的值做 XOR 密钥（如 PoW config 的 hash 等） |

### 结论

**代码层面的自动化解密已经走到头了。** 下一步必须从真实浏览器抓取 SDK 运行时的 XOR 密钥值，跟我们的 `rawProofAnswer(proofToken)` 做对照，才能知道差异在哪。

### 行动

- [x] 加细化日志，定位到 XOR 密钥不匹配
- [ ] **第三轮**：浏览器抓包，获取真实 SDK 的 XOR 密钥

---

## 第三轮：浏览器抓包 — SDK 调用链追踪 (2026-06-22)

### 目标

从真实浏览器的 ChatGPT 页面中，找到 SentinelSDK 用来解密 `so.collector_dx` 的 XOR 密钥来源和格式。

### 方法

DevTools Sources 面板 → 搜索 `collector_dx` → 命中的 JS 文件中设断点 → 逐层追踪调用链。

### 发现：SDK 函数调用链还原

通过断点追踪，还原了完整的 dx 处理调用链：

```
prepare 响应 e =
  ├── e.prepare_token
  ├── e.proofofwork: {required, seed, difficulty}
  ├── e.turnstile: {required, dx}
  ├── e.so: {required, collector_dx, snapshot_dx}
  └── ...

        ↓ GNt(e) 被调用

function GNt(e) {
    let t = UNt(e);                          // t = e.so
    !e || !WNt(t) || !t?.collector_dx        // 检查 required && collector_dx 存在
        || BNt(e, t.collector_dx).catch(...)  // ← 调用解密
}

        ↓ BNt(e, collector_dx)

function BNt(e, t) {                         // e=响应对象, t=collector_dx 字符串
    let n = PNt(e ?? {}) ?? ``;              // ← 从 WeakMap 取 XOR 密钥！
    return INt( () => VNt(t, n))             // VNt 用密钥 n 解密 collector_dx
}

        ↓ PNt(e)

function PNt(e) {
    return FNt.get(e)                         // FNt 是 WeakMap，key=e（响应对象）
}

        ↓ NNt(e, t) — WeakMap 写入

function NNt(e, t) {
    FNt.set(e, t)                             // key=响应对象, value=XOR 密钥
}
```

**核心发现**：

| 项目 | 我们的假设 | SDK 实际做法 |
|------|-----------|-------------|
| XOR 密钥来源 | `rawProofAnswer(proofToken)` — 剥掉 `gAAAAAB`/`~S` 的裸 base64 | `FNt.get(e)` — 从 WeakMap 取值，key 是整个响应对象 `e` |
| 密钥写入时机 | 在 `buildRequirements` 中当场提取 | PoW 解完后调用 `NNt(e, proofValue)` 存入 |
| 密钥格式 | 裸 PoW answer（无前后缀） | **未知** — 需要看 `NNt` 被调用时传了什么 |

### 关键拦截：PNt(e) 返回 undefined

在 `BNt` 断点处执行 `PNt(e)` → **返回 `undefined`**！

```
console.log("from WeakMap:", PNt(e))   // → undefined
console.log("length:", PNt(e)?.length)  // → undefined
console.log("type:", typeof PNt(e))     // → "undefined"
```

这说明 **`FNt.set(e, proofValue)` 还没被调用**。WeakMap 此时是空的，XOR 密钥还未写入。

两种可能：
1. **时序问题**：`NNt` 在 PoW 完成后的某个异步回调中调用，但 `BNt` 在断点处被提前触发了（在 PoW 还没算完的时候）
2. **key 不匹配**：`NNt` 用另一个对象作为 key 写入了 WeakMap，不是当前这个 `e`

### prepare 响应结构（从 e 对象 dump）

```
prepare_token: "gAAAAABqOR5VM6o6GiTFDbwLWYUHKov9pehFIt77-..."
proofofwork: {required: true, seed: '0.8155255253834088', difficulty: '06cc11'}
turnstile: {required: true, dx: 'PBp5bWFyd3lJaFttfBxfaTACQkxae2dEZQ1Jf1xlR3xf...'}
so: {required: true, collector_dx: 'PBp5bWF3e3lCYFttfBxfaTAHTkxRc2dEYQZJelllR383...', snapshot_dx: '...'}
```

### 待继续

- [ ] 找到 `NNt(e, t)` 的调用点，确认它何时被调用、传入的 `t` 值格式
- [ ] 确认 WeakMap 写入的时序——是否在 `BNt` 之前
- [ ] 如果真的没写入，需要理解 SDK 的 PoW→WeakMap 完整流程

### 搜索 `NNt(` 结果

5 处匹配：`NNt(e,t)`, `NNt(r,e)`, `NNt(r,e)`, `NNt(t,e)`, `NNt()` — 尚未逐一排查调用上下文。

`cachedProof` 变量名在当前 SDK 版本中未找到（可能已被重命名）。

---

## 第四轮：SDK 源码静态分析 — 时序还原 (2026-06-22)

### 目标

用户从浏览器复制了 `yFt` 和 `NNt` 相关源码到 `draft.md`。通过静态分析 `yFt` 函数（Sentinel 主流程），还原 WeakMap 写入和 dx 解密的真实时序。

### 发现：**XOR 密钥是 legacy p token，不是 proof token！**

**`yFt(e, t)` 函数**（`draft.md` 第 5-87 行）是 Sentinel 的核心流程函数。参数 `e` 是 **legacy p token**（`gAAAAAC...`）。

关键时序：

```
yFt(e, t)  入口: e = legacy p token ("gAAAAAC...")
│
├─ [同步]  NNt(r, e)           ← FNt.set(r, e) — WeakMap 存入 p token
│
├─ [异步]  POST /prepare {p: e}
│          r.so = a.so          ← populate so 字段
│          NNt(r, e)            ← 再次存入 p token
│          GNt(r)               ← ★ 触发 dx 解密！此时 FNt.get(r) = e = legacy p token
│                                  PoW 还没开始计算！
│
├─ [异步]  KM.getEnforcementToken(t)   ← PoW 才在这里开始算！（第 40 行）
│          uN.getEnforcementToken(t)   ← Turnstile 也在这里算
│          POST /finalize {proofofwork, turnstile}
│          NNt(t, e)           ← 第三次存入（finalize 后，此时 dx 早已解密完）
```

**对应代码行**（`draft.md`）：

```
第 17 行:  NNt(r, e)            ← 存入 p token
第 21 行:  POST /prepare {p: e}
第 33 行:  NNt(r, e)            ← 再次存入 p token  
第 34 行:  GNt(r)               ← 解密 collector_dx ←── 此时 PoW 还没开始！
第 40 行:  KM.getEnforcementToken(t)   ← PoW 在这里才开始！
```

### 结论

**`dx-Pow.md` 的分析在这一版本被证伪了。** 在当前 SDK 版本中：

| | `dx-Pow.md` 的结论 | 实际代码 |
|---|---|---|
| XOR 密钥 | PoW proof token (`gAAAAAB...`) | **Legacy p token** (`gAAAAAC...`) |
| 密钥绑定 | "与 PoW Token 强绑定" | 与 Turnstile 共享同一个密钥 |
| 时序 | PoW 完成后 dx 才能解密 | dx 在 PoW **之前**解密 |

**这解释了为什么第二轮日志中 XOR 解密结果是乱码**——我们用的密钥 `rawProofAnswer(proofToken)` 完全不是 SDK 用的密钥。SDK 用的是 `sourceP`（legacy p token）。

### 也解释了 Console `undefined` 之谜

`PNt(e)` 在 Console 返回 `undefined`，很可能是断点打在了**第一个** `GNt` 调用之前——即 `yFt` 里的 `NNt(r, e)` 还没执行，WeakMap 还是空的。或者断点触发了另一个代码路径。但这不妨碍静态分析结论的正确性。

### 行动

- [x] 还原 `yFt` 完整时序，确认 XOR 密钥 = legacy p token
- [ ] **第四轮代码修改**：`sourceP` 替换 `rawProofAnswer(proofToken)` 作为 XOR 密钥

---

## 第四轮代码修改：sourceP 替换 rawProofAnswer (2026-06-22)

### 改动

**`internal/backend/backend.go`** — `buildRequirements`：

修改前：
```go
if util.Clean(so["collector_dx"]) != "" {
    rawKey := rawProofAnswer(proofToken)
    if rawKey != "" {
        dxToken = solveSentinelDxToken(util.Clean(so["collector_dx"]), rawKey)
    }
}
```

修改后：
```go
if util.Clean(so["collector_dx"]) != "" {
    // SDK 源码分析确认：XOR 密钥是 legacy p token (sourceP)，不是 proof token。
    // 参见 yFt() 时序：NNt(r, e) 存入 p token → GNt(r) 调用时 PoW 还没开始。
    // dx-Pow.md 中 "密钥与 PoW Token 强绑定" 的结论在当前 SDK 版本被证伪。
    dxToken = solveSentinelDxToken(util.Clean(so["collector_dx"]), sourceP)
}
```

### 预期

部署后 `dxToken_produced` 变为 `true`，日志出现：
```
sentinel_dx: VM start — N instructions, proofKey len=...
```

如果 XOR 解密成功但 VM 产出空，则出现：
```
sentinel_dx: VM executed N instructions but result is EMPTY
```

如果出现未知 opcode 则日志会逐条报告。

### 编译/测试

✅ 编译通过  
✅ 4 个已有测试全绿

### 改动内容

`internal/backend/backend.go` — `buildRequirements`：

```diff
- rawKey := rawProofAnswer(proofToken)
- if rawKey != "" {
-     dxToken = solveSentinelDxToken(util.Clean(so["collector_dx"]), rawKey)
- }
+ // 使用 legacy p token (sourceP) 作为 XOR 密钥
+ dxToken = solveSentinelDxToken(util.Clean(so["collector_dx"]), sourceP)
```

### 新日志 (2026-06-22 20:23)

```
sentinel_dx: VM start — 283 instructions, proofKey len=583
sentinel_dx: so.required=true, collector_dx_present=true, pow_required=true, proofToken_empty=false, dxToken_produced=true
```

### 分析

🎉 **完全成功。** 链路全部打通：

| 指标 | 值 | 含义 |
|------|-----|------|
| VM 指令数 | 283 | XOR 解密成功，JSON 解析出 283 条指令 |
| proofKey 长度 | 583 | `sourceP` (legacy p token) 作为 XOR 密钥 |
| `dxToken_produced` | **true** | VM 执行完成，opcode 3 (Resolve) 产出非空结果 |
| unknown opcode 日志 | 0 条 | 全部 283 条指令在已知 opcode 表内（与 Turnstile VM 共用指令集） |
| `result is EMPTY` 日志 | 0 条 | VM 成功执行到 Resolve 指令 |

### 结论

**`dx-Pow.md` 的 "密钥与 PoW Token 强绑定" 在当前 SDK 版本被证伪。** 实际密钥是 legacy p token，与 Turnstile 共享。

**Sentinel dx VM 实现（`internal/backend/sentinel_dx.go`）的 opcode 表完全够用。** 283 条指令全部被已知 opcode 覆盖，不需要新增任何 opcode。

**数据流已完整**：

```
Bootstrap → legacy p token (gAAAAAC...)
    → POST /prepare {p: gAAAAAC...}
    → 响应: {proofofwork, turnstile, so.collector_dx}
    → PoW 解算 → proofToken (gAAAAAB...~S)
    → Turnstile 解密 (密钥=sourceP) → turnstileToken
    → Sentinel dx 解密 (密钥=sourceP) → dxToken  ← ✅ 新打通
    → POST /finalize {prepare_token, proofofwork, turnstile, so}  ← ✅ so 字段现在有值
```

### 待验证

- [ ] 长时间运行观察：是否还有 `dxToken_produced=false` 的情况？
- [ ] `snapshot_dx` 字段的用途（当前未处理）
- [ ] OpenAI 服务端对 `so` 字段的验证——是否接受我们产出的 dxToken？

---

## 第五轮：交叉验证 — 浏览器 vs 我们的 VM 输出 (待执行)

### 目标

`dxToken_produced=true` 只证明我们**产出了一个结果**。但 VM 执行过程中有大量浏览器环境模拟（`window.document.location`、`window.Object.keys(localStorage)`、`window.Math.random`、`window.performance.now` 等），这些模拟值如果跟真实浏览器不一致，最终 base64 编码后的 `so` 值就会不同。

**验证方法**：抓取真实浏览器 finalize 请求中的 `so` 值，与同一组输入（同一次 prepare 响应）下我们产出的 `dxToken` 对比。

### 方法

核心问题：prepare 响应是一次性的（含 `prepare_token`），同一个 prepare 只能被 finalize 一次。无法用"同一个 prepare"同时让浏览器和我们的代码各自解一遍。

因此需要**两次独立的 prepare 请求**，各自触发一次 dx 解密。虽然输入不同，但如果两次 VM 输出**结构相似**，就能确认我们的 VM 行为正确。

#### 操作步骤

**1. 浏览器侧（你操作）**

在 DevTools Network 面板中，搜索 `finalize`，展开请求体，复制 `so` 字段的值。形式类似：

```json
{
  "prepare_token": "...",
  "proofofwork": "gAAAAAB...",
  "turnstile": "...",
  "so": "eyJ3aW5kb3cuZG9jdW1lbnQ..."  ← 复制这个
}
```

**2. 服务器侧（我来操作）**

在 `sentinel_dx.go` 的 `solveSentinelDxToken` 成功返回处加一行日志，打印最终产出的 `dxToken`：

```go
log.Printf("sentinel_dx: dxToken output: %s", result)
```

部署后从 `journalctl` 取一条对应的 `dxToken`。

**3. 对比**

两条 `so` 值都是 base64，decode 后对比结构：
- key 列表是否相似（`window.document.location`、`window.Object.keys` 等）
- 值的类型是否匹配（字符串、数组、布尔）
- 差异是否集中在随机值（`Math.random`、`performance.now`）

### 当前状态

（待执行）
