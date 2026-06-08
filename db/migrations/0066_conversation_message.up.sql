-- 0066: 对话式平台（阶段B）—— conversation + message 两表。
--
-- 设计（见 docs/superpowers/specs/2026-06-07-conversational-platform.md §B + 记忆 project_phaseb_sse_arch）：
--   - 用户在前端对话发起扫描；agent 过程事件经 Redis 中转流式推前端（方案A，保留分布式）；
--     对话与事件落库可回看。
--   - conversation：一次对话会话。scan_id 关联本对话发起的 active_scan（纯聊天/passive 时 NULL）。
--     role_id 是场景 role（阶段C 用，先留列不接线）。
--   - message：对话内的消息。kind 区分「普通对话消息」与「agent 过程事件」（UI 渲染不同）：
--       message —— user 提问 / assistant 回复 / system 提示
--       event   —— agent 跑扫描时的过程事件（tool 调用 / 阶段切换 / finding 产出 …），
--                  结构化细节进 metadata（jsonb）
--   - seq（bigserial）：全局稳定顺序键。SSE agent 事件高频，created_at 同毫秒会乱序，
--     故用自增 seq 做对话内排序，不依赖时间戳精度。

CREATE TABLE conversation (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    title       text,
    scan_id     uuid REFERENCES active_scan(id) ON DELETE SET NULL,
    role_id     text,
    status      text NOT NULL DEFAULT 'active'
                CHECK (status IN ('active','archived')),
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX conversation_status_idx ON conversation (status, updated_at DESC);
CREATE INDEX conversation_scan_idx ON conversation (scan_id) WHERE scan_id IS NOT NULL;

CREATE TABLE message (
    seq             bigserial PRIMARY KEY,
    id              uuid NOT NULL DEFAULT gen_random_uuid(),
    conversation_id uuid NOT NULL REFERENCES conversation(id) ON DELETE CASCADE,
    role            text NOT NULL
                    CHECK (role IN ('user','assistant','system','tool')),
    kind            text NOT NULL DEFAULT 'message'
                    CHECK (kind IN ('message','event')),
    content         text NOT NULL DEFAULT '',
    metadata        jsonb,
    created_at      timestamptz NOT NULL DEFAULT now()
);
-- 对话内按 seq 取消息（回看 / 增量拉取）。
CREATE INDEX message_conversation_idx ON message (conversation_id, seq);
