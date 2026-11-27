-- 规划者排出的动作与提出的方向不再带优先级。
--
-- 优先级只在两处有含义：假设上（坐实后即发现的严重度）与用户指派的动作上（执行者派发的
-- 先后，缺省 high）。模型给自己排的动作、提出的方向打的优先级没有读取方赋予它意义——章程
-- 没有定级指导，实测规划者排的动作 72% 是 P0 / P1，派发先后只在待执行动作多于空位时才
-- 起作用，方向上的优先级写完后没有任何读取方。
--
-- 存量里清掉规划者排出的动作、方向与根目标上的 priority；假设、结果与用户指派的动作不动。
UPDATE exploration_node
   SET metadata = metadata - 'priority'
 WHERE metadata ? 'priority'
   AND kind IN ('action', 'objective')
   AND NOT (kind = 'action' AND metadata->>'source_type' = 'user');
