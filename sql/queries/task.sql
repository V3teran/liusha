-- sql/queries/task.sql
-- 任务相关的查询（示例）

-- name: GetTaskByID :one
-- 根据 ID 获取任务
SELECT * FROM task
WHERE id = $1;

-- name: ListTasks :many
-- 列出所有任务
SELECT * FROM task
ORDER BY created_at DESC
LIMIT $1 OFFSET $2;

-- name: ListTasksByStatus :many
-- 根据状态列出任务
SELECT * FROM task
WHERE status = $1
ORDER BY created_at DESC;

-- name: CreateTask :one
-- 创建新任务
INSERT INTO task (
    id, mode, target_host, target_port, brief, status, created_at, updated_at
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8
)
RETURNING *;

-- name: UpdateTaskStatus :exec
-- 更新任务状态
UPDATE task
SET status = $2, updated_at = $3
WHERE id = $1;

-- name: DeleteTask :exec
-- 删除任务
DELETE FROM task
WHERE id = $1;

-- name: CountTasksByStatus :one
-- 统计指定状态的任务数量
SELECT COUNT(*) FROM task
WHERE status = $1;
