# 账号养号前端 UI 开发计划

本文记录账号管理页中养号（warming）能力的前端展示与操作计划。目标是在不引入额外调度、代理或自动导入行为的前提下，把当前后端已经具备的养号能力变成可见、可控、可筛选的管理界面。

## 一、目标范围

本次迭代只覆盖账号管理页内的养号控制与展示：

- 展示 warming worker 当前运行状态和处理进度。
- 支持手动开始、停止、刷新养号任务。
- 在账号列表中清晰展示每个账号的养号状态、天数、失败次数和今日执行状态。
- 支持按养号状态筛选账号。
- 支持对选中账号批量设置养号状态。

不在本次迭代实现：

- 导入后自动进入养号。
- 页面加载后自动启动养号。
- 定时窗口调度。
- 独立养号代理配置。
- 复杂日历、任务队列或历史报表。

## 二、现有后端能力

当前后端已经提供以下 API：

| 方法 | 路径 | 用途 |
|------|------|------|
| `POST` | `/api/accounts/warming/start` | 启动一轮养号任务 |
| `POST` | `/api/accounts/warming/stop` | 停止当前养号任务 |
| `GET` | `/api/accounts/warming/status` | 获取养号任务状态 |
| `POST` | `/api/accounts/update` | 更新账号 `warming_status` 和 `warming_day` |

账号列表已经暴露以下前端字段：

| 字段 | 说明 |
|------|------|
| `warmingStatus` | `null` / `"warming"` / `"done"` |
| `warmingDay` | 已养号天数 |
| `warmingErrors` | 连续失败次数 |
| `warmingLastActionAt` | 上次养号动作时间 |

注意：当前 `stop` 是 context cancel 语义，可能中断等待或 SSE 消费；前端文案应使用“停止任务”，不要承诺“当前账号完成后停止”。

## 三、页面布局方案

在 `web/src/app/accounts/page.tsx` 的账号管理页中增加三个层次：

1. 顶部养号控制条。
2. 账号状态筛选。
3. 表格 badge 与批量操作增强。

### 3.1 顶部养号控制条

位置：账号列表工具栏附近，作为紧凑的运维控制区域。

展示内容：

- 运行状态：`未运行` / `运行中`。
- 处理进度：`已处理 N / Total`。
- 当前账号：展示后端返回的脱敏 token。
- 最近错误：仅在 `last_error` 非空时显示。

基础按钮：

| 按钮 | API | 状态规则 |
|------|-----|----------|
| 开始养号 | `POST /api/accounts/warming/start` | 运行中禁用 |
| 停止任务 | `POST /api/accounts/warming/stop` | 未运行时禁用 |
| 刷新状态 | `GET /api/accounts/warming/status` | 始终可用 |

运行中每 3 秒轮询一次 `/api/accounts/warming/status`。未运行时停止轮询，避免无意义请求。

### 3.2 账号筛选

在账号列表现有筛选区增加 segmented filter：

- `全部`
- `未养号`
- `养号中`
- `已养熟`
- `失败 >= 3`

筛选规则：

| 筛选项 | 条件 |
|--------|------|
| 未养号 | `!account.warmingStatus` |
| 养号中 | `account.warmingStatus === "warming"` |
| 已养熟 | `account.warmingStatus === "done"` |
| 失败 >= 3 | `(account.warmingErrors ?? 0) >= 3` |

### 3.3 表格 badge 增强

保留现有“养号”列，并增强展示逻辑：

| 状态 | 展示 |
|------|------|
| 未养号 | 不显示 badge，保持表格低噪声 |
| 养号中 | `养号中 Dn` |
| 已养熟 | `已养熟 Dn` |
| 失败 >= 3 | 在状态后追加 `(失败N次)`，使用危险色 |
| 今日已执行 | 可追加小型次级标记 `今日已跑` |

`今日已跑` 由前端根据 `warmingLastActionAt` 是否为本地当天判断。

## 四、批量操作

在已选择账号时，增加以下批量操作：

| 操作 | 后端更新 |
|------|----------|
| 设为养号中 | `warming_status = "warming"`，`warming_day = 0` |
| 设为已养熟 | `warming_status = "done"` |
| 取消养号 | `warming_status = null` |

说明：

- 第一版可以逐个调用现有 `/api/accounts/update`，不新增批量 API。
- 批量操作配合状态筛选和当前页全选实现：先筛选 `养号中` / `已养熟` / `失败 >= 3` 等状态，再通过当前页全选选择可见账号。
- 当前页全选只选择当前筛选和分页下可见的账号，不选择隐藏账号，避免误操作。
- 如果选中账号较多，再考虑后续增加批量更新接口。
- 不提供“重置失败次数”按钮，因为当前更新 API 未开放 `warming_errors`。
- `已养熟` 等于投入业务；当前后端只有 `warming_status === "warming"` 会被业务隔离。

## 五、交互细节

### 5.1 启动养号

点击“开始养号”后：

1. 调用 `POST /api/accounts/warming/start`。
2. 用返回值更新控制条状态。
3. 若状态为运行中，开启状态轮询。
4. 弹出成功提示。

如果当前没有可处理账号，后端会快速结束；前端只展示返回状态，不额外制造错误。

### 5.2 停止任务

点击“停止任务”后：

1. 调用 `POST /api/accounts/warming/stop`。
2. 用返回值更新控制条状态。
3. 保持短时间轮询，直到 `running = false`。

文案使用“正在请求停止”或“停止任务”，避免表达成强保证的优雅退出。

### 5.3 批量设为养号中

点击“设为养号中”后：

1. 对选中账号逐个调用 `/api/accounts/update`。
2. 成功后刷新账号列表状态。
3. 保留用户选择或清空选择均可，推荐清空选择，避免误操作。

批量“设为养号中”统一重置为 `warming_day = 0`，表示重新开始养号周期。

## 六、实现建议

建议新增或调整以下前端结构：

| 文件 | 变更 |
|------|------|
| `web/src/lib/api.ts` | 增加 `getWarmingStatus()`、`startWarming()`、`stopWarming()` API helper |
| `web/src/app/accounts/page.tsx` | 接入控制条、筛选、批量操作与轮询 |
| `web/src/components/ui/*` | 优先复用已有 Button、Badge、Select、Tabs/Segmented 控件 |

如果 `page.tsx` 继续膨胀，可将养号控制条拆为局部组件，例如：

```text
web/src/app/accounts/components/warming-control-bar.tsx
```

拆分只在组件逻辑明显独立时进行，避免为了抽象而抽象。

## 七、设计原则

- **KISS**：只展示当前后端真实支持的能力，不做调度、代理、历史报表等假入口。
- **YAGNI**：不新增批量 API、不新增 worker 配置，先用现有接口完成第一版闭环。
- **DRY**：复用现有账号更新逻辑、Badge 风格和 API helper 模式。
- **SOLID**：养号控制条只负责 worker 状态和控制；账号表格只负责账号状态展示与选择；批量操作只负责账号字段更新。

## 八、验收标准

- 账号管理页可以看到 warming worker 的运行状态和进度。
- 可以通过按钮启动、停止、刷新养号状态。
- 账号表格能区分未养号、养号中、已养熟、失败账号。
- 可以筛选养号相关账号。
- 可以批量把账号设为养号中、已养熟或取消养号。
- 前端不出现当前后端不支持的代理、定时调度、自动导入养号承诺。
