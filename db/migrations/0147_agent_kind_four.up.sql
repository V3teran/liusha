-- agent.kind 收敛为四智能体：planner / executor / evaluator / monitor。
-- 'domain' 是 0086 场景时代的残留枚举值，一并移除。
-- 历史库可能存在多个 kind CHECK（自动命名的 agent_kind_check1 等）：
-- 先删除全部旧 kind CHECK，再加唯一的新约束，把漂移一次性收敛。
DO $$
DECLARE
    r record;
BEGIN
    FOR r IN
        SELECT conname
        FROM pg_constraint
        WHERE conrelid = 'agent'::regclass
          AND contype = 'c'
          AND pg_get_constraintdef(oid) LIKE '%kind%'
          AND pg_get_constraintdef(oid) LIKE '%planner%'
    LOOP
        EXECUTE format('ALTER TABLE agent DROP CONSTRAINT %I', r.conname);
    END LOOP;
END $$;

ALTER TABLE agent ADD CONSTRAINT agent_kind_check
    CHECK (kind = ANY (ARRAY['planner'::text, 'executor'::text, 'evaluator'::text, 'monitor'::text]));
