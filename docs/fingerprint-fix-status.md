# 指纹修复 — 实施状态与改动总结

> 更新日期：2026-06-06
> 状态：P0-3 遗漏已修复；目录自动导入（P1-1~3）已完成

---

## 一、改动文件清单

| 文件 | 改动类型 | 说明 |
|------|---------|------|
| `internal/service/account.go` | 新增函数 + 逻辑修改 | 导入时固化 fp，永不更新 |
| `internal/backend/backend.go` | 新增函数 + 默认值修改 | 确定性 UUID 兜底 |
| `web/src/lib/api.ts` | 类型扩展 | `AccountImport` 新增 `fingerprint` 字段 |
| `web/src/app/accounts/components/account-import-dialog.tsx` | 逻辑修改 | CPA JSON 指纹透传 |

---

## 二、详细改动说明

### 2.1 `internal/service/account.go`

**新增函数：**

```
prepareAccountFP(record map[string]any) → map[string]any
detectImpersonateFromUA(ua string) → string
```

**`prepareAccountFP` 支持三种场景（优先级从高到低）：**

| 场景 | 触发条件 | 行为 |
|------|---------|------|
| 已有内部 fp | `record["fp"]` 存在 | 浅拷贝后确保 `oai-device-id` / `oai-session-id` 存在，原样保留 |
| 外部 CPA 格式 | `record["fingerprint.browser.*"]` 存在 | 映射 ua / sec_ch_ua / sec_ch_ua_platform / sec_ch_ua_mobile + 从 chrome_major 推断 impersonate |
| 纯 Token 导入 | 两者皆无 | 生成 Chrome 145 Windows 默认指纹 |

**核心修改 `AddAccountRecords()`（第 214-220 行）：**

```go
// Generate persistent fingerprint for accounts that don't have one yet.
// Once set, fp is NEVER overwritten — oai-device-id stays fixed for life.
if current["fp"] == nil {
    if fp := prepareAccountFP(record); fp != nil {
        updates["fp"] = fp
    }
}
```
- 在 `mergeMaps(current, updates)` **之前**注入 fp
- `current["fp"]` 非空时不覆盖——**现有账号的 fp 永不更新**
- 使用 `if current["fp"] == nil` 而非仅在新增时生成，确保旧账号（此前导入无 fp）也能得到 fp

**数据流：**

```
前端导入 → cleanAccountRecords()       → normalizeAccount()
              ↓                              ↓
          prepareAccountFP()              mergeMaps()
              ↓                              ↓
          {fp, access_token} → updates      → 保存到 storage
```

### 2.2 `internal/backend/backend.go`

**新增函数：**

```go
func deterministicUUID(accessToken, namespace string) string
```

- 对 `namespace:accessToken` 做 SHA256
- 按 UUID v4 规范设置版本位和变体位
- 格式化为标准 `xxxxxxxx-xxxx-4xxx-yxxx-xxxxxxxxxxxx` 字符串
- `accessToken` 为空时（匿名请求）→ 产生常量 UUID（所有匿名请求共享同一 device-id）
- `accessToken` 非空时 → 每个 token 对应唯一、稳定的 device-id

**修改 `buildFingerprint()`（第 225-226 行）：**

```go
// 改前
"oai-device-id":      util.NewUUID(),   // 每次 NewClient() 随机生成
"oai-session-id":     util.NewUUID(),

// 改后
"oai-device-id":      deterministicUUID(c.AccessToken, "device"),
"oai-session-id":     deterministicUUID(c.AccessToken, "session"),
```

`util.NewUUID()` 在其他位置（conversation message ID 等）保持不变，仅 device-id / session-id 改用确定性生成。

### 2.3 `web/src/lib/api.ts`

```typescript
export type AccountImport = {
  access_token: string;
  type?: AccountType;
  plan_type?: string;
  chatgpt_plan_type?: string;
  fingerprint?: unknown;  // 新增：外部 CPA 指纹透传
};
```

### 2.4 `web/src/app/accounts/components/account-import-dialog.tsx`

在 `handleCpaSelected` 中，构建 `AccountImport` 对象时增加：

```typescript
fingerprint: (parsed as Record<string, unknown>).fingerprint,
```

使 CPA JSON 文件中的 `fingerprint` 对象原样传递到后端，触发 `prepareAccountFP` 的场景 2 映射逻辑。

---

## 三、安全网架构

```
┌─────────────────────────────────────────────────────────────────┐
│                   双层安全网                                      │
├─────────────────────────────────────────────────────────────────┤
│                                                                  │
│  第一层（主动策略）：导入时在 account.go 生成 fp 并持久化           │
│  ┌──────────┐    ┌───────────────┐    ┌──────────────────┐      │
│  │ 导入账号   │ → │ prepareFP()  │ → │ storage.fp        │      │
│  └──────────┘    │ 生成 oai-     │    │ (永不更新)        │      │
│                   │ device-id     │    └──────────────────┘      │
│                   └───────────────┘          ↓                   │
│                                       ┌──────────────┐          │
│                                       │ backend.go    │          │
│                                       │ buildFP()     │          │
│                                       │ 读取 account  │          │
│                                       │ fp 字段       │          │
│                                       └──────────────┘          │
│                                                                  │
│  第二层（被动兜底）：fp 缺失时在后端用确定性 UUID                   │
│  ┌─────────────────┐    ┌──────────────────────────┐            │
│  │ account.fp 为空  │ → │ deterministicUUID(token)  │            │
│  │ (旧数据/异常)    │    │ SHA256 → UUIDv4           │            │
│  └─────────────────┘    │ 每次结果相同               │            │
│                          └──────────────────────────┘            │
│                                                                  │
└─────────────────────────────────────────────────────────────────┘
```

---

## 四、TODO 更新

### ✅ 已完成

- [x] **P0-1**: 导入时自动生成并固化 fp（`prepareAccountFP()` + `AddAccountRecords()`）
- [x] **P0-2**: 确定性 UUID 兜底（`deterministicUUID()` in `backend.go`）
- [x] **P0-3**: Case 2 复用外部 `oai_device_id`（`prepareAccountFP()` 第 1760-1771 行）
- [x] **P1-1**: 目录自动导入 — `ImportAccountJSONFiles()` + `ImportScanDir()`，扫描 → 解析 → `AddAccountRecords()` → 移至 `imported/`
- [x] **P1-2**: 手动触发扫描 API — `POST /api/accounts/import-scan`，返回 `{added, errors}`
- [x] **P1-3**: 启动时自动扫描 — `NewApp()` 中异步调用，非阻塞
- [x] **~~P1 指纹模板池~~** — 取消。每个 CPA JSON 自带完整指纹，天然离散化。

### ⏳ 待推进

**后续：**
- [ ] **P1-4**: 前端账号面板显示 fp 信息（可选，调试用）
- [ ] **P2-1**: 代理服务商模式优化（利用服务商 sticky session、健康检测等）
- [ ] **P2-2**: 多代理服务商入口支持（`CHATGPT2API_PROXY_POOL`，哈希取模分配）
- [ ] **P2-3**: 账号级代理绑定（account 中增加 `proxy_url` 字段）
- [ ] **P3-1**: 账号级请求频率控制
- [ ] **P3-2**: 请求参数随机化（screen / time_since_loaded / pixel_ratio 等）
