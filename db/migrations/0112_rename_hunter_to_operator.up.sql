-- 0112/0113 合并净效果：agent.kind 枚举 domain → executor。
-- （原一对 agent→executor→agent 改名迁移互相抵消且引用了不存在的约束名，
--   已按最终 schema 重写为单一净效果；表名与列名在本链上从未变过。）

ALTER TABLE agent DROP CONSTRAINT agent_kind_check;

UPDATE agent SET kind = 'executor' WHERE kind = 'domain';

ALTER TABLE agent ADD CONSTRAINT agent_kind_check
    CHECK (kind IN ('planner', 'executor'));
