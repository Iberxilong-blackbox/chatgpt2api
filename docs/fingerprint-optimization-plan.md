# 浏览器指纹 / 设备指纹 优化计划

## 一、当前架构风险诊断

### 1.1 致命问题：oai-device-id 非持久化

**当前代码位置：** `internal/backend/backend.go:201`

```go
defaults := map[string]string{
    "oai-device-id":  util.NewUUID(),  // 每次 NewClient 时重新生成
    "oai-session-id": util.NewUUID(),
}
```

当 `buildFingerprint()` 从账号记录中读取不到 `fp.oai-device-id` 时，会**每次**创建一个全新的 UUIDv4。这意味着：

- 同一个 access_token 每次经过 `internal/backend/backend.go:64` 的 `NewClient()` 时，device-id 都会变化
- 就算请求频率不高，只要服务重启、或不同请求路径走了不同的 Client 初始化路径，device-id 就不同
- OpenAI 风控看到的是：同一个 Token 在极短时间内，从成千上万个"全新设备"发起请求

**后续赋值链路：**
- `backend.go:76` → `c.deviceID = c.fp["oai-device-id"]`
- `backend.go:330` → headers 写入 `"OAI-Device-Id": c.deviceID`
- `account.go:1387` → remote session 也发送 `oai-device-id` header

### 1.2 致命问题：全局单代理 + 全账号共用同一指纹模板

**全局代理（单点出口）：**
- `config/config.go:288` → `Proxy()` 返回单个全局代理 URL
- `service/proxy.go:91-108` → 所有账号共用同一个代理出口
- 100 个账号全部位于同一个出口 IP，风控做 group by IP 即可识别为群控

**指纹模板单一化：**
- 全部默认使用 `defaultRemoteProfile = "chrome145"`
- `service/proxy.go:110-129` 的 `applyBrowserProfile()` 对非 Android/iOS/Mac/Linux 的全走 Windows + Chrome
- TLS 指纹、User-Agent、Sec-CH-UA 系列头全部 100% 相同
- 风控通过简单的特征聚合（IP × UA × TLS 指纹 × Device-ID 变化模式）可精确定位

### 1.3 次要问题：频率与并发控制缺失

- `backend.go` 直接创建上游 HTTP Client，没有请求间间隔控制
- 多个并发请求使用同一个 access_token 时，风控会看到同设备同账号的瞬时高频请求

---

## 二、Phase 1 — Quick Win（最高 ROI，代码改动量极小）

### 2.1 方案 A：在账号导入时生成并固化 fp（推荐）

**目标：** 确保每个账号从导入那一刻起就拥有一个永久的 oai-device-id。

**涉及文件：**
- `internal/service/account.go` — 修改 `AddAccountRecords()` 和 `cleanAccountRecords()`
- 导入时，如果记录中没有 `fp` 字段，自动生成默认指纹 JSON

**具体改造：**

在 `cleanAccountRecords()` 中，对每条记录的 `access_token` 生成一个确定性 fingerprint：

```go
type importFingerprint struct {
    OaiDeviceID  string `json:"oai-device-id"`
    OaiSessionID string `json:"oai-session-id"`
    Impersonate  string `json:"impersonate"`
    UserAgent    string `json:"user-agent"`
    SecChUa      string `json:"sec-ch-ua"`
    SecChUaMobile   string `json:"sec-ch-ua-mobile"`
    SecChUaPlatform string `json:"sec-ch-ua-platform"`
}
```

生成规则：
- `oai-device-id`：UUIDv4（纯随机），生成后**永远不变**
- `oai-session-id`：UUIDv4（纯随机），生成后**永远不变**
- `impersonate`：从池中随机分配，支持多平台离散化（见下文 2.4）
- `user-agent` / `sec-ch-ua` / `sec-ch-ua-platform`：与 impersonate 平台保持一致

**已有指纹的兼容：** 如果外部传入的 JSON 已经包含 `fingerprint` 字段（如 CPA JSON 格式），应保留外部传入的指纹。格式映射：

外部传入的格式 → 内部 `fp` 字段映射：
```
fingerprint.browser.ua                    → fp.user-agent
fingerprint.browser.sec_ch_ua             → fp.sec-ch-ua
fingerprint.browser.sec_ch_ua_platform    → fp.sec-ch-ua-platform
fingerprint.browser.sec_ch_ua_mobile      → fp.sec-ch-ua-mobile
fingerprint.codex.user_agent              → 仅用于 codex 场景，暂不处理
seed_hash / activation_id                 → 原生 token 验证用，非浏览器指纹
```

### 2.2 方案 B：确定性哈希后备方案（应急/兜底）

**目标：** 即使数据库中没有 fp，也能保证 device-id 与 access_token 绑定。

**涉及文件：**
- `internal/backend/backend.go` — 修改 `buildFingerprint()` 的默认值生成逻辑

将：
```go
"oai-device-id": util.NewUUID(),
"oai-session-id": util.NewUUID(),
```

改为：
```go
"oai-device-id": deterministicUUID(c.AccessToken, "device"),
"oai-session-id": deterministicUUID(c.AccessToken, "session"),
```

其中 `deterministicUUID` 对 access_token 做 SHA256 后格式化为 UUID v4 格式。只要 access_token 不变，输出永远一致。

```go
func deterministicUUID(seed, namespace string) string {
    h := sha256.Sum256([]byte(namespace + ":" + seed))
    h[6] = (h[6] & 0x0f) | 0x40 // UUID v4
    h[8] = (h[8] & 0x3f) | 0x80
    var buf [36]byte
    hex.Encode(buf[:], h[:4])
    buf[8] = '-'
    hex.Encode(buf[9:13], h[4:6])
    buf[13] = '-'
    hex.Encode(buf[14:18], h[6:8])
    buf[18] = '-'
    hex.Encode(buf[19:23], h[8:10])
    buf[23] = '-'
    hex.Encode(buf[24:], h[10:16])
    return string(buf[:])
}
```

**注意：** 方案 A 和方案 B 不是互斥的，可以同时实施。方案 A 是主动策略，方案 B 是被动兜底。

### 2.3 导入时接受外部传入的 fingerprint（前端 + 后端）

**前端 `account-import-dialog.tsx`：**

CPA JSON 导入模式（`handleCpaSelected`）已经能提取 `access_token`。需要在此基础上：

- 读取 JSON 中的 `fingerprint` 顶级字段（如果存在），将其打包到 `AccountImport` 对象中
- 在 `submitAccountImports` 时，把 `fingerprint` 作为 `fp` 传递给后端

```typescript
// account-import-dialog.tsx 的 parseCpaJson 函数
function parseCpaJson(raw: string) {
  const parsed = JSON.parse(raw);
  const token = getCpaAccessToken(parsed);
  const fingerprint = parsed.fingerprint;
  return {
    access_token: token,
    fp: fingerprint ? JSON.stringify(fingerprint) : undefined,
    // ...
  };
}
```

**后端 `account.go` 的 `AddAccountRecords()`：**

- 当记录中含有 `fp` 字段时，直接存储
- 当记录中没有 `fp` 时，按方案 A 自动生成默认指纹

### 2.4 指纹离散化（已取消 — 外部 JSON 天然覆盖）

> **2026-06-06 更新：此方案取消。**
>
> 经审查实际 CPA JSON 文件（如 `codex-JendroShella204_at_outlook.com.json`），每个 json 自带完整的 `fingerprint.browser.*` 数据：
> - `ua`, `sec_ch_ua`, `sec_ch_ua_platform`, `sec_ch_ua_mobile` → 直接映射
> - `chrome_major` (145) → `impersonate` = `chrome145`
> - `platform` (Win32/Mac/Linux) → 各账号来自不同设备，天然离散
> - `oai_device_id` → 已有稳定设备标识
>
> Case 2（外部 CPA 指纹）已完整覆盖指纹离散化需求，不需要项目内维护模板池。Case 3（纯 token 导入）保留现有默认 Chrome 145 Windows 指纹即可。

### 2.5 目录自动导入 CPA JSON（新增）

**目标：** 用户将 CPA JSON 文件放入指定目录，项目自动读取并导入账号。

**涉及文件：**
- `internal/service/account.go` — 新增 `ImportAccountJSONFiles(dir string)`
- `internal/httpapi/routes.go` — 新增 `POST /api/accounts/import-scan`
- `internal/httpapi/app.go` — 启动时异步扫描
- `internal/config/config.go` — 可选 `CHATGPT2API_IMPORT_DIR` 环境变量

**流程：**

```
{DataDir}/auto_import/           ← 用户放入 .json 文件
    codex-alice.json
    codex-bob.json
        ↓ 启动扫描 / API 触发
    json.Unmarshal → record
        ↓
    AddAccountRecords() → cleanAccountRecords() → prepareAccountFP() Case 2
        ↓
    os.Rename → auto_import/imported/
```

**与现有导入链路的兼容性：**
- CPA JSON 的 `access_token` 在顶层，`cleanAccountRecords()` 已处理
- CPA JSON 的 `fingerprint` 在顶层，`prepareAccountFP()` Case 2 已处理映射
- 纯 token JSON（仅 `{"access_token": "..."}`）同样支持，走 Case 3 默认指纹
- 文件移动防止重复导入

---

## 三、代理层（代理服务商模式）

### 架构决策

**本项目不管理 IP 池**。代理出口能力由下游代理服务商（如 BrightData、911、Proxies 等住宅/移动代理）提供。本项目只需一个固定的上游代理 URL。

```
┌──────────────────────────────────────────────┐
│                本项目服务端                      │
│  CHATGPT2API_PROXY = "http://user:pass@ gw:port" │
│  所有账号共用此入口                               │
└──────────────────┬───────────────────────────┘
                   │ 固定代理入口 URL
                   ▼
┌──────────────────────────────────────────────┐
│              代理服务商网关                      │
│  (BrightData / 911 / 其它)                     │
│  负责：IP 轮转 / 地域选择 / 会话保持            │
└──────────────────┬───────────────────────────┘
                   │ 每次请求 → 不同出口 IP
                   ▼
┌──────────────────────────────────────────────┐
│              OpenAI 风控端                      │
│  看到的是由服务商网络分散后的请求                │
└──────────────────────────────────────────────┘
```

### 3.1 当前架构

- `config/config.go:288` — `Proxy()` 返回 `CHATGPT2API_PROXY` 环境变量的值
- `service/proxy.go:91-108` — 所有 BrowserHTTPClient 调用走此单代理
- 当前已经是"一个固定 URL"架构，与代理服务商模式一致

### 3.2 可优化的方向

| 优化项 | 说明 | 代价 |
|--------|------|------|
| **会话保持（Session Stickiness）** | 利用代理服务商的 sticky session 功能，让同一账号尽可能走同一出口 IP | 取决于服务商 API，通常是 URL 参数 |
| **多账号不同服务商** | 极端情况下，不同账号组走不同的代理服务商入口 | 需要 account 级 proxy_url 支持 |
| **健康检测 & 故障切换** | 监测代理可用性，自动切换到备用入口 | 低 |

### 3.3 未来可能的扩展路径

如果代理服务商模式遇到瓶颈（如 IP 被封过多），可考虑：

**Phase A — 多入口支持（不改 DB）：**
```
CHATGPT2API_PROXY_POOL = "http://user1:pass1@gw1:port,http://user2:pass2@gw2:port"
```
哈希取模分配，每个入口指向不同服务商或不同账号。

**Phase B — 账号级代理绑定（改 DB）：**
在 account 中增加 `proxy_url` 字段（可空），支持为高危账号单独绑定入口。
优先读取 `account.proxy_url`，为空则回退到全局代理或池路由。

---

## 四、Phase 3 — 频率控制与行为模拟（中等代价）

### 4.1 账号级并发锁

**涉及文件：**
- `internal/backend/backend.go` 或新建 `internal/backend/ratelimit.go`

```go
type AccountRateLimiter struct {
    mu          sync.Mutex
    inflight    map[string]int    // token → 当前并发请求数
    lastRequest map[string]time.Time  // token → 上次请求时间
}

func (l *AccountRateLimiter) Acquire(token string) error {
    l.mu.Lock()
    defer l.mu.Unlock()
    if l.inflight[token] >= maxConcurrentPerAccount {
        return fmt.Errorf("account %s has too many concurrent requests", anonymize(token))
    }
    if since := time.Since(l.lastRequest[token]); since < minIntervalBetweenRequests {
        return fmt.Errorf("rate limited: %v since last request", since)
    }
    l.inflight[token]++
    return nil
}
```

### 4.2 请求间隔随机化

在 `backend.go` 的 `StreamConversation()` 和 `StreamMultimodalConversation()` 中，在发起实际 API 调用前增加随机延迟（模拟人类思考间隔）：

```go
// 随机延迟 500ms-3s，模拟人类操作间隔
delay := time.Duration(500+rand.Intn(2500)) * time.Millisecond
select {
case <-time.After(delay):
case <-ctx.Done():
    return
}
```

### 4.3 请求参数随机化

当前 `startTextConversation()` 中的 `client_contextual_info` 是完全硬编码的：

```go
"client_contextual_info": map[string]any{
    "is_dark_mode":      false,
    "time_since_loaded": 1200,
    "page_height":       1072,
    "page_width":        1724,
    "pixel_ratio":       1.2,
    "screen_height":     1440,
    "screen_width":      2560,
    "app_name":          "chatgpt.com",
},
```

建议改为对每个账号、每次会话生成合理范围内的随机值：

```go
func randomClientContext() map[string]any {
    return map[string]any{
        "is_dark_mode":      rand.Intn(2) == 0,
        "time_since_loaded": 300 + rand.Intn(7200),   // 5min - 2h
        "page_height":       700 + rand.Intn(500),
        "page_width":        1200 + rand.Intn(600),
        "pixel_ratio":       []float64{1, 1.25, 1.5, 2}[rand.Intn(4)],
        "screen_height":     []int{1080, 1440, 1600, 1800}[rand.Intn(4)],
        "screen_width":      []int{1920, 2560, 3440}[rand.Intn(3)],
        "app_name":          "chatgpt.com",
    }
}
```

---

## 五、数据流总览

```mermaid
flowchart TB
    subgraph Import["账号导入流程 Phase 1"]
        A1["POST /api/accounts"] --> A2["cleanAccountRecords()\naccount.go:1668"]
        A2 --> A3{"记录含 fp?"}
        A3 -->|"是 - 外部传入"| A4["保留外部指纹\n映射格式兼容"]
        A3 -->|"否 - 自动生成"| A5["随机分配指纹模板\nWindows/macOS/Linux/Firefox"]
        A5 --> A6["生成 oai-device-id\noai-session-id\n永不变化"]
        A4 --> A7["storage.SaveAccounts()"]
        A6 --> A7
    end

    subgraph Runtime["运行时请求"]
        B1["POST /v1/chat/..."] --> B2["NewClient()\nbackend.go:64"]
        B2 --> B3["buildFingerprint()\nbackend.go:180"]
        B3 --> B4{"account.fp 存在?"}
        B4 -->|"是"| B5["使用持久化 fp"]
        B4 -->|"否（兜底）"| B6["SHA256(accessToken)\n确定性 UUID"]
        B5 --> B7["applyBrowserFingerprint()\nbackend.go:214"]
        B6 --> B7
        B7 --> B8["headers() 构造请求头\nbackend.go:309"]
        B8 --> B9["OAI-Device-Id\nSec-CH-UA 系列\nUser-Agent\nAuthorization"]
        B9 --> B10["browserHTTPClientForProfile()\nproxy.go:91"]
        B10 --> B11["surf uTLS impersonate\n按 fp.impersonate 切换\nTLS 指纹"]
        B11 --> B12["CHATGPT2API_PROXY\n固定代理入口"]
        B12 --> B13["代理服务商\n住宅/移动 IP 池"]
        B13 --> B14["OpenAI upstream"]
    end

    subgraph Provider["代理服务商（下游）"]
        C1["BrightData / 911 / 其它"]
        C2["自动 IP 轮转"]
        C3["可选：地域锁定"]
        C4["可选：Sticky Session"]
        C1 --> C2
    end

    B12 -.-> C1
    C2 -.-> B14

    style A5 fill:#90EE90,stroke:#333
    style A6 fill:#90EE90,stroke:#333
    style B6 fill:#90EE90,stroke:#333
    style C1 fill:#E0E0FF,stroke:#333,stroke-dasharray: 5 5
```

---

## 六、各阶段工作量评估

| Phase | 改动点 | 代码量（估） | 风险 | 收益 |
|-------|--------|-------------|------|------|
| ~~1.1~~ | ✅ 导入固化 fp | account.go | 已完成 | — |
| ~~1.2~~ | ✅ 确定性哈希兜底 | backend.go | 已完成 | — |
| ~~1.3~~ | ✅ 前端 fp 透传 | account-import-dialog.tsx | 已完成 | — |
| 1.4 修复 oai_device_id 遗漏 | account.go | ~5 行 | 低 | **高** |
| 1.5 目录自动导入 | account.go + routes.go + app.go | ~80 行 | 低 | **高** |
| ~~2.4 指纹离散化~~ | 已取消（外部 JSON 覆盖） | — | — | — |
| 2.1 代理服务商模式优化 | proxy.go + config.go | ~50 行 | 低 | 中 |
| 2.2 多代理服务商入口 | config.go + proxy.go | ~80 行 | 低 | 中 |
| 2.3 账号级代理绑定 | account.go + proxy.go | ~80 行 | 中 | **高** |
| 3.1 并发锁 + 频率控制 | 新建 ratelimit.go | ~120 行 | 中 | 中 |
| 3.2 请求参数随机化 | backend.go | ~60 行 | 低 | 中 |

---

## 七、推荐实施路线（优先级排序）

```
第一梯队（已完工）：
  Phase 1.1 + 1.2 + 1.3 ：device-id 彻底持久化 + 前端 CPA 指纹透传

第一梯队（待修复）：
  Phase 1.4              ：修复 Case 2 未复用外部 oai_device_id（~5 行）

第二梯队（下一步推进）：
  Phase 1.5              ：目录自动导入 CPA JSON（启动扫描 + API 触发）
  Phase 2.1              ：代理服务商模式优化（sticky session 等）

第三梯队（持续优化）：
  Phase 2.2 + 2.3        ：多服务商入口 / 账号级代理绑定
  Phase 3.1 + 3.2        ：频率控制 + 请求参数随机化

已取消：
  Phase 2.4              ：指纹模板池 — 外部 CPA JSON 自带完整指纹，天然离散化
```
