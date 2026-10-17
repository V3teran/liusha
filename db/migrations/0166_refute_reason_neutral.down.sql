UPDATE exploration_node
SET metadata = jsonb_set(metadata, '{refute_reason}', '"not_a_vuln"'::jsonb)
WHERE metadata->>'refute_reason' = 'claim_invalid';
