-- 移除追加的知识工具节（精确块替换，best-effort：若运维在追加后改写过该节
-- 则不再匹配，down 不强求——章程属内容数据，回滚以不破坏为限）。
UPDATE agent
SET system_prompt = REPLACE(system_prompt, $charter$

## 知识工具（search_corpus）

- **search_corpus**：检索跨任务经验库（历史任务沉淀的教训与打法）。
- 使用时机：开局注入的经验简报与本任务目标不匹配、或进入陌生技术面（新框架/新中间件/非常规认证）时，以当前方向为查询词检索。
- 命中历史教训时，优先复用已验证的打法、避开已证死的路径。经验来自其它任务，仅供参考——与本任务的事实冲突时以本任务为准。
$charter$, '')
WHERE code = 'planner';
