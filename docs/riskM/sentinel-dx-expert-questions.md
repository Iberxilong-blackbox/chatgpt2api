# Sentinel dx 逆向 — 向专家请教的核心问题

> **日期**：2026-06-24
> **背景**：我们正在逆向 ChatGPT 的 sentinel SDK（运行在 `frame.html?sv=20260423af3c` 中），目标是让 Go 后端的 VM 实现能产出被服务端接受的 `turnstile` 证明。

---

## 项目现状速览

### 已验证正确的部分

| 层次 | 内容 | 证据 |
|---|---|---|
| **解密** | `atob(dx_challenge) → XOR(key, 循环) → JSON.parse → VM指令队列` | 解密出的 265-287 条指令全部合法，0 unknown opcode |
| **PoW** | FNV-1a 32-bit hash，23 项设备指纹，碰撞格式 `gAAAAAB...~S` | `token_present=true`，PoW 验证通过 |
| **VM opcode 0-35** | 全部实现，逻辑来自 sdk.js 逐 handler 逆向 | 指令 trace 全匹配，条件跳转、子 VM 调用正常 |
| **VM 输入数据** | 36 个 `__oai_so_*` 字段，语义/类型/范围已明确 | CDP Hook Reflect.set ×3 次独立捕获确认 |
| **Go 产出的 JSON 结构** | 36 字段齐全，null/0/非零语义与浏览器一致 | dxToken 解码对比 |

### 当前卡住的问题

**服务端持续返回 `so_token_present=false`** — 即我们的 `turnstile` 密文（或 finalize 请求整体）被判定为无效。

---

## 核心未知问题

### 一、turnstile 的加密机制

这是我们最大的盲区。

#### Q1.1：turnstile 的完整生成流程是什么？

我们目前的理解是：

```
simWindow (__oai_so_* 36字段) → VM 指令链操作 → ??? → btoa → turnstile (Base64 密文)
```

已知事实：
- 浏览器产出的 turnstile 解码后是 **~1640-2450 bytes 二进制密文**（非 JSON 明文），字节分布呈双峰（0x00-0x1F 控制字符 + 0x60-0x7F 小写 ASCII 区），**不是均匀随机分布**
- sdk.js 逆向确认 opcode 3 = `s(btoa("" + t))`，即纯粹 base64 编码，没有任何加密逻辑掺杂
- XOR 操作发生在 VM 指令链中（opcode 1），不是在 btoa 阶段

**问题**：
- btoa 的输入 `t` 到底是什么？是 JSON 经过 XOR 的密文，还是直接被 VM 指令链逐字节构建的二进制串？
- 如果是 XOR(JSON, key)，为什么已知的 2 份 turnstile 之间 0 bytes 公共前缀——是每次换 key 吗？
- 如果是逐字节构建，那 "uoxy" 重复 5 次的 XOR 特征怎么解释？

#### Q1.2：XOR 加密发生在哪里？

Go VM trace 显示 VM 执行了 8 轮 XOR+btoa 迭代（初始化层），产出 ~32 chars base64（~24 bytes）的累加器。但这是**孤儿数据**——VP opcode 3 读的寄存器里没有这个值。

浏览器有 ~2000 bytes 密文。差距 ~100x。

**问题**：
- 浏览器侧 VM 的 XOR 迭代在哪个阶段？初始化层只有 8 轮，剩下的在 op30 事件回调里吗？
- 如果是事件回调，为什么 T1/T2/T3 三份 turnstile 大小与交互量无关（1641/2303/2452 bytes，中等交互反而最小）？
- XOR 的 key 是什么？是固定种子还是动态派生（从 simWindow 字段、时间戳、PoW token）？

#### Q1.3：每个 turnstile 都完全独立吗？

Round 1 实验：3 次独立 finalize，3 份 turnstile 之间 **0 bytes 公共前缀、0 bytes 公共后缀**。

**问题**：
- 这是每次用新随机 key（如 `Math.random()` 派生），还是 key 相同但明文不同？
- 如果是新 key，key 如何传递给服务端？服务端如何解密？

---

### 二、VM 架构：事件回调（opcode 30）的真作用

#### Q2.1：4 个 op30 函数到底是干什么的？

Go VM trace Round 17 发现了 4 个通过 opcode 30 定义的函数：

| destReg | body 指令数 | 注册方式 |
|---------|:---:|------|
| `44.7` | 56 | `Reflect.set(window, "__oai_so_h", fn_44.7)` — 设为 window 属性 |
| `14.82` | **113** | `addEventListener("pointermove"/"click"/"scroll", fn_14.82)` |
| `15.77` | 3 | `addEventListener("paste", fn_15.77)` |
| `68.68` | 14 | `addEventListener("wheel", fn_68.68)` |

**问题**：
- 这 186 条"从不执行"的指令（Go VM 没有事件系统）到底是在做什么？
- 是 XOR 加密迭代（累加器追加），还是环境检测（检测 headless/automation），还是两者都有？
- 如果它们是加密层，为什么 T2（更多交互）反而比 T1（最少交互）小 662 bytes？"更多回调触发 → 更大"的逻辑不成立。
- `__oai_so_h` 是一个函数而不是值 — 这个字段在 turnstile JSON 中最终表现为什么？

#### Q2.2：如果回调不负责加密，那 ~2000 bytes 密文从哪来？

排除"回调累加器"模型后，剩下两个假设：

**假设 A：turnstile = XOR(simWindow 完整快照, key)**
- 大小差异来自 simWindow 中可变长度字段（如 cookie 字符串、userAgent 等嵌入在 so 对象中）
- 但我们 CDP Hook 只看到 36 个 `__oai_so_*` 字段是 number/null — 没有长字符串字段

**假设 B：turnstile 包含了 `__oai_so_*` 之外的额外数据**
- VM 在读取 simWindow 的同时还访问了 `navigator`、`document`、`screen`、cookie 等全局对象
- 这些数据被混入 XOR 流中，使得最终密文远大于 simWindow JSON 的大小
- Go VM 只模拟了 `__oai_so_*` 字段，跳过了所有其他数据源

**问题**：
- 哪个假设更接近真相？
- VM 指令中 opcode 17（`window.X.call()`）除了 `performance.now`、`Date.now`、`Math.random`、`Object.create`、`Object.keys`、`Reflect.set` 之外，还调用了哪些 browser API？

---

### 三、服务端校验机制

#### Q3.1：服务端到底在检查什么导致 `so_token_present=false`？

我们的 Go VM 产出被拒已经 20+ 轮。已经排除的原因：
- ❌ simWindow 字段缺失/类型错误（已修复为 36 字段全都正确）
- ❌ simWindow 值全为零（已填充合理随机值）
- ❌ JSON 结构无效（已验证为合法 JSON）
- ❌ opcode 实现错误（0 unknown opcode）
- ❌ PoW 未通过（`token_present=true`）

**问题**：
- 服务端校验了哪些维度？是否包括：
  - turnstile 密文的解密成功性（XOR key 是否正确）？
  - 解密后 JSON 的 key 顺序？
  - 解密后 JSON 中特定字段的值合理性（互相关联约束，如 `_ss ≈ _ss2 - _t0`）？
  - 解密后 JSON 中特定字段必须有真实浏览器才会产生的特征（如 `_m` 的小数位分布）？
  - HTTP 层面的元数据（prepare↔finalize 时间间隔、UA 一致性、IP 一致性）？
  - turnstile 的**加密算法**本身是否为特定格式（不是任意 XOR 都接受）？

#### Q3.2：turnstile 解密失败 vs JSON 内容验证失败，如何区分？

服务端只返回一个布尔值（"接受/拒绝"），没有错误详情。

**问题**：
- 有没有办法区分是"turnstile 解密失败（格式错误）"还是"解密成功但内容未通过验证"？
- 如果有服务端源码或 protocol 文档，校验流程是怎样的？
- `turnstile` 字段中是否包含某种校验和/MAC 来防止篡改？

---

### 四、完整数据流和缺失的数据源

#### Q4.1：VM 到底从哪些数据源读取？

我们已知 VM 读取了 36 个 `__oai_so_*` 字段。但浏览器 VM 还读取了什么？

在 sdk.js 的 opcode 17 handler 中可能有以下调用：
- `navigator.userAgent` / `navigator.platform`
- `screen.width` / `screen.height` / `screen.colorDepth`
- `document.cookie` （特别是 `__cf_bm`、`_dd_s` 等 bot 检测 cookie）
- `window.innerWidth` / `window.innerHeight`
- `Intl.DateTimeFormat().resolvedOptions().timeZone`
- WebGL renderer string / canvas fingerprint
- AudioContext fingerprint

**问题**：
- 完整的 VM 输入数据源有哪些？不仅仅是 36 个 so 字段？
- 这些数据在 VM 指令链的哪个阶段被读取并混入 XOR 流？
- Go VM 没读这些额外数据 — 这是造成 ~100x 大小差距的原因吗？

#### Q4.2：`__oai_so_h` / `_hi` / `_hp` / `_hw` 的 undefined 语义

浏览器侧这 4 个字段在 `JSON.stringify` 时被跳过（undefined），但 Go VM 输出为 `null`。

**问题**：
- 这是有意行为还是无关紧要？
- 服务端是否检查这 4 个 key 的存在性？如果 `undefined`（key 不出现）不等于 `null`（key 出现但值为 null），会影响校验吗？

---

### 五、XOR 密钥与 PoW 的关系

#### Q5.1：VM 指令解密密钥 vs turnstile 加密密钥

已知：
- VM 指令解密用 `sourceP`（prepare 响应中的 `p` 字段）作 XOR 密钥，已验证正确
- PoW 的 `rawProofAnswer`（碰撞结果）也是 fernet token 格式（`gAAAAAB...~S`）
- 文档说"dx XOR 密钥与 PoW 强绑定"，但代码注释指出 dx 解密在 PoW 开始**之前**调用

**问题**：
- turnstile 的**加密**密钥（产出最终密文）是什么？
- 是从 `sourceP` 派生的，还是从 `rawProofAnswer` 派生的，还是独立的？
- 服务端如何知道解密密钥？是通过 prepare → finalize session 关联，还是密钥嵌入在 turnstile 密文中？

#### Q5.2：为什么 T1/T2/T3 三份 turnstile 完全没有公共部分？

如果 XOR key 是 session 内固定的（如 `sourceP`），三份 turnstile XOR 同一 key，相同的明文段会产生相同的密文段。但实际结果是 0 公共字节连一个字节都不重叠。

**问题**：
- XOR key 是否每次 finalize 都变化？如果是，变化因子是什么（时间戳、计数器、`Math.random`）？
- 服务端如何同步这个变化的 key？

---

### 六、Go VM 输出规模问题

#### Q6.1：~24 bytes vs ~2000 bytes 的真正原因

Go VM 的 XOR 累加器只有 ~24 bytes 密文，浏览器有 ~2000 bytes。

已排除的原因：
- ❌ 回调未触发（Round 1：大小与交互无关）
- ❌ opcode 3 少了 XOR 步骤（Round 15：sdk.js 确认 opcode 3 只做 btoa）

**问题**：
- 浏览器 VM 的 XOR 密文是在哪个阶段构建到 ~2000 bytes 的？
- 初始化层（8 轮 XOR）只产出 ~24 bytes，op30 回调如果不负责加密，那 ~2000 bytes 是谁生成的？
- 是否 op30 回调（186 条指令）在浏览器中确实触发了很多次，但**每次写入的不是"追加"而是"覆写"某个固定大小区域**？这样大小不随交互次数变化，但回调对构建"正确的密文"仍然关键。

---

### 七、验证路径

#### Q7.1：如何最有效地获取 ground truth？

我们已有的尝试：
- CDP Reflect.set Hook → ✅ 获取 36 字段输入
- CDP JSON.stringify/btoa Hook → ❌ 导致 VM 崩溃（`this` 绑定丢失）
- CDP `Runtime.evaluate` 手动注入 → ❌ SDK 已缓存原生引用，Hook 太晚
- CDP `addScriptToEvaluateOnNewDocument` 预注入 btoa Hook → ❌ 仍崩溃

**问题**：
- 有没有一个安全的方式截获 btoa 的输入而不崩溃 VM？
- CDP `Debugger.setBreakpointByUrl` 在 sdk.js 的 btoa 调用处设断点，在断点触发后 `Runtime.evaluate` 读局部变量 — 这个方案可行吗？（断点是暂停而非替换函数，应该不会破坏 `this` 绑定）
- 或者直接用本地 patch 过的 Chrome/Electron build，在 native `btoa` 处加 log？

---

## 总结：Top 5 最高优先级问题

按"回答了这个问题就能大幅推进逆向进度"的重要性排序：

1. **Q1.1 的延伸**：btoa 的输入 `t` 到底长什么样？是 XOR(JSON, key) 的二进制密文，还是 VM 逐字节拼接的结构化串？—— 这决定了 Go VM 的输出格式。

2. **Q4.1 + Q6.1**：浏览器 VM 除了 36 个 `__oai_so_*` 字段还读了什么数据？~2000 bytes 密文的内容来源是什么？—— 这决定了 Go VM 的输入规模。

3. **Q5.1 + Q5.2**：turnstile 的 XOR 密钥是什么、如何派生、每次是否变化？—— 这决定了 Go VM 的加密是否正确。

4. **Q2.1**：op30 回调的 186 条指令到底做什么？是追加 XOR、覆写 XOR、还是纯环境检测？—— 这决定了 Go VM 是否需要实现事件循环。

5. **Q3.1**：服务端的校验维度有哪些？如何定位是"解密失败"还是"内容验证失败"？—— 这决定了调试效率。

---

## 附录：关键实验数据

### turnstile 三样本对比

| | T1 (最小交互) | T2 (中等交互) | T3 (大量交互) |
|---|---|---|---|
| Base64 长度 | 3072 chars | 2188 chars | 3272 chars |
| 解码 bytes | 2303 | 1641 | 2452 |
| 公共前缀 | 0 bytes（所有 3 个之间） |
| 公共后缀 | 0 bytes |
| 内部重复 | "uoxy"×5、"Reflect.set"特征 |
| 字节分布 | 双峰：0x00-0x1F + 0x60-0x7F |

### Go VM vs 浏览器对比

| 指标 | Go VM | 浏览器 |
|---|--:|--:|
| 密文大小 | ~24 bytes | ~1640-2450 bytes |
| 输出格式 | 明文 JSON | 二进制密文 |
| XOR 迭代数 | 8 轮 | 未知 |
| op30 回调触发 | 0 次 | 未知（≥0） |
| 输入数据源 | 36 个 so 字段 | 36 so 字段 + 可能的额外 browser API |
