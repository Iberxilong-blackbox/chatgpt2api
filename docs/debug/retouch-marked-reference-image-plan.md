# Retouch 改用涂鸦参考图修复计划

## 背景结论

当前 Retouch 链路已经验证：

1. 前端会发送 `input_image_mask`。
2. 后端 creation task 会保留 `input_image_mask`。
3. protocol 层能成功 decode mask，并在上游请求准备阶段同时拿到 `input_image_count=1` 和 `mask_decoded=true`。
4. prompt 已完成局部修图包装，`prompt_wrapped=true`。

但实际生成结果没有稳定按圈选区域修改，说明问题大概率不在本地 FormData、后端解析或 prompt 包装，而在 ChatGPT Web 上游路由对 `input_image_mask` 的支持/消费能力。因为本项目本质调用的是 ChatGPT Web 逆向链路，不应继续假设它完整支持官方 inpainting mask 语义。

## 当前问题

现有提交结构：

```text
原图 image
黑白 mask input_image_mask
包装后的 prompt
```

实际效果：

```text
上游接收请求不报错，但生成结果未严格受 mask 约束。
```

这说明“请求字段存在”不等于“上游模型实际使用 mask”。继续围绕 `input_image_mask` 调参，收益不确定且容易增加复杂度。

## 新方案目标

将 Retouch 从“原图 + 黑白 mask”改为更符合 ChatGPT Web 多图理解能力的方式：

```text
带涂鸦标注的图片 + 明确 prompt
```

核心思路：

1. 用户在 canvas 上圈选区域。
2. 提交时导出一张“原图叠加高对比度涂鸦”的 marked image。
3. 将 marked image 作为普通 `image` 字段提交给 `/api/creation-tasks/image-edits`。
4. prompt 明确说明：图片中的指定颜色涂鸦/框线用于标记编辑区域，只修改标注区域，最终结果不要保留涂鸦。
5. 不再依赖 `input_image_mask` 控制局部编辑。

## 方案取舍

### 方案 A：只发送带涂鸦图

```text
image = marked image
prompt = 包装后的局部修图 prompt
input_image_mask = 不发送
```

优点：

- 最简单，符合 KISS。
- 用户已验证类似方式有效。
- 避免原图和标注图同时输入时模型不知以哪张为主。
- 不依赖 Web 上游是否支持 mask 字段。

缺点：

- 模型需要理解涂鸦是标注，不是图像内容本身。
- 需要 prompt 明确要求最终图不要保留涂鸦。

### 方案 B：同时发送原图和带涂鸦图

```text
image[0] = original image
image[1] = marked image
prompt = 以第一张为原图，第二张为标注参考
input_image_mask = 不发送
```

优点：

- 原图不被涂鸦污染，理论上能保留更多细节。
- 标注图只承担区域提示职责。

缺点：

- 多图语义更复杂，模型可能混淆两张图。
- 更容易把第二张当风格/参考图，而不是区域标注。
- 比方案 A 增加实现和提示词复杂度。

### 建议

优先实现 **方案 A：只发送带涂鸦图**。

理由：

- 用户已验证该方式可行。
- 当前目标是解决局部修改不生效，不是追求完整 inpainting API 兼容。
- 删除对 `input_image_mask` 的依赖能降低复杂度，符合 KISS/YAGNI。
- 如方案 A 稳定性不足，再迭代方案 B。

## 目标数据流

```text
用户涂鸦
  ↓
RetouchCanvas.exportMarkedImage()
  ↓
带涂鸦 PNG File
  ↓
createImageEditTask(clientTaskId, markedFile, retouchPrompt, ...)
  ↓
/api/creation-tasks/image-edits
  ↓
上游 Web 图片生成链路
```

不再发送：

```text
input_image_mask
```

## Prompt 调整

现有 `buildRetouchPrompt(userPrompt, markerColorName)` 仍可复用，但语义需要从“mask/标注区域”更明确地改为“输入图片上可见涂鸦是标注，不是最终内容”。

建议 prompt：

```text
参考输入图像，用户已用高对比度的{markerColorName}涂鸦/框线标出需要修改的区域。

这些{markerColorName}标记只是编辑区域提示，不是图像内容，最终结果中必须移除所有标记痕迹。

仅对标注区域进行编辑，其余图像内容必须保持不变，包括人物、背景、光影、构图、色彩一致性。

在标注区域内：
{用户原始输入}

要求：
- 最终图像中不要出现涂鸦线、框线、标记颜色或遮挡痕迹
- 修改后的内容必须与原图光照、透视、风格一致
- 边缘自然融合，无拼接感
- 不改变未标注区域的细节
- 保持整体画面真实一致性；如果原图是动漫或插画，则匹配原图风格
```

## 涉及代码文件

| 文件 | 改动 | 说明 |
|------|------|------|
| `web/src/app/retouch/components/retouch-canvas.tsx` | 复用现有能力 | 已有 `exportMarkedImage()`，可直接导出带涂鸦图 |
| `web/src/app/retouch/retouch-prompt.ts` | 修改 prompt 文案 | 强化“涂鸦只是标注，最终不要保留” |
| `web/src/pages/EditorPage.tsx` | 修改提交逻辑 | 有涂鸦时发送 marked image，不再发送 `inputImageMask` |
| `web/src/lib/api.ts` | 暂不改 | `createImageEditTask` 已支持传单个 File |
| `internal/**` | 暂不改 | 后端仍按普通图片编辑任务处理 |

## 实施步骤

### 步骤 1：调整 prompt 构造器

修改 `buildRetouchPrompt()`，保留用户原始输入不变，增强两点：

1. 涂鸦/框线只是区域标注。
2. 最终结果必须移除标记痕迹。

原则：

- KISS：只改文案，不新增 prompt builder 分支。
- YAGNI：不保留 mask 专用 prompt 兼容路径。

### 步骤 2：修改 Retouch 提交流程

在 `EditorPage.tsx` 的 `handleGenerate` 中：

1. 有 canvas marks 时调用 `retouchCanvasRef.current.exportMarkedImage()`。
2. 将导出的 marked file 作为 `createImageEditTask` 的 `sourceFile`。
3. 不再构造或传递 `maskData ? { inputImageMask: maskData } : undefined`。
4. 历史树中仍可保存 `maskData` 或改为不保存，建议本次先不依赖它。

建议伪代码：

```typescript
const markedSourceFile =
  hasCanvasMarks && retouchCanvasRef.current
    ? await retouchCanvasRef.current.exportMarkedImage()
    : await imageAssetToFile(lockedSourceImage, sourceFilesByAssetId[lockedSourceImage.id] ?? null);

const submittedTask = await createImageEditTask(
  clientTaskId,
  markedSourceFile,
  retouchPrompt,
  ...
  undefined,
);
```

### 步骤 3：移除 mask 发送依赖

Retouch 页面不再传：

```typescript
maskData ? { inputImageMask: maskData } : undefined
```

如果其他页面仍使用 `createImageEditTask` 的 `inputImageMask` 参数，暂不删除 API 支持，避免扩大改动范围。

原则：

- SOLID/SRP：Retouch 页面只决定自己的提交策略。
- YAGNI：不删除后端通用字段，除非后续确认全项目不需要。

### 步骤 4：保留日志辅助验证

当前后端日志仍可保留短期观察：

```text
creation image edit task running
responses image upstream request prepared
```

新方案上线后预期日志：

```text
mask_present=false
input_image_count=1
prompt_wrapped=true
```

这说明 Retouch 已停止依赖 `input_image_mask`，改为普通图片输入。

## 验证方式

### 浏览器 Network

提交一次 Retouch，确认：

```text
FormData image = 带涂鸦标注的图片
FormData prompt = 包装后的局部修图 prompt
FormData input_image_mask 不存在
```

### 服务器日志

确认：

```text
mask_present=false
prompt_wrapped=true
```

### 结果验证

使用同一张图做对比：

1. 圈选头发，输入“把头发染成红色”。
2. 圈选衣服，输入“把衣服改成黑色夹克”。
3. 圈选背景小物体，输入“移除这个物体并补全背景”。

期望：

- 生成图不保留涂鸦痕迹。
- 修改集中在圈选区域。
- 未圈选区域变化明显少于旧 mask 方案。

## 回滚方案

如新方案效果更差，可回滚到当前 mask 方案：

1. 恢复 `exportMaskDataUrl()` 获取 mask。
2. 恢复 `inputImageMask` 传参。
3. 保留 prompt 包装逻辑。

## 后续可选迭代

如果只发带涂鸦图仍不够稳定，再尝试方案 B：

```text
第一张：原图
第二张：带涂鸦图
prompt：第一张是原图，第二张仅用于标注区域
```

但这应作为第二轮实验，不在本次修复中一次性实现，避免同时改变多个变量导致难以判断效果。
