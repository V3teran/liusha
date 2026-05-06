-- 0016 down: 恢复旧 confidence 约束
ALTER TABLE vuln_finding DROP CONSTRAINT IF EXISTS finding_confidence_check;
ALTER TABLE vuln_finding ADD CONSTRAINT finding_confidence_check
    CHECK (confidence = ANY (ARRAY['unverified'::text, 'verified'::text, 'rejected'::text]));
