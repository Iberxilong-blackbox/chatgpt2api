# 对话中 401 token_expired 自动刷新与透明重试

> 起草日期：2026-06-08
> 状态：代码已完成，待手动验证（Step 1.2）

---

## 一、问题描述

当前对话遇到 `token_expired`（401）时，行为是：

```
对话请求 → 401 token_expired
  → markTextTokenExpiredForRetry
    → IsAccountTokenExpiredErrorMessage 匹配 ✅（已修复）
    → HandleTokenExpiredOnRequest
      → session_token 有值 → 状态设为"刷新中"
      → refreshAccountViaSessionAsync（后台异步刷新，fire-and-forget）
      → 返回 ("", true)
    → 将当前 token 加入 exhaustedTokens
    → 循环换另一个账号的 token 重试
```

**问题：** 当前对话**不等刷新完成**，直接换别的账号。用户视角：
1. 如果池中还有其他可用账号 → 用别的账号回复，当前账号的刷新对本次对话无感
2. 如果池中没有其他可用账号 → 最终返回 401 错误给用户
3. **无论哪种情况，已过期账号第一个 401 报错必然发生**

### 用户期望

当对话遇到 `token_expired` 时：
1. 自动用 `session_token` 刷新获取新 `access_token`
2. 用新 token **立即重试本次对话**
3. 如果刷新成功+重试成功 → 用户只感受到轻微延迟，看不到 401
4. 如果刷新失败 → 走当前逻辑（换号或报错）

---

## 二、设计原则

本方案遵循两条核心原则：

### 原则一：避免短期内大量重复刷新，防止触发风控

Session refresh 本身就是敏感操作。宁可手动刷新，也不要因为自动刷新逻辑触发 OpenAI 的风控导致账号被标记。具体措施：

- **单账号冷却**：同一账号刷新后 5 分钟内不再自动刷新
- **不主动刷新**：如果有其他可用账号，优先换号，不触发刷新
- **同步刷新仅作最后手段**：只在没有其他可用账号时，才同步等待刷新结果（避免用户看到 401 报错）

### 原则二：在满足原则一的前提下，适度推进自动化

- 优先补全 watcher 兜底机制（批量和定时，风险最低）
- 同步刷新降级为"救急"手段而非默认行为
- 实施顺序：watcher 兜底 → 保守同步刷新

---

## 三、现状分析

### 3.1 已有去重机制

`SessionRefresher`（`session_refresher.go`）已经实现了**按 access_token 去重**：

```go
// RefreshToken 内部调用 RefreshSession
// 如果同一 access_token 已有 in-flight 的刷新请求，
// 后续调用者会等待同一个结果返回，不会重复发起 HTTP 请求
```

`refreshAccountViaSessionAsync` 开头也有 `IsRefreshing` 检查。

### 3.2 对话重试机制

`streamTextDeltasWithTokenRetry` 和 `collectVisionTextWithTokenRetry` 中的循环：

```go
for attempt := 0; attempt < MaxTokenSwitchAttempts; attempt++ {
    client, token := textBackendWithRetry(exhaustedTokens)
    text, err := CollectVisionText(ctx, client, ...)
    if err != nil {
        if !markTextTokenExpiredForRetry(token, err, exhaustedTokens) {
            return "", err  // 不重试，返回错误
        }
        // 重试（换另一个 token）
    }
}
```

`markTextTokenExpiredForRetry` 返回 `true` → 当前 token 被耗尽 → 循环取下一个 token。

### 3.3 缺少的环节

| 环节 | 现状 | 需要 |
|------|------|------|
| 刷新同步性 | 异步（fire-and-forget） | 仅当无其他可用账号时，等待刷新完成再决定是否重试 |
| 同号重试 | 换另一个 token | 刷新成功后**重试同一账号** |
| 刷新频率控制 | 无 | 单账号 5 分钟内不重复刷新 |

---

### 3.4 "过期待刷新"无兜底机制

**现状：** `StartLimitedWatcher` 每 5 分钟扫描一次账号池，但只对 `status == "限流"` 且 `restore_at` 已过的账号调用 `RefreshAccounts`：

```go
func (s *AccountService) listRefreshableLimitedTokens(now time.Time) []string {
    for _, item := range s.items {
        if item["status"] != "限流" {   // ← 只处理"限流"
            continue
        }
        // ...
    }
}
```

`status == "过期待刷新"` 的账号不在扫描范围内。同时 `filterNonFreeLocked` / `filterFreeLocked` 也排除了 `"过期待刷新"` 和 `"刷新中"` 账号，导致它们不会被选入对话池。

**问题链路：**

```
对话 401 → ApplyAccountErrorMessage(event="refresh_accounts")
  → 设置 status="过期待刷新"（不异步刷新）
  → 等待 RefreshAccounts 第二阶段来处理它

但是：
  - 没有定时任务去刷"过期待刷新"账号
  - 对话循环（exhaustedTokens）跳过它选别的号
  → 账号卡在"过期待刷新"状态，无人处理
```

用户如果只看号池面板，这个账号会一直挂着"过期待刷新"，直到手动点单行刷新。

**已实现对策：** `listRefreshableLimitedTokens` 扫描范围已扩展至 `"过期待刷新"` 账号，同时 `restore_at` 检查仅对 `"限流"` 账号生效：

```go
if item["status"] != "限流" && item["status"] != "过期待刷新" {
    continue
}
// ...
if item["status"] == "限流" {
    if restoreAt, ok := parseAccountRestoreAt(item["restore_at"]); ok && restoreAt.After(now) {
        continue
    }
}
```

这样 `RefreshAccounts` 第二阶段会对它们做串行 session refresh。5 分钟间隔 + 串行处理，频率很低，不会触发风控。

**暂不覆盖** `"刷新中"` 状态。如果出现刷新中卡死的情况，先人工排查根因，再决定是否需要兜底。

---

## 四、方案设计

### 4.1 整体策略

```
对话请求 → token_expired
  ├─ 有其他可用账号？
  │   ├─ 是 → exhaust 当前 token → 换号重试
  │   │       → 异步触发刷新当前号（HandleTokenExpiredOnRequest，fire-and-forget）
  │   └─ 否 → 尝试同步刷新（最后手段）
  │           ├─ 冷却检查（5min 内未刷新过）→ 通过？
  │           │   ├─ 是 → 同步等待刷新（超时 15s）
  │           │   │   ├─ 成功 → 用新 token 内联重试本次对话
  │           │   │   │   ├─ 重试成功 → 返回结果
  │           │   │   │   └─ 重试失败 → exhaust，继续循环（换号或结束）
  │           │   │   └─ 失败 → exhaust，继续循环（换号或结束）
  │           │   └─ 否（冷却中）→ exhaust，继续循环
  │           └─ 无 session_token → exhaust，继续循环
  └─ 无 session_token → exhaust，继续循环
```

关键设计决策：
- **有其他号时绝不刷新**（原则一：减少不必要的刷新请求）
- **同步刷新只在用户即将看到 401 时才触发**（原则二：适度自动化）
- **5 分钟冷却**确保即使连续触发 401，也不会频繁刷新（原则一）
- **SSE 流式场景不做同步刷新**（已发增量数据无法撤回）

### 4.2 已实现改动

#### 文件：`internal/service/account.go`

**新增字段：** `lastRefreshAttempt map[string]time.Time` 到 `AccountService` 结构体，用于冷却跟踪（内存存储，不持久化）。

**新增方法：`TrySyncRefresh(accessToken string) (string, bool)`**

返回 `(newAccessToken, true)` 表示刷新成功，调用方使用新 token 重试；返回 `("", false)` 表示账号不存在、无 session_token、在冷却期内或刷新 HTTP 请求失败。

核心逻辑：
1. 查找账号，检查 session_token
2. `canRefresh()` 冷却检查（同一账号 5 分钟内不重复刷新）
3. 调用 `SessionRefresher.RefreshToken()`（复用在飞去重 + 5 并发限制）
4. 成功后调用 `RefreshAccountViaSession` 更新账号数据（旧 token → 新 token）
5. `markRefreshed()` 记录刷新时间戳

```go
func (s *AccountService) TrySyncRefresh(accessToken string) (string, bool) {
    account := s.GetAccount(accessToken)
    if account == nil {
        return "", false
    }
    sessionToken := util.Clean(account["session_token"])
    if sessionToken == "" {
        return "", false
    }
    if !s.canRefresh(accessToken) {
        return "", false
    }

    ctx, cancel := context.WithTimeout(context.Background(), refreshTimeout)
    defer cancel()
    newAT, newST, newExp, err := s.refresher.RefreshToken(ctx, accessToken, sessionToken)
    if err != nil {
        return "", false
    }
    s.RefreshAccountViaSession(accessToken, newAT, newST, newExp)
    s.markRefreshed(accessToken)
    return newAT, true
}
```

**新增辅助方法：**

- `canRefresh(accessToken string) bool` — 检查距离上次成功刷新是否 >= 5 分钟（常量 `refreshCooldown`）
- `markRefreshed(accessToken string)` — 记录刷新成功时间戳
- `HasOtherAvailableToken(accessToken string, exhaustedTokens map[string]struct{}) bool` — 只读检查（无副作用），判断除当前 token 和已 exhaust 的 token 外是否还有可用账号

#### 文件：`internal/protocol/api.go`

**修改 `collectVisionTextWithTokenRetry`：** 在 exhaust token 之前，先检查是否有其他可用账号。只有在没有其他账号时才尝试同步刷新。同步刷新成功后，**在同一轮迭代内**用新 token 内联重试：

```go
if service.IsAccountTokenExpiredErrorMessage(err.Error()) {
    // 有其他号：直接换号（异步刷新，不等待）
    if e.Accounts.HasOtherAvailableToken(token, exhaustedTokens) {
        exhaustedTokens[token] = struct{}{}
        e.Accounts.HandleTokenExpiredOnRequest(token)
        continue
    }
    // 没有其他号：同步刷新作为最后手段
    if newToken, ok := e.Accounts.TrySyncRefresh(token); ok {
        client = e.TextBackend(newToken)
        text, err = e.CollectVisionText(ctx, client, messages, model, images)
        if err == nil {
            return text, nil
        }
        lastErr = err
        exhaustedTokens[token] = struct{}{}
        continue
    }
    exhaustedTokens[token] = struct{}{}
    continue
}
```

**SSE 流式函数（`streamTextDeltasWithTokenRetry`、`streamVisionDeltasWithTokenRetry`）保持不变**，不做同步刷新。

---

## 五、并发安全设计

### 5.1 多请求同时命中同一过期账号

| 场景 | 处理 |
|------|------|
| 请求 A 和 B 同时发现 token 过期 | A 进入 `TrySyncRefresh` → `RefreshToken` 发起 HTTP 请求；B 调用 `RefreshToken` 时发现 `inFlight` 中有同一 token → 等待 A 的结果（复用现有去重机制） |
| 刷新 15s 超时 | 调用方用 `context.WithTimeout` 控制，超时后 `RefreshToken` 返回 error，上层走原逻辑 |
| 冷却期内的重复触发 | `canRefresh()` 检查失败 → 返回 `false`，不重复刷新 |

### 5.2 冷却窗口

- 刷新**成功后**记录 `lastRefreshAt`（内存 map，不持久化；进程重启后冷却自然重置）
- **5 分钟**内对同一账号的 `TrySyncRefresh` 请求直接返回 `false`
- 刷新失败**不记录**冷却时间——失败意味着 token 仍然过期，如果后续请求没有其他号可用，应该允许再次尝试刷新
- 目的：防止网络抖动或连续 401 导致频繁刷新，避免触发风控

### 5.3 全局限流的考量

当前设计依赖**两层限速**来控制刷新频率：

1. **单账号冷却（5 分钟）**：同一账号不会短时间重复刷新
2. **场景限制**：有其他账号时绝不刷新，同步刷新只在最后手段触发 —— 大幅减少刷新次数

暂不加跨账号的全局限流器（如"N 次/10 分钟"）。原因：
- 5 分钟单账号冷却 + "有其他号不刷新"策略已经大幅限制了刷新总量
- 实际场景中不太可能短时间内多个不同账号同时过期且都没其他号可用
- 如果后续生产环境发现刷新频率仍然偏高，可以再加全局限流不迟

---

## 六、风险与边界

### 6.1 刷新本身也可能触发 token_invalidated

当前刷新客户端使用代理 + surf（TLS fingerprint chrome145），虽然 `IsAccountTokenExpiredErrorMessage` 已把 `token_invalidated` 导向刷新路径，但如果同步刷新也返回 `token_invalidated`，会导致重试仍失败。

**对策：** `TrySyncRefresh` 刷新失败只返回 `false`，不修改账号状态。由上层错误处理（`ApplyAccountError`）决定是否删除账号。

### 6.2 同步刷新增加对话延迟

- 刷新耗时：通常 1-3s，最坏 15s（超时）
- 仅在没有其他可用账号时触发（低频场景）
- 5 分钟冷却确保短时间内不会重复等待

### 6.3 仅限 token_expired，不覆盖其他 401

- 只对 `IsAccountTokenExpiredErrorMessage` 匹配的错误做自动刷新
- `token_revoked`、`invalidated oauth token` 等仍走原删除路径
- `token_invalidated` 也会触发尝试（匹配 `IsAccountTokenExpiredErrorMessage`），但如果刷新失败说明账号可能真的有问题

### 6.4 SSE 流式场景的处理

`streamTextDeltasWithTokenRetry` 和 `streamVisionDeltasWithTokenRetry` 中遇到 401 时，可能已经发送了部分数据给用户。**不做同步刷新**：
- 已发送的增量数据无法撤回，重试会产生重复内容
- SSE 流式请求量大，频繁触发刷新风险更高
- 采用原逻辑：exhaust token → 换号重试；如果没号可换 → 报错

---

## 七、实施计划

### 第一步：补全 watcher 兜底 ✅

| 步骤 | 状态 | 改动 | 文件 |
|------|------|------|------|
| 1.1 | 已完成 | `listRefreshableLimitedTokens` 扫描范围加上 `"过期待刷新"`，`restore_at` 检查仅限 `"限流"` | `internal/service/account.go` |
| 1.2 | 待验证 | 手动将一个账号置为"过期待刷新"，确认 5 分钟内 watcher 能捞起来刷新 | — |

### 第二步：保守同步刷新 ✅

| 步骤 | 状态 | 改动 | 文件 |
|------|------|------|------|
| 2.1 | 已完成 | 新增 `TrySyncRefresh`（返回新 token）、`canRefresh`、`markRefreshed`、常量 `refreshCooldown = 5min` | `internal/service/account.go` |
| 2.2 | 已完成 | 新增 `lastRefreshAttempt` 冷却跟踪（内存 map） | `internal/service/account.go` |
| 2.3 | 已完成 | 新增 `HasOtherAvailableToken` 只读判断方法 | `internal/service/account.go` |
| 2.4 | 已完成 | 修改 `collectVisionTextWithTokenRetry`：有号换号，没号才同步刷新 + 同轮内联重试 | `internal/protocol/api.go` |
| 2.5 | 已完成 | SSE 流式函数保持原逻辑，不做同步刷新 | — |

### 后续观察项（不在本次范围）

- 生产环境观察刷新频率，如有需要再加跨账号全局限流
- `"刷新中"` 卡死情况监控，如有发生再针对性加兜底

---

## 八、已决策事项记录

| 决策点 | 结论 | 理由 |
|--------|------|------|
| 同步刷新触发条件 | 仅当无其他可用账号时 | 原则一：有其他号优先换号，不主动刷新 |
| 单账号冷却时间 | 5 分钟 | 原则一：避免短时间重复刷新触发风控 |
| 冷却记录时机 | 仅刷新成功时记录 | 失败说明 token 仍过期，应允许后续重试 |
| 跨账号全局限流 | 暂不加 | 单账号冷却 + 场景限制已足够，后续按需添加 |
| "刷新中"卡死兜底 | 暂不加 | 先观察再针对性解决 |
| 实施顺序 | watcher 兜底 → 保守同步刷新 | 先做最安全的改动 |
| SSE 流中 401 | 不做同步刷新 | 已发数据无法撤回，风险高于收益 |
| TrySyncRefresh 返回值 | `(string, bool)` | 刷新后旧 token 被替换，需返回新 token 供重试 |
