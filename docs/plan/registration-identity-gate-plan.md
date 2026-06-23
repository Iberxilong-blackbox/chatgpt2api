# Plan: 注册前置身份 ID 白名单

## Context

当前本地账号注册由 `/auth/register` 提供。开启 `registration_enabled` 后，未登录用户只要提交合法 `username` 和 `password` 即可注册普通用户。现有流程没有邀请码、邮箱验证、注册审核或注册接口限流。

目标是新增一个注册前置判断：后台维护一份“有共识的唯一字符串”列表，用户注册前必须输入自己的字符串。每个字符串只能使用一次，可理解为好友身份 ID。输入正确且未使用后，用户才能注册为普通用户。

核心安全要求：**不能只在前端页面判断**。最终注册接口 `/auth/register` 必须在后端强制校验并消费该身份 ID，否则攻击者可以绕过页面直接调用接口。

## 目标

1. 后台可维护一份注册身份 ID 列表。
2. 注册页变为两步体验：
   - 第一步输入身份 ID。
   - 第二步输入用户名、密码、昵称并提交注册。
3. 每个身份 ID 只能成功注册一次。
4. `/auth/register` 后端必须拒绝缺少、无效、禁用或已使用的身份 ID。
5. 身份 ID 允许明文保存和管理员查看；普通用户永不回显。
6. 注册失败响应避免泄露 ID 是否存在，降低枚举风险。
7. 建立身份 ID 与本地用户名之间的一一映射，管理员可以在后台查看该映射。

## 非目标

1. 不做复杂 CDK 营销系统、批次系统或兑换权益系统。
2. 不做多类型注册策略或兼容旧注册流程。
3. 不引入邮件验证、短信验证或第三方验证码。
4. 不改变管理员手动创建用户流程。
5. 不改变现有登录流程。
6. 不向普通用户提供身份 ID 查看、找回、历史记录或映射查询能力。

## 数据模型

新增注册身份 ID 记录，建议存储在 JSON document 中，例如 `registration_identity_ids.json`。

建议字段：

```json
{
  "items": [
    {
      "id": "rid_xxx",
      "identity_id": "friend-unique-string",
      "label": "好友备注",
      "enabled": true,
      "used": false,
      "used_by_user_id": "",
      "used_by_username": "",
      "used_user_deleted": false,
      "used_at": "",
      "created_at": "2026-06-23T00:00:00Z",
      "updated_at": "2026-06-23T00:00:00Z"
    }
  ]
}
```

### 一一映射规则

身份 ID 与用户名的关系必须是严格一一映射：

- 一个身份 ID 最多只能绑定一个本地用户。
- 一个本地用户最多只能由一个身份 ID 注册产生。
- 注册成功后，身份 ID 记录必须写入 `used_by_user_id` 和 `used_by_username`。
- 只有管理员后台能查看“身份 ID 记录 -> 用户名”的映射。
- 管理员后台展示原始身份 ID、`label`、状态、绑定用户名、绑定用户 ID、账号是否已删除、使用时间，用于管理员追踪。
- 普通用户注册后，任何个人资料、会话、用户 API、页面状态中都不应返回或展示自己的身份 ID、身份 ID label 或映射记录。

由于普通用户只能在注册时创建一次本地账号，主约束是“身份 ID 不可重复消费”。同时建议在用户侧保存反向映射或提供查询函数，避免后续数据修复、导入或并发异常造成一个用户名关联多个身份 ID。

### 明文存储与输入规范

- 身份 ID 是用户侧已公开或可共识确认的信息，V1 允许明文保存。
- 管理员后台可以查看原始身份 ID、备注、启用状态、使用状态、绑定用户名、绑定用户 ID 和使用时间。
- 普通用户接口、普通用户页面、登录响应和 session 中不得返回身份 ID 明文。
- 不强制限定字符集，但必须做基础边界：`trim`、非空、长度上限、拒绝控制字符和换行。
- 建议长度上限为 256 字符；比较时使用 trim 后的原文，避免用户复制时首尾空白导致失败。

## 后端设计

### 1. 新增服务

新增 `RegistrationGateService`，职责保持单一：

- 维护身份 ID 列表。
- 规范化输入。
- 校验身份 ID 是否可用。
- 在注册成功时原子标记为已使用。

建议核心方法：

```go
type RegistrationGateService struct {
    mu sync.Mutex
    store storage.JSONDocumentBackend
}

func (s *RegistrationGateService) List() []map[string]any
func (s *RegistrationGateService) Add(rawID, label string) (map[string]any, error)
func (s *RegistrationGateService) Update(id string, updates map[string]any) (map[string]any, error)
func (s *RegistrationGateService) Delete(id string) bool
func (s *RegistrationGateService) Consume(rawID, userID, username string) error
func (s *RegistrationGateService) MarkUserDeleted(userID string) error
```

### 2. 修改注册接口

当前 `/auth/register` 逻辑：

1. 检查 `registration_enabled`。
2. 读取 JSON body。
3. 调用 `RegisterPasswordUser(username, password, name)`。

目标逻辑：

1. 检查 `registration_enabled`。
2. 读取 JSON body。
3. 提取 `identity_id`。
4. 在同一注册临界区内完成：
   - 校验身份 ID 存在、启用、未使用。
   - 创建普通用户。
   - 标记身份 ID 已使用并绑定用户 ID 与用户名。
   - 确认该用户没有绑定过其他身份 ID。
5. 返回登录响应。

注意：不要实现“先校验身份 ID，前端保存通过状态，然后注册时不带身份 ID”的流程。

### 3. 原子性要求

由于当前项目主要使用 JSON 存储，没有数据库事务，必须通过服务端锁保证同一个身份 ID 不会被并发请求重复消费。

可选实现：

- 简单方案：在 `App` 层新增注册互斥锁，包住 `Validate + RegisterPasswordUser + Consume`。
- 更推荐方案：新增组合方法，把“校验身份 ID + 创建用户 + 消费身份 ID”放在同一个服务编排里，并共享同一个临界区。

考虑 KISS，本项目可先使用一个明确的注册互斥锁，避免引入过重抽象。
### 4. 删除账号后的映射保留

删除本地用户账号时，不释放已使用身份 ID：

- 身份 ID 记录继续保留 `used=true`。
- 继续保留 `used_by_user_id`、`used_by_username` 和 `used_at`。
- 将 `used_user_deleted` 标记为 `true`，或在后台列表中动态判断账号是否还存在。
- 同一个身份 ID 不能因为账号删除而再次注册。
- 管理员后台应能看到该映射对应的账号已被删除。

### 5. 每日注册上限

新增后台设置项，例如 `daily_registration_limit`：

- 仅约束本地用户名密码注册 `/auth/register`。
- 不影响管理员手动创建用户。
- 不影响 LinuxDo/OAuth 首次登录。
- `-1` 表示不限制；`0` 表示当天不允许注册；大于 `0` 时表示每天最多成功注册的本地账号数。
- 统计口径建议使用服务端本地日期，按已成功消费的身份 ID `used_at` 计数。
- 达到上限后，后端直接拒绝新的本地注册请求，并返回统一错误文案。

## API 设计

### 用户注册

`POST /auth/register`

请求：

```json
{
  "identity_id": "friend-unique-string",
  "username": "alice",
  "password": "Password123",
  "name": "Alice"
}
```

失败响应统一：

```json
{
  "error": "身份 ID 无效或已使用"
}
```

不要区分：

- ID 不存在
- ID 已禁用
- ID 已使用
- ID 为空、超长或包含控制字符

### 管理接口

建议挂在现有 admin API 下：

- `GET /api/admin/registration-ids`
- `POST /api/admin/registration-ids`
- `PATCH /api/admin/registration-ids/{id}`
- `DELETE /api/admin/registration-ids/{id}`

权限要求：仅管理员。不要把身份 ID 映射接口下放给普通用户角色或可自定义用户权限。

批量导入不纳入 V1。后续实现时可增加 `POST /api/admin/registration-ids/import`，重复 ID 导入直接跳过，并返回 skipped count。

## 前端设计

### 登录/注册页

注册模式下增加两步流程：

1. 输入身份 ID。
2. 输入用户名、密码、昵称。

前端可以在第一步做轻量校验，但最终注册请求仍必须携带 `identity_id`。

建议避免新增单独“验证成功 token”流程，除非后续需要更复杂的用户体验。当前需求下，直接在最终注册时提交 `identity_id` 更简单，也更不容易绕错安全边界。

### 后台管理页

在设置或用户管理区域新增“注册身份 ID”管理：

- 添加单个身份 ID，带备注。
- 查看列表：原始身份 ID、备注、启用状态、使用状态、绑定用户名、绑定用户 ID、账号是否已删除、使用时间、创建时间。
- 禁用/启用未使用 ID。
- 删除未使用 ID。
- 已使用 ID 不允许删除或重置为未使用，避免审计记录丢失。
- 支持按绑定用户名搜索，方便管理员确认某个用户来自哪个身份 ID。

## 防绕过策略

1. `/auth/register` 必须后端强校验 `identity_id`。
2. 不允许旧请求体在开启注册后绕过身份 ID。
3. 前端“进入注册页面”不是安全边界，只是体验。
4. 身份 ID 校验失败统一错误，避免枚举。
5. 身份 ID 明文只允许管理员接口返回，普通用户接口必须过滤。
6. 每个身份 ID 成功注册后立即标记 used。
7. 注册成功时同时记录 `used_by_user_id` 和 `used_by_username`，形成可审计映射。
8. 注册接口增加失败限流：
   - IP 维度
   - `identity_id` 维度
   - username 维度
9. 身份 ID 与映射信息只允许管理员接口返回，普通用户接口必须过滤这些字段。
10. 增加每日注册数量上限，达到上限后当天拒绝继续注册。
11. 记录安全日志：
   - 注册成功绑定了哪个记录
   - 多次失败的来源 IP
   - 尝试使用已使用 ID 的行为

## 实施步骤

### Phase 1: 后端强校验

- [ ] 新增 `RegistrationGateService`
- [ ] 新增身份 ID JSON document 存取
- [ ] 修改 `/auth/register`，要求 `identity_id`
- [ ] 保证校验、创建用户、消费 ID 的并发安全
- [ ] 新增每日注册数量上限配置并在注册前校验
- [ ] 注册成功后保存身份 ID 与用户名的一一映射
- [ ] 添加后端单元测试

验收：

- 未开启注册时仍返回“已关闭注册通道”。
- 开启注册但不传 `identity_id` 时注册失败。
- 传无效 `identity_id` 时注册失败。
- 传有效未使用 `identity_id` 时注册成功。
- 同一个 `identity_id` 第二次注册失败。
- 注册成功后记录能查到 `identity_id` 对应的 `username` 和 `user_id`。
- 同一个用户不能被多个身份 ID 绑定。
- 并发请求无法重复消费同一个 `identity_id`。
- 达到每日注册上限后，当天新的本地注册请求失败。

### Phase 2: 后台管理

- [ ] 新增仅管理员可访问的管理 API
- [ ] 新增管理员专属权限声明，且不加入普通用户默认权限
- [ ] 新增后台 UI 列表
- [ ] 支持添加、启用/禁用、删除未使用 ID
- [ ] 支持查看原始身份 ID、绑定用户名、绑定用户 ID、账号是否已删除和使用时间
- [ ] 支持按绑定用户名搜索

验收：

- 管理员可以维护 ID。
- 普通用户不能访问管理接口。
- 普通用户的 `/auth/session`、`/api/profile` 等响应中不包含身份 ID、label 或映射信息。
- 注册成功后的前端页面不展示身份 ID，也不在本地 session 中保存身份 ID。
- 管理员可以清楚看到原始身份 ID 与用户名的一一映射。

### Phase 3: 注册页体验

- [ ] 注册模式增加身份 ID 输入框
- [ ] 注册请求携带 `identity_id`
- [ ] 错误提示使用统一文案
- [ ] 保持登录流程不受影响

验收：

- 用户必须输入身份 ID 才能提交注册。
- 输入有效身份 ID 后注册成功并自动登录。
- 注册成功后，普通用户后续任何页面都看不到自己的身份 ID。
- 输入无效或已使用身份 ID 时提示失败。

### Phase 4: 限流与审计

- [ ] 为 `/auth/register` 增加注册失败限流
- [ ] 增加安全审计日志
- [ ] 后台展示基础注册使用记录

验收：

- 高频失败请求被限制。
- 每日注册数达到后台配置上限后，当天拒绝继续注册；当 `daily_registration_limit=0` 时，当天不允许任何本地注册。
- 日志中能追踪成功和异常尝试。

## 原则应用

### KISS

使用“身份 ID 白名单 + 一次性消费”的直接模型，不引入完整 CDK、批次、权益兑换等复杂概念。

### YAGNI

第一阶段只实现注册准入，不实现邮件验证、短信验证、复杂邀请关系或营销兑换功能。

### DRY

身份 ID 规范化、状态校验和一次性消费集中在 `RegistrationGateService`，避免前端、路由、管理接口重复实现校验逻辑。

### SOLID

- `RegistrationGateService` 只负责注册门禁。
- `AuthService` 继续负责账号创建与登录会话。
- HTTP handler 只负责请求解析和响应，不承载核心业务规则。

## 风险与注意事项

1. 如果只做前端跳转验证，攻击者可直接调用 `/auth/register` 绕过。
2. 如果身份 ID 太短或可猜，仍可能被爆破。
3. 如果错误信息区分过细，会泄露哪些 ID 存在或已使用。
4. 如果没有并发锁，同一个 ID 可能在并发注册中被重复消费。
5. 身份 ID 明文保存后，必须确保仅管理员接口可读，并避免普通接口、前端 session 或日志泄露。
6. 已使用 ID 不能因为用户账号被删除而自动释放，否则会破坏一一映射和审计。

## 建议默认策略

- `registration_enabled=false` 仍作为总开关。
- 身份 ID 不强制字符集；仅做 trim、非空、长度上限和控制字符过滤。
- 失败统一返回“身份 ID 无效或已使用”。
- 已使用记录保留，不允许物理删除；即使对应账号被删除，该 ID 也不能再次注册。
- V1 仅支持单个添加；批量导入留到后续。
- LinuxDo/OAuth 首次登录不受身份 ID 门禁影响；白名单注册方式只约束本地用户名密码注册。








