-- 0028: 命名清理 + finding.summary 形态收紧
--
-- 背景（本会话审计发现）：
--   1. agent_run.skill 是 v0024 单层 hunter 架构前的遗留——单层后 role 总等于 skill
--      （DB 实测 'hunter'/'hunter' 100% 命中），字段冗余。
--   2. llm_invocation 表已从 llm_call 改名，但 sequence / pkey / engagement_idx /
--      engagement_id_fkey 4 处仍带旧前缀 `llm_call_`，命名不一致难维护。
--   3. finding.summary 设计本意是 "git commit subject 风格的一行短标题"
--      （第一行 ≤72 chars 当 UI label），但 LLM 实际写入 1k+ 字符长文，
--      与 evidence jsonb 的结构化内容事实重复。加 DDL check 强制单行 + 长度上限，
--      让 LLM 收到 DB 报错时下一步主动收敛（配合 WriteFinding tool description 改写）。

-- 1. agent_run.skill 删字段
ALTER TABLE agent_run DROP COLUMN skill;

-- 2. llm_invocation 命名残留 rename（不影响数据）
ALTER SEQUENCE llm_call_id_seq RENAME TO llm_invocation_id_seq;
ALTER INDEX    llm_call_pkey   RENAME TO llm_invocation_pkey;
ALTER INDEX    llm_call_engagement_idx RENAME TO llm_invocation_engagement_idx;
ALTER TABLE    llm_invocation
    RENAME CONSTRAINT llm_call_engagement_id_fkey TO llm_invocation_engagement_id_fkey;

-- 3. finding.summary 收紧：单行（无换行）+ 长度 ≤ 500
--    500 给 LLM 留余量写 "type — path — 核心机理"；超出说明又在写报告了。
ALTER TABLE finding
    ADD CONSTRAINT finding_summary_one_line CHECK (summary !~ E'\\n'),
    ADD CONSTRAINT finding_summary_max_500  CHECK (length(summary) <= 500);
