-- 0017: v1.3 命名整改 — 让数据库 schema 与 agentic 哲学一致
--
-- 改动一览（全部纯 rename / drop column，不改语义、无数据迁移）：
--
-- 表名：
--   flow_decision   → flow_facts        （v1.3 起本表只存事实，不再做决策）
--   react_run       → agent_run         （ReAct 是实现细节，agent 才是稳定抽象）
--   vuln_finding    → finding           （只此一种 finding，前缀冗余）
--
-- 列名：
--   flow_facts.attack_surfaces        → param_locations   （只是参数位置，与"攻击面"无关）
--   host_lesson.payload               → structured_payload（区别于 graph 表 payload）
--   llm_invocation.role               → call_purpose      （与 OpenAI message.role 区分）
--   llm_invocation.messages_json      → messages          （列已是 jsonb，去 _json 后缀）
--   llm_invocation.result_json        → result            （同上）
--
-- 删列：
--   flow_facts.required_skills        — v1.3 已停止写入，路由决策回归主 ReAct
--
-- 索引/外键约束名也跟着 rename，保持「约束名前缀=表名」的一致性。
-- 旧索引/约束名是历次 migration 累积下来的（如 agent_task_pkey 来自更老的表名），
-- 这次一次性归一，让 schema 名实相符。

-- ===== 1. flow_decision → flow_facts（含列改名 + 删 required_skills） =====
ALTER TABLE flow_decision RENAME COLUMN attack_surfaces TO param_locations;
ALTER TABLE flow_decision DROP COLUMN required_skills;
ALTER TABLE flow_decision RENAME TO flow_facts;
ALTER INDEX flow_decision_engagement_idx RENAME TO flow_facts_engagement_idx;
ALTER INDEX flow_decision_flow_idx RENAME TO flow_facts_flow_idx;
ALTER SEQUENCE flow_decision_id_seq RENAME TO flow_facts_id_seq;

-- ===== 2. react_run → agent_run =====
ALTER TABLE react_run RENAME TO agent_run;
ALTER INDEX agent_task_pkey RENAME TO agent_run_pkey;
ALTER INDEX agent_task_engagement_idx RENAME TO agent_run_engagement_idx;
ALTER TABLE agent_run RENAME CONSTRAINT agent_task_status_check TO agent_run_status_check;
ALTER TABLE agent_run RENAME CONSTRAINT agent_task_engagement_id_fkey TO agent_run_engagement_id_fkey;

-- ===== 3. vuln_finding → finding =====
ALTER TABLE vuln_finding RENAME TO finding;

-- ===== 4. host_lesson 列改名 =====
ALTER TABLE host_lesson RENAME COLUMN payload TO structured_payload;

-- ===== 5. llm_invocation 列改名 =====
ALTER TABLE llm_invocation RENAME COLUMN role TO call_purpose;
ALTER TABLE llm_invocation RENAME COLUMN messages_json TO messages;
ALTER TABLE llm_invocation RENAME COLUMN result_json TO result;
