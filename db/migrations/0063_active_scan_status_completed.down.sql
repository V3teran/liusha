-- 回滚：先把 'completed' 行降级为 'aborted'（否则重建严格约束会因存量行失败），
-- 再把 CHECK 约束收回 {active, aborted}。
UPDATE active_scan SET status='aborted', ended_at=COALESCE(ended_at, now())
	WHERE status='completed';
ALTER TABLE active_scan DROP CONSTRAINT active_scan_status_check;
ALTER TABLE active_scan ADD CONSTRAINT active_scan_status_check
	CHECK (status = ANY (ARRAY['active'::text, 'aborted'::text]));
