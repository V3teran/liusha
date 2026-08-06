-- 回滚：四个智能体 cli_tools 恢复为全部 31 个（0092 之后的状态）。
UPDATE hunter
SET cli_tools = '["subfinder","httpx","katana","nmap","wafw00f","arjun","dirsearch","ffuf","gau","spectral","nuclei","sqlmap","dalfox","ysomap","ysoserial","phpggc","hydra","jwt_tool","mysql","interactsh-client","semgrep","trufflehog","go","java","php","gcc","node","browser-use","curl","python3","jq"]'::jsonb
WHERE code IN ('reconnaissance', 'exploitation', 'traffic-analysis', 'orchestrator');
