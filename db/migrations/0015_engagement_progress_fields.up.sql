-- 0015: engagement 加进度 / 结束时间 / 错误原因字段
--
-- 借鉴 liusha2 task 表的 ended_at / error_message / *_count 字段思路，
-- 让 UI / 报告 / e2e 测试有现成入口看扫描进度与失败原因，不必每次 join 三个子表。
--
-- 不加 started_at：与 created_at 重合（engagement 创建即 active），YAGNI。
-- *_count 维护策略：
--   - 写路径（vulnfinding.Save / flow.Append / reactrun.Create）best-effort UPDATE +1
--   - Abort 时事务内 SELECT count(*) 重算 3 个子表精确兜底
ALTER TABLE engagement
    ADD COLUMN ended_at        timestamptz,
    ADD COLUMN error_message   text NOT NULL DEFAULT '',
    ADD COLUMN flow_count      int  NOT NULL DEFAULT 0,
    ADD COLUMN finding_count   int  NOT NULL DEFAULT 0,
    ADD COLUMN react_run_count int  NOT NULL DEFAULT 0;
