-- 删除 tool_invocation.done 死列：行由遥测拦截器在工具执行完成后一次性插入，
-- 没有任何写路径置 true、没有任何读者——列恒 false 却暗示"未完成"，误导审计。
-- 完成语义由 duration_ms/output_* 非空与行存在本身表达（与 llm_invocation 单阶段口径一致）。
ALTER TABLE tool_invocation DROP COLUMN IF EXISTS done;
