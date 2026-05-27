# CPA JSON 导入与账号刷新流程

本文说明本地 CPA JSON 文件导入、远程 CPA 服务器导入，以及导入后为什么会访问 `chatgpt.com` 并可能遇到 Cloudflare 403。

## 核心结论

- CPA JSON 里的 `access_token` 只负责提供 ChatGPT 账号凭证，不等于账号已经可用。
- 导入完成后，系统会调用 `RefreshAccounts(tokens)` 刷新账号信息。（对应前端页面的 ""）
- `RefreshAccounts(tokens)` 会访问 ChatGPT Web 上游，第一步是 `GET https://chatgpt.com/` bootstrap。
- 如果报错包含 `bootstrap failed: HTTP 403, upstream returned Cloudflare challenge page`，说明失败发生在访问 `https://chatgpt.com/` 时，还没有进入 `/backend-api/me` 或 `/backend-api/conversation/init`。
- 远程 CPA 服务器只是 token 来源；ChatGPT 可用性校验仍然由本项目后端直接访问 `chatgpt.com` 完成。

## 本地 CPA JSON 文件导入流程

本地 CPA JSON 文件导入发生在浏览器前端。前端读取用户选择的 JSON 文件，从中提取 `access_token`，然后提交到本项目后端的账号池接口。

```mermaid
flowchart TD
    A[用户在号池管理选择本地 CPA JSON 文件] --> B[前端读取本地 JSON 文件]
    B --> C{是否提取到 access_token}
    C -- 否 --> D[提示没有可用 access_token]
    C -- 是 --> E[POST /api/accounts 提交 tokens]
    E --> F[AccountService.AddAccounts 保存到本地号池]
    F --> G[AccountService.RefreshAccounts 校验并刷新账号信息]
    G --> H[GET https://chatgpt.com/ bootstrap]
    H --> I{Cloudflare / 网络 / 代理是否通过}
    I -- 否 --> J[记录导入刷新失败: bootstrap failed]
    I -- 是 --> K[GET /backend-api/me]
    K --> L[POST /backend-api/conversation/init]
    L --> M[更新邮箱、账号类型、额度、恢复时间、状态]
```

## 远程 CPA 服务器导入流程

远程 CPA 导入由本项目后端访问已配置的 CPA 服务。该服务需要提供管理接口，本项目用 `base_url` 和 `secret_key` 访问它。

```mermaid
flowchart TD
    A[管理员在设置页配置 CPA 连接] --> B[保存 base_url 和 secret_key]
    B --> C[读取远程账号列表]
    C --> D[GET {base_url}/v0/management/auth-files]
    D --> E[用户选择要导入的远程账号文件]
    E --> F[POST /api/cpa/pools/{pool_id}/import]
    F --> G[后端并发下载远程 auth JSON]
    G --> H[GET {base_url}/v0/management/auth-files/download?name=...]
    H --> I{是否取得 access_token}
    I -- 否 --> J[记录 CPA 下载失败]
    I -- 是 --> K[AccountService.AddAccounts 保存到本地号池]
    K --> L[AccountService.RefreshAccounts 校验并刷新账号信息]
    L --> M[GET https://chatgpt.com/ bootstrap]
    M --> N{Cloudflare / 网络 / 代理是否通过}
    N -- 否 --> O[记录导入刷新失败: bootstrap failed]
    N -- 是 --> P[GET /backend-api/me]
    P --> Q[POST /backend-api/conversation/init]
    Q --> R[更新账号信息]
```

## RefreshAccounts 做了什么

`RefreshAccounts(tokens)` 的职责是把刚导入的 token 从“字符串凭证”刷新成“可调度账号”。

它主要做三件事：

1. 验证 `access_token` 是否仍能访问 ChatGPT Web。
2. 拉取账号基础信息，例如邮箱、用户 ID、账号类型。
3. 拉取创作相关状态，例如额度、限流恢复时间、默认模型等。

当前代码里的上游访问顺序是：

```text
GET  https://chatgpt.com/
GET  https://chatgpt.com/backend-api/me
POST https://chatgpt.com/backend-api/conversation/init
```

第一步 `GET https://chatgpt.com/` 是 bootstrap，用于模拟浏览器访问 ChatGPT 首页。如果这一步被 Cloudflare 拦截，就会出现：

```text
bootstrap failed: HTTP 403, upstream returned Cloudflare challenge page; refresh browser fingerprint/session or change proxy
```

## refresh_token 是否必须

`RefreshAccounts(tokens)` 不要求一定有 `refresh_token` 或 `session_token`。

只有 `access_token` 时，系统仍会尝试访问 ChatGPT Web 上游来校验账号并刷新信息。如果 token 已经过期，而且本地没有可用于刷新会话的 `session_token`，系统无法自动换取新的 access token，只能把账号标记为异常或过期待刷新。

简化理解：

```text
access_token: 用于当前请求认证，导入后会立即验证。
session_token: 可选，用于 access_token 过期后的自动刷新。
```

所以，导入 `access_token` 后执行 `RefreshAccounts(tokens)` 的目的确实是检查这些 token 是否还可用，并补齐账号状态信息；它不是单纯保存 token。

## CPA 服务器指的是什么

这里的 CPA 服务器不是本项目内置的服务，也不是本项目默认自动启动的本地服务。它是另一个对外提供 CPA auth 文件管理接口的服务。

本项目只实现了“连接远程 CPA 服务并导入 token”的客户端逻辑。对应配置入口在管理端：

```text
设置 -> CPA 连接管理
```

需要配置：

```text
CPA 地址: base_url
CPA 管理密钥: secret_key
```

远程 CPA 服务需要至少支持这些接口：

```text
GET {base_url}/v0/management/auth-files
GET {base_url}/v0/management/auth-files/download?name=...
```

请求头会带：

```http
Authorization: Bearer <secret_key>
Accept: application/json
```

## 是否必须在同一台机器运行 CPA 服务

不必须。

只要本项目后端能访问到 CPA 服务的 `base_url`，并且 `secret_key` 正确，就可以导入。CPA 服务可以在：

- 同一台机器。
- 同一内网的另一台机器。
- 公网可访问的服务器。
- Docker Compose 的另一个服务容器。

如果本项目运行在 Docker 容器内，需要注意 `localhost` 的含义：

```text
容器内的 localhost 指容器自己，不是宿主机。
```

因此如果 CPA 服务跑在宿主机上，Docker 部署时通常不要写：

```text
http://127.0.0.1:8317
```

而应按实际网络环境使用：

```text
http://host.docker.internal:8317
```

或使用 Compose 服务名：

```text
http://cpa-service:8317
```

如果你把本项目和 CPA 服务都直接运行在同一台宿主机上，非 Docker 场景下可以使用：

```text
http://127.0.0.1:8317
```

## 代理与 Cloudflare 的关系

CPA JSON 下载和 ChatGPT Web 刷新是两类访问：

```text
CPA 下载: 访问你配置的 CPA base_url。
账号刷新: 访问 https://chatgpt.com/ 和 /backend-api/*。
```

遇到 Cloudflare 403 的通常是账号刷新阶段访问 `chatgpt.com`，不是 CPA JSON 文件已经缺 token。

如果服务器所在网络访问 ChatGPT 会触发 Cloudflare challenge，需要配置能正常访问 ChatGPT Web 的代理。全局代理配置项是：

```env
CHATGPT2API_PROXY=socks5h://user:pass@host:port
```

也可以使用：

```env
CHATGPT2API_PROXY=http://host:port
CHATGPT2API_PROXY=https://host:port
CHATGPT2API_PROXY=socks5://host:port
CHATGPT2API_PROXY=socks5h://host:port
```

配置后需要重启服务。管理端也提供代理配置和测试入口：

```text
设置 -> 代理设置
```

代理测试会访问：

```text
https://chatgpt.com/
```

如果代理测试仍返回 Cloudflare challenge，导入后的 `RefreshAccounts(tokens)` 也大概率会失败。

## 问答

### 1. 这里说的 CPA 服务器是指哪个？本地的 CPA 服务吗？

CPA 服务器指外部 CPA auth 文件管理服务，不是本项目默认内置的服务。本项目只是保存 CPA 连接配置，并通过 `base_url` 和 `secret_key` 去访问该服务。

配置位置是：

```text
设置 -> CPA 连接管理
```

也可以从接口角度理解为本项目保存了一组 CPA pool：

```text
base_url
secret_key
name
```

### 2. RefreshAccounts(tokens) 是做什么？是否需要 refresh_token？

`RefreshAccounts(tokens)` 是导入后的账号校验与信息刷新步骤。它会用 `access_token` 访问 ChatGPT Web，确认 token 是否可用，并拉取邮箱、账号类型、额度、恢复时间等信息。

它不强制需要 `refresh_token` 或 `session_token`。但是如果 `access_token` 已经过期，且本地没有 `session_token`，系统无法自动续期，只能把账号标记为异常或过期待刷新。

所以你的理解基本正确：它的目的就是检测当前导入的 token 是否还能用，并补齐账号池调度所需的状态信息。

### 3. 如果从远程 CPA 服务器导入，是否只需要在相同机器上再运行 CPA 服务，然后导入 base_url 和 apikey？

本质上是这样，但不要求必须是同一台机器。

你需要准备一个 CPA 服务，并确保本项目后端能访问它的 `base_url`，同时管理密钥正确。然后在管理端配置：

```text
CPA 地址: http://your-cpa-host:8317
CPA 管理密钥: <secret_key>
```

之后本项目会先从 CPA 服务拉取 auth 文件列表，再下载选中的 JSON，提取 `access_token`，保存到本地号池，并执行 `RefreshAccounts(tokens)`。

需要注意：即使 CPA 服务和本项目在同一台机器上，最终刷新账号仍然需要本项目后端访问 `https://chatgpt.com/`。因此 CPA 服务可访问只解决 token 来源问题，不解决 Cloudflare 或代理问题。
