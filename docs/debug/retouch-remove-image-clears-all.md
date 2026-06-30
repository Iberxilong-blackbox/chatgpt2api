# Retouch "移除图片" 按钮误清空全部版本树

## 现象描述

在 Retouch 页面经过多轮编辑交互后（假设版本树中共有 4 张图，右侧版本树 sidebar 可看到 4 个缩略图），点击左侧工具栏的 **"移除图片"** 按钮后：

1. **主画布**：所有图片消失，回到初始上传界面（"拖入一张图片开始编辑"）
2. **版本树 sidebar**：右侧版本树面板消失（因为 `rootNodeId` 被置 null）
3. **历史记录**：打开历史对话框，只能看到该会话的初始输入图片缩略图

用户预期：移除当前正在编辑的**一张**图片，而非摧毁整个工作区。

## 复现步骤

1. 进入 `/retouch` 页面
2. 上传一张图片 → 进入编辑界面
3. 输入提示词并生成 → 版本树出现第 2 个节点（generatedImage）
4. 继续在生成结果上编辑 → 版本树出现第 3、4 个节点
5. 点击左侧工具栏的 **"移除图片"** 按钮
6. 观察到整个工作区清空，所有版本丢失

## 根因分析

### 核心问题：按钮语义与实际行为严重不符

按钮名称为 **"移除图片"**（暗示移除单张图），但实际调用的是 `handleRemoveImage`，其内部行为等价于 **"重置整个工作区"**。

### 关键代码调用链

#### 1. 按钮渲染位置

**文件：** `web/src/pages/EditorPage.tsx:965-972`

```tsx
<button
  type="button"
  onClick={() => void handleRemoveImage()}
  className="inline-flex h-10 w-full items-center gap-2 rounded-full bg-slate-950 ..."
>
  <Trash2 className="size-4" />
  移除图片
</button>
```

按钮位于左侧工具栏（通过 `createPortal` 渲染到 `document.body`），与"历史"、"更换图片"同级。

#### 2. `handleRemoveImage` 函数体

**文件：** `web/src/pages/EditorPage.tsx:504-520`

```tsx
const handleRemoveImage = useCallback(async () => {
  await persistActiveSession(prompt);     // Step 1: 保存当前版本树快照到历史
  generationRunIdRef.current += 1;        // Step 2: 作废任何进行中的生成
  activeTaskIdRef.current = null;         // Step 3: 清除活跃任务 ID
  resetTree();                             // Step 4: ★★★ 清空整个图片树 ★★★
  setActiveSessionId(null);               // Step 5: 断开与当前会话的连接
  setSourceFilesByAssetId({});            // Step 6: 清空缓存的 File 引用
  setPrompt("");                          // Step 7: 清空提示词
  setHasCanvasMarks(false);               // Step 8: 重置所有页面状态
  setMarkerColor(null);
  setPendingSourceImage(null);
  setPendingMaskData(undefined);
  setErrorMessage("");
  setSplitSelection(null);
  setPageStatus("empty");                 // Step 9: 回到初始上传界面
  setGeneratingStartedAt(null);
}, [persistActiveSession, prompt, resetTree]);
```

**关键点：** 虽然第 1 步 `persistActiveSession` 在清空前保存了快照到历史，但之后的 `resetTree` 和 `setActiveSessionId(null)` 导致：
- 当前工作区的版本树被完全摧毁
- 当前会话连接被切断，后续自动保存失效
- 用户被迫回到初始上传状态

#### 3. `resetTree()` — 真正造成数据清空的函数

**文件：** `web/src/store/useImageTreeStore.ts:194-201`

```ts
resetTree: () => {
  set({
    nodesById: {},      // 所有版本树节点全部删除
    rootNodeId: null,    // 根节点引用清除
    currentNodeId: null, // 当前节点引用清除
    nextImageNumber: 1,  // 图片序号计数器重置为 1
  });
}
```

#### 4. `resetTree` 影响的数据流

**版本树缩略图来源：** `web/src/pages/EditorPage.tsx:354`

```tsx
const imageList = useMemo(() => getUniqueImages(nodesById), [nodesById]);
```

**`getUniqueImages` 函数：** `web/src/pages/EditorPage.tsx:256-265`

```ts
function getUniqueImages(nodesById: Record<string, ImageNode>) {
  const images = new Map<string, ImageTreeAsset>();
  for (const node of Object.values(nodesById)) {
    images.set(node.baseImage.id, node.baseImage);
    if (node.generatedImage) {
      images.set(node.generatedImage.id, node.generatedImage);
    }
  }
  return Array.from(images.values())
    .sort((a, b) => (a.sequenceNumber ?? 0) - (b.sequenceNumber ?? 0));
}
```

`nodesById` 被 `resetTree()` 置为 `{}` 后，`imageList` 变为空数组，右侧版本树 sidebar 和主画布全部消失。

### 与 `resetWorkspace` 的重复

**文件：** `web/src/pages/EditorPage.tsx:576-591`

```tsx
const resetWorkspace = useCallback(() => {
  generationRunIdRef.current += 1;
  activeTaskIdRef.current = null;
  resetTree();
  setActiveSessionId(null);
  setSourceFilesByAssetId({});
  setPrompt("");
  // ... 完全相同的一系列 setState ...
  setPageStatus("empty");
}, [resetTree]);
```

`handleRemoveImage` 与 `resetWorkspace` 的逻辑**几乎完全一致**，说明代码层面两者就是同一件事。`resetWorkspace` 在删除历史会话时使用（第 633 行、第 640 行），用于清空工作区。

### 历史对话框预览仅显示初始图片的原因

**文件：** `web/src/store/retouch-history.ts:159-163`

```ts
export function getRetouchSessionPreviewUrl(session) {
  const currentNode = session.currentNodeId
    ? session.nodesById[session.currentNodeId]
    : null;
  const preview = currentNode?.generatedImage ?? currentNode?.baseImage;
  return preview?.url;
}
```

历史列表中的缩略图取自 `currentNode` 的预览。如果当前节点是根节点（原始上传图片），则缩略图就是原始图片。但这是历史对话框的**显示行为**，与数据是否丢失无关。

### 用户感知数据丢失的风险

`persistActiveSession` 在 `resetTree` 之前调用（`handleRemoveImage` 第 1 步），理论上会把版本树快照保存到历史。用户可通过打开历史、点击对应会话恢复所有节点（`handleOpenHistorySession` → `replaceTree`）。

但真实使用中仍然会产生“历史也找不到”的强烈丢失感，原因包括：

1. `resetTree()` 后 `currentNode` 变成 `null`，左侧工具栏不再渲染，当前页面上连“历史”入口也消失
2. 历史列表缩略图只取 `currentNode` 的预览；如果保存时当前节点仍是根节点，用户看到的就是初始上传图片，容易误判为多轮生成结果没有保存
3. 历史保存是本浏览器、当前账号作用域下的 IndexedDB 记录；如果保存失败、账号作用域变化、浏览器存储被清理，用户确实无法从历史恢复
4. 当前按钮没有任何确认或解释，用户不会知道这个操作只是清空工作区，而不是删除单张图

因此不能把它简单归类为“用户误解”。当前交互本身存在高风险：它把清空工作区伪装成了移除图片，并且在清空后隐藏了恢复入口。

整体体验问题：
1. 用户必须手动导航到历史、找到对应会话、点击恢复
2. 没有任何确认/撤销机制
3. 用户不知道数据可以通过历史恢复

### 历史删除与“删除当前版本”的区别

历史对话框中的删除按钮位于每个 `RetouchHistorySession` 项目上：

```tsx
onClick={() => setDeleteConfirm({ type: "one", id: session.id })}
```

确认后调用：

```tsx
await deleteRetouchHistorySession(target.id);
```

这删除的是整个 Retouch 项目/会话，也就是该会话的完整版本树快照和本地历史入口。它不是“删除当前版本节点”。

如果未来要支持“删除当前版本”，需要在 `useImageTreeStore` 中对树节点做局部删除，例如删除 `currentNode` 及其子分支，然后回退到父节点，并保存当前会话。这个能力和当前历史删除不是同一件事。

## 涉及的代码文件与行号汇总

| 文件 | 行号 | 内容 | 角色 |
|---|---|---|---|
| `web/src/pages/EditorPage.tsx` | 504-520 | `handleRemoveImage` | 按钮事件处理函数 |
| `web/src/pages/EditorPage.tsx` | 965-972 | 按钮 JSX | UI 渲染入口 |
| `web/src/pages/EditorPage.tsx` | 576-591 | `resetWorkspace` | 对比参考（逻辑相同） |
| `web/src/pages/EditorPage.tsx` | 256-265 | `getUniqueImages` | 缩略图列表来源 |
| `web/src/pages/EditorPage.tsx` | 354 | `imageList` | `nodesById` 驱动版本树 sidebar |
| `web/src/pages/EditorPage.tsx` | 287-308 | `buildRetouchSession` | 保存历史快照逻辑 |
| `web/src/pages/EditorPage.tsx` | 435-457 | `persistActiveSession` | 在清空前保存当前树 |
| `web/src/pages/EditorPage.tsx` | 593-622 | `handleOpenHistorySession` | 从历史恢复版本树 |
| `web/src/store/useImageTreeStore.ts` | 194-201 | `resetTree` | **清空版本树的罪魁祸首** |
| `web/src/store/useImageTreeStore.ts` | 78-101 | `addRootNode` | 上传图片（覆盖旧树） |
| `web/src/store/useImageTreeStore.ts` | 103-143 | `addNode` | 生成结果后添加子节点 |
| `web/src/store/useImageTreeStore.ts` | 42-47 | `ImageTreeSnapshot` | 历史快照数据结构 |
| `web/src/store/retouch-history.ts` | 165-176 | `getRetouchHistoryStats` | 历史列表图片/编辑计数 |
| `web/src/store/retouch-history.ts` | 159-163 | `getRetouchSessionPreviewUrl` | 历史缩略图来源 |

## 分析结论

1. **按钮命名误导**："移除图片" 暗示仅移除单张图片，实际调用 `resetTree()` 清空整个版本树
2. **无确认机制**：操作无确认对话框，直接破坏用户工作成果
3. **与"更换图片"功能重叠**：用户若想重新开始，"更换图片"已通过 `addRootNode` 覆盖旧树达到类似效果
4. **与 `resetWorkspace` 功能重复**：两个函数逻辑几乎一致，仅使用场景不同
5. **历史删除不是删除当前版本**：历史列表删除的是整个 Retouch 项目/会话，不是版本树中的当前节点

## 最终修复方案

### 目标

本次先把该操作明确改成 **"清空工作区"**，让 UI 文案、确认流程和实际行为一致。暂不实现“删除当前版本节点”，避免在版本树引用关系上引入额外复杂度。

### 方案 1：将按钮语义改为"清空工作区"

把左侧工具栏按钮从：

```tsx
移除图片
```

改为：

```tsx
清空工作区
```

推荐使用 **"清空工作区"**，因为它和实际执行的 `resetWorkspace` / `resetTree` 行为最一致。

### 方案 2：增加确认对话框

点击"清空工作区"不再立即调用 `handleRemoveImage()`，而是打开确认对话框。

建议文案：

```text
清空当前工作区？

这会关闭当前画布和版本树，并回到上传图片界面。
如当前项目已成功保存到本浏览器的 Retouch 历史中，你可以稍后从历史重新打开。
```

确认按钮：

```text
确认清空
```

取消按钮：

```text
取消
```

实现上建议复用现有 Dialog 组件，新增独立状态，例如：

```ts
const [clearWorkspaceConfirmOpen, setClearWorkspaceConfirmOpen] = useState(false);
```

确认后再执行：

```ts
await handleClearWorkspace();
```

### 方案 3：合并重复逻辑

当前 `handleRemoveImage` 和 `resetWorkspace` 基本重复。建议改为：

1. 保留一个同步的 `resetWorkspace()`，只负责清空前端工作台状态
2. 新增一个异步的 `handleClearWorkspace()`，负责先保存历史，再调用 `resetWorkspace()`
3. 删除或重命名 `handleRemoveImage`

建议结构：

```tsx
const resetWorkspace = useCallback(() => {
  generationRunIdRef.current += 1;
  activeTaskIdRef.current = null;
  resetTree();
  setActiveSessionId(null);
  setSourceFilesByAssetId({});
  setPrompt("");
  setHasCanvasMarks(false);
  setMarkerColor(null);
  setPendingSourceImage(null);
  setPendingMaskData(undefined);
  setErrorMessage("");
  setSplitSelection(null);
  setPageStatus("empty");
  setGeneratingStartedAt(null);
}, [resetTree]);

const handleClearWorkspace = useCallback(async () => {
  const taskId = activeTaskIdRef.current;
  await persistActiveSession(prompt);
  resetWorkspace();
  if (taskId) {
    try {
      await cancelCreationTask(taskId);
    } catch {
      // Best-effort cancellation; local workspace is already cleared.
    }
  }
}, [persistActiveSession, prompt, resetWorkspace]);
```

这样符合 DRY：清空状态只维护一份逻辑。

### 方案 4：空白页也保留历史入口

当前工具栏只在 `currentNode` 存在时通过 `createPortal` 渲染。清空工作区后，用户回到上传界面，历史入口也消失。

建议至少在空白上传页提供一个轻量历史入口：

```text
历史
```

保持当前空白页左上方的历史入口位置即可。它只需要打开现有 `isHistoryOpen` 对话框，不需要新增历史逻辑。

这一步能直接解决用户“历史也找不到”的核心体验问题。

### 方案 5：暂不做删除当前版本

当前版本树模型中，一个节点的 `generatedImage` 可能成为子节点的 `baseImage`。如果按“删除单张图片 asset”实现，容易造成子节点引用断裂。

真正的“删除当前版本”应该是树节点/子树删除，而不是图片 asset 删除：

1. 根节点不可作为普通版本删除
2. 删除非根节点时，应删除该节点及其所有子分支
3. 删除后 `currentNodeId` 回退到父节点
4. 删除后更新父节点 `childrenIds`
5. 删除后立即 `persistActiveSession()`

这属于后续独立功能，不纳入本次“清空工作区”修复。

## 实施步骤

1. 重命名按钮：`移除图片` → `清空工作区`
2. 新增清空工作区确认 Dialog
3. 将 `handleRemoveImage` 重命名为 `handleClearWorkspace`，并复用 `resetWorkspace`
4. 确保清空前调用 `persistActiveSession(prompt)`
5. 保持空白上传态左上方的历史入口可见
6. 清空时如存在 `activeTaskIdRef.current`，在本地清空后调用 `cancelCreationTask(taskId)` 做后端任务取消
7. 手动验证：
   - 多轮修图后点击"清空工作区"会出现确认
   - 取消后版本树不变
   - 确认后回到上传态
   - 上传态左上方仍可打开历史
   - 点击历史项目可以恢复版本树
   - 正在生成时清空工作区会尝试取消后端任务

## 原则应用

- **KISS**：本次只把现有行为改成明确的"清空工作区"，不混入复杂的节点删除能力
- **YAGNI**：暂不实现“删除当前版本”，因为用户当前确认的是清空工作区语义
- **DRY**：把 `handleRemoveImage` 和 `resetWorkspace` 的重复状态清理逻辑合并
- **SRP**：`resetWorkspace` 只负责清空本地 UI/tree 状态，`handleClearWorkspace` 负责保存历史、清空工作区并 best-effort 取消后端任务
- **OCP**：后续如果要支持“删除当前版本”，可以在 tree store 增加节点删除能力，不需要改动清空工作区流程

## 已确认决策

1. **按钮文案**：采用"清空工作区"。
2. **确认文案强度**：采用保守表述，写成"如当前项目已成功保存到历史中，可以稍后恢复"，避免承诺 IndexedDB 一定保存成功。
3. **空白页历史入口位置**：保持当前空白页左上方入口，不调整到上传卡片或顶部导航。
4. **清空时处理生成任务**：清空确认后顺手调用 `cancelCreationTask(taskId)` 取消后端任务；取消失败只作为 best-effort，不阻塞本地清空。
5. **删除当前版本**：本次不做。历史里的删除是删除整个 Retouch 项目，不是版本级删除；如后续需要，另开独立版本树删除功能。