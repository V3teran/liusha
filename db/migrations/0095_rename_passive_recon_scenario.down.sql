-- 0095 down: 删除 api-pentest 场景行并回滚引用（seed 事实源在 md；代码回滚后 seed 重建 passive-recon）。
DELETE FROM scenario WHERE code = 'api-pentest';
UPDATE task          SET scenario_id = 'passive-recon' WHERE scenario_id = 'api-pentest';
UPDATE assignment    SET scenario_id = 'passive-recon' WHERE scenario_id = 'api-pentest';
UPDATE cron_schedule SET scenario_id = 'passive-recon' WHERE scenario_id = 'api-pentest';
