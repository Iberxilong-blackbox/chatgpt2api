# 🚀 空间分支式 AI 绘图编辑器 - 架构与开发计划

## 一、 产品愿景与交互流概述 (Interaction Flow)

本项目旨在打破传统的“上下滚动瀑布流”图片生成界面，构建一个具备**空间纵深感 (Spatial UI / 2.5D UI)** 与**版本分支树 (Branching Version Control)** 的新一代沉浸式 AI 绘图前端。

**核心交互链路：**
1. **初始态 (State A)**：极简大画幅 Dropzone（拖拽区），用户上传基底图。
2. **涂鸦与指令态 (Mask & Prompt)**：依托现有涂鸦逻辑，用户在图片上框选/画圈，在底部浮层输入 Prompt 并发送。
3. **并列对比态 (Split View)**：AI 返回结果后，页面进入核心模式。屏幕主体被均分为左右两块（占据 80% 以上容器视口），左侧为原图/基底图，右侧为新生成的图，方便对比。
4. **迭代与分支 (Branching)**：用户可选择任意一侧作为新的基底图，继续涂鸦和生成，形成无限延伸的二叉树/多叉树节点。
5. **空间时光机 (Spatial History & Scroll-jacking)**：当产生多轮迭代时，当前聚焦的“图片对 (Image Pair)”处于屏幕最前方。历史“图片对”在 Z 轴上向远方退去（缩小、变暗、位于上方）。用户使用鼠标滚轮**不产生传统页面滚动**，而是通过“滚动拦截 (Scroll-jacking)”实现在历史节点中穿梭。
6. **全局视野 (Tree Minimap)**：界面角落悬浮一个节点拓扑图，展现完整的衍生分支路径，支持一键点击跳转至任意历史状态。

---

## 二、 核心技术栈与专有名词映射 (Tech Stack & Terminology)

为了实现上述效果，前端将使用以下特定技术与库：

* **基础框架**：`React 19` + `TypeScript` + `Vite 8` + `Tailwind CSS v4`
* **状态大脑 (State Management)**：`Zustand`
  * *作用*：摒弃脆弱的事件驱动，建立强类型的多叉树（N-ary Tree）数据结构，管理当前高亮节点 (`currentNodeId`) 与历史快照。
* **动画与空间交互 (Animation & Spatial UI)**：`Framer Motion`
  * *作用*：实现历史卡片退向远方的景深动画（Scale & Translate 联动），以及界面的平滑转场。
* **滚动接管 (Scroll-jacking)**：原生 `wheel` 事件监听 或 Framer Motion 的 `useScroll`
  * *作用*：阻止浏览器的默认 `overflow-y` 滚动行为，将滚轮信号转化为 Zustand 树节点的 `goBack()` / `goForward()` 动作。
* **涂鸦引擎 (Canvas Engine)**：保留现有业务逻辑（通常底层基于 `react-konva`、`fabric.js` 或原生 Canvas API），只需进行 Tailwind UI 换肤。
* **拓扑小地图 (Minimap)**：`React Flow` (或 `xyflow`)
  * *作用*：用于在右下角渲染带有连线的节点分支图。

---

## 三、 分步开发计划 (Step-by-Step Implementation)

> **⚠️ 致 AI 代理的提示**：请严格按照以下 Phase 顺次执行，不要在同一轮对话中跨阶段开发。每个 Phase 必须在沙盒中确认效果无误后，再推进下一步。

### Phase 1: 构建数据大脑 (State Architecture)
**目标**：脱离 UI，先建立支持分支回溯的数据流。
**核心任务**：
1. 引入 `Zustand`。
2. 定义节点数据接口 `ImageNode`：包含 `id`, `parentId`, `baseImage`, `generatedImage`, `maskData`, `prompt`, `childrenIds`。
3. 实现 Store 的核心方法：
   - `addNode(payload)`: 生成新图后，挂载到当前节点下，并推进当前视图。
   - `navigateNode(nodeId)`: 任意跳转到历史/其他分支节点。
   - `getAncestors(nodeId)`: 计算当前节点到根节点的路径，用于渲染“远方”的层叠历史背景。

### Phase 2: 初始上传与涂鸦界面的 UI 重构 (The "A" Style)
**目标**：利用现有涂鸦逻辑，套上符合现代极简审美的 Tailwind v4 皮肤。
**核心任务**：
1. **Dropzone**：实现一个全屏居中的大区块，包含简单的虚线边界和“+”图标，支持拖拽和点击。
2. **涂鸦态适配**：将原有的 Canvas 涂鸦组件引入新布局。
3. **Prompt 悬浮舱 (Floating ControlBar)**：在屏幕中下方放置一个类似 Mac 聚焦搜索框 (`max-w-2xl`, `backdrop-blur`, `shadow-2xl`) 的毛玻璃输入框和发送按钮。

### Phase 3: 核心攻坚 - 空间并列布局与 Scroll-jacking
**目标**：实现“左右大图对比”以及“历史在远方层叠”的时光机视觉。
**核心任务**：
1. **双图并列 (Split View)**：使用 CSS Flex/Grid (`w-1/2` 均分屏幕) 渲染当前的 `[基底图, 生成图]` 组合。
2. **历史层叠 (Spatial Z-Depth)**：
   - 利用 `Zustand` 拿到 `getAncestors()` 祖先数组。
   - 使用 `framer-motion` 的 `<motion.div>` 对祖先节点进行循环渲染。
   - 越古老的节点，其 `scale` 越小（如 `0.9, 0.8...`），`translateY` 越高（位于上方），`opacity` 越低，`zIndex` 越小。
3. **滚轮拦截 (Scroll-jacking)**：
   - 在顶层容器绑定 `onWheel` 事件，调用 `e.preventDefault()` 阻止原生滚动。
   - 判断滚轮方向 `deltaY`：向上滚触发回退到 `parentId`，向下滚进入最新生成的 `childId`，并伴随 Framer Motion 的平滑空间位移。

### Phase 4: 开发角落的小地图导航 (Branching Minimap)
**目标**：让用户在复杂的衍变分支中不迷路。
**核心任务**：
1. 在界面的右下角或左下角实现一个绝对定位 (`absolute bottom-8 right-8`) 的悬浮微缩窗口。
2. 引入 `React Flow`（以无背景、极简节点样式运行）。
3. 订阅 `Zustand` 中的全量节点数据，动态生成包含节点缩略图（Thumbnail）和贝塞尔曲线连接线的树状图。
4. 当前活跃的节点施加明显的光晕高亮 (`ring-2 ring-primary`)，点击其他节点则触发 `navigateNode()` 飞梭跳转。

---

