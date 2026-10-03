-- finding.agent_task_id 是多代归属列改名的孤儿（0052 加→0055 改名→0085 再改名走的是
-- 别的列），NOT NULL 无默认且无任何代码写入——它把 finding 表的全部 INSERT 堵死
-- （evaluator 验证坐实后写 finding 必炸 23502）。
-- 归属语义已由 task_id(FK task) + agent_id(FK agent_run, nullable) 承载，此列纯残留。
DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_name = 'finding' AND column_name = 'agent_task_id'
    ) THEN
        ALTER TABLE finding DROP CONSTRAINT IF EXISTS finding_task_id_fkey;
        ALTER TABLE finding DROP COLUMN agent_task_id;
    END IF;
END $$;
