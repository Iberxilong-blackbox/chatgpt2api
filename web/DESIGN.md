# Design Principles (设计原则)

为了保持界面美观和现代感，所有由 AI 生成或修改的组件必须严格遵守以下原则：

## 1. Layout & Scrollbar Constraints (布局与滚动条限制)
- **非必要，绝不出现滚动条**：页面高度应当严格契合视口（Viewport-aware height，如 `h-screen` 或结合 flexbox 自动填满空间）。能全屏展示的信息尽量一次性全部展示，禁止由于容器高度未计算对齐而产生不合理的纵向或横向滚动条。
- **允许滚动的例外场景**：仅允许在确实无法一次性展示的长对话页面（聊天历史）、长列表/长表格以及系统级日志控制台（Console）中使用局部滚动条。

## 2. Button Design (按钮设计)
- **图标化与轻量化**：按钮展示应尽量少用冗长的文字说明。
- **实现方式**：优先使用语义化的图标（必须来自 `lucide-react`）作为按钮展示。对于复杂的交互，可以使用"轻量图标 + 悬浮提示（Tooltip）"的组合，使界面保持呼吸感和现代极简风。
- **无障碍性要求**：视觉极简不能牺牲语义。使用纯图标按钮时，必须添加 `aria-label` 属性提供按钮语义，或在按钮内部添加 `<span className="sr-only">文字说明</span>` 供屏幕阅读器读取。如果图标 + 文字同时存在（如"撤销"按钮内含图标），组件本身已具备语义文本，可以不加 `aria-label`。

## 3. Height Cascade & Layout Reference (高度级联与布局参考系)
- **`h-full` 全链路传承规则**：使用 `h-full`（`height: 100%`）时，必须确保从视口到目标元素的**每一层 DOM** 都有确定高度，否则 `h-full` 会退化为 `auto`，造成不可预期的布局偏移。排查方法：在 DevTools 中沿 DOM 树向上逐层检查计算高度，找到第一个 `auto` / `0` 的节点即断裂点。
- **优先使用 `absolute inset-0` / `fixed inset-0` 撑满容器**：全屏或沉浸式页面中，优先用 `absolute inset-0`（相对于最近 positioned 父级）或 `fixed inset-0`（完全脱离文档流相对于视口），替代 `h-full` 级联方案。它们的尺寸不依赖父级高度传递链，更健壮。
- **⚠️ `fixed` 滥用警告——层叠上下文（Stacking Context）与 Z-index 灾难**：`fixed inset-0` 创建新的层叠上下文，如果在组件树深处滥用，会导致 z-index 管理陷入"军备竞赛"——每个深层组件都要不断加大 z-index 来覆盖彼此，最终难以维护。**`fixed inset-0` 应仅限于全局根容器（如 AppShell），以及真正的全局浮层（Modal / Toast / Drawer 且包裹在 Portal 中）**。不要在组件树深处随意使用 `fixed` 定位来达成视觉上的"填满"。
- **嵌套容器内用百分比约束，全屏顶层才用视口单位**：在嵌套容器内约束子元素大小时，优先使用百分比（`max-h-full`、`w-full`），让子元素的参考系与父容器一致。仅在真正的全屏顶层元素（如 AppShell 的 `fixed inset-0`）使用视口单位（`h-dvh`、`w-dvw`）。禁止在深层子元素中用 `calc(100dvh - 某某px)` 做约束——这混用了两个参考系（视口 vs 容器），父容器尺寸变化时需要重新计算 magic number。
- **`h-dvh` vs `min-h-dvh` 选型指南**：`h-dvh` 强制等于动态视口高度，`min-h-dvh` 确保至少等于动态视口高度但允许更长。全屏根容器如果使用 `overflow-hidden`（如 retouch 页面），必须用 `h-dvh`（精确锁定视口，不允许滚动）；普通内容页面推荐使用 `min-h-dvh`，防止极端小屏幕下内容被截断。简单规则：**"我要填满，不允许滚动"用 `h-dvh`；"我要至少一屏高，但可以更长"用 `min-h-dvh`**。
- **布局偏移排查第一步：检查高度传递链路**：任何使用 `h-full` / `h-screen` 的页面出现内容溢出、底部元素跑出视口时，调试第一步是沿着 DOM 树向上检查每一层的计算高度，找到高度断链点。

## 4. Flexbox Alignment & Height Stretch Traps (Flexbox 对齐与高度拉伸陷阱)
- **`align-items`/`justify-content` 会覆盖 `flex-1` 的拉伸行为**：`align-items` 控制**交叉轴**方向。在 `flex-row` 中交叉轴是纵轴（高度），在 `flex-col` 中交叉轴是横轴（宽度）。使用 `items-center` / `items-end` 等非 `stretch` 值时，子元素的交叉轴尺寸会退回到内容高度/宽度，不再继承父容器的确定尺寸。
- **`min-height: auto` 是 Flexbox 的默认陷阱**：Flex 子元素的 `min-width` 和 `min-height` 默认值为 `auto`（而非 `0`）。这意味着当子元素内部包含超大内容（Canvas、Img、长文本）时，即使父容器通过 `flex-1` 分配了一个较小的尺寸，子元素也会拒绝收缩到小于其内容尺寸——导致撑破布局。**只要使用了 `flex-1` 或 `flex-shrink-1` 准备让元素按比例分配空间，且内部包含可能超大的媒体元素或长列表，必须同时在子元素上加 `min-h-0`（`flex-col` 场景）或 `min-w-0`（`flex-row` 场景），或者设置 `overflow-hidden`**。
- **`flex-1` 只控制主轴方向**：`flex-1`（`flex-grow: 1`）仅在主轴方向生效。`flex-row` 下它控制宽度，不影响高度；`flex-col` 下它控制高度，不影响宽度。不要误以为 `flex-1` 能让元素同时在两个轴向上拉伸。
- **`self-stretch` / `self-start` / `self-center` 覆盖父级的 `align-items`**：当父容器必须维持 `items-center` 等对齐方式时，你可以在特定子元素上用 `self-stretch`（`align-self: stretch`）单独覆盖，让该子元素沿交叉轴拉伸填满父容器。
- **典型故障场景——retouch 页面上传图片后 canvas 撑满全屏**：`<motion.div className="flex items-center justify-center">` 作为 flex-row 容器时，内部的 retouch 卡片高度退回到内容高度。canvas 的 intrinsic size（4000×3000）决定容器高度，形成"内容决定容器、容器又要约束内容"的循环依赖，`max-h-full` 失效。修复：在卡片容器加 `self-stretch`，使其沿父级交叉轴拉伸到确定高度，恢复约束链。
- **排查方法**：在 DevTools 中选中溢出元素，检查父容器链中每个 flex 容器的 `align-items` 计算值。如果某层是 `center`/`flex-start`/`flex-end` 而非 `stretch`（默认值），且该层子元素出现异常的高度/宽度溢出，则此处可能是断裂点。在 Elements 面板中临时勾选/取消 `align-items: stretch` 即可快速验证。

### 预防原则：分离尺寸容器与对齐容器

在撰写任何 flex 布局代码前，先想清楚三个问题：

1. **这个容器是负责尺寸分配（sizing），还是视觉对齐（alignment）？**
2. **如果子元素需要填满父容器，交叉轴上能否使用 `stretch`？**
3. **如果既有填充需求又有居中需求，能不能拆成两层？**

**坏的做法（本次故障的写法）—— 尺寸与对齐混在同一层：**

```tsx
{/* ❌ 既负责填充（size-full），又负责对齐（items-center） */}
<motion.div className="flex size-full items-center justify-center">
  <div className="flex-1 flex-col">
    <RetouchCanvas />
  </div>
</motion.div>
```

**好的做法——拆成两层：外层只做尺寸/填充，内层只做视觉对齐：**

```tsx
{/* ✅ 外层只负责填充尺寸 */}
<motion.div className="flex size-full">
  {/* ✅ 内层只负责视觉居中，不参与尺寸约束 */}
  <div className="flex flex-1 items-center justify-center">
    <div className="flex-1 flex-col self-stretch">
      <RetouchCanvas />
    </div>
  </div>
</motion.div>
```

> 或者更简洁的写法：如果子元素已经用 `flex-1` 填满容器，就不需要 `items-center justify-center` 了——因为没有剩余空间需要分配。
>
> ```tsx
> {children} {/* flex-1 子元素会自然撑满 */}
> ```

**关键原则**：`align-items: stretch`（默认值）是唯一能让子元素沿交叉轴拉伸的值。一旦改为 `center` / `start` / `end`，你就切断了父容器对子元素交叉轴尺寸的控制。如果子元素需要填满父容器，就不要在同一个容器上使用非 `stretch` 的对齐值。必要时拆成两层，让布局与对齐各司其职。

### 更优解法：使用 CSS Grid 替代 Flex 嵌套

"拆两层"方案虽然正确，但现代 CSS 有更优雅的解法——用 **CSS Grid** 替代 flex 布局中的尺寸+居中层。Grid 定义的 track 尺寸（如 `1fr`）具有确定的包含块尺寸，不会像 flex 那样被内容撑开形成循环依赖：

```tsx
{/* ✅ 使用 Grid，一层解决填充 + 绝对居中 */}
<motion.div className="grid size-full min-h-0 max-w-6xl place-items-center">
  <div className="min-h-0 w-full rounded-[32px] bg-white/72 p-4 shadow ...">
    <RetouchCanvas className="flex-1" />
  </div>
</motion.div>
```

> `place-items-center` 是 `align-items: center; justify-items: center` 的简写。在 Grid 中，子元素的包含块是 grid cell——它的尺寸由 track 定义决定，不依赖子元素内容。因此 `max-h-full` 等百分比约束始终有确定的参考系，不会退化。
>
> **适用场景**：当你需要一个容器"既有确定尺寸约束、同时内容绝对居中"时，`grid place-items-center` 是最简洁且健壮的方案。当子元素需要按比例分配空间（如左侧导航 + 右侧内容）时，用 Flexbox 更自然。两者互补，不存在替代关系。
