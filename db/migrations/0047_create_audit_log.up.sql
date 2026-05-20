-- 0047: audit_log 表——记录系统级敏感操作的审计日志。
--
-- 设计动因：
--   - 安全 / 合规需求："谁在何时建/中止 owner、删凭证"
--   - 调试场景：误删凭证或 owner 被意外 abort 时复盘根因
--   - 通用 actor / action / target 三元结构，避免一个事件一张表的爆炸
--
-- 写入策略：业务侧明确感知"这是审计事件"才写——不靠 trigger 全自动
-- （trigger 隐式行为难维护，且后续添加列总会破坏 trigger 假设）。

CREATE TABLE audit_log (
    id          bigserial   PRIMARY KEY,
    -- actor 标识发起者：'system' / 'sweep' / 'api_user:<key_prefix>' / 'scanner' 等
    actor       text        NOT NULL,
    -- action 是命名空间动词：'owner.abort' / 'owner.create' / 'credential.set' / 'credential.delete'
    action      text        NOT NULL,
    -- target_kind / target_id 共同定位被操作对象
    target_kind text        NOT NULL,
    target_id   text        NOT NULL,
    metadata    jsonb       NOT NULL DEFAULT '{}'::jsonb,
    created_at  timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX audit_log_actor_idx  ON audit_log (actor, created_at DESC);
CREATE INDEX audit_log_target_idx ON audit_log (target_kind, target_id, created_at DESC);
CREATE INDEX audit_log_action_idx ON audit_log (action, created_at DESC);

COMMENT ON TABLE audit_log IS '系统敏感操作审计：owner abort/create、credential 写删等';
COMMENT ON COLUMN audit_log.actor IS '示例: system / sweep / api_user:abcd1234 / scanner';
COMMENT ON COLUMN audit_log.action IS '命名空间动词，示例: owner.abort / credential.set';
