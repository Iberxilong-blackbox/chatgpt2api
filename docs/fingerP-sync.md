# 指纹同步开发计划

> **状态**：Step 1 ✅ 完成 | Step 2 ✅ 完成 | 待验证

## 问题

通过 `chatgpt_login.py` 登录获取 session JSON 并导入到 chatgpt2api 后，账号在号池中显示正常（Bootstrap ✅ + CheckSession ✅），但实际发消息时 ChatGPT 后端 API 返回 `token_invalidated`。

**根因**：ChatGPT 后端 API 将 `access_token` 与浏览器当前的 `OAI-Device-Id` 和 `OAI-Session-Id` 绑定。当 API 代理使用不同的设备/会话 ID 发送请求时，后端判定 token 被盗用 → 返回 `token_invalidated`。

## 现有资源

### 1. 登录脚本产出的 JSON 已有指纹数据

`fingerprint.browser` 包含：

| JSON 字段 | 示例值 |
|---|---|
| `fingerprint.oai_device_id` | `308b78cc-5523-4e70-9520-9f99023b72a6` |
| `fingerprint.browser.ua` | `Mozilla/5.0 (...) Chrome/145.0.4867.78 Safari/537.36` |
| `fingerprint.browser.sec_ch_ua` | `"Chromium";v="145", "Google Chrome";v="145", ...` |
| `fingerprint.browser.sec_ch_ua_mobile` | `?0` |
| `fingerprint.browser.sec_ch_ua_platform` | `"Windows"` |
| `fingerprint.browser.chrome_major` | `145` |
| `fingerprint.browser.chrome_version` | `145.0.4867.78` |

**缺失（已解决）**：`oai_session_id` — 通过 Playwright 请求拦截从出站 API 请求 header 中捕获。

### 2. Go 后端已有指纹处理能力

`prepareAccountFP()`（`internal/service/account.go:1760`）已支持 `fingerprint.browser.*` 格式，能将其映射为内部 `fp` 字段：

| 内部 fp key | 来源 |
|---|---|
| `oai-device-id` | `fingerprint.oai_device_id` |
| `oai-session-id` | `fingerprint.oai_session_id`（缺失时生成随机 UUID） |
| `user-agent` | `fingerprint.browser.ua` |
| `sec-ch-ua` | `fingerprint.browser.sec_ch_ua` |
| `sec-ch-ua-platform` | `fingerprint.browser.sec_ch_ua_platform` |
| `sec-ch-ua-mobile` | `fingerprint.browser.sec_ch_ua_mobile` |
| `impersonate` | `fingerprint.browser.chrome_major` → `"chrome145"` |

### 3. 两个导入路径

当前 chatgpt2api 有两个导入接口：

| 接口 | 路径 | 是否处理 fingerprint |
|---|---|---|
| Token/Account 批量导入 | `POST /api/accounts`（`AddAccountRecords`） | ✅ 已支持（`prepareAccountFP`） |
| Session JSON 导入 | `POST /api/accounts/session`（`AddAccountFromSession`） | ✅ 已支持 |
| 前端 Session 导入对话框 | 调用 `/api/accounts/session` | ✅ 已支持 |

## 改动方案

### Step 1 — Python 登录脚本：捕获 `oai-session-id` ✅

**文件**：`chatgpt_login.py`、`fingerprint.py`

**关键发现**：`oai-session-id` **不在 localStorage / sessionStorage 中**，而是 ChatGPT 前端 JS 在内存中生成，每次 API 请求时动态附加到 header。因此必须通过 Playwright 请求拦截捕获。

**方案**：在 `page` 创建后注册 `page.on("request")`，从首个携带 `oai-session-id` header 的请求中提取值，写入 `Fingerprint._oai_session_id`。

```python
# chatgpt_login.py — 在 CDP anti-detection 之后、任何导航之前注册
_captured_oai_sid = []

def _on_request(request):
    if _captured_oai_sid:
        return
    try:
        h = {k.lower(): v for k, v in (request.headers or {}).items()}
        sid = h.get("oai-session-id", "")
        if sid:
            _captured_oai_sid.append(sid)
            fingerprint._oai_session_id = sid
            print(f"    [fingerprint] Captured oai-session-id: {sid}")
    except Exception:
        pass

page.on("request", _on_request)
```

**配套改动**：`fingerprint.py` 的 `Fingerprint` 类：
- `__init__` 新增 `oai_session_id=None` 参数，存储为 `self._oai_session_id`
- `to_dict()` 输出 `"oai_session_id": self._oai_session_id`
- `from_dict()` 还原 `fp._oai_session_id = d.get("oai_session_id", "")`
- `main()` 非 codex 路径补充 `account["fingerprint"] = fingerprint.to_dict()`

写入 JSON 的 `fingerprint` 块：

```json
"fingerprint": {
    "oai_device_id": "...",
    "oai_session_id": "...",    # ← 从请求 header 捕获
    "browser": { ... }
}
```

### Step 2 — Go 后端 `AddAccountFromSession`：转发 fingerprint ✅

**文件**：`internal/service/account.go` — `AddAccountFromSession()` 方法

保留原有 `session` struct 解析保证兼容性，新增一次 `json.Unmarshal` 到 `map[string]any` 以提取 `fingerprint` 块，调用 `prepareAccountFP()` 转换后写入 `updates["fp"]`。

**实际实现**：

```go
// 原有 struct 解析保持不变
var session struct { ... }
json.Unmarshal([]byte(sessionJSON), &session)

// 新增：解析完整 JSON 提取 fingerprint（best-effort，非致命）
var record map[string]any
json.Unmarshal([]byte(sessionJSON), &record)

// ... 原有 session 验证、刷新逻辑 ...

// 新增：处理 fingerprint
if record != nil {
    updates["fp"] = prepareAccountFP(record)
}
```

`prepareAccountFP()` 已支持从 `record["fingerprint"]` 中读取 `oai_session_id`（缺失时自动生成随机 UUID），无需额外修改。

### Step 3 — 前端 Session 导入对话框：无需改动

因为后台改为了从完整 JSON 中提取 `fingerprint`，前端只需要把 `chatgpt_login.py` 生成的完整 JSON 粘贴到导入框即可。

### Step 4（可选） — 如果用 Token 导入方式

如果不想走 session 导入路径，也可以用 `POST /api/accounts`（批量导入）方式直接导入整条记录。该路径已通过 `AddAccountRecords` → `prepareAccountFP` 支持 fingerprint。但需要注意**不触发 RefreshSession**，避免 token 被提前轮换。

## 验证方法

1. 用 `chatgpt_login.py` 登录并生成新 JSON（含 `oai_session_id`）
2. 通过 Session 导入到号池
3. 对该账号点「诊断」按钮
4. 确认 Bootstrap ✅ + CheckSession ✅
5. 发一条消息，确认不再返回 `token_invalidated`
6. 回到浏览器页面刷新确认浏览器端仍可正常对话

## 涉及文件

| 文件 | 改动类型 | 状态 |
|---|---|---|
| `phone-reg/chatgpt_login.py` | 新增 `page.on("request")` 拦截捕获 `oai-session-id` | ✅ |
| `phone-reg/fingerprint.py` | `Fingerprint` 类新增 `oai_session_id` 字段 | ✅ |
| `chatgpt2api/internal/service/account.go` | `AddAccountFromSession` 增加 fingerprint 处理 | ✅ |

## 数据流

```
chatgpt_login.py                          Go AddAccountFromSession
─────────────────                       ──────────────────────────
page.on("request") 拦截                   json.Unmarshal → record
  ↓ 捕获 Oai-Session-Id header           prepareAccountFP(record)
fingerprint._oai_session_id = sid           ↓
  ↓                                     updates["fp"] = fp
fingerprint.to_dict()                      ↓
  ↓                                     UpdateAccount → 存储到 account["fp"]
写入 account JSON
                                          buildFingerprint() 读取
                                            ↓
                                          API 请求携带真实 Oai-Session-Id
```

## 优先级建议

1. **Step 1**（Python `oai-session-id` 捕获）— 必须，否则仍缺少关键指纹
2. **Step 2**（Go 后端 fingerprint 转发）— 必须，否则导入时不存指纹
3. **验证** — 全流程测试
