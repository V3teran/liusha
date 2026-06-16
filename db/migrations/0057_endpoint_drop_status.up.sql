-- 删 endpoint.status 字段 — chaining 友好设计。
--
-- 设计反思（0056 status 字段的根本缺陷）：
-- status='tested_vulnerable' / 'tested_clean' 假设 endpoint 是"原子可判断"，但漏洞挖掘
-- 是 context 相关的反复试验过程——同一 endpoint 在不同凭证/已知 finding 下挖洞结果不同。
-- 例：guest 凭证下 /api/users tested_clean，admin 凭证下应该 re-test 找 BAC。
-- status 字段强行驱动 orchestrator done 判定，会阻止合理的 chaining re-spawn。
--
-- 业界对齐（OWASP ZAP / Burp Suite / Caido）：sitemap 节点持久存在，多次扫描结果叠加，
-- 节点本身不带"状态"。我们的 endpoint 表回归同样设计。
--
-- 替代方案：projector 投影时**派生** status（endpoint 关联 finding → tested_vulnerable，
-- 否则 discovered），前端染色不受影响；orchestrator done 自检改用 list_exploitations brief 历史推断。

ALTER TABLE endpoint DROP CONSTRAINT IF EXISTS endpoint_status_check;
DROP INDEX IF EXISTS endpoint_owner_status_idx;
ALTER TABLE endpoint DROP COLUMN IF EXISTS status;
