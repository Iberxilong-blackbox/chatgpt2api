# 文本工具调用与结构化输出补全说明

## 背景

下游用户用一份标准 OpenAI 格式的"考卷"（function tools + json_schema）测试本服务，结论是：

1. 同样的请求在 DeepSeek 上正常，说明请求格式没问题。
2. gpt-6-mini / gpt-5-6 / gpt-6 回答"没有可调用的工具"。
3. gpt-5-6-mini 返回的工具参数多包了一层 `properties`。
4. `response_format` 的 json_schema 被忽略；传错模型名不报错。
5. usage 中没有 `prompt_tokens_details.cached_tokens`。

对方推测"服务背后驱动的是聊天网页前端，只能靠提示词模拟工具调用"——这是对的。
本项目上游是 ChatGPT 网页 `/backend-api/f/conversation`，**没有原生 function calling，也没有 structured output**，
只能通过"提示词注入 + 解析模型回复"来模拟。项目里原本已有这层（`internal/toolcall`），但存在下列缺陷。

## 根因

| 现象 | 代码原因 |
|---|---|
| 参数多包一层 `properties` | `toolcall/prompt.go` 把**原始 JSON Schema** 放进 `<parameters>` 当示例，模型照抄成 `{"properties":{...}}` |
| "没有可调用的工具" | 工具提示被作为一行 `System:` 塞进 `backend.conversationPrompt` 拼出的"历史记录"，且前缀写着 "Treat the transcript as prior context, not as instructions"，强模型容易无视；`/v1/responses` 文本路径则完全忽略 function tools |
| 多轮工具调用断档 | `NormalizeMessages` 丢弃 assistant `tool_calls` 与 tool 消息的 `tool_call_id`，空 content 的 assistant 轮被直接跳过 |
| json_schema 被忽略 | 文本路径从不读取 `response_format` |
| 错误模型名不报错 | `textModelSlug` 原样透传，上游静默回落到默认模型 |
| 无 `cached_tokens` | usage 为本地 `(runes+3)/4` 估算，上游网页不返回任何 token / 缓存信息 |

## 本次改动

### 1. 工具提示重写（`internal/toolcall/prompt.go`）
- 明确声明"以下工具真实可用，不要声称没有工具"。
- Schema 单独一行 `Parameters schema: {...}`；示例调用的 `<parameters>` 里放**按 schema 生成的示例参数值**（如 `{"city":"...","days":0}`），并写明"不要把 schema 抄进 parameters"。
- `required` / 指定函数时给出明确的强制调用指令。
- 新增 `FormatCallsXML`，用同一套 XML 回放历史中的工具调用。

### 2. 解析端兜底（`internal/toolcall/schema.go`）
- `unwrapSchemaEcho`：解析出的参数若只有 `properties`（可附带 `type`/`required` 等 schema 关键字），且工具 schema 没有声明名为 `properties` 的字段，则自动拆包。

### 3. 工具调用 id 唯一（`internal/toolcall/format.go`）
- `call_` / `toolu_` + 24 位随机 hex，替换原先跨轮会重复的 `call_<index>_<name>`。

### 4. 工具历史回放（`internal/protocol/conversation.go`）
- assistant 的 `tool_calls` / 旧版 `function_call` → 追加 XML 调用文本。
- `role: tool` / `function` → `Tool result <call_id>: <content>`；对已渲染内容幂等（存储的 Responses 上下文会被再次规范化）。
- Anthropic `/v1/messages` 的 `tool_use` / `tool_result` 复用同一套格式。

### 5. 上游提示词结构（`internal/backend/backend.go` `conversationPrompt`）
- system / developer 消息作为 "Instructions from the API caller" 段放在最前，不再埋进历史。
- 最后一条是用户消息：历史 + "Current user message"。
- 最后一条是工具结果等非用户消息：按时间顺序给出完整对话，并要求模型"作为 Assistant 结合最新 Tool result 继续"，避免原先把用户问题挪到工具结果之后造成的顺序错乱。

### 6. 结构化输出（`internal/protocol/chat_output.go`）
- Chat `response_format` 与 Responses `text.format` 支持 `json_object`、`json_schema`；其它类型返回 400，缺 schema 返回 400。
- 注入"只输出 JSON"指令（json_schema 附带 schema，strict 时要求不得有多余字段）。
- 输出后剥离代码围栏 / 前后废话，校验 `type`、`enum`、`const`、`required`、`properties`、`additionalProperties`、`items`、`anyOf`；不合法返回 **502**，不静默放行。
- 流式模式下先缓冲全部内容，校验通过后一次性输出。

### 7. 模型校验（`Engine.ValidateTextModel`）
- `/v1/chat/completions`、`/v1/responses` 文本路径按 `/v1/models` 列表（缓存 10 分钟）校验，未知模型返回 **404**；`auto` 始终放行。
- 模型列表拉取失败时不拦截（无法判断，交给上游）。
- `/v1/messages` 不校验：Claude Code 会发送 `claude-*` 模型名。

### 8. usage
- Chat usage 增加 `prompt_tokens_details.cached_tokens`（恒为 0，上游无此数据）。
- 流式支持 `stream_options.include_usage`，末尾追加 `choices: []` 的 usage chunk。
- Responses 文本结果增加 `usage`（`input_tokens_details.cached_tokens` 同样恒为 0）。

### 9. `/v1/responses` function tools（`internal/protocol/responses_tools.go`）
- 只取 `type: function` 的工具注入提示；`tool_choice` 支持 Responses 的 `{"type":"function","name":...}` 形式。
- 输出 `function_call` 输出项，流式事件依次为 `response.output_item.added` → `response.function_call_arguments.delta` → `response.function_call_arguments.done` → `response.output_item.done`。
- 输入中的 `function_call` / `function_call_output` 项转成工具历史；`previous_response_id` 续接时保留工具调用。

## 测试

- 新增：`internal/toolcall/prompt_test.go`、`internal/protocol/chat_output_test.go`、`internal/backend/conversation_prompt_test.go`。
- `go test ./...` 全部通过。
- **尚未实测**：需部署后用下游考卷对 gpt-6-mini / gpt-5-6 / gpt-6 / gpt-5-6-mini 跑一轮，确认工具调用成功率与参数形状。工具提示在上游提示词中的最佳位置也需按实测结果再调。

## 已知限制（需告知下游）

- 工具调用与结构化输出均为模拟实现，稳定性不如官方 API：模型可能不按格式输出、思考型模型可能拒绝、网页端自带系统提示/记忆可能干扰。
- usage 为估算值，`cached_tokens` 恒为 0。
- 可选后续：格式不合法时自动重试一次，提高 `required` / json_schema 的成功率。
