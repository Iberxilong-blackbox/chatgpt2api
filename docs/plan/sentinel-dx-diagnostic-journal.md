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
| A | PoW 非 required → proofToken 为空 → rawProofAnswer 返回 "" → 跳过 dx 解密 | **最高** | dx 的 XOR 密钥来自 PoW proof token。如果 prepare 响应中 `proofofwork.required=false`，就没有密钥来解密 `collector_dx` |
| B | XOR 解密失败或 JSON 解析失败 | 中 | proofKey 格式不匹配，或 `so.collector_dx` 的指令格式与预期不同 |
| C | VM 执行完成但无 opcode 3 (Resolve) | 低 | 指令集中没有结束指令，result 保持 "" |

**关键盲点**：当前日志没有区分这三种情况。

### 待确认

- [ ] `proofofwork.required` 是否为 true？
- [ ] 如果 PoW required，`proofToken` 是否成功生成？
- [ ] `rawProofAnswer(proofToken)` 是否提取到了非空密钥？
- [ ] `solveSentinelDxToken` 内部在哪个阶段失败（base64 decode / JSON parse / VM 执行）？
- [ ] 是否有 "unknown opcode" 日志被忽略？

### 行动

- [x] 分析日志，列出可能原因
- [ ] **第二轮**：加细化诊断日志，区分 A/B/C 三种情况

---

## 第二轮：加细化日志 (2026-06-22)

### 改动

两个文件加日志：

**`internal/backend/backend.go`** — `buildRequirements`：
- 新增：`collector_dx` 存在但 `rawKey` 为空时，单独的警告日志（含 `pow_required`、`proofToken_empty`、`proofToken_len`）
- 现有日志扩展：增加 `pow_required` 和 `proofToken_empty` 字段

**`internal/backend/sentinel_dx.go`** — `solveSentinelDxToken`：
- Base64 decode 失败时 log（含 dx len、proofKey len）
- JSON parse 失败时 log（含 XOR 解密后文本前 200 字符）
- VM 开始执行时 log 指令数量
- VM 执行完成但 result 为空时 log

编译 ✅ 测试 ✅ (4 个已有测试全绿)

### 预期

部署后观察日志，根据输出可明确区分：

| 如果看到... | 说明 |
|------------|------|
| `NO XOR KEY — pow_required=false` | **场景 A 确认**：PoW 不下发，没有密钥解密 dx |
| `NO XOR KEY — pow_required=true, proofToken_empty=true` | PoW 要求了但解算失败 |
| `base64 decode FAILED` | dx 编码异常 |
| `JSON parse FAILED` + xorResult preview | **场景 B 确认**：XOR 密钥不匹配或指令格式变化 |
| `VM start — N instructions` + `result is EMPTY` | **场景 C 确认**：VM 跑了但没有 opcode 3 |
| `unknown opcode N` | 指令集出现了新的 opcode |

### 新日志

（待部署后填入 `journalctl` 输出）

### 分析

（待填入）

---

## 第三轮：（待定）

