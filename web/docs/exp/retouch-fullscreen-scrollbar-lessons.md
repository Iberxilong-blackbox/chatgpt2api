# Retouch 全屏编辑器滚动条问题经验总结

## 背景

Phase 3 的 Retouch 编辑器目标是空间分支式 AI 图像编辑器：画面应占满视口、禁止浏览器级滚动条，并通过滚轮事件在图像树节点之间导航。

当前问题是：进入局部修图页面后，浏览器同时出现纵向和横向滚动条；主画面不在屏幕中心；底部 Prompt 输入框需要滚动后才能看到。上传图片后，根节点阶段也只显示灰色 Canvas 占位区，没有显示用户上传的图片。

## 直接原因

### 1. 全屏子页面被放进了普通应用外壳

当前应用外壳结构大致是：

```tsx
<main className="min-h-screen">
  <div className="mx-auto flex min-h-screen max-w-[1440px] flex-col gap-2 px-3 py-3 sm:px-5 lg:px-6">
    <TopNav />
    <AnimatedRoutes />
  </div>
</main>
```

而 `EditorPage` 根节点使用了：

```tsx
<main className="relative flex h-screen w-screen overflow-hidden px-6 py-6">
```

这两个布局模型冲突：

- 外层是普通文档流页面：有 `TopNav`、`gap`、`px/py`、`max-w-[1440px]`。
- 内层是独占视口页面：`h-screen w-screen overflow-hidden`。

结果是纵向高度变成：

```text
外层 padding + TopNav 高度 + gap + EditorPage 的 100vh
```

这必然大于一个视口高度，所以会出现纵向滚动条。

横向宽度也类似：

```text
父容器 padding / 居中 max-width + 子元素 100vw
```

`w-screen` 按浏览器视口宽度计算，不会扣掉父容器 padding、滚动条宽度和 `max-w` 居中偏移，所以子元素会向右溢出，导致横向滚动条。

### 2. 全局样式默认保留纵向滚动槽

全局 CSS 中有：

```css
html {
  scrollbar-gutter: stable;
  overflow-y: scroll;
}
```

这对普通管理后台页面是合理的，可以避免页面切换时宽度抖动。但对 Retouch 这种全屏编辑器，它会让浏览器始终保留纵向滚动语义。即使内层 `EditorPage` 写了 `overflow-hidden`，也只能隐藏自己的内部溢出，不能取消 `html/body` 或外层 AppShell 的滚动。

### 3. Prompt 输入框被定位在子页面底部，但子页面本身已经被外层顶下去

Prompt 表单当前是：

```tsx
<form className="absolute inset-x-0 bottom-8 ...">
```

它相对 `EditorPage` 自己的 `h-screen` 底部定位。由于 `EditorPage` 前面还有 `TopNav` 和外层 padding，`EditorPage` 的底部已经落在首屏可视区域之外。于是 Prompt 视觉上被推到浏览器首屏下方，需要滚动才能看到。

### 4. 上传图片已进入 store，但根节点 UI 没有渲染图片

上传逻辑会创建 `URL.createObjectURL(file)` 并写入 `baseImage`：

```tsx
addRootNode({
  baseImage: createImageAsset(file),
  prompt,
});
```

但当前根节点没有 `generatedImage` 时，UI 进入的是灰色占位分支：

```tsx
<div className="... bg-slate-200/80 ...">
  Canvas 占位区
</div>
```

这个分支只显示“已载入文件名”，没有 `<img src={currentNode.baseImage.url} />`。所以看不到图片不是上传失败，而是 UI 壳子没有把根节点图像接回舞台。

涂鸦功能同理：Phase 2 为了先做壳子，暂时没有接入真实 `RetouchCanvas`。这在阶段目标上可以解释，但进入 Phase 3 后，如果用户已经上传图片，主舞台至少应该显示原图；涂鸦能力则应在后续接回真实 Canvas 时恢复。

## 复发规律

这类问题通常来自三种布局混用：

1. 普通后台页面容器：`TopNav + max-width + padding + min-h-screen`。
2. 全屏编辑器容器：`fixed inset-0` 或独占 `100dvh`。
3. 视口单位子元素：`w-screen / h-screen / 100vw / 100vh`。

如果第 2、3 类组件被直接塞进第 1 类容器，滚动条几乎一定会出现。

## 后续避免规则

### 规则 1：全屏编辑器路由必须绕开普通内容容器

Retouch 这类页面不应该作为普通 `AnimatedRoutes` 内容直接放进 `AppShell` 的 `max-w + padding` 容器里。

推荐方案：

- 在 `AppShell` 按路由识别沉浸式页面，Retouch 使用独立 layout。
- 或让 `/retouch` 页面根节点使用 `fixed inset-0 z-*` 脱离外层文档流。
- 或为 Retouch 单独提供 `FullscreenRouteShell`，不渲染普通 `TopNav`，不加 `max-w` 和 `padding`。

### 规则 2：在嵌套容器里优先使用 `w-full`，不要用 `w-screen`

`w-screen` 适合真正顶层视口容器。只要组件可能被嵌套，就应该优先用：

```tsx
className="w-full min-w-0"
```

只有在确认节点已经脱离外层布局，例如 `fixed inset-0` 时，才使用视口宽度。

### 规则 3：全屏高度优先用 `h-dvh` / `min-h-0` / flex 填充，而不是盲目 `h-screen`

普通页面中 `h-screen` 不会扣掉导航栏高度。要么让页面独占视口，要么在父级 flex 容器里使用：

```tsx
className="min-h-0 flex-1 overflow-hidden"
```

移动端或嵌入式预览中，`100dvh` 比 `100vh` 更接近真实可见区域。

### 规则 4：滚轮拦截页面必须先消灭浏览器滚动源

Scroll-jacking 的前提是页面没有浏览器级滚动条。否则同一个滚轮事件会同时承担两个含义：

- 浏览器滚动页面。
- 应用导航图像树节点。

正确顺序应该是：

1. 先让页面和所有祖先容器 `overflow-hidden`。
2. 再把滚轮事件绑定到唯一的全屏舞台。
3. 最后加 throttle/debounce 控制导航频率。

### 规则 5：上传后的第一屏必须显示真实图片，不要停留在抽象占位

占位灰底只适合“未上传”或“真实 Canvas 还未挂载”的短暂状态。用户上传后，Retouch 的主舞台应至少满足：

- 能看到原图。
- 图片不溢出舞台。
- Prompt 输入框始终在首屏可见。
- 后续接入涂鸦时，图片显示层和 mask 层在同一个稳定容器里。

## 建议修复方向

最小修复可以分两步：

1. 布局修复：让 Retouch 页面脱离普通 AppShell 内容容器，使用真正的全屏舞台，保证无横纵滚动条。
2. 内容修复：根节点阶段渲染 `currentNode.baseImage.url`，把灰底占位改成图片舞台；后续再把真实 `RetouchCanvas` 接入该舞台。

这两个问题应该分开修。先修布局，确保输入框和舞台永远在首屏；再修 Canvas 内容，确保上传图像与涂鸦能力逐步回归。
