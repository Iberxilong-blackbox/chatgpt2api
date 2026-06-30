# 创作台预设增强方案

> 目标：打破当前 4 个固定预设的限制，支持更多预设条目、纯文本预设（无参考图）、以及更好的浏览体验。
> 计划日期：2026-06-30

## 背景

当前创作台空状态展示 4 张预设卡片，数据硬编码在 `web/src/app/image/image-presets.ts`。用户选择预设后，自动填充 prompt、尺寸、数量并将预设图片作为参考图注入底部 dock。

实际使用中发现 4 个预设远不足以覆盖用户需求场景，且产品侧已有大量新增预设诉求（如各种风格的纯文本 prompt）。

## 现状

### 数据结构

```typescript
// web/src/app/image/image-presets.ts
export type ImagePromptPreset = {
  id: string;
  title: string;
  prompt: string;
  hint: string;
  imageSrc: string;      // ❌ 必填，无法支持纯文本预设
  count: number;
  size: string;
};
```

### 渲染逻辑

- 文件：`web/src/app/image/components/image-results.tsx:356`
- 布局：`<sm 横向滚动 | sm:grid-cols-2 | lg:grid-cols-4>` 固定 4 列网格
- 所有预设同时渲染，无分页、无分类、无换一批机制

### 入口文件

- `web/src/app/image/page.tsx:33` 导入 `IMAGE_PROMPT_PRESETS`
- `web/src/app/image/page.tsx:3391` 以 props 传入 `ImageResults`

### 与 Prompt Market 的区别

| 维度 | 本地预设 (Presets) | 远程 Prompt Market |
|------|-------------------|-------------------|
| 数据源 | 硬编码数组 | GitHub 远程获取 |
| 数量 | 4 条 | 数百条 |
| 分页 | 无 | 有 (加载更多) |
| 参考图 | 必有 | 部分有 |
| 分类 | 无 | 有 tags 筛选 |

## 需求

1. **新增更多预设条目**，数量不设硬上限，方便随时增删。
2. **支持纯文本预设**，即没有参考图的预设条目，前端渲染兜底视觉。
3. **用户可直接翻阅/选择**，不需要像"换一批"那样盲选。
4. **适配现有 UI 风格**，不破坏创作台整体视觉语言。

## 方案对比

### 方案 A：水平滚动 + 箭头导航（推荐）

将固定网格改为可横向滚动的卡片容器，左右各一个箭头按钮辅助导航。

```
[←]  [卡1] [卡2] [卡3] [卡4] [卡5] [卡6] [卡7] [卡8] ...  [→]
```

- 预设数量不限
- 用户可直接点击看到的任意卡片
- 移动端已实现 `overflow-x-auto`，桌面统一此模式
- 可叠加滚动位置指示点
- 不依赖第三方轮播库
- 无图预设：`imageSrc` 改为可选，渲染渐变色块 + 图标

### 方案 B：分类 Tab + 筛选网格

给预设增加 `category` 字段，顶部渲染分类 Tabs 用于筛选。

- 默认显示"全部"
- 适合预设数量继续膨胀后的场景
- 可与方案 A 叠加使用

### 方案 C：分页 + 页码圆点

每页固定 4 张卡片，底部页码圆点可跳转任意页。

- 保留当前 4 卡整洁布局
- 圆点指示器解决"不能直接选择"问题
- 增加交互复杂度，不如滚动直观

### 方案对比矩阵

| 维度 | A: 水平滚动 | B: 分类 Tab | C: 分页圆点 |
|------|------------|------------|------------|
| 改动量 | 小 | 中 | 中 |
| 直接选择 | ✅ | ✅ | ✅ |
| 支撑大量预设 | ✅ | ✅ | ✅ |
| 无图预设 | ✅ 改类型 | ✅ | ✅ |
| 实现复杂度 | 低 | 中 | 中 |
| 移动端兼容 | 已有基础 | 好 | 好 |

## 推荐方案：A + B 叠加

第一阶段实现方案 A（水平滚动 + 箭头导航 + 纯文本预设），后续根据预设数量膨胀再叠加方案 B（分类 Tab）。

### 第一阶段改动清单

#### 1. 预设数据结构调整

`web/src/app/image/image-presets.ts`：

```typescript
export type ImagePromptPreset = {
  id: string;
  title: string;
  prompt: string;
  hint: string;
  imageSrc?: string;     // ✅ 改为可选，无图时兜底
  count: number;
  size: string;
};
```

#### 2. 新增更多预设条目

在 `IMAGE_PROMPT_PRESETS` 数组中新增条目，部分不带 `imageSrc`。例如：

- 纯文字 prompt 类（如"生成一张极简主义摄影作品"）
- 风格模板类（如"赛博朋克城市夜景"）
- 垂直领域类（如"电商产品白底图"）

#### 3. 渲染组件改造

`web/src/app/image/components/image-results.tsx`：

- 网格容器改为横向滚动容器（桌面取消 `grid-cols-4`，统一使用 `flex overflow-x-auto`）
- 新增左右箭头按钮
- 新增滚动位置指示点
- 卡片渲染适配无图场景：`imageSrc` 为 falsy 时渲染渐变色块 + 图标 + 纯文本标识
- 无图预设不再尝试下载参考图

#### 4. 套用逻辑适配

`web/src/app/image/page.tsx` 中 `handleApplyPromptPreset`：

- 当 `preset.imageSrc` 不存在时，跳过 `buildReferenceImageFromUrl`，清空参考图数组
- 避免 toast 错误提示

### 非目标（第一阶段不做）

1. 不引入第三方轮播库（`embla-carousel` / `swiper` 等）。
2. 不上移预设数据到后端 API（仍以硬编码为主）。
3. 不实现预设 CUD 管理界面。
4. 不做预设搜索功能。
5. 不改动 Prompt Market。

### 第二阶段候选（分类 Tab）

在水平滚动基础上增加：

1. `ImagePromptPreset` 增加 `category?: string` 字段。
2. 顶部渲染分类筛选 Tabs。
3. 切换 Tab 时滚动容器自动跳转到对应分类起始位置或筛选展示。

### 风险与应对

| 风险 | 应对 |
|------|------|
| 水平滚动在桌面端不够直觉 | 添加可见箭头按钮 + hover 大阴影 + 光标提示 |
| 无图预设辨识度不足 | 使用差异化渐变色 + 类别图标 |
| 预设数量过多后滚动疲劳 | 第二阶段引入分类 Tab 分流 |

## 关键文件

| 文件 | 改动类型 |
|------|---------|
| `web/src/app/image/image-presets.ts` | 改类型、增条目 |
| `web/src/app/image/components/image-results.tsx` | 改渲染布局 |
| `web/src/app/image/page.tsx` | 改套用逻辑 |
| `web/src/app/image/components/image-composer.tsx` | 可能涉及无图适配 |

## 构建验证

```bash
cd web && npm run build
cd web && npm run lint
```
