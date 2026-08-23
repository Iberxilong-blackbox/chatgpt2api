# 账号重新登录 M1 单账号验收成果说明

> 验收日期：2026-08-23
> 状态：M1 单账号邮件 OTP 重新登录及认证 API 验收已完成
> 范围：仅 Ubuntu 服务器上的单账号、串行、可观测运行；不包含 M2 后端接口、前端按钮、批量或异步任务。

## 1. 结论

已在 Desi Ubuntu 服务器完成一次真实的 `@zainy.art` 账号重新登录，并通过完整验收：

1. Chrome 在 Xvfb 中启动并由 Playwright CDP 控制。
2. 登录脚本输入邮箱并提交邮箱所属登录表单。
3. 脚本从 Zoho 邮箱读取 OTP 并提交。
4. 浏览器进入已登录的 ChatGPT 首页。
5. `GET /api/auth/session` 返回新的 `accessToken` 与 `sessionToken`。
6. `GET /backend-api/me` 返回 HTTP `200`。
7. `POST /backend-api/conversation/init` 返回 HTTP `200`。

成功运行目录：

```text
/opt/chatgpt2api/email-relogin/runtime/20260823T-api-validated-retry/
```

成功摘要的关键状态：

```text
status=success
stage=session_fetched
timed_out=false
```

日志只记录 HTTP 状态码、Content-Type 和响应顶层字段名，不记录 Token、Cookie、密码、OTP、代理或响应正文。

## 2. 运行与部署边界

| 项目 | 位置 | 用途 |
|---|---|---|
| Git 源码仓库 | `/root/chatgpt2api` | 接收 `own` 分支的代码同步 |
| 实际运行 bundle | `/opt/chatgpt2api/email-relogin` | 脚本、虚拟环境、私密配置和运行产物 |
| 账号归档输入 | `/opt/chatgpt2api/data/auto_import/imported` | 单账号验证的原始 JSON 来源 |
| 每次运行产物 | `/opt/chatgpt2api/email-relogin/runtime/<run_id>` | 摘要、脱敏日志、失败截图和临时账号副本 |

服务器环境已验证：

- Ubuntu 24.04。
- Google Chrome for Testing 145。
- Xvfb，`DISPLAY=:99`。
- Playwright、`pyotp`、`curl_cffi`、Faker Python 依赖。
- 运行前预检通过。
- 运行结束后临时账号副本删除，CDP `9224` 端口释放。

## 3. 可观测性与失败分类

M1 采用“日志 + 失败截图”，不部署 noVNC。通过 VS Code Remote-SSH 可以直接打开服务器目录中的 `summary.json`、`run.log` 与 `screenshots/`。

当前已能明确区分以下状态：

| 状态 | 含义 | 行为 |
|---|---|---|
| `session_fetched` | 登录、会话与两个认证 API 均验证成功 | 成功 |
| `account_deactivated` | OTP 提交后 OpenAI 明确返回账号已删除或停用 | 保存失败截图；候选加入本地停用排除状态 |
| `external_identity_provider` | 跳转至 Google、Microsoft 或 Apple 登录 | 保存失败截图；当前邮件 OTP M1 不支持该路径 |
| `otp_timeout` | 指定时限内没有收到可用 OTP | 不更新原始账号文件 |
| `timeout` | 整个运行超出 runner 时限 | 不把历史 Token 误报为本次成功 |
| `session_persist_failed` | 登录与 API 验证成功，但安全回写原始 JSON 失败 | 不报告为成功，保留失败原因 |

候选排除状态位于：

```text
/opt/chatgpt2api/email-relogin/runtime/candidate-state.json
```

该文件仅保存候选 JSON 的 SHA-256，不保存邮箱、密码、TOTP、Cookie 或 Token。只有 OpenAI 明确返回 `account_deactivated` 的候选才进入“确认停用”列表；外部身份提供商和其它失败不会被误标为停用。

## 4. 本次修复

### 4.1 邮箱表单提交

问题：未登录 ChatGPT 首页和登录弹窗中都可能存在 `button[type='submit']`。全局选择器可能点击到被弹窗遮挡的聊天输入框提交按钮，导致邮箱没有真正提交。

修复：只在邮箱输入框所属的 `form` 内寻找并提交 `Continue` 按钮；无表单时才对邮箱输入框发送 Enter。移除了全局 `button[type='submit']` 回退。

### 4.2 页面和会话判断

- ChatGPT 首页 URL 不再被视为登录成功的充分条件；检测公开 `Log in` 入口。
- 邮箱提交后记录脱敏响应元数据，并在回到公开首页时截图。
- `/api/auth/session` 缺失 Token 时截图，且只记录安全元数据。

### 4.3 外部身份提供商与摘要准确性

- Google、Microsoft、Apple 跳转被识别为 `external_identity_provider`，避免无效循环和超时。
- 超时、停用和外部认证失败不再把输入 JSON 中的旧 Token 误报为本次成功。
- 运行成功必须同时满足 CLI `[OK]` 标记、结果 JSON 的 `success=true` 和进程正常结束。

### 4.4 认证 API 验证

重新登录后，脚本在同一浏览器认证上下文中执行：

```text
GET  /backend-api/me
POST /backend-api/conversation/init
```

`conversation/init` 使用与现有服务实现一致的请求体：

```json
{
  "gizmo_id": null,
  "requested_default_model": null,
  "conversation_id": null,
  "timezone_offset_min": -480
}
```

只有两个请求都为 HTTP `200`，才会将脚本结果标记为成功。

## 5. 成功后原始 JSON 回写

此前第一次成功验证采用隔离临时副本：浏览器拿到了新 Token，但临时副本在运行结束后删除，原始账号 JSON 没有被更新。

随后已实现并部署“成功后安全回写”规则：

1. 运行开始时记录原始 JSON 的 SHA-256。
2. 仅在登录、会话、`/backend-api/me` 和 `/backend-api/conversation/init` 全部成功后执行回写。
3. 回写前重新比较 SHA-256；若原始 JSON 已被其它进程或人工修改，拒绝覆盖。
4. 使用同目录临时文件和 `os.replace` 原子替换原始 JSON。
5. 保留原文件权限。
6. 运行摘要增加 `source_json_updated`：只有该值为 `true` 才表示新会话已持久化。

本地已覆盖测试“成功原子替换”和“并发修改拒绝覆盖”两种情形。该功能已部署到 Desi，但在本说明形成时，尚未进行一次新的线上成功登录来验收 `source_json_updated=true`；该项应在下一次成功运行时复核。

## 6. 已提交的实现

```text
83-增强重登录会话响应诊断
84-修复重登录未登录状态误判
85-完善重登录页面登录态检测
86-修正未登录页面文本匹配
87-完善邮箱提交后登录诊断
88-修复重登录邮箱提交按钮定位
89-修正停用账号重登录状态摘要
90-完善重登录外部认证状态诊断
91-修正重登录运行结果成功判定
92-验证重登录认证接口可用性
93-持久化已验证的重登录会话
```

## 7. 当前范围与下一步

M1 已完成的边界：

- 单账号、串行邮件 OTP 重新登录。
- 失败日志和截图。
- 停用账号与外部身份提供商分类。
- 会话和服务认证 API 验证。
- 成功后的原始 JSON 安全回写实现。

仍未开始的范围：

- M0：数据库中凭据字段的正式持久化与对外脱敏接口。
- M2：Go 后端 `POST /api/accounts/relogin`、全局串行锁和数据库回写。
- M3：管理页面单账号按钮、权限与交互。
- 批量、异步队列、实时进度和 noVNC。

下一次执行建议先选择一个最近刷新且未被排除的邮件 OTP 候选，运行 `run_single_relogin.py` 后检查：

```text
summary.json: status=success
summary.json: stage=session_fetched
summary.json: source_json_updated=true
```

确认该值后，再进入 M0/M2 的服务侧集成工作。
