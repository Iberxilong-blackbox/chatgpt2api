# 每日自动刷新账号额度计划

> 目标：用一个简单、可理解的后台定时任务，自动执行现有“一键刷新额度”逻辑，降低账号 access token 过期后首次对话/生图失败的概率。
> 计划日期：2026-06-22

## 背景

当前账号刷新主要依赖三类触发：

1. 管理员在账号页手动点击“一键刷新额度”。
2. 后台 `StartLimitedWatcher` 定时扫描 `限流` 且 `restore_at` 已到期的账号。
3. 对话或生图请求过程中，上游返回 token expired 后触发 session token 刷新。

这套机制能处理已发现的问题，但对“所有 access token 在一天后同时过期”的场景不够主动：第一次真实请求可能先失败，刷新完成后第二次才恢复。为保持 KISS，本轮先不改复杂请求链路，而是增加每日一次的自动刷新任务，行为等同于管理员每天在低峰区间点击一次“一键刷新额度”。

## 目标

- 默认开启每日自动刷新账号。
- 默认每天在 `04:00` 到 `05:00` 区间内随机执行一次全量账号刷新。
- 前端设置页新增一个开关按钮，用于启用/禁用每日自动刷新。
- 后端复用现有 `AccountService.RefreshAccounts(ctx, accounts.ListTokens())`，不复制刷新逻辑。
- 自动刷新完成后写入日志，便于排查刷新数量、失败数量和耗时。

## 非目标

- 不在本轮实现“请求时无可用账号后同步刷新少量账号”的兜底。
- 不新增复杂调度系统、任务队列或多时间段配置。
- 不为每个账号单独配置刷新时间。
- 不主动刷新无 `session_token` 的账号；这类账号仍依赖重新导入有效 session。

## 配置设计

新增配置项：

| 配置键 | 环境变量 | 默认值 | 说明 |
| --- | --- | --- | --- |
| `daily_account_refresh_enabled` | `CHATGPT2API_DAILY_ACCOUNT_REFRESH_ENABLED` | `true` | 是否启用每日自动刷新 |
| `daily_account_refresh_start_time` | `CHATGPT2API_DAILY_ACCOUNT_REFRESH_START_TIME` | `04:00` | 每日刷新开始时间，格式 `HH:mm` |
| `daily_account_refresh_end_time` | `CHATGPT2API_DAILY_ACCOUNT_REFRESH_END_TIME` | `05:00` | 每日刷新结束时间，格式 `HH:mm` |

前端设置页：

- 在“基础参数”区域新增“每日自动刷新账号”开关，默认打开。
- 新增“每日刷新区间”输入项，默认 `04:00` 到 `05:00`。
- 保存后写入 `.env`，与现有设置项保持一致。

## 时区规则

每日刷新区间按 **服务器进程本地时区** 解释，并在区间内随机选择一个执行时间。

具体表现：

- 如果直接在中国时区服务器运行，`04:00` 到 `05:00` 就是服务器的 `Asia/Shanghai` 04:00 到 05:00。
- 如果在 Docker 中运行，容器默认时区可能是 UTC，那么 `04:00` 到 `05:00` 就是 UTC 04:00 到 05:00，也就是北京时间 12:00 到 13:00。
- 若希望固定为北京时间，应在部署环境设置容器/进程时区，例如 `TZ=Asia/Shanghai`。
- 如果结束时间小于等于开始时间，视为跨天区间，例如 `23:00` 到 `02:00` 表示从当天 23:00 到次日 02:00。

查看时区命令：

```bash
# Linux 服务器
date
timedatectl
cat /etc/timezone

# Docker 容器内
docker exec -it <container_name> date
docker exec -it <container_name> sh -c 'echo $TZ'
docker exec -it <container_name> sh -c 'cat /etc/timezone 2>/dev/null || true'
```

Windows / PowerShell：

```powershell
Get-TimeZone
Get-Date
```

本轮计划不新增独立 timezone 配置，避免引入额外复杂度。设置页文案需要明确提示“按服务器本地时区执行”。

## 后端实现计划

### 1. 配置层

在 `internal/config/config.go` 增加两个配置项：

- `DailyAccountRefreshEnabled() bool`
- `DailyAccountRefreshStartTime() string`
- `DailyAccountRefreshEndTime() string`

校验规则：

- enabled 使用现有 bool 解析方式。
- start/end 接受 `HH:mm`，非法值分别回退到 `04:00` / `05:00`。

### 2. 定时任务

新增一个轻量 watcher，例如：

```go
accounts.StartDailyRefreshWatcher(ctx, cfg)
```

核心行为：

1. 启动后在当前或下一次刷新区间内随机计算一个执行时间。
2. 到点后读取当前所有账号 token。
3. 调用 `RefreshAccounts(ctx, tokens)`。
4. 写入日志：
   - 总账号数
   - `refreshed`
   - `session_refreshed`
   - `failed`
   - `duration_ms`
5. 计算下一天刷新区间内的随机执行时间。

实现约束：

- 若账号数为 0，直接跳过并记录简短日志或不记录。
- 不与现有手动刷新逻辑重复实现。
- 避免并发重叠：如果上一次每日刷新仍在运行，下一次触发应跳过。
- 使用服务进程本地时间 `time.Local` / `time.Now()` 计算下一次执行时间。
- 最多每分钟重新读取一次配置，前端保存开关或区间后无需重启服务。

### 3. 启动入口

在 `internal/httpapi/app.go` 中，和 `StartLimitedWatcher` 同层启动每日刷新 watcher。

现有逻辑：

```go
accounts.StartLimitedWatcher(ctx, time.Duration(cfg.RefreshAccountIntervalMinute())*time.Minute)
```

新增逻辑放在附近，保持账号后台任务集中。

## 前端实现计划

### 1. API 类型

在 `web/src/lib/api.ts` 的配置类型中加入：

- `daily_account_refresh_enabled?: boolean`
- `daily_account_refresh_start_time?: string`
- `daily_account_refresh_end_time?: string`

### 2. 设置 store

在 `web/src/app/settings/store.ts` 中加入状态映射和 setter：

- `setDailyAccountRefreshEnabled(value: boolean)`
- `setDailyAccountRefreshStartTime(value: string)`
- `setDailyAccountRefreshEndTime(value: string)`

保存配置时随现有 `saveConfig` 一起提交。

### 3. 设置页 UI

在 `web/src/app/settings/components/config-card.tsx` 的“基础参数”区域新增：

- 一个开关：每日自动刷新账号
- 两个时间输入：每日刷新开始/结束时间
- 提示文案：每天在区间内随机执行一次；按服务器本地时区执行；Docker 默认可能是 UTC，建议设置 `TZ=Asia/Shanghai`

按钮/开关默认打开，符合用户预期。

## 可见行为

每日刷新成功后，账号数据会更新：

- `status`
- `quota`
- `image_quota_unknown`
- `restore_at`
- `session_expires`
- access token/session token

前端账号页当前不会收到后台任务推送。用户需要刷新账号列表、重新进入账号页，或后续增加轻量轮询，才能看到最新“恢复时间”和额度。

本轮不强制增加账号页轮询，避免额外噪声。

## 测试计划

### 后端

- 配置默认值测试：
  - 默认 enabled 为 `true`
  - 默认 start/end 为 `04:00` / `05:00`
  - 非法 start/end 分别回退 `04:00` / `05:00`
- 下一次随机执行时间计算测试：
  - 当前时间早于区间，落在当天区间内
  - 当前时间在区间内，落在剩余区间内
  - 当前时间晚于区间，落在次日区间内
  - 跨天区间能正确识别午夜后的剩余窗口
- watcher 行为测试：
  - 到点后调用一次 `RefreshAccounts`
  - 无账号时不报错
  - 上一次刷新未完成时不并发执行

### 前端

- `npm run build`
- `npm run lint`
- 设置页能展示默认开启状态和 `04:00` 到 `05:00`
- 修改开关和区间后，保存请求包含新配置项

## 风险与处理

| 风险 | 处理 |
| --- | --- |
| 账号很多时每日刷新上游请求量较大 | 默认低峰区间 04:00-05:00 随机执行；复用现有 RefreshAccounts 最大 10 worker |
| Docker 时区不是北京时间 | 文档和 UI 提示按服务器本地时区执行；部署时设置 `TZ=Asia/Shanghai` |
| 每日任务失败后首个真实请求仍可能失败 | 本轮接受；后续可补“请求时少量同步刷新”兜底 |
| 配置修改后任务时间是否立即生效 | watcher 最多每分钟重新读取一次配置 |

## 原则应用

- **KISS**：复用现有“一键刷新额度”逻辑，只增加一个每日调度入口。
- **YAGNI**：暂不做多时间段、按账号配置、独立时区配置和请求链路大改。
- **DRY**：不复制 `RefreshAccounts` 的刷新流程，避免手动刷新和自动刷新行为分叉。
- **SOLID**：配置读取、定时调度、账号刷新职责分离；后续若增加请求兜底，也可独立扩展。

## 后续建议

1. 先实现每日自动刷新并上线观察。
2. 如果仍出现“任务未跑成功导致首请求失败”，再增加请求级少量同步刷新兜底。
3. 如果用户经常打开账号页观察状态，可再给账号页增加低频手动/自动刷新提示。
