-- 删除四张老架构死表：代码零引用、dev 库零行。
-- react_run 被 llm_call 的历史 FK 引用（v1.3 时代遗留，llm_call 自身也已无代码引用），
-- 走 CASCADE 一并清理依赖对象。
DROP TABLE IF EXISTS llm_call CASCADE;
DROP TABLE IF EXISTS react_run CASCADE;
DROP TABLE IF EXISTS actor_checkpoint;
DROP TABLE IF EXISTS wm_move;
DROP TABLE IF EXISTS llm_alias;
