-- 0095: 重命名场景 passive-recon → api-pentest（聚焦测 HTTP 数据包漏洞）。
--   scenario 的 name/description/instruction 事实源是 scenarios/api-pentest.md，启动期 seed
--   幂等重建（insert-only：不存在才建）。故这里**删除**旧 passive-recon 行——让 seed 以新 md
--   内容重建 api-pentest，而非就地改 code 却留下旧文案。
--   task/assignment/cron_schedule 的 scenario_id 是裸 text 无 FK，连带 remap 保持派发链一致。
DELETE FROM scenario WHERE code = 'passive-recon';
UPDATE task          SET scenario_id = 'api-pentest' WHERE scenario_id = 'passive-recon';
UPDATE assignment    SET scenario_id = 'api-pentest' WHERE scenario_id = 'passive-recon';
UPDATE cron_schedule SET scenario_id = 'api-pentest' WHERE scenario_id = 'passive-recon';
