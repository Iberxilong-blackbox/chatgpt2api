# h-full 高度级联断裂问题经验总结

## 背景

在 Retouch 全屏编辑器中，用户上传一张图片后，图片占据很大空间，把底部输入框往下推，导致输入框只有一半显示在屏幕中（另一半被遮挡）。

问题页面：`web/src/pages/EditorPage.tsx`
问题路由：`/retouch`

## 现象

1. 进入 `/retouch` 页面（初始态）：上传占位区域居中，底部输入框在首屏可见
2. 上传一张图片后：图片渲染出来的画布占据了大量垂直空间，底部输入框被推到屏幕外，只有上半部分可见
3. 图片越大（高分辨率），画布占位越大，输入框被推出越多

## 直接原因

### 1. `<main>` 的 `h-full` 找不到确定参考高度（核心原因）

AppShell（`app-shell.tsx`）中，Retouch 路由的 DOM 层级如下：

```
main.fixed.inset-0 (AppShell, h-dvh 提供确定高度)
  div.flex.h-full.flex-col (AppShell 中 flex 容器)
    TopNav (~48-64px)
    div.min-h-0.flex-1.overflow-hidden (flex 弹性项，有确定高度)
      motion.div.min-w-0 (AnimatedRoutes 的路由壳子)     ← 没有高度！h-full 在此断裂
        main.relative.h-full (EditorPage)               ← h-full 退化为 auto
          section.absolute.inset-0                      [图片/画布区域]
          form.absolute.inset-x-0.bottom-4              [底部输入框]
```

关键链条分析：
- `flex-1` div 自身在 flex-col 布局中拥有**确定高度**（等于 flex 容器剩余空间）
- 但它的直接子元素 `motion.div.min-w-0` **不是 flex 弹性项**——flex 布局只作用于 flex 容器的直接子元素，嵌套的子元素不参与 flex 分配
- `motion.div` 没有设置任何高度约束（无 `h-full`、无 `flex-1`），它的高度由其内容撑开
- `<main className="h-full">` 向父级 `motion.div` 查询 `height: 100%` → 父级高度不确定 → CSS 规范规定 `height: 100%` 退化为 `auto`
- 结果：`<main>` 高度为 `auto`（实际接近 0，因为所有子元素都是绝对定位脱离文档流）

虽然部分浏览器的渲染引擎可能不会严格退化为 `auto`，但这造成了**不可预测的跨浏览器行为**。

### 2. 图片加载后画布拉高，与异常高度叠加产生视觉偏移

- 上传前：上传占位按钮内容少，`<main>` 高度不明确时没有明显问题（section 内元素高度适中）
- 上传后：`RetouchCanvas` 渲染的 `<canvas>` 具有内禀尺寸（图片自然分辨率）。CSS `max-h-[calc(100dvh-300px)]` 约束后，画布仍然占据大量垂直空间
- 画布卡片撑高后，由于 `<main>` 高度异常，`form.absolute.bottom-4` 定位在 `<main>` 的底部——而这个底部落在首屏可见区域之外
- 结果：输入框在视觉上被"往下推"，实际是 `<main>` 高度计算失准导致定位参考点错误

### 3. `section` 缺少 `overflow-hidden`（次要原因）

`EditorPage.tsx:194` 中的 `<section>` 使用 `absolute inset-0`，但没有设置 `overflow-hidden`。内部画布卡片如果超出 section 边界，不会被裁切。结合高度级联断裂，内容溢出进一步加剧了视觉错位。

## 修复方案

### 修复 1（核心）：让 `AnimatedRoutes` 的 `motion.div` 传递高度

**文件**：`web/src/app/animated-routes.tsx`

在 `motion.div` 上添加 `h-full`，使其继承父级 `flex-1` div 的确定高度，从而为子级的 `h-full` 提供确定的参考值。

```diff
- <motion.div className="min-w-0" ...>
+ <motion.div className="h-full min-w-0" ...>
```

**原理**：`motion.div` 的直接父级（`flex-1` div）在 flex-col 布局中有确定高度（等于 flex 容器剩余空间）。`h-full` 使 `motion.div` 高度=父级高度，从而为子级 `<main>` 的 `h-full` 提供可靠的参考值。

**无损说明**：对非全屏路由（普通后台页面），`AnimatedRoutes` 的父容器使用 `min-h-screen` 而非确定高度，此时 `h-full` 在父容器高度为 `auto` 时同样退化为 `auto`，与之前行为一致，不会造成影响。

### 修复 2：给 EditorPage 的 `section` 添加 `overflow-hidden`

**文件**：`web/src/pages/EditorPage.tsx`

```diff
- <section className="absolute inset-0 mx-auto flex w-full max-w-7xl items-center justify-center px-6 pb-32 pt-6">
+ <section className="absolute inset-0 mx-auto flex w-full max-w-7xl items-center justify-center overflow-hidden px-6 pb-32 pt-6">
```

### 修复 3（可选优化）：将 `max-h` 约束改为基于 flex 容器的百分比值

当前画布的 `max-h-[calc(100dvh-300px)]` 在高度级联修复后理论上已够用。为进一步增强健壮性，可以改用基于容器的百分比：

```diff
- [&_canvas]:!max-h-[calc(100dvh-300px)]
+ [&_canvas]:max-h-full
```

并确保父级容器通过 flex 布局正确分配高度：

```diff
- className="relative flex max-h-full w-full flex-col ..."
+ className="relative min-h-0 flex-1 flex-col ..."
```

## 复发规律

这类问题的共同特征：

1. 页面需要**确定性高度**（全屏、`absolute inset-0`、`overflow-hidden`）
2. 页面根节点使用 `h-full` 或 `h-screen`（依赖父级高度）
3. 父级链中有至少一层**没有设置高度**的中间容器（`motion.div`、普通 `div`、`<Routes>` 等）
4. 内容（图片、表格、长列表）加载后撑大容器，导致底部元素被推出视口

核心判断准则：

```
如果 h-full 到视口（或确定高度的祖先）之间的所有父级节点中，
有任何一个节点没有明确设置 height 或 flex-1 / h-full，
那么 h-full 就是不可靠的，一定会出现高度塌陷。
```

## 后续避免规则

### 规则 1：全屏页面的路由节点必须手动确认高度传递链路

检查从 `html/body` 到目标元素之间**每一层**的 CSS，确保 `height` 没有断链。特别留意：

- 动画容器（AnimatePresence、`motion.div`）
- 路由壳子（Routes、Outlet）
- 非 flex 弹性项（flex item）的普通 div

`h-full` 可靠性检查清单：
- [ ] 目标元素使用了 `h-full`
- [ ] 直接父级有确定高度（`h-*`、`flex-1`、`inset-0`、`h-dvh`）
- [ ] 或者直接父级也使用了 `h-full`，且递归上述检查直到确定高度源
- [ ] 所有 `h-full` 依赖链上的节点都是 block 布局（默认 div 行为），没有 inline 或 inline-block

### 规则 2：优先使用 `absolute inset-0` 或 `fixed inset-0` 替代 `h-full` 来撑满容器

当目标元素需要填满父容器或视口时，以下两种方式等价：

| 方式 | 条件 | 可靠度 | 推荐场景 |
|------|------|--------|----------|
| `absolute inset-0` | 父容器是 positioned | 高 | 全屏编辑器、对话框 |
| `fixed inset-0` | 脱离文档流 | 高 | 全屏模态、独立路由 |
| `h-full` / `h-screen` | 父级链高度必须完整 | 低 | 仅用于简单确定的层级 |

**推荐**：全屏/沉浸式页面使用 `absolute inset-0` 或 `fixed inset-0`，避免 `h-full` 级联问题。例如，EditorPage 可以将根 `<main>` 改为：

```tsx
<div className="absolute inset-0 overflow-hidden bg-[#f5f7fa] text-slate-950">
  {/* section 和 form 作为子元素，它们使用 absolute 相对此容器定位 */}
</div>
```

这种方式完全摆脱了对父级高度的依赖。

### 规则 3：元素使用 `overflow-hidden` 应在最近的溢出可能层

如果一个容器内放入了具有内禀尺寸的子元素（`<img>`、`<canvas>`、`<video>`、`<svg>`），且子元素可能超过容器边界，应该在该容器或其直接父级上设置 `overflow-hidden`。

### 规则 4：用 `100dvh` / `100vh` 约束嵌套元素时，必须扣除外层所有固定的 UI 元素高度

在嵌套容器中使用 `100dvh` 做约束时，必须减去所有介于之间的固定高度元素：
- 导航栏（TopNav）高度
- 容器 padding
- flex gap
- 底部表单/工具栏高度

最佳实践是：**在容器本身（而不是深层子元素）使用基于 flex 百分比的值**，让约束随容器自适应：

```tsx
// ❌ 不推荐：深层子元素用 100dvh 做约束，不跟随实际容器
[&_canvas]:!max-h-[calc(100dvh-300px)]

// ✅ 推荐：利用 flex 布局自动分配高度
<div className="flex flex-col h-full">
  <div className="min-h-0 flex-1 overflow-hidden">
    <canvas className="h-full w-full object-contain" />
  </div>
</div>
```

### 规则 5：在 `AnimatePresence` / `motion.div` 路由壳子中，全屏页面必须确保高度传递

React 动画库（framer-motion / motion）的 `motion.div` 默认行为类似普通 div，不会自动拉伸填满父容器。全屏页面必须手动添加 `h-full` 到 `motion.div`。

```tsx
// AnimatedRoutes 中：
<motion.div className="h-full min-w-0" ...>
```

或者，全屏页面使用 `fixed inset-0` 完全脱离文档流，彻底规避高度传递问题：

```tsx
export default function FullscreenPage() {
  return (
    <div className="fixed inset-0 overflow-hidden">
      {/* 内容 */}
    </div>
  );
}
```

### 规则 6：任何具有 `h-full` 的页面发生布局偏移时，先检查高度传递链路

排查步骤：
1. 在浏览器 DevTools Elements 面板中选中 `h-full` 的元素
2. 沿着 DOM 树向上，检查每一个父级元素的计算高度
3. 找到第一个高度不是确定数值（而是 `auto` 或 `0`）的父级
4. 在该节点上添加 `h-full` 或改为 flex 弹性项
5. 递归向上直到找到确定高度源（`100vh`、`h-dvh`、`flex-1` 等）

## 引用

- 关联文档：[retouch-fullscreen-scrollbar-lessons.md](./retouch-fullscreen-scrollbar-lessons.md)
- CSS 规范：https://www.w3.org/TR/CSS22/visudet.html#the-height-property（`height: 100%` 在包含块高度不确定时退化为 `auto`）
- 问题组件：`web/src/pages/EditorPage.tsx`
- 路由壳子：`web/src/app/animated-routes.tsx`
- 页面壳子：`web/src/app/app-shell.tsx`
- 状态管理：`web/src/store/useImageTreeStore.ts`
