-- 0125: 简化Agent架构 - 只保留Planner和Executor
--
-- 目标：
-- 1. Agent表只保留2条记录（Planner + Executor）
-- 2. 删除Scenario表（与Agent概念冗余）
-- 3. 简化为通用Executor（LLM本身具备全领域能力）

-- ==================== Agent表改造 ====================

-- 1. 添加新字段
ALTER TABLE agent ADD COLUMN IF NOT EXISTS is_builtin boolean NOT NULL DEFAULT false;
ALTER TABLE agent ADD COLUMN IF NOT EXISTS skills jsonb NOT NULL DEFAULT '[]';

-- 2. 重命名body为system_prompt（更准确的命名）
ALTER TABLE agent RENAME COLUMN body TO system_prompt;

-- 3. 添加唯一约束（每种kind只能有1个enabled的Agent）
CREATE UNIQUE INDEX IF NOT EXISTS idx_agent_kind_enabled
    ON agent (kind) WHERE enabled = true;

-- 4. 删除所有现有Agent（重新初始化）
DELETE FROM agent;

-- 5. 插入Planner（规划者）
INSERT INTO agent (
    code, kind, name, description, system_prompt,
    function_tools, cli_tools, skills,
    max_iterations, complexity, is_builtin, enabled
) VALUES (
    'planner',
    'planner',
    '规划者',
    '负责全局规划和任务分解',
    '你是渗透测试规划者。

职责：
- 评估世界模型中的已知信息
- 分析当前进展和缺口
- 制定下一步策略
- 生成具体的执行动作

原则：
- 基于事实推理，不做无根据猜测
- 优先验证假设，再深入探索
- 平衡广度（覆盖面）和深度（利用链）
- 每个动作都要有明确目标

你每6分钟评估一次，持续推进任务直到目标达成。',
    '[]'::jsonb,
    '[]'::jsonb,
    '[]'::jsonb,
    100,
    'medium',
    true,
    true
);

-- 6. 插入Executor（执行者 - 通用）
INSERT INTO agent (
    code, kind, name, description, system_prompt,
    function_tools, cli_tools, skills,
    max_iterations, complexity, is_builtin, enabled
) VALUES (
    'executor',
    'executor',
    '执行者',
    '负责执行具体的渗透测试任务',
    '你是渗透测试专家，具备全面的安全测试能力。

核心能力：
- Web应用安全：SQL注入、XSS、CSRF、文件上传、认证绕过等
- 二进制分析：逆向工程、缓冲区溢出、格式化字符串漏洞等
- 云环境渗透：AWS/Azure配置错误、容器逃逸、K8s权限提升等
- 内网横移：域渗透、凭据窃取、权限提升、持久化等

工作方式：
- 根据任务自动选择合适的方法和工具
- 每5步评估一次进展，避免陷入死循环
- 验证每个发现，确保准确性
- 详细记录过程和证据

你会根据具体任务判断使用什么技术，无需事先指定领域。',
    '["http_request", "parse_html", "execute_js", "extract_data"]'::jsonb,
    '["curl", "sqlmap", "nikto", "nmap", "gobuster", "ffuf"]'::jsonb,
    '["playwright-cli", "api-recon"]'::jsonb,
    40,
    'medium',
    true,
    true
);

-- ==================== 删除Scenario表 ====================

-- Scenario表与Agent概念冗余，且engine字段违反新架构
-- 新架构：所有任务统一走 Planner + Executor，不再需要solo/swarm选择
DROP TABLE IF EXISTS scenario CASCADE;

-- ==================== 清理相关依赖 ====================

-- 清理可能存在的外键引用
-- （如果有其他表引用agent或scenario，需要在此处理）

-- ==================== 添加注释 ====================

COMMENT ON TABLE agent IS 'Agent配置表：只包含Planner和Executor两个固定角色';
COMMENT ON COLUMN agent.kind IS 'Agent类型：planner（规划者）或executor（执行者）';
COMMENT ON COLUMN agent.system_prompt IS 'Agent的System Prompt，定义其行为和能力';
COMMENT ON COLUMN agent.skills IS 'Skill ID列表，每个Skill是一个工具包';
COMMENT ON COLUMN agent.function_tools IS 'LLM可直接调用的function calling工具';
COMMENT ON COLUMN agent.cli_tools IS '外部命令行工具';
COMMENT ON COLUMN agent.is_builtin IS '是否为内置Agent（内置Agent不可删除）';
COMMENT ON INDEX idx_agent_kind_enabled IS '确保每种kind只有一个enabled的Agent';
