-- provider 显示名称（昵称）：仅展示用、可随时修改；空串回退 code 展示。
-- code 仍是被引用的稳定标识（路由 FK + REST 寻址），不可改——ID/名称分离。
ALTER TABLE llm_provider ADD COLUMN display_name text NOT NULL DEFAULT '';
