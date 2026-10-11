-- 探索图 confidence 语义修复：
--   旧编码：GraphNode.Confidence 浮点存 metadata._confidence（0.0/0.5/1.0 折算
--   refuted/unverified/verified）。写入侧存在两处缺陷——executor 把
--   low/medium/high 写进 unverified/verified/refuted 枚举（适配层全部漏映射，
--   observation 一律落 0.0）；读取侧从不回填该字段，读回值恒错。
--
--   新编码：枚举原文存 metadata.confidence（unverified/verified/refuted）。
--   规则：
--     result      → verified（result 仅由复现门坐实产出）
--     observation → 有审计标记按标记（confirmed→verified / refuted→refuted）；
--                   无标记且 content 带 repro → unverified（待裁决假设）；
--                   其余（执行记录/证据，无 repro）→ 不携带 confidence
--                   （不参与裁决生命周期）
--
--   executor 的自评置信度（low/medium/high）属假设载荷，新代码已随 content 落库；
--   旧数据中该信息在写入侧已丢失，无从恢复。

-- 1. result 节点：恒为 verified
UPDATE exploration_node
SET metadata = COALESCE(metadata, '{}'::jsonb) || '{"confidence": "verified"}'::jsonb
WHERE kind = 'result';

-- 2. observation 节点：按审计标记恢复裁决态
UPDATE exploration_node
SET metadata = COALESCE(metadata, '{}'::jsonb) || jsonb_build_object(
    'confidence',
    CASE metadata->>'verification_outcome'
        WHEN 'confirmed' THEN 'verified'
        ELSE 'refuted'
    END)
WHERE kind = 'observation'
  AND metadata->>'verification_outcome' IN ('confirmed', 'refuted');

-- 3. observation 节点：无裁决标记的待裁决假设（带 repro）
UPDATE exploration_node
SET metadata = COALESCE(metadata, '{}'::jsonb) || '{"confidence": "unverified"}'::jsonb
WHERE kind = 'observation'
  AND NOT COALESCE(metadata, '{}'::jsonb) ? 'verification_outcome'
  AND content ? 'repro';

-- 4. 清理旧浮点编码与死信封（business_metadata 从未有读取方）
UPDATE exploration_node
SET metadata = metadata - '_confidence' - 'business_metadata'
WHERE COALESCE(metadata, '{}'::jsonb) ?| ARRAY['_confidence', 'business_metadata'];
