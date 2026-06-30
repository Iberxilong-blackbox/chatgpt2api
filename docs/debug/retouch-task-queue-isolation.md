# 修图页任务未出现在任务队列中的分析

## 现象

在 retouch（局部修图）页面进行 prompt 修图时，页面内有倒计时和状态反馈，但页面顶部的**任务队列组件**（`ImageTaskQueue`）中完全看不到该任务。只有在创作台（`/image`）发起的生图/修图请求才会出现在任务队列中。

## 原因分析

### 架构概览：两个独立的任务追踪系统

| 维度 | 创作台 (`/image`) | 修图 (`/retouch` / `EditorPage`) |
|---|---|---|
| **持久化存储** | `image-conversations` (IndexedDB) | `retouch-history` (IndexedDB) |
| **任务队列数据源** | `image-conversations` 中的 `ImageTurn` | 不写入 `image-conversations` |
| **队列感知** | `saveImageConversation()` → 触发 `IMAGE_CONVERSATIONS_CHANGED_EVENT` → 队列组件刷新 | `saveRetouchHistorySession()` → 触发 `RETOUCH_HISTORY_CHANGED_EVENT` → 队列组件不监听此事件 |
| **轮询机制** | 基于存储的轮询（通过 `updateConversation()` 更新持久化数据） | 本地 ref + React state 自轮询，仅存在于内存 |

### 根本原因

**修图页跳过了 `image-conversations` 存储的生命周期。**

任务队列组件（`ImageTaskQueue`）**仅**从 `image-conversations`（IndexedDB）读取数据，通过监听 `IMAGE_CONVERSATIONS_CHANGED_EVENT` 刷新。其过滤逻辑为：查找 `status === "queued"` 或 `status === "generating"` 的 `ImageTurn`，或其下有 `image.status === "loading"` 的轮次。

修图页的提交流程：
1. 直接调用 `POST /api/creation-tasks/image-edits`
2. 将返回的 `CreationTask.id` 存在本地 `activeTaskIdRef`（useRef）中
3. 自管理轮询（每 2s 调 `GET /api/creation-tasks?ids=...`）
4. 完成后通过 `addNode()` 更新图像树、通过 `saveRetouchHistorySession()` 更新修图历史

整个过程**从未**：
- 在 `image-conversations` 中创建 `ImageTurn`
- 调用 `saveImageConversation()` 或 `saveImageConversations()`
- 触发 `IMAGE_CONVERSATIONS_CHANGED_EVENT`

### 相关代码位置

- 修图提交入口：`web/src/pages/EditorPage.tsx` ~L645-777
- 修图历史存储：`web/src/store/retouch-history.ts`
- 图像对话存储：`web/src/store/image-conversations.ts`
- 任务队列组件：`web/src/components/image-task-queue.tsx`
- 创作台提交入口：`web/src/app/image/page.tsx` ~L2764-2903

## 潜在后果与 Bug

### 1. 任务队列中完全缺失修图任务（已暴露）
用户无法通过顶栏任务队列监控修图进度，只能依赖页面内的倒计时。

### 2. 页面刷新导致任务孤儿
创作台刷新后可恢复进行中的任务（`recoverConversationHistory()`），修图页的 `activeTaskIdRef` 是内存变量——**刷新即丢失**。如果任务在服务端实际成功完成但前端轮询被中断，结果图像可能已生成但再也无法挂载到版本树。

### 3. 无重试机制
创作台可以基于 `image-conversations` 中持久化的 `taskStatus` 对失败任务重试。修图页没有等效的重试入口。

### 4. 取消操作无持久化痕迹
`handleCancelGeneration` 只发 `cancelCreationTask()` API 调用，不更新任何存储。用户或系统无法追溯某次修图曾取消。

### 5. 并发门控脆弱
`generationRunIdRef` 作为简单的运行 ID 门控，用户在旧任务未完成时发起新生成，旧任务结果会被 `applyTerminalTask` 的 `runId` 守卫丢弃（即静默忽略）。没有像创作台那样的队列排队机制。

### 6. 误导性空状态提示
任务队列组件的空状态文案："在创作台提交图片或对话任务后，这里会显示对应的处理详情和进度。" 修图不会被提及，用户可能误判系统行为。
