-- 0239 知识库改造：词元检索、场景归属与任务印证。
--
-- 字面召回原用 pg_trgm 的词相似度：它按空格切词，中文整句是一个「词」，多词查询永远够不到
-- 阈值（实测历史查询对相关条目召回为零）。改为按词元匹配：英文按词、中文按相邻两字切分，
-- 词元由数据库函数在写入时生成（生成列），写入与查询同一个函数，不会失步。
--
-- 标签由两个写入方各自起名、彼此对不上，检索按它硬过滤只会误伤，删除。知识改按场景归属：
-- 空 = 通用（任何任务可见），否则只对该场景的任务可见。存量条目全部来自任务沉淀，按来源任务
-- 的场景快照回填。
--
-- hit_count 混了「被检索到」与「重复写入」两种计数，改为「印证过它的任务」关系：同一任务
-- 重复收敛不重复计数；来源任务即第一条印证，source_task_id 并入印证表。

CREATE FUNCTION corpus_terms(t text) RETURNS text[]
LANGUAGE sql IMMUTABLE PARALLEL SAFE AS $$
  SELECT coalesce(array_agg(DISTINCT term ORDER BY term), '{}')
  FROM (
    SELECT CASE WHEN m ~ '^[㐀-鿿豈-﫿]+$'
                THEN substr(m, i, CASE WHEN length(m) = 1 THEN 1 ELSE 2 END)
                ELSE m END AS term
    FROM regexp_matches(lower(t), '[a-z0-9][a-z0-9_.+#-]*[a-z0-9+#]|[a-z0-9]|[㐀-鿿豈-﫿]+', 'g') AS r(a),
         LATERAL (SELECT a[1] AS m) mm,
         LATERAL generate_series(1, CASE WHEN m ~ '^[㐀-鿿豈-﫿]+$'
                                         THEN greatest(length(m) - 1, 1) ELSE 1 END) AS i
  ) x
  WHERE length(term) > 1 OR term ~ '[㐀-鿿豈-﫿]';
$$;

COMMENT ON FUNCTION corpus_terms(text) IS '知识库词元：小写化后英文按词（去掉单个字母或数字）、中文按相邻两字切分（单字成词保留），去重排序';

CREATE TABLE corpus_confirmation (
    entry_id     uuid NOT NULL REFERENCES corpus(id) ON DELETE CASCADE,
    task_id      uuid NOT NULL REFERENCES task(id) ON DELETE CASCADE,
    confirmed_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (entry_id, task_id)
);

CREATE INDEX corpus_confirmation_task_idx ON corpus_confirmation (task_id);

COMMENT ON TABLE corpus_confirmation IS '印证过某条知识的任务：沉淀出它或在收尾整合时判定与它一致';

ALTER TABLE corpus ADD COLUMN scenario text;

UPDATE corpus c SET scenario = t.scenario
FROM task t
WHERE t.id = c.source_task_id;

INSERT INTO corpus_confirmation (entry_id, task_id, confirmed_at)
SELECT c.id, c.source_task_id, c.created_at
FROM corpus c
JOIN task t ON t.id = c.source_task_id;

UPDATE corpus SET title = left(content, 30) WHERE title = '';

ALTER TABLE corpus
    DROP COLUMN tags,
    DROP COLUMN hit_count,
    DROP COLUMN source_task_id,
    ADD COLUMN terms text[] GENERATED ALWAYS AS (corpus_terms(title || ' ' || content)) STORED,
    ADD CONSTRAINT corpus_title_check CHECK (title <> '');

DROP INDEX corpus_content_trgm;
DROP INDEX corpus_title_trgm;
DROP EXTENSION pg_trgm;

CREATE INDEX corpus_terms_gin ON corpus USING gin (terms);
CREATE INDEX corpus_scenario_idx ON corpus (scenario);

COMMENT ON COLUMN corpus.scenario IS '所属场景标识；空 = 通用，对任何任务可见';
COMMENT ON COLUMN corpus.source IS '当前内容的作者：agent = 任务收尾沉淀，expert = 人工录入或导入（人工改过标题或正文也记为 expert）';
COMMENT ON COLUMN corpus.terms IS '词元（corpus_terms 生成），字面检索按它匹配';
