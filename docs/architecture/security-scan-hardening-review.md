# 公网扫描日志复盘与本轮安全加固

## 背景

服务器日志中出现了来自同一公网 IP 的集中路径探测，例如：

- `/api/config.js`
- `/api/credentials`
- `/api/phpinfo.php`
- `/api/v1/workflows`
- `/auth/*`
- `/v1/*`

这些路径不是本项目真实业务入口，更像自动化扫描器在批量探测常见泄露点、Node/PHP 配置文件、n8n/工作流接口和通用 API 凭据接口。日志中这些请求均返回 `404`，当前没有看到成功命中敏感接口或直接数据泄露的迹象。

## 本次判断

这是典型公网“打野”扫描。它不代表服务已经被攻破，但说明服务入口已经能被公网扫描器访问到。

对本项目来说，主要风险不是扫描器命中这些不存在的路径，而是后续攻击面：

- 对 `/auth/login` 做密码爆破或撞库。
- 拿到管理员会话后导出号池 `access_token`。
- 若 Nginx 配错静态目录，误暴露 `.env`、`data/chatgpt2api.db`、`data/auto_import`。
- 如果服务器本身被入侵，数据库中明文保存的账号 token 可被直接读取。

## 已确认的现有防护

- `/api/*`、`/v1/*`、私有图片访问路径已经有身份校验。
- 普通用户 API 权限走 RBAC，非管理员默认不能访问号池 token 导出。
- `linuxdo_client_secret`、`update_github_token` 在设置接口返回时已隐藏。
- CPA、Sub2API 配置列表返回时会删除 `secret_key`、`password`、`api_key`。
- 日志已有字段级脱敏逻辑，常见 `password`、`access_token`、`session_token` 等字段不会完整输出。

## 本轮代码加固

### 1. 登录失败限流

文件：

- `internal/httpapi/app.go`
- `internal/httpapi/login_rate_limiter.go`
- `internal/httpapi/app_test.go`

改动：

- 为 `/auth/login` 增加内存级失败登录限流。
- 同一来源 IP 在 15 分钟内失败 8 次后返回 `429 Too Many Requests`。
- 登录成功后清除该来源 IP 的失败计数。

目的：

- 抑制公网撞库和弱密码爆破。
- 实现保持简单，不引入数据库表或复杂状态，符合 KISS/YAGNI。

限制：

- 这是进程内限流，服务重启后计数会清空。
- 多实例部署时各实例独立计数。
- Nginx 侧仍应保留 `limit_req`，应用层限流作为第二道防线。

### 2. 修正来源 IP 识别

文件：

- `internal/httpapi/audit.go`
- `internal/httpapi/audit_test.go`

改动：

- `clientIP` 不再无条件信任 `X-Forwarded-For` / `X-Real-IP`。
- 只有请求来源是本机或私网反代时，才信任转发头。
- 公网直连请求会使用真实 `RemoteAddr`。

目的：

- 避免攻击者伪造 `X-Forwarded-For` 污染日志。
- 避免登录限流被伪造请求头绕过。

Nginx 反代部署下的预期：

- Nginx 与 Go 服务在同机时，Go 看到的 `RemoteAddr` 是 `127.0.0.1`，因此会信任 Nginx 设置的 `X-Forwarded-For`。
- 如果 Go 服务被公网直接访问，则不会信任客户端自己传的转发头。

### 3. 加强日志脱敏

文件：

- `internal/service/log.go`
- `internal/service/log_test.go`

改动：

- `LogService.Add` 写入前统一对 `detail` 做脱敏。
- 除字段名脱敏外，新增整段文本脱敏：
  - `Bearer <token>`
  - `access_token=...`
  - `refresh_token=...`
  - `session_token=...`
  - `api_key=...`
  - `authorization=...`

目的：

- 防止上游错误信息或字符串化响应中夹带 token。
- 保留可排障上下文，同时减少日志泄露凭据的可能。

## 验证结果

已通过：

```bash
go test ./internal/httpapi
go test ./internal/service -run "TestSanitizeLogValue|TestLogServiceStoresLogsInDatabase"
```

未作为本轮结果判断的命令：

```bash
go test ./internal/service ./internal/httpapi
```

该命令当前会被工作区已有的 `internal/service/account.go`、`internal/service/account_test.go` 账号刷新相关改动影响，失败点不属于本轮安全加固新增逻辑。

## Nginx 反代部署核对清单

你已经有 Nginx 反代，建议确认以下几点。

### 1. Go 服务只监听本机入口

Systemd 裸机部署时，建议让 Nginx 反代到：

```nginx
proxy_pass http://127.0.0.1:8822;
```

同时用防火墙阻止公网访问 `8822`。

Docker 部署时，不建议：

```yaml
ports:
  - "3000:80"
```

建议改为只绑定本机：

```yaml
ports:
  - "127.0.0.1:3000:80"
```

然后 Nginx 反代到：

```nginx
proxy_pass http://127.0.0.1:3000;
```

### 2. 保留真实客户端 IP

Nginx 反代应设置：

```nginx
proxy_set_header Host $host;
proxy_set_header X-Real-IP $remote_addr;
proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
proxy_set_header X-Forwarded-Proto $scheme;
```

这样应用日志和登录限流会基于真实公网客户端 IP，而不是统一显示 `127.0.0.1`。

### 3. 登录接口限流

建议在 Nginx `http` 块中定义：

```nginx
limit_req_zone $binary_remote_addr zone=chatgpt2api_login:10m rate=3r/m;
```

在 `/auth/login` 中启用：

```nginx
location = /auth/login {
    limit_req zone=chatgpt2api_login burst=5 nodelay;

    proxy_pass http://127.0.0.1:8822;
    proxy_http_version 1.1;
    proxy_buffering off;
    proxy_cache off;
    proxy_set_header Host $host;
    proxy_set_header X-Real-IP $remote_addr;
    proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
    proxy_set_header X-Forwarded-Proto $scheme;
    proxy_read_timeout 600s;
}
```

如果你的管理后台只给自己用，可以在这里加 IP 白名单。

### 4. 管理入口白名单

优先保护这些入口：

- `/auth/login`
- `/api/admin/`
- `/api/accounts`
- `/api/settings`
- `/api/logs`
- `/api/storage/info`
- `/api/proxy`

示例：

```nginx
location ^~ /api/admin/ {
    allow 你的固定公网IP;
    deny all;

    proxy_pass http://127.0.0.1:8822;
    proxy_http_version 1.1;
    proxy_buffering off;
    proxy_cache off;
    proxy_set_header Host $host;
    proxy_set_header X-Real-IP $remote_addr;
    proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
    proxy_set_header X-Forwarded-Proto $scheme;
    proxy_read_timeout 600s;
}
```

如果公网 IP 不固定，至少保留 Nginx 限流，不要只依赖应用层限流。

### 5. 禁止静态泄露敏感文件

确保 Nginx 没有把项目根目录当静态目录直接暴露。建议显式拒绝：

```nginx
location ~ /\. {
    deny all;
    return 404;
}

location ^~ /data/ {
    deny all;
    return 404;
}

location ~* (\.env|\.git|chatgpt2api\.db|docker-compose|Dockerfile|Makefile|\.sql)$ {
    deny all;
    return 404;
}
```

### 6. SSE 和长任务设置

项目有流式响应和生图任务，反代中应保留：

```nginx
proxy_buffering off;
proxy_cache off;
chunked_transfer_encoding on;
proxy_read_timeout 600s;
proxy_send_timeout 600s;
client_max_body_size 50m;
```

## 服务器侧建议动作

### P0：立即确认

- 管理员密码不是弱密码，且已单独设置 `CHATGPT2API_ADMIN_PASSWORD`。
- 关闭不需要的注册入口：`CHATGPT2API_REGISTRATION_ENABLED=false`。
- 防火墙只开放 `80/443`，不要开放 Go 服务端口或 Docker 映射端口。
- `.env`、`data/`、数据库文件不在 Nginx 静态根目录下。
- Nginx access log 中 `/auth/login` 没有持续失败请求。

### P1：建议近期做

- 配置 Nginx 登录限流。
- 给管理入口加 IP 白名单或额外访问控制。
- 配置 fail2ban 读取 Nginx access log，对 `/auth/login` 失败进行封禁。
- 定期轮换管理员密码和用户 API Key。

### P2：后续代码级改进

- 为账号池 `access_token`、`session_token` 做静态加密存储。
- 增加可配置的登录限流参数，而不是固定在代码里。
- 增加管理员安全面板，展示近期失败登录、扫描 IP、被限流次数。
- 对 `/health`、`/version` 是否公网可见做配置化控制。

## 当前最大剩余风险

账号池 token 当前仍是明文存储在数据库 JSON 中。只要攻击者拿到服务器文件、数据库备份或容器挂载目录，就可以直接读取 `access_token` / `session_token`。

因此，Nginx 防扫描和登录限流只能保护 Web 入口；服务器系统安全、数据目录权限、备份文件保护仍然同等重要。下一轮安全改进建议优先做 token 静态加密。

