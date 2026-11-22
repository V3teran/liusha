-- 消息只留一个标识，角色只留实际产生的两种。
--
-- 1. message.id 是库侧生成的第二个 UUID：主键是 seq（全局自增、会话内顺序键，增量拉取与实时
--    推送都按它），没有任何查询按 id 查找，id 上也没有索引。删除。
-- 2. 角色约束允许 user / assistant / system / tool，实际只有人说的（user）与系统产出的
--    （assistant）两种：agent 过程事件的细分在 kind 与 metadata.kind 上，system / tool 从无写入方。
--    存量里若有这两种（早期版本写入的过程事件），归入 assistant 后收紧约束。
ALTER TABLE message DROP COLUMN id;

UPDATE message SET role = 'assistant' WHERE role IN ('system', 'tool');
ALTER TABLE message DROP CONSTRAINT message_role_check;
ALTER TABLE message ADD CONSTRAINT message_role_check CHECK (role IN ('user', 'assistant'));
