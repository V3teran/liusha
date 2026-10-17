-- 0166: 裁决分类法领域中立化——not_a_vuln → claim_invalid。
-- 探索图是领域中立的认知架构，领域语义只活在章程与领域存储；
-- planner 按 refute_reason 值分流处置，存量值必须跟随重命名。
-- 审计链（exploration_verification.evidence）是历史记录，不改写。
UPDATE exploration_node
SET metadata = jsonb_set(metadata, '{refute_reason}', '"claim_invalid"'::jsonb)
WHERE metadata->>'refute_reason' = 'not_a_vuln';
