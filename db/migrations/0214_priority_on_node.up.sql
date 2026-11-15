-- 0214：优先级只在节点层存一份。
--
-- 观察与结果的真实优先级此前只在 content.priority，节点层 metadata.priority 两条
-- 晋升路径都写死 medium——图与详情面板把严重级结果显示成「P2 中」，卡片上又靠
-- 内容另画一个等级徽章。现在节点层是唯一一份（写入时按 NormalizePriority 归一），
-- 内容不再存。存量按同一口径归一后迁到节点层。

UPDATE public.exploration_node
   SET metadata = metadata || jsonb_build_object('priority',
         CASE lower(btrim(content->>'priority'))
           WHEN 'critical' THEN 'critical'
           WHEN 'high' THEN 'high'
           WHEN 'low' THEN 'low'
           ELSE 'medium'
         END),
       content = content - 'priority'
 WHERE kind IN ('observation', 'result')
   AND content ? 'priority';
