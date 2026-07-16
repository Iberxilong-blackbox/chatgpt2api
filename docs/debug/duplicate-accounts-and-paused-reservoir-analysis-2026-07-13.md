# 重复账号与蓄水池暂停后仍活动 — 根因分析与修复计划

> 日期：2026-07-13

## 疑问 1：为什么会出现两个相同账号？

### 现象

前端搜索 `bryan57@zainy.art` 出现两条记录：
- 一条 `正常 / quota=25`，iat=07-06
- 一条 `限流 / quota=0`，iat=06-25

两者 `chatgpt_account_id` 和 `session_id` 完全相同，说明是同一个 ChatGPT 账号。

### 根因：`AddAccountRecords` 只按 `access_token` 去重，不按 email/用户身份去重

`internal/service/account.go:230` — `AddAccountRecords`

```go
indexed := map[string]map[string]any{}   // key = access_token
for _, record := range cleaned {
    token := util.Clean(record["access_token"])
    current, ok := indexed[token]         // 只检查 access_token 是否已存在
    if ok {
        skipped++
    } else {
        added++
        current = map[string]any{}
        ...
    }
}
```

**去重唯一键是 `access_token`**，不检查 `email`、`user_id`、`chatgpt_account_id`。

对比 `findSessionImportAccountToken`（`AddAccountFromSession` 使用）则做了三层匹配：

```go
// internal/service/account.go:426
func (s *AccountService) findSessionImportAccountToken(...) string {
    if accessToken != "" && s.findIndexLocked(accessToken) >= 0 { return accessToken }  // 1. 按 token
    if userID != "" {
        for _, item := range s.items {
            if util.Clean(item["user_id"]) == userID { ... }                             // 2. 按 user_id
        }
    }
    if email != "" {
        for _, item := range s.items {
            if strings.ToLower(util.Clean(item["email"])) == email { ... }               // 3. 按 email
        }
    }
    return ""
}
```

**`AddAccountRecords` 缺少 email/user_id 级别的去重。**

### 重复账号如何产生

```
时间线：
  06-25  → 导入 JSON export 文件，access_token=AAAA，email=bryan57@zainy.art  → 账号 #1
  07-06  → 用新 session 重新导出 JSON，access_token=BBBB，email=bryan57@zainy.art  → 账号 #2
            (AddAccountRecords 看到 AAAA ≠ BBBB，认为"新账号"，added++)
```

两个记录同时存在于系统中。后续 `RefreshAccounts` 的 session refresh 阶段可能已产出新的 access_token，但旧记录未被合并。

### 附加问题：账号 ID 不断变化

`internal/storage/storage.go:224` — `saveRows` 实现：

```go
func (b *DatabaseBackend) saveRows(table, keyColumn string, items []map[string]any) error {
    tx.Exec("DELETE FROM " + table)         // 1. 全部删除
    for _, item := range items {
        stmt.Exec(key, string(data))         // 2. 逐条 INSERT（id 自增）
    }
    return tx.Commit()
}
```

**每次保存都 DELETE ALL + 全量 INSERT**。因此 `id` 字段（自增主键）每次都会变化。这不是 bug，是设计如此。

### 影响

- 同一用户的配额被分散在多条记录中，无法有效利用
- 账号管理页面出现重复，用户困惑
- 重复账号共用一个 `session_token`，刷新其中一条不会清理另一条

---

## 疑问 2：为什么暂停蓄水池后仍看到更新日志？

### 现象

点击"暂停调度"后，前端操作日志中仍有大量 `accounts` 模块的日志：
- 状态：`过期待刷新`
- 消息：`更新账号`

### 根因：`PauseReservoirScheduler` 只阻塞了一小部分

`internal/service/account_reservoir.go:211` — `PauseReservoirScheduler`

```go
func (s *AccountService) PauseReservoirScheduler() ReservoirSnapshot {
    s.reservoirPaused = true     // 仅设置这一个布尔值
}
```

阻塞点（`account_reservoir.go:230`）：

```go
if s.reservoirRefreshing > 0 || (!manual && s.reservoirPaused) {
    return nil
}
```

**只有 `runReservoirCycle(ctx, manual=false)` 被阻塞。** 以下路径完全不受影响：

| 路径 | 触发者 | 是否受暂停影响 |
|------|--------|---------------|
| `runReservoirCycle(ctx, false)` — 定时调度 | 系统定时器 | **是（被阻塞）** |
| `TriggerReservoirRefill` — 调用 `runReservoirCycle(ctx, true)` | 前端"一键补水"按钮 | **否**（manual=true 绕过） |
| `RefreshAccounts` — 直接从路由调用 | 前端"刷新账号信息和额度"按钮 | **否**（不经过 reservoir） |
| `RefreshAccountState` — 生图前自动刷新 | 每次生图请求 | **否** |
| `refreshAccountViaSessionAsync` — goroutine | 生图请求中 401 触发 | **否** |
| `UpdateAccount` — 通用更新 | 以上所有路径 | **否**（底层函数） |
| `StartDailyRefreshWatcher` — 每日自动刷新 | 独立定时器 | **否**（独立 goroutine） |
| 手动导入账号（JSON 文件/`AddAccountRecords`） | 文件导入 | **否** |

**简单来说：暂停蓄水池只停了"IOT"（周期性自动调度），没有任何其他东西。**

### `过期待刷新` 日志的具体来源

当 `RefreshAccounts`（或 `runReservoirCycle`）的**阶段一**（并发 `FetchRemoteInfo`）发现 token 过期：

```
FetchRemoteInfo → /backend-api/me 返回 401
  → ApplyAccountError("refresh_accounts", err)
    → ApplyAccountErrorMessage: event="refresh_accounts"
      → 有 session_token → status = "过期待刷新"（不是"刷新中"）
      → 不触发异步刷新（等待阶段二统一处理）
  → UpdateAccount → 日志："更新账号" + status="过期待刷新"
```

即使蓄水池暂停，以下操作仍会触发这个流程：
1. 前端手动点某个账号的"刷新"按钮 → `RefreshAccounts`
2. 生图请求触发 `RefreshAccountState` → 但此时 event="refresh_account_state"，状态是"刷新中"而非"过期待刷新"
3. 前端"一键补水" → `TriggerReservoirRefill`

### 验证方法

检查这些"过期待刷新"日志的**时间戳**：
- 如果时间戳在暂停之前 → 是历史日志，无影响
- 如果刚好与用户点"刷新"按钮的时间吻合 → 是手动刷新按钮触发的
- 如果是程序自主产生 → 可能来自异步 goroutine 或每日刷新

---

## 修复计划

### 修复 1：`AddAccountRecords` 增加 email/user_id 去重

**文件**：`internal/service/account.go`

在 `AddAccountRecords` 中增加 `findSessionImportAccountToken` 风格的匹配：

**方案 A（推荐）**：遍历当前已有的 `indexed` map，如果新记录的 email 或 user_id 已匹配到某个已有账号，则合并（merge）到该账号而非新建。

```go
// 伪代码
for _, record := range cleaned {
    token := util.Clean(record["access_token"])
    // 现有逻辑：按 access_token 去重
    current, ok := indexed[token]
    if ok {
        skipped++
    } else {
        // 新逻辑：按 email / user_id 查找是否已有同身份账号
        existingToken := ""
        if email := util.Clean(record["email"]); email != "" {
            for _, existing := range indexed {
                if strings.EqualFold(util.Clean(existing["email"]), email) {
                    existingToken = util.Clean(existing["access_token"])
                    break
                }
            }
        }
        if existingToken != "" {
            // 同身份已存在 → 更新该记录（替换旧 access_token）
            current = indexed[existingToken]
            updated++
            delete(indexed, existingToken)
            newOrder = removeTokenFromOrder(order, existingToken)
            order = append(order, token)
        } else {
            // 真正的新账号
            added++
            current = map[string]any{}
            order = append(order, token)
        }
    }
    updates := map[string]any{"access_token": token, ...}
    ...
}
```

**方案 B（简单但有风险）**：用 email 和 chatgpt_account_id 构建一个 `identityKey`，作为额外的去重键，与 access_token 并行。如果 identityKey 已存在，将新的 access_token 覆盖旧的。

**风险**：需要确认 `session_token` 是否是同一个——如果不同，说明是真正不同的会话，不应合并。

### 修复 2：暂停蓄水池应暂停所有自动活动

**文件**：多个文件

**待暂停的项目**：

| 项目 | 文件 | 修复方式 |
|------|------|----------|
| Reservoir 定时器 | `account_reservoir.go:194` | 已有，正常 |
| 每日自动刷新 | `account.go:1490` — `StartDailyRefreshWatcher` | 新增 `reservoirPaused` 检查 |
| 生图请求级 refresh | `account.go:867` — `GetAvailableAccessTokenForWithObserver` | **不应暂停**（实时请求需要有效账号） |
| 异步 session refresh | `account.go:690` — `refreshAccountViaSessionAsync` | 已有 `IsRefreshing` 去重，合理保留 |

**核心修改**：在 `StartDailyRefreshWatcher` 中增加暂停检查：

```go
// account.go:1490 — StartDailyRefreshWatcher
for {
    ...
    select {
    case <-ctx.Done():
        return
    case <-timer.C:
    }
    s.reservoirMu.Lock()
    paused := s.reservoirPaused
    s.reservoirMu.Unlock()
    if paused {
        continue  // 跳过本周期
    }
    s.runDailyAccountRefresh(ctx)
    ...
}
```

### 修复 3：`saveRows` 改为 UPSERT 而非 DELETE ALL + INSERT

**文件**：`internal/storage/storage.go:224`

**当前**：`DELETE FROM accounts` → `INSERT INTO accounts ...`（N 条）

**问题**：
- ID 每次都变化（微不足道但混乱）
- 在高并发下可能导致短暂的"空表"窗口
- 每次保存都是全量写操作

**建议改为 UPSERT**（仅针对 PostgreSQL/SQLite）：

```sql
-- PostgreSQL
INSERT INTO accounts (access_token, data) VALUES ($1, $2)
ON CONFLICT (access_token) DO UPDATE SET data = EXCLUDED.data

-- SQLite
INSERT OR REPLACE INTO accounts (access_token, data) VALUES (?, ?)
```

**收益**：
- ID 不变（不再有"同一个账号 ID 一直在变"的困惑）
- 只写变更行，减少 I/O
- 消除 DELETE ALL 带来的竞态窗口

**风险**：
- 需要验证 `access_token` 作为唯一键（当前 accounts 表需要对应的 unique constraint）
- 删除操作需要单独处理：先标记"to_delete"再 DELETE

### 修复优先级

| 优先级 | 修复项 | 理由 |
|--------|--------|------|
| P0 | 修复 1：重复账号去重 | 直接影响账号可用性，可能造成生图失败 |
| P1 | 修复 2：暂停应暂停每日刷新 | 影响排查效率，用户预期暂停就是全部停 |
| P2 | 修复 3：UPSERT 替代 DELETE+INSERT | 改善但不紧急 |
