-- 0238 升级：回填 target_host；恢复被交付失败错驳的假设。
--
-- target_host 的派生此前只认带 scheme 的 URL，brief 写裸地址（跑分适配器只拿得到
-- host:port）的任务派生全空——它是坐实交付主机线索落空时的最终兜底，全空令无 URL
-- 的主张（steps 配方）交付必被「Host 必填」拒绝。按现行派生规则回填存量：URL 优先、
-- 裸 IPv4（端口可选）次之、含点且带端口的主机名最后（与 task.targetHostOf 同口径）。
-- 交付失败当时被错误计入验证阶梯、耗尽后按 not_reproducible 出圈：审计链里那次
-- confirmed 判定才是真结论，这类假设恢复为已坐实，交由补交付收回台账。

UPDATE task
SET target_host = COALESCE(
    substring(goal FROM 'https?://([^/[:space:]]+)'),
    (regexp_match(goal, '\m(\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3})(:[0-9]{1,5})?\M'))[1]
        || COALESCE((regexp_match(goal, '\m(\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3})(:[0-9]{1,5})?\M'))[2], ''),
    (regexp_match(goal, '\m([A-Za-z0-9][A-Za-z0-9.-]*\.[A-Za-z0-9-]+:[0-9]{1,5})\M'))[1]
)
WHERE target_host = '' AND goal <> '';

UPDATE exploration_node n
SET metadata = (n.metadata - 'refute_reason' - 'failure_reason' - 'verify_attempts')
    || '{"verification_status":"confirmed"}'::jsonb
WHERE n.kind = 'observation'
  AND n.metadata->>'verification_status' = 'refuted'
  AND n.metadata->>'refute_reason' = 'not_reproducible'
  AND n.metadata->>'failure_reason' LIKE '%交付失败%'
  AND EXISTS (SELECT 1 FROM exploration_verification v
              WHERE v.node_id = n.id AND v.outcome = 'confirmed');
