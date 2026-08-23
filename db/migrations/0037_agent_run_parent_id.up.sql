-- 0037: agent_run 重新引入 parent_id（子任务父子关系），为 subtask swarm 准备。
--
-- 背景：0001_init 原 agent_task 表自带 parent_task_id（自引用 FK），0002_v1_1_redesign
-- 阶段为简化 v1.1 架构 drop 掉。v1.4 引入主/子 active agent 模型（父 active 通过
-- spawn_child 工具派子任务并行深挖）需要重新追踪父子关系：
--   - list_children 工具按 parent_id SQL 查全部子任务状态
--   - 前端阶段 2 按 parent_id 拼父子树
--
-- 设计：
--   - 类型 uuid（与 agent_run.id 一致），NULL 表示独立任务/根任务
--   - 不加 FK：子任务父引用是业务记录，业务逻辑不依赖 PG 外键级联；engagement_id 已 CASCADE
--     兜底（engagement 删时所有 agent_run 都没了，FK 触发也无意义）
--   - partial index（WHERE NOT NULL）：独立任务占绝大多数，全索引浪费空间
ALTER TABLE agent_run ADD COLUMN parent_id uuid;
CREATE INDEX agent_run_parent_id_idx ON agent_run (parent_id) WHERE parent_id IS NOT NULL;
