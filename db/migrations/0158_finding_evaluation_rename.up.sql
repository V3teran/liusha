-- finding 证据列正名：evaluation。
-- 列内容 = 复现门裁决评估（withReplayLog：verdict/reasoning/replay 日志/assert 明细，
-- 含 verification_id 回指审计链），名为 evidence 名不副实；Go 字段本就叫 Evaluation。
-- web 契约键同步 evidence → evaluation（FindingRow/FindingDrawer）。

ALTER TABLE finding RENAME COLUMN evidence TO evaluation;
