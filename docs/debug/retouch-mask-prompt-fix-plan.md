# Retouch 局部修图 prompt 未包装 & mask 语义缺失修复计划

## 现象描述

在 Retouch（局部修图）页面中：

1. **涂鸦圈选后提交，prompt 裸传**：用户在 canvas 上涂鸦圈出待修改区域，输入"把头发染成红色"并提交，API 收到的 prompt 就是"把头发染成红色"，没有任何"只改圈选区域"的指令。
2. **`buildRetouchPrompt` 是死代码**：`web/src/app/retouch/retouch-prompt.ts` 中定义了完整的局部修图 prompt 构造器，但从未在运行时被 import 或调用。
3. **mask 虽发送但缺乏 prompt 配合**：`input_image_mask` 字段确实携带了正确的黑白 mask 图发送给上游 API，但 prompt 中没有语义说明，导致上游模型可能忽略 mask 约束，做全图生成而非局部编辑。

## 数据流现状

```
用户涂鸦 → canvas 覆盖层（原图不变）
         ↓
exportMaskDataUrl() → 黑底白笔 mask PNG → input_image_mask (FormData)
                                              ↓
用户输入 prompt "把头发染成红色" ──────────→ prompt (FormData)  ← 裸传，未包装
                                              ↓
                                       /api/creation-tasks/image-edits
                                              ↓
                                    上游 API: DALL·E 3 Responses API
                                    { input_image_mask: {image_url: ...},
                                      prompt: "把头发染成红色" }  ← 缺少局部编辑语义
```

## 根因分析

### 核心原因 1：`buildRetouchPrompt` 未被调用

`web/src/app/retouch/retouch-prompt.ts:7-24` 定义了 `buildRetouchPrompt(userPrompt, markerColorName)`，它会构造包含以下语义的完整 prompt：

```
参考输入图像，用户已用高对比度的红色涂鸦/框线标出需要修改的区域。

仅对标注区域进行编辑，其余图像内容必须保持不变...

在标注区域内：
[用户原始输入]

要求：
- 修改后的内容必须与原图光照、透视、风格一致
- 边缘自然融合，无拼接感
- 不改变未标注区域的细节
- ...
```

但在 `EditorPage.tsx:665` 中，`createImageEditTask` 的 `prompt` 参数直接传入 `nextPrompt`（即 `prompt.trim()` 的裸用户输入），没有经过 `buildRetouchPrompt` 包装。

### 核心原因 2：`EditorPage` 未存储当前 marker 颜色

`buildRetouchPrompt` 的第二个参数是 `markerColorName`（如"红色"），但 `EditorPage` 只通过 `onMarkerChange` 回调接收了 `hasMarks: boolean`（`EditorPage.tsx:819`），并没有保存具体的 marker 颜色名称。需要增加状态来保存当前 marker 颜色。

### 辅助信息：mask 发送逻辑是正确的

| 环节 | 状态 | 说明 |
|------|------|------|
| `retouch-canvas.tsx:345-363` `exportMaskDataUrl()` | ✅ 正确 | 创建黑底画布，白笔绘制 mask 区域，导出 PNG data URL |
| `EditorPage.tsx:595-605` 获取 mask | ✅ 正确 | 有涂鸦时调用 `exportMaskDataUrl()` |
| `EditorPage.tsx:678` 传入 | ✅ 正确 | `maskData ? { inputImageMask: maskData } : undefined` |
| `api.ts:1129-1131` 发送 | ✅ 正确 | `formData.append("input_image_mask", toolOptions.inputImageMask)` |
| `responses_image.go:311-312` 转发上游 | ✅ 正确 | `{"input_image_mask": {"image_url": "data:image/png;base64,..."}}` |

### 辅助信息：原图不包含涂鸦是正常行为

标准 inpainting 流程要求：
- **原图** — 无任何涂鸦标记
- **mask 图** — 黑底白笔，白色区域 = 待修改区域
- **prompt** — 描述修改内容

三者在 API 调用中各自独立。原图上没有涂鸦是正确行为。

## 涉及代码文件

| 文件 | 行号 | 说明 |
|------|------|------|
| `web/src/app/retouch/retouch-prompt.ts` | 7-24 | `buildRetouchPrompt()` 定义，当前是死代码 |
| `web/src/pages/EditorPage.tsx` | 277 | `const [prompt, setPrompt] = useState("")` — 用户原始 prompt |
| `web/src/pages/EditorPage.tsx` | 282 | `const [hasCanvasMarks, setHasCanvasMarks] = useState(false)` — 只有 boolean |
| `web/src/pages/EditorPage.tsx` | 595-605 | mask 获取逻辑，位置正确 |
| `web/src/pages/EditorPage.tsx` | 665-678 | `createImageEditTask` 调用，prompt 裸传 |
| `web/src/pages/EditorPage.tsx` | 819 | `onMarkerChange={(_, hasMarks) => setHasCanvasMarks(hasMarks)}` — 丢弃了 color 信息 |
| `web/src/app/retouch/components/retouch-canvas.tsx` | 35 | `onMarkerChange?: (color: RetouchMarkerColor, hasMarks: boolean) => void` — 回调签名含 color |

## 修复方案

### 步骤 1：`EditorPage` 增加 marker 颜色状态

在 `EditorPage.tsx` 的状态声明区新增：

```typescript
const [markerColor, setMarkerColor] = useState<RetouchMarkerColor | null>(null);
```

导入 `RetouchMarkerColor` 类型和 `buildRetouchPrompt` 函数。

### 步骤 2：接入 `onMarkerChange` 的 color 参数

将现有回调 `EditorPage.tsx:819`：

```typescript
onMarkerChange={(_, hasMarks) => setHasCanvasMarks(hasMarks)}
```

改为接收 color：

```typescript
onMarkerChange={(color, hasMarks) => {
  setMarkerColor(color);
  setHasCanvasMarks(hasMarks);
}}
```

### 步骤 3：调用 `buildRetouchPrompt` 包装 prompt

在 `EditorPage.tsx:665` 调用 `createImageEditTask` 之前，构造最终 prompt：

```typescript
const finalPrompt = markerColor
  ? buildRetouchPrompt(nextPrompt, markerColor.name)
  : nextPrompt;
```

然后将 `finalPrompt`（而非 `nextPrompt`）传给 `createImageEditTask`。

这里需要决定是**只包装提交时的 prompt**（不修改输入框显示），还是**也把包装后的 prompt 存入历史记录**。建议方案：提交时临时包装，输入框内仍显示用户原始输入，历史记录中也保存原始 prompt。这样好处是用户看到的一直是自己输入的内容，不迷惑。

### 步骤 4：确认 mask 发送条件不变

步骤 3 只是修改 prompt 参数，mask 的获取和发送逻辑（`EditorPage.tsx:595-605`）保持不变。

## 修改文件清单

| 文件 | 改动类型 | 说明 |
|------|---------|------|
| `web/src/pages/EditorPage.tsx` | 新增 import | import `buildRetouchPrompt` 和 `RetouchMarkerColor` |
| `web/src/pages/EditorPage.tsx` | 新增 state | `const [markerColor, setMarkerColor] = useState<RetouchMarkerColor\|null>(null)` |
| `web/src/pages/EditorPage.tsx` | 修改回调 | `onMarkerChange` 接收 color 参数 |
| `web/src/pages/EditorPage.tsx` | 修改提交逻辑 | 调用 `buildRetouchPrompt` 包装 prompt |
| `web/src/app/retouch/retouch-prompt.ts` | 无改动 | 仅需确认导出签名正确 |

## 验证方式

1. 在 Retouch 页面上传图片，用红色涂鸦圈选区域，输入"把头发染成红色"并提交。
2. 确认发送到 `/api/creation-tasks/image-edits` 的 `prompt` 字段包含以下内容：
   - "参考输入图像，用户已用高对比度的红色涂鸦/框线标出需要修改的区域。"
   - "仅对标注区域进行编辑..."
   - "在标注区域内：把头发染成红色"
3. 确认 `input_image_mask` 字段仍然携带正确的 mask data URL。
4. 确认生成结果中，仅涂鸦区域被修改，其余部分保持不变。
