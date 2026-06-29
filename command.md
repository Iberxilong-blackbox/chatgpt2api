# 启动项目
go run ./internal 


# 重新编译+启动项目
cd web && bun install && bun run build && cd .. && go build -o chatgpt2api.exe ./internal && ./chatgpt2api.exe

# 导入XLSV的数据
go run ./internal/tools/registrationidsxlsx -xlsx "C:\Users\orechi\Documents\好友微信号.xlsx" -out .\registration_identity_ids.json
