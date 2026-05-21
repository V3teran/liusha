-- 0044 down: 反向 rename role → call_purpose。

ALTER TABLE llm_invocation RENAME COLUMN role TO call_purpose;
