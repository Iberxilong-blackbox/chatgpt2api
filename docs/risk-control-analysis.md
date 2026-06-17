# 多用户风控分析与规避策略

## 风控核心维度

ChatGPT 的反爬/反滥用系统从以下维度检测异常行为：

### 1. IP 维度（风险最高）

**现状：** 所有用户、所有账号的请求都从服务器单一出口 IP 发出。

ChatGPT 视角：
- 同一 IP 在短时间内切换多个不同的 access_token
- 同一 IP 高频请求（多用户并发时更明显）
- 这是**多账号共享/API 滥用**的典型特征

**结论：IP 是当前架构中最薄弱的环节。**

### 2. 设备指纹维度（项目处理较好）

每个 ChatGPT 账号独立维护一套持久指纹（`fp`），项目代码位于 `internal/service/account.go:prepareAccountFP`：

| 指纹字段 | 作用 | 策略 |
|----------|------|------|
| `oai-device-id` | 设备唯一标识 | 首次生成后**永不改变**，模拟稳定的长期设备 |
| `oai-session-id` | 会话标识 | 同上 |
| `user-agent` | 浏览器 UA | 从导入的 CPA JSON 读取，或使用默认 Chrome 145 |
| `sec-ch-ua` | 浏览器品牌版本 | 同上 |
| `sec-ch-ua-platform` | 操作系统 | 同上 |
| `impersonate` | uTLS 模拟目标 | 根据 Chrome 主版本号自动推导，用于 TLS 指纹绕过 |

指纹导入优先级：
1. 已有 `fp` 字段 → 直接保留
2. CPA JSON 格式的 `fingerprint.browser.*` → 映射转换
3. 无任何指纹 → 自动生成默认 Chrome 145 Windows 指纹

### 3. 请求频率与行为模式

**文本对话（`GetTextAccessToken`）：**
- Free 账号：10 次/5 小时软限制（`maxRequestsPerAccount = 10`），5 小时后重置计数
- Plus/Pro/Team 账号：不限制次数，采用最少请求数优先的选择算法
- 选择策略：优先选当前周期内请求次数最少的账号

**生图（`reserveNextCandidateToken`）：**
- 按 `quota`（剩余额度）做 slot 预留，每个账号的并发槽位数 = 其剩余 quota
- quota 用完的账号状态变为 "限流"，不再进入候选池
- 轮询（round-robin index）在有可用 slot 的账号间分配
- 图片 quota 未知时保守设为 1

**Session 保持（`SessionRefresher`）：**
- `/api/auth/session` 端点刷新，最大并发 5 个 refresh
- 同一 token 的去重刷新（in-flight 合并）
- 限流账号每 N 分钟检查一次，调用 refresh 尝试恢复

### 4. 账号池隔离

项目区分了多种账号状态，确保异常账号不污染业务池：

| 状态 | 是否参与业务 |
|------|-------------|
| 正常 | 是 |
| 限流 | 否，等待定时刷新恢复 |
| 禁用 | 否 |
| 异常 | 否 |
| 刷新中 | 否 |
| 过期待刷新 | 否，等待定时刷新 |
| 养号中（warming） | 否，仅走预热流程 |

---

## 多用户同时使用的风控要点

### 已实现且有效的措施

1. **账号池轮询** — 多用户的请求分散到不同账号，避免单个账号超高频使用
2. **稳定设备指纹** — 每个账号有唯一且不变动的 `oai-device-id`，模拟真实长期设备
3. **uTLS 指纹模拟** — 通过 `surf` 库模拟 Chrome/Firefox TLS 握手特征，绕过 Cloudflare WAF
4. **账号预热（warming）** — 新导入的账号先经历模拟人类浏览的预热流程再投入业务
5. **限流自动恢复** — `StartLimitedWatcher` 定时检查限流账号并尝试刷新
6. **Session Token 刷新** — 支持通过 `session_token` 刷新 `access_token`，延长账号可用时间
7. **养号账号隔离** — `warming_status` 为 "warming" 的账号不会被拿去处理业务请求

### 需要人工关注的方面

1. **账号池要足够大且类型分散** — 不要全用 Free 账号。混入 Plus/Pro/Team 提高池子鲁棒性。付费账号无 10次/5h 限制，行为更接近正常用户
2. **每个账号的指纹信息尽量不同** — 从不同来源导入的 CPA JSON 带有不同的 `fingerprint.browser.*`，自动映射后自然形成多样化指纹
3. **避免同一账号同时处理多个请求** — 项目已有 slot 预留和请求计数机制，但要注意如果设置了极高的并发限制可能导致单账号过载
4. **监控账号状态变化** — 关注 status 变为 "异常"、"token_invalid" 的账号，可能是上游风控的预警信号
5. **请求间隔** — warming 中有间隔模拟，但业务请求没有加入随机延迟。大量用户同时请求时可以考虑在前端/API 层面做请求队列和限速

---

## WARP 分析与代理方案

### WARP 能做什么

Cloudflare WARP（基于 WireGuard 的 VPN）将服务器流量通过 Cloudflare 全球网络出口：

**有利方面：**
- 出口 IP 变更为 Cloudflare IP 段，不再是容易被标记的 VPS/IDC IP
- Cloudflare 拥有大量 IP，部分属于住宅/企业混合型 IP
- 如果被目标封了特定 IP，WARP 重连可能获取新 IP

**局限性：**
- **仍然是单一出口 IP** — 所有账号依然共享同一 IP，多账号关联没有本质解决
- ChatGPT 本身也部署在 Cloudflare 上，Cloudflare-to-Cloudflare 的流量可能有特殊的检测逻辑
- WARP IP 可能被 ChatGPT 标记为 VPN/代理 IP（参考：很多 VPN IP 已被 OpenAI 封禁）
- WARP 不能通过 `CHATGPT2API_PROXY` 直接配置，需要系统级网络设置（如 `iptables` 将目标为 chatgpt.com 的流量路由到 WARP 接口）

### 代理池方案（推荐）

项目的 `ProxyService`（`internal/service/proxy.go`）已支持 http/https/socks5/socks5h 代理。当前通过 `CHATGPT2API_PROXY` 设置**全局**代理。

**理想的多代理方案：**

```
账号A → 代理IP_1 → chatgpt.com
账号B → 代理IP_2 → chatgpt.com
账号C → 代理IP_3 → chatgpt.com
```

**需要注意的改造点：**
- 当前 `ProxyService` 只读取一个全局 `Proxy()` 配置
- 要实现每个账号独立代理，需要：
  1. 在账号数据中增加 `proxy` 字段
  2. 修改 `ProxyService` 支持按 token 选择代理
  3. 代理 IP 的来源可以是：住宅代理服务商、自建代理池、或不同地区的 VPS 做中转

**代理选择建议：**
- **住宅代理（Residential Proxy）** — 信任度最高，但成本较高
- **数据中心代理（Datacenter Proxy）** — 成本低但容易被标记，需要轮换频率更高
- **ISP 代理** — 介于两者之间，性价比相对较好

### 各方案对比

| 方案 | IP 隔离 | 信任度 | 成本 | 实施难度 |
|------|---------|--------|------|----------|
| 裸机直连 | 无，多账号关联严重 | 低（IDC IP） | 无 | 无需改造 |
| 全局代理（单一） | 无，仅换一个出口 | 取决于代理类型 | 低 | 设置环境变量即可 |
| WARP | 无，单一 Cloudflare IP | 中 | 无 | 需要系统级网络配置 |
| 每账号独立代理 | 有，理想隔离 | 取决于代理类型 | 中-高 | 需要改造 ProxyService |

---

## 风控优先级总结

| 优先级 | 措施 | 当前状态 | 是否需要改代码 |
|--------|------|----------|---------------|
| P0 | 账号池充裕（多类型、多账号） | 已支持，需人工管理 | 否 |
| P0 | 稳定且多样化的设备指纹 | 已支持 | 否 |
| P0 | 账号预热后再投入使用 | 已支持 | 否 |
| P1 | **IP 隔离（每账号独立出口 IP）** | 仅支持全局代理 | **需要改造** |
| P1 | 请求限速（按用户/按账号） | 部分支持（并发+slot） | 可配置 |
| P1 | 异常账号监控与告警 | 有状态标记，无告警 | 需自行监控 |
| P2 | 业务请求加入人类行为延迟 | warming 中有，业务无 | 需要改造 |
| P2 | 代理健康检查和自动切换 | 无 | 需要改造 |

### 最有效的单项改进

**让每个账号走不同的代理 IP。** 这是目前最薄弱的环节。即使有再好的设备指纹和账号池管理，同一个 IP 频繁切换多个 access_token 是 ChatGPT 反爬系统最容易捕获的模式。建议优先实现账号级别的代理配置，并使用住宅或 ISP 代理做 IP 隔离。
