# 号池账号「重新登录获取凭证」功能开发计划

> 起草日期：2026-08-22
> 状态：规划中（M1 单账号可观测验证优先，尚未编码）
> 范围：仅 Zoho（`@zainy.art`）账号；`@outlook.com` 账号忽略（仅存在于本地开发环境，服务器上无）
> 首期决策：仅验证单账号、同步串行、单临时 JSON 文件；使用 `email-relogin/` 运行 bundle、Xvfb、阶段化日志和失败截图；不部署 noVNC，不实现批量入口。

---

## 一、背景与目标

### 1.1 要解决的现状问题

号池中部分账号刷新后出现：

```
token刷新成功，但账号信息验证失败（阶段: me）:
/backend-api/me failed: HTTP 401, body=Could not parse your authentication token. Please try signing in again.
```

产生位置：`internal/service/account.go:1273`（`refreshAccountsWithWorkerLimit` 阶段二）。

链路：`RefreshToken` 用 `session_token` 调 `GET /api/auth/session` 成功 → 返回「新」`access_token` → 再用该 token 调 `GET /backend-api/me` → 被 OpenAI 以 401 拒绝。

**根因**：`Could not parse your authentication token` 是 OpenAI 上游原文，含义是该 `access_token` 本身已过期/不可解析。说明账号的 `session_token` 在 OpenAI 服务端已失效或被轮换，`/api/auth/session` 刷新返回的「新 token」同样是废的。

**结论**：session 级刷新已不可恢复，**只能走完整浏览器重新登录**（邮箱 + 验证码 + TOTP）拿到全新会话。

### 1.2 已有能力

`email-relogin/email_login_turnstile.py`（从 PRM-mail 筛选、扁平化后的运行 bundle）：

- 对已有账号做**完整浏览器登录**：输入邮箱密码 → 读邮箱验证码（OTP，仅支持 Zoho IMAP）→ 可选 TOTP 挑战 → 抓取 `/api/auth/session`。
- 写回新的 `access_token`、`session_token`、`fingerprint`、`oai_device_id`、`expires`、`session_refreshed_at` 等。
- 单账号模式 `--fingerprint-json <file>`：读指定 JSON，**原地覆盖写回**。
- 批量模式 `--input-dir <dir>`：扫描 `session-*.json`，串行处理，`--on-failure skip` 失败跳过。

### 1.3 目标

在 **chatgpt2api 号池管理页面**：

1. 对**单个账号**提供「重新登录获取凭证」按钮。
2. 支持**批量勾选**多个账号后点「批量重新登录」。
3. 脚本在 **Ubuntu 服务器**上执行（脚本与号池服务同机，推荐拓扑见 §2）。
4. 重登结果**自动导回号池**并更新账号状态，无需人工复制粘贴。

### 1.4 非目标 / 明确排除

- 不改动 `@outlook.com` 账号（它们只在本地开发环境存在，服务器上没有；且 Outlook 验证码无法被脚本的 Zoho IMAP 读取）。
- 不实现「从零注册新账号」。
- 本期不做脚本的深度改造（headless 真支持等列为可选低优先级项，见 §4.3）。

---

## 二、总体架构

### 2.1 部署拓扑（推荐：同机部署）

```
Ubuntu 服务器
├── chatgpt2api/            # 号池服务（Go）+ Web UI + data/chatgpt2api.db
└── email-relogin/          # 单账号重登脚本、依赖模块和服务器私密配置
```

- Go 后端与脚本**同机**，后端通过子进程 `exec.Command` 调用脚本。
- 脚本需要：Chrome（可见窗口或 Xvfb）、Zoho IMAP（`imappro.zoho.com:993`）、可选代理。
- 账号数据以 **chatgpt2api DB 为准**；脚本输入/输出用**临时 JSON 文件**承载，不做双份持久状态。

> ⚠️ 若号池服务与脚本**分机**，本方案需改为「任务队列 + 服务器端 runner 轮询」的桥接架构（见 §7 开放问题）。同机是本方案的默认前提。

### 2.2 一次「重新登录」的数据流

```text
前端按钮（M2 后的单账号入口）
  → POST /api/accounts/relogin  { account_ids: [id] }
  → 后端 AccountService.ReloginAccounts(ids)
     对单个账号（全局串行锁）：
        1) 校验账号存在 + 有 password（无则报「缺少凭据」）
        2) 从 DB 组装临时 session JSON（email/password/totp/fingerprint/access_token/session_token）
           写入 data/relogin/<email>.json  (权限 600)
        3) 调用脚本（带超时，如 240s）：
           python email_login_turnstile.py \
             --fingerprint-json <temp.json> --no-adjust-settings [--proxy ...]
        4) 解析脚本原地写回的文件：success / stage / access_token / session_token /
           expires / fingerprint / error_message
        5) 成功 → UpdateAccount：替换 access_token/session_token/session_expires/fp/email/user_id，
           状态「异常」→「正常」，保留 quota/统计；记录日志
           失败 → 记录错误，状态按错误归类（异常 / 限流 / is-ban）
        6) 删除临时文件
  → 返回逐账号 results：success/error/耗时/新 token 预览
  → 前端用 items 覆盖号池 + toast 汇总
```

### 2.3 关键约束

| 约束 | 说明 | 处理 |
|---|---|---|
| 脚本固定 CDP 端口 `9224` | 同一服务器同一时刻只能跑一个脚本实例 | 后端**全局互斥**，重登串行执行 |
| 每次登录 1~3 分钟 | 首期采用同步请求，只验证一个账号 | 行内 loading；批量、异步任务和实时进度在单账号闭环后再决策 |
| 服务器图形环境 | 首期需要保留浏览器实际页面以支持失败截图排查 | 使用 Xvfb 和非 headless Chrome；headless 只在单账号闭环后另行验证 |
| OTP 只读 Zoho | 非 zainy.art 域收不到验证码 | 本期仅支持 zainy.art 账号 |

---

## 三、功能拆解与关键设计

### 3.1 凭据持久化（前置依赖）

现状：DB 账号 `data` 字段不含 `password`/`totp`。`AddAccountRecords`（`internal/service/account.go:231`）导入时只透传 `email/user_id/chatgpt_account_id/refresh_token/id_token/expired`，**丢弃** password/totp。

改动点：

- `cleanAccountRecords` / `AddAccountRecords` 增加 `password`、`totp` 的透传存储。
- `normalizeAccount` 保留这两个字段（内部使用）。
- 对外序列化安全：
  - `publicAccounts`（`account.go:2227`）是**白名单**，天然不含 password/totp/session_token。✔
  - `redactAccountToken`（`internal/httpapi/routes.go:1363`）追加 `delete(item,"password")`、`delete(item,"totp")` 兜底。
- 前端补充「补录凭据」入口：无凭据账号可手动粘贴 email/password/totp.secret（复用现有 `POST /api/accounts/update` 或新增字段）。

> 只有凭据齐全的账号，「重新登录」按钮才可用。

### 3.2 后端：重新登录执行器

#### 新端点

| 项 | 值 |
|---|---|
| 路由 | `POST /api/accounts/relogin` |
| 权限 | 号池管理组新增权限位 `POST /api/accounts/relogin`（默认 false），见 `internal/service/permissions.go` |
| 入参 | `{ "account_ids": [...] }`（前端只传 ID，后端 `ListTokensByIDs` 映射回 token，复用现有模式） |
| 出参 | `{ "results": [ {account_id, email, success, error, token_preview, duration_ms} ], "items": [...], "total", "failed" }`（沿用刷新接口风格） |

#### 核心方法 `AccountService.ReloginAccounts(ctx, tokens)`

- 全局限量锁（包级 `sync.Mutex` 或 service 字段）保证**同一时刻只有一个脚本实例**。
- 对每个 token：
  1. `GetAccount` 校验存在；`util.Clean(account["password"]) == ""` → 该账号标记「缺少凭据」失败，继续下一个。
  2. 组装临时 JSON（字段与脚本 `--fingerprint-json` 契约一致，见 §4.4）。
  3. `exec.CommandContext(ctx, "python", "email_login_turnstile.py", "--fingerprint-json", tmp, "--no-adjust-settings", "--proxy", proxy)`，工作目录指向 `email-relogin/`；stdout/stderr 采集到 run_id 日志；超时 240s。
  4. 脚本写回临时文件 → `json.Unmarshal`。
  5. 成功（`success==true` 且 `access_token` 非空）：
     - `UpdateAccount` 更新 `access_token`/`session_token`/`session_expires`/`fp`/`email`/`user_id`/`chatgpt_account_id`/`type`。
     - 若原状态为「异常/过期待刷新/刷新中」→ 置「正常」（参考 `isRecoverableSessionImportStatus` 语义）。
     - `RefreshAccountViaSession` 的 token 迁移逻辑（旧 token → 新 token 的预留计数迁移）可复用。
  6. 失败：`recordAccountRefreshFailure` 记录 `last_refresh_error/stage/at`；`is-ban` 情况按封号处理。
  7. 清理临时文件。

> **关键原则：导入重登结果不走 `RefreshSession`**。`AddAccountFromSession`（`account.go:371`）现在会无条件调 `RefreshSession`（`/api/auth/session`），这正是历史上导致 `token_invalidated` 的根因之一（见 `docs/token_invalidated-root-cause.md`）。新流程信任脚本返回的原始 token，直接入库。

#### 批量执行（后续范围，首期不实现）

- 多账号仍须串行，账号间 `delay`（如 5s，参考脚本 `--delay`）降低风控频率。
- 单账号失败不中断整体；汇总逐账号 results。
- 是否改为异步任务、任务轮询和实时进度，必须在 M1 单账号闭环完成后另行决策。

### 3.3 脚本侧要求

#### 复用现有能力（不强制改脚本）

- 单账号：`--fingerprint-json <file>` 原地写回，Go 读回即可，**无需脚本改动**。
- 服务器环境：
  - Ubuntu + Python 3.10+ + `email-relogin/requirements.txt` + `python -m playwright install chromium`。
  - Google Chrome 可执行；无桌面时配 Xvfb，首期不传 `--headless`。
  - `email-relogin/config.jsonc` 或环境变量配好 Zoho IMAP（admin 邮箱/密码）；catch-all 域默认从 admin 邮箱域名推导，也可用 `ZHUCE6_ZOHO_DOMAIN` 覆盖为 `zainy.art`。
  - 代理：`email-relogin/config.json` 的 `proxy.default` 或 `--proxy` 传参。

#### 首期必要脚本小改

- 增加 `run_id`、日志目录和截图目录参数，使 stdout/stderr、结果 JSON 与失败截图落到同一 `runtime/<run_id>/` 下。
- `--recon` 当前会输出输入框值和 Cookie 前缀，首期禁止使用；改造后只能输出字段名、长度和脱敏摘要。
- Ubuntu 启动前以 Linux 方式检查并清理残留的 `9224` Chrome 进程；不依赖脚本中遗留的 Windows `taskkill` 逻辑。

#### 可选脚本小改（单账号闭环后再评估）

- 验证并决定是否支持 headless，决定后才考虑去掉 Xvfb。
- 将脚本内部阶段进一步规范为稳定的结构化事件；当前原地 JSON 回写已足够供 Go 解析。

### 3.4 前端：号池页按钮

文件：`web/src/app/accounts/page.tsx`、`web/src/lib/api.ts`。

- **行内操作**：账号行新增「重新登录」按钮（`lucide` 图标如 `RefreshCw` / `KeyRound`）。
  - 有凭据（前端拿到 `hasCredentials` 字段或后端权限）才显示/可用；无凭据时置灰并 tooltip「缺少密码/TOTP」。
- **首期不做批量入口**；单账号验收完成后，再增加多选行的「批量重新登录」按钮。
- **交互**：
  - 点击 → 行内 loading、禁用重复点击；批量时逐账号更新进度。
  - 调 `web/src/lib/api.ts` 新增 `reloginAccounts(ids)` → `POST /api/accounts/relogin`。
  - 完成 → 用返回 `items` 覆盖号池、派发 `chatgpt2api:quota-refresh`、toast 成功/失败数 + 首个错误（复用刷新接口的 toast 文案风格）。
- **权限**：`hasAPIPermission("POST", "/api/accounts/relogin")` 控制按钮显隐（参考现有 `canImportSessionAccounts` 用法）。
- **安全**：前端不显示明文 password/totp；仅显示「有/无凭据」标记。

### 3.5 安全与脱敏

| 项 | 措施 |
|---|---|
| password/totp 存储 | 与现有 `session_token` 同级信任，明文存账号 `data` JSON |
| 对外泄漏 | `publicAccounts` 白名单 + `redactAccountToken` 双保险剔除 |
| 临时文件 | `data/relogin/` 下，`chmod 600`，处理完删除 |
| 日志 | 不打印明文凭据；token 统一 `AnonymizeToken`；按 `run_id` 记录阶段、耗时、退出码和脱敏错误 |
| 失败截图 | 失败时保存到 `data/relogin-debug/<run_id>/`，目录和文件权限均限制为仅服务用户可读；不保存 HAR、Cookie、完整页面 HTML 或 token 原文 |
| 调试保留 | 调试产物短期保留（建议 24 小时）后清理；成功运行默认不保留截图 |
| 权限 | 新增权限位默认 false；非号池管理员不可调用 |

---

## 四、脚本输入/输出契约（已核对，供实现引用）

### 4.1 输入 JSON（Go 组装，临时文件）

```json
{
  "email": "user@zainy.art",
  "password": "****",
  "totp": { "secret": "BASE32..." },
  "fingerprint": { "selected_os": "windows", "oai_device_id": "...", "oai_session_id": "...", "browser": {}, "codex": {} },
  "access_token": "...",
  "session_token": "..."
}
```

- 必需：`email`、`password`（缺 password 脚本跳过）。
- 可选：`totp.secret`（有则自动填 TOTP；无则走 OTP 邮箱验证码，Zoho 可读）。
- `fingerprint` 保留：脚本登录时会沿用，避免设备上下文漂移。

### 4.2 输出 JSON（脚本原地写回，Go 解析）

关键字段：`success`、`stage`、`access_token`、`session_token`、`expires`、`email`、`password`、`account_id`、`user`、`fingerprint`、`oai_device_id`、`session_refreshed_at`、`error_message`、`is-ban`（封号时）。

- `id_token` 恒为空字符串，不依赖它。
- 失败场景 `success=false`，`stage` ∈ {`no_access_token`, `otp_timeout`, `account_deactivated`, ...}。

### 4.3 批量模式（后续备选：M1 完成后才评估）

```bash
cd /srv/email-relogin
python email_login_turnstile.py \
  --input-dir /srv/email-relogin/accounts \
  --skip-refreshed-hours 0 \
  --no-adjust-settings \
  --on-failure skip \
  --delay 5
```

- 汇总写入 `json2server/re-login-summary-YYYYMMDD_HHMMSS.json`。
- 优于逐账号 `--fingerprint-json` 的场景：一次性处理一大批示选中账号，脚本自身带串行 + 延迟 + 失败跳过。
- 实现取舍：按钮批量可走「Go 生成 N 个临时 JSON → `--input-dir` 指向临时目录 → 解析汇总 + 逐个写回文件」。两套均可，推荐后者（复用脚本成熟的批量调度）。

---

## 五、里程碑与任务拆分

| 里程碑 | 内容 | 产出 |
|---|---|---|
| M0 | 凭据持久化：导入透传 password/totp + 序列化双保险 + 前端补录凭据 | DB 账号可存凭据，API 不泄漏 |
| M1 | 服务器脚本环境跑通（不改 chatgpt2api）：部署 email-relogin bundle、Zoho 私密配置、Xvfb、单账号 `--fingerprint-json` 可观测验证 | 一个明确授权的异常 zainy.art 账号可完成重登并通过号池验证 |
| M2 | 后端执行器：`ReloginAccounts` + `POST /api/accounts/relogin` + 串行锁 + 临时文件 + 解析回写 | 接口级重登可用（可用 curl 验证） |
| M3 | 前端：行内按钮 + 批量按钮 + loading/toast + 权限位 | 页面级闭环 |
| M4 | 端到端验证 + 本计划文档更新为「已实现」 | 全链路可用 |

**建议实施顺序**：M1 先行（纯环境，不动 chatgpt2api 业务代码，能尽早验证「重登产物可用」），随后 M0 → M2 → M3。M1 未完成前，不开始批量、异步任务和页面按钮开发。

### 5.1 M1.0：email-relogin 运行 bundle 与服务器私密配置

首个账号使用仓库根目录的 `email-relogin/`，它是从 PRM-mail 筛选出的扁平化、自包含运行 bundle。不要再复制完整 PRM-mail 工作目录；它包含注册、短信、历史账号、浏览器 profile 和本地调试产物，超出本功能范围。此前建立的 `tools/prm-relogin/` 仅作迁移草案，不是本期运行来源。

| 类别 | 入库内容 | 不入库内容 |
|---|---|---|
| 登录主流程 | `email_login_turnstile.py`、`page_state_matcher.py`、`page_states.json` | `screenshots/`、`output/`、调试日志 |
| Turnstile | `turnstile_controller/`（含 extension） | 浏览器 profile、运行期缓存 |
| 指纹与交互 | `fingerprint.py`、`human_delay.py`、`page_interaction.py`、`human_sim_bundle.js`、`config.json.example` | `config.json`、代理、账号、Cookie、HAR、SMS 文件 |
| Zoho 读取 | `base_mailbox.py`、`constants.py`、`mailbox.py`、`zoho-config.jsonc.example` | `config.jsonc`、`email_history.txt`、邮箱凭据 |
| Python 依赖 | `requirements.txt` | `.venv/`、Playwright 浏览器缓存 |
| 文档与模板 | 本计划和 Ubuntu 单账号 runbook；私密配置的 `.example` 模板 | 真实账号 JSON、密码、TOTP、access/session token |

迁入后的 Python 语法与 JSON 静态检查已通过；仍须在 Ubuntu 实际环境完成依赖导入和 Chrome/Xvfb smoke test。复核 Git diff 中不含凭据、账号标识、Cookie、代理 URL、截图或生成文件。服务器实际运行目录为 `/srv/email-relogin`，其中 `config.json`、`config.jsonc`、`accounts/`、`runtime/`、`debug_output/` 和 `.venv/` 只由服务器侧创建并设置为受限权限；不得通过 Git 同步。

### 5.2 M1.1：Ubuntu 单账号可观测验证

首期以“能够定位失败原因”为验收目标，而不是只以脚本退出码为成功标准。

1. 在 Ubuntu 上以 `Xvfb` 提供固定虚拟显示器；首期不配置 noVNC。脚本从 `/srv/email-relogin` 以非 headless Chrome 模式运行。
2. 每次运行生成唯一 `run_id`，用于关联终端/文件日志、输入临时 JSON、脚本 stdout/stderr、失败截图和最终结果。
3. 日志至少覆盖：`environment_check`、`browser_launch`、`open_login_page`、`submit_email`、`submit_password`、`wait_email_otp`、`submit_totp`、`session_capture`、`pool_validate_me`、`pool_validate_conversation_init`、`success` 或 `failed`。
4. 每个阶段记录时间、耗时、页面 URL/标题（适用时）、脚本退出码和脱敏错误；禁止记录 password、TOTP、Cookie、session_token、完整 access_token 或完整 HTML。
5. 脚本失败时保存页面截图；已有脚本的默认策略是仅保存失败截图，`--debug` 才保存正常流程检查点。首个账号验证不启用 `--debug`，除非失败截图与阶段日志不足以定位问题。
6. 脚本取得会话后，必须以 chatgpt2api 的请求环境执行 `/backend-api/me` 和 `/backend-api/conversation/init`。两者成功才记为 M1 成功；脚本 `success=true` 仅表示浏览器侧会话采集成功。
7. 失败账号和其原有凭据保留，不自动删除；由 `stage`、截图和脱敏错误决定下一次排查动作。需要图形交互仍无法判断时，再按需启用 noVNC，并仅经 VS Code Remote-SSH 端口转发访问。

---

## 六、验证方案

### 6.1 单账号

1. 选中 `gbecker@zainy.art`（DB 已有、有密码 + TOTP）。
2. 点「重新登录」→ 服务器跑脚本 → 成功后账号 `access_token`/`session_token` 更新，状态从「异常/正常」保持「正常」。
3. 点号池「刷新」→ 不再出现「token刷新成功，但账号信息验证失败（阶段: me）」。
4. 发一条消息 → 200 正常。

### 6.2 批量

1. 多选若干 zainy.art 账号 → 「批量重新登录」→ 逐账号成功/失败汇总。
2. 全部成功后统一刷新验证；失败账号的错误信息可读（OTP 超时 / 封号 / 缺凭据）。

### 6.3 异常与安全

- 无凭据账号按钮置灰 / 调用返回「缺少凭据」。
- 两台并发点击 → 串行锁兜底，不出现脚本冲突（CDP 9224）。
- 重登期间号池其它功能（文本/图片）不受影响（重登串行 + 与其它刷新路径隔离）。
- 检查 `/api/accounts`、刷新返回的 `items`、日志中均无明文 password/totp。

---

## 七、风险与开放问题

| # | 问题 | 影响 | 建议 |
|---|---|---|---|
| 1 | **服务器上是否已部署 chatgpt2api？** 同机是本方案前提 | 分机需改桥接架构（任务队列 + runner） | 确认生产拓扑；若分机，另出「relogin-agent」设计 |
| 2 | 服务器显示环境（Xvfb/noVNC） | 首期以失败截图排障，需要可见 Chrome 的虚拟显示器 | 部署 Xvfb；仅当日志与截图不足时，再通过 VS Code Remote-SSH 端口转发启用 noVNC |
| 3 | Zoho IMAP 凭据在服务器上的配置（admin 邮箱/密码/catch-all） | 读不到验证码则登录失败 | 在 `/srv/email-relogin/config.jsonc` 配置或使用环境变量 |
| 4 | 部分账号可能已启用 TOTP 但 DB/源文件未存 secret | 登录卡 TOTP 挑战 | 补录凭据入口；跑单账号时人工确认 |
| 5 | 无 TOTP 账号依赖 Zoho OTP | 依赖 Zoho IMAP 稳定 | 批量间隔 `--delay` 降低风控 |
| 6 | 重登期间号池自动刷新/补水可能同时动同一账号 | 状态竞争 | 重登期间对目标账号加锁或标记「重登中」 |
| 7 | headless 行为未完成实测 | 若省略 Xvfb，失败页面缺乏可视证据 | 首期固定 Xvfb + 非 headless；M1 成功后单独评估 headless |
| 8 | outlook 账号在 DB 中但服务器无 | 点按钮会失败 | 前端/后端仅对 zainy.art（或 DB 有凭据）账号开放按钮 |
| 9 | 超时/异常中断后的临时文件残留 | 磁盘残留 | 启动清理 `data/relogin/` 旧文件 |

---

## 八、涉及文件清单（实现期参考）

| 文件 | 改动 |
|---|---|
| `internal/service/account.go` | `cleanAccountRecords`/`AddAccountRecords` 透传 password/totp；新增 `ReloginAccounts`；`isRecoverableSessionImportStatus` 复用 |
| `internal/service/session_refresher.go` | 不动（新流程绕过 RefreshSession） |
| `internal/service/permissions.go` | 新增 `POST /api/accounts/relogin` 权限位 |
| `internal/httpapi/routes.go` | 新路由 + `redactAccountToken` 兜底删 password/totp |
| `internal/storage/storage.go` | 不动（账号为 JSON 文档，天然可存新字段） |
| `web/src/lib/api.ts` | `reloginAccounts(ids)` |
| `web/src/app/accounts/page.tsx` | 行内/批量按钮 + loading/toast + 权限控制 |
| `web/src/app/accounts/components/account-import-dialog.tsx`（或 update 弹窗） | 凭据补录入口 |
| `data/relogin/` | 临时 JSON 工作目录（不入库） |
