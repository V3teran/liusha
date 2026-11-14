-- 0220：结果的陈述与假设同名（statement）。
--
-- 结果是坐实的假设，陈述是同一段文字，却在结果里叫 summary：评估者晋升时要把
-- statement 映射成 summary，读的一侧各自按其中一个名字读——规划者「观察图状态」的
-- 发现一节读 statement，于是一条坐实的发现都看不到。存量改名。

UPDATE public.exploration_node
   SET content = (content - 'summary') || jsonb_build_object('statement', content->'summary')
 WHERE kind = 'result'
   AND content ? 'summary'
   AND NOT content ? 'statement';
