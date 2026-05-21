-- 0042 down: 重建 *_count 列（值不可逆——历史值已丢，全部 DEFAULT 0）。

ALTER TABLE passive_session ADD COLUMN flow_count int NOT NULL DEFAULT 0;
ALTER TABLE passive_session ADD COLUMN finding_count int NOT NULL DEFAULT 0;
ALTER TABLE passive_session ADD COLUMN agent_run_count int NOT NULL DEFAULT 0;

ALTER TABLE active_scan ADD COLUMN finding_count int NOT NULL DEFAULT 0;
ALTER TABLE active_scan ADD COLUMN agent_run_count int NOT NULL DEFAULT 0;
