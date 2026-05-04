-- 0012: 4 张表 rename 业界最佳实践命名（合并到一个 migration）
--
-- 命名理由：
--   engagement → scan_session    "engagement" 黑客松术语外人难懂；scan_session 直接点明"一次扫描会话"
--   agent_task → react_run       表实际记录每次 ReAct 调用，原名"任务"产生歧义
--   finding    → vuln_finding    "finding" 在金融/调查领域有别义；加 vuln_ 前缀消歧
--   llm_call   → llm_invocation  invocation 是更标准/正式的英语用词（业界更常用）
--
-- PG ALTER TABLE RENAME 自动更新所有 FK constraint 引用。
-- 索引 / 约束名 仍带旧前缀（PG 不自动 rename）；功能不受影响，暂不动。
ALTER TABLE engagement RENAME TO scan_session;
ALTER TABLE agent_task RENAME TO react_run;
ALTER TABLE finding    RENAME TO vuln_finding;
ALTER TABLE llm_call   RENAME TO llm_invocation;
