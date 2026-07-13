# Windows 沙箱补丁工具异常：命令与报错记录

> 记录范围：2026-07-13 本轮“生图调用可观测性改造”开始实施后，所有发生错误的编辑相关命令及紧随其后的替代尝试。  
> 目的：保留可复现证据；不包含 access token、session token 或其他敏感数据。

## 背景

原计划使用环境提供的 `apply_patch` 工具修改工作区文件。该工具在 Windows 的受限令牌沙箱初始化阶段失败，未执行任何文件写入。

## 完整时间顺序

### 1. 首次使用 `apply_patch`

调用方式：通过工具编排执行 `tools.apply_patch(...)`，补丁目标包括：

- 新建 `internal/protocol/image_call_trace.go`；
- 新建 `internal/service/account_trace.go`；
- 修改 `internal/service/account.go`、`internal/protocol/conversation.go`。

结果：失败，补丁未落盘。

完整报错：

```text
apply_patch verification failed: Failed to read file to update
C:\My_project\chatgpt2api\internal\service\account.go:
failed to prepare fs sandbox:
failed to prepare windows sandbox wrapper:
windows unelevated restricted-token sandbox cannot enforce split writable root sets directly;
refusing to run unsandboxed
```

判断：这是文件系统沙箱包装器初始化失败，不是补丁上下文冲突，也不是目标文件不存在。

### 2. 替代尝试：使用 `git apply`，但错误地传入 `apply_patch` 格式

在获得受控写入批准后，执行的命令结构为：

```powershell
@'
*** Begin Patch
...
*** End Patch
'@ | git apply --whitespace=nowarn
```

结果：失败，未落盘。

完整报错：

```text
error: No valid patches in input (allow with "--allow-empty")
```

原因：`git apply` 只接受标准 unified diff，而 `*** Begin Patch` / `*** Update File` 是 `apply_patch` 的专用格式。

### 3. 替代尝试：使用标准 unified diff 新建 `image_call_trace.go`

命令结构：

```powershell
@'
diff --git a/internal/protocol/image_call_trace.go b/internal/protocol/image_call_trace.go
new file mode 100644
--- /dev/null
+++ b/internal/protocol/image_call_trace.go
@@ -0,0 +1,58 @@
...
'@ | git apply --whitespace=nowarn
```

结果：成功。

说明：这确认工作区允许受控的 `git apply` 写入；此前失败来自工具格式和沙箱包装器，而非仓库权限。

### 4. 替代尝试：标准 unified diff 新建 `account_trace.go`，hunk 行数错误

首次命令的 hunk 头为：

```text
@@ -0,0 +1,47 @@
```

结果：失败。

完整报错：

```text
error: corrupt patch at line 51
```

### 5. 重试：调整 hunk 行数为 51

重试 hunk 头：

```text
@@ -0,0 +1,51 @@
```

结果：失败。

完整报错：

```text
error: corrupt patch at line 51
```

### 6. 重试：调整 hunk 行数为 49

重试 hunk 头：

```text
@@ -0,0 +1,49 @@
```

结果：失败。

完整报错：

```text
error: corrupt patch at line 51
```

### 7. 只读诊断：计算 `account_trace.go` 内容的实际行数

执行命令的核心逻辑：

```powershell
$p = @'
... account_trace.go 的完整内容 ...
'@
($p -split "`n").Count
```

结果：成功，输出：

```text
45
```

用途：确认前面三次失败的直接原因是 unified diff hunk 声明行数与实际内容不一致。

### 8. 最终重试：使用正确的 hunk 行数 45

最终 hunk 头：

```text
@@ -0,0 +1,45 @@
```

结果：成功。

## 当前结论

1. `apply_patch` 在本 Windows 沙箱会因受限令牌无法处理分离可写根目录而失败。
2. 经用户批准后，标准 unified diff 通过 `git apply` 可以安全写入当前工作区。
3. `git apply` 使用时必须：
   - 使用 unified diff；
   - 保证每个 hunk 的行数准确；
   - 在分批应用后检查 `git diff`，避免覆盖用户已有改动。
4. 本轮没有运行 `git reset`、`git checkout`、删除命令或其他破坏性命令。

## 后续编辑建议

在该运行环境中，优先顺序建议为：

1. 先尝试 `apply_patch`；
2. 若再次出现同一沙箱初始化错误，记录错误后使用经批准的 `git apply` 标准 unified diff；
3. 大补丁拆分为小补丁，并先计算/校验 hunk 行数；
4. 每批完成后执行只读 `git diff --check` 与相关测试。
