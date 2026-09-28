-- agent 表的 task_id 是运行表时代（0074 多态 owner 坍缩）的遗留列：
-- 0086 重建 agent 为配置表后它失去语义，0126 改造时未清理。
-- 配置表按 code 唯一标识，与具体 task 无关；agent 的运行归属在 agent_run.task_id。
--
-- 历史库（0074→0126 一路升级）才有该列；全新库的 agent 表（0086 重建）本就没有。
-- 防御式写法：列不存在时整段跳过。
DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_name = 'agent' AND column_name = 'task_id'
    ) THEN
        ALTER TABLE agent ALTER COLUMN task_id DROP DEFAULT;
        UPDATE agent SET task_id = NULL
        WHERE task_id IS NOT NULL AND NOT EXISTS (SELECT 1 FROM task WHERE task.id = agent.task_id);
        ALTER TABLE agent ALTER COLUMN task_id DROP NOT NULL;
        ALTER TABLE agent DROP COLUMN task_id;
        DROP INDEX IF EXISTS agent_task_idx;
    END IF;
END $$;
