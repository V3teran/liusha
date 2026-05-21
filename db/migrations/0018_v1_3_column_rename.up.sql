-- 0018: v1.3 列名整改 — 跨表外键列名一致 + 时间列统一 + 含糊列改名
--
-- 本次只动列名/约束名，零数据迁移。
--
-- 改动一览：
--   1. 外键列 task_id → agent_run_id（与 0017 表名 agent_run 一致）：
--      - flow_facts.task_id          → agent_run_id
--      - finding.task_id             → agent_run_id
--      - llm_invocation.task_id      → agent_run_id
--   2. engagement.react_run_count    → agent_run_count
--   3. 时间列统一 *_at：http_flow.ts → created_at
--   4. 含糊列改名：llm_invocation.error → error_message（与 engagement.error_message 一致）
--   5. 外键约束名 / 索引名跟齐 task_id → agent_run_id

-- ===== 1. flow_facts =====
ALTER TABLE flow_facts RENAME COLUMN task_id TO agent_run_id;
ALTER TABLE flow_facts RENAME CONSTRAINT flow_decision_task_id_fkey TO flow_facts_agent_run_id_fkey;

-- ===== 2. finding =====
ALTER TABLE finding RENAME COLUMN task_id TO agent_run_id;
ALTER TABLE finding RENAME CONSTRAINT finding_task_id_fkey TO finding_agent_run_id_fkey;

-- ===== 3. llm_invocation =====
ALTER TABLE llm_invocation RENAME COLUMN task_id TO agent_run_id;
ALTER TABLE llm_invocation RENAME CONSTRAINT llm_call_task_id_fkey TO llm_invocation_agent_run_id_fkey;
ALTER INDEX llm_call_task_idx RENAME TO llm_invocation_agent_run_idx;
ALTER TABLE llm_invocation RENAME COLUMN error TO error_message;

-- ===== 4. engagement =====
ALTER TABLE engagement RENAME COLUMN react_run_count TO agent_run_count;

-- ===== 5. http_flow =====
ALTER TABLE http_flow RENAME COLUMN ts TO created_at;
