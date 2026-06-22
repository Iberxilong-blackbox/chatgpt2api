# DNS 污染永久修复记录

**日期**: 2026-06-22  
**服务器**: ser838159181480  
**问题**: 账号刷新额度时 `dial tcp` 到污染 IP 导致 `i/o timeout`

---

## 问题现象

刷新账号额度失败，错误：

```
Get "https://chatgpt.com/": surf: HTTP/2 request failed:
dial tcp 128.242.240.91:443: i/o timeout;
HTTP/1.1 fallback failed: dial tcp 128.242.240.91:443: i/o timeout
```

## 诊断过程

### 1. 确认 DNS 污染

```bash
nslookup chatgpt.com           # → 128.121.243.77 ❌ 污染 IP
nslookup chatgpt.com 8.8.8.8   # → 104.18.32.47 / 172.64.155.209 ✅ Cloudflare
```

### 2. 查看 systemd-resolved 状态

```bash
resolvectl status eth0
```

输出：

```
Current DNS Server: 114.114.114.114   ← 污染源
       DNS Servers: 114.114.114.114   ← 主 DNS（中国 114DNS）
                    1.1.1.1           ← 备用（正确，但永远不会被使用）
                    8.8.8.8           ← 备用（正确，但永远不会被使用）
```

**关键认知**: systemd-resolved 只要主 DNS 能通就不会切备用。114DNS 是通的，只是返回被污染的结果。

### 3. 追溯 DNS 配置来源

```bash
networkctl status eth0
# → Network File: /run/systemd/network/10-netplan-eth0.network
#   DNS: 114.114.114.114, 1.1.1.1, 8.8.8.8
```

`/run` 是临时文件系统，内容由 netplan 生成。继续追溯：

```bash
cat /etc/netplan/50-network.yaml
```

```yaml
nameservers:
    addresses: 
    - 114.114.114.114   # ← 根因：云厂商模板硬编码的污染 DNS
    - 1.1.1.1
```

### 4. 完整因果链

```
/etc/netplan/50-network.yaml (cloud-init 模板)
  → systemd-networkd 生成 /run/systemd/network/10-netplan-eth0.network
    → systemd-resolved 把 114.114.114.114 设为主 DNS
      → chatgpt.com 解析到污染 IP
        → TCP 连接超时 → 刷新额度失败
```

## 修复步骤

### 永久修复

修改 netplan 配置，用 `1.1.1.1` 和 `8.8.8.8` 替换 `114.114.114.114`：

```bash
cat > /etc/netplan/50-network.yaml << 'EOF'
# network-config
network:
    version: 2
    ethernets:
        lo:
            addresses:
            - 127.0.0.1/8
        eth0:
            addresses:
            - 10.0.15.2/24
            routes:
            - to: 0.0.0.0/0
              via: 10.0.15.1
            match:
                macaddress: d2:48:c3:00:02:93
            set-name: eth0
            nameservers:
                addresses: 
                - 1.1.1.1
                - 8.8.8.8
EOF

netplan apply
```

### 验证

```bash
resolvectl status eth0     # DNS Servers 应为 1.1.1.1 + 8.8.8.8，无 114
nslookup chatgpt.com       # 应返回 Cloudflare IP
```

## 防复发措施

### 锁定 netplan 配置文件

cloud-init 或云厂商可能在重启/重配时重新写入 netplan 文件，用 `chattr +i` 设置为不可变：

```bash
chattr +i /etc/netplan/50-network.yaml
```

### 验证锁定

```bash
lsattr /etc/netplan/50-network.yaml
# → ----i---------e----- /etc/netplan/50-network.yaml
```

`i` 标志表示文件不可变，root 也无法修改或删除。

### 模拟恢复测试

如果将来需要修改该文件，先解锁：

```bash
chattr -i /etc/netplan/50-network.yaml   # 解锁
# 修改文件...
chattr +i /etc/netplan/50-network.yaml   # 重新锁定
```

## 重启验证

| 检查项 | 命令 | 预期结果 |
|--------|------|----------|
| chattr 锁 | `lsattr /etc/netplan/50-network.yaml` | 仍有 `i` 标志 |
| DNS 服务器 | `resolvectl status eth0` | `1.1.1.1` + `8.8.8.8`，无 `114` |
| 域名解析 | `nslookup chatgpt.com` | Cloudflare IP |

三项均通过即修复生效且持久。

## 快速参考

```bash
# 诊断
nslookup chatgpt.com                   # 当前 DNS 解析结果
nslookup chatgpt.com 8.8.8.8           # 对比正确结果
resolvectl status eth0                 # 查看 DNS 配置来源
cat /etc/netplan/50-network.yaml       # 查看 netplan 配置

# 修复
netplan apply                          # 应用 netplan 更改

# 锁定
chattr +i /etc/netplan/50-network.yaml # 防止 cloud-init 覆盖
lsattr /etc/netplan/50-network.yaml    # 确认锁定
```

## 相关文档

- [DNS 污染排查与修复](./dns-poisoning-troubleshooting.md) — 详细诊断流程
