# 生图调用可观测性改造：当前状态与后续计划

> 记录日期：2026-07-13  
> 状态：实施中（尚未形成可用的端到端功能）

## 目标

让管理员能够从一条图片调用日志中追溯：请求使用了哪些账号、每次选号/刷新前后的额度状态、是否试用了未知额度账号、失败发生在哪个阶段，以及最终的上游错误。

原则：

- **KISS**：一次业务调用只保存一条结构化调用链日志，不引入独立追踪服务。
- **YAGNI**：本期只覆盖图片调用；文本调用和 token 刷新在图片链路稳定后复用同一模式。
- **DRY**：账号选择事件由 `AccountService` 统一生成，HTTP 层只负责写入最终日志。
- **SRP**：协议层采集调用事件；HTTP 层落库；前端只负责检索与展示。
- **安全**：日志只记录稳定账号 ID，绝不记录 access token 或 session token。

## 当前已完成

已新增但尚未接入的基础数据结构：

1. `internal/protocol/image_call_trace.go`
   - `ImageCallTrace`：单次生图调用的线程安全追踪容器。
   - 包含 `trace_id`、`attempt_count` 和 `account_attempts`。
   - 明确不保存 access token、session token。

2. `internal/service/account_trace.go`
   - `ImageAccountSelectionEvent`：描述单个候选账号的刷新前后状态。
   - 字段包括稳定账号 ID、状态、额度、`image_quota_unknown`、蓄水池层级和错误文本。
   - `AccountIDFromToken` 统一生成脱敏稳定 ID。

## 当前未完成

上述两个文件目前没有被实际调用；因此当前系统行为、日志输出和前端页面尚未变化。

| 项目 | 状态 | 说明 |
| --- | --- | --- |
| 账号选择前后快照 | 未接入 | 需在 `GetAvailableAccessTokenFor` 的刷新循环中采集。 |
| 协议层上游尝试事件 | 未接入 | 需覆盖选中、上游开始、成功、限流、失败、重试。 |
| trace 注入 | 未接入 | HTTP 请求和异步创建任务均需创建并向 protocol payload 传递。 |
| 业务日志写入 | 未接入 | `logCall` 需合并 trace 的结构化字段。 |
| 日志筛选 | 未接入 | 增加 `trace_id`、`account_id`、错误阶段筛选。 |
| 前端详情时间线 | 未接入 | 日志详情应展示每个 attempt 的账号与前后状态。 |
| 测试与构建 | 未执行 | 当前未应宣称可用。 |

## 后续实施顺序

1. 在 `AccountService` 增加可选观察回调。
   - 保存候选账号的刷新前快照。
   - 执行远端刷新后记录刷新后快照。
   - 对 `selected`、`selected_cached`、`rejected_after_refresh`、`refresh_failed` 生成事件。
   - 重点验收：账面正额度刷新后变为 `quota=0` 时，单条事件同时包含前后值。

2. 在 `protocol.Engine` 接入 `ImageCallTrace`。
   - 每次上游尝试记录 `upstream_started`。
   - 成功记录 `upstream_succeeded`；错误记录 `upstream_failed`。
   - 记录选号来源：会话固定账号或普通号池。
   - 重点验收：`quota_unknown_before=true && quota_before=0` 的账号被选中时可见。

3. 在 HTTP 与异步创建任务入口创建 trace。
   - 同步接口：`/v1/images/generations`、`/v1/images/edits`。
   - 异步接口：`/api/creation-tasks/image-generations`、`/api/creation-tasks/image-edits`。
   - 在最终 `logCall` 中附加 `trace_id`、`attempt_count`、`account_attempts`。

4. 扩展日志查询与前端。
   - 后端 `LogQuery` 增加 `trace_id`、`account_id`。
   - 日志列表增加“最终账号 / 尝试次数 / 调用链”摘要。
   - 详情弹窗增加按时间顺序的调用链区块。
   - 初期采用现有管理台的紧凑、信息密度优先风格，不单独新增仪表盘。

5. 测试与验证。
   - 账号服务：未知额度候选、远端刷新归零、刷新错误回退。
   - protocol：多账号重试与上游错误事件。
   - httpapi：最终业务日志包含 trace 字段，且不含敏感 token。
   - 执行 `go test ./...` 与 `cd web && npm run build`。

## 可验收结果

对任意一条后续图片调用，管理员应能回答：

1. `trace_id` 是什么？
2. 选中了哪些账号，顺序如何？
3. 该账号在刷新前后是何种状态、额度是多少、是否未知额度？
4. 失败处于选号、刷新还是上游调用阶段？
5. 502 是上游网关错误，还是前置账号额度/限流问题？

