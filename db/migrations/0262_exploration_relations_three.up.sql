-- 探索图的关系收敛为三种：推导、滋生、跟进。
--
-- 去掉 covers（在办的动作「覆盖」某条线索）：观察只在动作结束时落图，线索出现时产出它的
-- 动作必然已经结束，同一批动作又彼此独立——这条边要的情形被另外两条规则排除，上线以来
-- 没有产生过一条。线索只有一种跟进方式：为它排出的动作认领它（pursues）。
--
-- 去掉 supersedes（新派生目标「吸收」旧派生目标）与方向的生命周期：方向只作分组，由结果
-- 分析追加，任务进行期间不收敛；上线以来没有产生过一条吸收边，也没有方向被宣告达成。
-- superseded 状态随之去掉，存量里若有按「没做完就被收回」归入 aborted。
ALTER TABLE exploration_edge DROP CONSTRAINT ck_exploration_edge_rel;
DELETE FROM exploration_edge WHERE rel IN ('covers', 'supersedes');
ALTER TABLE exploration_edge
    ADD CONSTRAINT ck_exploration_edge_rel CHECK ((rel = ANY (ARRAY['generates'::text, 'triggers'::text, 'pursues'::text])));

ALTER TABLE exploration_node DROP CONSTRAINT ck_exploration_node_state;
UPDATE exploration_node SET state = 'aborted' WHERE state = 'superseded';
ALTER TABLE exploration_node
    ADD CONSTRAINT ck_exploration_node_state CHECK ((state IS NULL) OR (state = ANY (ARRAY['open'::text, 'running'::text, 'completed'::text, 'failed'::text, 'exhausted'::text, 'aborted'::text, 'rejected'::text])));

COMMENT ON TABLE public.exploration_edge IS '探索图关系边：generates / triggers / pursues（见 ck_exploration_edge_rel）';
COMMENT ON COLUMN public.exploration_edge.rel IS '关系类型：generates(推导链) / triggers(结果催生方向或动作) / pursues(动作为跟进线索而排出，src=动作、dst=线索)';
COMMENT ON COLUMN public.exploration_node.state IS 'action/objective 的状态：open/running/completed/failed/exhausted/aborted（没做完即被收回，不含对错判断）/rejected（action 专用：被监察者或用户否决，见 metadata.rejected_by）';
