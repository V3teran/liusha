-- 0006: finding 改 append-only（每次重发现新增一行，不再 UPSERT 合并）
--
-- 背景：v1.2 之前 (host, dedup_key) UNIQUE + ON CONFLICT DO UPDATE 合并 evidence——
-- jsonb || 顶层键合并语义会把同名字段（如 violating: ["user1"]）被新值覆盖，丢历史。
--
-- 改成 append-only：
--   - 每次 finding.Save 都 INSERT 新行（同 (host, dedup_key) 多行合法）
--   - finding.Store.Save 用 SELECT EXISTS 提前判断 isFirstSeen，决定触发 distill 还是 lesson hit_count++
--   - ListByEngagement 用 DISTINCT ON 按 (host, dedup_key) 取最新一行 + 顺带带回发现次数
--
-- 影响：
--   - 历史发现的完整 evidence 永久保留（每次新行）
--   - 行数随重扫次数线性增长——可接受，与 host_lesson.hit_count 是一回事的两种表达
--   - 旧调用 Save 默认 evidence 合并行为不再生效——caller 需自行决定怎么用历史行

DROP INDEX IF EXISTS finding_host_dedup_uniq;
CREATE INDEX finding_host_dedup_idx ON finding (host, dedup_key);
