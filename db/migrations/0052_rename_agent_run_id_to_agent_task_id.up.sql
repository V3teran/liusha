-- 0052: finding / llm_invocation 表的 agent_run_id 列重命名为 agent_task_id。
-- v1.1 重构把 agent_run 表 rename 为 agent_task（0050/0051 已完成），但外键列名一直
-- 滞留旧名 agent_run_id。tool_invocation 上次重构时已经改过，本次补齐剩余两表。
--
-- 同步 Go 侧：
--   - internal/finding/store.go：colsSelect / INSERT SQL 用新列名
--   - internal/llminvocation/store.go：copyFrom batch / SELECT SQL 用新列名
--   - 外部 API JSON / 前端已在 commit af10c79 切到 agent_task_id（无需再动）
--
-- 不动：
--   - internal/notes/store.go Redis 持久化字段（破坏已写入 notes 的 backward compat）
--   - internal/tools/common/note.go 写入 note 时的 "agent_run_id" 字段（同上）

ALTER TABLE finding RENAME COLUMN agent_run_id TO agent_task_id;
ALTER TABLE llm_invocation RENAME COLUMN agent_run_id TO agent_task_id;
