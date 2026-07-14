-- 0076: conversation.scan_id → task_id（两轨对称，见 spec §2「与 conversation 的关系」）。
--
-- 设计：
--   现状 active 用 conversation.scan_id→active_scan（对话持有），passive 用
--   passive_session.conversation_id→conversation（反向持有），不对称。
--   合表后统一为 conversation.task_id→task（对话持有），两轨对称。
--   passive_session 表在 0077 删除，其 conversation_id 列一并消失。
--
-- 基数：conversation 0..1 ↔ 1 task。FollowUp 同对话追加 hunter run，不新建对话。
-- FK ON DELETE SET NULL：删 task 不连带删对话（对话是用户资产）。

DROP INDEX IF EXISTS conversation_scan_idx;
ALTER TABLE conversation DROP COLUMN IF EXISTS scan_id;
ALTER TABLE conversation ADD COLUMN task_id uuid REFERENCES task(id) ON DELETE SET NULL;
CREATE INDEX conversation_task_idx ON conversation (task_id) WHERE task_id IS NOT NULL;
