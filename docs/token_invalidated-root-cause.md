# token_invalidated 根因分析与解决方案

## 现象

1. 通过浏览器登录 ChatGPT 获取 session JSON（含 `access_token`、`session_token`）
2. 通过号池管理页面的 **Session 导入** 功能导入账号
3. 账号在号池中出现，Diagnose 显示 Bootstrap ✅ + CheckSession ✅
4. 但实际发消息（文本生成）时报 401 `token_invalidated`
5. 错误经过账号错误处理链路 → `IsAccountInvalidErrorMessage` 匹配 → `RemoveInvalidToken` → 账号被永久删除
6. 若多次重试，号池中所有账号逐渐变空

## 根因

### 问题链路

```
用户浏览器正常登录（账号 + 密码）
  → 登录成功后，手动访问 https://chatgpt.com/api/auth/session
  → 从页面 JSON 响应中复制 access_token + session_token + expires + user 等
  → 整理为 JSON 文件（含 fingerprint），放入 auto_import/ 目录
  → 项目启动时自动扫描 → ImportAccountJSONFiles() → AddAccountRecords()

AddAccountRecords() 处理 JSON 时：
  ✅ access_token（顶层）→ 存储为账号主 key
  ✅ fingerprint（顶层）→ prepareAccountFP() → 存储为 fp
  ✅ refresh_token（顶层）→ 存储（但不会用于自动刷新）
  ❌ session_token（顶层）→ 值为 ""，跳过不存储
  ❌ session.sessionToken（嵌套）→ AddAccountRecords 不读取嵌套字段
  ❌ session.accessToken（嵌套）→ AddAccountRecords 不读取嵌套字段

结果：账号没有 session_token，access_token 过期后无法刷新 → 被删除
```

这个问题的根因有两层：

**第一层**：JSON 格式的 `session_token` 在顶层是空字符串，真实值嵌套在 `session.sessionToken` 里，`AddAccountRecords` 只处理顶层字段。

**第二层**：即使 `session_token` 正确存储了，`AddAccountFromSession`（另一种导入路径）会在第 291 行无条件调用 `RefreshSession`，用代理的指纹去刷新 token，导致设备上下文不一致 → `token_invalidated`。

两个路径的问题都要修复。看解决方案章节。

AddAccountFromSession() 第 291 行:
  → refresher.RefreshSession(ctx, A1, S1)
     → 用 s.refresher 的 HTTP 客户端调 GET /api/auth/session
     → 这个 HTTP 客户端是 browserHTTPClient(defaultRemoteProfile)
        → 使用 surf 库的 TLS 指纹 "chrome145"
        → 经过配置的代理 IP
        → OAI-Device-Id / OAI-Session-Id 为自动生成值（非浏览器真实值）
     → OpenAI 检测到 session_token S1 从不同环境被使用
        → IP 不同（代理 vs 浏览器直连）
        → TLS 指纹不同（surf 模拟 vs 真实 Chrome）
        → 设备 ID 不同（自动生成 vs 浏览器真实）
     → 虽然返回新的 access_token A2（替换 A1）
     → 但 session 已被标记可疑/被盗用

后续聊天请求:
  → NewClient(A2) → buildFingerprint() → 从 account 读取 F1
  → 请求 header: OAI-Device-Id=F1.device_id, OAI-Session-Id=F1.session_id
  → ChatGPT 后端检测到 A2 与请求设备不匹配
  → 返回 401 code=token_invalidated → 账号被删除
```

### 关键代码位置

| 步骤 | 文件:行号 |
|---|---|
| Session 导入入口 | `internal/service/account.go:268` |
| 主动 RefreshSession（问题来源） | `internal/service/account.go:296` |
| IsAccountInvalidErrorMessage 匹配 | `internal/service/account.go:1580` |
| RemoveInvalidToken 删除账号 | `internal/service/account.go:1021` |
| 指纹构建流程 | `internal/backend/backend.go:204` |
| prepareAccountFP 处理外部指纹 | `internal/service/account.go:1768` |

### 为什么 diagnose 通过但实际请求失败

`DiagnoseSession` 中的 `CheckSession()` 调用 `GET /api/auth/session`（NextAuth 端点）。这个端点校验的是 NextAuth session 本身是否有效，而 `access_token` 的实际有效性由 ChatGPT 后端 API（`/backend-api/sentinel/chat-requirements` 等）验证，两者的校验逻辑不同。

| 端点 | 行为 |
|---|---|
| `GET /api/auth/session` | 验证 session_token cookie，返回当前 access_token（宽松） |
| `POST /backend-api/sentinel/chat-requirements` | 验证 access_token + device_id + session_id 绑定性（严格） |

### 为什么账号被删除而不是刷新

`token_invalidated` 不等于 `token expired`。错误检测链：

```
auth_chat_requirements failed: status=401, body={"code": "token_invalidated"}

IsAccountTokenExpiredErrorMessage()
  → 检查 "token expired" → 不匹配（文本是 "has been invalidated"）
  → 检查 "authentication token is expired" → 不匹配
  → 返回 false（不过期，不走刷新路径）

IsAccountInvalidErrorMessage()
  → 检查 "token_invalidated" → 匹配!
  → 返回 true → RemoveInvalidToken() → 删除账号
```

`token_invalidated` 被设计为不可恢复的错误（封号/撤销），所以不会尝试用 `session_token` 刷新，而是直接删除。

### ⚠️ "检测到封号" 是真的封号吗？

**不一定是。** 这是代码中的一个误判（false positive）。

当前代码将 `IsAccountInvalidErrorMessage` 匹配的结果一律显示为 `"检测到封号"`（account.go:1084），但这个函数匹配的范围包含了多种情况：

| 错误类型 | 含义 | 实际是否封号 |
|---|---|---|
| `token_revoked` | 账号被主动撤销/封禁 | ✅ 大概率 |
| `invalidated oauth token` | OAuth token 被撤销 | ✅ 大概率 |
| `token_invalidated` | access_token 被废弃 | ❌ **不一定** — 也可能是 token 被轮换或设备不匹配 |
| `authentication token has been invalidated` | 同上 | ❌ 同上 |

**我们的场景**：`token_invalidated` 是因为 `RefreshSession` 使用了与浏览器不同的设备指纹，而非 OpenAI 封禁账号。证据：浏览器端仍可正常对话。

所以"检测到封号"这个文案是**过度诊断**——实际上只是 token 因设备上下文不一致被废弃，账号本身是正常的。

### Token 导入路径为什么不受影响

`POST /api/accounts` 走 `AddAccountRecords`，**不会主动调用 RefreshSession**，而是原样存储传入的 `access_token`、`session_token`、`fingerprint`。当 `access_token` 真正过期时，系统会通过 `session_token` 自动刷新。

| 路径 | 是否 RefreshSession | 是否支持 fingerprint | 是否安全 |
|---|---|---|---|
| Session 导入 `/api/accounts/session` | ✅ 无条件 RefreshSession | ✅ 已支持 | ❌ 导致 token_invalidated |
| Token 导入 `/api/accounts` | ❌ 不刷新 | ✅ 已支持 | ✅ 安全 |

## 解决方案

### 方案（推荐）： `AddAccountFromSession` 跳过 `RefreshSession`

**文件**：`internal/service/account.go`

将 session 导入改为**不主动刷新 token**，直接接受浏览器提供的原始 token：

```go
// 改动前
validated, err := s.refresher.RefreshSession(context.Background(), accessToken, sessionToken)
if err != nil {
    return nil, fmt.Errorf("session token validation failed: %w", err)
}
accessToken = validated.AccessToken
sessionToken = validated.SessionToken

// 改动后
// 跳过 RefreshSession，信任浏览器提供的原始 token
// session_token 已存储，实际过期时会自动刷新
```

核心原则：**信任浏览器的原始 token，不提前干预**。

- 用户在浏览器中已经是登录状态，`access_token` 当前有效
- `session_token` 会被一并存储 → 当 `access_token` 真正过期时（聊天 API 返回 `token_expired`），`IsAccountTokenExpiredErrorMessage` 会匹配 → 自动用 `session_token` 刷新换新 token
- 在 token 仍然有效时主动调用 `RefreshSession` 去换一次新 token，不仅无意义，反而会因为设备指纹不一致导致 `token_invalidated`
- 导入速度更快（省去一次 HTTP 请求）

### 注意事项

- 跳过 `RefreshSession` 意味着导入时不验证 token 有效性。如果用户粘贴了无效的 session JSON，导入仍然成功，但第一次聊天时才会失败。这可以通过前端在粘贴后做一次轻量验证来解决（例如解析 JSON 检查字段完整性）。
- 实际使用中，用户粘贴的是刚刚从浏览器复制的内容，token 有效性能得到保证。

## 涉及文件

| 文件 | 改动 | 关联路径 |
|---|---|---|
| `internal/service/account.go` | `AddAccountRecords` 读取 `session.sessionToken` 等嵌套字段 | auto_import |
| `internal/service/account.go` | `AddAccountFromSession` 跳过 `RefreshSession` | Session 导入对话框 |
| `docs/temp.md` | 更新指纹同步开发计划状态 | — |

## 两个导入路径的问题汇总

| 路径 | 代码入口 | 问题 | 修复 |
|---|---|---|---|
| auto_import（JSON 文件放入目录） | `ImportAccountJSONFiles` → `AddAccountRecords` | 不读取 `session` 嵌套块中的 `sessionToken`/`accessToken`，导致 `session_token` 为空 | `AddAccountRecords` 增加嵌套字段读取 |
| Session 导入对话框 | `AddAccountFromSession` | 第 296 行无条件 `RefreshSession`，用代理指纹刷新导致 `token_invalidated` | 移除 `RefreshSession`，信任原始 token |

## 验证步骤

1. 用 `chatgpt_login.py` 登录生成 JSON（含 `access_token`、`session_token`、`fingerprint.oai_session_id`）
2. 通过 Session 导入到号池
3. 对该账号点「诊断」按钮 → Bootstrap ✅ + CheckSession ✅
4. 发一条消息 → 不再返回 `token_invalidated`
5. 号池中账号数量不变，不会消失
