# 账号养号系统 — 开发规划

基于 `build-feed.md` 中的养号思路，结合当前 `chatgpt2api` 代码现状，制定统一的分阶段开发计划。

---

## 一、目标

将"囤号"变为真正的"养号"，通过模拟人类正常使用行为，在 OpenAI 风控系统中为账号建立"稳定高价值用户"标签，提升账号在投入业务使用时的抗封禁能力。

---

## 二、账号新增字段

为区分养号中的账号和正常业务账号，在 account record 中新增以下字段：

| 字段名 | 类型 | 说明 |
|--------|------|------|
| `warming_status` | string | 养号状态，仅三个值：`""`（正常业务，不养号）、`"warming"`（养号进行中）、`"done"`（养号完成，可投入业务） |
| `warming_day` | int | 已养号天数，从养号开始起算，每天+1。day 1=破冰，2-3=活跃，4-7=成熟，>=7 可标记 done |
| `warming_started_at` | string | 养号开始时间 RFC3339 |
| `warming_last_action_at` | string | 上次养号动作时间 RFC3339，防止同一天重复执行 |
| `warming_proxy` | string | 预留：养号专用独立代理地址。**当前暂不实现，仅做字段占位** |

**设计说明：** 不设 `new/icebreaking/active/mature` 等多阶段字段，因为 `warming_day` 本身就是精确的阶段指标。业务代码只需要判断 `warming_status == "warming"` 来决定是否隔离，具体的行为频率由 `warming_day` 数值驱动。

### 导入时的处理

- 用户可在导入 JSON 时直接提供 `warming_status: "warming"` + `warming_day: 0`，系统原样存储
- 如果 `warming_status` 为空，视为正常业务账号，不参与养号流程
- `fp` 字段沿用现有逻辑（`prepareAccountFP`），养号不改变指纹

### 后端存储

字段存储在 account map 中（`AccountService.items`），由 storage backend 持久化。无需新增数据库迁移——storage 层自动序列化 map 中的新 key。

---

## 三、业务请求隔离（P0）

确保养号中的账号不会被业务请求选中。

### 3.1 文本请求隔离

**文件：** `internal/service/account.go`

需修改以下函数，跳过 `warming_status == "warming"` 的账号：

- `filterNonFreeLocked()`（第 576 行）
- `filterFreeLocked()`（第 590 行）
- `selectTextTokenFromPoolLocked()`（第 608 行）— 作为最后防线

新增辅助函数：

```go
func isWarmingAccount(account map[string]any) bool {
    return util.Clean(account["warming_status"]) == "warming"
}
```

### 3.2 图像生成请求隔离

- `reserveNextCandidateToken()`（第 1245 行）— 在 `allow` 过滤后、slot 检查前，跳过 `isWarmingAccount() == true` 的账号
- `GetAvailableAccessTokenFor()`（第 670 行）— 调用链的上层，在 `RefreshAccountState` 前增加一层 warming 过滤
- `IsImageAccountAvailable()`（第 1492 行）— 增加 warming 状态判断

### 3.3 刷新/维护操作隔离

- `StartLimitedWatcher()`（第 1218 行）— 养号中的账号不触发限流恢复检查
- `RefreshAccounts()` — 养号中的账号不参与批量刷新

---

## 四、前端管理界面

### 4.1 账号列表增强

**`publicAccounts()`（第 1614 行）** 需要暴露以下新字段给前端：

```go
"warmingStatus":    account["warming_status"],
"warmingDay":       util.ToInt(account["warming_day"], 0),
"warmingStartedAt": account["warming_started_at"],
```

### 4.2 账号列表筛选

前端账号管理页面增加按 `warming_status` 的筛选：
- 全部 / 正常业务 / 养号中 / 已养熟

### 4.3 手动操作

- 将账号标记为"开始养号"（设置 `warming_status: "warming"`, `warming_day: 0`）
- 将养熟账号标记为"投入业务"（设置 `warming_status: "done"`）
- 暂停/重置养号进度

---

## 五、Warming Worker（养号执行引擎）

新建 `internal/service/account_warming.go`，在 `httpapi/app.go` 的 `NewApp()` 中启动。

### 5.1 执行流程

每天凌晨 ~02:00（随机偏移 ±30min），Worker 执行以下逻辑：

1. 遍历所有 `warming_status == "warming"` 的账号
2. 跳过当天已执行过的（`warming_last_action_at` 为今天）
3. 根据 `warming_day` 确定今日行为次数和复杂度：
   - day 1（破冰）：1 次登录 + 1 轮简短对话
   - day 2-3（活跃）：1-2 次，2-3 轮上下文对话
   - day 4-7（成熟）：2-3 次，长文本/代码/复杂对话
4. 为每次执行分配随机时间点（分布在 07:00-23:00 之间）
5. 到达时间点时，执行完整的浏览器模拟流（见 5.2）
6. 执行完毕后更新 `warming_day += 1` 和 `warming_last_action_at`
7. 当 `warming_day >= 7` 时，将 `warming_status` 切换为 `done`

### 5.2 单次养号请求流

每次执行模拟完整浏览器行为（复用现有 `backend.Client`）：

```
1. Bootstrap        → GET /
2. Session Check    → GET /api/auth/session
3. Load History     → GET /backend-api/conversations
4. Think Time       → sleep(3~8s 随机)
5. Send Question    → POST /backend-api/f/conversation（完整消费 SSE stream）
6. Read Time        → sleep(10~20s 随机)
7. 可选：多轮对话   → 重复步骤 4-6（2-3 轮）
```

### 5.3 单次养号耗时估算

按照上述请求流，每个账号每次养号会话的耗时：

| 步骤 | 耗时 |
|------|------|
| Bootstrap（GET /） | ~1-2s |
| Session Check | ~1s |
| Load History | ~1-2s |
| Think Time（随机抖动） | 3-8s |
| Send + 完整消费 SSE stream | 5-30s（取决于问题长短和回答长度） |
| Read Time（模拟阅读） | 10-20s |
| **单轮对话合计** | **约 21-63s** |
| 多一轮上下文对话 | +15-50s |

不同阶段每天耗时：

| warming_day | 会话次数 | 每会话轮数 | 每天总耗时（估算） |
|-------------|----------|------------|-------------------|
| day 1（破冰） | 1 次 | 1 轮 | **~30s-1min** |
| day 2-3（活跃） | 1-2 次 | 2-3 轮 | **~1-4min** |
| day 4-7（成熟） | 2-3 次 | 2-3 轮（长内容） | **~2-8min** |

**结论：** 每个账号每天养号耗时在 30 秒到 8 分钟之间，不会占用大量时间。

### 5.4 IP 层面的风险：养号与业务账号的交叉关联

**即使账号是隔离的，IP 是共享的。**

假设同一个 IP（例如 WARP 节点）下部署了 10 个账号，其中 3 个在养号、7 个在跑业务。从 OpenAI 风控视角看这个 IP：

```
时间线：下午 3 点

账号 A（warming, day3）：GET / → 停 5s → /api/auth/session → 停 3s → /conversations
                           → 停 7s → POST 短问题 → 完整读流 → 停 15s → 结束
                           ↑ 像人：慢、有停顿、完整消费

账号 B（业务，正常）：POST /v1/images/generations → 返回 → POST → 返回 → POST → 返回
                     ↑ 像机器：快、无停顿、批量连发
```

**风险：同一 IP 在同一时间窗口，混杂了"像人"和"像机器"两种截然不同的行为模式。** 风控系统的 IP 画像会同时看到这两种信号，可能推导出：

> "这个 IP 有人在正常用，也有脚本在批量调用 → 这是一个共用代理/VPN → 这个 IP 下的所有账号都值得被标记复查。"

**比时间混用更糟糕的是——** 如果 OpenAI 把养号账号 A 和业务账号 B 在 IP 维度上做了关联聚类，即使 A 养得再好，也可能因为和 B 共享 IP 而被连带降权。

#### 推荐策略：时间分片（独立 IP 暂不可用的替代方案）

在独立代理落地之前，让养号和业务使用**不同时段**：

| 时段 | 用途 | 说明 |
|------|------|------|
| **03:00-07:00**（业务低谷） | 养号 | 所有 warming 账号在这个窗口内随机分配执行时间 |
| **07:00-03:00**（业务高峰） | 业务 | 正常业务请求，养号脚本完全不活动 |

**原理：** 风控系统对 IP 的行为模式分析有较强的"时间局部性"——如果某个时间段 IP 的行为模式统一（全部是养号 or 全部是业务），不易触发异常检测。不同时间段展现不同行为模式，比同一时间混杂两种模式要安全得多。

**但这仍然是折中方案。** 理想情况是每个账号有独立 IP（见 Phase 4），这样 IP 维度的交叉关联完全不存在。

### 5.5 状态切换的时机

`warming_status` 从 `"warming"` 切换到 `"done"` 时，该账号从"养号窗口"进入"业务窗口"。这个切换应当在养号窗口内完成（凌晨 3-7 点之间），不要在业务高峰期突然把一个"养号中"的账号推到业务池里。

### 5.6 语料库

第一版：在 `data/` 下放一个 `warming_prompts.json`，手工录入 50-100 条日常问题，养号时随机抽取。

后续迭代：通过管理后台，使用高质量 Plus 账号自动生成 500-1000 条多样化语料，存入 `warming_prompts` 表。

### 5.7 关键技术点

- **完整消费 SSE stream**：复用 `iterSSEPayloads()`，不能发完就断连
- **作息随机化**：每个账号的每日触发时间随机分配，不集中在同一时刻
- **指纹稳定**：使用账号已有的 `fp`（`oai-device-id` 不变），通过 proxy 走隔离网络
- **Progress 持久化**：状态实时写入 storage，防止重启丢失进度
- **环境隔离**：每次请求使用该账号绑定的 proxy+fingerprint（复用现有 `ProxyService` + `backend.Client`）

---

## 六、分阶段实施计划

### Phase 1：数据模型 + 业务隔离 ✅ 已完成

| 任务 | 文件 | 描述 | 状态 |
|------|------|------|------|
| 1.1 | `account.go:1492` | 新增 `isWarmingAccount()` 辅助函数 | ✅ |
| 1.2 | `account.go:580,596` | `filterNonFreeLocked()` / `filterFreeLocked()` 跳过养号账号 | ✅ |
| 1.3 | `account.go:1260` | `reserveNextCandidateToken()` 跳过养号账号 | ✅ |
| 1.4 | `account.go:1495` | `IsImageAccountAvailable()` 增加 warming 判断 | ✅ |
| 1.5 | `account.go:1628,1660` | `normalizeAccount()` 标准化 warming 字段；`publicAccounts()` 暴露 `warmingStatus`/`warmingDay` | ✅ |
| 1.6 | `routes.go:1249,1254` | 更新白名单新增 `warming_status`/`warming_day`；新增值校验 | ✅ |
| 1.7 | `account.go:162` | `listRefreshableLimitedTokens()` 跳过养号账号 | ✅ |
| 1.8 | 前端 `api.ts` | `Account` 类型新增 `warmingStatus`/`warmingDay`；`updateAccount()` 参数新增 warming 字段 | ✅ |
| 1.9 | 前端 `page.tsx` | 表格新增"养号"列 + warming badge；编辑对话框新增养号状态/天数控件 | ✅ |

**Phase 1 实施细节：**
- `warming_status` 仅三个合法值：`""`（正常业务）、`"warming"`（养号中）、`"done"`（已养熟）
- `warming_day` 为 int，由 `normalizeAccount()` 保证
- 养号中的账号从**文本请求、图像生成、限流恢复**三个路径全部排除
- 前端编辑对话框根据 warming 状态动态显示/隐藏天数输入框
- 全量测试通过（10 个包，无回归）

### Phase 2：Warming Worker（P1，核心功能）

| 任务 | 文件 | 描述 |
|------|------|------|
| 2.1 | `account_warming.go`（新建） | Warming Worker 主逻辑（每日调度） |
| 2.2 | `account_warming.go` | 单次养号执行流（浏览器模拟） |
| 2.3 | `data/warming_prompts.json` | 初始语料库（手工 50-100 条） |
| 2.4 | `app.go` | 启动 Warming Worker goroutine |
| 2.5 | `account_warming.go` | 渐进式频率控制（按 warming_day 分级） |
| 2.6 | `account_warming.go` | 多轮上下文对话支持 |

### Phase 3：语料库 + 管理后台（P2，规模化）

| 任务 | 文件 | 描述 |
|------|------|------|
| 3.1 | 前端 + API | 语料库管理界面（增删改查） |
| 3.2 | `account_warming.go` | 语料库随机抽取（去重） |
| 3.3 | `account_warming.go` | 母号批量生成语料（调用 API） |
| 3.4 | 前端 | 养号进度看板（各阶段账号数量、执行日志） |

### Phase 4：独立代理（Future）

| 任务 | 描述 |
|------|------|
| 4.1 | 账号 `warming_proxy` 字段落地 |
| 4.2 | Warming Worker 使用独立代理发起请求 |
| 4.3 | 代理池管理（代理-账号绑定关系） |
| 4.4 | 移除时间分片策略，养号和业务可并发执行（独立 IP 后不再需要时间隔离） |

---

## 七、不改动的现有逻辑

以下逻辑维持不变，养号系统不介入：

- `PrepareAccountFP()` — 指纹生成逻辑
- `SessionRefresher` — token 刷新机制
- `BootstrapRemote()` / `FetchRemoteInfo()` — 远程状态查询
- `ApplyAccountError()` — 错误分类和状态更新
- `StartLimitedWatcher()` — 限流恢复检测（通过 `listRefreshableLimitedTokens()` 增加 warming 过滤）
