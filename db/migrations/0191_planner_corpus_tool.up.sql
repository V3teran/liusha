-- planner 接通跨任务长期记忆：search_corpus 进入其 function_tools 白名单。
-- 战略规划者此前对长期记忆零访问（只有 executor 有 search_corpus）——开局
-- 经验简报之外，规划者需要与执行层同等的按需检索能力。
-- 按旧白名单精确匹配守卫：运维经前端自定义过的白名单不被本迁移覆写。
UPDATE agent
SET function_tools = '["observe_state","evaluate_progress","search_corpus"]'::jsonb
WHERE code = 'planner'
  AND function_tools = '["observe_state","evaluate_progress"]'::jsonb;
