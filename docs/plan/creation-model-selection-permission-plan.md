# Plan: 创作台模型选择权限化

## Context

当前创作台同时支持图片创作和对话模式。前端在 `web/src/app/image/components/image-composer.tsx` 渲染模型选择菜单，页面状态 `imageModel` 会在提交时传给：

- `/api/creation-tasks/image-generations`
- `/api/creation-tasks/image-edits`
- `/api/creation-tasks/chat-completions`

后端在 `internal/httpapi/routes.go` 的 `handleCreationTasks` 中直接读取 `body["model"]` 并提交到 `ImageTaskService`。对话任务最终会走 ChatGPT Web 会话链路，而上游常会把用户选择的模型重定向到实际可用的固定模型，导致用户看到的“选择模型”和实际回答模型不一致。

现有权限体系已经可复用：

- 权限目录在 `internal/service/permissions.go`。
- 登录态通过 `writeLoginResponse` 下发 `api_permissions`。
- 前端通过 `web/src/store/auth.ts` 的 `hasAPIPermission` 判断功能权限。
- 普通用户默认权限由 `DefaultPermissionSetForRole(user)` 决定。

因此本次不需要新增复杂配置或数据库模型，适合把“能否选择创作台模型”收敛成一个独立权限。

## 目标

1. 普通用户默认不能选择创作台模型。
2. 没有模型选择权限时，创作台 UI 不展示模型下拉选择，只使用默认模型。
3. 没有模型选择权限时，即使直接调用创作任务接口传入 `model`，后端也会规范化为默认模型，避免绕过前端。
4. 管理员和被授予权限的角色仍可选择模型。
5. 保留任务记录中的 `model` 字段，但未授权用户记录的是实际提交的默认模型。
6. 尽量复用现有 RBAC、session 和创作任务流程，不引入独立权限系统。

## 非目标

1. 不解决上游真实回答模型识别问题。
2. 不新增“按模型计费”“按模型额度”“按模型白名单”等细粒度能力。
3. 不改 `/v1/chat/completions`、`/v1/responses` 等 OpenAI 兼容接口的对外模型参数语义，除非后续明确要求。
4. 不引入兼容旧权限的 fallback；当前项目规则要求面向当前 API 版本实现。
5. 不改变图片生成模型链路本身，只控制创作台是否允许用户选择。

## 核心设计

### 权限定义

新增一个创作组 API 权限，建议命名为：

```go
apiPermission("POST", "/api/creation-tasks/model-selection", "选择创作模型", "创作", false)
```

这是一个能力型权限，用于 RBAC 配置和前后端判断，不需要真的暴露对应 HTTP 路由。

原因：

- 不复用 `GET /v1/models`，避免把“查看模型列表”和“允许选择模型”混在一起。
- 不复用 `POST /api/creation-tasks`，避免所有能提交创作任务的普通用户自动拥有模型选择能力。
- 使用现有 `api_permissions` 数据结构，KISS，不新增独立 capabilities 字段。

默认权限策略：

- `admin`：自动拥有全部权限，保持现状。
- 默认 `user`：不包含 `post/api/creation-tasks/model-selection`。
- 管理员可在角色权限页手动给特定角色打开该权限。

### 默认模型策略

统一使用现有默认值：

- 对话模式：`DEFAULT_CHAT_MODEL` / 后端 `"auto"`。
- 图片模式：`DEFAULT_IMAGE_MODEL` / 后端 `util.ImageModelAuto`。

未授权用户的请求处理原则：

1. 前端创建新轮次时固定使用默认模型。
2. 前端编辑历史轮次时不允许改模型；如果历史数据里有旧模型，提交前也要按权限规范化。
3. 后端提交任务时再次规范化 `model`，以服务端结果为准。

## 后端改动计划

### 1. 增加权限目录项

文件：`internal/service/permissions.go`

在创作权限组中新增：

```go
apiPermission("POST", "/api/creation-tasks/model-selection", "选择创作模型", "创作", false)
```

普通用户默认权限不添加该 key。管理员因 `allAPIPermissionKeys()` 自动拥有。

原则：

- KISS：复用现有 API 权限目录。
- YAGNI：不做模型白名单、等级、过期时间等未来能力。
- SRP：权限目录只描述能力，具体降级逻辑放在 HTTP 层。

### 2. 增加创作模型权限判断 helper

文件：`internal/httpapi/app.go` 或靠近 `identityCanAccessAPI` 的位置

建议新增：

```go
func (a *App) identityCanSelectCreationModel(identity service.Identity) bool {
    return a.identityCanAccessAPI(identity, http.MethodPost, "/api/creation-tasks/model-selection")
}
```

原则：

- DRY：避免在多个创作任务分支重复写权限 key。
- SRP：权限判断与模型规范化分开。

### 3. 增加模型规范化 helper

文件：`internal/httpapi/routes.go` 或 `app.go`

建议新增两个小函数：

```go
func (a *App) creationChatModelForIdentity(identity service.Identity, requested string) string {
    if !a.identityCanSelectCreationModel(identity) {
        return "auto"
    }
    return firstNonEmpty(util.Clean(requested), "auto")
}

func (a *App) creationImageModelForIdentity(identity service.Identity, requested string) string {
    if !a.identityCanSelectCreationModel(identity) {
        return util.ImageModelAuto
    }
    return firstNonEmpty(util.Clean(requested), util.ImageModelAuto)
}
```

如果想更简洁，也可以合并成一个带默认值参数的 helper：

```go
func (a *App) creationModelForIdentity(identity service.Identity, requested, fallback string) string
```

推荐合并版，减少重复。

### 4. 接入创作任务提交入口

文件：`internal/httpapi/routes.go`

替换 `handleCreationTasks` 三个提交分支中的模型读取：

- `image-generations`：使用 `creationModelForIdentity(identity, body["model"], util.ImageModelAuto)`
- `image-edits`：使用 `creationModelForIdentity(identity, body["model"], util.ImageModelAuto)`
- `chat-completions`：使用 `creationModelForIdentity(identity, body["model"], "auto")`

重点是后端降级而不是返回 403。原因是用户仍然有提交创作任务权限，只是没有选择模型权限；降级到默认模型更符合“暂时不提供选择”的产品目标。

## 前端改动计划

### 1. 增加权限判断

文件：`web/src/app/image/page.tsx`

引入或复用：

```ts
import { hasAPIPermission } from "@/store/auth";
```

在 `ImagePageContent` 中新增：

```ts
const canSelectCreationModel = hasAPIPermission(session, "POST", "/api/creation-tasks/model-selection");
```

### 2. 收敛模型状态

在 `composerMode` 或权限变化时，如果没有模型选择权限，将 `imageModel` 重置为当前模式默认模型：

```ts
useEffect(() => {
  if (canSelectCreationModel) {
    return;
  }
  setImageModel(composerMode === "chat" ? DEFAULT_CHAT_MODEL : DEFAULT_IMAGE_MODEL);
}, [canSelectCreationModel, composerMode]);
```

提交时 `effectiveModel` 也必须以权限为准：

```ts
const effectiveModel = !canSelectCreationModel
  ? effectiveImageMode === "chat"
    ? DEFAULT_CHAT_MODEL
    : DEFAULT_IMAGE_MODEL
  : existingModelSelectionLogic;
```

这样可避免 localStorage 里残留旧模型影响提交。

### 3. 隐藏或只读模型选择 UI

文件：`web/src/app/image/components/image-composer.tsx`

给 `ImageComposer` 增加 props：

```ts
canSelectModel: boolean;
```

无权限时建议直接不渲染模型菜单，只显示当前模式图标/默认模型的非交互状态也可以。更推荐第一阶段直接隐藏：

- UI 更简洁，符合“与其提供选择，不如暂时作为权限”的需求。
- 避免用户误以为显示的模型可配置。

如果仍需要让用户知道当前模式使用默认模型，可在结果卡片里继续展示任务 `model` 字段，不在输入区增加说明文案。

### 4. 编辑历史轮次时限制模型修改

文件：`web/src/app/image/page.tsx`

编辑弹窗里的模型 `Select` 当前总是可编辑。无权限时：

- 不展示模型 `Select`，或展示禁用态。
- 保存编辑草稿时，如果无权限，强制把 draft model 规范化为默认模型。

推荐直接隐藏编辑弹窗的模型选择行，减少误导。

## 测试计划

### 后端测试

文件：`internal/httpapi/app_test.go`

新增或扩展测试：

1. 普通用户无 `post/api/creation-tasks/model-selection` 时：
   - 提交 `/api/creation-tasks/chat-completions`，body 中传 `model: "gpt-5"`。
   - 断言任务 payload / task `model` 为 `"auto"`。
2. 普通用户拥有该权限时：
   - 提交同样请求。
   - 断言 `model` 保留为 `"gpt-5"`。
3. 管理员提交时：
   - 断言模型不被降级。
4. 图片任务可加一条轻量测试：
   - 无权限传 `codex-gpt-image-2`，断言降级为 `auto`。

### 前端验证

命令：

```bash
cd web && npm run build
cd web && npm run lint
```

手动验证：

1. 默认普通用户登录创作台：
   - 输入区不显示模型选择菜单。
   - 发送对话任务，任务记录模型为 `auto`。
2. 管理员或授予权限的角色登录：
   - 模型选择菜单正常显示。
   - 选择模型后提交，任务记录保留所选模型。
3. 普通用户 localStorage 中已有旧模型：
   - 登录后不会继续用旧模型提交。
4. 编辑历史轮次：
   - 普通用户不能修改模型。
   - 有权限用户仍可修改模型。

### 全量验证

```bash
go test ./...
```

前提：前端 embedded assets 已存在或先执行 `cd web && npm run build`。

## 风险与处理

1. **历史会话仍显示旧模型**
   - 历史任务记录应保持原样，不做迁移。
   - 新提交任务按权限降级即可。

2. **前端隐藏后仍可被直接请求绕过**
   - 后端业务层强制规范化 `model`，这是必须项。

3. **能力型权限没有真实路由**
   - 现有权限目录已是管理配置来源，不要求每个权限都有真实 handler。
   - 如果后续希望更语义化，可再引入 capabilities 字段；本次不做，避免过度设计。

4. **用户可能不理解为什么不能选模型**
   - 第一阶段不在创作台增加解释文案，避免干扰输入区。
   - 可在角色权限页通过权限名称“选择创作模型”表达管理含义。

## 迭代交付顺序

1. 后端新增权限目录项和模型规范化 helper。
2. 后端接入三个创作任务提交入口。
3. 后端补充权限降级测试。
4. 前端 session 权限判断接入创作台页面。
5. 前端隐藏模型菜单和编辑弹窗模型选择。
6. 执行 `go test ./...`、`cd web && npm run build`、`cd web && npm run lint`。

## 原则应用

- **KISS**：使用现有 RBAC 和 session 权限，不新增配置表或复杂策略。
- **YAGNI**：只做“能否选择模型”，不做模型白名单、套餐、额度、计费差异。
- **DRY**：后端用统一 helper 处理模型权限降级，前端用一个 `canSelectCreationModel` 控制所有模型选择入口。
- **SOLID / SRP**：权限目录负责声明能力，HTTP 层负责请求规范化，前端组件只负责展示是否可选。
- **OCP**：后续如果要扩展“指定角色可选模型”，只需在角色权限中授予该权限，不需要修改创作台业务流程。

