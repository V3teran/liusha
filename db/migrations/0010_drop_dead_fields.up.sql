-- 0010: 删 6 个深度复审确认的死字段
--
-- 复审依据（grep 生产代码 + 实际 DB 数据双重印证）：
--   1. agent_task.budget          — NewParams.Budget 永远 nil，全部 '{}'
--   2. finding.tool               — BAC builder 不传 tool，全部空字符串
--   3. finding.payload            — evidence 已承载证据，payload 永远 '{}'
--   4. finding.updated_at         — append-only 后永远 = created_at（无 UPDATE 路径）
--   5. engagement.last_activity_at — Touch() 函数无人调；仅 Abort 时设一次但无下游消费
--   6. http_flow.request_truncated + response_truncated — proxy 写库前已裁，flag 永远 false
--
-- 不可逆：down 仅恢复 schema 形状（默认值），存量数据无法回滚（也无价值）。
ALTER TABLE agent_task DROP COLUMN budget;

ALTER TABLE finding
    DROP COLUMN tool,
    DROP COLUMN payload,
    DROP COLUMN updated_at;

ALTER TABLE engagement DROP COLUMN last_activity_at;

ALTER TABLE http_flow
    DROP COLUMN request_truncated,
    DROP COLUMN response_truncated;
