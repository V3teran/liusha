-- 0193 down: 删登录账号与会话表（先子后父，CASCADE 亦兜底）。

DROP TABLE IF EXISTS account_session;
DROP TABLE IF EXISTS account;
