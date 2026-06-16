-- 0062 撤回 0061：恢复 http_flow.hunter_id 列（双字段语义 — owner_id 顶层归档 + hunter_id 细粒度可追溯）
--
-- 反思（见 0061 commit + 后续 git log）：删 hunter_id 是过早优化，损失了 active 模式下
-- "哪个 hunter 发的流量"的关键可观测性（orchestrator vs exploitation 不可区分）。业界 hierarchy
-- 实践（K8s labels / OTel trace / Datadog）均要求最细粒度 ID 直接打到流量上，上层维度
-- 通过 join 解析。每流量 1 次 indexed PK 反查 hunter 表 < 1ms，不是真瓶颈。
--
-- 0061 之后写入的 hunter_id 已丢失原值（不可恢复）；新流量按修复后链路填入。

ALTER TABLE http_flow ADD COLUMN hunter_id uuid REFERENCES hunter(id) ON DELETE SET NULL;
CREATE INDEX http_flow_hunter_idx ON http_flow (hunter_id) WHERE hunter_id IS NOT NULL;
