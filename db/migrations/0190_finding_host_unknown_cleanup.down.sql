-- 逆迁移：恢复非空约束（要求现库无空 host；有空行时先自行清理）。
UPDATE finding SET host = 'unknown' WHERE host = '';
ALTER TABLE finding ADD CONSTRAINT finding_host_nonempty CHECK (host <> '');
