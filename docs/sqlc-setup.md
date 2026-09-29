# sqlc 安装和配置

本项目已配置 sqlc，但需要手动安装工具。

## 快速安装

```bash
# macOS
brew install sqlc

# 或使用 Go
go install github.com/sqlc-dev/sqlc/cmd/sqlc@latest

# 验证安装
sqlc version
```

## 生成代码

安装后运行：

```bash
sqlc generate
```

这会在 `internal/sqlc/` 目录生成类型安全的数据库访问代码。

## 文档

完整使用指南见: [docs/sqlc-guide.md](./sqlc-guide.md)

## 当前状态

✅ 配置文件已创建: `sqlc.yaml`  
✅ 示例 schema: `sql/schema/task.sql`  
✅ 示例 queries: `sql/queries/task.sql`  
⚠️  需要安装 sqlc 工具  
⚠️  新功能优先使用 sqlc  
