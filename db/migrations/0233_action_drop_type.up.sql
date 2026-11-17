-- 0233：动作不再携带类型。固定的阶段词表本身是某个领域的方法论，动作意图由 instruction
-- 表达；规划者不再写 content.type，执行者与界面也不再读它。去掉存量动作节点里的这个键。

UPDATE public.exploration_node
   SET content = content - 'type'
 WHERE kind = 'action' AND content ? 'type';
