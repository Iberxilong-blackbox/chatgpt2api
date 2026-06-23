# Plan: 精简 sentinel dx 日志 + so_event 文件记录

## Context

经过 13 轮实验，sentinel dx VM 已稳定运行——XOR 解密正确、opcode 全覆蓋、dxToken 产出合法 JSON。当前逐指令 trace 日志每次对话产生 ~800 行，已无诊断价值。需要：
1. 移除逐指令 trace，只保留关键节点日志
2. 当出现 `so_token_present=true` 等关键事件时，写入专用文件 `data/logs/so_events.log`，方便偶尔登服务器检查

## 涉及文件

### 1. `internal/backend/sentinel_dx.go` — 精简日志

**移除**以下 18 个 trace 日志调用：
- 逐指令 dispatch: `[%d] dispatch key=%v args=%v` (line 684)
- 每次寄存器读写: `set %v = %s` (line 95), `get %v -> nil (UNINITIALIZED)` (line 88)
- 条件跳转详情: op20 EQ true/false (lines 361, 364), op21 distance (lines 382, 389), op23 null-check (lines 397, 400)
- 函数调用详情: `op7 call target=%v` (line 211)
- Reflect.set 每次写入: `Reflect.set -> simWindow[...]` (line 221), `Reflect.set -> orderedMap[...]` (line 218), `Reflect.set -> UNHANDLED` (line 223)
- 子 VM 进出: `op0 recursive entry` (line 423), `op22 sub-VM enter/exit` (lines 469, 498)
- opcode 3 fallback: `op3 resolve -- reg %v is nil, falling back to simWindow` (line 135)
- opcode 4 Reject: (line 150) — 改为仅在实际 reject 发生时 log

**保留** 10 个关键日志（`sentinel_dx.go` 7 个 + `backend.go` 3 个）：

*sentinel_dx.go:*
- `base64 decode FAILED` (line 23) — base64 解码错误
- `xorResult preview` (line 35) — XOR 解密后的文本预览
- `JSON parse FAILED` (line 43) — JSON 解析错误
- `VM start — N instructions, proofKey len=X` (line 46) — 关键里程碑
- `UNKNOWN key=%v args=%v` (line 680) — 新的未知 opcode 出现
- `RESULT set: %s` (line 687) — VM 产出结果
- `VM executed N instructions but result is EMPTY` (line 692) — VM 空结果
- `dxToken output (len=%d): %s` (line 694) — 最终 dxToken

*backend.go:*
- `so.required=true, collector_dx_present=..., dxToken_produced=...` (buildRequirements) — 每轮 dx 处理结果摘要
- `finalize response — status=..., token_present=..., so_token_present=...` (line 479) — 服务端接受度诊断

### 2. `internal/backend/sentinel_dx.go` — 新增 so_event 文件记录函数

新增函数 `logSOEvent(format string, args ...any)`：
- 路径: `data/logs/so_events.log`（相对于工作目录，即 `/opt/chatgpt2api/data/logs/so_events.log`）
- 函数内做 `os.MkdirAll("data/logs", 0755)` 确保目录存在
- 每次调用以追加模式打开文件，写入带时间戳的一行 JSON
- 格式: `{"ts":"2026-06-23T15:32:33Z","event":"<event_name>","detail":{...}}`
- 写入失败静默忽略（不影响主流程）

**各调用点的 `detail` 结构：**

① `backend.go` finalize 响应中 `soToken != ""` 时：
```json
{
  "event": "so_token_present",
  "detail": {
    "so_token": "<soToken 值>",
    "dx_token_prefix": "<dxToken 前 80 字符>",
    "dx_token_len": 704,
    "proof_key_prefix": "<sourceP 前 50 字符>",
    "finalize_status": 200
  }
}
```

② `sentinel_dx.go` 遇到新的 unknown opcode 时：
```json
{
  "event": "unknown_opcode",
  "detail": {
    "key": 50,
    "args": ["<args 摘要，截断到 200 字符>"],
    "instruction_index": 42,
    "dx_token_preview": "<当前 dxToken 前 80 字符，或空>",
    "proof_key_prefix": "<sourceP 前 50 字符>"
  }
}
```

③ `backend.go` `buildRequirements` 中 dxToken 产出但结构异常时（如 `dxToken_produced=true` 但 base64 decode 后 JSON 只有少数几个 key、或值全部为 null/0）：
```json
{
  "event": "dx_token_anomaly",
  "detail": {
    "dx_token_prefix": "<dxToken 前 80 字符>",
    "dx_token_len": 8,
    "decoded_keys": ["<JSON key 列表，截断到 10 个>"],
    "proof_key_prefix": "<sourceP 前 50 字符>"
  }
}
```

### 3. `internal/backend/backend.go` — 调用 so_event 记录

**调用点 A** — finalize 响应处理（line 479 附近）：
- 条件：`soToken != ""`
- 调用 `logSOEvent`，按上面定义的 `so_token_present` detail 结构记录

**调用点 B** — `buildRequirements`（line 490 附近）：
- 条件：`dxToken != ""` 且 base64 decode 后 JSON 结构异常（如只有 `"true"`/`"54.72"` 等单个值，或 key 数量 < 5，或所有值均为 null/0）
- 调用 `logSOEvent`，按上面定义的 `dx_token_anomaly` detail 结构记录
- 设计理由：正常产出的 dxToken 是 700+ 字节的 base64，含 30+ 个 `__oai_so_*` key。如果产出的是 `"true"`（8 字节 base64）或 `"54.72"`，说明 VM 走到了 nil fallback 路径但 simWindow 里几乎没有数据——这可能是 XOR 密钥即将失效或指令格式变化的早期信号。

## 部署注意事项

- `data/logs/` 目录已在 `ReadWritePaths` 白名单中（`/opt/chatgpt2api/data`），无需修改 systemd service 配置
- 首次写入时文件自动创建，权限由 `chatgpt2api` 用户继承
- 文件增长缓慢（仅在关键事件时写入，不是每次请求），无需 rotation

## 验证方法

1. `go build ./...` 编译通过
2. `go test ./internal/backend/` 已有测试全绿
3. 部署后 `journalctl -u chatgpt2api -f` 确认每次请求日志量从 ~800 行降到 ~10 行
4. 触发对话后 `cat /opt/chatgpt2api/data/logs/so_events.log` 检查文件是否正常创建
5. 当前环境下 `so_token_present` 预期为 false，文件可能为空——这是正常的。等到某天 OpenAI 开启验证，文件里就会出现记录
