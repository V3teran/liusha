-- planned_move：表改名补齐（索引此前已按 planned_move 命名创建）+ 约束命名统一。

ALTER TABLE execution_plan RENAME TO planned_move;

ALTER TABLE planned_move RENAME CONSTRAINT execution_plan_domain_check TO planned_move_domain_check;
ALTER TABLE planned_move RENAME CONSTRAINT execution_plan_kind_check TO planned_move_kind_check;
ALTER TABLE planned_move RENAME CONSTRAINT execution_plan_status_check TO planned_move_status_check;

COMMENT ON TABLE planned_move IS 'Planner 产出的待执行 Move，支持异步 Plan-Execute 循环';
