-- 0133: 创建 Skill 表，支持前端可编辑的知识库管理
--
-- Skill 是 Agent 可访问的知识库文档（如 dom-xss、browser-use）
-- 前端可对 Skill 进行增删改查，Agent 通过 skills 字段关联

-- ① 创建 Skill 表
CREATE TABLE skill (
    id             uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    code           text        NOT NULL UNIQUE,
    category       text        NOT NULL DEFAULT '',  -- tooling / vuln
    name           text        NOT NULL,
    description    text        NOT NULL DEFAULT '',
    body           text        NOT NULL DEFAULT '',  -- Markdown 正文
    is_builtin     boolean     NOT NULL DEFAULT false,
    enabled        boolean     NOT NULL DEFAULT true,
    created_at     timestamptz NOT NULL DEFAULT now(),
    updated_at     timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX idx_skill_category ON skill(category);
CREATE INDEX idx_skill_enabled ON skill(enabled) WHERE enabled = true;

COMMENT ON TABLE skill IS 'Skill 配置表：Agent 可访问的知识库文档';
COMMENT ON COLUMN skill.code IS 'Skill 唯一标识，如 tooling/browser-use, vuln/dom-xss';
COMMENT ON COLUMN skill.category IS 'Skill 分类：tooling（工具类）或 vuln（漏洞类）';
COMMENT ON COLUMN skill.body IS 'Skill 正文内容（Markdown 格式）';
COMMENT ON COLUMN skill.is_builtin IS '是否为内置 Skill（内置不可删除）';
