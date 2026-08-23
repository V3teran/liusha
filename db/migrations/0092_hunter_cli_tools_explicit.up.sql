-- 0092: cli_tools 去「空=域内全部可见」魔法，改严格白名单（空=不装配任何外部工具）。
--   · 语义变更前，cli_tools=[] 的猎手在运行时经 FilterByNames 得到域内全集；
--     变更后 FilterByNames 空→空集，[] 的猎手将丢失全部外部 CLI 工具。
--   · 为保持运行时行为不变，把当前仍为 [] 的猎手显式回填 tools.yaml 全量 31 个工具名——
--     域过滤（FilterByDomain）仍在白名单之前生效，全集白名单不额外收窄，与迁移前等价。
--   · 仅回填 [] 行，不覆盖任何已手动裁剪的白名单。回填后由运维在配置页按需收窄。
UPDATE agent
SET cli_tools = '["subfinder","httpx","katana","nmap","wafw00f","arjun","dirsearch","ffuf","gau","spectral","nuclei","sqlmap","dalfox","ysomap","ysoserial","phpggc","hydra","jwt_tool","mysql","interactsh-client","semgrep","trufflehog","go","java","php","gcc","node","browser-use","curl","python3","jq"]'::jsonb,
    updated_at = now()
WHERE cli_tools = '[]'::jsonb;
