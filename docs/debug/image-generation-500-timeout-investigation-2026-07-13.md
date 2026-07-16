# 生图 500 超时问题排查 — 2026-07-13

> 背景：OpenAI ChatGPT 官网近期有一次较大更新。更新后，项目在服务器上无法正常生图，所有请求 500 超时。

## 现象

### 1. 号池管理页面

所有账号状态异常：
- 要么显示"限流"
- 要么显示"已到重置时间"，时间是昨天
- 看起来没有成功更新过状态

### 2. 生图请求日志

```
Jul 13 17:58:12  responses image upstream request prepared  (model=auto)
Jul 13 17:58:12  sentinel: /req returned 401 — token_invalidated  (best-effort, continuing)
                  ↓
               120 秒无任何日志
                  ↓
Jul 13 18:00:17  新请求 responses image upstream request prepared  (model=auto)
Jul 13 18:00:18  sentinel: /req returned 401 — token_invalidated
```

**关键点**：
- `/req` 的 401 是 best-effort，代码记录日志后继续
- 之后 `/prepare`、`/finalize` 的 sentinelLog 没有出现
- 两次请求之间有 **120 秒的日志空白期**
- 用户最终收到 500 超时

### 3. 蓄水池在运行但

蓄水池调度日志存在，但选的账号要么是"限流"要么刷新后发现 quota=0。
每日自动刷新的 grep 结果为空 — 可能未配置，或未被触发。

### 4. 已验证的事实

| 测试项 | 结果 | 说明 |
|--------|------|------|
| 本机 `/api/auth/session` | 200 OK | session_token 本身有效，返新 access_token |
| 服务器 `/req` | 401 | access_token 在这个端点被拒绝 |
| 服务器 `/prepare` | **未知** | 没有日志，疑似挂死或超时 |
| 每日自动刷新日志 | 空 | 可能未启用 |
| 服务器代理 | 不存在 | 服务器直连 chatgpt |

---

## 系统核心概念

### 三种 Token

| 名称 | 来源 | 用途 | 生命周期 |
|------|------|------|----------|
| `access_token` | 登录获取或 session 刷新 | 作为 Bearer token 调用所有 ChatGPT API | ~10 天 |
| `session_token` | 登录时一起获取 | 当 access_token 过期时，换取新的 access_token | 较长 |
| `sentinel token` (gAAAAAC...) | chat-requirements/finalize 返回 | 每次生图请求时作为 proof token header 发回 | 单次使用 |

### 生图请求完整链路

```
用户请求进入
  │
  ├─ 1. 账号选择 (nextImageAccessToken)
  │     ├─ reserveNextCandidateToken: 遍历账号，跳过 禁用/限流/异常/刷新中/过期待刷新/quota=0
  │     ├─ RefreshAccountState → FetchRemoteInfo: GET /backend-api/me → 刷新远端额度状态
  │     │     ├─ 成功 quota>0 → 正常，账号可用
  │     │     ├─ 成功 quota=0 → 限流，账号被跳
  │     │     └─ 失败 token_invalidated → 过期待刷新 + 异步 session refresh，账号被跳
  │     └─ 找不到可用账号 → "no available image quota"
  │
  ├─ 2. 创建 Client (newImageClient)
  │     └─ NewClient(access_token) — 只传 access_token，session_token 不参与
  │
  ├─ 3. Bootstrap
  │     └─ GET chatgpt.com/ — 获取 PoW 脚本资源
  │
  ├─ 4. Sentinel 握手 (getChatRequirements)
  │     ├─ POST /backend-api/sentinel/req              ← best-effort，401 不阻断
  │     ├─ POST /backend-api/sentinel/chat-requirements/prepare
  │     ├─ PoW + Turnstile + SO 求解 (CPU)
  │     └─ POST /backend-api/sentinel/chat-requirements/finalize → 获取 chat-requirements-token
  │
  └─ 5. 生图 API 调用
        ├─ uploadImage (如有参考图)
        ├─ prepareOfficialImageConversation → POST /backend-api/conversation (prepare)
        └─ startOfficialImageConversation → POST /backend-api/conversation (SSE stream)
```

### Session Refresh（doRefresh）的触发时机

| 触发场景 | 路径 | 同步/异步 |
|----------|------|-----------|
| 请求时发现 token 过期 | RefreshAccountState → FetchRemoteInfo 失败 → ApplyAccountErrorMessage → refreshAccountViaSessionAsync | 异步 goroutine |
| 批量刷新第二阶段 | RefreshAccounts → 发现"过期待刷新" → RefreshToken | 同步串行 |
| 蓄水池补水 | ReservoirCycle → RefreshAccountsSerial → 发现"过期待刷新" → RefreshToken | 同步串行 |
| 每日自动刷新 | StartDailyRefreshWatcher → runDailyAccountRefresh → RefreshAccounts | 同步 |

### 蓄水池（Reservoir）日常做什么

蓄水池不是"刷新全部账号"，而是一个**低速调度器**：

1. 每 45-75 秒评估一次水位
2. 水位不足时最多选 3 个账号刷新（串行）
3. 水位充足时最多选 1 个做维护刷新（间隔 10 分钟）
4. 只刷新符合候选条件的账号：restore_due > unverified > long_unrefreshed > stale_verified > available_unknown_quota
5. 刷新动作本身是 `RefreshAccountState`（拉取远端额度），如果发现 token 过期，再用 session_token 刷新

---

## 可能性分析

### 假设 1：Sentinel 协议有改动（中等概率）

**分析**：`registerSentinelSDK` 硬编码了版本号 `/sentinel/20260124ceb8/sdk.js`。如果 OpenAI 更新后更换了 SDK 版本、PoW 算法或 endpoint 行为，`/req` 返回 401 可能是版本不兼容的信号。同时 `/prepare` 可能也改了格式而返回错误。

**支持证据**：`/req` 明确返回 401 `token_invalidated`，但 `/prepare` 和 `/finalize` 无日志。

**不支持证据**：`/req` 的 401 也可能是单纯因为 access_token 在 sentinel 端点上校验更严格。

---

### 假设 2：IP 被针对（较低概率）

**分析**：服务器直连 chatgpt（无代理），如果频繁请求导致 IP 被标记。

**支持证据**：120 秒空白期，如果 `/prepare` HTTP 调用在服务器上被 TCP 层面拒绝或静默丢弃，就会表现为长超时。

**不支持证据**：`/req` 返回了明确的 JSON 错误（401 `token_invalidated`），而不是 Cloudflare 挑战页或连接拒绝。说明 TCP 连接本身通，问题在应用层认证。

**验证方法**：在服务器上直接测试 `/prepare` 端点。

---

### 假设 3：所有账号认证信息过期（最高概率）

**分析**：每个请求 `/req` 都是 `token_invalidated` — 所有账号的 access_token 在 sentinel 端点都被拒绝了。虽然 `/prepare` 可能仍接受这个 token（就像 `/backend-api/me` 仍能在 FetchRemoteInfo 中成功），但如果 `/prepare` 或 `/finalize` 也拒绝了，生图就无法继续。

如果每日自动刷新没有启用，而蓄水池只刷新远端额度信息不触发 token 刷新，那么 account_token 会在约 10 天后全部自然过期。一旦所有 token 都过期，蓄水池刷新额度也会因为 token 过期而失败，形成"死锁"。

**支持证据**：
- 每日自动刷新日志为空
- 所有账号管理页都是"限流"或"恢复时间昨天"
- `/req` 持续返回 `token_invalidated`

**不支持证据**：
- 本机测试 session_token 可以成功换新 access_token
- 但服务器上没有测试过

---

### 假设 4：/prepare 端点在服务器上超时（新假设，需要验证）

**分析**：120 秒空白期 + 没有任何 `/prepare`/`/finalize` 日志 = `/prepare` 的 HTTP 调用可能在服务器上挂死。这可能是：
- OpenAI 对服务器 IP 在这个端点上有特殊限制（比如要求特定 header）
- `/prepare` 请求体格式变了，服务器返回了无法解析的响应导致 hang
- TCP 连接建立后，响应被中间网络设备丢弃

**验证方法**：直接在服务器上 curl `/prepare`。

---

### 假设 5：账号状态判断导致"无账号可选"（你提出的）

**分析**：`IsImageAccountAvailable` 拒绝了所有 "限流" 和 "过期待刷新" 状态的账号。如果所有账号都处于这些状态，`reserveNextCandidateToken` 返回错误，`runSingleImageOutput` 返回 `"no available image quota"`。

但日志显示 `responses image upstream request prepared`，说明请求已经通过了账号选择阶段，进入了 `getChatRequirements`。所以**账号选择本身没有失败** — 系统选到了一个账号，进入了 sentinel 流程。

---

## 当前最可能的原因

**综合判断**：

1. 蓄水池正常运行，`RefreshAccountState` 把有额度消耗的账号正确标记为"限流"
2. 账号的 access_token 可能已经过期，但 `session_token` 仍有效
3. 请求时第一个 `RefreshAccountState` 失败 → 触发异步 refresh → 但异步 refresh 的结果来得太晚，本请求已经选了一个"过期待刷新"的账号进入 sentinel 流程（通过 `cachedAccountForTransientRefreshError` 的 fallback 路径）
4. 进入 sentinel 流程后，old access_token 在 `/req` 返回 401，在 `/prepare` 很可能也失败（或超时）
5. 120 秒后超时，重试下一个账号，同样结果
6. 所有账号遍历完毕，返回 500

**需要验证的关键点**：服务器上 `/prepare` 端点是否可以正常访问。

---

## 排查过程中的关键 Q&A

### Q1：`POST /backend-api/conversation` 用的是什么 token？

**只用 `access_token`。** 作为 `Authorization: Bearer <access_token>` header 发出。`session_token` 完全不参与任何 ChatGPT API 调用。

`session_token` 的唯一作用是：当 access_token 过期时，作为 cookie 去 `/api/auth/session` 换取新的 access_token。

### Q2：系统如何发现 access_token 过期？都是什么报错？

系统是**被动发现**的 — 只有在实际用这张 access_token 调 API 时，ChatGPT 返回 401，才会知道它过期了。

| 端点 | 过期时的返回 |
|------|-------------|
| `/backend-api/me` (FetchRemoteInfo) | HTTP 401，错误信息包含 `token_expired` 或 `token_invalidated` |
| `/backend-api/sentinel/req` | HTTP 401 `token_invalidated`（你日志里看到的） |
| `/backend-api/sentinel/chat-requirements/prepare` | 未知（服务器上没日志） |

**如果很久没人调用某个账号**（因为蓄水池选了别的号），它的 access_token 在无人知晓的情况下悄悄过期了。等到需要用它的时候才发现。

系统检测到 401 后处理逻辑：
1. 错误信息匹配 `token expired` / `token_invalidated` → `IsAccountTokenExpiredErrorMessage` 返回 true
2. 有 `session_token` → 标记为"过期待刷新"，触发 session refresh
3. 没有 `session_token` → 标记为"异常"，无法恢复

**关键点**：被动发现意味着如果系统长期没触发过 session refresh（异步的失败了、蓄水池没选中、每日的没开），账号就一直挂着过期的 access_token，直到被使用才发现。

### Q3：蓄水池"补水"到底调用什么 API？AT 过期和 quota=0 是什么关系？

**蓄水池补水调用的是 `FetchRemoteInfo`**，它访问：

| API | 作用 |
|-----|------|
| `GET /backend-api/me` | 获取用户邮箱、user_id |
| `POST /backend-api/conversation/init` | 获取 `limits_progress`（含图片剩余额度、重置时间）、默认模型 |

然后根据 `limits_progress` 解析出 `quota` 和 `restore_at`。

**AT 过期 ≠ quota=0，这是两件完全不同的事**：
- AT 过期 → `FetchRemoteInfo` **直接报错**（HTTP 401），不会返回任何数据。进入错误处理 → 标记"过期待刷新"。
- AT 有效但没额度 → `FetchRemoteInfo` **成功返回**，但 `limits_progress` 的 `remaining=0`。系统设置 `quota=0, status=限流`。

**"刷新账号信息和额度"按钮**走的是 `POST /api/accounts/refresh` → `RefreshAccounts`，它是一个完整的两阶段流程：

1. **阶段一**：并发拉取所有账号的远端信息（`FetchRemoteInfo`）— 获取最新额度和状态
2. **阶段二**：对阶段一中发现"过期待刷新"的账号，串行用 ST 刷新 AT（调用 `doRefresh` → `/api/auth/session`）

所以点这个按钮可以同时刷新额度信息**和**恢复过期的 access_token。

### Q4：`/req` 和 `/prepare` 是什么？为什么在生图之前需要它们？

它们是 **Sentinel 协议** 的三个步骤，是 ChatGPT 的反爬/反 bot 安全机制。每次生图请求前必须完成这个握手：

```
Step 0: POST /backend-api/sentinel/req
        └─ 注册 sentinel session
           └─ best-effort 401 不阻断（你日志里的那个）
            ↓
Step 1: POST /backend-api/sentinel/chat-requirements/prepare
        └─ 下发 PoW（Proof of Work）挑战
           └─ 下发 Turnstile 挑战（加密 VM opcode）
           └─ 下发 SO（Session Observer）挑战
           └─ 返回 prepare_token
            ↓
        本地求解：
        ├─ PoW: CPU 运算（min 128ms）
        ├─ Turnstile: 执行 JS 虚拟机 opcode
        └─ SO: 启动 Session Observer 虚拟机
            ↓
Step 2: POST /backend-api/sentinel/chat-requirements/finalize
        └─ 提交 PoW 解答、Turnstile 解答
           └─ 返回 chat-requirements-token (gAAAAAC...)
```

这三步都带 `Authorization: Bearer <access_token>`。如果 access_token 在这些端点上被拒绝，生图就无法继续。

`registerSentinelSDK` 在 `internal/service/register.go:41` 硬编码了 SDK 版本：
```
https://sentinel.openai.com/sentinel/20260124ceb8/sdk.js
```
如果 OpenAI 更新后换了版本，整个 sentinel 流程可能都需要更新。

### Q5：为什么 120 秒之间没有任何日志？

三种可能：

1. **`/prepare` HTTP 调用超时**：TCP 连接建立后，chatgpt 服务器没有返回响应，HTTP client 超时约 120 秒后放弃。
2. **PoW 求解时间过长**：如果 PoW 难度大幅提高（比如 OpenAI 更新后增加了），CPU 求解时间从几百毫秒变成了分钟级。
3. **代码在 `/prepare` 之前就退出了**：不太可能，因为 `upstream request prepared` 日志说明已经进入 `StreamResponsesImageOutputs`。

最可能是选项 1 — 需要在服务器上直接 curl `/prepare` 来验证。

### Q6：access_token 什么时候过期？

access_token 是 JWT，可以解码查看 `exp` 字段：

```bash
# 取 access_token 的第二段（payload），base64 解码
echo "access_token_payload段" | base64 -d 2>/dev/null | python3 -m json.tool 2>/dev/null | grep -E "exp|iat"
```

如果 `exp` 已经过去，说明 token 确实过期了。然后问题就变成：**为什么 session refresh 在服务器上没有成功过**（即使 session_token 是好的）。

---

## 下一步验证计划

1. [ ] 在服务器上直接 curl `/prepare` 端点（需要使用有效或过期的 access_token 都行，先看能否连通）
2. [x] 解码一个 access_token 确认 exp 是否已过去
3. [ ] 在服务器上 curl `/api/auth/session` 测试 session refresh 是否可通
4. [x] 检查每日自动刷新是否配置（`CHATGPT2API_DAILY_ACCOUNT_REFRESH_*` 环境变量）
5. [ ] 关注蓄水池最近一轮调度的诊断面板（前端管理页），看选中、成功、失败情况

---

## 2026-07-13 23:02 请求诊断 — 第二轮深度排查

### 新现象：502 + "no available image quota"

与第一次（18:00 左右）500 超时不同，第二次请求的现象是：

| 字段 | 值 |
|------|-----|
| 错误码 | 502 |
| 错误信息 | `no available image quota` |
| 耗时 | 101.56 s |
| `attempt_count` | **70** |
| 账号调用链 | 8 个账号，全部 `rejected_after_refresh` |

关键特征：
- 70 次尝试 = 8 个账号循环了约 9 轮
- 每个账号在 `RefreshAccountState` 后从"正常/quota>0"变成了"刷新中"
- 因为"刷新中"被 `IsImageAccountAvailable` 拒绝，所以循环选下一

### `rejected_after_refresh` 的代码逻辑

```
reserveNextCandidateToken 选账号
  → RefreshAccountState → FetchRemoteInfo → GET /backend-api/me
    → 返回 401 → ApplyAccountErrorMessage
      → 匹配 "token_invalidated" → IsAccountTokenExpiredErrorMessage = true
        → 有 session_token → 状态设为 "刷新中" → 触发 refreshAccountViaSessionAsync (goroutine)
    → 返回账户状态 "刷新中"
  → IsImageAccountAvailable 检查 → status == "刷新中" → 拒绝
  → 循环下一个
```

所以 **70 次 rejected_after_refresh 不是因为"所有号没额度"**，而是 `FetchRemoteInfo` 调 `/backend-api/me` 时返回了 401 `token_invalidated`。

### 服务器直接测试结果

#### 测试 1：JWT 解码（`access_token` 来自 bryan57@zainy.art）

| 字段 | 值 |
|------|-----|
| `iat` | 2026-07-06 03:39:35 UTC |
| `exp` | 2026-07-16 03:39:35 UTC |
| 距过期 | **还有 2.5 天（未过期）** |
| `jti` | `5241606ae7bb4c0a8c0d03baa9141f7b`（用于判断 token 是否被刷新过） |

#### 测试 2：`curl https://chatgpt.com/backend-api/me` (Bearer token 直接 curl)

**结果：被 Cloudflare JS 挑战页面拦截（不是 401）**

加上完整的 browser headers（origin, referer, sec-fetch-*, user-agent）后仍然被 Cloudflare 拦截。

**但应用层使用 `surf` 库做 TLS 指纹伪装**（`browserHTTPClientForProfile` → `SecureTLS()` + `Impersonate()` → `chrome145` profile），能绕过 Cloudflare 并收到真正的 JSON 错误响应。日志中确认应用层收到了 `{"error":{"code":"token_invalidated","message":"Your authentication token has been invalidated..."}}`。

#### 测试 3：Sentinel SDK 可访问性

```
GET https://sentinel.openai.com/sentinel/20260124ceb8/sdk.js → 200 OK
```

SDK 版本 `20260124ceb8` 仍然可以访问，不是因版本废弃导致的 401。

#### 测试 4：当前数据库账号状态

此刻所有账号状态已恢复为"正常/quota>0"。说明至少 `/backend-api/me` 端点在间歇性可用或异步刷新最终成功。

但 access_token 的 `jti` **与之前完全相同**——证明令牌从未被实际替换过。Session refresh 要么没有成功执行，要么执行了但 OpenAI 没有返回新 token。

#### 测试 5：journalctl 日志空白

23:02–23:20 期间，journalctl **完全没有** `refresh/session/token/error/异常/刷新` 相关日志。这与 70 次尝试、每尝试应触发一次异步 refresh 的预期严重不符。

**可能原因**：
- `refreshAccountViaSessionAsync` 内部的 `IsRefreshing` 去重逻辑：如果第一次尝试触发了异步刷新，后续 69 次尝试看到同一个 token 正在刷新中就直接 return 了
- session refresh 本身的日志没有打到 journalctl（但不是 app 级别的 structured log）

### 当前最可能的根因假设（更新版）

**OpenAI 近期更新后，分层撤销 access_token**：

1. **Sentinel 端点最先被撤销** — `/req`、`/prepare`、`/finalize` 返回 401 `token_invalidated`
2. **`/backend-api/me` 随后也被撤销** — 导致 23:02 的所有账号 `RefreshAccountState` 失败
3. **`/api/auth/session` (session refresh) 可能也受影响** — 异步 refresh 触发但从未真正产生新 token（jti 不变）  
4. **但这些都是间歇性的** — 数据库现在显示"正常"，说明 `/backend-api/me` 之后又恢复了

**矛盾点**：access_token JWT 的 `exp` 字段显示未过期（7 月 16 日），但 OpenAI 返回 `token_invalidated`。这说明 **OpenAI 在后端主动标记 token 无效**，而不是 token 自然过期。可能的触发原因：
- 账号在官网被重新登录导致旧 token 被撤销
- OpenAI 的更新重置了所有旧 token
- IP/设备指纹变更导致的吊销
- Token 的 `auth_time`（2024 年 6 月底的 `pwd_auth_time`）过旧

**另一个矛盾**：如果 session refresh 确实失败了，为什么数据库现在显示"正常"？可能存在一种"反弹"机制：请求时 `/backend-api/me` 返回的是 Cloudflare/错误导致被标记为 token 过期，但蓄水池后续的 `/backend-api/me` 又成功了。

### 等待验证的关键问题

1. **Session refresh 在服务器上是否能真正工作？** — 用 `session_token` 直接 curl `/api/auth/session`
2. **`/prepare` 端点是否能连通？** — 需要 surf-level 客户端而不能直接 curl（会被 Cloudflare 拦截）
3. **为什么 23:02 的请求在 journalctl 无任何日志？** — 18:00 有日志但 23:02 没有（很可疑，可能表示请求在进入 sentinel 流程之前已在应用层出错退出，或者应用进程在 18:00-23:02 之间重启过且某些日志配置改变）
4. **蓄水池是否在干扰排查？** — 它持续刷新账号，可能间歇性恢复又破坏状态

### 建议的排查顺序

1. **先暂停蓄水池** — 消除这一变量，防止账号状态在排查期间变动
2. **让用户在前端点"全部刷新"** — 测试 session refresh 能否返回真的新 token（对比 jti）
3. **解码一个刷新后的 token** — 如果 jti 确实变了，说明 refresh 链路 OK，问题只在 sentinel
4. **如果 refresh 也失败** — 那根本原因是 session_token 也失效了，需要重新导入账号

---

## 2026-07-13 23:20~23:40 Session Refresh 专项排查

### 测试：前端点"刷新账号信息和额度"

**操作**：对 bryan57@zainy.art（正常/quota=25）点单个刷新

**结果**：
- 前端提示：**"刷新成功0个账户"**
- 响应中 bryan57 出现 3 次（1 个正常/成功 + 1 个限流 + ?）
- 其中一份响应包含 `"message": "token刷新成功"`、`"success": true`
- 但解码数据库中的 token 后 **jti 完全未变**

**结论**：响应中的"token刷新成功"是假的——可能是在 `RefreshAccounts` 两阶段流程中 stage 1（FetchRemoteInfo）成功了，但 stage 2（session refresh）没有产出新的 access_token。

### 测试：服务器直接 curl /api/auth/session

**结果**：被 Cloudflare JS 挑战页面拦截（403）。

应用层使用 `surf` 库的 TLS 指纹伪装（`chrome145` profile）来绕过 Cloudflare，纯 curl 无法复现。但 `/backend-api/me`（同样被 Cloudflare 保护）在应用层能正常工作，说明 surf 指纹并非完全失效。

### 全局 token 普查

对所有 510 个账号的 access_token 解码 JWT 并统计 `iat`（签发时间）：

| 指标 | 值 |
|------|-----|
| 总账号 | 510 |
| 已过期 | 272 |
| 有效 | 238 |
| **最新 iat** | **2026-07-10** |
| **今天（07-13）的 iat** | **0 个（零）** |

**iat 分布**：
```
05-23:   1    06-19:  26    06-27:  45    07-03:   1    07-07:  34
05-24:   3    06-22:   2    06-29:  28    07-05:  62    07-10:  87
05-25:   1    06-24:  10    07-01:  29    07-06:  55
06-18:   8    06-25: 118
```

### 历史刷新时间线推断

| 日期 | iat 数量 | 事件 |
|------|----------|------|
| 06-25 | 118 | 大规模批量导入或刷新 |
| 07-05 | 62 | 又一次大规模刷新 |
| 07-06 | 55 | 持续刷新 |
| 07-10 | 87 | **最后一次成功产生新 token** |
| 07-11~13 | **0** | **完全停止，零产出** |

### 定论：Session Refresh 从 7 月 10 日起完全失效

**证据链**：

1. 全部 510 个 token 的 `iat` 均不超过 07-10
2. 近三天（07-11、07-12、07-13）没有一枚新 token
3. 前端刷新操作提示 0 成功
4. 异步 session refresh（请求时 401 触发的 goroutine）从未真正替换过 token（所有 jti 不变）
5. 238 个有效 token（iat 07-05 ~ 07-10）将在未来 7 天内全部过期

**失效原因推测**：

1. **session_token 本身过期** — 最可能。OpenAI 的 session_token 有过期时间（通常数周）。如果所有账号的 session_token 都来自同一批导入，它们可能于 7 月 10 日左右统一过期。

2. **`/api/auth/session` 端点行为改变** — OpenAI 更新后可能加强了对该端点的保护，即使 surf TLS 指纹也无法绕过。

3. **账号的 session_token 在官网被撤销** — 如果账号在 chatgpt.com 上重新登录，旧的 session_token 会立即失效。

**影响范围**：

- 当前 238 个有效 token 最晚 7 月 20 日全部过期
- 届时整个系统的生图能力将完全瘫痪
- 即使现在还能间歇性生图（靠有效的 token 碰运气），sentinel 端点返回 `token_invalidated` 意味着实际生图成功率接近零

**修复方向**：

唯一能真正解决问题的方法是 **重新获取 session_token**。每枚新的 session_token 可以通过以下方式获得：
1. 重新登录 chatgpt.com 获取新的 session cookie
2. 刷新 session_token → 获取新的 access_token
3. 将新账号（含 session_token）导入系统

---

## 关联文档

- **[重复账号与蓄水池暂停后仍活动 — 根因分析与修复计划](duplicate-accounts-and-paused-reservoir-analysis-2026-07-13.md)** — 排查过程中发现的 2 个项目 bug：
  1. `AddAccountRecords` 只按 access_token 去重导致同一邮箱出现多条记录
  2. `PauseReservoirScheduler` 只暂停了定时调度器，每日刷新和手动刷新不受影响
