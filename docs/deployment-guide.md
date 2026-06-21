# 服务器部署指南

## 部署前准备

### 必须配置的环境变量

| 变量 | 说明 | 示例 |
|------|------|------|
| `CHATGPT2API_BASE_URL` | 部署域名，图片 URL 生成依赖此值 | `https://your-domain.com` |
| `CHATGPT2API_ADMIN_PASSWORD` | 管理员密码，不设则启动时自动生成并打印到日志 | `your-password` |
| `PORT` | 监听端口，Docker 内默认 80，裸机默认 8822 | `8822` |

### 存储后端选择

默认使用 SQLite（`data/chatgpt2api.db`）。**多用户场景强烈建议使用 PostgreSQL**。

### PostgreSQL 安装与配置（Ubuntu）

```bash
# 1. 安装
sudo apt update && sudo apt install postgresql postgresql-client -y

# 2. 启动 PostgreSQL
sudo systemctl start postgresql
sudo systemctl enable postgresql

# 3. 创建数据库和用户
sudo -u postgres psql <<'SQL'
CREATE USER chatgpt2api WITH PASSWORD 'your-strong-password';
CREATE DATABASE chatgpt2api OWNER chatgpt2api;
GRANT ALL PRIVILEGES ON DATABASE chatgpt2api TO chatgpt2api;
\q
SQL
```

> 建议使用 `127.0.0.1` 而非 `localhost` 连接，避免 Unix socket 认证问题。

安装完成后确认 PG 实例的实际服务名（不同版本名称不同）：

```bash
sudo systemctl list-units --all | grep postgres
# 输出示例：postgresql@16-main.service、postgresql@12-main.service 等
```

记下运行中的实例名，后续 `chatgpt2api.service` 的 `After=` 需要改成该名称，确保 systemd 按正确顺序启动。

然后在 `.env` 中配置：

```
STORAGE_BACKEND=postgres
DATABASE_URL=postgresql://chatgpt2api:your-strong-password@127.0.0.1:5432/chatgpt2api
```

项目启动时会自动建表，无需手动导入 schema。

### 首次从 SQLite 迁移到 PostgreSQL

如果已有 SQLite 数据，启动 PostgreSQL 模式后，将账号 JSON 文件放入 `data/auto_import/` 目录重新导入即可：

```bash
curl -X POST http://localhost:8822/api/accounts/import-scan
```

### 使用 MySQL

```
STORAGE_BACKEND=mysql
DATABASE_URL=mysql://user:password@host:3306/chatgpt2api
```

### 数据持久化

以下目录需要持久化挂载：

| 路径 | 内容 |
|------|------|
| `/app/data` | 账号、用户、日志、图片等全部业务数据 |
| `/app/.env` | 配置文件 |

图片存储子目录：
- `data/images/` — 生成的图片原文件
- `data/image_thumbnails/` — 缩略图
- `data/image_metadata/` — 图片元数据

### 账号自动导入

将账号 JSON 文件放入 `data/auto_import/` 目录，服务启动时会自动扫描导入。也可以通过 `CHATGPT2API_IMPORT_DIR` 指定其他目录。

---

## Docker 部署

项目已提供完整的 Docker 构建文件和 compose 配置。

### 使用 docker-compose（推荐）

```bash
# 1. 准备 .env 配置文件
cp .env.example .env
# 编辑 .env 填入必要配置

# 2. 启动
cd deploy
docker compose up -d
```

`docker-compose.yml` 默认映射端口 `3000:80`，挂载 `data` 目录和 `.env` 文件。

### 从源码构建镜像

```bash
# 标准构建
docker build -f deploy/Dockerfile -t chatgpt2api:latest .

# 资源受限服务器（低配 VPS）
bash deploy/docker-build-limited.sh
```

### 使用预构建镜像

```bash
docker pull zyphrzero/chatgpt2api:latest
```

---

## 裸机部署

### 前置依赖安装

```bash
# 安装 Go（编译后端必需）
# 方法一：使用 apt 安装（版本可能较旧，不推荐）
sudo apt update && sudo apt install golang-go -y

# 方法二：从官网安装最新版（推荐）
GO_VERSION=1.22.4
wget https://go.dev/dl/go${GO_VERSION}.linux-amd64.tar.gz
sudo rm -rf /usr/local/go && sudo tar -C /usr/local -xzf go${GO_VERSION}.linux-amd64.tar.gz
rm go${GO_VERSION}.linux-amd64.tar.gz
```

**bash 用户** —— 将 Go 加入 PATH：

```bash
echo 'export PATH=/usr/local/go/bin:$PATH' >> ~/.bashrc
source ~/.bashrc
```

**zsh 用户** —— 将 Go 加入 PATH：

```zsh
echo 'export PATH=/usr/local/go/bin:$PATH' >> ~/.zshrc
source ~/.zshrc
```

安装 Bun（JavaScript 运行时与包管理器，构建前端必需）：

```bash
# bash / zsh 通用 —— install 脚本固定用 bash 执行
curl -fsSL https://bun.sh/install | bash
```

**bash 用户** —— 重新加载：

```bash
source ~/.bashrc
```

**zsh 用户** —— 重新加载：

```zsh
source ~/.zshrc
```

> 验证安装：`go version`（需 ≥ 1.21）、`bun --version`

### 构建

```bash
# 1. 构建前端
cd web && bun install && bun run build

# 2. 构建后端（前端已嵌入）
cd .. && go build -o chatgpt2api ./internal

# 3.1本地
./chatgpt2api

# 3.2 服务器见 #### 4. 更新部署
```

### systemd 服务配置

将应用注册为系统服务，开机自启并通过 systemctl 管理。

#### 1. 创建专用用户和目录

```bash
# 创建低权限系统用户（不能登录 shell，home 目录指向应用目录）
sudo useradd -r -s /usr/sbin/nologin -d /opt/chatgpt2api -M chatgpt2api

# 创建应用目录并部署文件
sudo mkdir -p /opt/chatgpt2api/data
sudo cp chatgpt2api /opt/chatgpt2api/
sudo cp .env /opt/chatgpt2api/

# 赋权
sudo chown -R chatgpt2api:chatgpt2api /opt/chatgpt2api
sudo chmod 640 /opt/chatgpt2api/.env
```

#### 2. 注册 systemd 服务

项目中已提供 `deploy/chatgpt2api.service`。如果使用 PostgreSQL，需要先修改 `After=` 中的实例名：

```bash
# 查看 PG 实例的实际服务名
sudo systemctl list-units --all | grep postgres

# 编辑服务文件，将 After= 那行的 postgresql@版本-main.service 改为实际名称
# 例如：After=network.target postgresql@16-main.service
```

然后注册服务：

```bash
# 复制服务文件
sudo cp deploy/chatgpt2api.service /etc/systemd/system/

# 重新加载 systemd 配置
sudo systemctl daemon-reload

# 启动服务
sudo systemctl start chatgpt2api

# 设置开机自启
sudo systemctl enable chatgpt2api

# 查看状态
sudo systemctl status chatgpt2api
```

#### 3. 日常管理命令

```bash
# 查看实时日志
sudo journalctl -u chatgpt2api -f

# 查看最近 100 行日志
sudo journalctl -u chatgpt2api -n 100

# 查看今天的日志
sudo journalctl -u chatgpt2api --since today

# 重启服务（如更新 .env 后）
sudo systemctl restart chatgpt2api

# 停止服务
sudo systemctl stop chatgpt2api

# 重载配置（不中断服务）
sudo systemctl reload chatgpt2api
```

#### 4. 更新部署

在服务器上拉取最新代码后，使用一键脚本完成构建 + 部署：

```bash
# 首次使用前，给脚本加上执行权限
chmod +x deploy/update.sh

# 拉取最新代码
git pull

# 方案 A：代码有变更 —— 重新构建前端 + 编译后端 + 更新部署
sudo ./deploy/update.sh

# 方案 B：仅改动了 .env —— 直接更新配置并重启，跳过构建编译（省时）
sudo ./deploy/update.sh --env
```

> 服务文件中的 `ProtectSystem=strict` 和 `NoNewPrivileges=yes` 提供了基础沙箱隔离，进一步提升了运行安全性。

---

## Nginx 反向代理配置

### 创建 Nginx 配置文件

在 Ubuntu/Debian 上，nginx 站点配置文件的规范路径是：

```
/etc/nginx/sites-available/chatgpt2api   # 存放配置文件
/etc/nginx/sites-enabled/chatgpt2api     # 启用站点（软链接）
```

创建步骤：

```bash
# 1. 创建配置文件
sudo nano /etc/nginx/sites-available/chatgpt2api

# 2. 将下方配置粘贴进去，替换域名和 IP
# 3. 启用站点（创建软链接到 sites-enabled）
sudo ln -sf /etc/nginx/sites-available/chatgpt2api /etc/nginx/sites-enabled/

# 4. 测试配置语法
sudo nginx -t

# 5. 重新加载 nginx
sudo systemctl reload nginx
```

> 配置文件也可以放 `/etc/nginx/conf.d/chatgpt2api.conf`（CentOS/RHEL 惯例），效果相同。

---

### 第一步：HTTP 配置（初始部署）

先用 HTTP 跑通，后面用 certbot 一键添加 SSL。

此配置整合了[安全加固指南](security-hardening-guide.md)的核心措施：敏感文件拦截、IP 白名单、频率限制、安全响应头等。

```nginx
# /etc/nginx/sites-available/chatgpt2api

# ---- 频率限制：登录防爆破 ----
limit_req_zone $binary_remote_addr zone=login_limit:10m rate=3r/m;

server {
    listen 80;
    server_name your-domain.com;  # ← 替换为你的域名

    # ---- 不泄露 nginx 版本 ----
    server_tokens off;

    # ---- 安全响应头 ----
    add_header X-Content-Type-Options "nosniff" always;
    add_header X-Frame-Options "SAMEORIGIN" always;
    add_header X-XSS-Protection "1; mode=block" always;
    add_header Referrer-Policy "strict-origin-when-cross-origin" always;

    # ---- 上传和请求体大小限制 ----
    client_max_body_size 50m;

    # ==============================================================
    # 安全拦截：拒绝访问敏感文件和目录
    # ==============================================================

    # 拒绝所有 .开头的隐藏文件/目录
    location ~ /\. {
        deny all;
        return 404;
    }

    # 拒绝 data 目录（含 SQLite 数据库）
    location /data {
        deny all;
        return 404;
    }

    # 拒绝项目敏感文件
    location ~* (\.env|\.git|Dockerfile|docker-compose|chatgpt2api\.db|Makefile|\.sql|chatgpt2api)$ {
        deny all;
        return 404;
    }

    # ==============================================================
    # 重要前提：图片鉴权
    # 项目中的图片访问有权限校验（authorizeImageFileRequest），区分 public 和 private。
    # 如果让 nginx 直接 serve 图片目录，会绕过鉴权。
    #
    # 方案 A（默认）：走 Go 后端，保留鉴权 —— 下方 location / 统一处理
    # 方案 B（仅全 public 场景）：nginx 直接 serve 图片 —— 见下方"性能优化配置"注释块
    #
    # 本配置默认采用方案 A。如需方案 B，取消下面图片 location 块的注释，
    # 并将 location / 的 proxy_pass 改为只代理非图片路径（见注释提示）。
    # ==============================================================

    # ---- 方案 B：nginx 直接 serve 图片（全 public 场景，取消注释启用） ----
    # location /images/ {
    #     alias /app/data/images/;
    #     expires 7d;
    #     add_header Cache-Control "public, max-age=604800";
    #     add_header Access-Control-Allow-Origin "*";
    # }
    # location /image-thumbnails/ {
    #     alias /app/data/image_thumbnails/;
    #     expires 1y;
    #     add_header Cache-Control "public, max-age=31536000, immutable";
    #     add_header Access-Control-Allow-Origin "*";
    # }

    # ==============================================================
    # 管理入口 IP 白名单
    # ==============================================================

    # 登录接口：频率限制 + IP 白名单
    location /auth/login {

        limit_req zone=login_limit burst=5 nodelay;

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

    # 管理 API：IP 白名单
    location /api/admin/ {
        allow 1.2.3.4;    # ← 替换为你的 IP
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

    # ==============================================================
    # 其余所有请求走 Go 后端
    # ==============================================================
    location / {
        proxy_pass http://127.0.0.1:8822;
        proxy_http_version 1.1;

        # SSE 流式响应 —— 必须关闭 buffering
        proxy_buffering off;
        proxy_cache off;
        chunked_transfer_encoding on;

        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;

        proxy_read_timeout 600s;
        proxy_send_timeout 600s;
    }
}
```

### 第二步：certbot 申请 SSL 证书

HTTP 跑通后，用 certbot 一键添加 HTTPS，它会**自动修改**当前 nginx 配置：

```bash
# 安装 certbot
sudo apt install certbot python3-certbot-nginx -y

# 申请证书并自动配置 SSL（certbot 会自动改写 nginx 配置）
sudo certbot --nginx -d your-domain.com
```

certbot 执行完毕后会自动：
- 申请 SSL 证书
- 在配置中追加 `listen 443 ssl http2;` 和证书路径
- 添加 HTTP → HTTPS 的 301 跳转
- 添加 HSTS 等安全响应头

> 无需手动填写 SSL 相关配置。certbot 处理完后，用 `sudo nginx -t` 验证，`sudo systemctl reload nginx` 重载即可。

### 关键配置说明

| 配置项 | 重要性 | 说明 |
|--------|--------|------|
| `proxy_buffering off` | **必须** | `/v1/chat/completions` 和生图接口使用 SSE 流式输出，nginx 缓冲会导致客户端收不到实时数据 |
| `proxy_read_timeout` | **必须** | 生图默认 300s 超时（可配置 `CHATGPT2API_IMAGE_TASK_TIMEOUT_SECONDS`），nginx 超时必须大于该值 |
| `client_max_body_size` | 建议 | 生图编辑（`/v1/images/edits`）会上传参考图，需要足够大 |
| `limit_req_zone` | 建议 | 登录接口 3 次/分钟，防暴力破解 |
| IP 白名单 | 建议 | `/auth/login` 和 `/api/admin/` 仅允许你的 IP 访问 |
| `server_tokens off` | 建议 | 隐藏 nginx 版本号，增加攻击者指纹识别难度 |

> HTTPS 配置见上方的 certbot 步骤。更多安全加固措施（fail2ban、防火墙、SSH 加固等）详见 [安全加固指南](security-hardening-guide.md)。

---

## 健康检查

服务提供 `/health` 端点，可用于负载均衡或监控探活：

```nginx
# 在 nginx upstream 中使用
upstream chatgpt2api {
    server 127.0.0.1:8822;
}

# 或在 Docker HEALTHCHECK 中使用（Dockerfile 已内置）
```

Dockerfile 中已内置健康检查：
```dockerfile
HEALTHCHECK --interval=30s --timeout=10s --start-period=10s --retries=3 \
    CMD curl -fsS http://127.0.0.1:${PORT:-80}/health || exit 1
```

---

## 其他重要配置

```bash
# 注册开关
CHATGPT2API_REGISTRATION_ENABLED=true

# 新用户默认并发限制（0 = 不限制）
CHATGPT2API_USER_DEFAULT_CONCURRENT_LIMIT=3

# 新用户默认 RPM 限制（0 = 不限制）
CHATGPT2API_USER_DEFAULT_RPM_LIMIT=10

# 图片存储上限（MB，0 = 不限制）
CHATGPT2API_IMAGE_STORAGE_LIMIT_MB=10240

# 自动移除失效账号
CHATGPT2API_AUTO_REMOVE_INVALID_ACCOUNTS=true

# 自动移除限流账号（建议 false，等刷新恢复）
CHATGPT2API_AUTO_REMOVE_RATE_LIMITED_ACCOUNTS=false

# 日志保留天数
CHATGPT2API_LOG_RETENTION_DAYS=7
```
