-- 0017 down: 回滚 v1.3 命名整改
--
-- 逆操作：表名/列名/索引/约束/序列名按 0017 up 反向 rename。
-- 唯一不可完全无损回滚的是 flow_facts 已 DROP 的 required_skills 列——
-- 这里重建为空 jsonb 列（不恢复历史值；本来 v1.3 就停止写入了）。

-- ===== 5. llm_invocation 列改回 =====
ALTER TABLE llm_invocation RENAME COLUMN result TO result_json;
ALTER TABLE llm_invocation RENAME COLUMN messages TO messages_json;
ALTER TABLE llm_invocation RENAME COLUMN call_purpose TO role;

-- ===== 4. host_lesson 列改回 =====
ALTER TABLE host_lesson RENAME COLUMN structured_payload TO payload;

-- ===== 3. finding → vuln_finding =====
ALTER TABLE finding RENAME TO vuln_finding;

-- ===== 2. agent_run → react_run =====
ALTER TABLE agent_run RENAME CONSTRAINT agent_run_engagement_id_fkey TO agent_task_engagement_id_fkey;
ALTER TABLE agent_run RENAME CONSTRAINT agent_run_status_check TO agent_task_status_check;
ALTER INDEX agent_run_engagement_idx RENAME TO agent_task_engagement_idx;
ALTER INDEX agent_run_pkey RENAME TO agent_task_pkey;
ALTER TABLE agent_run RENAME TO react_run;

-- ===== 1. flow_facts → flow_decision（含列改回 + 重建 required_skills） =====
ALTER SEQUENCE flow_facts_id_seq RENAME TO flow_decision_id_seq;
ALTER INDEX flow_facts_flow_idx RENAME TO flow_decision_flow_idx;
ALTER INDEX flow_facts_engagement_idx RENAME TO flow_decision_engagement_idx;
ALTER TABLE flow_facts RENAME TO flow_decision;
ALTER TABLE flow_decision ADD COLUMN required_skills jsonb NOT NULL DEFAULT '[]'::jsonb;
ALTER TABLE flow_decision RENAME COLUMN param_locations TO attack_surfaces;
