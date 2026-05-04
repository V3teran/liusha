-- 0012 down: 4 表 rename 回原名。
ALTER TABLE scan_session   RENAME TO engagement;
ALTER TABLE react_run      RENAME TO agent_task;
ALTER TABLE vuln_finding   RENAME TO finding;
ALTER TABLE llm_invocation RENAME TO llm_call;
