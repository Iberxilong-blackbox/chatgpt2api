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
| `internal/service/account.go` | 养号账号的过滤隔离（文本请求、图像生成、限流恢复等路径） |
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

如果 `warming_prompts.json` 文件不存在或内容为空，worker 初始化失败，养号功能整体禁用。

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
- `RefreshAccounts()` 批量刷新不包含养号账号

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
| 账号选取 | 遍历所有 `warming_status == "warming"` 且今日未执行过的账号 |
| 完成条件 | `warming_day >= 7` 时自动将 `warming_status` 标记为 `"done"` |
| 并发模型 | 单 goroutine 串行执行，一个接一个处理 |
| 停止方式 | `POST /api/accounts/warming/stop` 触发 context cancel，当前账号完成后退出 |

### 4.2 单次养号会话流程

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

### 4.3 成功处理

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

### 4.4 失败处理

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

### 4.5 提示词语料库

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
| `POST` | `/api/accounts/warming/start` | 开始养号（后台串行处理所有 warming 账号） |
| `POST` | `/api/accounts/warming/stop` | 停止养号（当前账号完成后退出） |
| `GET` | `/api/accounts/warming/status` | 查看养号状态 |

响应格式：

```json
{
  "status": {
    "running": true,
    "current_account": "sk-...abc",
    "processed": 3,
    "total": 10,
    "last_error": ""
  }
}
```

### 5.2 账号更新中的 Warming 字段

`POST /api/accounts/update` 支持更新 `warming_status` 和 `warming_day`，服务端校验 `warming_status` 只能为 `""`、`"warming"`、`"done"`。

---

## 六、前端交互现状

### 6.1 已实现

| 功能 | 位置 | 说明 |
|------|------|------|
| 养号状态标签 | `page.tsx:535-546` | 表格"养号"列显示 `已养熟` / `养号中 Dx`，错误≥3次标红 |
| 编辑对话框 | `page.tsx:757-781` | 手动设置养号状态和天数 |
| Account 类型定义 | `api.ts:164-167` | `warmingStatus`、`warmingDay`、`warmingErrors`、`warmingLastActionAt` |
| updateAccount 参数 | `api.ts:863-864` | `warming_status`、`warming_day` |

### 6.2 未实现

| 功能 | 说明 |
|------|------|
| 养号开始/停止按钮 | 已有 API（`/api/accounts/warming/start`、`stop`），前端未对接 |
| 养号进度面板 | 已有 API（`/api/accounts/warming/status`），前端未展示 |
| 养号状态筛选 | 按 warming_status 过滤账号列表 |

---

## 七、配置项

| 配置 | 位置 | 说明 |
|------|------|------|
| `warming_prompts.json` | `data/warming_prompts.json` | 养号提示词语料库，由 `DataDir` 指定路径 |
| `auto_remove_invalid_accounts` | `config.go` | 自动移除异常账号（与养号无直接关系但影响号池） |
| `auto_remove_rate_limited_accounts` | `config.go` | 自动移除限流账号（同上） |

---

## 八、IP 层面的风险说明

养号账号与业务账号即使账号级隔离，但若共享同一出口 IP，OpenAI 风控系统可能在 IP 维度做关联聚类。当前没有独立代理支持（`warming_proxy` 字段为预留），建议养号与业务使用不同时段运行以降低风险。
