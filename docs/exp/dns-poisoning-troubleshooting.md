# DNS 污染导致 ChatGPT 诊断连接失败 — 排查与修复

## 问题现象

- **本地开发环境**：号池管理 → 诊断连接状态 → 正常
- **服务器环境**：号池管理 → 诊断连接状态 → 失败

### 错误信息

| 检查项 | 错误 |
|--------|------|
| Bootstrap | `upstream connection failed before TLS handshake completed; check proxy reachability to chatgpt.com or change proxy` |
| CheckSession | `context deadline exceeded` (30s 超时) |
| 结论 | Bootstrap 和 Session 均失败 → 可能是代理 IP 被封，也可能是 Session 过期 |

## 关键环境差异

| 项目 | 本地 | 服务器 |
|------|------|--------|
| 位置 | - | 美国机房 |
| 代理配置 | - | 无代理（直连） |
| STORAGE_BACKEND | sqlite | postgres |

## 排查过程

### Step 1 — 代码链路分析

追踪诊断连接的完整调用链：

```
前端 POST /api/accounts/diagnose
  → routes.go:1224 ctx 30s 超时
  → Engine.TextBackend(token)
  → backend.NewClient(accessToken, e.Accounts, e.Proxy)  ← 全局代理
  → proxy.BrowserHTTPClientWithProfile(profile, 300s)    ← 使用 surf/uTLS
  → DiagnoseSession(ctx)
      → Bootstrap(ctx)     GET https://chatgpt.com/
      → CheckSession(ctx)  GET https://chatgpt.com/api/auth/session
```

关键发现：**账户级别的 proxy 字段不被 backend.Client 使用**，所有请求都走全局代理（或直连）。

### Step 2 — DNS 解析检查

```bash
nslookup chatgpt.com
# 返回 185.60.216.36（疑似污染）
```

### Step 3 — TCP/TLS 连通性测试

```bash
timeout 5 bash -c 'echo > /dev/tcp/chatgpt.com/443'
# TCP FAIL — 端口不通

curl -v4 --connect-timeout 10 https://chatgpt.com/
# Connection timed out after 10001 milliseconds
```

### Step 4 — DNS 对比验证

```bash
dig A chatgpt.com +short
# 157.240.2.14  ← 错误！这是 Facebook/Meta 的 IP

dig A chatgpt.com @8.8.8.8 +short
# 172.64.155.209
# 104.18.32.47    ← 正确！Cloudflare IP

dig A chatgpt.com @1.1.1.1 +short
# 104.18.32.47
# 172.64.155.209  ← 正确！Cloudflare IP
```

### Step 5 — 绕过 DNS 验证

```bash
curl -v4 --connect-timeout 5 --resolve chatgpt.com:443:104.18.32.47 https://chatgpt.com/
# 成功连接到 Cloudflare，拿到 challenge 页面
```

→ **确认问题在 DNS 解析环节。**

### Step 6 — 定位 DNS 配置来源

```bash
resolvectl status
# eth0:
#  Current DNS Server: 114.114.114.114   ← 根因！
#       DNS Servers: 114.114.114.114
#                    1.1.1.1
```

### Step 7 — 确认 DNS 下发方式

```bash
networkctl status eth0
# Network File: /run/systemd/network/10-netplan-eth0.network
# DNS: 114.114.114.114   ← DHCP/Cloud-init 下发
#      1.1.1.1
```

## 根因

**一台位于美国机房的服务器，其 DNS 主服务器被配置为中国的 `114.114.114.114`（114DNS）。**

`114.114.114.114` 是中国境内的公共 DNS，对 OpenAI 相关域名（`chatgpt.com`、`chat.openai.com` 等）返回被污染的 IP，指向了 Facebook/Meta 的服务器（`157.240.x.x`）。虽然备用 DNS `1.1.1.1` 返回的是正确的 Cloudflare IP，但 systemd-resolved 默认仅当主 DNS 完全不可达时才会切换备用，而 114DNS 是可通且正常响应的（只是内容被污染），所以备用 DNS 永远不会被使用。

| DNS 来源 | chatgpt.com 解析结果 |
|----------|---------------------|
| 114.114.114.114 (主) | `157.240.2.14` ❌ Facebook IP |
| 1.1.1.1 (备) | `104.18.32.47` / `172.64.155.209` ✅ Cloudflare |

污染后的 IP 属于 Facebook，对这些 IP 发起 HTTPS 连接时：
- **Bootstrap**：surf/uTLS 库尝试 TLS 握手，对端是 Facebook 服务器而非 ChatGPT，TLS 握手失败 → `upstream connection failed before TLS handshake completed`
- **CheckSession**：TCP 连接挂起直到 30 秒 context 超时 → `context deadline exceeded`

## 修复方案

### 临时修复（立即生效，重启后失效）

```bash
resolvectl dns eth0 1.1.1.1 8.8.8.8
```

### 永久修复

创建 systemd-networkd 的 override 配置，覆盖 DHCP 下发的 DNS：

```bash
mkdir -p /etc/systemd/network/10-netplan-eth0.network.d

cat > /etc/systemd/network/10-netplan-eth0.network.d/override-dns.conf << 'EOF'
[Network]
DNS=1.1.1.1
DNS=8.8.8.8
EOF

networkctl reload
```

### 验证

```bash
# DNS 配置
networkctl status eth0 | grep DNS
# 应显示: 1.1.1.1 / 8.8.8.8

# DNS 解析
dig A chatgpt.com +short
# 应返回 Cloudflare IP: 104.18.32.47 / 172.64.155.209

# HTTPS 连通性
curl -v4 --connect-timeout 5 https://chatgpt.com/ 2>&1 | tail -3
# 应返回 Cloudflare challenge 页面
```

## 涉及文件

| 文件 | 说明 |
|------|------|
| `internal/httpapi/routes.go:1213-1228` | 诊断接口 handler（30s context 超时） |
| `internal/backend/backend.go:67-82` | NewClient — 使用全局代理创建 HTTP 客户端 |
| `internal/backend/backend.go:388-412` | Bootstrap 方法 |
| `internal/backend/backend.go:1164-1211` | DiagnoseSession 方法 |
| `internal/backend/backend.go:1215-1227` | CheckSession 方法 |
| `internal/service/proxy.go:91-108` | browserHTTPClientForProfile — surf/uTLS 客户端创建 |
| `internal/util/upstream_error.go:7-24` | SummarizeUpstreamConnectionError — 错误标准化 |
| `/etc/systemd/resolved.conf` | systemd-resolved 配置 |
| `/run/systemd/network/10-netplan-eth0.network` | netplan/cloud-init 下发的网络配置 |
| `/etc/systemd/network/10-netplan-eth0.network.d/override-dns.conf` | 手动创建的 DNS override |
