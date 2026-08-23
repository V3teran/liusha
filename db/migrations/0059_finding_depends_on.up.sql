-- 删 finding_relation 表 + finding 加 depends_on uuid[] 字段。
--
-- 设计反思（finding_relation 实测从未被使用）：
-- 4+ 次 e2e 跑（v8-v11），planner/exploitation 全程 0 次调用 write_relation。
-- 根因：操作链长（先 read_findings 拿 ID → 再 write_relation），LLM 自然倾向把 chaining
-- 描述写在 finding.summary 里。独立的关系表是过度设计。
--
-- 替代设计（对齐 GitHub Advisory / CVE 的 references[] 模式）：
-- finding.depends_on uuid[] 数组挂在自身——write_finding 时一次性传入：
--   write_finding(summary="组合 RCE", depends_on=[a.id, b.id])
-- LLM 操作链短 40%（5 步 → 3 步），数据集中无需 join。
--
-- projector 投影：遍历 findings.depends_on 派生 chains 边（a→c + b→c），
-- 前端 D3 force-directed 图渲染成虚线弧形。

ALTER TABLE finding ADD COLUMN depends_on uuid[] NOT NULL DEFAULT '{}';

DROP TABLE IF EXISTS finding_relation;
