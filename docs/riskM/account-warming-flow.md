# 账号养号系统（Warming System）

本文说明账号养号系统的完整设计、数据模型、业务隔离策略、执行引擎流程以及前端交互现状。养号系统通过模拟人类正常使用行为，在 OpenAI 风控系统中为账号建立"稳定高价值用户"标签，提升账号在投入业务使用时的抗封禁能力。

---

## 一、数据模型

### 1.1 账号 Warming 字段

每个账号记录中的 warming 相关字段，存储在 `accounts` 表的 `data`（JSON）列中，由 storage backend 自动序列化/反序列化：

| 字段 | 类型 | 说明 |
|------|------|------|
| `warming_status` | string | 养号状态，仅三个值：`""`（正常业务）、`"warming"`（养号中）、`"done"`（已养熟） |
| `warming_day` | int | 已养号天数，每天+1。day≥7 自动标记为 `done` |
| `warming_started_at` | string | 养号开始时间 RFC3339 |
| `warming_last_action_at` | string | 上次养号动作时间 RFC3339，用于防止同一天重复执行 |
| `warming_errors` | int | 连续养号失败次数，≥3 次前端显示危险标记 |
| `warming_proxy` | string | 预留：养号专用独立代理地址，当前暂未实现 |

### 1.2 持久化机制

账号存储在数据库（SQLite / PostgreSQL / MySQL）中，**不是 JSON 文件**。通过 `UpdateAccount()` 修改 warming 字段会触发全量写回（`DELETE FROM accounts + INSERT ALL`），因此 warming 状态变更一定会持久化。

写回路径：

```
recordSuccess/recordFailure → UpdateAccount(token, updates)
  → mergeMaps(items[idx], updates)
  → normalizeAccount()      // 规范化 warming 字段
  → saveLocked()
    → DatabaseBackend.SaveAccounts(items)
      → BEGIN; DELETE FROM accounts; FOR each item: INSERT ...; COMMIT
```

---

## 二、后端实现结构

### 2.1 核心文件

| 文件 | 职责 |
|------|------|
| `internal/service/account_warming.go` | 接口定义（`WarmingRunner`）、数据结构（`WarmingStatus`、`WarmingPrompt`）、工具函数（`RandomThinkDuration`、`RandomReadDuration`、`IsToday`） |
| `internal/httpapi/warming_worker.go` | `WarmingRunner` 接口的具体实现：单次养号会话、SSE 消费、成功/失败记录 |
| `internal/service/account.go` | 养号账号的过滤隔离、开始养号前置刷新、账号 warming 字段规范化 |
| `internal/httpapi/app.go` | 启动时初始化 warming worker，加载 `warming_prompts.json` |
| `internal/httpapi/routes.go` | 三个养号控制 API + 账号编辑时支持修改 warming 字段 |

### 2.2 接口定义（`account_warming.go`）

```go
type WarmingRunner interface {
    Start()
    Stop()
    Status() WarmingStatus
}

type WarmingStatus struct {
    Running        bool   `json:"running"`
    CurrentAccount string `json:"current_account,omitempty"`
    Processed      int    `json:"processed"`
    Total          int    `json:"total"`
    LastError      string `json:"last_error,omitempty"`
}

type WarmingPrompt struct {
    Topic  string `json:"topic"`
    Prompt string `json:"prompt"`
}
```

### 2.3 初始化

在 `internal/httpapi/app.go:82`：

```go
if worker, err := newWarmingWorker(accounts, proxy,
    filepath.Join(cfg.DataDir, "warming_prompts.json")); err != nil {
    logger.Warning("warming worker init failed, warming disabled", "error", err.Error())
} else {
    accounts.SetWarmingRunner(worker)
}
```

如果 `warming_prompts.json` 文件不存在、内容为空或 JSON 格式错误，worker 初始化失败，养号功能整体禁用。此时调用 `POST /api/accounts/warming/start` 会返回类似：

```json
{
  "refresh": null,
  "status": {
    "running": false,
    "processed": 0,
    "total": 0
  }
}
```

线上 systemd 部署时，运行目录通常是 `/opt/chatgpt2api/data/warming_prompts.json`。`deploy/update.sh` 在完整更新和 `--sync-ac` 模式下会把项目目录 `data/warming_prompts.json` 同步到该运行目录。

---

## 三、业务请求隔离

养号中的账号（`warming_status == "warming"`）在以下路径被完全排除，确保不参与实际业务：

### 3.1 文本请求

- `filterNonFreeLocked()`（`account.go:692-706`）— 普通账号池过滤
- `filterFreeLocked()`（`account.go:709-723`）— Free 账号池过滤

### 3.2 图像生成

- `reserveNextCandidateToken()`（`account.go:1367-1397`）— 候选 token 分配
- `IsImageAccountAvailable()`（`account.go:1650-1656`）— 图像账号可用性检查

### 3.3 维护操作

- `listRefreshableLimitedTokens()`（`account.go:163-179`）— 限流恢复扫描
- `StartLimitedWatcher()` 触发的自动限流恢复不会处理养号账号
- `RefreshAccounts()` 本身不强制排除养号账号；管理员手动刷新指定账号、或开始养号前置刷新时，可以刷新养号账号的信息和额度

### 3.4 辅助函数

```go
func isWarmingAccount(account map[string]any) bool {
    return util.Clean(account["warming_status"]) == "warming"
}
```

---

## 四、Warming Worker 执行引擎

位于 `internal/httpapi/warming_worker.go`，实现 `service.WarmingRunner` 接口。

### 4.1 执行规则

| 规则 | 说明 |
|------|------|
| 触发方式 | API 调用 `POST /api/accounts/warming/start` |
| 执行频率 | 每个账号每天最多一次（按 `warming_last_action_at` 判断） |
| 账号选取 | 遍历原始账号记录中 `warming_status == "warming"` 且今日未执行过的账号 |
| 完成条件 | `warming_day >= 7` 时自动将 `warming_status` 标记为 `"done"` |
| 并发模型 | 单 goroutine 串行执行，一个接一个处理 |
| 停止方式 | `POST /api/accounts/warming/stop` 触发 context cancel，当前账号完成后退出 |

### 4.2 开始养号前置刷新

`AccountService.StartWarming(ctx)` 在启动 worker 前，会先找出需要刷新额度的养号账号，并调用内部 `RefreshAccounts()`：

```go
refresh := s.RefreshAccounts(ctx, s.listRefreshableWarmingTokens(time.Now()))
s.warmingWorker.Start()
```

筛选条件：

| 条件 | 说明 |
|------|------|
| `warming_status == "warming"` | 只处理当前处于养号中的账号 |
| `status == "过期待刷新"` | 账号 token/会话过期后需要先刷新 |
| `restore_at <= now` | 前端显示“已到恢复时间”的限流账号 |

说明：

- 这是后端内部调用，不会在浏览器 Network 中出现单独的 `/api/accounts/refresh` 请求。
- 刷新结果会作为 `POST /api/accounts/warming/start` 响应中的 `refresh` 字段返回。
- 如果 worker 未初始化或已经运行中，`refresh` 为 `null`。
- 如果没有需要刷新的养号账号，`refresh` 是一个空刷新结果对象，`total` 为 `0`。

### 4.3 账号收集

worker 通过 `ListTokens()` + `GetAccount()` 读取原始账号记录，再判断 `warming_status`、`warming_last_action_at`。不要使用前端展示用的 `ListAccounts()` 字段名做 worker 判定，因为列表响应中字段会转换为 `warmingStatus`、`warmingDay`。

```go
func (w *warmingWorker) collectWarmingAccounts() []map[string]any {
    tokens := w.svc.ListTokens()
    for _, token := range tokens {
        item := w.svc.GetAccount(token)
        if util.Clean(item["warming_status"]) != "warming" {
            continue
        }
        if service.IsToday(util.Clean(item["warming_last_action_at"])) {
            continue
        }
        out = append(out, item)
    }
    return out
}
```

### 4.4 单次养号会话流程

```
1. Bootstrap (GET /)
   → 模拟页面加载，暴露 Cloudflare/网络/代理问题

2. Session Check (GET /api/auth/session)
   → 验证 token 是否存活

3. Load History (GET /backend-api/conversations)
   → 模拟侧边栏加载对话列表（非致命：失败也继续）

4. 思考延迟 (RandomThinkDuration)
   → 90% 概率 3-8s，10% 概率 15-45s

5. 发送问题 (StreamConversation + 完整消费 SSE)
   → 从 warming_prompts 随机选取一条 prompt
   → 完整 drain SSE channel（模仿真实用户等待回答）

6. 阅读延迟 (RandomReadDuration)
   → 90% 概率 10-20s，10% 概率 30-90s

7. 可选追问 (warming_day >= 3 时 50% 概率)
   → 再次思考延迟 → 发送追问 → 完整消费 SSE → 阅读延迟
```

### 4.5 成功处理

```go
func (w *warmingWorker) recordSuccess(account map[string]any) {
    day := util.ToInt(account["warming_day"], 0)
    updates := map[string]any{
        "warming_last_action_at": time.Now().Format(time.RFC3339),
        "warming_errors":         0,
    }
    if day >= 6 {
        updates["warming_status"] = "done"  // 第7天标记养熟
        updates["warming_day"] = day + 1
    } else {
        updates["warming_day"] = day + 1
    }
    w.svc.UpdateAccount(token, updates)
}
```

### 4.6 失败处理

```go
func (w *warmingWorker) recordFailure(account map[string]any) {
    errors := util.ToInt(account["warming_errors"], 0) + 1
    w.svc.UpdateAccount(token, map[string]any{
        "warming_last_action_at": time.Now().Format(time.RFC3339),
        "warming_errors":         errors,
    })
}
```

连续失败 ≥3 次，前端会显示危险标记 `(失败N次)`。

### 4.7 提示词语料库

存储在 `data/warming_prompts.json`，格式：

```json
[
  {"topic": "greeting", "prompt": "Hello! How are you today?"},
  {"topic": "weather", "prompt": "What's the weather like in Tokyo?"}
]
```

---

## 五、API 接口

### 5.1 养号控制

| 方法 | 路径 | 说明 |
|------|------|------|
| `POST` | `/api/accounts/warming/start` | 先刷新到期/过期的 warming 账号，再后台串行处理所有 warming 账号 |
| `POST` | `/api/accounts/warming/stop` | 停止养号（当前账号完成后退出） |
| `GET` | `/api/accounts/warming/status` | 查看养号状态 |

响应格式：

```json
{
  "refresh": {
    "refreshed": 1,
    "session_refreshed": 0,
    "session_failed": 0,
    "total": 1,
    "failed": 0,
    "errors": [],
    "results": [],
    "duration_ms": 1234,
    "items": []
  },
  "status": {
    "running": true,
    "current_account": "sk-...abc",
    "processed": 3,
    "total": 10,
    "last_error": ""
  }
}
```

`refresh` 为开始养号前置刷新结果。该刷新在后端内部完成，不会产生额外的 `/api/accounts/refresh` 浏览器请求。若 worker 未初始化或已经在运行，`refresh` 为 `null`。

### 5.2 账号更新中的 Warming 字段

`POST /api/accounts/update` 支持更新 `warming_status` 和 `warming_day`，服务端校验 `warming_status` 只能为 `""`、`"warming"`、`"done"`。

前端取消养号时会发送：

```json
{
  "account_id": "xxx",
  "warming_status": null
}
```

后端允许 `warming_status: null` 作为显式清空操作，并由 `normalizeAccount()` 规范化为未养号状态。

---

## 六、前端交互现状

### 6.1 已实现

| 功能 | 位置 | 说明 |
|------|------|------|
| 养号任务面板 | `web/src/app/accounts/page.tsx` | 展示 worker 运行状态、进度、当前账号、最近错误 |
| 开始/停止/刷新状态 | `web/src/app/accounts/page.tsx` | 调用 `/api/accounts/warming/start`、`stop`、`status` |
| 养号状态筛选 | `web/src/app/accounts/page.tsx` | 支持全部养号、未养号、养号中、已养熟、失败账号 |
| 养号状态标签 | `web/src/app/accounts/page.tsx` | 表格"养号"列显示 `已养熟` / `养号中 Dx`，错误≥3次标红，并标记今日已跑 |
| 编辑对话框 | `web/src/app/accounts/page.tsx` | 手动设置养号状态和天数 |
| 批量操作 | `web/src/app/accounts/page.tsx` | 对当前筛选结果中的选中账号批量设为养号中、已养熟、取消养号 |
| Account 类型定义 | `web/src/lib/api.ts` | `warmingStatus`、`warmingDay`、`warmingErrors`、`warmingLastActionAt` |
| updateAccount 参数 | `web/src/lib/api.ts` | `warming_status`、`warming_day`，支持 `warming_status: null` 清空 |

### 6.2 交互注意事项

- “开始养号”不接收前端选中的账号列表；后端会扫描所有 `warming_status == "warming"` 的账号。
- 点击“开始养号”前，若存在到期或过期的养号账号，后端会内部刷新其账号信息和额度。
- 浏览器 Network 中只会看到 `/api/accounts/warming/start` 请求，不会看到额外的 `/api/accounts/refresh` 请求。
- 如果 `/api/accounts/warming/start` 返回 `refresh: null` 且 `status.running == false`，通常表示 worker 未初始化；优先检查运行目录中的 `data/warming_prompts.json` 和服务启动日志。

---

## 七、配置项

| 配置 | 位置 | 说明 |
|------|------|------|
| `warming_prompts.json` | `data/warming_prompts.json` | 养号提示词语料库，由 `DataDir` 指定路径 |
| `auto_remove_invalid_accounts` | `config.go` | 自动移除异常账号（与养号无直接关系但影响号池） |
| `auto_remove_rate_limited_accounts` | `config.go` | 自动移除限流账号（同上） |

### 7.1 systemd 部署同步

`deploy/update.sh` 会在以下模式同步语料库：

| 模式 | 行为 |
|------|------|
| `sudo ./deploy/update.sh` | 构建并更新二进制后，启动服务前同步 `data/warming_prompts.json` 到 `/opt/chatgpt2api/data/warming_prompts.json` |
| `sudo ./deploy/update.sh --sync-ac` | 同步 `data/auto_import/*.json` 后，同步 `warming_prompts.json` 并重启服务 |

`--env` 模式只更新 `.env`，不会同步语料库。

---

## 八、IP 层面的风险说明

养号账号与业务账号即使账号级隔离，但若共享同一出口 IP，OpenAI 风控系统可能在 IP 维度做关联聚类。当前没有独立代理支持（`warming_proxy` 字段为预留），建议养号与业务使用不同时段运行以降低风险。
