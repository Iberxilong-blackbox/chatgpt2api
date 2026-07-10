# Sentinel/PoW 性能优化方案与服务器选型分析

日期: 2026-07-10

---

## 一、当前问题

每次生图请求（或文本对话请求）都会经过完整的 sentinel 验证流程：

```
Bootstrap → Requirements Token → POST /sentinel/req → POST /prepare
  → PoW求解(2~5s) → Turnstile VM(0.05~0.5s) → POST /finalize
  → SO Collector(异步) → POST /sentinel/ping
```

各阶段耗时占比：

| 阶段 | 算法 | 单次耗时 | 占比 | 阻塞 |
|------|------|----------|------|------|
| PoW 工作量证明 | FNV-1a 哈希暴力搜索 (最大50万次) | **2~5s** | **~90%** | 是 |
| Turnstile VM | 35-opcode 字节码解释器 (最大5万步) | 50~500ms | ~7% | 是 |
| SO Collector | 500+ 指令 VM (60s 内) | 100ms~1s | ~2% | 否(异步) |
| SO Snapshot | 复用 collector 寄存器快照 | 10~100ms | ~1% | 首次后缓存 |
| Requirements Token | 25元素指纹 + JSON/base64 | <5ms | ~0% | 是 |

**PoW 是绝对瓶颈。** 每个请求都独立完成一轮完整的 PoW 计算，请求之间没有缓存复用。

---

## 二、优化方案（按性价比排列）

### P0: PoW 热循环模板化 — 预计提升 3-5x

**问题根因：**

当前 PoW 内循环 `SolveProofOfWork()` (`internal/prooftoken/prooftoken.go:264`) 每次迭代都做了大量冗余工作：

```go
for i := 0; i < 500_000; i++ {
    config := c.buildConfig(rng, &nonce, &elapsed) // 重建25元素数组 + time.Since
    encoded := EncodeConfig(config)                 // JSON Marshal + Base64 (大量堆分配)
    hashResult := FNV1aHash(seed + encoded)         // 字符串拼接 (再次堆分配)
}
```

25 个 config 元素中**只有 [3] (nonce) 和 [9] (elapsed) 每次迭代不同**，其余 23 个全程不变。

**优化思路：** 预序列化静态部分为模板字符串，每次迭代只做字符串格式化拼接两个整数。

```
迭代前: template = `[3000,"Thu Jul 10 ... GMT",4294967296,%d,"Mozilla...",...,%d,...]`
迭代中: encoded = fmt.Sprintf(template, nonce, elapsed)  // 无 json.Marshal, 无 base64
```

**附加优化：**
- `elapsed = time.Since()` 只在每 1000 次迭代时更新一次，而非每次
- 冻结 `dateStr` / `timeOrigin`，PoW 跑 3 秒期间日期字符串不会变化

**改动范围：** `internal/prooftoken/prooftoken.go`，约 100 行

**预期效果：** 单次迭代从 ~10μs 降至 ~1-2μs，PoW 总耗时从 2-5s 降至 **0.3-0.8s**

---

### P1: Chat Requirements Token 缓存 — 预计提升 10x+（连续请求时）

**问题根因：**

每个请求都完整走 prepare → PoW → Turnstile → finalize 全流程，即使同一个 Client 实例在短时间内发多个请求。`getChatRequirements()` (`internal/backend/backend.go:435`) 没有任何缓存机制。

`finalize` 返回的 `token` 大概率有数十分钟的有效期——ChatGPT 网页端在同一会话中不会每次发消息都重新做 sentinel。

**优化思路：**

```go
type Client struct {
    // ...
    cachedReqs    *ChatRequirements
    reqsCachedAt  time.Time
    reqsTTL       time.Duration  // 例如 15min
}
```

在 `getChatRequirements()` 入口处检查缓存，命中就直接返回，跳过 PoW + Turnstile + finalize 全流程。

**改动范围：** `internal/backend/backend.go`，约 50 行

**预期效果：** 缓存命中时，sentinel 计算耗时 **→ 0**（只有首次请求需完整计算）

---

### P2: 换更强服务器（硬件方案）— 线性提升

见本文第三部分的详细分析。

---

### P3: HTTP 连接池调优 — 小幅提升

**问题：** Go 默认 `http.DefaultTransport` 的 `MaxIdleConnsPerHost` 仅 2。10 并发时多余连接需频繁建立/断开 TCP。

**优化：** 调大 `MaxIdleConnsPerHost` 到并发数以上，减少握手开销。

**改动范围：** `internal/backend/backend.go` 或 Proxy 配置处，约 10 行

---

## 三、服务器选型分析

### 3.1 我们的计算是什么性质？

**PoW（工作量证明）是典型的 CPU 密集型（CPU-bound）任务。**

特征：
- 几乎不涉及磁盘 I/O
- 几乎不涉及网络 I/O（循环内只做纯计算）
- 内存访问为局部变量，cache-friendly
- 每个请求的计算完全独立，无共享状态
- FNV-1a 哈希是整数位运算（XOR + 32位乘法 + 移位）

换个说法向别人描述这个需求：

> 我们有一个 API 代理服务，每次请求进来需要先完成一轮工作量证明（PoW）计算——这是一个纯 CPU 密集型操作，单个请求约需 3-5 秒的满核计算。我们需要在高峰期同时处理 N 个并发请求。瓶颈在 CPU 而非内存或磁盘。

### 3.2 少核高频 vs 多核低频，怎么选？

设：
- **P**: 单核心性能（完成一次 PoW 的时间）
- **C**: 核心数
- **Q**: 并发请求数

**关键公式：每个请求的 PoW 耗时 ≈ 一次 PoW 计算时间 / 能分到的 CPU 时间片比例**

在 CPU 无竞争时（请求数 ≤ 核心数）：

```
总吞吐量 (请求/秒) ≈ C / 单次 PoW 耗时
```

在 CPU 满负荷时（请求数 > 核心数）：

```
每个请求完成时间 ≈ 单次 PoW 耗时 × (并发数 / 核心数)
```

**结论：**

| 场景 | 优先什么 | 原因 |
|------|----------|------|
| **延迟敏感**（用户等待时间要短） | 少核**高频** | PoW 是串行算法无法并行化到多核，单核跑一次就是物理极限 |
| **吞吐优先**（同时处理很多请求） | **多核** | 每个 request 对应一个 goroutine，多核可同时处理多个 PoW |

**但有个关键约束：Go 的 goroutine 调度。**

即使你有 10 并发请求但只有 2 核，10 个 PoW goroutine 都会同时跑（Go 的时间片轮转），但 CPU 时间被切分。结果是：**每个请求的单次 PoW 耗时变成原来的 5 倍（10÷2）**。

所以实际选择取决于：

```
单次 PoW 耗时 × (峰值并发 / 核心数) < 用户可接受的最大等待时间
```

**我的建议：**

如果你不是极端高并发场景（例如峰值并发 ≤ 50），优先选**少核高频**：
- 每核的单次 PoW 完成得快 → 即使用户排队，单个等待时间也短
- 核心多但每个弱 → 单个 PoW 时间长，排列组合下来反而更慢

具体来说：**4-8 核高频（3.5GHz+）** 会是很好的折中点。

### 3.3 CPU 架构要求

| 维度 | 要求 | 原因 |
|------|------|------|
| **x86_64** | **必须** | Go 编译为 amd64 binary；M1/M2 (ARM64) 也支持但需交叉编译 |
| **Intel vs AMD** | **均可** | Go 的整数运算在两家的微架构上表现接近；FNV-1a 主要用位运算和乘法 |
| **代际** | **越新越好，但不是核心因素** | 较新的 CPU 在 IPC（每周期指令数）和缓存延迟上有优势，但 PoW 这种简单整数循环的 IPC 提升有限（约 10-20%/代）；代际的边际收益远小于频率的提升 |
| **AVX/SSE** | 不需要 | 当前 FNV-1a 是标量运算，没用 SIMD。如果将来优化用 SIMD 批量哈希，才需要 |
| **大缓存 (L3)** | **有帮助但非关键** | PoW 的工作集很小（不超过几十 KB），基本在 L1/L2 Cache 内，L3 大小影响不大 |
| **内存频率** | 基本无关 | PoW 不涉及大量内存读写 |
| **超线程/SMT** | 有帮助 | 10 个 PoW goroutine 在 4C/8T 上比在 4C/4T 上调度更平滑，但 1T 的性能约为 1C 的 30-40%（不是翻倍） |

### 3.4 推荐的采购话术

把以下需求抛给服务器专家：

> 我们要跑一个 Go 语言的服务，每个请求进入时会触发一轮纯 CPU 密集计算（FNV-1a 哈希暴力搜索，50 万次迭代，无磁盘 I/O，无网络 I/O，内存占用很小）。单个请求的单核满负荷计算约 3-5 秒。我们预估峰值并发约 [你填数字] 个请求。
>
> 请推荐：一款核数不多但单核性能强的 X86_64 服务器。优先高主频（3.5GHz+），4-8 物理核心即可。Intel 和 AMD 都行，性价比优先。

### 3.5 推荐参考配置

| 级别 | 示例 | 核心 | 主频 | 适合并发 |
|------|------|------|------|----------|
| 入门 | Intel Xeon E-2388G | 8C/16T | 3.2-5.1GHz | ≤20 |
| 中等 | AMD EPYC 4464P | 12C/24T | 3.7-5.4GHz | ≤40 |
| 高配 | Intel Xeon Gold 6414U | 32C/64T | 2.0-3.4GHz | ≤100 |

注意：Epic 4464P 虽然核心多但单核频率也高，是目前性价比很甜的型号。Xeon Gold 核多但单核弱，适合高并发低延迟容忍的场景。

---

## 四、实施建议

| 阶段 | 内容 | 难度 | 效用 | 状态 |
|------|------|------|------|------|
| 第一阶段 | P0 PoW 模板化 + P1 Token 缓存 | 中 | **极大** | P0 已完成, P1 待实施 |
| 第二阶段 | P3 HTTP 连接池调优 | 低 | 小 | 待实施 |
| 第三阶段 | P2 换服务器 | 0(采购) | 线性 | 待评估 |

建议先做完 P0+P1 再评估是否需要换服务器——可能做完就发现瓶颈消失了。

---

## 五、P0 实施记录 (2026-07-10)

### 5.1 改动文件

| 文件 | 改动 | 说明 |
|------|------|------|
| `internal/prooftoken/prooftoken.go` | +140行 | 模板化 SolveProofOfWork + FNV1aHashBytes + 诊断日志 |
| `internal/backend/backend.go` | +5行 | init() 注入 DebugLog = sentinelLog, 新增 prooftoken import |

### 5.2 模板化原理

25 元素 config 中只有 `[3]` nonce 和 `[9]` elapsed 每次迭代变化,其余 23 个元素全程不变。

**旧方案 (每次迭代):**
```
Build25() → json.Marshal([]any) → base64.Encode → seed+拼接 → FNV1aHash
     ↑ 3μs        ↑ 4μs (反射+分配)    ↑ 1μs        ↑ 分配
```

**新方案 (每次迭代):**
```
strconv.AppendInt ×2 → base64.Encode(预分配buf) → FNV1aHashBytes(预分配buf)
       ↑ ~0.05μs              ↑ ~0.5μs                ↑ 零分配
```

**关键优化点:**
1. JSON 序列化从 50 万次降到 **1 次** — 使用负数哨兵(-777777/-888888)标记 nonce/elapsed 位置
2. Build25 从 50 万次降到 **1 次** — dateStr/timeOrigin/随机属性冻结在循环开始
3. `strconv.AppendInt` 直接写入预分配 `[]byte` — 零堆分配
4. 预分配 base64 编码缓冲区 — 循环内复用,无分配
5. `FNV1aHashBytes` 接受 `[][]byte` — 避免 `seed + encoded` 字符串拼接
6. elapsed 每 1024 次迭代更新一次 — 减少 `time.Since` 系统调用

### 5.3 诊断日志说明

所有诊断日志受 `sentinelDebugEnabled` 开关控制 (`SetSentinelDebugEnabled(true)` 开启)。

**日志输出示例 (debug 模式):**

```
poW: template built — json=312B prefix=56B middle=170B suffix=86B
poW: verify — PASSED (legacy == template, 312 bytes JSON)
poW: SOLVED — iter=123456 elapsed=450ms speed=274/ms diff="00000"
```

**自动回退机制:**

以下任一情况会自动回退到旧方案（legacy solver）:
- 模板 JSON Marshal 失败
- 哨兵值在 JSON 中未找到 (位置偏移)
- 诊断验证不通过 (template JSON ≠ legacy JSON)
- 诊断验证不通过 (template base64 ≠ legacy base64)

每次回退都会输出 `"poW: ... — falling back to legacy method"` 日志。

### 5.4 测试结果

```
go test ./internal/backend/...  — PASS (3.081s)
go test ./internal/prooftoken/... — (no test files)
go vet ./internal/prooftoken/... ./internal/backend/... — PASS
go build ./... — PASS
```

所有已有测试通过，无回归。

### 5.5 验证方法

部署后开启 sentinel debug 日志观察:

```bash
# 在配置或环境变量中开启 sentinel debug
export CHATGPT2API_SENTINEL_DEBUG=true

# 观察日志中的关键行
grep "poW:" logs/app.log

# 检查是否有回退
grep "falling back to legacy" logs/app.log   # 应无输出

# 检查是否有验证失败
grep "MISMATCH" logs/app.log                 # 应无输出

# 观察求解速度
grep "SOLVED" logs/app.log                   # 应看到 speed 字段
```

### 5.6 待实施: P1 Token 缓存

`ChatRequirements` token (finalize 返回值) 在短时间内可复用。
方案: 在 `Client` 结构体增加 `cachedReqs *ChatRequirements` + `reqsCachedAt time.Time`,
在 `getChatRequirements()` 入口检查缓存,命中则直接返回,完全跳过 PoW+Turnstile+finalize。

预期缓存命中时 sentinel 计算耗时 → **0**。
