# sqlc 使用指南

本项目使用 [sqlc](https://sqlc.dev/) 生成类型安全的数据库访问代码。

## 为什么使用 sqlc？

✅ **编译时类型安全** - SQL 错误在编译时发现  
✅ **防止 SQL 注入** - 自动使用预编译语句（Prepared Statements）  
✅ **性能最佳** - 生成的是标准 `database/sql` 代码  
✅ **易于审查** - 生成的代码清晰可读  
✅ **开发体验好** - 写 SQL，自动生成 Go 代码  

## 安装

```bash
# macOS
brew install sqlc

# Go
go install github.com/sqlc-dev/sqlc/cmd/sqlc@latest

# 验证安装
sqlc version
```

## 目录结构

```
liusha/
├── sqlc.yaml              # sqlc 配置文件
├── sql/
│   ├── schema/            # 数据库 schema（给 sqlc 参考）
│   │   └── task.sql
│   └── queries/           # SQL 查询定义
│       └── task.sql
└── internal/
    └── sqlc/              # 生成的 Go 代码（自动生成，不要手动编辑）
        ├── db.go
        ├── models.go
        └── task.sql.go
```

## 工作流程

### 1. 编写 SQL 查询

在 `sql/queries/` 目录下创建 `.sql` 文件：

```sql
-- sql/queries/task.sql

-- name: GetTaskByID :one
SELECT * FROM task WHERE id = $1;

-- name: ListTasks :many
SELECT * FROM task ORDER BY created_at DESC LIMIT $1;

-- name: CreateTask :one
INSERT INTO task (id, mode, target_host, status, created_at, updated_at)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: UpdateTaskStatus :exec
UPDATE task SET status = $2, updated_at = $3 WHERE id = $1;
```

**注释格式**：
- `-- name: <函数名> :<返回类型>`
- 返回类型：`:one` (单行), `:many` (多行), `:exec` (执行), `:execrows` (受影响行数)

### 2. 生成 Go 代码

```bash
# 在项目根目录运行
sqlc generate

# 或者监听文件变化自动生成
sqlc generate --watch
```

生成的代码在 `internal/sqlc/` 目录。

### 3. 使用生成的代码

```go
package main

import (
    "context"
    "database/sql"
    "time"

    "github.com/V3teran/liusha/internal/sqlc"
    _ "github.com/jackc/pgx/v5/stdlib"
)

func main() {
    // 连接数据库
    db, err := sql.Open("pgx", "postgres://user:pass@localhost/liusha")
    if err != nil {
        panic(err)
    }
    defer db.Close()

    // 创建 queries 实例
    queries := sqlc.New(db)
    ctx := context.Background()

    // ✅ 类型安全的查询
    task, err := queries.GetTaskByID(ctx, "task-123")
    if err != nil {
        panic(err)
    }
    println(task.Brief)

    // ✅ 自动参数绑定（防止 SQL 注入）
    tasks, err := queries.ListTasks(ctx, sqlc.ListTasksParams{
        Limit:  10,
        Offset: 0,
    })

    // ✅ 创建任务
    newTask, err := queries.CreateTask(ctx, sqlc.CreateTaskParams{
        ID:         "task-456",
        Mode:       "passive",
        TargetHost: "example.com",
        Status:     "pending",
        CreatedAt:  time.Now(),
        UpdatedAt:  time.Now(),
    })

    // ✅ 更新状态
    err = queries.UpdateTaskStatus(ctx, sqlc.UpdateTaskStatusParams{
        ID:        "task-456",
        Status:    "running",
        UpdatedAt: time.Now(),
    })
}
```

## 最佳实践

### 1. 事务支持

```go
// 使用事务
tx, err := db.BeginTx(ctx, nil)
if err != nil {
    return err
}
defer tx.Rollback()

// 传入 tx 而不是 db
qtx := queries.WithTx(tx)

// 在事务中执行多个操作
err = qtx.UpdateTaskStatus(ctx, ...)
if err != nil {
    return err
}

err = qtx.CreateTask(ctx, ...)
if err != nil {
    return err
}

// 提交事务
return tx.Commit()
```

### 2. 复杂查询

```sql
-- name: GetTasksWithFindings :many
SELECT 
    t.id,
    t.brief,
    COUNT(f.id) as finding_count
FROM task t
LEFT JOIN finding f ON f.task_id = t.id
WHERE t.status = $1
GROUP BY t.id, t.brief
ORDER BY finding_count DESC
LIMIT $2;
```

### 3. 可选参数

```sql
-- name: SearchTasks :many
SELECT * FROM task
WHERE 
    ($1::text IS NULL OR target_host ILIKE '%' || $1 || '%')
    AND ($2::text IS NULL OR status = $2)
ORDER BY created_at DESC;
```

### 4. 批量插入

```sql
-- name: BatchInsertFindings :copyfrom
INSERT INTO finding (
    id, task_id, title, severity, created_at
) VALUES (
    $1, $2, $3, $4, $5
);
```

```go
// 使用 CopyFrom 进行批量插入（高性能）
findings := []sqlc.BatchInsertFindingsParams{
    {ID: "f1", TaskID: "t1", Title: "XSS", Severity: "high", CreatedAt: time.Now()},
    {ID: "f2", TaskID: "t1", Title: "SQLi", Severity: "critical", CreatedAt: time.Now()},
}
count, err := queries.BatchInsertFindings(ctx, findings)
```

## 与现有代码集成

### 逐步迁移策略

1. **新功能优先使用 sqlc**
   ```go
   // ✅ 新代码
   task, err := queries.GetTaskByID(ctx, taskID)
   ```

2. **重构时迁移旧代码**
   ```go
   // ❌ 旧代码（手写 SQL）
   row := db.QueryRow("SELECT * FROM task WHERE id = $1", taskID)
   
   // ✅ 迁移后
   task, err := queries.GetTaskByID(ctx, taskID)
   ```

3. **保持接口不变**
   ```go
   // 在现有 Repository 中使用 sqlc
   type TaskRepository struct {
       queries *sqlc.Queries
   }
   
   func (r *TaskRepository) GetByID(ctx context.Context, id string) (*Task, error) {
       sqlcTask, err := r.queries.GetTaskByID(ctx, id)
       if err != nil {
           return nil, err
       }
       return toTask(sqlcTask), nil
   }
   ```

## 常见问题

### Q: 生成的代码可以修改吗？
**A**: 不可以。生成的代码每次运行 `sqlc generate` 都会重新生成。如需自定义逻辑，在生成代码外层包装。

### Q: 如何处理 NULL 值？
**A**: sqlc 会自动使用 `sql.NullString`, `sql.NullInt64` 等类型。

```sql
-- nullable 字段
CREATE TABLE task (
    id TEXT PRIMARY KEY,
    brief TEXT  -- 可以为 NULL
);
```

```go
// 生成的结构
type Task struct {
    ID    string
    Brief sql.NullString  // 自动处理 NULL
}

// 使用
if task.Brief.Valid {
    println(task.Brief.String)
}
```

### Q: 性能如何？
**A**: sqlc 生成的代码就是标准 `database/sql`，性能与手写相同。预编译语句会被数据库缓存，重复查询更快。

### Q: 支持哪些数据库？
**A**: PostgreSQL（推荐）、MySQL、SQLite。本项目使用 PostgreSQL。

## 参考资源

- [sqlc 官方文档](https://docs.sqlc.dev/)
- [sqlc GitHub](https://github.com/sqlc-dev/sqlc)
- [示例项目](https://github.com/sqlc-dev/sqlc/tree/main/examples)

## 下一步

1. 安装 sqlc: `brew install sqlc` 或 `go install github.com/sqlc-dev/sqlc/cmd/sqlc@latest`
2. 查看示例: `sql/queries/task.sql`
3. 生成代码: `sqlc generate`
4. 在新功能中使用生成的代码

---

**注意**: `internal/sqlc/` 目录下的文件是自动生成的，不要手动编辑。如需修改，编辑 `sql/queries/` 下的 SQL 文件，然后重新运行 `sqlc generate`。
