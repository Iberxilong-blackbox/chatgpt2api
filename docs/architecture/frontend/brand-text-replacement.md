# 前端品牌名称替换清单

> 列出前端代码中所有 `chatgpt2api` 字样的出现位置，按用户可见性分类。

---

## 一、用户可见（需优先替换，共 8 处）

### 1. 登录页 — 品牌标题

| 项目 | 内容 |
|------|------|
| **文件** | `web/src/app/login/page.tsx:176` |
| **代码** | `{appMeta.app_title \|\| "chatgpt2api"}` |
| **位置** | 登录页左上角品牌名称区域。当后端未返回自定义 `app_title` 时，显示 "chatgpt2api"。 |

### 2. 登录页 — 注册模式描述文字

| 项目 | 内容 |
|------|------|
| **文件** | `web/src/app/login/page.tsx:195` |
| **代码** | `` `创建账号后进入 ${appMeta.app_title \|\| "chatgpt2api"} 控制台。` `` |
| **位置** | 登录页注册模式下，表单上方的提示文案。 |

### 3. 登录页 — 登录模式描述文字

| 项目 | 内容 |
|------|------|
| **文件** | `web/src/app/login/page.tsx:196` |
| **代码** | `` `使用账号和密码进入 ${appMeta.app_title \|\| "chatgpt2api"} 控制台。` `` |
| **位置** | 登录页登录模式下，表单上方的提示文案。 |

### 4. 默认 app_title 定义（上游源头）

| 项目 | 内容 |
|------|------|
| **文件** | `web/src/lib/app-meta.ts:25` |
| **代码** | `app_title: "chatgpt2api"` |
| **说明** | `appMeta.app_title` 的默认值来源，直接影响上述 3 处渲染。若后端返回自定义值则被覆盖。 |

### 5. 设置页 — 更新源输入框默认值

| 项目 | 内容 |
|------|------|
| **文件** | `web/src/app/settings/components/version-update-card.tsx:101` |
| **代码** | `: "ZyphrZero/chatgpt2api";` |
| **位置** | 设置页 → 版本更新卡片 → "更新源" 输入框的默认值。 |

### 6–8. 设置 store 中的相关默认值

| 项 | 文件 | 行 | 内容 | 说明 |
|----|------|----|------|------|
| 6 | `web/src/app/settings/store.ts` | 108 | `: "ZyphrZero/chatgpt2api",` | `update_repo` 默认值，影响第 5 项 |
| 7 | `web/src/app/settings/store.ts` | 350 | `: "ZyphrZero/chatgpt2api").trim(),` | 保存配置时 `update_repo` 回退默认值 |
| 8 | `web/src/app/settings/store.ts` | 578 | `app_title: "chatgpt2api"` | 保存登录页图片配置时 `app_title` 回退默认值，影响第 1–3 项 |

---

## 二、条件可见（建议一并替换，共 2 处）

| 文件 | 行 | 内容 | 说明 |
|------|----|------|------|
| `web/src/lib/app-meta.ts` | 26 | `project_name: "chatgpt2api"` | 当后端 `app_title` 被覆盖且不等于 `project_name` 时，登录页副标题会显示 "chatgpt2api" |
| `web/src/app/settings/store.ts` | 579 | `project_name: "chatgpt2api"` | 同上，保存登录页图片配置时的回退值 |

---

## 三、内部标识（不显示给用户，可选替换，共 26 处）

### 已注释代码

| 文件 | 行 | 内容 |
|------|----|------|
| `web/src/app/login/page.tsx` | 39 | `// const githubUrl = "https://github.com/ZyphrZero/chatgpt2api";` |

### localStorage 存储键名

| 文件 | 行 | 内容 |
|------|----|------|
| `web/src/app/image/page.tsx` | 121 | `"chatgpt2api:image_composer_mode"` |
| `web/src/app/image/page.tsx` | 122 | `"chatgpt2api:image_last_model"` |
| `web/src/app/image/page.tsx` | 123 | `"chatgpt2api:image_last_size"` |
| `web/src/app/image/page.tsx` | 124 | `"chatgpt2api:image_last_size_mode"` |
| `web/src/app/image/page.tsx` | 125 | `"chatgpt2api:image_last_aspect_ratio"` |
| `web/src/app/image/page.tsx` | 126 | `"chatgpt2api:image_last_resolution"` |
| `web/src/app/image/page.tsx` | 127 | `"chatgpt2api:image_last_custom_ratio"` |
| `web/src/app/image/page.tsx` | 128 | `"chatgpt2api:image_last_custom_width"` |
| `web/src/app/image/page.tsx` | 129 | `"chatgpt2api:image_last_custom_height"` |
| `web/src/app/image/page.tsx` | 130 | `"chatgpt2api:image_last_output_format"` |
| `web/src/app/image/page.tsx` | 131 | `"chatgpt2api:image_last_output_compression"` |
| `web/src/app/image/similar-image-intent.ts` | 1 | `"chatgpt2api:image_similar_intent"` |
| `web/src/store/image-conversations.ts` | 107 | `"chatgpt2api:image_active_conversation_id"` |
| `web/src/store/auth.ts` | 34 | `"chatgpt2api_auth_session"` |
| `web/src/lib/theme.ts` | 3 | `"chatgpt2api:color-theme"` |

### 自定义 DOM 事件名

| 文件 | 行 | 内容 |
|------|----|------|
| `web/src/app/accounts/page.tsx` | 71 | `"chatgpt2api:quota-refresh"` |
| `web/src/components/top-nav.tsx` | 44 | `"chatgpt2api:quota-refresh"` |
| `web/src/app/image/page.tsx` | 132 | `"chatgpt2api:quota-refresh"` |
| `web/src/store/image-turn-progress.ts` | 9 | `"chatgpt2api:image-turn-progress-changed"` |
| `web/src/store/image-conversations.ts` | 106 | `"chatgpt2api:image-conversations-changed"` |
| `web/src/store/image-conversations.ts` | 108 | `"chatgpt2api:image-open-conversation"` |
| `web/src/lib/session.ts` | 15 | `"chatgpt2api:auth-session-change"` |
| `web/src/lib/app-meta.ts` | 11 | `"chatgpt2api:app-meta-updated"` |

### IndexedDB 数据库名

| 文件 | 行 | 内容 |
|------|----|------|
| `web/src/store/image-conversations.ts` | 102 | `name: "chatgpt2api"` |
| `web/src/store/auth.ts` | 37 | `name: "chatgpt2api"` |

---

## 建议替换顺序

1. **一、用户可见（8 处）** → 改完即可清除前端所有用户可见的 "chatgpt2api" 字样
2. **二、条件可见（2 处）** → 建议一并改掉防止遗漏
3. **三、内部标识（26 处）** → 用户看不到，可改可不改。注意 localStorage/event 键名改名后旧存储数据会丢失，需评估影响

> 注："chat2api" 在前端代码中**未出现**。
