# 前端架构概览

## 技术栈

| 层面 | 选型 |
|------|------|
| 框架 | React 19 + TypeScript |
| 构建 | Vite 8 + Tailwind CSS v4 + PostCSS |
| 路由 | React Router 7 |
| 动画 | `motion` (Framer Motion) |
| 样式 | Tailwind CSS v4 + `clsx`/`tailwind-merge` (`cn()`) |
| 图标 | `lucide-react` |
| UI 基础 | `@radix-ui/react-*` (dialog, popover, select, checkbox, slot) |
| 通知 | `sonner` |
| 表单 | `react-hook-form` |
| HTTP | `axios` |
| 持久化 | `localforage` + 自定义事件 |
| 日期 | `date-fns` + `react-day-picker` |
| 不变性 | `immer` |

## 组件树

```
<BrowserRouter>
  <App>
    <Toaster />
    <AppShell>
      <TopNav />
      <AnimatedRoutes>
        <PermissionRoute>
          <PageComponent />
        </PermissionRoute>
      </AnimatedRoutes>
    </AppShell>
  </App>
</BrowserRouter>
```

## 目录结构

```
web/src/
├── main.tsx              # 入口：BrowserRouter + App
├── App.tsx               # Toaster + AppShell
├── app/
│   ├── globals.css       # Tailwind CSS 入口
│   ├── app-shell.tsx     # 布局外壳
│   ├── animated-routes.tsx   # 路由过渡动画 + 权限守卫
│   ├── route-config.tsx   # 路由声明
│   ├── page.tsx          # 首页
│   ├── image/            # 创作台（最复杂页面）
│   ├── retouch/          # 局部修图
│   ├── image-manager/    # 图片库
│   ├── login/            # 登录
│   ├── accounts/         # 号池管理
│   ├── users/            # 用户管理
│   ├── rbac/             # 角色权限
│   ├── logs/             # 日志
│   ├── settings/         # 设置
│   ├── register/         # 注册机
│   └── profile/          # 个人中心
├── components/
│   ├── ui/               # shadcn-style primitives
│   │   ├── badge.tsx
│   │   ├── button.tsx
│   │   ├── calendar.tsx
│   │   ├── card.tsx
│   │   ├── checkbox.tsx
│   │   ├── dialog.tsx
│   │   ├── field.tsx
│   │   ├── input.tsx
│   │   ├── popover.tsx
│   │   ├── select.tsx
│   │   ├── table.tsx
│   │   └── textarea.tsx
│   ├── top-nav.tsx       # 顶部导航
│   ├── authenticated-image.tsx
│   ├── image-lightbox.tsx
│   ├── image-task-queue.tsx
│   ├── announcement-banner.tsx
│   ├── page-header.tsx
│   └── ...
├── lib/
│   ├── api.ts            # 所有后端 API 调用 (1800+ 行)
│   ├── request.ts        # axios 实例 + 拦截器
│   ├── session.ts        # 认证会话管理
│   ├── theme.ts          # 主题 (dark/light) 管理
│   ├── utils.ts          # cn() 等工具函数
│   ├── image-path.ts     # 图片路径工具
│   ├── image-size.ts     # 图片文件大小格式化
│   └── use-auth-guard.ts # 权限守卫 hook
├── store/
│   ├── auth.ts           # 权限/会话类型定义
│   ├── image-conversations.ts  # 图片对话 CRUD (500+ 行)
│   └── image-turn-progress.ts  # 任务进度追踪
└── constants/
    └── common-env.ts     # 环境常量
```

## 布局架构

- **AppShell** (`app/app-shell.tsx`) — 全屏容器 `min-h-screen`，水平居中 `max-w-[1440px]`，`flex-col` 纵向排列
- **TopNav** (`components/top-nav.tsx`) — 粘性定位 `sticky top-3 z-40`，毛玻璃背景，收起的导航栏适配移动端
- **AnimatedRoutes** — 使用 `motion` 的 `AnimatePresence mode="wait"` 实现页面切换淡入淡出；尊重用户 `prefers-reduced-motion` 系统设置
- 每个页面独立控制自己的内部布局（flexbox / CSS Grid / 响应式断点）

### 权限守卫

路由定义在 `route-config.tsx`，每个受保护路由有一个 `requiredPath`。`PermissionRoute` 组件检查 `getCachedAuthSession()` 的 `menuPaths`，无权限时 `<Navigate>` 到默认页面。admin 角色绕过所有检查。

## 状态管理模式

- 主要状态不依赖 Zustand
- 所有持久化数据存 **localForage (IndexedDB)**，通过 `window.dispatchEvent` 自定义事件实现跨组件通信
- 常见生命周期模式：

```
页面 mount
  → listImageConversations()      # 从 IndexedDB 读取历史
  → setState(conversations)
  → recoverConversationHistory()  # 恢复中断任务
用户操作 (生成/编辑/删除)
  → saveImageConversation()       # 写回 IndexedDB
  → dispatchEvent(...)            # 通知其他组件
```

## 页面布局模式

### 局部修图页 (`app/retouch/page.tsx`)

```
┌───────────────────────────────────────────────┐
│  图片上传区 (section)                          │
│  ┌───────────────────────────────────────┐    │
│  │  RetouchCanvas  /  空态上传按钮        │    │
│  └───────────────────────────────────────┘    │
├───────────────────────────────────────────────┤
│  xl:grid-cols-[280px_280px_1fr]              │
│  ┌──────────┐ ┌──────────┐ ┌──────────────┐  │
│  │ 标注与    │ │ 提交设置  │ │ 最近结果      │  │
│  │ 提示词    │ │          │ │              │  │
│  └──────────┘ └──────────┘ └──────────────┘  │
└───────────────────────────────────────────────┘
```

- 上传区是 `<section>` + 白色卡片
- 下方用 `<aside className="grid ... xl:grid-cols-[280px_280px_1fr]">` 三列栅格
- 每列是一个 `<Card>` 组件 + 内部 flex/grid 子布局
- 移动端自动折叠为单列

### 创作台 (`app/image/page.tsx`)

```
┌───────────────────────────────────────────────┐
│  工具栏 (模型选择 / 提示词市场 / 参数)          │
├───────────────────────────────────────────────┤
│  ┌──────────┬──────────────────────────────┐  │
│  │ 侧边栏    │ 结果区 / 预设展示卡片           │  │
│  │ 历史记录  │   (masonry 式图片网格)         │  │
│  └──────────┴──────────────────────────────┘  │
├───────────────────────────────────────────────┤
│  底部 Composer Dock (提示词输入 + 参考图 + 提交) │
│  通过 ResizeObserver 动态测量自身高度             │
└───────────────────────────────────────────────┘
```

- 侧边栏 (`image-sidebar.tsx`) 和结果区 (`image-results.tsx`) 通过 flex 并排
- Composer 固定在页面底部 (`image-composer.tsx`)，高度可拖拽调节

## 修改页面布局的指引

1. 主要修改目标页面的 `app/<page>/page.tsx` 中的 JSX 和 Tailwind class
2. 复杂页面的子组件在 `app/<page>/components/` 下
3. 使用 `@/components/ui/` 下的 primitives 组合 UI
4. 新后端 API 在 `lib/api.ts` 中追加
5. 新持久化状态参照 `store/image-conversations.ts` 的模式
6. `app/globals.css` 是 Tailwind v4 入口和全局 CSS 变量定义

### 各页面涉及关键文件速查

| 页面 | 主文件 | 子组件目录 |
|------|--------|-----------|
| 创作台 (image) | `app/image/page.tsx` | `app/image/components/` |
| 局部修图 (retouch) | `app/retouch/page.tsx` | `app/retouch/components/` |
| 设置 (settings) | `app/settings/page.tsx` | `app/settings/components/` |
| 注册机 (register) | `app/register/page.tsx` | `app/register/components/` |
| 号池管理 (accounts) | `app/accounts/page.tsx` | `app/accounts/components/` |

---

*参见同级目录下的其他文档以了解特定功能流程。*
