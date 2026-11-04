-- 0193: 登录账号 + 登录会话——控制面从静态 API Key 切换到账号密码认证。
--
-- 数据模型：
--   account          管理员账号（单操作者平台，预期一行；username 唯一约束兜底）
--   account_session  登录会话（浏览器 Cookie 里的随机 token 服务端只存其 SHA-256，
--                    库泄露拿不到可用凭证）
--
-- 会话有效期是双条件的：expires_at 滑动续期（活跃用户不被频繁登出） +
-- created_at 起算的绝对上限（30 天，滑动会话不得永生）。两者都在查询谓词里判定，
-- 无需后台清理任务的强一致——过期行由登出/改密删除与自然淘汰覆盖。

CREATE TABLE account (
    id            bigserial   PRIMARY KEY,
    username      text        NOT NULL UNIQUE,
    password_hash text        NOT NULL,               -- bcrypt PHC 串（含盐与 cost）
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE account_session (
    token_hash  text        PRIMARY KEY,              -- sha256(Cookie 明文 token) hex
    account_id  bigint      NOT NULL REFERENCES account(id) ON DELETE CASCADE,
    created_at  timestamptz NOT NULL DEFAULT now(),
    expires_at  timestamptz NOT NULL,
    renewed_at  timestamptz NOT NULL DEFAULT now()    -- 最近一次滑动续期时间（不逐请求写）
);

CREATE INDEX account_session_account_idx ON account_session (account_id);

COMMENT ON TABLE  account         IS '控制面登录账号（单管理员）；初始账号经首启初始化向导建立';
COMMENT ON COLUMN account.password_hash IS 'bcrypt PHC 格式哈希，cost 12';
COMMENT ON TABLE  account_session IS '登录会话：token 只存哈希；expires_at 滑动续期 + created_at 起 30 天绝对上限';
