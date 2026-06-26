# 启动项目
go run ./internal 

# 导入XLSV的数据
go run ./internal/tools/registrationidsxlsx -xlsx "C:\Users\orechi\Documents\好友微信号.xlsx" -out .\registration_identity_ids.json
