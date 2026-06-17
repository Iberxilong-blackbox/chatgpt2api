# 服务器部署指南

## 部署前准备

### 必须配置的环境变量

| 变量 | 说明 | 示例 |
|------|------|------|
| `CHATGPT2API_BASE_URL` | 部署域名，图片 URL 生成依赖此值 | `https://your-domain.com` |
| `CHATGPT2API_ADMIN_PASSWORD` | 管理员密码，不设则启动时自动生成并打印到日志 | `your-password` |
| `PORT` | 监听端口，Docker 内默认 80，裸机默认 8822 | `8822` |

### 存储后端选择

默认使用 SQLite（`data/chatgpt2api.db`）。**多用户场景强烈建议使用 PostgreSQL**：

```
STORAGE_BACKEND=postgres
DATABASE_URL=postgresql://user:password@host:5432/chatgpt2api
```

也支持 MySQL：
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

```bash
# 1. 构建前端
cd web && bun install && bun run build

# 2. 构建后端（前端已嵌入）
cd .. && go build -o chatgpt2api ./internal

# 3. 运行
./chatgpt2api
```

---

## Nginx 反向代理配置

### 重要前提：图片鉴权

项目中的图片访问有权限校验（`authorizeImageFileRequest`），区分 `public` 和 `private`：
- `public` 图片：任何人均可访问
- `private` 图片：仅 owner 和管理员可访问

**如果让 nginx 直接 serve 图片目录，会绕过鉴权。** 需要根据你的业务决定：
- 如果所有图片都是 public，可以让 nginx 直接 serve 以提升性能
- 如果有 private 图片，必须让 Go 后端处理图片请求（走鉴权）

### 推荐配置（走 Go 后端，保留鉴权）

```nginx
server {
    listen 443 ssl http2;
    server_name your-domain.com;

    # 上传和请求体大小限制
    client_max_body_size 50m;

    # API 和 Web —— 全部反向代理到 Go
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

        # 长连接超时（生图默认 300s）
        proxy_read_timeout 600s;
        proxy_send_timeout 600s;
    }
}
```

### 性能优化配置（nginx 直接 serve 图片，仅适用于全 public 图片场景）

```nginx
server {
    listen 443 ssl http2;
    server_name your-domain.com;

    client_max_body_size 50m;

    # 图片 —— nginx 直接 serve，卸载 Go 压力
    location /images/ {
        alias /app/data/images/;
        expires 7d;
        add_header Cache-Control "public, max-age=604800";
        add_header Access-Control-Allow-Origin "*";
    }

    location /image-thumbnails/ {
        alias /app/data/image_thumbnails/;
        expires 1y;
        add_header Cache-Control "public, max-age=31536000, immutable";
        add_header Access-Control-Allow-Origin "*";
    }

    location /image-references/ {
        alias /app/data/images/;
        expires 7d;
        add_header Cache-Control "public, max-age=604800";
    }

    # 其余请求走 Go
    location / {
        proxy_pass http://127.0.0.1:8822;
        proxy_http_version 1.1;
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

### 关键配置说明

| 配置项 | 重要性 | 说明 |
|--------|--------|------|
| `proxy_buffering off` | **必须** | `/v1/chat/completions` 和生图接口使用 SSE 流式输出，nginx 缓冲会导致客户端收不到实时数据 |
| `proxy_read_timeout` | **必须** | 生图默认 300s 超时（可配置 `CHATGPT2API_IMAGE_TASK_TIMEOUT_SECONDS`），nginx 超时必须大于该值 |
| `client_max_body_size` | 建议 | 生图编辑（`/v1/images/edits`）会上传参考图，需要足够大 |
| SSL | 建议 | 生产环境务必配置 HTTPS，可使用 Let's Encrypt + certbot |

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
