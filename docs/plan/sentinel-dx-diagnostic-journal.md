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

**第一次成功证明 XOR 密钥正确，VM 架构正确。** 但后续运行揭示 opcode 表并不完整（见第六轮）。

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

### ⚠️ 注意

"opcode 表完全够用"的结论在 20:23 之后被打破——后续请求出现了大量未知 opcode，见第六轮。

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

---

## 第六轮：多轮运行 — dx 程序是变化的 (2026-06-22 21:01~21:05)

### 现象

20:23 那次成功后，后续 3 次请求全部失败：

```
时间    指令数  未知opcode   proofKey  dxToken
20:23   283     0           583       ✅ true  ← 第一次成功
21:01   264     6 个         595       ❌ false ← unknown opcodes 50,82,59,73,85,29
21:04   289     3 个         591       ❌ false ← unknown opcodes 54,45,27
21:05   280     0           591       ❌ false ← 全已知但 result 为空！
```

### 分析

**核心发现：OpenAI 每次下发的 `so.collector_dx` 是不同的程序。** 20:23 的成功是"恰好拿到了一个只用已知 opcode (1-24) 的简单程序"。

**两种失败模式**：

| 模式 | 示例 | 特征 | 原因 |
|------|------|------|------|
| **A: 未知 opcode** | 21:01, 21:04 | 出现 opcode 27-85 | 我们的 opcode 表（仅 1-24）不完整，SDK 有更高编号的 opcode |
| **B: 已知但无结果** | 21:05 | 全已知 opcode，result 为空 | opcode 3 (Resolve) 没有被直接执行——它可能被 opcode 20 (条件调用) 或 opcode 7 (函数调用) 包裹，而条件不满足 |

**新出现的未知 opcode**：

```
27, 29, 45, 50, 54, 59, 73, 82, 85
```

**模式 B 的关键特征**（21:05）：280 条指令全是已知 opcode，但没有 opcode 3 被直接调用。Resolve 指令很可能是通过条件跳转间接触发的——而我们的浏览器模拟值（`Math.random`、`performance.now`、`localStorage` keys 等）不满足跳转条件。

**关于 opcode 参数的新观察**（21:04 日志）：

```
unknown opcode 27 的指令包含大量嵌套数组：
[27.56 66.79 82.19 [
  [21.24 24.57 12.44]
  [35.54 84.8 0]
  [87.75 51.78 false]
  ...
]]
```

所有数字都是浮点数（如 `50.04`, `66.01`, `0.13`）。**这些不是寄存器索引！** 这是 XOR 解密后的**混淆文本**——opcode 解析本身就是错的！

### 根因重新判断

回看 21:01 那条：
```
unknown opcode 50 (instruction: [50.04 66.01 0.13 66.01])
```

opcode 50.04？寄存器索引 66.01？**这不可能**——指令参数应该是整数索引。**XOR 密钥错了！**

等等——之前 20:23 成功了。但如果 XOR 密钥错了，20:23 也不可能成功。真正的情况可能是：**不同的 prepare 请求使用了不同的 XOR 密钥。**

回忆 `yFt` 的逻辑：
```javascript
NNt(r, e);   // 存入 e (p token)
POST /prepare {p: e}
NNt(r, e);   // 再次存入
GNt(r);      // 解密 dx，用 FNt.get(r) = e
```

如果 `p` token 在两次请求之间变了（token 刷新？），那 WeakMap 里的密钥就跟 collector_dx 不匹配。

### 也可能是：指令格式本身就不是整数数组

另一种可能：SDK 的 VM 指令本来就可以用浮点数 opcode。opcode `50.04` 中的 `50` 是操作码，`.04` 是变体标志。如果这样，我们现有的整数 opcode 解析 (`turnstileKey(token[0])`) 正确地取整为 50——但 50 不在我们的 opcode 表中。

### 待确认

- [ ] **关键**：opcode 参数是整数还是浮点数？重新检查 SDK 的 VM 执行循环，看它是怎么解析指令的
- [ ] 搜索 SDK 中的 `process[27]` ~ `process[85]` — 找到更高编号的 opcode 定义
- [ ] 确认 21:01/21:04 的 XOR 密钥是否正确——是否有时候 sourceP 不对？

### 下一步

在浏览器 Sources 面板中：
1. 找到 VM 执行循环（`for...of tokenList` / `shift()` 那段）
2. 看它是如何从 `token[0]` 提取 opcode 的——是直接取整还是有其他处理
3. 找到完整的 opcode dispatch table（`process[27]` = ..., `process[50]` = ...）
4. 把所有 opcode 定义复制到 `draft.md`

---

## 第七轮：anything-analyzer MCP 自动化分析 (2026-06-23)

### 背景

前六轮的核心瓶颈有两个：
1. **opcode 表不完整**（仅 1-24，实际有 50+）
2. **XOR 密钥稳定性存疑**（有时成功有时失败，需要确认 sourceP 是否总是正确的密钥）

之前用手动 DevTools 的方式，每次都要：设断点 → 单步 → Console 执行 → 手动复制。这种方式的局限性：
- 断点位置依赖人工猜测（如第三轮 `PNt(e)` 断点打在了 WeakMap 写入之前导致 `undefined`）
- 无法批量捕获多次请求的运行时数据
- opcode dispatch table 是混淆在 `sdk.js` 里的，需要运行时 dump

### 工具切换

**anything-analyzer** 是一个 Electron 封装的浏览器 + MCP 服务器，提供：

| 工具 | 能力 | 本轮的用途 |
|------|------|-----------|
| `cdp_send_command` + `Runtime.evaluate` | 在页面上下文执行任意 JS | 🔥 注入 WeakMap hook、dump opcode table、拦截 VM |
| `navigate` / `execute_browser_action` | 自动化页面操作 | 刷新 → 输入对话 → 触发 prepare/finalize |
| `start_capture` / `stop_capture` | 录制 HTTP 流量 | 捕获完整的 prepare/finalize 请求/响应 |
| `filter_requests` / `get_request_detail` | 查询已捕获的请求 | 提取 `collector_dx`、`so` 字段 |
| `get_hooks` | JS hook 记录（crypto/XHR/cookie） | 辅助验证 SDK 的 API 调用时序 |

**关键突破**：`cdp_send_command` 让我们可以在页面中执行任意 JS。这意味着我们可以 **hook `WeakMap.prototype.set`**，在 SDK 调用 `FNt.set(r, e)` 时直接捕获 XOR 密钥的原始值——这是手动 DevTools 做不到的。

### 方案设计

**Phase 1 — 静态 dump（无需刷新，在当前页面做）**

通过 CDP 读取 SDK 内存中的对象：
```
目标 A: opcode dispatch table — 遍历 process 数组，输出所有 opcode 编号和处理函数
目标 B: 当前页面的 WeakMap 状态 — 检查 FNt 是否已被写入
目标 C: SDK 版本信息 — sentinel 版本号、VM 指令集特征
```

**Phase 2 — 注入 hook + 重新触发流程**

```
1. CDP 注入 WeakMap.prototype.set hook → 记录所有 set 操作
2. CDP 注入 WeakMap.prototype.get hook → 记录所有 get 操作
3. 导航到 chatgpt.com（或刷新当前页面）
4. 输入对话 → 触发完整的 prepare → PoW → dx解密 → finalize 流程
5. 从 hook 日志中提取：
   - XOR 密钥的原始值（WeakMap.set 的参数）
   - collector_dx 解密后的原始指令集
   - VM 执行结果
```

**Phase 3 — 对比验证**

```
浏览器产出的 dxToken vs 我们的 VM 产出的 dxToken：
- 解码 base64 对比 key 列表
- 确认随机值差异是否在可接受范围
- 确认 opcode 覆盖是否完整
```

### 预期产出

| 产出 | 用途 |
|------|------|
| 完整 opcode 表（编号 → 功能描述） | 修复 `turnstileKey` 的未知 opcode 问题 |
| WeakMap 写入的完整时序 | 确认 XOR 密钥 = sourceP 的结论是否稳定 |
| 浏览器 finalize 的 `so` 值 | 与服务器日志中的 `dxToken` 交叉验证 |
| collector_dx 解密后的原始 JSON | 验证指令格式（整数 vs 浮点数 opcode） |

### 当前状态

- [x] 分析 anything-analyzer 工具能力，设计推进方案
- [x] Phase 1：CDP dump opcode table + WeakMap 状态
- [x] Phase 2：Go VM 扩展 opcode 表 1-24 → 0-35
- [ ] Phase 3：对比浏览器 vs 我们的 VM 输出

---

## Phase 1 执行结果 (2026-06-23)

### 方法

1. 在 `chatgpt.com` 主页通过 CDP `Page.addScriptToEvaluateOnNewDocument` 注入全局 `WeakMap.prototype.set/get` hook
2. 触发一轮对话（"hello"），SDK 按需加载
3. 从 iframe (`frame.html?sv=20260423af3c`) 获取 `SentinelSDK` 各函数的 `toString()` 源码
4. 从 HTTP 抓包获取 `sdk.js` 完整源码，静态提取 opcode dispatch table

### 发现 1: WeakMap 确认 XOR 密钥

主页面捕获到 2 条 SDK WeakMap 操作：

```
WeakMap.set: key={persona, token, expire_after, turnstile, proofofwork}, value="gAAAAAC..." [len=785]
WeakMap.get: 返回相同 "gAAAAAC..." [len=785]
```

- `set` 来自 `I()` (即 `NNt`)，`get` 来自 `$()` (即 `PNt`)
- **确认**：XOR 密钥 = legacy p token (`gAAAAAC...`)，与 Round 4 结论一致

### 发现 2: SDK 版本与双 VM 架构

当前 SDK 版本：**`20260423af3c`**（新旧两套 sentinel 共存：`20260219f9f6` 用于旧 `/req` 端点，`20260423af3c` 用于新 `chat-requirements` 端点）

SDK 包含**两个 VM**，共享**完全相同的 opcode 指令集**：

| VM | 函数 | 用途 | 状态存储 |
|----|------|------|---------|
| Sentinel VM | `Nt` | 解密 `collector_dx` / `snapshot_dx` | `At` (Map) |
| Turnstile VM | `Pn` (包装 `Tn`) | 解密 `turnstile.dx` | `Cn` (Map) |

### 发现 3: 完整 Opcode Dispatch Table

从 `sdk.js` 源码静态提取（变量名已反混淆）：

| Opcode | 内部名 | 功能 | Go VM 是否已有 |
|--------|--------|------|:---:|
| 0 | W | 递归调用 `Nt`（子程序入口，由 opcode 22 触发） | ❌ |
| 1 | z | XOR 解密 `target ^= source` | ✅ |
| 2 | B | 赋值 `set(target, value)` | ✅ |
| **3** | H | **Resolve**（成功回调，输出 btoa 结果） | ✅ |
| 4 | V | Reject（错误回调） | ✅ |
| 5 | Z | 字符串拼接 `target += value` | ✅ |
| 6 | K | 数组索引 `arr[index]` | ✅ |
| 7 | Y | 函数调用 `fn(...args)` | ✅ |
| 8 | X | 复制/Move `target = source` | ✅ |
| 9 | tt | 指令队列本身（VM 内部使用） | ✅ |
| 10 | nt | `window` 全局对象引用 | ✅ |
| 11 | et | `document.scripts` 正则匹配 | ✅ |
| 12 | rt | Map 自身引用 `At` | ✅ |
| 13 | ot | Void 函数调用（try/catch，错误写入 target） | ✅ |
| 14 | ct | `JSON.parse` | ✅ |
| 15 | it | `JSON.stringify` | ✅ |
| 16 | st | **XOR 密钥存储**（VM 输入参数，即 `sourceP`） | ✅ |
| 17 | ut | Try/catch 函数调用（异步安全） | ✅ |
| 18 | at | `atob` base64 decode | ✅ |
| 19 | ft | `btoa` base64 encode | ✅ |
| 20 | dt | 条件相等跳转 `if a===b → call fn` | ✅ |
| 21 | ht | 距离阈值跳转 `if |a-b|>threshold → call fn` | ✅ |
| 22 | pt | **子 VM 执行**（压入新指令队列，执行后恢复） | ✅ |
| 23 | lt | Null check 条件调用 `if a!==undefined → call fn` | ✅ |
| 24 | Q | 方法 bind `obj.method.bind(obj)` | ✅ |
| **25** | mt | **Noop** | **❌** |
| **26** | wt | **Noop** | **❌** |
| **27** | yt | **数组 splice / 数值减法** | **❌** |
| **28** | gt | **Noop** | **❌** |
| **29** | vt | **小于比较** `a < b` | **❌** |
| **30** | bt | **函数定义**（动态创建 callable，带参数绑定） | **❌** |
| 31 | — | 未定义（gap） | — |
| 32 | — | 未定义（gap） | — |
| **33** | kt | **乘法** `a * b` | **❌** |
| **34** | Ct | **Promise resolve**（await 异步值 → 存入 target） | **❌** |
| **35** | St | **除法** `a / b`（除零保护 → 0） | **❌** |

### 反直觉结论：Round 6 的"未知 opcode"其实是 XOR 解密错误

Round 6 报告了 opcode `50.04`, `66.01`, `82.19` 等浮点值。但 SDK 源码明确显示：

- **opcode 是纯整数 0-35**，不存在浮点 opcode
- 浮点数 `50.04` 中的 `50` 不是真实 opcode 编号
- Round 6 中 `21:01`/`21:04` 的"未知 opcode"实际上是 **XOR 解密失败** 产生的乱码

这与 Round 4 的结论吻合：`20:23` 那次 XOR 解密正确 → 283 条指令全在已知范围（1-24）。而 `21:01`/`21:04` 的 XOR 密钥（`sourceP`）可能因某些原因与 `collector_dx` 不匹配。

**但是**，即使 XOR 解密正确（如 `20:23`），也存在 opcode 25-30, 33-35 的 gap。这些高编号 opcode 如果在未来的 dx 程序中出现，我们当前的 Go VM（仅 1-24）会报 unknown opcode。所以 opcode 表仍然需要扩展。

### 对下一步的指导

1. **扩展 Go VM opcode 表 0-35**：新增 0, 25-30, 33-35 共 10 个 opcode
2. **XOR 密钥稳定性**是更根本的问题——需要确认什么情况下 `sourceP` 与 `collector_dx` 不匹配
3. `snapshot_dx` 通过 `sessionObserverToken` 独立处理（opcode 19 检查 `snapshot_dx`），当前未启用

---

## Phase 2 Implementation：Go VM opcode 扩展 1-24 → 0-35 (2026-06-23)

### 目标

根据 Phase 1 从 `sdk.js` (20260423af3c) 提取的完整 opcode dispatch table，将 Go VM 的 opcode 覆盖从 1-24（缺 4,11,12,13）扩展到 0-35。

### 改动

**`internal/backend/turnstile.go`**（共享 helper）：
- 新增 `turnstileToFloat(value any) float64` — 将 VM 寄存器值转换为 float64，供数学 opcode 使用

**`internal/backend/sentinel_dx.go`** — `solveSentinelDxToken`：
- 新增 opcode `0`：递归 Sentinel 入口 — base64 decode → XOR decrypt → JSON parse → execute sub-VM
- 新增 opcode `4`：Reject — 将错误值 btoa 编码后设为 result
- 新增 opcode `11`：`document.scripts` 正则匹配 — 模拟返回 nil
- 新增 opcode `12`：Map 自身引用 — 将 `process` map 存入寄存器
- 新增 opcode `13`：Void 函数调用 — try/catch 包装，错误写入 target
- 修复 opcode `21`：从 Noop 改为**距离阈值条件调用**（`|a-b| > threshold → call fn`）
- 新增 opcode `22`：**子 VM 执行** — 保存/恢复 tokenList 和 result，执行子指令队列
- 新增 opcode `25`, `26`, `28`：Noop（对应 SDK 的 mt, wt, gt）
- 新增 opcode `27`：数组 splice 或数值减法（根据 target 类型判断）
- 新增 opcode `29`：小于比较 `a < b`
- 新增 opcode `30`：**函数定义** — 创建动态 callable，支持参数绑定和子 VM 执行
- 新增 opcode `33`：乘法 `a * b`
- 新增 opcode `34`：Promise resolve（Go 中同步执行）
- 新增 opcode `35`：除法 `a / b`（除零保护 → 0）

**`internal/backend/turnstile.go`** — `solveTurnstileToken`：
- 完全相同的新增 opcode（Turnstile VM 与 Sentinel VM 共享指令集）

### 关键设计

**子 VM 执行模式（opcode 22）**：
```
保存 tokenList → 替换为子指令队列 → 执行 → 存储 sub-result → 恢复 tokenList
```
result 变量同样保存/恢复，确保子 VM 的 Resolve 不会污染外层。

**函数定义模式（opcode 30）**：
```
定义: destReg, returnReg, [bindings], body → 创建 turnstileFunc
调用时: 保存队列 → 绑定参数 → 设置队列为 body → 执行 → 结果写入 returnReg → 恢复队列
```

### 编译/测试

✅ 编译通过（`go build ./...`）
✅ 6 个已有测试全绿（`go test ./internal/backend/`）

### 待验证

- [ ] **部署后观察**：是否还有 `unknown opcode` 日志？
- [ ] **dxToken_produced 稳定性**：XOR 密钥（`sourceP`）是否始终与 `collector_dx` 匹配？
- [ ] Phase 3：浏览器 vs VM 输出交叉验证

### 当前 opcode 覆盖：0-35 全部实现（31,32 是 SDK 空白）

| Opcode | 功能 | 状态 |
|--------|------|:---:|
| 0 | 递归 Sentinel 入口 | ✅ |
| 1 | XOR 解密 | ✅ |
| 2 | 赋值 | ✅ |
| 3 | Resolve (btoa) | ✅ |
| 4 | Reject | ✅ |
| 5 | 字符串拼接 | ✅ |
| 6 | 数组索引 | ✅ |
| 7 | 函数调用 | ✅ |
| 8 | 复制/Move | ✅ |
| 9 | 指令队列 | ✅ |
| 10 | window 对象 | ✅ |
| 11 | document.scripts 匹配 | ✅ |
| 12 | Map 自身引用 | ✅ |
| 13 | Void 函数调用 | ✅ |
| 14 | JSON.parse | ✅ |
| 15 | JSON.stringify | ✅ |
| 16 | XOR 密钥存储 | ✅ |
| 17 | Try/catch 调用 | ✅ |
| 18 | atob | ✅ |
| 19 | btoa | ✅ |
| 20 | 条件相等调用 | ✅ |
| 21 | 距离阈值调用 | ✅ (修复) |
| 22 | 子 VM 执行 | ✅ |
| 23 | Null check 调用 | ✅ |
| 24 | 方法 bind | ✅ |
| 25 | Noop (mt) | ✅ |
| 26 | Noop (wt) | ✅ |
| 27 | 数组 splice / 减法 | ✅ |
| 28 | Noop (gt) | ✅ |
| 29 | 小于比较 | ✅ |
| 30 | 函数定义 | ✅ |
| 31-32 | (SDK gap) | — |
| 33 | 乘法 | ✅ |
| 34 | Promise resolve | ✅ |
| 35 | 除法 | ✅ |
