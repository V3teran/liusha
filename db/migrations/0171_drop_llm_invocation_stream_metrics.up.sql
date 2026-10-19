-- 0171: 删除 llm_invocation 的流式专属审计列 ttft_ms / is_stream
--
-- 背景：流式 LLM 路径已整体移除（Provider.Stream/StreamEvent/聚合层），
-- 全部调用均为非流式 Complete——is_stream 恒 false、ttft_ms 恒 0，
-- 两列失去信息量（0084 引入它们时的唯一消费者是流式 instrumentation）。

BEGIN;

ALTER TABLE llm_invocation
    DROP COLUMN IF EXISTS ttft_ms,
    DROP COLUMN IF EXISTS is_stream;

COMMIT;
