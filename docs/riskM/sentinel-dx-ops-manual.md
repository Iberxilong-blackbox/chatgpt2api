# Sentinel dx 运维手册

> 日常监控、异常识别、故障排查的操作指南。
> 实现方案：[`sentinel-dx-log-cleanup`](sentinel-dx-log-cleanup.md)
> 实验记录：[`sentinel-dx-diagnostic-journal`](sentinel-dx-diagnostic-journal.md)

---

## 一、涉及的文件

| 文件 | 位置 | 用途 | 查看方式 |
|------|------|------|---------|
| 实时日志 | `journalctl` | 每次请求的 6 行关键摘要 | `journalctl -u chatgpt2api -f` |
| so 事件文件 | `/opt/chatgpt2api/data/logs/so_events.log` | 关键事件持久记录（追加、不滚动） | `cat /opt/chatgpt2api/data/logs/so_events.log` |

---

## 二、日常监控：每次请求看什么

一次正常的对话请求，sentinel 相关日志固定为 **6 行**：

```
sentinel_dx: xorResult preview: "[[8, 11.39, 8], [11.39, 72.25, 2]..."
sentinel_dx: VM start — 270 instructions, proofKey len=595
sentinel_dx: RESULT set: str("eyJ3aW5kb3cu...")
sentinel_dx: dxToken output (len=704): eyJ3aW5kb3cu...
sentinel_dx: so.required=true, collector_dx_present=true, pow_required=true, proofToken_empty=false, dxToken_produced=true
sentinel_dx: finalize response — status=200, token_present=true, so_token_present=false
```

### 2.1 每行的含义

| 行 | 日志 | 告诉你什么 | 正常值 |
|----|------|-----------|--------|
| 1 | `xorResult preview` | XOR 解密后的指令文本预览。以 `[[` 开头 = 合法 JSON；乱码 = 密钥不对 | 以 `[[8, N, 8]` 模式开头 |
| 2 | `VM start` | XOR 解密成功，VM 开始执行。指令数和密钥长度是 SDK 版本的指纹 | 指令数 250-300，proofKey len 570-600 |
| 3 | `RESULT set` | VM 执行到 opcode 3（Resolve），产出了结果 | 出现即正常 |
| 4 | `dxToken output` | 最终产出的 base64 字符串。正常是 700+ 字节（30+ 个 `__oai_so_*` 字段的 JSON → base64） | len=700-800 |
| 5 | `so.required=...` | dx 处理全链路结果摘要：是否要求 so、有无 collector_dx、PoW 状态、最终是否产出 | `dxToken_produced=true` |
| 6 | `finalize response` | 服务端的态度：是否返回了 token、是否返回了 `so_token` | `status=200, token_present=true` |

### 2.2 当前正常基线（chatgpt-freeaccount）

| 指标 | 正常值 |
|------|--------|
| 指令数 | 250-300 |
| proofKey 长度 | 570-600 |
| dxToken 长度 | 700-800 字节 |
| `dxToken_produced` | `true` |
| `so_token_present` | **`false`**（当前 OpenAI 未全量启用 so 验证） |
| 每次请求 sentinel 日志量 | 6 行 |

---

## 三、异常模式识别

### 模式 1：XOR 密钥失效 🔴

**症状：**
```
sentinel_dx: xorResult preview: "kc0\x17,0,\x11\x04\f-..."     ← 不是 [[ 开头
sentinel_dx: JSON parse FAILED — invalid character 'k'...
sentinel_dx: so.required=true, ..., dxToken_produced=false
```

**判断**：XOR 解密结果是乱码。`xorResult preview` 不是以 `[[` 开头 = 密钥错误。

**排查方向**：
1. 检查 `sourceP`（legacy p token，`gAAAAAC...`）是否与 prepare 请求中带的 `p` 字段一致
2. SDK 可能更新了密钥方案（从 `sourceP` 切换到其他值）
3. 如果只是偶发：可能是 token 过期刷新的时序问题

**影响**：dxToken 无法产出，但不影响对话（当前 so 非必须）。

---

### 模式 2：未知 opcode 出现 🟡

**症状：**
```
sentinel_dx: VM start — 270 instructions, proofKey len=595
sentinel_dx: UNKNOWN key=50 args=[50.04 66.01 0.13]
sentinel_dx: RESULT set: str("...")
sentinel_dx: dxToken output (len=8): dHJ1ZQ==             ← 产出物很小！
```

**判断**：SDK 新增了我们的 opcode 表（0-35）中没有的指令。VM 虽然没崩，但新指令被跳过了，导致产出物质量下降（`"true"` 而不是完整的 700+ 字节 so JSON）。

**会自动写入 `so_events.log`**：
```json
{"ts":"...","event":"unknown_opcode","detail":{"key":50,"args":[...],"instruction_index":42,...}}
```

**排查方向**：
1. `cat /opt/chatgpt2api/data/logs/so_events.log` 查看具体的新 key 和参数
2. 从浏览器重新 dump SDK 的 dispatch table（`sdk.js` 中 `At.set(N, function)` 部分）
3. 在 `sentinel_dx.go` 中补全新 opcode 的 handler

**影响**：dxToken 质量可能下降（产出不完整的 so JSON）。如果 so 验证开启，可能被拒绝。

---

### 模式 3：VM 空结果 🟡

**症状：**
```
sentinel_dx: VM start — 270 instructions, proofKey len=595
sentinel_dx: VM executed 270 instructions but result is EMPTY
sentinel_dx: so.required=true, ..., dxToken_produced=false
```

**判断**：VM 执行了全部指令，但 opcode 3（Resolve）没有被触发。没有 `RESULT set` 行。

**排查方向**：
1. 条件跳转路径变了——某个 opcode 20/21/23 的判断条件与 SDK 不一致
2. SDK 微调了指令序列，改变了触发 opcode 3 的前提条件
3. 与已知正常请求对比 `xorResult preview` 的指令结构是否一致

**影响**：本请求 dxToken 为空。

---

### 模式 4：dxToken 结构异常 🟡

**症状：**
```
sentinel_dx: dxToken output (len=8): dHJ1ZQ==             ← 只有 8 字节
或
sentinel_dx: dxToken output (len=8): NTQuNzI=             ← base64 decode = "54.72"
```

**判断**：VM 跑完了且 `dxToken_produced=true`，但产出的不是正经的 so JSON。正常是 700+ 字节，这里只有 8 字节（`"true"` 或一个浮点数字符串）。

**会自动写入 `so_events.log`**（`dx_token_anomaly` 事件）。

**根因**：VM 走到了 opcode 3 的 nil fallback 路径——结果寄存器是 nil，回退到 `simWindow.toJSON()`，但 simWindow 里几乎没数据。

**排查方向**：
1. 可能前一个异常模式（未知 opcode）的连锁反应
2. 可能 `Reflect.set` 的目标对象类型不匹配
3. 可能浏览器 API 模拟值（`Date.now`、`performance.now` 等）与 SDK 预期偏差太大

**影响**：dxToken 存在但可能无效。如果 so 验证开启，大概率被拒绝。

---

### 模式 5：so_token_present 变为 true 🟢🔴

**症状：**
```
sentinel_dx: finalize response — status=200, token_present=true, so_token_present=true
```

**判断**：**OpenAI 开启了 so 验证！** 这是一个关键时刻——服务端返回了 `so_token`，意味着后续 conversation 请求必须带 `OpenAI-Sentinel-SO-Token` header。

**会自动写入 `so_events.log`**：
```json
{"ts":"...","event":"so_token_present","detail":{"so_token":"...","dx_token_prefix":"...","proof_key_prefix":"...","finalize_status":200}}
```

**需要做的**：
1. 检查 `so_events.log` 获取完整的 `so_token` 和对应 `dxToken`
2. 确认代码中 `ChatRequirements.SOToken` 已被正确赋值（当前 `backend.go` line 487 已有）
3. 确认 conversation 请求中已带 `OpenAI-Sentinel-SO-Token` header
4. 如果 conversation 返回 403/401，说明 dxToken 产出与服务端预期不符——需要重新做浏览器 vs VM 交叉验证

---

## 四、so_events.log 文件说明

### 4.1 文件特性

- **路径**：`/opt/chatgpt2api/data/logs/so_events.log`
- **写入方式**：追加（append），每次事件一行 JSON
- **不自动滚动**：文件只增不减，永不删除。因为事件本身稀少（正常情况下可能几天甚至几周才有一条）
- **容错**：写入失败静默忽略，不影响主请求流程
- **权限**：由 `chatgpt2api` 用户创建和写入，与项目其他文件一致

### 4.2 三种事件类型

| 事件 | 触发条件 | 含义 |
|------|---------|------|
| `so_token_present` | finalize 响应中 `so_token != ""` | 服务端认可了我们的 dxToken |
| `unknown_opcode` | VM 遇到不在 0-35 dispatch table 的 key | SDK 新增了 opcode |
| `dx_token_anomaly` | dxToken 产出但 base64 decode 后结构异常 | VM 产出物质量有问题 |

### 4.3 查看命令

```bash
# 查看所有事件
cat /opt/chatgpt2api/data/logs/so_events.log

# 只看 so_token_present 事件
grep '"so_token_present"' /opt/chatgpt2api/data/logs/so_events.log

# 只看 unknown_opcode 事件
grep '"unknown_opcode"' /opt/chatgpt2api/data/logs/so_events.log

# 查看最近 5 条事件
tail -5 /opt/chatgpt2api/data/logs/so_events.log
```

---

## 五、快速诊断流程图

```
journalctl -u chatgpt2api -f 看到一次请求
         │
         ├─ xorResult preview 以 [[ 开头？
         │   ├─ 是 → 继续
         │   └─ 否 → 模式1: XOR 密钥失效
         │
         ├─ 有 UNKNOWN key= 行？
         │   ├─ 否 → 继续
         │   └─ 是 → 模式2: 查看 so_events.log
         │
         ├─ 有 RESULT set 行？
         │   ├─ 是 → 继续
         │   └─ 否 → 模式3: VM 空结果
         │
         ├─ dxToken output len > 500？
         │   ├─ 是 → 继续
         │   └─ 否 → 模式4: 查看 so_events.log
         │
         └─ finalize response so_token_present=？
             ├─ false → 正常（当前基线）
             └─ true  → 模式5: 关键时刻！查看 so_events.log
```

---

## 六、当前状态与已知限制

| 项目 | 状态 | 说明 |
|------|:---:|------|
| XOR 密钥 | ✅ | `sourceP`（legacy p token），100% 正确（第十轮验证） |
| Opcode 表 | ✅ | 0-35 全覆盖（31,32 是 SDK gap） |
| VM 寄存器系统 | ✅ | 浮点 key 正确保留 |
| dxToken 产出 | ✅ | 通过 opcode 3 nil fallback → `simWindow.toJSON()` 稳定产出 |
| 服务端接受度 | ⏳ | 当前 `so_token_present=false`（与浏览器行为一致），等待 OpenAI 开启 |
| 浏览器交叉验证 | ❌ | 浏览器当前不发 `so`，无法对照 |

**已知限制**：dxToken 是通过 nil fallback 路径产出的（VM 程序期望真实的浏览器事件来填充结果对象，我们没有浏览器环境）。当 OpenAI 开启 so 验证时，产出的 dxToken 可能因缺少真实事件数据而被拒绝。届时需要重新评估。
