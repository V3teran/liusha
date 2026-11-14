-- 0217：动作内容只留动作本身——type / instruction / reasoning。
--
-- 规划产出的动作内容此前重复存着 complexity、priority（节点层已有）与 triggered_by
-- （已落成 triggers 边），还带着没有任何读者的 metadata；人工注入的动作带 origin（与
-- source_type 重复）。规划理由在规划动作里叫 reason、在结果分析的延续动作里叫
-- reasoning，同一个概念两个名字——规划者提示词的「失败的尝试」一节曾因此把规划理由
-- 当成失败原因展示。现在统一叫 reasoning。

UPDATE public.exploration_node
   SET content = (content - 'reason' - 'complexity' - 'priority' - 'triggered_by' - 'metadata' - 'origin')
       || CASE WHEN content ? 'reason' AND NOT content ? 'reasoning'
               THEN jsonb_build_object('reasoning', content->'reason')
               ELSE '{}'::jsonb END
 WHERE kind = 'action'
   AND content ?| ARRAY['reason', 'complexity', 'priority', 'triggered_by', 'metadata', 'origin'];
