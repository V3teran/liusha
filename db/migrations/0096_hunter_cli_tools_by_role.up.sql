-- 按职能收敛各智能体 cli_tools（最小授权）。
-- seed 是 insert-only 不更新旧行，故此处 UPDATE 同步已有 DB 记录；与 agents/*.md frontmatter 保持一致。
-- 分配逻辑：侦察=发现/探测类；利用=打洞武器+payload runtime；流量=HTTP 包级攻击子集；编排=空（无 run_command）。

-- 侦察：recon + discovery + sast(secrets) + browser + utility（去打洞武器与 payload runtime）
UPDATE agent
SET cli_tools = '["subfinder","httpx","katana","nmap","wafw00f","arjun","dirsearch","ffuf","gau","spectral","semgrep","trufflehog","browser-use","curl","python3","jq"]'::jsonb
WHERE code = 'reconnaissance';

-- 利用：全套打洞武器 + payload runtime + wafw00f/ffuf（绕过与 fuzz）（去站点级侦察工具）
UPDATE agent
SET cli_tools = '["wafw00f","ffuf","nuclei","sqlmap","dalfox","ysomap","ysoserial","phpggc","hydra","jwt_tool","mysql","interactsh-client","go","gcc","java","php","node","browser-use","curl","python3","jq"]'::jsonb
WHERE code = 'exploitation';

-- 流量：HTTP 包级攻击子集（注入/反序列化/JWT/mysql/oob + java·php·node·python 造 payload）（去主动发现类与 browser）
UPDATE agent
SET cli_tools = '["sqlmap","dalfox","ysomap","ysoserial","phpggc","jwt_tool","mysql","interactsh-client","java","php","node","curl","python3","jq"]'::jsonb
WHERE code = 'traffic-analysis';

-- 编排：清空（无 run_command，装配即 prompt 噪音）
UPDATE agent
SET cli_tools = '[]'::jsonb
WHERE code = 'planner';
