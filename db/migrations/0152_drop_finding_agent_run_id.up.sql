-- finding.agent_run_id 与 0151 删的 agent_task_id 同源：0074 时代的 task 归属列被
-- 多代改名链（owner→task_id→hunter→agent_run）遗弃成尸——NOT NULL 无默认、
-- 代码零写入，把 finding 的全部 INSERT 堵死（23502）。
-- 现行归属语义：task_id(FK task) + agent_id(FK agent_run, nullable)。
DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_name = 'finding' AND column_name = 'agent_run_id'
    ) THEN
        ALTER TABLE finding DROP CONSTRAINT IF EXISTS finding_task_id_fkey1;
        ALTER TABLE finding DROP COLUMN agent_run_id;
    END IF;
END $$;
