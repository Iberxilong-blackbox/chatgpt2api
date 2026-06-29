# Retouch B-lite 历史方案开发计划

更新时间：2026-06-29

## 1. 方案定位

本方案命名为：

```text
方案 B-lite：Retouch 专用历史 store，参考创作台历史实现
```

目标是在 Retouch 页面中新增独立的历史项目能力，让用户可以在不同日期继续找回以前的局部修图项目、缩略图、prompt、版本树和当前编辑位置。

本方案不把 Retouch 强行塞进创作台现有 `ImageConversation` 数据结构，而是复用创作台历史的实现思路：

- 使用 `localforage` 持久化到 IndexedDB。
- 按当前登录身份隔离历史数据。
- 使用自定义事件通知历史变化。
- 历史列表按 `updatedAt` 倒序展示。
- 支持单个历史删除和全部清空。

Retouch 自己保留版本树数据模型，保证能还原 `useImageTreeStore` 中的父子关系、图片编号、当前节点和后续继续编辑能力。

## 2. 背景与问题

当前 Retouch 主路径已经支持一次完整局部修图：

- 上传图片。
- 在图片上涂鸦标记。
- 输入 prompt。
- 提交图片编辑任务。
- 等待生成。
- 成功后左右对比。
- 从任意已有图片继续编辑。
- 基础树状 minimap 可回看版本节点。

现有问题是 Retouch 页面缺少长期历史：

1. 用户第一天对 A 图做了一系列修改，第二天想对 B 图开始新修改时，只能移除 A 图。
2. A 图的版本树、缩略图、prompt 和生成结果没有像创作台那样形成历史入口。
3. 当前上传图使用 `URL.createObjectURL(file)` 生成临时地址，刷新或隔天后不适合作为可恢复历史。
4. Retouch 的版本树是分支结构，创作台的 `conversation -> turns -> images` 更偏线性，不适合直接复用为唯一模型。

## 3. 目标

第一阶段目标：

1. Retouch 页面支持多个历史项目。
2. 每个历史项目能保存并恢复完整版本树。
3. 用户可从历史入口打开旧项目，继续在任意历史节点上修图。
4. 每个历史项目展示缩略图、标题、更新时间和基础统计。
5. 历史数据按登录身份隔离，匿名用户也有独立历史。
6. 第一版只做本浏览器本账号下可恢复，不做跨设备同步。

## 4. 非目标

第一阶段不做：

1. 后端数据库持久化 Retouch 项目。
2. 跨设备同步。
3. 搜索、标签、收藏、批量管理。
4. 多人协作或共享历史。
5. 复杂历史动画。
6. 完整图片资产治理策略重构。
7. 将 Retouch 历史和创作台历史合并成同一个列表。

## 5. 核心设计

### 5.1 数据结构建议

新增 Retouch 专用历史类型，建议放在 `web/src/store/retouch-history.ts`。

```ts
export type RetouchHistorySession = {
  id: string;
  title: string;
  createdAt: string;
  updatedAt: string;
  thumbnailUrl?: string;
  draftPrompt?: string;
  nodesById: Record<string, StoredRetouchImageNode>;
  rootNodeId: string | null;
  currentNodeId: string | null;
  nextImageNumber: number;
};
```

节点结构应与 `useImageTreeStore` 的 `ImageNode` 接近，但使用可持久化字段：

```ts
export type StoredRetouchImageNode = {
  id: string;
  parentId: string | null;
  baseImage: StoredRetouchImageAsset;
  generatedImage?: StoredRetouchImageAsset;
  maskData?: string;
  prompt: string;
  childrenIds: string[];
  createdAt: string;
};
```

图片资产建议显式标记来源，避免恢复时误判：

```ts
export type StoredRetouchImageAsset = {
  id: string;
  url: string;
  name?: string;
  sequenceNumber?: number;
  width?: number;
  height?: number;
  source?: "upload" | "generated";
};
```

### 5.2 Store 职责

`retouch-history.ts` 只负责历史持久化，不负责画布绘制和任务提交。

建议导出：

```ts
listRetouchHistorySessions()
saveRetouchHistorySession(session)
deleteRetouchHistorySession(id)
clearRetouchHistorySessions()
```

内部实现参考 `image-conversations.ts`：

- `localforage.createInstance({ name: "chatgpt2api", storeName: "retouch_history" })`
- 使用当前 `auth` session 生成 scope key。
- 写入操作串行排队，避免快速连续保存导致旧数据覆盖新数据。
- 保存前做 normalize，修复缺失字段、非法时间和空标题。
- 写入后触发 `chatgpt2api:retouch-history-changed`。

### 5.3 `useImageTreeStore` 调整

当前 `useImageTreeStore` 已经维护：

- `nodesById`
- `rootNodeId`
- `currentNodeId`
- `nextImageNumber`
- `addRootNode`
- `addNode`
- `navigateNode`
- `resetTree`

为了支持历史恢复，建议新增轻量 action：

```ts
replaceTree(snapshot)
exportTree()
```

也可以不新增 `exportTree()`，由页面直接读取 Zustand state 后组装 session；但 `replaceTree()` 更建议放在 store 内部，避免页面直接知道太多初始化细节。

设计原则：

- **SRP**：树 store 只管当前工作区树状态。
- **DRY**：历史恢复不重复 `addRootNode + addNode` 重建逻辑。
- **KISS**：第一版只做整棵树替换，不做节点级增量 merge。

### 5.4 Retouch 页面接入点

Retouch 页面需要在以下时机同步历史：

1. 上传第一张图片后：
   - 创建新的 Retouch session。
   - 写入 root node。
   - 设置当前 active session id。
   - 初始化 `draftPrompt` 为空字符串。

2. 生成成功后：
   - `addNode()` 创建新版本节点。
   - 保存当前树快照到 active session。
   - 更新 `thumbnailUrl` 为当前节点预览图。
   - 保留或清空输入框 prompt 由页面当前交互决定，但每次保存 session 时同步当前 `draftPrompt`。

3. 点击 minimap 导航节点后：
   - 更新 `currentNodeId`。
   - 保存 active session 的当前位置。

4. prompt 输入变化时：
   - 同步保存当前 active session 的 `draftPrompt`。
   - 可做轻量 debounce，避免每个字符都立即写 IndexedDB。

5. 删除或清空当前工作区时：
   - 只清空页面当前树，不默认删除历史 session。
   - 是否删除历史由历史面板中的明确删除入口控制。

6. 打开历史 session：
   - 调用 `replaceTree()` 恢复树。
   - 设置 active session id。
   - 恢复页面状态到 `editing` 或 `success_split`。
   - 恢复 `draftPrompt` 到输入框。

## 6. 图片持久化策略

这是本方案最重要的决策点。

当前上传图通过 `URL.createObjectURL(file)` 生成临时 URL，不适合作为历史保存。生成图如果来自后端稳定 URL，恢复风险较低；如果是 data URL，也会增加 IndexedDB 体积。

第一阶段有三个可选策略：

### 方案 1：上传图转 data URL 存入历史

上传时将 File 转为 data URL，`baseImage.url` 使用 data URL。

优点：

- 实现最简单。
- 刷新和隔天可恢复。
- 不依赖后端新 API。

缺点：

- IndexedDB 体积增长明显。
- 大图会让历史存储变重。
- 需要后续补清理策略。

### 方案 2：上传图保存 Blob 到 IndexedDB，节点只存 asset id

新增本地图片资产 store，例如 `retouch_assets`，session 节点只引用 asset id。

优点：

- 比 data URL 更适合存二进制。
- 后续清理、去重更容易。

缺点：

- 第一版复杂度更高。
- 需要维护 asset 引用和清理。

### 方案 3：上传图先上传/落盘到后端图片资产

Retouch 的上传原图也进入后端图片存储，历史只保存稳定 path。

优点：

- 最适合长期和跨设备扩展。
- 与后端图片治理更一致。

缺点：

- 需要后端 API 和权限设计。
- 超出第一阶段“本地历史”的轻量范围。

第一版建议：

```text
优先选方案 1：上传图转 data URL 存入 Retouch session。
```

理由是它最符合 B-lite 的边界：不新增后端能力，先解决用户隔天找回历史的问题。后续如果 IndexedDB 体积成为真实问题，再演进到 Blob asset store。

## 7. 历史 UI 设计

第一版建议使用 Retouch 页面内独立历史入口，不和创作台历史混在一起。

入口建议：

- 页面顶部或版本树区域增加“历史记录”按钮。
- 点击后打开 Dialog 或 Sheet。
- 移动端优先 Dialog，桌面端可使用侧栏或弹窗。

列表项展示：

- 缩略图。
- 标题。
- 更新时间。
- 图片数量或版本数量。
- 当前状态标识，例如“共 5 张图 / 4 次修图”。
- 删除按钮。

打开行为：

- 点击历史项恢复该 session。
- 如果当前工作区有未保存状态，第一版可以先自动保存当前 active session，再切换。
- 不做复杂冲突提示，除非后续发现误操作风险高。

标题生成建议：

- 优先使用用户上传文件名。
- 其次使用第一轮 prompt 的前若干字符。
- 最后 fallback 为“未命名修图”。

## 8. 状态恢复规则

恢复历史时根据当前节点判断页面状态：

1. 当前节点只有 `baseImage`，没有 `generatedImage`：
   - 恢复为 `editing`。
   - 画布显示 `baseImage`。

2. 当前节点有 `generatedImage`：
   - 恢复为 `success_split`。
   - 左侧显示 `baseImage`，右侧显示 `generatedImage`。
   - `splitSelection` 默认为 `null`，用户需选中一张图后继续编辑。

3. 历史中存在异常节点：
   - normalize 时尽量修复。
   - 无法修复的节点不参与展示。
   - 如果 root 缺失，忽略该 session 或标记为不可恢复。

第一版恢复：

- 已提交的每轮 prompt：保存在对应 `StoredRetouchImageNode.prompt` 中。
- 当前输入框 prompt 草稿：保存在 `RetouchHistorySession.draftPrompt` 中，打开历史时恢复到输入框。

第一版不恢复：

- 画布当前未发送的涂鸦草稿。
- 正在运行中的任务进度。

涂鸦草稿和运行中任务属于后续增强，避免第一版状态过重。

## 9. 与真实 API 接入的关系

Retouch 历史可以和真实 `/api/creation-tasks/image-edits` 接入并行推进，但建议顺序是：

1. 先补 Retouch 历史的本地 session 和版本树恢复。
2. 再接真实图片编辑 API，或在真实 API 接入时复用保存时机。

如果真实 API 已经接入，保存规则不变：

- 创建任务成功但未生成结果：第一版不新增历史节点。
- 生成成功后：创建新节点并保存 session。
- 生成失败或取消：不创建新图片编号，不新增结果节点。

## 10. 风险与处理

### 风险 1：IndexedDB 体积变大

原因：

- 上传图和生成图可能以 data URL 存储。

第一版处理：

- 只保存 Retouch 历史必须恢复的图片。
- 历史列表提供删除和清空。
- 后续再增加容量统计、保留天数或 Blob asset store。

### 风险 2：版本树和历史 session 状态不一致

原因：

- 页面状态与持久化状态分离。
- 快速连续生成或导航可能发生保存覆盖。

处理：

- 写入队列串行化。
- session 使用 `updatedAt` 做新旧判断。
- 每次保存整棵树快照，不做局部 merge。

### 风险 3：创作台历史和 Retouch 历史重复实现

处理：

- 第一版允许轻度重复，保持职责清晰。
- 只复用小 helper 或实现模式，不提前抽泛型历史框架。
- 如果两个 store 后续稳定后重复明显，再提取 shared helper。

### 风险 4：旧 object URL 无法恢复

处理：

- 新历史写入必须把上传图转换为可持久化 URL。
- 不承诺迁移历史功能上线前的临时 object URL。

## 11. 分阶段计划

### 阶段 1：历史数据模型与持久化

目标：

- 新增 `retouch-history.ts`。
- 支持 list/save/delete/clear。
- 按登录身份隔离。
- 写入后触发 changed event。

验收标准：

- 上传并保存一个 session 后，刷新页面仍能从 IndexedDB 读到。
- 删除单个 session 后不再出现在列表。
- 清空历史后当前 scope 下列表为空。

### 阶段 2：版本树导入导出

目标：

- `useImageTreeStore` 支持整树恢复。
- Retouch 页面能从 session 恢复 root、current node 和 next image number。

验收标准：

- 多分支版本树保存后，刷新再打开仍保留父子关系。
- minimap 能展示恢复后的版本树。
- 继续编辑旧节点时，新节点编号继续递增，不从 `#1` 重置。

### 阶段 3：Retouch 历史 UI

目标：

- Retouch 页面增加历史入口。
- 展示 session 列表、缩略图、标题、更新时间和版本数量。
- 支持打开、删除、清空。

验收标准：

- 用户可以在 A 图项目和 B 图项目之间切换。
- 删除 B 图项目不会影响 A 图项目。
- 打开历史项目后可继续局部修图。

### 阶段 4：图片持久化修正

目标：

- 上传图不再只保存 `object URL`。
- 第一版将上传图转成 data URL 写入 session。
- 生成图优先保存稳定 URL；如果只有 base64/data URL，也按现状保存。

验收标准：

- 刷新页面或隔天打开，上传原图仍可展示。
- 从上传原图节点继续编辑时，能重新构造 File 并提交编辑任务。

### 阶段 5：体验打磨

目标：

- 自动标题更自然。
- 切换历史前自动保存当前项目。
- 空历史、加载中、删除确认等状态完整。

验收标准：

- 历史列表无项目时有明确空态。
- 删除和清空有确认。
- 快速切换项目不丢失当前版本树。

## 12. 已确认决策与剩余问题

### 决策 1：上传图第一版直接存 data URL

结论：

```text
是。第一版用 data URL，后续再演进 Blob asset store。
```

剩余注意：

- 本地历史占用会变大。
- 第一版先不额外限制单张上传图大小，复用现有上传校验；如果后续出现 IndexedDB 体积问题，再增加容量提示、保留策略或 Blob asset store。

### 决策 2：Retouch 历史不和创作台历史合并展示

结论：

```text
第一版 Retouch 页面内单独展示。
```

原因：

- Retouch 是版本树，创作台是对话时间线。
- 合并展示会增加筛选、入口和数据模型复杂度。

### 决策 3：保存 prompt 草稿，不保存涂鸦草稿

结论：

```text
第一版保存当前输入框 prompt 草稿；不保存画布当前未发送的涂鸦草稿。
```

原因：

- prompt 是历史上下文的一部分，保存成本低。
- 涂鸦草稿涉及 canvas 路径、mask 导出和图片尺寸绑定，第一版先不增加这部分复杂度。
- 已提交的每轮 prompt 始终保存在版本树节点中，不依赖 `draftPrompt`。

### 决策 4：切换历史时自动保存当前工作区

结论：

```text
自动保存当前 active session，然后切换。
```

原因：

- 行为接近创作台历史。
- 避免每次切换都打断用户。

### 决策 5：失败任务不保存为版本树节点

结论：

```text
第一版不保存为版本树节点。
```

原因：

- 失败任务不产生新图片编号，符合当前 Retouch 规则。
- 可后续在 session metadata 中记录最近错误，但不进入树。

## 13. 验证命令

涉及前端 store 和页面 UI，建议验证：

```text
cd web && npm run build
cd web && npm run lint
```

如后续引入前端单元测试，再补充对应测试命令。

## 14. 当前建议结论

建议按以下产品边界进入开发：

```text
Retouch 独立历史 store。
参考创作台历史的 localforage、scope、排序、删除和事件模式。
保留 Retouch 版本树为核心数据模型。
第一版使用 data URL 解决上传图持久化。
第一版只保证本浏览器本账号恢复，不做跨设备同步。
```

