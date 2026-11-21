-- 发现的类型。评估者坐实主张时按判定契约给出主张类别（词表由任务的场景定），此前它只存在结果
-- 节点的去重规范键「类别|对象|作用点」里，没有进漏洞台账；台账上的 cwe_id / owasp_category
-- 两列则从来没有写入方，界面据此提供的是永远查不到的分类。

-- 结果节点单独记下主张类别（交付的出处），存量从规范键的首段取出
UPDATE exploration_node
SET metadata = metadata || jsonb_build_object('claim_class', split_part(metadata->>'claim_fingerprint', '|', 1))
WHERE kind = 'result'
  AND metadata ? 'claim_fingerprint'
  AND NOT metadata ? 'claim_class'
  AND split_part(metadata->>'claim_fingerprint', '|', 1) <> '';

ALTER TABLE finding ADD COLUMN class text NOT NULL DEFAULT '';
COMMENT ON COLUMN finding.class IS '发现类型：坐实主张时评估者给出的主张类别（小写 snake_case，词表由场景定）；空 = 未给出';

UPDATE finding f
SET class = n.metadata->>'claim_class'
FROM exploration_node n
WHERE n.id = f.result_node_id
  AND n.metadata ? 'claim_class';

ALTER TABLE finding DROP COLUMN cwe_id, DROP COLUMN owasp_category;
