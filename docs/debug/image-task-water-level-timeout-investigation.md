# 图生图水位、超时与账号调用链排查记录

> 记录日期：2026-07-13  
> 状态：已获得初步调用链证据，正在定位服务器到 ChatGPT 的刷新连通性问题。

## 现象

管理页蓄水池曾显示：

- 水位约 `2.3k / 200`；
- `213` 个“有额度可用”账号；
- 调度器处于“维护中”。

但异步图生图任务仍会失败，且常在约 5 分钟后超时。任务提交接口日志为：

```text
POST /api/creation-tasks/image-edits
200
约 0.03 秒
```

这只表示任务已成功入队，不表示图片已生成成功。

## 水位的实际含义

当前 `currentWater` 由本地账号记录计算：状态正常、验证信息未过期、且本地 `quota > 0` 的账号额度相加。

因此水位是“最近一次本地确认的账面额度”，不是 ChatGPT 上游的强一致实时余额，也不会扣除正在执行的图片任务已经预留的并发槽位。

不能将 `水位 2.3k` 解释为“此刻必然有 2300 次请求可以立即成功”。

## 已取得的调用链证据

一次失败图生图任务中出现以下类型的事件：

### 1. 远端刷新超时

```text
refresh_failed
Get "https://chatgpt.com/": context deadline exceeded
```

刷新前后本地状态可能仍为“正常 / 额度 5、10、25”。这证明本地缓存没有立即归零，但服务器无法在请求时成功向 ChatGPT 验证该账号。

### 2. access token 已失效

```text
auth_chat_requirements/prepare failed: status=401
token_invalidated
```

这类账号的 access token 已失效。若有有效 session token，可尝试刷新；否则应退出正常候选池并重新导入或人工处理。

### 3. 账号处于刷新中

```text
刷新前：正常 / 额度 25
刷新后：刷新中 / 额度 25
```

这不是额度耗尽。它表示该账号在本次选号期间被其他刷新流程占用，例如蓄水池调度、另一条图片任务或并发账号刷新；当前任务不会重复使用它。

### 4. 刷新后仍为正常但被拒绝

```text
rejected_after_refresh
刷新前：正常 / 额度 25
刷新后：正常 / 额度 25
```

当前诊断尚未记录精确拒绝原因。可能原因包括：图片并发预留槽位不可用、刷新失败后缓存回退条件不满足，或检查期间账号状态被并发流程改变。

## 为什么常在 5 分钟后失败

异步图片任务默认超时是 `300` 秒，由 `image_task_timeout_seconds` 控制（可配置范围 30–3600 秒）。

当前可能的执行链路为：

```text
从本地候选池选账号
→ 刷新 ChatGPT 远端账号信息
→ 远端连接超时 / token 无效 / 账号正在刷新
→ 换下一个候选账号
→ 多次失败后耗尽 300 秒任务上下文
```

因此最终 `context deadline exceeded` 或 `no available image quota` 的含义更接近：

> 在该任务的时间窗口内，没有找到可成功验证且可立即调用的账号。

它不等价于本地账面额度已全部消耗。

## 推荐排查顺序

1. 检查服务器到 `https://chatgpt.com/` 的连通性与代理出口：代理可用性、IP 风控/限速、TLS/HTTP2/uTLS 握手及连接超时。
2. 按日志中的 `account_id` 检查反复出现 `token_invalidated` 的账号；有 session token 则验证刷新，无则标记异常或重新导入。
3. 观察是否多个任务同时运行，确认是否存在大量图片 reservation 槽位占用。
4. 临时将 `image_task_timeout_seconds` 从 300 提高到 600 秒，用于观察；这不是根因修复。
5. 查看创建任务最终状态（`queued`、`running`、`success`、`error`）及其 `error`、`started_at`、`finished_at`，不要以提交接口的 200 日志判断任务成功。

## 后续诊断改进建议

为 `rejected_after_refresh` 增加明确的 `rejected_reason`，候选值建议为：

```text
refresh_in_progress
no_reservation_slot
cached_fallback_not_allowed
no_longer_image_candidate
```

这样可直接区分：账号刷新占用、图片并发槽位不足、缓存回退限制和账号状态变化。
