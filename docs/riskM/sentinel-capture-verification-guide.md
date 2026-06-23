# Sentinel 机制抓包验证与逆向指南

> 当 OpenAI 更新 ChatGPT SentinelSDK 的风控机制时（PoW config 结构变化、新增 dx/VM、新增挑战类型等），
> 按本文档流程抓包 → 分析 → 落代码。可重复使用。

## 一、需要收集的信息

要完整理解一个新的或变更的 Sentinel 机制，需要回答以下问题：

| 层面 | 要弄清楚 | 关键信息来源 |
|------|---------|-------------|
| **HTTP 流程** | 哪些端点、请求/响应结构、新增了什么字段 | HAR 文件 |
| **算法细节** | PoW config 结构、hash 算法、VM opcode 语义 | JS 源码 + 运行时 dump |
| **密钥绑定** | 新挑战用什么作为 XOR 密钥（p token? proof token? 其他?） | JS 源码 |
| **结果回传** | VM/挑战的结果通过哪个字段、哪个端点回传给服务端 | HAR + JS 源码 |

---

## 二、抓包方案

### 方案 A：HAR 文件（你负责的部分）

**工具**：Chrome/Edge DevTools → Network 面板 → 导出 HAR

**操作步骤**：

1. 打开浏览器，进入 **无痕模式**（避免缓存和已登录状态干扰）
2. 打开 DevTools（F12）→ **Network** 面板
3. 勾选 **Preserve log**（防止页面跳转时日志清空）
4. 访问 `https://chatgpt.com/`，**不要登录**
5. 等待页面完全加载，随便输入一句话发送
6. 等待回复完成后，在 Network 面板右键 → **Save all as HAR with content**
7. 命名格式：`sentinel_YYYYMMDD_描述.har`（如 `sentinel_20260620_anon_chat.har`）

**HAR 能告诉我们什么**：
- 哪些 sentinel 端点被调用了（`/prepare`、`/finalize`、`/req` 等）
- 请求体结构（各字段名、字段值类型）
- 响应体结构（`proofofwork`、`turnstile`、`so`、`arkose` 等对象的完整 JSON）
- 请求头和响应头
- 请求时序

**HAR 的局限**：
- 看不到 JS 运行时变量（PoW config 数组在 hash 前的具体值、dx 解密后的明文指令、VM 执行中间状态）
- 看不全 SDK 源码（sentinel SDK 可能被压缩/混淆）

### 方案 B：补充 JS 运行时 dump（我来配合的增强方案）

当 HAR 不够时，需要在浏览器中**注入日志**捕获关键运行时值。

#### B1. 定位 Sentinel SDK

在 DevTools → **Sources** 面板中，搜索关键词：
- `getConfig`（PoW config 构建函数）
- `_runCheck`（PoW hash 校验函数）
- `decryptDx` 或 `Pn`（dx 解密入口）
- `zvt`（FNV hash 函数名）

#### B2. 手动断点 dump（快速方案）

在关键函数处设断点，然后在 Console 中执行：

```javascript
// 在 getConfig 断点处，dump config 数组
copy(JSON.stringify(getConfig()))  // 复制到剪贴板

// 在 _runCheck 断点处，dump 中间值
console.log("seed:", seed)
console.log("difficulty:", difficulty)  
console.log("nonce:", nonce)
console.log("fp:", JSON.stringify(fp))
console.log("base64fp:", base64fp)
console.log("hash:", hash)

// 在 decryptDx/Pn 断点处，dump dx 解密过程
console.log("raw dx:", dx_string)
console.log("decrypted:", plaintext)  // XOR 解密后的明文指令
console.log("instructions:", JSON.stringify(instructions))
```

#### B3. 注入覆盖脚本（可重复方案）

在浏览器中加载一个 Tampermonkey 脚本或在 Console 中注入一段代码，拦截关键函数：

```javascript
// 注入到页面，监控 sentinel 关键函数
(function() {
    // 等待 sentinel SDK 加载
    const interval = setInterval(() => {
        // 尝试找到 SDK 暴露的对象（具体变量名需根据实际 SDK 调整）
        const sentinel = window.__SENTINEL__ || window.sentinel;
        if (!sentinel) return;
        clearInterval(interval);

        // Hook PoW
        const origGetConfig = sentinel.getConfig;
        sentinel.getConfig = function() {
            const cfg = origGetConfig.apply(this, arguments);
            console.log('[CAPTURE] getConfig:', JSON.stringify(cfg));
            return cfg;
        };

        // Hook _runCheck
        const origRunCheck = sentinel._runCheck;
        sentinel._runCheck = function() {
            const result = origRunCheck.apply(this, arguments);
            console.log('[CAPTURE] _runCheck args:', JSON.stringify(arguments));
            console.log('[CAPTURE] _runCheck result:', result);
            return result;
        };
        
        console.log('[CAPTURE] Sentinel hooks installed');
    }, 100);
})();
```

> **注意**：具体变量名取决于 SDK 的混淆程度，需要先阅读 SDK 源码确定切入点。

---

## 三、分析流程

拿到 HAR 和 JS dump 数据后，按以下步骤分析：

### Step 1：识别端点变化

在 HAR 中搜索 `sentinel` 关键字，列出所有涉及的 HTTP 请求：

```
端点清单：
├── POST chatgpt.com/backend-anon/sentinel/chat-requirements/prepare
│   ├── 请求体: {"p": "gAAAAAC..."}
│   └── 响应体: {prepare_token, proofofwork, turnstile, so, arkose, expire_after}
├── POST chatgpt.com/backend-anon/sentinel/chat-requirements/finalize
│   ├── 请求体: {prepare_token, proofofwork, turnstile, so}
│   └── 响应体: {token, so_token, ...}
└── ...
```

对比代码中已有的端点路径，标记新增或变更的。

### Step 2：提取响应结构

对每个端点响应，展开 JSON，**逐字段列出**：

```json
// prepare 响应
{
  "prepare_token": "string",
  "proofofwork": {
    "required": true,
    "seed": "string",
    "difficulty": "string"     // 如 "061a80"
  },
  "turnstile": {
    "required": true,
    "dx": "base64_string",       // XOR 加密的 Turnstile 指令
    "snapshot_dx": "base64_string"  // ??
  },
  "so": {
    "required": true,
    "collector_dx": "base64_string"  // XOR 加密的 Sentinel dx 指令
  },
  "arkose": {
    "required": false
  },
  "expire_after": 3600
}
```

**对每个新出现的字段**，问三个问题：
1. 它是挑战输入还是服务端返回？（决定我们要**解密它**还是**生成它**）
2. 它用什么做 XOR 密钥？（决定密钥来源）
3. 处理结果要发回哪里？（决定结果回传位置）

### Step 3：提取 PoW Config 结构

如果有 `getConfig()` 的 dump，直接拿到完整数组。如果没有，从 JS 源码中找。

拿到后逐索引对比当前代码的 `buildPOWConfig`：

| 索引 | HAR/源码中的值 | 当前代码值 | 是否匹配 |
|------|---------------|-----------|---------|
| 0 | ... | ... | ✅/❌ |
| ... | ... | ... | ... |

### Step 4：提取 VM 指令

如果有 `decryptDx` 的 dump，直接拿到解密后的 `[][]any` 指令数组。分析每条指令的 opcode 和参数，判断是否需要新增 opcode 支持。

### Step 5：验证密钥绑定

确认新挑战的 XOR 密钥来源：

```
挑战类型 → 密钥来源
├── turnstile.dx → legacy p token ("gAAAAAC...")
├── so.collector_dx → proof token ("gAAAAAB..." 的裸 base64)
└── 新字段 → ???
```

在 JS 源码中追踪：谁会读取 `cachedProof`（WeakMap 中的 PoW 答案）作为 XOR 密钥参数。

---

## 四、从分析到代码改动的映射

| 发现 | 对应代码改动 |
|------|-------------|
| Prepare 响应出现新的 `xxx.dx` 字段 | `backend.go` 的 `buildRequirements` → 新增解密调用 |
| PoW config 数组结构变化 | `pow.go` 的 `buildPOWConfig` → 更新索引语义 |
| PoW hash 算法有变体 | `pow.go` 的 `zvtHash` → 可能需要新 hash 函数 |
| VM 出现新 opcode | `sentinel_dx.go` 或 `turnstile.go` → 新增 opcode handler |
| Finalize 请求需要新字段 | `backend.go` 的 `getChatRequirements` → finalize payload 加字段 |
| 新密钥来源 | 新增类似 `rawProofAnswer` 的密钥提取函数 |
| 出现全新的挑战类型（非 dx/非 turnstile） | 可能需要在 `buildRequirements` 中新增完整的挑战处理分支 |

---

## 五、你需要提供给我的信息

每次发现新的风控变化时，提供以下内容即可：

### 最小集（足够开始分析）

```
1. HAR 文件（含 response content）
2. 访问方式：匿名 or 登录
3. 触发了什么操作：刚打开页面 / 发送了对话 / 生图
```

### 完整集（能加速分析）

```
4. 在 Console 中 dump 的 getConfig() 数组
5. 在 Sources 中找到的 sentinel SDK JS 文件路径
6. 是否有任何错误或风控页面出现
```

### 不需要的

- 不需要视频/截图（HAR 已经包含了请求内容）
- 不需要修改浏览器设置（默认 Chrome/Edge 即可）
- 不需要安装额外工具

---

## 六、示例：一次完整的抓包验证流程

以验证 `so.collector_dx` 为例：

```
[你] 打开 Chrome 无痕 → DevTools Network → 访问 chatgpt.com → 发送一句话 → 导出 HAR
[你] 把 HAR 文件发给我

[我] 1. 搜索 HAR 中的 "so" 关键词
[我] 2. 找到 prepare 响应，确认是否存在 so.collector_dx 字段
[我] 3. 如果有：提取 dx 值
[我] 4. 在代码中对照：buildRequirements 是否处理了这个字段
[我] 5. 在代码中对照：finalize payload 是否包含 "so" 字段
[我] 6. 如果有 gap → 编写代码填补
[我] 7. 如果 JS dump 中也有 dx 解密后的明文指令 → 验证 VM 执行结果
```

---

## 七、后续机制变更的快速定位

当 OpenAI 更新风控时，按以下 checklist 检查我们的代码：

```
□ HAR 中有没有新的 sentinel 端点路径？
  → 搜索 "sentinel" in HAR
  → 对比代码中已有的路径

□ prepare 响应中有没有新的子对象？
  → 展开 prepare 响应 JSON
  → 对比 backend.go buildRequirements 中处理的字段（proofofwork / turnstile / so / arkose）

□ finalize 请求中是否新增了字段？
  → 展开 finalize 请求 JSON
  → 对比 backend.go getChatRequirements 中 finalizePayload 的字段

□ PoW config 数组结构是否变化？
  → JS dump getConfig() 返回的 JSON
  → 对比 pow.go buildPOWConfig 返回的数组

□ 是否有新的请求头需要发送？
  → 检查 HAR 中 conversation 请求的请求头
  → 对比 backend.go conversationHeaders 设置的请求头
```