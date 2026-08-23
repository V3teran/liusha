-- 0119: 将 Tier 概念重命名为 Complexity，更准确反映其语义
--
-- 背景：原 Tier（heavy/vision/light）命名模糊，实际表达的是推理复杂度而非能力类型。
-- 新命名 Complexity（simple/medium/complex）更直观，便于理解和配置。
--
-- 由于是测试环境，直接清空数据并重建结构。

-- 1. 删除 agent 表的旧 tier 列（0118 已添加 complexity 列）
ALTER TABLE agent DROP COLUMN IF EXISTS tier;

-- 2. 清空并更新 llm_role_route 表
-- 删除所有旧的 tier 路由配置（heavy/vision/light）
DELETE FROM llm_role_route WHERE role IN ('heavy', 'vision', 'light');

-- 3. 更新注释
COMMENT ON COLUMN agent.complexity IS '所需 LLM 复杂度档位：simple（快速响应）| medium（标准推理，默认）| complex（深度推理）';
COMMENT ON TABLE llm_role_route IS 'LLM 角色路由表：role（agent code 或 complexity 档位）→ provider_key';
