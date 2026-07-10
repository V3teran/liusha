-- 0073: 合并 active_scan + passive_session → 统一 task 表（P0 地基）。
--
-- 设计（见 docs/superpowers/specs/2026-07-05-assignment-task-lead-design.md §2）：
--   - active_scan（强调动作）与 passive_session（强调时段）命名不对称、字段各半，
--     合并为单表 task，mode ∈ {active,passive} 区分，字段取两表并集。
--   - passive task 语义变化：不再是「常驻监控会话」，而是「对某 host 一批捕获流量的一次分析」。
--     故砍掉 passive_session 的 expires_at / Rotator / 单 host 唯一约束（那些留在 0077 删表时消失）。
--   - assignment_id 属 P1（assignment 表尚未建），本阶段 task 不带该列；P1 迁移再加列 + 收紧 FK。
--
-- 字段来源：
--   brief         ← active_scan.brief（passive 为空）
--   target_host   ← active_scan.target_host / passive_session.host（passive 必填）
--   status        ← 两表 status 并集（active/completed/aborted；archived 不再使用）
--   heartbeat_at  ← 0072 两表都有（B2 心跳探活）
--   paused_ms     ← 0070 active_scan（FollowUp 停顿扣除；passive 恒 0）
--
-- 存量可丢：不搬运旧数据，旧表在 0077 DROP。

CREATE TABLE task (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    mode          text NOT NULL CHECK (mode IN ('active','passive')),
    brief         text NOT NULL DEFAULT '',   -- active：用户 brief；passive：空
    target_host   text NOT NULL DEFAULT '',   -- active 可空；passive 必填（被分析 host）
    status        text NOT NULL DEFAULT 'active'
                  CHECK (status IN ('active','completed','aborted')),
    heartbeat_at  timestamptz NOT NULL DEFAULT now(),
    paused_ms     bigint NOT NULL DEFAULT 0,
    created_at    timestamptz NOT NULL DEFAULT now(),
    ended_at      timestamptz,
    error_message text NOT NULL DEFAULT ''
);

CREATE INDEX task_mode_status_idx ON task (mode, status, created_at DESC);
CREATE INDEX task_host_idx ON task (target_host) WHERE target_host <> '';
-- reaper 巡逻：活跃且心跳老的行（对齐 0072 旧索引语义）。
CREATE INDEX task_heartbeat_idx ON task (mode, heartbeat_at) WHERE status = 'active';

COMMENT ON TABLE task IS '统一扫描任务（合并 active_scan + passive_session）；mode 区分主动/被动';
COMMENT ON COLUMN task.brief IS 'active：用户自然语言 brief；passive：空';
COMMENT ON COLUMN task.target_host IS 'active 可空；passive 必填（被分析 host）';
COMMENT ON COLUMN task.paused_ms IS 'FollowUp 历次停顿累计 ms；WallclockMs 减去它 = 纯 agent 耗时';
