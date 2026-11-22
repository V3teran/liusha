-- 回到收紧之前（id 以新值回填，归并过的角色不恢复）。
ALTER TABLE message DROP CONSTRAINT message_role_check;
ALTER TABLE message ADD CONSTRAINT message_role_check
    CHECK (role IN ('user', 'assistant', 'system', 'tool'));
ALTER TABLE message ADD COLUMN id uuid NOT NULL DEFAULT gen_random_uuid();
