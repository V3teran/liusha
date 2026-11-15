-- 0230：漏洞关联它的坐实结果节点，交付按结果节点幂等。
--
-- 此前按 dedup_key（host + cwe + 规范化路径 + method + parameter 的生成列）去重，但 cwe 与
-- parameter 从无写入方，键退化成「主机 + 路径 + 方法」：同一接口上先交付的低危主张（如缺少
-- CSP 响应头）会吞掉之后坐实的高危主张（如反射型 XSS），坐实结果在台账里凭空消失；同一结果
-- 重放交付时 target 缺席又会让键变化，产生重复记录。主张的语义去重已由复现门按主张指纹在
-- 探索图里完成（同一主张的再次确认折叠进同一结果节点），台账与结果一一对应即可。
--
-- 存量：result_node_id 从 evaluation.node_id 回填（结果节点须存在）；同一结果的重复记录合并为
-- 一条——优先保留人工处置过的，否则保留最早的；回填不到的历史记录保留、不关联。

ALTER TABLE public.finding ADD COLUMN result_node_id uuid
  REFERENCES public.exploration_node(id) ON DELETE CASCADE;

UPDATE public.finding f
   SET result_node_id = n.id
  FROM public.exploration_node n
 WHERE n.kind = 'result'
   AND n.task_id = f.task_id
   AND n.id::text = f.evaluation->>'node_id';

DELETE FROM public.finding f
 USING (SELECT id, row_number() OVER (PARTITION BY task_id, result_node_id
                                      ORDER BY (triaged_at IS NOT NULL) DESC, seq) AS rn
          FROM public.finding
         WHERE result_node_id IS NOT NULL) d
 WHERE f.id = d.id AND d.rn > 1;

DROP INDEX public.finding_task_dedup_v3_uniq;
ALTER TABLE public.finding DROP COLUMN dedup_key;
CREATE UNIQUE INDEX finding_task_result_uniq ON public.finding USING btree (task_id, result_node_id);

COMMENT ON COLUMN public.finding.result_node_id IS '坐实这条漏洞的探索图结果节点：一个结果一条漏洞，交付按它幂等（主张的语义去重在探索图里按主张指纹完成）。0230 之前无法回填的历史记录为空。';
