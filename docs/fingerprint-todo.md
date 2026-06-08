# 指纹优化 TODO

> 优先级：P0=紧急/致命 → P1=重要 → P2=优化

---

## ✅ 已完成

- [x] **P0-1: 导入时自动生成并固化 oai-device-id**
  - 文件：`internal/service/account.go` — `AddAccountRecords()` + `prepareAccountFP()`
  - 三种场景：已有 fp 保留、外部 CPA fingerprint 映射、无指纹自动生成默认
  - 一旦写入，`current["fp"] == nil` 检查保证永不覆盖

- [x] **P0-2: 确定性 UUID 兜底（当 fp 不存在时）**
  - 文件：`internal/backend/backend.go` — `deterministicUUID()` + `buildFingerprint()`
  - SHA256(accessToken) 格式化为 UUID v4，同一 token 永远同一 device-id
  - 覆盖旧数据、异常数据、匿名请求

- [x] **P0-3: `prepareAccountFP()` Case 2 复用外部 `oai_device_id`**
  - 文件：`internal/service/account.go` — `prepareAccountFP()` Case 2
  - 外部 CPA JSON 的 `fingerprint.oai_device_id` 和 `fingerprint.oai_session_id` 直接复用
  - 仅当外部未提供时才生成新 UUID

- [x] **P1-1: 目录扫描自动导入 CPA JSON 文件**
  - 文件：`internal/service/account.go` — `ImportAccountJSONFiles()` + `ImportScanDir()`
  - 扫描 `{DataDir}/auto_import/`（可通过 `CHATGPT2API_IMPORT_DIR` 覆盖）
  - 导入成功后移至 `imported/` 子目录，避免重复导入

- [x] **P1-2: 手动触发扫描 API**
  - 文件：`internal/httpapi/routes.go`
  - 新增 `POST /api/accounts/import-scan`，返回 `{added, errors}`

- [x] **P1-3: 启动时自动扫描**
  - 文件：`internal/httpapi/app.go`
  - `NewApp()` 中异步调用 `ImportScanDir()`，非阻塞

- [x] **P1-3 (原): 前端 CPA JSON 指纹透传**
  - 文件：`web/src/lib/api.ts` + `account-import-dialog.tsx`
  - `AccountImport` 增加 `fingerprint` 字段
  - CPA 导入时将 `parsed.fingerprint` 原样传递到后端

---

## ~P1 — 指纹离散化（已取消）~

> **取消原因：** 每个 CPA JSON 文件自带完整的 `fingerprint.browser.*`（ua, sec-ch-ua, platform, chrome_major 等），且各账号的 json 来自不同平台/设备，天然具备离散化。不需要项目内维护模板池和随机分配逻辑。Case 3（无指纹纯 token 导入）保留现有默认 Chrome 145 Windows 指纹即可。

---

## P2 — 代理层优化

**架构决策：本项目不管理 IP 池。代理出口由下游服务商（BrightData/911/其它）负责。**

- [ ] **P2-1: 代理服务商模式优化**
  - 利用服务商的 sticky session 功能让同一账号走固定出口 IP
  - 健康检测 & 故障切换（监测代理可用性，切备用入口）

- [ ] **P2-2: 多代理服务商入口支持**
  - 文件：`internal/config/config.go` + `internal/service/proxy.go`
  - 新增 `CHATGPT2API_PROXY_POOL` 环境变量（逗号分隔多个代理入口）
  - 哈希取模分配，一个账号固定走同一个入口

- [ ] **P2-3: 账号级代理绑定**
  - 文件：`internal/service/account.go`
  - account 中增加 `proxy_url` 字段（可空）
  - 优先读取 `account.proxy_url`，为空则回退到全局或池路由
  - 解决代理池增删时账号漂移问题

---

## P3 — 持续优化

- [ ] **P3-1: 账号级请求频率控制**
  - 新建 `internal/service/ratelimit.go`
  - per-account 并发限制 + 请求间隔控制

- [ ] **P3-2: 请求参数随机化**
  - 文件：`internal/backend/backend.go`
  - screen_width / screen_height / page_height / pixel_ratio 改为合理随机值
  - time_since_loaded 每次非固定值

---

## 进度记录

| 日期 | 完成项 | 备注 |
|------|--------|------|
| 2026-05-27 | P0-1, P0-2, P1-3 | 导入固化 fp + 确定性 UUID 兜底 + 前端指纹透传 |
| 2026-06-06 | 发现 P0-3 遗漏 | Case 2 未复用外部 oai_device_id；指纹模板池 P1-1 取消（外部 json 已覆盖） |
| — | — | — |
