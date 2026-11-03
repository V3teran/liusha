-- planner 章程补 search_corpus 使用指引。0191 只送达了白名单；章程正文走
-- 启动种子（insert-only，DB 是事实源），存量库的章程到不了新正文。
-- 追加式更新 + 语义守卫：已提及 search_corpus 的章程（新库种子 / reseed /
-- 运维已自行补充）跳过，天然幂等；运维自定义的其余正文原样保留——追加而
-- 非覆写，且不依赖存量库持有的具体历史种子版本（全量精确匹配会因版本/空白
-- 差异静默空转）。
UPDATE agent
SET system_prompt = system_prompt || $charter$

## 知识工具（search_corpus）

- **search_corpus**：检索跨任务经验库（历史任务沉淀的教训与打法）。
- 使用时机：开局注入的经验简报与本任务目标不匹配、或进入陌生技术面（新框架/新中间件/非常规认证）时，以当前方向为查询词检索。
- 命中历史教训时，优先复用已验证的打法、避开已证死的路径。经验来自其它任务，仅供参考——与本任务的事实冲突时以本任务为准。
$charter$
WHERE code = 'planner'
  AND system_prompt NOT LIKE '%search_corpus%';
