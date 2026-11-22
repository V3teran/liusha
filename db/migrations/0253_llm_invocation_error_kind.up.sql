-- LLM 调用的错误类别是封闭词表（llminvocation.ErrorKinds，成功为空串），审计页按它区分限流、
-- 服务端、网络与其它错误；库里补上约束。存量的词表外取值归入 error。
UPDATE llm_invocation SET error_kind = 'error'
WHERE error_kind NOT IN ('', 'rate_limited', 'server', 'network', 'error');
ALTER TABLE llm_invocation ADD CONSTRAINT llm_invocation_error_kind_check
    CHECK (error_kind IN ('', 'rate_limited', 'server', 'network', 'error'));
