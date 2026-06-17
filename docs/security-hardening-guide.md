# 安全加固指南

> 部署前必读。花 30 分钟做完 P0 措施，可挡住 90% 以上的自动化攻击。

---

## 攻击者如何发现和攻击你的服务

### 第一步：全网扫描

攻击者用 **Shodan / Censys / ZoomEye / Fofa** 持续扫描整个 IPv4 空间。服务暴露到公网后，几分钟到几小时内就会被收录。

筛选条件包括：
- 特定端口的 HTTP/HTTPS 服务
- 响应头和页面特征（关键词、框架指纹）
- SSL 证书中的域名信息
- API 返回的 JSON 字段（`"error"`、`"access_token"` 等）

### 第二步：指纹识别与漏洞探测

一旦发现服务 IP，攻击者用自动化工具（nuclei、httpx）进行：
- **路径探测**：`/.env`、`/api/admin/system`、`/health`、`/v1/chat/completions`、`/data/chatgpt2api.db`
- **版本识别**：从 JS bundle 路径、响应头反推框架和版本
- **已知漏洞匹配**：如果命中了某个 CVE，直接上 exploit

### 第三步：凭证攻击

- **暴力破解**：对 `/auth/login` 跑常用用户名/密码组合
- **API Key 猜测**：`sk-` + 24 位随机字符，`64^24` 搜索空间理论上安全，但弱密码开门后就不需要猜 API Key
- **Session Cookie 窃取**：中间人攻击（如果没有 HTTPS）

### 第四步：资源盗用

攻击者进来后的目标：
- **白嫖 ChatGPT 额度** — 用你的 API key 免费调用
- **偷取账号 token** — 数据库中存储的 access_token 可提取变现
- **数据窃取** — 对话记录、用户信息

---

## 这个项目特定的风险分析

### 真正的风险：API 凭证被盗用（不是服务器入侵）

你的 Go 服务没有命令注入、反序列化漏洞、SSRF 等常见的 RCE（远程代码执行）入口。攻击者**不需要控制服务器**就能造成损失——只需要拿到有效的 API key，就能"合法"地使用你的服务，消耗你账号池里的 ChatGPT 额度。

### 风险点清单

| 风险 | 严重度 | 详情 |
|------|--------|------|
| 登录接口无频率限制 | **高** | `/auth/login` 可被无限暴力破解 |
| 无 fail2ban / 账户锁定 | **高** | 失败登录不跟踪、不封禁 |
| 无 IP 白名单 | 中 | 管理接口对所有 IP 开放 |
| SSH 弱密码 | **高** | 这是真正入���服务器的入口 |
| 数据库文件可能被下载 | **高** | 如果 Nginx 直接 serve 了项目目录 |
| 敏感文件暴露 | 中 | `.env`、`.git` 等被访问 |
| 无安全响应头 | 低 | 缺 HSTS、X-Content-Type-Options 等 |
| 注册开关未关闭 | 中 | 如开���注册，任何人都能创建账号 |

---

## P0 — 部署前必须做（无需改代码）

### 1. SSH 加固

```bash
# 编辑 /etc/ssh/sshd_config
# 禁用密码登录，只用密钥
PasswordAuthentication no
PubkeyAuthentication yes

# 改掉默认端口（可选，但有效过滤低级扫描）
Port 22022  # 或其他非标准端口

# 禁止 root 登录
PermitRootLogin no

# 重启 SSH
systemctl restart sshd
```

**验证**：新开一个终端窗口，确认可以用密钥登录后再关掉当前 session。

### 2. Nginx 配置（安全版）

```nginx
server {
    listen 443 ssl http2;
    server_name your-domain.com;

    # ---- SSL 证书（强烈建议配置） ----
    # ssl_certificate     /etc/letsencrypt/live/your-domain.com/fullchain.pem;
    # ssl_certificate_key /etc/letsencrypt/live/your-domain.com/privkey.pem;

    # ---- 安全响应头 ----
    add_header X-Content-Type-Options "nosniff" always;
    add_header X-Frame-Options "SAMEORIGIN" always;
    add_header X-XSS-Protection "1; mode=block" always;
    add_header Referrer-Policy "strict-origin-when-cross-origin" always;
    # 仅 HTTPS 时启用 HSTS
    # add_header Strict-Transport-Security "max-age=31536000; includeSubDomains" always;

    # ---- 不泄露服务版本 ----
    server_tokens off;

    # ---- 上传和请求体大小限制 ----
    client_max_body_size 50m;

    # ---- 明确拒绝敏感文件和目录 ----
    location ~ /\. {
        deny all;
        return 404;
    }

    location /data {
        deny all;
        return 404;
    }

    location ~* (\.env|\.git|Dockerfile|docker-compose|chatgpt2api\.db|Makefile|\.sql)$ {
        deny all;
        return 404;
    }

    # ---- IP 白名单：保护管理入口和登录 ----
    # 只允许你的 IP 访问。改掉下面的 IP！
    location /auth/login {
        allow 1.2.3.4;   # 替换为你的家庭/办公 IP
        allow 5.6.7.8;   # 备用 IP
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

    location /api/admin/ {
        allow 1.2.3.4;
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

    # ---- 频率限制：防止暴力破解 ----
    limit_req_zone $binary_remote_addr zone=login_limit:10m rate=3r/m;

    location /auth/login {
        limit_req zone=login_limit burst=3 nodelay;
        # ... 与上面的 /auth/login IP 白名单合并使用
    }

    # ---- 其余所有请求走 Go 后端 ----
    location / {
        proxy_pass http://127.0.0.1:8822;
        proxy_http_version 1.1;

        # 必须关闭 buffering（SSE 流式响应）
        proxy_buffering off;
        proxy_cache off;
        chunked_transfer_encoding on;

        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;

        # 长连接超时（生图默认 300s）
        proxy_read_timeout 600s;
        proxy_send_timeout 600s;
    }
}

# ---- HTTP → HTTPS 重定向 ----
server {
    listen 80;
    server_name your-domain.com;
    return 301 https://$host$request_uri;
}
```

### 3. fail2ban 防爆破

```bash
# 安装
apt install fail2ban -y
```

创建 `/etc/fail2ban/filter.d/chatgpt2api-login.conf`：

```ini
[Definition]
failregex = ^<HOST>.*"POST /auth/login HTTP.*" 401
ignoreregex =
```

创建 `/etc/fail2ban/jail.local`：

```ini
[chatgpt2api-login]
enabled  = true
filter   = chatgpt2api-login
port     = http,https
logpath  = /var/log/nginx/access.log
maxretry = 5
findtime = 300
bantime  = 3600
```

```bash
# 启动
systemctl enable --now fail2ban

# 查看状态
fail2ban-client status chatgpt2api-login
```

**效果**：同一 IP 5 分钟内 5 次登录失败 → 封禁 1 小时。

### 4. 关闭注册（如果用户已手动创建完成）

```bash
# .env 或环境变量
CHATGPT2API_REGISTRATION_ENABLED=false
```

### 5. 开启防火墙，只开放必要端口

```bash
# ufw（Ubuntu/Debian）
ufw default deny incoming
ufw default allow outgoing
ufw allow 22022/tcp   # SSH（如果改了端口）
ufw allow 80/tcp      # HTTP
ufw allow 443/tcp     # HTTPS
ufw enable

# 确认
ufw status verbose
```

**服务器上只需要开放 3 个端口**：SSH（建议改端口）、HTTP（80）、HTTPS（443）。Go 服务的 8822 端口**不要**对外开放——只有 Nginx 通过 `127.0.0.1:8822` 访问它。

### 6. Go 服务只监听本地

确保 Go 服务绑定在 `127.0.0.1` 而不是 `0.0.0.0`：

```bash
# 如果通过 PORT 环境变量控制
# 确保 Nginx proxy_pass 指向 127.0.0.1:8822
# Go 服务监听 127.0.0.1:8822 即可

# Docker 场景：ports 只映射到宿主机 127.0.0.1
# docker-compose.yml:
#   ports:
#     - "127.0.0.1:8822:80"
```

---

## P1 — 强烈建议做

### 7. 登录失败延迟（Go 代码小改动）

在登录失败处理中加入 1-2 秒延迟，让自动爆破几乎不可行。改 `internal/httpapi/app.go` 中登录 handler 对应的错误返回前加一句：

```go
time.Sleep(2 * time.Second)
```

### 8. 定期更新系统和依赖

```bash
# 系统更新
apt update && apt upgrade -y

# Go 依赖更新
go get -u ./...
go mod tidy

# 重建 Docker 镜像（如果用 Docker）
docker build --no-cache -t chatgpt2api:latest .
```

### 9. 导入账号前检查 token 有效性

如果从不可信来源导入 CPA JSON，注意 JSON 可能含有恶意 payload（虽然 Go 的 `json.Unmarshal` 到 `map[string]any` 是安全的）。如果导入的文件里有可执行脚本或二进制字段，不要执行。

---

## P2 — 代码层可选加固

### 10. 管理接口增加二次认证考虑

对于生产环境的多用户服务，管理面板可以加入简单的二次认证（如 TOTP），但这需要一定的开发量。

### 11. API Key 存储改为 bcrypt

当前 API key 的 hash 使用的是 SHA-256（无盐）。改为 bcrypt 会更安全，但需要评估对认证性能的影响——当前用的是内存查找 + constant-time comparison，改为 bcrypt 后每次认证都需要做一次 bcrypt.Compare，会略微增加延迟。

---

## 部署后验证清单

部署完成后，从**外部网络**（不要从服务器上 curl 127.0.0.1）执行以下验证：

```bash
DOMAIN="https://your-domain.com"

# 1. 数据库文件不可访问
curl -I "$DOMAIN/data/chatgpt2api.db"
# 期望: 404

# 2. 环境变量文件不可访问
curl -I "$DOMAIN/.env"
# 期望: 404

# 3. Git 目录不可访问
curl -I "$DOMAIN/.git/config"
# 期望: 404

# 4. data 目录不可列
curl -I "$DOMAIN/data/"
# 期望: 404

# 5. SSH 端口变了，默认端口不开
nc -zv your-server-ip 22
# 期望: Connection refused（如果改了 SSH 端口）

# 6. 新 SSH 端口可达
nc -zv your-server-ip 22022
# 期望: Connection succeeded

# 7. Go 服务 8822 端口不对外开放
nc -zv your-server-ip 8822
# 期望: Connection refused

# 8. 查看 fail2ban 状态
fail2ban-client status chatgpt2api-login

# 9. 确认只对外开放了必要端口
nmap -p- your-server-ip --open
# 期望只有: 80, 443, 22022（SSH端口）
```

**判断标准**：只有正常的 API 响应和图片 `/images/` 路径返回有效内容，其他所有路径返回 404。

---

## 快速检查清单

| # | 措施 | 状态 |
|---|------|------|
| 1 | SSH 禁用密码登录，只用密钥 | ☐ |
| 2 | SSH 端口已更改 | ☐ |
| 3 | 防火墙只开放 80/443/SSH | ☐ |
| 4 | Nginx 拒绝 `.env`、`.git`、`/data` 访问 | ☐ |
| 5 | Nginx 加了安全响应头 | ☐ |
| 6 | 管理面板 / 登录接口 IP 白名单生效 | ☐ |
| 7 | fail2ban 运行中且检测登录失败 | ☐ |
| 8 | 注册已关闭（如不需要） | ☐ |
| 9 | Go 服务只监听 127.0.0.1 | ☐ |
| 10 | HTTPS 已配置（如使用域名） | ☐ |
| 11 | curl 敏感路径均返回 404 | ☐ |

---

## 常见问题

### Q: 我不用域名，直接用 IP 部署行不行？

不推荐。没有域名就没有 HTTPS（Let's Encrypt 需要域名），所有流量明文传输，包括你的登录密码和 API key。如果暂时没有域名，至少配置自签名证书 + HSTS，但最佳实践是买一个廉价域名（几美元/年）。

### Q: WARP 能替代 fail2ban 吗？

不能。WARP 解决的是出口 IP（你的服务器请求 ChatGPT 时用的 IP），fail2ban 解决的是入口安全（谁在访问你的服务）。两个是不同方向的防护。

### Q: 如果我的家庭 IP 是动态的怎么配白名单？

两个方案：
1. **动态 DNS** — 家里跑个 ddclient 更新域名，Nginx 里用 `allow` + `set` 配合 `ngx_http_geo_module` 做动态解析（稍微复杂）
2. **VPN 方案** — 在服务器上搭 WireGuard，管理面板只允许 WireGuard 内网 IP 访问。配好后你连上 VPN 再访问管理页。这是最安全的方案。
3. **Tailscale / ZeroTier** — 服务器和你本地都装 Tailscale（免费），管理面板只允许 Tailscale IP（`100.x.x.x`）。零配置，最简单。

推荐方案 3（Tailscale），免费且几乎零配置。

### Q: Docker 部署和裸机部署在安全上有什么区别？

Docker 多一层隔离，但 `ports: - "8822:80"` 默认绑定 `0.0.0.0`，记得改为：

```yaml
ports:
  - "127.0.0.1:8822:80"
```

否则 Go 容器内的 80 端口会直接暴露到公网，绕过 Nginx。
