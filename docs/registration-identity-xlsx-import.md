# 注册身份 ID 白名单 XLSX 导入

注册白名单的运行数据存储在 `json_documents` 表中，文档名是 `registration_identity_ids.json`。该工具用于把 XLSX 中的一列 `identity_id` 转成同名 JSON 文档，方便管理员批量维护。

## XLSX 格式

- 默认读取第一个工作表的 `A` 列。
- 每个非空单元格是一条 `identity_id`。
- 如果第一行是表头，使用 `-skip-header` 跳过。

## 只生成 JSON

```powershell
go run ./internal/tools/registrationidsxlsx -xlsx .\ids.xlsx -out .\registration_identity_ids.json -skip-header
```

常用参数：

- `-sheet IDs`：指定工作表名称。
- `-column B`：指定读取列。
- `-existing .\registration_identity_ids.json`：合并已有 JSON，保留已使用记录，只新增不存在的 ID。
- `-label "2026-06 batch"`：为新增 ID 设置统一备注。

## 直接写入数据库

默认本地 SQLite 可使用项目自己的 storage backend 写入，避免手写 SQL 转义：

```powershell
go run ./internal/tools/registrationidsxlsx `
  -xlsx .\ids.xlsx `
  -skip-header `
  -database-url "sqlite:///C:/My_project/chatgpt2api/data/chatgpt2api.db" `
  -out .\registration_identity_ids.json
```

PostgreSQL/MySQL 部署也可以传当前服务使用的 `DATABASE_URL`：

```powershell
go run ./internal/tools/registrationidsxlsx -xlsx .\ids.xlsx -skip-header -database-url $env:DATABASE_URL
```

如果要保留线上已有的已使用记录，先从后台或数据库导出现有 `registration_identity_ids.json`，再通过 `-existing` 合并后写入。

## 注意事项

- 已使用的身份 ID 不会被释放，合并时会保留原有绑定信息。
- 重复 `identity_id` 会跳过，不会覆盖原记录。
- 控制字符、空值、超过 256 字符的 ID 会报错。
- 服务正在运行时直接改数据库有并发风险，建议先停服或避开注册窗口。