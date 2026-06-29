# Retouch Split-View 图片裁剪问题

## 现象描述

在 Retouch（局部修图）页面中：

- **单图模式**：上传第一张图片后，图片完整显示，无任何裁剪。
- **双图模式**（输入 prompt 生成结果后，进入左右对比的 split-view 布局）：左右两张图片的**四周最外围部分被隐藏/裁剪**，图片相当于被适当放大后只显示了中间区域。

## 复现步骤

1. 进入 Retouch 页面，上传一张图片（单图模式正常显示）。
2. 在底部 prompt 输入框输入修改要求并提交。
3. 生成完成后，页面切换至左右双栏对比布局。
4. 观察左右两侧图片，外围边缘被裁剪不可见。

## 根因分析

### 核心原因：`object-cover` 导致图片等比填充裁剪

图片的 CSS `object-fit` 属性在两个模式下的使用不一致是根本原因。

#### 单图模式（正常）

`EditorPage.tsx:1069-1074` 渲染 `renderEditableCanvas(sourceImage)`。

该函数内部（`retouch-canvas.tsx:383`）的 `<canvas>` 元素使用：

```
className="... object-contain"
```

`object-contain` 的行为是将图片等比缩放到**完整容纳于容器内**，图片四周不会超出容器边界。

#### 双图模式（异常）

`EditorPage.tsx:947-1068` 进入 split-view 后，**未选中编辑侧**的图片直接用普通 `<img>` 标签渲染：

- 左侧图片 `EditorPage.tsx:966`：`className="size-full object-cover ..."`
- 右侧图片 `EditorPage.tsx:1031`：`className="size-full object-cover ..."`

`object-cover` 的行为是将图片等比缩放以**完全填满容器**，当容器宽高比与图片原始宽高比不一致时，图片会被放大并裁剪掉四周超出容器的部分。

### 加剧因素：双图容器固定高度

双图模式的容器使用了**固定高度**：

```
className="grid h-[min(74vh,820px)] min-h-[520px] w-full grid-cols-2 gap-4 ..."
```

- `h-[min(74vh,820px)]`：高度取 74vh 和 820px 的较小值
- `grid-cols-2`：两列等宽布局

在**宽屏显示器**上，容器的宽度远大于高度，加之 `object-cover` 要求填满容器，图片会按容器比例被放大，超出部分再被 `overflow-hidden` 裁剪。实际裁剪方向取决于图片原始宽高比与面板宽高比：常见情况是裁掉上下或左右；当图片内容边缘敏感时，会表现为外围边缘不可见。

### 选中侧 vs 未选中侧

| 面板状态 | 渲染方式 | object-fit | 效果 |
|---------|---------|------------|------|
| 未选中 | `<img>` 标签 | `object-cover` | 图片被裁剪 |
| 选中编辑 | `<canvas>` (RetouchCanvas) | `object-contain` | 图片完整显示 |

点击选中一侧后，该侧切换为 `renderEditableCanvas`（`object-contain`），图片才恢复正常显示。但**未选中侧仍然保持 `object-cover`，持续存在裁剪问题**。

## 涉及代码文件

| 文件 | 行号 | 说明 |
|------|------|------|
| `web/src/pages/EditorPage.tsx` | 948 | 双图模式容器：固定高度 `h-[min(74vh,820px)]` |
| `web/src/pages/EditorPage.tsx` | 966 | 左侧图片 `<img>` 使用 `object-cover` |
| `web/src/pages/EditorPage.tsx` | 1031 | 右侧图片 `<img>` 使用 `object-cover` |
| `web/src/pages/EditorPage.tsx` | 1069-1074 | 单图模式使用 `renderEditableCanvas` |
| `web/src/pages/EditorPage.tsx` | 810-821 | `renderEditableCanvas` 函数定义 |
| `web/src/app/retouch/components/retouch-canvas.tsx` | 383 | `<canvas>` 使用 `object-contain`（正确行为） |

## 建议修复方案

将双图模式下未选中侧 `<img>` 的 `object-cover` 改为 `object-contain`：

- `EditorPage.tsx:966`：`object-cover` → `object-contain`
- `EditorPage.tsx:1031`：`object-cover` → `object-contain`

这样无论容器宽高比如何，图片都能完整显示，与单图模式下行为一致。

同时需要移除未选中图片上的 `group-hover:scale-[1.02]`。否则即使默认态使用 `object-contain`，hover 时图片仍会在父级 `overflow-hidden` 中被轻微放大，边缘仍可能被裁掉。

建议最终 class：

```
className="size-full object-contain transition duration-500"
```

## 调试验证建议

为方便本地验证 split-view 显示效果，可在 Retouch 页面加入仅前端生效的 API / Mock 模式开关：

- **API 模式**：保持现有真实 `createImageEditTask` 提交与轮询逻辑。
- **Mock 模式**：不请求后端，提交后生成一个本地成功任务，复用当前源图作为结果图进入 split-view。

Mock 模式的目的不是模拟真实修图效果，而是快速验证页面布局、版本树、历史保存和后续编辑选择流程，避免每次调试布局都消耗真实图片生成额度。
