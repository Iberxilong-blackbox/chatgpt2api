# Cloudflare 403 诊断：IP 问题 vs TLS 指纹问题

## 现象

升级 WARP 代理 IP 后，项目运行报错：

```
bootstrap failed: HTTP 403, upstream returned Cloudflare challenge page;
refresh browser fingerprint/session or change proxy
```

刷新账号结果：**成功 0 个，失败 1 个**。

代理访问 `https://chatgpt.com/` 时，项目内部的 HTTP 客户端返回 403，但 `curl` 通过同一个代理却能正常获取主页。

## 环境

| 项目 | 值 |
|------|-----|
| 服务器 | Desi (`45.15.124.192`) |
| 代理协议 | socks5h |
| 代理地址 | `127.0.0.1:10086` |
| WARP 代理出口 IP | `107.170.226.114`（测试时） |
| 项目代理配置 | `CHATGPT2API_PROXY=socks5h://127.0.0.1:10086` |

## 诊断过程

### 步骤 1：验证代理本身是否连通

```bash
curl -s -X POST http://localhost:8822/api/proxy/test \
  -H "Authorization: Bearer sess--<token>" \
  -H "Content-Type: application/json" \
  -d '{}'
```

结果：

```json
{"result":{"error":null,"latency_ms":37,"ok":true,"status":403}}
```

- `ok: true`（status < 500）→ 代理能连通 `chatgpt.com`
- `status: 403` → chatgpt.com 返回 403
- `latency_ms: 37` → 延迟很小，请求确实走了代理

### 步骤 2：通过 curl + socks5h 访问 chatgpt.com（用系统真实 TLS 栈）

```bash
curl -sS --socks5-hostname 127.0.0.1:10086 https://chatgpt.com/ | head -20
```

结果：

```html
<html>
  <head>
    <meta name="viewport" content="width=device-width, initial-scale=1" />
    <!-- ... ChatGPT 正常主页 HTML，含 logo SVG ... -->
```

**curl 通过同一个代理能正常拿到主页（200）**。这说明代理出口 IP 没有被 Cloudflare 封锁。

### 步骤 3：对比测试 — curl 直连 chatgpt.com

```bash
curl -sS https://chatgpt.com/ | head -20
```

结果：返回**完全相同的**正常主页 HTML。

**服务器本机 IP 也没有被封锁。**

### 步骤 4：确认代理出口 IP 和服务器本机 IP

```bash
# 通过代理访问 IP 查询服务
curl --socks5-hostname 127.0.0.1:10086 https://api.ipify.org
# → 107.170.226.114

# 直连访问 IP 查询服务
curl -s https://api.ipify.org
# → 45.15.124.192
```

### 步骤 5：查找项目的 Bootstrap 请求实现

在 `internal/backend/backend.go` 中找到 `Bootstrap()` 方法：

```go
func (c *Client) Bootstrap(ctx context.Context) error {
    req, _ := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+"/", nil)
    for key, value := range c.bootstrapHeaders() {
        req.Header.Set(key, value)
    }
    resp, err := c.httpClient.Do(req)
    // ...
    if resp.StatusCode < 200 || resp.StatusCode >= 300 {
        return upstreamHTTPError("bootstrap", resp.StatusCode, data)
    }
}
```

其中 `c.httpClient` 来自 `proxy.BrowserHTTPClientWithProfile()`，底层使用 `surf` 库 + 浏览器指纹模拟。

### 步骤 6：检查项目中硬编码的浏览器指纹常量

**`internal/backend/backend.go`（第 26-38 行）：**

```go
browserUserAgent              = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/145.0.0.0 Safari/537.36"
browserSecCHUA                = `"Not:A-Brand";v="99", "Google Chrome";v="145", "Chromium";v="145"`
browserSecCHUAFullVersion     = `"145.0.0.0"`
browserSecCHUAFullVersionList = `"Not:A-Brand";v="99.0.0.0", "Google Chrome";v="145.0.0.0", "Chromium";v="145.0.0.0"`
browserSecCHUAMobile          = "?0"
browserSecCHUAPlatform        = `"Windows"`
browserSecCHUAPlatformVersion = `"19.0.0"`
browserSecCHUAArch            = `"x86"`
browserSecCHUABitness         = `"64"`
browserImpersonationProfile   = "chrome145"
DefaultClientVersion          = "prod-be885abbfcfe7b1f511e88b3003d9ee44757fbad"
DefaultClientBuildNumber      = "5955942"
```

**`internal/browserfp/browserfp.go`（第 108 行）：**

```go
const DefaultBuildID = "prod-2e2e6a5279d822603df0be74f1018da3099d7573"
```

## 分析

### 分离变量

| 变量 | curl + socks5h | Go 程序 + socks5h |
|------|---------------|-------------------|
| 代理出口 IP | `107.170.226.114` | `107.170.226.114` |
| 目标域名 | `chatgpt.com` | `chatgpt.com` |
| 结果 | ✅ 200 正常主页 | ❌ 403 Cloudflare |
| TLS 栈 | 系统 OpenSSL（真实浏览器级） | Go 标准库 + `surf` impersonate |

两个测试的唯一区别是 **TLS 客户端栈不同**。

### 错误消息解读

```
bootstrap failed: HTTP 403, upstream returned Cloudflare challenge page;
refresh browser fingerprint/session or change proxy
```

错误来自 `internal/backend/backend.go:1090`：

```go
func isCloudflareChallengeBody(lower string) bool {
    return strings.Contains(lower, "cf_chl") ||
        strings.Contains(lower, "challenge-platform") ||
        strings.Contains(lower, "enable javascript and cookies to continue") ||
        strings.Contains(lower, "cloudflare")
}
```

### 根因

`surf` 库的 `Impersonate().Chrome()` 模拟的**TLS 握手特征**（JA3 指纹、ALPN、支持的密码套件等）与 Cloudflare 期望的真实 Chrome 145 不符。Cloudflare 在 TLS 层识别到客户端并非真实浏览器，直接返回 challenge 页阻止请求。

**不是 IP 问题**——IP 更换后 curl 能正常访问；
**不是代理连通性问题**——延迟 37ms；
**是 Go 程序 TLS 指纹模拟被 Cloudflare 识别为自动化工具。**

## 最终结论

> **当前项目无法访问 chatgpt.com 的原因是 Go 程序的 `surf` 库 TLS 指纹模拟不够新/不够真实，被 Cloudflare WAF 检测到并拦截。更换代理 IP 无法解决此问题。**

## 后续修复方向

### 修复 1：更新硬编码的浏览器版本常量

更新 `internal/backend/backend.go` 中的 Chrome 版本号到当前最新（如 Chrome 127+/128+）：

| 常量 | 当前值（过时） | 需要更新为 |
|------|---------------|-----------|
| `browserUserAgent` | Chrome/145 | 当前最新 Chrome 版本 |
| `browserImpersonationProfile` | chrome145 | 对应新版本的 profile 名 |
| `browserSecCHUA` / fullVersion / fullVersionList | v="145" | 对应新版本号 |
| `DefaultClientVersion` | prod-be885a... | chatgpt.com 当前 data-build |
| `DefaultClientBuildNumber` | 5955942 | chatgpt.com 当前 build 号 |
| `DefaultBuildID` | prod-2e2e6a... | chatgpt.com 当前 data-build |

### 修复 2：升级 `surf` 库或更换 impersonate 方案

- `surf` 库的 impersonate 能力取决于其内置的 TLS 参数库
- 如果版本过旧，考虑升级 `github.com/enetx/surf` 依赖

### 修复 3：自动化指纹更新

- 在项目启动时或定时任务中自动从 chatgpt.com 拉取最新的 `data-build` 和 `build-number`

### 修复 4：更换代理类型

- socks5h + WARP 出口均为数据中心 IP，Cloudflare 对数据中心 IP 的 TLS 检测较为严格
- 可尝试住宅代理或原生 ISP 代理，但这些也需配合正确的 TLS 指纹才能通过检测

> **最终判断依据**：当 curl（真实 TLS 栈）走同一代理能正常访问，而程序走同一代理返回 403 时，问题一定在 TLS 指纹/HTTP 客户端层面，不在网络/代理/IP 层面。

---

## 补充分析：根因与 JSON 版本号的关系

### 为什么之前能用，某天突然不行了

Chrome 145 确实存在，但 surf 库中的 `HelloChrome_145` 是**手工推测拼出来的** TLS 参数（`profiles/chrome/145.go`），而非从真实浏览器流量抓包提取。Cloudflare 更新 WAF 规则后，识别出了这份手工指纹的异常特征，于是开始拦截。

### JSON 中的 `chrome_major` 从未影响 TLS 指纹

账号 JSON 文件的 `fingerprint.browser.chrome_major` 字段只决定了：

- `fp["impersonate"]` 字符串（如 `"chrome145"`）
- UA / Sec-CH-UA header 中的版本号

但在 `internal/service/proxy.go:applyBrowserProfile()` 中，profile 字符串只用于判断：
- **OS**：是否包含 android/ios/mac/linux 关键词
- **浏览器类型**：是否包含 firefox/ff 关键词 → Chrome vs Firefox

**版本号（145/148/200）被完全忽略**。无论 JSON 里写什么版本，pre-fix 的 surf 都写死使用 `HelloChrome_145`；post-fix 也都写死使用 uTLS `HelloChrome_Auto`。

## 实际修复（2026-07-24）

### 根因

surf 的 `Impersonate().Chrome()` 内部调用 `JA().Chrome145()`，使用 surf 库手工拼的 `HelloChrome_145` TLS ClientHello 参数，被 Cloudflare WAF 识别为假指纹。

### 修复方案

在 `applyBrowserProfile()` 中，`impersonate.Chrome()` 返回后追加 `b.JA().Chrome()`，将 TLS 指纹从 surf 手工拼的 Chrome 145 覆盖为**uTLS 从真实 Chrome 133 流量抓包提取的 `HelloChrome_Auto`**（当前等价于 `HelloChrome_133`）。

```go
// internal/service/proxy.go — applyBrowserProfile()
b := impersonate.Chrome()
// Override: surf's hand-crafted Chrome 145 spec → uTLS's real Chrome 133 spec
b.JA().Chrome()  // = HelloChrome_Auto = HelloChrome_133
```

`b.JA().Chrome()` 设置 `HelloChrome_Auto`（`HelloChrome_133`），根据 uTLS `getSpec()` 的逻辑，当 `ClientHelloID` 有值时，uTLS 会用 ID 查询**内置的真实指纹库**（从真实浏览器流量提取），而非使用 surf 之前 `SetHelloSpec` 的手工参数。

### 改动清单

| 文件 | 改动 |
|------|------|
| `internal/service/proxy.go:128-133` | `impersonate.Chrome()` 后追加 `b.JA().Chrome()` 覆盖 TLS 指纹 |
| `internal/backend/backend.go:25-38` | 默认常量从 Chrome/145 → Chrome/133（UA、Sec-CH-UA、impersonation profile） |
| `internal/service/account.go:78` | `defaultRemoteProfile` 从 `"chrome145"` → `"chrome133"` |
| `internal/service/account.go:2209-2227` | 无指纹回退的默认值从 145 → 133 |
| `internal/service/account.go:2231-2243` | `detectImpersonateFromUA` 回退值 → `"chrome133"` |

### 不需要做的

- **不需要升级 surf 库**：surf v1.0.202 要求 Go 1.27，当前项目 Go 1.26.2
- **不需要修改 JSON 账号文件**：`chrome_major` 版本号不影响 TLS 指纹选择
- **不需要改 uTLS 依赖**：当前 uTLS 内置的 `HelloChrome_Auto` = Chrome 133 已足够

### 关于版本号的认知纠正

Chrome 145 是真实存在的版本（Chrome 版本号迭代很快）。问题不在于 Chrome 145 不存在，而在于 **surf 库手工拼的 `HelloChrome_145` TLS 参数没有完全复现真实 Chrome 145 的 TLS 指纹特征**。Cloudflare 更新的 WAF 检测模型识别到了这个偏差。

---

## 实验记录

### 实验 1：uTLS Chrome 133 覆盖 TLS 指纹 — ❌ 仍然 403

**时间**：2026-07-24

**假设**：uTLS 从真实 Chrome 133 抓包提取的 `HelloChrome_Auto` 指纹替换 surf 手工拼的 `HelloChrome_145` 即可绕过 Cloudflare。

**改动**：`applyBrowserProfile()` 中 `impersonate.Chrome()` 后追加 `b.JA().Chrome()`

**编译**：通过（go build + go test 全部 OK）

**前置验证 — IP 是否被封锁**：

| 测试 | 结果 |
|------|------|
| `curl --socks5-hostname 127.0.0.1:10086 https://chatgpt.com/` | 200 正常 |
| `curl https://chatgpt.com/` | 200 正常 |
| WARP 出口 IP | `107.170.226.114` |
| 服务器本机 IP | `45.15.124.192` |

**结论：两个 IP 都干净，未被 Cloudflare 封锁。**

**部署后结果**：刷新账号仍然 `HTTP 403, upstream returned Cloudflare challenge page`

**middleware 执行顺序分析**：
- surf 的 `JA().build()` 中 `addCliMW(func(*Client) error, math.MaxInt)` → 两个 middleware 同优先级，按插入顺序执行
- Chrome145 middleware order=0 → 先执行，Chrome133 middleware order=1 → 后执行
- Chrome133 的 `c.GetClient().Transport = newRoundTripper(j, c.GetTransport())` 覆盖了 Chrome145 的 roundtripper
- **代码逻辑确认正确，修复已生效**

**失败原因推测**：surf 的 `impersonate.Chrome()` 体内还设置了 H2 SETTINGS（Chrome 145 参数）和 header 构建函数，这些与 uTLS Chrome 133 的 TLS 参数一起使用时可能产生不一致，在 H2 connection preface 阶段被 Cloudflare 检测到。

---

### 后续尝试方向（按优先级）

#### 方向 1：JA().Firefox148() — Firefox 指纹生态系 🔜 待尝试

**原理**：Firefox 使用完全不同的 TLS 指纹、ALPN 行为和 H2 SETTINGS 参数，Cloudflare 对 Chrome 的检测规则可能对 Firefox 不适用。

**操作**：将 `impersonate.Chrome()` 改为 `impersonate.Firefox()`。surf 中 Firefox 148 的 H2 SETTINGS 也是专为 Firefox 设计的，不存在 Chrome 混用问题。

**风险**：Firefox 不支持 Sec-CH-UA headers，HTTP headers 层面可能与账号配置中的 UA 产生不匹配。

---

#### 方向 2：JA().Firefox() — uTLS 真实 Firefox 指纹覆盖 🆕 待尝试

**原理**：与方向 1 类似，但用 uTLS 内置的 `HelloFirefox_Auto` 覆盖 surf 手工的 `HelloChrome_145`，获得真实 Firefox 的 TLS 指纹。同时 surf `impersonate.Chrome()` 设置的 Chrome H2 SETTINGS 不会被 Firefox TLS 覆盖（因为只覆盖了 TLS spec）。

**操作**：在 `impersonate.Chrome()` 之后 `b.JA().Firefox()` 而非 `b.JA().Chrome()`

**注意**：这会产生 TLS=Fox, H2=Chrome 的混合指纹，可能比单一指纹更容易被检测。

---

#### 方向 3：surf JA().Randomized() — 完全随机 TLS 指纹 🆕 待尝试

**原理**：随机化 TLS ClientHello 参数，Cloudflare 的规则匹配通常是针对固定指纹的，随机化可能绕过。

**操作**：在 `impersonate.Chrome()` 之后 `b.JA().Randomized()`

**风险**：Cloudflare 对完全随机的指纹也可能标记为可疑（真实浏览器不会随机化）。

---

#### 方向 4：绕过 surf impersonate 层，直接构建 uTLS 客户端 🆕 待尝试

**原理**：surf 的 `impersonate.Chrome()` 负责设置 TLS spec + H2 SETTINGS + H3 SETTINGS + headers，而我们只覆盖 TLS spec 一项。如果问题出在 H2 SETTINGS 层面，需要彻底绕过 surf 的 impersonate 流程。

**操作**：在 `proxy.go` 中新增一个函数，直接用 uTLS + socks5 transport 构建 HTTP client，不经过 surf 的 `Impersonate().Chrome()` 链路。这能确保从 TLS 到 H2 到 headers 所有层面都干净一致。

**复杂度**：较高，需要直接操作 `utls.Config` 和 `net/http.Transport`

---

#### 方向 5：升级 surf 库至最新 🆕 待尝试

**原理**：surf v1.0.202 重构了 profiles 结构（从手动拼的 `HelloChrome_145` 改为了基于真实流量的 uTLS built-in spec），可能整体修复了 Cloudflare 兼容性问题。

**操作**：升级 `go.mod` 中 surf 版本并升级 Go 版本（surf v1.0.202 要求 Go ≥ 1.27）

**风险**：需要升级 Go，可能引入其他兼容性问题
