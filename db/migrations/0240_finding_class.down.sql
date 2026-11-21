ALTER TABLE finding ADD COLUMN cwe_id text, ADD COLUMN owasp_category text;
ALTER TABLE finding DROP COLUMN class;
UPDATE exploration_node SET metadata = metadata - 'claim_class' WHERE kind = 'result' AND metadata ? 'claim_class';
