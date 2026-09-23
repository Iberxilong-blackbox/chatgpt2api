# 从本机切换 Desi 上 warp-plus 的出口 IP

## 什么时候用

chatgpt2api 报下面这个错误，并且生图、刷新账号大量失败时，通常是当前代理出口 IP 被 Cloudflare 盯上了：

```text
upstream returned Cloudflare challenge page; refresh browser fingerprint/session or change proxy
```

诊断背景见 [exp/cloudflare-403-diagnosis.md](exp/cloudflare-403-diagnosis.md)。本文只讲怎么换 IP。

## 链路

```text
chatgpt2api ──socks5h──> 127.0.0.1:10086 (warp-plus 主进程 relay)
                              └─> 当前 active 子进程 ──WARP──> Psiphon ──> 出口 IP

control API：127.0.0.1:9099（只监听 Desi 本机，不对公网开放）
  GET  /health                 -> {"status":"ok"}
  POST /connectivity/refresh   -> 切换到另一个预热好的子进程
```

Desi 上的相关配置：

| 项目 | 位置 / 值 |
|------|-----------|
| 服务 | `warp-plus.service` |
| 参数文件 | `/etc/default/warp-plus` |
| 业务代理端口 | `WARP_BIND=127.0.0.1:10086` |
| control API | `WARP_CONTROL=127.0.0.1:9099` |
| 鉴权 token | `/etc/default/warp-plus` 里的 `WARP_TOKEN`，通过 `--control-token ${WARP_TOKEN}` 传给程序 |
| chatgpt2api 代理配置 | `/opt/chatgpt2api/.env` 里的 `CHATGPT2API_PROXY=socks5h://127.0.0.1:10086` |

切换只改 relay 的上游，是毫秒级的内存操作，**不需要重启 warp-plus，也不需要重启 chatgpt2api**。新建的连接走新 IP，已经建立的长连接仍走旧线路。

## 方法一：本机一条命令（推荐，手动操作时用）

用 LearnSSH 在 Desi 上执行刷新。token 始终留在服务器上，本机不需要保存。

PowerShell：

```powershell
& "$env:USERPROFILE\.codex\bin\learn-ssh.cmd" exec Desi -- '. /etc/default/warp-plus && curl -s -m 20 -X POST -H "Authorization: Bearer $WARP_TOKEN" http://127.0.0.1:9099/connectivity/refresh; echo; echo "ip=$(curl -s -m 8 --socks5-hostname 127.0.0.1:10086 https://api.ipify.org)"'
```

Git Bash：

```bash
~/.codex/bin/learn-ssh exec Desi -- '. /etc/default/warp-plus && curl -s -m 20 -X POST -H "Authorization: Bearer $WARP_TOKEN" http://127.0.0.1:9099/connectivity/refresh; echo; echo "ip=$(curl -s -m 8 --socks5-hostname 127.0.0.1:10086 https://api.ipify.org)"'
```

注意：

- 远程命令必须用**单引号**包住，`$WARP_TOKEN` 才会在服务器上展开，而不是在本机展开成空字符串。
- LearnSSH `exec` 有 **30 秒**的远程超时。一次调用只刷新一次，不要在命令里写 `sleep` 循环重试。

## 方法二：SSH 隧道 + 本机 HTTP 请求（给本机程序频繁调用时用）

把本机的 `127.0.0.1:19099` 转发到 Desi 的 `127.0.0.1:9099`，本机程序就可以像调用本地服务一样发 HTTP 请求。9099 仍然不对公网开放。

### 1. 打开隧道（单独开一个终端，保持不关）

```powershell
& "$env:USERPROFILE\.codex\bin\learn-ssh.cmd" tunnel Desi --local-port 19099 --remote-host 127.0.0.1 --remote-port 9099 --idle-timeout 3600
```

看到 `Desi tunnel 127.0.0.1:19099 -> 127.0.0.1:9099` 就说明隧道已经建立。超过 `--idle-timeout` 秒没有流量会自动关闭，设成 `0` 则不自动关闭。

### 2. 在本机准备 token

token 在 Desi 的 `/etc/default/warp-plus` 里（`WARP_TOKEN=...`）。请自己在终端里查看并设置环境变量，**不要把 token 粘贴到聊天、文档或代码仓库里**：

```powershell
$env:WARP_TOKEN = "<从服务器复制的 WARP_TOKEN>"
```

### 3. 发请求

```powershell
curl.exe -s --noproxy "*" -m 20 http://127.0.0.1:19099/health
```

```powershell
curl.exe -s --noproxy "*" -m 20 -X POST -H "Authorization: Bearer $env:WARP_TOKEN" http://127.0.0.1:19099/connectivity/refresh
```

注意：

- **必须加 `--noproxy "*"`**。本机设置了 `HTTP_PROXY=http://127.0.0.1:10808`，不加的话 curl 会把请求发给本机代理，而不是隧道，结果是空响应。
- 隧道建立后，前一两个请求可能需要 5 到 8 秒（SSH 通道刚建立时比较慢），之后稳定在 1 秒左右。所以超时要设长一点（`-m 20`）。
- 这一步只能切换 IP，看不到新 IP。要查新的出口 IP，用方法一里的 ipify 命令，或者再开一条隧道转发 10086。

## 返回值

| HTTP | 返回内容 | 含义 / 处理 |
|------|----------|-------------|
| 200 | `{"status":"ok"}` | 切换成功 |
| 401 | `{"status":"unauthorized"}` | token 缺失或错误 |
| 405 | `method_not_allowed` | 用了 GET，refresh 必须用 POST |
| 409 | `{"status":"busy"}` | 下一个子进程还在预热（约 15 秒），或者另一个刷新正在进行。旧 IP 继续服务，稍后再试 |
| 503 | `{"status":"no_acceptable_egress"}` | 候选出口和当前 IP 相同、最近用过或在黑名单里 |
| 500 | `{"status":"refresh_failed"}` | 其他失败，查 `journalctl -u warp-plus` |

## 切换后必须检查的事

1. **看真实出口 IP，不要只看 warp-plus 日志。** warp-plus 记录的是 Psiphon 的入口 IP（`server_entry_ip`），它可能和实际出口不一样。2026-09-23 实测：日志里写旧 IP 是 `192.241.210.85`，而用 ipify 查到的实际出口是 `198.199.117.74`。所以"最近用过的 IP 不再切回"这个机制可能失效，会切回一个已经被拦的出口。
2. **确认 ChatGPT 能用。** 最可靠的是在前端试一次生图。用 curl 访问 `https://chatgpt.com/` 返回 403 只能作参考，因为 chatgpt2api 用的是 surf 的 Firefox 指纹，curl 被挑战不等于程序也会被挑战。
3. 如果新 IP 还是不行，等 `409 busy` 结束后再切一次。

查看切换记录：

```powershell
& "$env:USERPROFILE\.codex\bin\learn-ssh.cmd" exec Desi -- 'journalctl -u warp-plus --since "30 min ago" --no-pager | grep -E "switched active child|child ready\"" | tail -10'
```

## 已知问题

- 当前配置是 `--cfon --country US`，候选出口基本都是 DigitalOcean 上的 Psiphon IP（198.199.x、165.232.x、143.198.x、159.223.x 等）。2026-09-16 起 Cloudflare 对这类 IP 明显收紧，换 IP 可能只能缓解一段时间。
- `--egress-check-interval 0` 关闭了定期检测，出口被拦后不会自动切换，要按本文手动触发。
- LearnSSH `tunnel` 原来有一个 bug：本机连接建立后、SSH 通道打开之前发出的数据会被丢掉，表现为 `curl: (52) Empty reply from server`。2026-09-23 已在本机的 `~/.codex/skills/learn-ssh/scripts/ssh-node-ops.mjs` 里修复（在 `tunnelCommand` 里先对 socket 调用 `socket.pause()`）。**重新安装 LearnSSH 会覆盖这个修复**，届时需要重新修改，或者向上游提交修复。
- 停止隧道时，如果只停掉外层 shell，node 进程可能还留着占用端口，再次开隧道会报 `EADDRINUSE`。用 `netstat -ano | findstr 19099` 找到 PID 后结束它。

## 相关文档

- warp-plus 原理、参数和从其他电脑触发：`C:\My_project\warp-plus\start.md`（第 12.7.1 节）
- Cloudflare 403 诊断记录：[exp/cloudflare-403-diagnosis.md](exp/cloudflare-403-diagnosis.md)
