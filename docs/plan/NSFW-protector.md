# Plan: 图片 Prompt NSFW 轻量管控模块

## Summary

新增一个独立的图片 prompt 风险判断模块，用于在图片生成/图片编辑任务入队前识别明显 NSFW 请求，降低用户通过网站生成色情图片的风险。

第一版采用轻量本地规则，不接外部审核服务，不新增数据库表，不做复杂人工审核。目标是“拦明确违规，放正常创作，记录疑似风险”，避免过严误伤艺术、医学、服装、泳装、健身等正常使用。

建议文档落地路径：`docs/plan/nsfw-prompt-moderation-plan.md`

## Context

当前图片任务主要通过后端创作任务入口提交：

- `/api/creation-tasks/image-generations`
- `/api/creation-tasks/image-edits`

后端在 `internal/httpapi/routes.go` 的 `handleCreationTasks` 中读取 `body["prompt"]`，再调用 `ImageTaskService.SubmitGenerationWithOptions` 或 `SubmitEditWithOptions` 入队。

因此 NSFW 判断应放在后端入队前，不能只放前端。这样可以覆盖 Retouch 页面、普通创作台、以及直接调用 API 的用户。

可参考的开源词表：

- LDNOOBW：多语言词库，CC-BY-4.0，可作为初始素材来源。
- `@coffeeandfun/google-profanity-words`：MIT，多语言，可参考分类思路。
- `bad-words`：MIT，API 简单，但偏 JS profanity filter，不建议直接引入 Go 后端。

第一版不直接全量硬拦这些词库，而是从中筛选小规模高风险词，并维护项目自己的规则，降低误伤。

## Goals

1. 在图片生成和图片编辑任务提交前判断 prompt 风险。
2. 明确色情生成、性行为、未成年人性化、非自愿/偷拍等请求直接拒绝。
3. 对性感、泳装、人体艺术、医学、服装、电商等可能正常的 prompt 不默认硬拦。
4. 模块独立，后续可扩展词表、风险类别或接入外部 moderation 服务。
5. 第一版保持简单，不新增数据库、后台配置页、人工审核流或复杂模型分类器。

## Non-Goals

1. 不处理普通文本聊天 NSFW 内容。
2. 不对上传图片本身做视觉 NSFW 检测。
3. 不做外部 moderation API 接入。
4. 不做用户封禁、积分扣罚、账号风控联动。
5. 不做复杂绕过识别，例如谐音、拆字、emoji 替代、跨语言混写的完整对抗。
6. 不全量导入开源脏词库后逐词硬拦。

## Core Design

### 1. 风险决策

新增 prompt 审核结果：

```go
type PromptModerationDecision string

const (
    PromptModerationAllow PromptModerationDecision = "allow"
    PromptModerationWarn  PromptModerationDecision = "warn"
    PromptModerationBlock PromptModerationDecision = "block"
)
```

结果结构建议：

```go
type PromptModerationResult struct {
    Decision   PromptModerationDecision
    Categories []string
    Reason     string
}
```

第一版只需要三种结果：

- `allow`：正常通过。
- `warn`：允许通过，但记录日志或 metadata，用于后续观察误判。
- `block`：拒绝提交任务。

### 2. 风险分类

建议第一版内置以下分类：

- `explicit_sexual_content`：明确色情图片、性行为、露骨描写。
- `sexualized_minor`：未成年人、学生、儿童、少女/少年等与色情化词汇同时出现。
- `non_consensual`：偷拍、强迫、迷奸、泄露、非自愿等。
- `nudity_request`：明确要求裸体、脱衣、裸露特定部位。
- `suggestive_adult`：性感、挑逗、成人氛围、内衣、泳装等疑似但不一定违规内容。
- `benign_context`：医学、艺术、服装设计、人体结构、健身、电商模特等正常上下文。

### 3. 判定策略

第一版使用简单规则评分：

- 命中高危词组：直接 `block`。
- 命中未成年人词 + 成人色情词：直接 `block`。
- 命中非自愿/偷拍词 + 裸露/色情词：直接 `block`。
- 仅命中“性感、泳装、内衣、人体艺术”等词：`warn` 或 `allow`。
- 命中医学、艺术、服装、电商、健身等正常上下文：降低风险，不因单个成人相关词硬拦。

不要使用“命中任意一个词就 block”的策略。比如“泳装写真”“人体素描”“医学解剖图”“内衣电商模特”应默认允许或只记录。

### 4. 错误响应

当 `block` 时，创作任务接口返回 `400`，错误文案保持克制：

```text
该提示词可能涉及不适合生成的成人内容，请修改后重试
```

不返回具体命中的敏感词，避免给用户提供绕过线索。

## Backend Changes

### 1. 新增独立模块

建议文件：

- `internal/service/prompt_moderation.go`
- `internal/service/prompt_moderation_test.go`

职责：

- 规范化 prompt。
- 执行本地规则判断。
- 返回 `PromptModerationResult`。
- 不依赖 HTTP、任务队列、计费、账号系统。

原则：

- **SRP**：审核模块只负责判断 prompt 风险。
- **KISS**：第一版只用本地规则和小词表。
- **YAGNI**：不做配置中心、外部服务、复杂策略 DSL。

### 2. 规则结构

建议把词表分成几类小数组：

```go
var explicitSexualTerms = []string{...}
var nudityTerms = []string{...}
var minorTerms = []string{...}
var nonConsensualTerms = []string{...}
var suggestiveTerms = []string{...}
var benignContextTerms = []string{...}
```

第一版词表要小而明确，优先覆盖高风险表达，不追求完整。

### 3. 接入任务提交入口

建议在 `internal/httpapi/routes.go` 的 `handleCreationTasks` 中，在调用任务 service 前执行：

- `image-generations`：检查 `body["prompt"]`
- `image-edits`：检查 `body["prompt"]`
- `chat-completions`：第一版不处理，除非 `protocol.IsImageChatRequest(body)` 为 true 时后续再扩展

如果 `Decision == block`：

- 不创建任务。
- 不进入队列。
- 不扣费。
- 返回 `400`。

如果 `Decision == warn`：

- 继续创建任务。
- 可把结果写入任务 metadata，例如 `prompt_moderation_decision`、`prompt_moderation_categories`。
- 不在前端打断用户。

### 4. 日志与审计

第一版建议复用现有请求日志，不新增数据库表。

如需要保留风险信息，优先写任务 metadata 或调用日志 detail，避免新增存储结构。

注意不要在普通用户可见错误中暴露敏感词命中详情。

## Frontend Changes

第一版前端只做最小改动：

1. 当后端返回 `400` 且错误为 prompt 风险时，在创作台和 Retouch 页展示后端错误文案。
2. 不在前端维护完整词表，避免前后端规则漂移。
3. 可选：提交前做空 prompt 和基础长度校验，NSFW 判断以后端为准。

原则：

- **DRY**：审核规则只在后端维护一份。
- **KISS**：前端只展示错误，不实现复杂预审核 UI。
- **OCP**：后续可增加温和提示、改写建议或管理员开关，但第一版不做。

## Test Plan

### 后端单元测试

为 `EvaluateImagePromptModeration` 或类似函数增加测试：

1. 正常图片 prompt 返回 `allow`。
2. 明确色情图片请求返回 `block`。
3. 未成年人 + 色情化词汇返回 `block`。
4. 非自愿/偷拍 + 裸露词返回 `block`。
5. “泳装写真”“内衣电商模特”“人体艺术素描”不返回 `block`。
6. 医学或解剖上下文不因单个身体词返回 `block`。
7. 中英文混合 prompt 能命中基础英文高危词。
8. 空白、大小写、标点、重复空格规范化后判断一致。

### 后端接口测试

在 `internal/httpapi/app_test.go` 增加或扩展测试：

1. `/api/creation-tasks/image-generations` 命中 `block` 时返回 `400`，不创建任务。
2. `/api/creation-tasks/image-edits` 命中 `block` 时返回 `400`，不创建任务。
3. 正常 prompt 仍可创建任务。
4. `warn` prompt 仍可创建任务，并可在任务 metadata 中看到审核结果。
5. 被拒绝的任务不进入 queued/running 状态。

### 验证命令

```bash
go test ./...
```

如涉及前端错误展示：

```bash
cd web
npm run build
npm run lint
```

## Risks

1. **误伤正常创作**
   应对：不全量硬拦开源词库；使用高危组合规则；对艺术、医学、服装、电商等正常上下文降权。

2. **规则被绕过**
   应对：第一版接受有限覆盖，先拦明显违规；后续基于日志样本补充规则或接外部 moderation。

3. **词表维护失控**
   应对：保持小词表，分类清晰；新增词必须对应测试；避免把所有 profanity 都当 NSFW。

4. **用户看到绕过线索**
   应对：错误文案不返回命中词，只返回通用提示。

## Principles

- **KISS**：第一版只做本地轻量规则，不引入外部服务和复杂审核流。
- **YAGNI**：不提前做后台配置、人工审核、封禁、视觉检测。
- **DRY**：审核逻辑集中在一个后端模块，前端不复制规则。
- **SOLID / SRP**：审核模块、HTTP 入口、任务队列职责分离。
- **OCP**：后续可以扩展规则、词表来源或接入外部 moderation，而不改变任务提交主链路。

## Milestones

### MVP

1. 新增 `prompt_moderation` 服务模块。
2. 实现小规模分类词表和 `allow / warn / block` 判定。
3. 在图片生成和图片编辑任务入队前接入。
4. 增加单元测试和接口测试。
5. 前端复用现有错误展示。

### 1.1

1. 根据真实日志补充误判测试样本。
2. 对 `warn` prompt 做后台统计或管理员可见标记。
3. 可选支持配置文件覆盖词表，但不做后台 UI。

### 1.2+

1. 评估是否接入外部 moderation API。
2. 增加图像输入本身的 NSFW 检测。
3. 根据账号风险、频率和历史行为做更细粒度策略。
