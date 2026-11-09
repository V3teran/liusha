-- 0201 内置内容分层：出厂值编译进程序，数据库只存用户自定义。
--
-- agent、内置技能、系统设置的出厂值从此随程序发布，不再写入数据库；数据库只存
-- 用户改过的字段（agent_override / skill_override / setting_override）与用户自建技能
-- （custom_skill）。原 agent / skill / system_setting 三张表在本迁移内转换后删除。
--
-- 老数据逐字段判定：等于任一历史出厂值（下方对照表由 git 全部历史生成）→ 视为未改，
-- 升级后跟随新版；否则视为用户修改，转为自定义保留（基线未知）。名单字段以最接近的
-- 历史出厂名单为起点换算为新增与删除。提示词已拆分为章程与契约，改过的整份旧提示词
-- 无法自动拆分，原文归档到 agent_prompt_archive，由用户对照后决定是否重新应用。
-- 误判方向只会是「未改判为改过」，表现为多一个可一键恢复默认的自定义，不丢数据。
--
-- 本文件的历史对照表由 scripts/gen-migration-0201 生成。

-- ── 新表 ────────────────────────────────────────────────────────────

CREATE TABLE public.agent_override (
    agent_code text NOT NULL PRIMARY KEY,
    fields jsonb DEFAULT '{}'::jsonb NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT agent_override_agent_code_check CHECK ((agent_code = ANY (ARRAY['planner'::text, 'executor'::text, 'evaluator'::text, 'monitor'::text]))),
    CONSTRAINT agent_override_fields_check CHECK ((jsonb_typeof(fields) = 'object'::text))
);
COMMENT ON TABLE public.agent_override IS 'agent 的用户自定义层：只存被自定义的字段；出厂值编译在程序里';
COMMENT ON COLUMN public.agent_override.fields IS '字段名 → 自定义记录：整值替换 {value, baseline}，名单 {added, removed}';

CREATE TABLE public.agent_prompt_archive (
    agent_code text NOT NULL PRIMARY KEY,
    prompt text NOT NULL,
    archived_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT agent_prompt_archive_agent_code_check CHECK ((agent_code = ANY (ARRAY['planner'::text, 'executor'::text, 'evaluator'::text, 'monitor'::text])))
);
COMMENT ON TABLE public.agent_prompt_archive IS '升级前用户自定义过的整份提示词：提示词拆分为章程与契约后无法自动拆分，归档原文待用户对照处理';

CREATE TABLE public.skill_override (
    skill_code text NOT NULL PRIMARY KEY,
    fields jsonb DEFAULT '{}'::jsonb NOT NULL,
    last_saved jsonb DEFAULT '{}'::jsonb NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT skill_override_fields_check CHECK ((jsonb_typeof(fields) = 'object'::text))
);
COMMENT ON TABLE public.skill_override IS '内置技能的用户自定义层：只存被自定义的字段；出厂内容编译在程序里';
COMMENT ON COLUMN public.skill_override.last_saved IS '最后一次保存时的完整生效内容：内置技能下线后据此转为同 code 的自建技能';

CREATE TABLE public.custom_skill (
    code text NOT NULL PRIMARY KEY,
    category text DEFAULT ''::text NOT NULL,
    name text NOT NULL,
    description text DEFAULT ''::text NOT NULL,
    body text DEFAULT ''::text NOT NULL,
    enabled boolean DEFAULT true NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL
);
COMMENT ON TABLE public.custom_skill IS '用户自建技能；与内置技能同 code 时自建优先';
COMMENT ON COLUMN public.custom_skill.category IS '知识域：vuln（漏洞知识）或 tool（工具使用知识）';

CREATE TABLE public.setting_override (
    group_key text NOT NULL PRIMARY KEY,
    fields jsonb DEFAULT '{}'::jsonb NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT setting_override_group_key_check CHECK ((group_key = ANY (ARRAY['compaction'::text, 'runtime'::text, 'proxy_filter'::text]))),
    CONSTRAINT setting_override_fields_check CHECK ((jsonb_typeof(fields) = 'object'::text))
);
COMMENT ON TABLE public.setting_override IS '系统设置的用户自定义层：只存被自定义的字段；出厂默认 = 代码 + config.yaml 部署值';

-- ── 历史出厂值对照表（生成）────────────────────────────────────────────

CREATE TEMP TABLE migration_history (seq integer, resource text, key text, field text, value jsonb);
INSERT INTO migration_history (seq, resource, key, field, value) VALUES
  (0, 'agent', 'evaluator', 'system_prompt', '"de0fa459f2eeab0a7181cb1c1338d29a"'::jsonb),
  (1, 'agent', 'evaluator', 'description', '"b9814e653921dad08b58b4399af1e353"'::jsonb),
  (2, 'agent', 'evaluator', 'function_tools', '["list_traffic","view_traffic","http_request","run_command","drive_browser","read_skill","replay_for_verification"]'::jsonb),
  (3, 'agent', 'evaluator', 'cli_tools', '["*"]'::jsonb),
  (4, 'agent', 'evaluator', 'skills', '["*"]'::jsonb),
  (5, 'agent', 'evaluator', 'max_iterations', '100'::jsonb),
  (6, 'agent', 'evaluator', 'complexity', '"complex"'::jsonb),
  (7, 'agent', 'executor', 'system_prompt', '"ad4692e89f6dfc24390d04a7217c844f"'::jsonb),
  (8, 'agent', 'executor', 'description', '"84e2fb8773b5746e6a558d7ad5838278"'::jsonb),
  (9, 'agent', 'executor', 'function_tools', '["read_credentials","write_credential","search_corpus","list_traffic","view_traffic","http_request","run_command","drive_browser","read_skill"]'::jsonb),
  (10, 'agent', 'executor', 'cli_tools', '["*"]'::jsonb),
  (11, 'agent', 'executor', 'skills', '["*"]'::jsonb),
  (12, 'agent', 'executor', 'max_iterations', '200'::jsonb),
  (13, 'agent', 'executor', 'complexity', '"medium"'::jsonb),
  (14, 'agent', 'monitor', 'system_prompt', '"2f2ebd721e55c47627723275e635f197"'::jsonb),
  (15, 'agent', 'monitor', 'description', '"9c3289600ea55a01e1fad5a8745ee3cf"'::jsonb),
  (16, 'agent', 'monitor', 'function_tools', '["get_global_state","publish_decision"]'::jsonb),
  (17, 'agent', 'monitor', 'cli_tools', '[]'::jsonb),
  (18, 'agent', 'monitor', 'skills', '[]'::jsonb),
  (19, 'agent', 'monitor', 'max_iterations', '30'::jsonb),
  (20, 'agent', 'monitor', 'complexity', '"medium"'::jsonb),
  (21, 'agent', 'planner', 'system_prompt', '"b1752b0bac26692df736fea8285c04df"'::jsonb),
  (22, 'agent', 'planner', 'description', '"f938949b7900ac978b9ec256cb85ffdf"'::jsonb),
  (23, 'agent', 'planner', 'function_tools', '["observe_state","evaluate_progress","search_corpus"]'::jsonb),
  (24, 'agent', 'planner', 'cli_tools', '[]'::jsonb),
  (25, 'agent', 'planner', 'skills', '[]'::jsonb),
  (26, 'agent', 'planner', 'max_iterations', '50'::jsonb),
  (27, 'agent', 'planner', 'complexity', '"complex"'::jsonb),
  (28, 'agent', 'evaluator', 'system_prompt', '"fd48fa13c4e070c33af48cc02f479bc4"'::jsonb),
  (29, 'agent', 'executor', 'system_prompt', '"20330c6225afe24f12962b3bb898582e"'::jsonb),
  (30, 'agent', 'monitor', 'system_prompt', '"355040a8cca782681b7f8ea859213cb8"'::jsonb),
  (31, 'agent', 'planner', 'system_prompt', '"94c769547d5bb952106f67af3ff4ae36"'::jsonb),
  (32, 'agent', 'evaluator', 'system_prompt', '"96db2f99bc61356e56bf2d2f4539a8fb"'::jsonb),
  (33, 'agent', 'executor', 'system_prompt', '"4f5c42178dffc9f7ca8afc47e744b4c6"'::jsonb),
  (34, 'agent', 'monitor', 'system_prompt', '"8e348b6f32c2d1050f703283927eb396"'::jsonb),
  (35, 'agent', 'planner', 'system_prompt', '"4b2da441ee6e829fbb80229e48132c32"'::jsonb),
  (36, 'agent', 'planner', 'system_prompt', '"a36356d3fcf8ad53258fa46adbb7dfc7"'::jsonb),
  (37, 'agent', 'executor', 'system_prompt', '"276b3d91a8cb7ac20b9c02c71b153e28"'::jsonb),
  (38, 'agent', 'planner', 'system_prompt', '"6ee3879e1fbd16fb2ab9b5b651f93f3e"'::jsonb),
  (39, 'agent', 'planner', 'function_tools', '["observe_state","evaluate_progress"]'::jsonb),
  (40, 'agent', 'executor', 'system_prompt', '"137358b27b78a347e80318f2edf6937e"'::jsonb),
  (41, 'agent', 'evaluator', 'system_prompt', '"dc3dca4414719cd08895b239cf11e271"'::jsonb),
  (42, 'agent', 'evaluator', 'function_tools', '["list_traffic","view_traffic","http_request","run_command","drive_browser","read_skill","replay_for_verification","read_insights","write_insight"]'::jsonb),
  (43, 'agent', 'executor', 'system_prompt', '"48df420fbb2a3cf48dcbc85d5049e925"'::jsonb),
  (44, 'agent', 'executor', 'function_tools', '["read_credentials","write_credential","search_corpus","read_insights","write_insight","list_traffic","view_traffic","http_request","run_command","drive_browser","read_skill"]'::jsonb),
  (45, 'agent', 'evaluator', 'system_prompt', '"bffe292dd9ce95f2a2758066baafd019"'::jsonb),
  (46, 'agent', 'executor', 'system_prompt', '"2f92a9bf2e883fc4763484b1beef09cc"'::jsonb),
  (47, 'agent', 'monitor', 'system_prompt', '"315df8fe9dd1039f5cceb4f951aa33d4"'::jsonb),
  (48, 'agent', 'planner', 'system_prompt', '"bd7601696c2fdd2c043618e7843748cd"'::jsonb),
  (49, 'agent', 'monitor', 'system_prompt', '"474d4a9cf7c7edd821416d2d5e5b1570"'::jsonb),
  (50, 'agent', 'executor', 'system_prompt', '"8602fbfc9d9d676202d40871eb33f40e"'::jsonb),
  (51, 'agent', 'executor', 'system_prompt', '"f87328d648d5045f2de410e82c6e2a75"'::jsonb),
  (52, 'agent', 'evaluator', 'cli_tools', '["ROPgadget","RsaCtfTool","SSRFmap","SSTImap","afl-fuzz","angr","arjun","binwalk","bloodhound-python","browser-use","certipy","checkov","checksec","chisel","cloudfox","cloudsplaining","commix","curl","dalfox","evil-winrm","exiftool","fcrackzip","feroxbuster","ffuf","foremost","fscan","gau","gcc","gdb","ghidra","go","hash-identifier","hashcat","httpx","hydra","impacket-secretsdump","interactsh-client","java","john","jq","jwt_tool","katana","kube-bench","kube-hunter","kubectl","ligolo-ng","linpeas","masscan","msfconsole","mysql","netexec","nikto","nmap","node","nuclei","one_gadget","pacu","paramspider","patchelf","peirates","php","phpggc","prowler","proxychains4","pwninit","python3","radare2","responder","ruby","seccomp-tools","semgrep","sliver","spectral","sqlmap","steghide","stegoveritas","stegseek","strace","subfinder","testdisk","trivy","trufflehog","tshark","vol","wafw00f","wpscan","ysomap","ysoserial","zsteg"]'::jsonb),
  (53, 'agent', 'evaluator', 'skills', '["bac","browser-use","dom-xss"]'::jsonb),
  (54, 'agent', 'executor', 'cli_tools', '["ROPgadget","RsaCtfTool","SSRFmap","SSTImap","afl-fuzz","angr","arjun","binwalk","bloodhound-python","browser-use","certipy","checkov","checksec","chisel","cloudfox","cloudsplaining","commix","curl","dalfox","evil-winrm","exiftool","fcrackzip","feroxbuster","ffuf","foremost","fscan","gau","gcc","gdb","ghidra","go","hash-identifier","hashcat","httpx","hydra","impacket-secretsdump","interactsh-client","java","john","jq","jwt_tool","katana","kube-bench","kube-hunter","kubectl","ligolo-ng","linpeas","masscan","msfconsole","mysql","netexec","nikto","nmap","node","nuclei","one_gadget","pacu","paramspider","patchelf","peirates","php","phpggc","prowler","proxychains4","pwninit","python3","radare2","responder","ruby","seccomp-tools","semgrep","sliver","spectral","sqlmap","steghide","stegoveritas","stegseek","strace","subfinder","testdisk","trivy","trufflehog","tshark","vol","wafw00f","wpscan","ysomap","ysoserial","zsteg"]'::jsonb),
  (55, 'agent', 'executor', 'skills', '["bac","browser-use","dom-xss"]'::jsonb),
  (56, 'agent', 'monitor', 'system_prompt', '"7eb564e44aff6993767355f1d55336b2"'::jsonb),
  (57, 'agent', 'evaluator', 'system_prompt', '"00d8d51d024ca499a63043d899cf7cfd"'::jsonb),
  (58, 'agent', 'executor', 'system_prompt', '"d357e61035f79b940ec52613a8614aa3"'::jsonb),
  (59, 'agent', 'executor', 'system_prompt', '"f225f5368f897b3948f25b67023b05b6"'::jsonb),
  (60, 'agent', 'executor', 'system_prompt', '"625ae0b911563ef48e85d853fdd2e777"'::jsonb),
  (61, 'agent', 'executor', 'system_prompt', '"cda997b03ddc06446abfbff0ccc2b756"'::jsonb),
  (62, 'agent', 'executor', 'system_prompt', '"187bda69cde1bf1f3cc3acdfe6369fbc"'::jsonb),
  (63, 'agent', 'executor', 'system_prompt', '"d095a6d588f4062e9d73b8383042961f"'::jsonb),
  (64, 'agent', 'evaluator', 'system_prompt', '"08ebea6551cddc88bf0484738b61876a"'::jsonb),
  (65, 'agent', 'evaluator', 'system_prompt', '"16facf08e18d50ae0cc5e30b39e74bea"'::jsonb),
  (66, 'agent', 'executor', 'system_prompt', '"cdc56e8d7ced6d58c52c1a0e2c240c07"'::jsonb),
  (67, 'agent', 'evaluator', 'system_prompt', '"936bc1fd4a54a38de20d2bdf504e5cde"'::jsonb),
  (68, 'agent', 'evaluator', 'max_iterations', '50'::jsonb),
  (69, 'agent', 'executor', 'system_prompt', '"b2d5c3ad9d94424be9ad5d17012afda6"'::jsonb),
  (70, 'agent', 'executor', 'max_iterations', '100'::jsonb),
  (71, 'agent', 'monitor', 'system_prompt', '"3e62d2e15ed376fd6fdf8c58e6199054"'::jsonb),
  (72, 'agent', 'monitor', 'max_iterations', '20'::jsonb),
  (73, 'agent', 'planner', 'system_prompt', '"45399e0974311e7259e713a5fcfdffa9"'::jsonb),
  (74, 'agent', 'planner', 'max_iterations', '100'::jsonb),
  (75, 'agent', 'evaluator', 'max_iterations', '30'::jsonb),
  (76, 'agent', 'executor', 'system_prompt', '"ed0e59d7236afb345fc5c58dbeac8242"'::jsonb),
  (77, 'agent', 'executor', 'function_tools', '["read_credentials","write_credential","search_corpus","read_insights","write_insight","list_traffic","view_traffic","http_request","run_command","drive_browser","read_skill","write_observation"]'::jsonb),
  (78, 'agent', 'executor', 'max_iterations', '50'::jsonb),
  (79, 'agent', 'monitor', 'max_iterations', '10'::jsonb),
  (80, 'agent', 'evaluator', 'system_prompt', '"6dab0210ca44049f47d1ba72773d75ea"'::jsonb),
  (81, 'agent', 'evaluator', 'description', '"bbb247695d28eba6e06c4c187d2c2b2a"'::jsonb),
  (82, 'agent', 'evaluator', 'function_tools', '["replay_for_verification","run_command"]'::jsonb),
  (83, 'agent', 'evaluator', 'cli_tools', '["ROPgadget","RsaCtfTool","SSRFmap","SSTImap","afl-fuzz","angr","arjun","binwalk","bloodhound-python","browser-use","certipy","checkov","checksec","chisel","cloudfox","cloudsplaining","commix","curl","dalfox","evil-winrm","exiftool","fcrackzip","feroxbuster","ffuf","foremost","fscan","gau","gcc","gdb","ghidra","go","hash-identifier","hashcat","httpx","hydra","impacket-secretsdump","interactsh-client","java","john","jq","jwt_tool","katana","kube-bench","kube-hunter","kubectl","ligolo-ng","linpeas","masscan","msfconsole","mysql","netexec","nikto","nmap","node","nuclei","one_gadget","pacu","paramspider","patchelf","peirates","php","phpggc","prowler","proxychains4","pwninit","python3","radare2","responder","ruby","seccomp-tools","semgrep","sliver","spectral","sqlmap","steghide","stegoveritas","stegseek","strace","subfinder","testdisk","trivy","trufflehog","trufflehog","tshark","vol","wafw00f","wpscan","ysomap","ysoserial","zsteg"]'::jsonb),
  (84, 'agent', 'evaluator', 'skills', '["evidence-verification","claim-analysis"]'::jsonb),
  (85, 'agent', 'evaluator', 'complexity', '"medium"'::jsonb),
  (86, 'agent', 'executor', 'system_prompt', '"97a586bec78b550d767cec08cb816b67"'::jsonb),
  (87, 'agent', 'executor', 'description', '"a5e3ac0f095bbc4065fefcf58dd8c45b"'::jsonb),
  (88, 'agent', 'executor', 'function_tools', '["done","mark_insight","read_credentials","write_credential","read_findings","update_finding","search_corpus","write_corpus","write_insight","list_traffic","view_traffic","replay_traffic","http_request","run_command","browser_use","read_tooling_skill","read_vuln_skill","write_observation","write_evidence"]'::jsonb),
  (89, 'agent', 'executor', 'cli_tools', '["ROPgadget","RsaCtfTool","SSRFmap","SSTImap","afl-fuzz","angr","arjun","binwalk","bloodhound-python","browser-use","certipy","checkov","checksec","chisel","cloudfox","cloudsplaining","commix","curl","dalfox","evil-winrm","exiftool","fcrackzip","feroxbuster","ffuf","foremost","fscan","gau","gcc","gdb","ghidra","go","hash-identifier","hashcat","httpx","hydra","impacket-secretsdump","interactsh-client","java","john","jq","jwt_tool","katana","kube-bench","kube-hunter","kubectl","ligolo-ng","linpeas","masscan","msfconsole","mysql","netexec","nikto","nmap","node","nuclei","one_gadget","pacu","paramspider","patchelf","peirates","php","phpggc","prowler","proxychains4","pwninit","python3","radare2","responder","ruby","seccomp-tools","semgrep","sliver","spectral","sqlmap","steghide","stegoveritas","stegseek","strace","subfinder","testdisk","trivy","trufflehog","trufflehog","tshark","vol","wafw00f","wpscan","ysomap","ysoserial","zsteg"]'::jsonb),
  (90, 'agent', 'executor', 'skills', '[]'::jsonb),
  (91, 'agent', 'executor', 'max_iterations', '40'::jsonb),
  (92, 'agent', 'monitor', 'system_prompt', '"c710487d611c9db9ea0a6796e4a2db12"'::jsonb),
  (93, 'agent', 'monitor', 'description', '"26a7510f0b3de03dba620c1608059d06"'::jsonb),
  (94, 'agent', 'planner', 'system_prompt', '"f2d2b9149c1b5deaef5f30ecb7b64d84"'::jsonb),
  (95, 'agent', 'planner', 'description', '"cb2e875cf853c6a71425dff43884744b"'::jsonb),
  (96, 'agent', 'planner', 'complexity', '"medium"'::jsonb),
  (97, 'agent', 'executor', 'system_prompt', '"88a75bf798703e7b8701848a27d58323"'::jsonb),
  (98, 'agent', 'executor', 'system_prompt', '"515d16448399c243442c519196347523"'::jsonb),
  (99, 'agent', 'evaluator', 'function_tools', '["replay_for_verification","run_command","replay_traffic"]'::jsonb),
  (100, 'agent', 'executor', 'system_prompt', '"9390cbe75b3074026db12ac9763aa992"'::jsonb),
  (101, 'agent', 'evaluator', 'function_tools', '["replay_for_verification","run_command","list_traffic","view_traffic","replay_traffic"]'::jsonb),
  (102, 'agent', 'executor', 'system_prompt', '"f70f2f65191a683cb73e5c351e247e60"'::jsonb),
  (103, 'agent', 'executor', 'system_prompt', '"75dce9d4b2e4358a80b7807d4b3899fd"'::jsonb),
  (104, 'agent', 'evaluator', 'function_tools', '["write_evaluation","write_result","read_exploration_graph","replay_for_verification"]'::jsonb),
  (105, 'agent', 'evaluator', 'cli_tools', '[]'::jsonb),
  (106, 'agent', 'executor', 'function_tools', '["http_request","parse_html","execute_js","extract_data"]'::jsonb),
  (107, 'agent', 'executor', 'cli_tools', '["curl","sqlmap","nikto","nmap","gobuster","ffuf"]'::jsonb),
  (108, 'agent', 'executor', 'skills', '["tooling/browser-use","vuln/dom-xss"]'::jsonb),
  (109, 'agent', 'monitor', 'function_tools', '[]'::jsonb),
  (110, 'agent', 'planner', 'function_tools', '[]'::jsonb),
  (111, 'agent', 'evaluator', 'system_prompt', '"d67b89617d21039451f9e00bc9d7c191"'::jsonb),
  (112, 'agent', 'evaluator', 'function_tools', '["write_evaluation","write_result","read_knowledge_graph","replay_for_verification"]'::jsonb),
  (113, 'agent', 'planner', 'system_prompt', '"4ea320c9c19780055346cc87c35a7d41"'::jsonb),
  (114, 'agent', 'evaluator', 'skills', '[]'::jsonb),
  (115, 'skill', 'browser-use', 'name', '"7df48439ef2d6991ac4b597cff0b5449"'::jsonb),
  (116, 'skill', 'browser-use', 'description', '"3b01cb5e4de2c92132dcd64df795b38b"'::jsonb),
  (117, 'skill', 'browser-use', 'body', '"5714b0699efae321f4e561e16e7f98e3"'::jsonb),
  (118, 'skill', 'bac', 'name', '"15ab859caa695ba2151fe794c3497af1"'::jsonb),
  (119, 'skill', 'bac', 'description', '"4d320fbd8693064ea40f6f39c57b87ac"'::jsonb),
  (120, 'skill', 'bac', 'body', '"a276364c8062af9e21b74617277621dd"'::jsonb),
  (121, 'skill', 'tool/browser-use', 'name', '"7df48439ef2d6991ac4b597cff0b5449"'::jsonb),
  (122, 'skill', 'tool/browser-use', 'description', '"3b01cb5e4de2c92132dcd64df795b38b"'::jsonb),
  (123, 'skill', 'tool/browser-use', 'body', '"5714b0699efae321f4e561e16e7f98e3"'::jsonb),
  (124, 'skill', 'vuln/bac', 'name', '"15ab859caa695ba2151fe794c3497af1"'::jsonb),
  (125, 'skill', 'vuln/bac', 'description', '"4d320fbd8693064ea40f6f39c57b87ac"'::jsonb),
  (126, 'skill', 'vuln/bac', 'body', '"a276364c8062af9e21b74617277621dd"'::jsonb),
  (127, 'skill', 'bac', 'description', '"86af3fa359fb52d1b6f169b820adcc8e"'::jsonb),
  (128, 'skill', 'bac', 'body', '"f7259dc2cffc20c9d78a4d14cdd7f6eb"'::jsonb),
  (129, 'skill', 'vuln/bac', 'description', '"86af3fa359fb52d1b6f169b820adcc8e"'::jsonb),
  (130, 'skill', 'vuln/bac', 'body', '"f7259dc2cffc20c9d78a4d14cdd7f6eb"'::jsonb),
  (131, 'skill', 'browser-use', 'body', '"87b1dd51e2284b0da69562ddfe3cecd7"'::jsonb),
  (132, 'skill', 'tool/browser-use', 'body', '"87b1dd51e2284b0da69562ddfe3cecd7"'::jsonb),
  (133, 'skill', 'bac', 'description', '"e1d87fe3ee2e2c69174d716bdaceefac"'::jsonb),
  (134, 'skill', 'bac', 'body', '"90196f0581bb6685cffcee17ba159479"'::jsonb),
  (135, 'skill', 'vuln/bac', 'description', '"e1d87fe3ee2e2c69174d716bdaceefac"'::jsonb),
  (136, 'skill', 'vuln/bac', 'body', '"90196f0581bb6685cffcee17ba159479"'::jsonb),
  (137, 'skill', 'browser-use', 'description', '"7094a1ce1fc2ea4c5b3d7076436aa3f7"'::jsonb),
  (138, 'skill', 'browser-use', 'body', '"a4e20a50f870fc5fa6ccdfba3d048923"'::jsonb),
  (139, 'skill', 'tool/browser-use', 'description', '"7094a1ce1fc2ea4c5b3d7076436aa3f7"'::jsonb),
  (140, 'skill', 'tool/browser-use', 'body', '"a4e20a50f870fc5fa6ccdfba3d048923"'::jsonb),
  (141, 'skill', 'bac', 'description', '"b453b1ddaebbea49f554b19c9815a054"'::jsonb),
  (142, 'skill', 'bac', 'body', '"79c7d79df1e44ce09fa6b7e4aca3bd36"'::jsonb),
  (143, 'skill', 'vuln/bac', 'description', '"b453b1ddaebbea49f554b19c9815a054"'::jsonb),
  (144, 'skill', 'vuln/bac', 'body', '"79c7d79df1e44ce09fa6b7e4aca3bd36"'::jsonb),
  (145, 'skill', 'dom-xss', 'name', '"368aa8deee5c84bbe19605e4fcbf9e64"'::jsonb),
  (146, 'skill', 'dom-xss', 'description', '"727208ce459189b13154430513fe90ea"'::jsonb),
  (147, 'skill', 'dom-xss', 'body', '"7088ddf210b33accbc67fe4321161763"'::jsonb),
  (148, 'skill', 'vuln/dom-xss', 'name', '"368aa8deee5c84bbe19605e4fcbf9e64"'::jsonb),
  (149, 'skill', 'vuln/dom-xss', 'description', '"727208ce459189b13154430513fe90ea"'::jsonb),
  (150, 'skill', 'vuln/dom-xss', 'body', '"7088ddf210b33accbc67fe4321161763"'::jsonb),
  (151, 'skill', 'tooling/browser-use', 'name', '"7df48439ef2d6991ac4b597cff0b5449"'::jsonb),
  (152, 'skill', 'tooling/browser-use', 'description', '"7094a1ce1fc2ea4c5b3d7076436aa3f7"'::jsonb),
  (153, 'skill', 'tooling/browser-use', 'body', '"a4e20a50f870fc5fa6ccdfba3d048923"'::jsonb),
  (154, 'skill', 'bac', 'body', '"53bbc08659d9a8eb3ffe52547036b518"'::jsonb),
  (155, 'skill', 'vuln/bac', 'body', '"53bbc08659d9a8eb3ffe52547036b518"'::jsonb),
  (156, 'skill', 'dom-xss', 'body', '"1cea98477abd789d9d5b6fcbd91b1f6f"'::jsonb),
  (157, 'skill', 'vuln/dom-xss', 'body', '"1cea98477abd789d9d5b6fcbd91b1f6f"'::jsonb),
  (158, 'skill', 'browser-use', 'description', '"f0cf5f1aaa0b347a2c66506689a09419"'::jsonb),
  (159, 'skill', 'browser-use', 'body', '"786e252a34b70cc378c7a3ef1bec6e49"'::jsonb),
  (160, 'skill', 'tooling/browser-use', 'description', '"f0cf5f1aaa0b347a2c66506689a09419"'::jsonb),
  (161, 'skill', 'tooling/browser-use', 'body', '"786e252a34b70cc378c7a3ef1bec6e49"'::jsonb),
  (162, 'skill', 'bac', 'body', '"6b424a586e2079814f42cceb955a1234"'::jsonb),
  (163, 'skill', 'vuln/bac', 'body', '"6b424a586e2079814f42cceb955a1234"'::jsonb),
  (164, 'skill', 'dom-xss', 'body', '"8d406e270f471a9ad060d53a78ddba52"'::jsonb),
  (165, 'skill', 'vuln/dom-xss', 'body', '"8d406e270f471a9ad060d53a78ddba52"'::jsonb),
  (166, 'skill', 'browser-use', 'body', '"2e580f6ef9290be96d5873c127bc49ac"'::jsonb),
  (167, 'skill', 'tooling/browser-use', 'body', '"2e580f6ef9290be96d5873c127bc49ac"'::jsonb),
  (168, 'skill', 'browser-use', 'name', '"b44ac0bffe17354e4e6a8fdd1007caa3"'::jsonb),
  (169, 'skill', 'tooling/browser-use', 'name', '"b44ac0bffe17354e4e6a8fdd1007caa3"'::jsonb),
  (170, 'skill', 'bac', 'name', '"79ec16df80b57696a03bb364410061f3"'::jsonb),
  (171, 'skill', 'vuln/bac', 'name', '"79ec16df80b57696a03bb364410061f3"'::jsonb),
  (172, 'skill', 'dom-xss', 'name', '"60140feb86966d0b8807834b2e6af317"'::jsonb),
  (173, 'skill', 'vuln/dom-xss', 'name', '"60140feb86966d0b8807834b2e6af317"'::jsonb),
  (174, 'skill', 'browser-use', 'body', '"b63feab26578ad0b0521d22e184907a5"'::jsonb),
  (175, 'skill', 'tooling/browser-use', 'body', '"b63feab26578ad0b0521d22e184907a5"'::jsonb),
  (176, 'skill', 'bac', 'body', '"ef7391333ebc2d5949a13518c6f4c0cb"'::jsonb),
  (177, 'skill', 'vuln/bac', 'body', '"ef7391333ebc2d5949a13518c6f4c0cb"'::jsonb),
  (178, 'skill', 'bac', 'body', '"62958dbbd6d297d8cf4f88c072ed6238"'::jsonb),
  (179, 'skill', 'vuln/bac', 'body', '"62958dbbd6d297d8cf4f88c072ed6238"'::jsonb),
  (180, 'skill', 'bac', 'description', '"57cd22740c3dcccfec6a945a2506c1af"'::jsonb),
  (181, 'skill', 'bac', 'body', '"a425e6b7f600ae59485fa0f7c3187b53"'::jsonb),
  (182, 'skill', 'vuln/bac', 'description', '"57cd22740c3dcccfec6a945a2506c1af"'::jsonb),
  (183, 'skill', 'vuln/bac', 'body', '"a425e6b7f600ae59485fa0f7c3187b53"'::jsonb),
  (184, 'skill', 'browser-use', 'body', '"ff61f220faeeedc75b9fa7b8dc931adb"'::jsonb),
  (185, 'skill', 'tooling/browser-use', 'body', '"ff61f220faeeedc75b9fa7b8dc931adb"'::jsonb),
  (186, 'skill', 'bac', 'body', '"9df0f7b8609cbbe49f3155c9df153eef"'::jsonb),
  (187, 'skill', 'vuln/bac', 'body', '"9df0f7b8609cbbe49f3155c9df153eef"'::jsonb),
  (188, 'skill', 'browser-use', 'body', '"e93e75fe40ffb000b886c344c21f8dc1"'::jsonb),
  (189, 'skill', 'tooling/browser-use', 'body', '"e93e75fe40ffb000b886c344c21f8dc1"'::jsonb),
  (190, 'skill', 'bac', 'body', '"3b81dbbc9907f67a50471b98766cd2c9"'::jsonb),
  (191, 'skill', 'vuln/bac', 'body', '"3b81dbbc9907f67a50471b98766cd2c9"'::jsonb),
  (192, 'skill', 'browser-use', 'body', '"c2501523ed2db8f4a88c98842fca0f38"'::jsonb),
  (193, 'skill', 'tooling/browser-use', 'body', '"c2501523ed2db8f4a88c98842fca0f38"'::jsonb),
  (194, 'skill', 'bac', 'body', '"b2a02f9b71c9cf575f70e61a8d465c61"'::jsonb),
  (195, 'skill', 'vuln/bac', 'body', '"b2a02f9b71c9cf575f70e61a8d465c61"'::jsonb),
  (196, 'skill', 'browser-use', 'body', '"1a2e4b55245d3b24d028feabddff91d2"'::jsonb),
  (197, 'skill', 'tooling/browser-use', 'body', '"1a2e4b55245d3b24d028feabddff91d2"'::jsonb),
  (198, 'skill', 'bac', 'body', '"08a59a1c28dba668113877359fca6d1a"'::jsonb),
  (199, 'skill', 'vuln/bac', 'body', '"08a59a1c28dba668113877359fca6d1a"'::jsonb),
  (200, 'skill', 'bac', 'description', '"65014fe6badd12d631a183854bd2d7bd"'::jsonb),
  (201, 'skill', 'bac', 'body', '"b9bbb14f505e549742e226dfde327387"'::jsonb),
  (202, 'skill', 'vuln/bac', 'description', '"65014fe6badd12d631a183854bd2d7bd"'::jsonb),
  (203, 'skill', 'vuln/bac', 'body', '"b9bbb14f505e549742e226dfde327387"'::jsonb),
  (204, 'skill', 'browser-use', 'body', '"5b9b261b69df18380aca0d9582ca51d9"'::jsonb),
  (205, 'skill', 'tooling/browser-use', 'body', '"5b9b261b69df18380aca0d9582ca51d9"'::jsonb),
  (206, 'skill', 'browser-use', 'body', '"115385115f04f2b93a21d133a4a21fcc"'::jsonb),
  (207, 'skill', 'tooling/browser-use', 'body', '"115385115f04f2b93a21d133a4a21fcc"'::jsonb),
  (208, 'skill', 'browser-use', 'body', '"70affcf403489a8d47b015dbf02b60b5"'::jsonb),
  (209, 'skill', 'tooling/browser-use', 'body', '"70affcf403489a8d47b015dbf02b60b5"'::jsonb),
  (210, 'skill', 'browser-use', 'body', '"fdf193229cd30b799865a32f5d07f3db"'::jsonb),
  (211, 'skill', 'tooling/browser-use', 'body', '"fdf193229cd30b799865a32f5d07f3db"'::jsonb),
  (212, 'skill', 'browser-use', 'body', '"8352ab73fe5f0db9e87dae70b370b0b9"'::jsonb),
  (213, 'skill', 'tooling/browser-use', 'body', '"8352ab73fe5f0db9e87dae70b370b0b9"'::jsonb),
  (214, 'skill', 'curl', 'name', '"f6e57c9de709e45feb0d955351f53548"'::jsonb),
  (215, 'skill', 'curl', 'description', '"9e120ada1b48d44a32b73ca107c80363"'::jsonb),
  (216, 'skill', 'curl', 'body', '"3f68abc8c1f8dd8de63d61eb9b177a7a"'::jsonb),
  (217, 'skill', 'tooling/curl', 'name', '"f6e57c9de709e45feb0d955351f53548"'::jsonb),
  (218, 'skill', 'tooling/curl', 'description', '"9e120ada1b48d44a32b73ca107c80363"'::jsonb),
  (219, 'skill', 'tooling/curl', 'body', '"3f68abc8c1f8dd8de63d61eb9b177a7a"'::jsonb),
  (220, 'skill', 'sqlmap', 'name', '"da3bcb630536a3d32bc8db4adb4de1a0"'::jsonb),
  (221, 'skill', 'sqlmap', 'description', '"96ccc4fa6fa995bdf3945aa327f7c748"'::jsonb),
  (222, 'skill', 'sqlmap', 'body', '"5900ab6c4460f8587b267a306c1b0bed"'::jsonb),
  (223, 'skill', 'tooling/sqlmap', 'name', '"da3bcb630536a3d32bc8db4adb4de1a0"'::jsonb),
  (224, 'skill', 'tooling/sqlmap', 'description', '"96ccc4fa6fa995bdf3945aa327f7c748"'::jsonb),
  (225, 'skill', 'tooling/sqlmap', 'body', '"5900ab6c4460f8587b267a306c1b0bed"'::jsonb),
  (226, 'skill', 'hunter', 'name', '"6b1b36cbb04b41490bfc0ab2bfa26f86"'::jsonb),
  (227, 'skill', 'hunter', 'description', '"037bc5c75388b6b4a955ff46c29e7a0b"'::jsonb),
  (228, 'skill', 'hunter', 'body', '"ec7e68d070bf74611d23752b2fe426b1"'::jsonb),
  (229, 'skill', 'skills/hunter', 'name', '"6b1b36cbb04b41490bfc0ab2bfa26f86"'::jsonb),
  (230, 'skill', 'skills/hunter', 'description', '"037bc5c75388b6b4a955ff46c29e7a0b"'::jsonb),
  (231, 'skill', 'skills/hunter', 'body', '"ec7e68d070bf74611d23752b2fe426b1"'::jsonb),
  (232, 'skill', 'hunter', 'body', '"f9a1447af9b0fcc0aa5fe8054ccc1190"'::jsonb),
  (233, 'skill', 'skills/hunter', 'body', '"f9a1447af9b0fcc0aa5fe8054ccc1190"'::jsonb),
  (234, 'skill', 'hunter', 'body', '"44d11bbf9daf42bb2a893a2159903522"'::jsonb),
  (235, 'skill', 'skills/hunter', 'body', '"44d11bbf9daf42bb2a893a2159903522"'::jsonb),
  (236, 'skill', 'hunter', 'body', '"4f62e7f04c94848d822b9fd98f1c259b"'::jsonb),
  (237, 'skill', 'skills/hunter', 'body', '"4f62e7f04c94848d822b9fd98f1c259b"'::jsonb),
  (238, 'skill', 'arjun', 'name', '"7626d28b710e7f9e98d9dfbe9bf0d123"'::jsonb),
  (239, 'skill', 'arjun', 'description', '"2c7ca3323b9dea45671892fb40803936"'::jsonb),
  (240, 'skill', 'arjun', 'body', '"635c567de23509251ad4757757c60962"'::jsonb),
  (241, 'skill', 'tooling/arjun', 'name', '"7626d28b710e7f9e98d9dfbe9bf0d123"'::jsonb),
  (242, 'skill', 'tooling/arjun', 'description', '"2c7ca3323b9dea45671892fb40803936"'::jsonb),
  (243, 'skill', 'tooling/arjun', 'body', '"635c567de23509251ad4757757c60962"'::jsonb),
  (244, 'skill', 'curl', 'description', '"3c0f382b5d970ed9121c1439069443bc"'::jsonb),
  (245, 'skill', 'curl', 'body', '"cb1c52c7ac8dd15403fd0a4409e4c6cd"'::jsonb),
  (246, 'skill', 'tooling/curl', 'description', '"3c0f382b5d970ed9121c1439069443bc"'::jsonb),
  (247, 'skill', 'tooling/curl', 'body', '"cb1c52c7ac8dd15403fd0a4409e4c6cd"'::jsonb),
  (248, 'skill', 'dalfox', 'name', '"3b7ea717d97ef17c46c3aeb2f4d6ed83"'::jsonb),
  (249, 'skill', 'dalfox', 'description', '"d4ae16555f0b29d852f10e4ecb073554"'::jsonb),
  (250, 'skill', 'dalfox', 'body', '"b12b451d8454599a817e14d0dc1ccf14"'::jsonb),
  (251, 'skill', 'tooling/dalfox', 'name', '"3b7ea717d97ef17c46c3aeb2f4d6ed83"'::jsonb),
  (252, 'skill', 'tooling/dalfox', 'description', '"d4ae16555f0b29d852f10e4ecb073554"'::jsonb),
  (253, 'skill', 'tooling/dalfox', 'body', '"b12b451d8454599a817e14d0dc1ccf14"'::jsonb),
  (254, 'skill', 'dirsearch', 'name', '"933c2f062e01627f74165c48aaae3394"'::jsonb),
  (255, 'skill', 'dirsearch', 'description', '"b412c45032ae107d78d59a9cded85ab4"'::jsonb),
  (256, 'skill', 'dirsearch', 'body', '"511c3bd4e4509efc9d01cc2f0a3a33bf"'::jsonb),
  (257, 'skill', 'tooling/dirsearch', 'name', '"933c2f062e01627f74165c48aaae3394"'::jsonb),
  (258, 'skill', 'tooling/dirsearch', 'description', '"b412c45032ae107d78d59a9cded85ab4"'::jsonb),
  (259, 'skill', 'tooling/dirsearch', 'body', '"511c3bd4e4509efc9d01cc2f0a3a33bf"'::jsonb),
  (260, 'skill', 'ffuf', 'name', '"6d829b1c7adeb008b882d0995fd2ac23"'::jsonb),
  (261, 'skill', 'ffuf', 'description', '"9fb5a5d2657a904ecd5ea156eb987644"'::jsonb),
  (262, 'skill', 'ffuf', 'body', '"032208782e0d92ca7de0f3fcf8d9f172"'::jsonb),
  (263, 'skill', 'tooling/ffuf', 'name', '"6d829b1c7adeb008b882d0995fd2ac23"'::jsonb),
  (264, 'skill', 'tooling/ffuf', 'description', '"9fb5a5d2657a904ecd5ea156eb987644"'::jsonb),
  (265, 'skill', 'tooling/ffuf', 'body', '"032208782e0d92ca7de0f3fcf8d9f172"'::jsonb),
  (266, 'skill', 'httpx', 'name', '"8fc910640414197ba650d6f9e124c7fd"'::jsonb),
  (267, 'skill', 'httpx', 'description', '"fc7b21fde86a786fe652d08521acd210"'::jsonb),
  (268, 'skill', 'httpx', 'body', '"ce6d1602bad279df05f224117b1f1e98"'::jsonb),
  (269, 'skill', 'tooling/httpx', 'name', '"8fc910640414197ba650d6f9e124c7fd"'::jsonb),
  (270, 'skill', 'tooling/httpx', 'description', '"fc7b21fde86a786fe652d08521acd210"'::jsonb),
  (271, 'skill', 'tooling/httpx', 'body', '"ce6d1602bad279df05f224117b1f1e98"'::jsonb),
  (272, 'skill', 'hydra', 'name', '"84366803777e6ba127f4668d0a6fcb96"'::jsonb),
  (273, 'skill', 'hydra', 'description', '"273445eb2e27cdcf714873e38b4f5d3b"'::jsonb),
  (274, 'skill', 'hydra', 'body', '"461b174c4bfebf5c1069f337f2911249"'::jsonb),
  (275, 'skill', 'tooling/hydra', 'name', '"84366803777e6ba127f4668d0a6fcb96"'::jsonb),
  (276, 'skill', 'tooling/hydra', 'description', '"273445eb2e27cdcf714873e38b4f5d3b"'::jsonb),
  (277, 'skill', 'tooling/hydra', 'body', '"461b174c4bfebf5c1069f337f2911249"'::jsonb),
  (278, 'skill', 'interactsh-client', 'name', '"c38771967c8e6c7c085da702cca66f27"'::jsonb),
  (279, 'skill', 'interactsh-client', 'description', '"9e695b5ed1b53fa4dd85c9a2af77da1d"'::jsonb),
  (280, 'skill', 'interactsh-client', 'body', '"a0a1b60af85477821f030b242e91a815"'::jsonb),
  (281, 'skill', 'tooling/interactsh-client', 'name', '"c38771967c8e6c7c085da702cca66f27"'::jsonb),
  (282, 'skill', 'tooling/interactsh-client', 'description', '"9e695b5ed1b53fa4dd85c9a2af77da1d"'::jsonb),
  (283, 'skill', 'tooling/interactsh-client', 'body', '"a0a1b60af85477821f030b242e91a815"'::jsonb),
  (284, 'skill', 'jq', 'name', '"d8757fb4e9ea07606d23a1ef5a4b8d4e"'::jsonb),
  (285, 'skill', 'jq', 'description', '"e92a1fb68953e5c8b119baa49d599c0a"'::jsonb),
  (286, 'skill', 'jq', 'body', '"a1a691fa22b41f9389ea57646c0912c4"'::jsonb),
  (287, 'skill', 'tooling/jq', 'name', '"d8757fb4e9ea07606d23a1ef5a4b8d4e"'::jsonb),
  (288, 'skill', 'tooling/jq', 'description', '"e92a1fb68953e5c8b119baa49d599c0a"'::jsonb),
  (289, 'skill', 'tooling/jq', 'body', '"a1a691fa22b41f9389ea57646c0912c4"'::jsonb),
  (290, 'skill', 'jwt_tool', 'name', '"96b76c959f11f6ccf2eff00f6cb88fe0"'::jsonb),
  (291, 'skill', 'jwt_tool', 'description', '"83571896014338bcd0e00c013c3fe141"'::jsonb),
  (292, 'skill', 'jwt_tool', 'body', '"30a18ffa2161aff8de3f46281781222b"'::jsonb),
  (293, 'skill', 'tooling/jwt_tool', 'name', '"96b76c959f11f6ccf2eff00f6cb88fe0"'::jsonb),
  (294, 'skill', 'tooling/jwt_tool', 'description', '"83571896014338bcd0e00c013c3fe141"'::jsonb),
  (295, 'skill', 'tooling/jwt_tool', 'body', '"30a18ffa2161aff8de3f46281781222b"'::jsonb),
  (296, 'skill', 'katana', 'name', '"c6983b366e2ff8a0ab159d6778cff745"'::jsonb),
  (297, 'skill', 'katana', 'description', '"498c3e18bc95d0f930e50de74271ce97"'::jsonb),
  (298, 'skill', 'katana', 'body', '"18373d61d23b9ebe5b5a2ae9f04bab4a"'::jsonb),
  (299, 'skill', 'tooling/katana', 'name', '"c6983b366e2ff8a0ab159d6778cff745"'::jsonb),
  (300, 'skill', 'tooling/katana', 'description', '"498c3e18bc95d0f930e50de74271ce97"'::jsonb),
  (301, 'skill', 'tooling/katana', 'body', '"18373d61d23b9ebe5b5a2ae9f04bab4a"'::jsonb),
  (302, 'skill', 'nmap', 'name', '"7c0132070a0ef71d542663e9dc1f5dee"'::jsonb),
  (303, 'skill', 'nmap', 'description', '"960543ad5e5b1c471c533de265167c8e"'::jsonb),
  (304, 'skill', 'nmap', 'body', '"1ee8e36c4bebe13d1884f620ad0cd9ca"'::jsonb),
  (305, 'skill', 'tooling/nmap', 'name', '"7c0132070a0ef71d542663e9dc1f5dee"'::jsonb),
  (306, 'skill', 'tooling/nmap', 'description', '"960543ad5e5b1c471c533de265167c8e"'::jsonb),
  (307, 'skill', 'tooling/nmap', 'body', '"1ee8e36c4bebe13d1884f620ad0cd9ca"'::jsonb),
  (308, 'skill', 'nuclei', 'name', '"709b38b27304df6257a86a60df742c4c"'::jsonb),
  (309, 'skill', 'nuclei', 'description', '"231731222ffddc361426bb9e77a1e60b"'::jsonb),
  (310, 'skill', 'nuclei', 'body', '"d208d192c8afbb3deb10ae432a321842"'::jsonb),
  (311, 'skill', 'tooling/nuclei', 'name', '"709b38b27304df6257a86a60df742c4c"'::jsonb),
  (312, 'skill', 'tooling/nuclei', 'description', '"231731222ffddc361426bb9e77a1e60b"'::jsonb),
  (313, 'skill', 'tooling/nuclei', 'body', '"d208d192c8afbb3deb10ae432a321842"'::jsonb),
  (314, 'skill', 'phpggc', 'name', '"b40468ee58251876f271b669e6b91686"'::jsonb),
  (315, 'skill', 'phpggc', 'description', '"0d17101ec7dc0db1de33ff800db0303b"'::jsonb),
  (316, 'skill', 'phpggc', 'body', '"ca96c842e8967ec64a6037d7dee20056"'::jsonb),
  (317, 'skill', 'tooling/phpggc', 'name', '"b40468ee58251876f271b669e6b91686"'::jsonb),
  (318, 'skill', 'tooling/phpggc', 'description', '"0d17101ec7dc0db1de33ff800db0303b"'::jsonb),
  (319, 'skill', 'tooling/phpggc', 'body', '"ca96c842e8967ec64a6037d7dee20056"'::jsonb),
  (320, 'skill', 'python3', 'name', '"6e8efbe3a442748b716644025479aab5"'::jsonb),
  (321, 'skill', 'python3', 'description', '"1b25092bdc763573dde7c69f5020fac8"'::jsonb),
  (322, 'skill', 'python3', 'body', '"e66234287b50ef4a3ff7cab8967cf775"'::jsonb),
  (323, 'skill', 'tooling/python3', 'name', '"6e8efbe3a442748b716644025479aab5"'::jsonb),
  (324, 'skill', 'tooling/python3', 'description', '"1b25092bdc763573dde7c69f5020fac8"'::jsonb),
  (325, 'skill', 'tooling/python3', 'body', '"e66234287b50ef4a3ff7cab8967cf775"'::jsonb),
  (326, 'skill', 'semgrep', 'name', '"82fe8847acb706c97e123b721e27e0d6"'::jsonb),
  (327, 'skill', 'semgrep', 'description', '"475dfc017a5a966cbda8bc4e86c7d064"'::jsonb),
  (328, 'skill', 'semgrep', 'body', '"f56554fecdf9e8c6cea43783df01324d"'::jsonb),
  (329, 'skill', 'tooling/semgrep', 'name', '"82fe8847acb706c97e123b721e27e0d6"'::jsonb),
  (330, 'skill', 'tooling/semgrep', 'description', '"475dfc017a5a966cbda8bc4e86c7d064"'::jsonb),
  (331, 'skill', 'tooling/semgrep', 'body', '"f56554fecdf9e8c6cea43783df01324d"'::jsonb),
  (332, 'skill', 'sqlmap', 'description', '"963ec87207bf5bc5258113edaf605cd1"'::jsonb),
  (333, 'skill', 'sqlmap', 'body', '"977fe8e4ca34773d9db771d865f772d3"'::jsonb),
  (334, 'skill', 'tooling/sqlmap', 'description', '"963ec87207bf5bc5258113edaf605cd1"'::jsonb),
  (335, 'skill', 'tooling/sqlmap', 'body', '"977fe8e4ca34773d9db771d865f772d3"'::jsonb),
  (336, 'skill', 'subfinder', 'name', '"71f9655f23ee07ffad6f9212f18244e0"'::jsonb),
  (337, 'skill', 'subfinder', 'description', '"96ab9214aea07e47e6f205992f723bac"'::jsonb),
  (338, 'skill', 'subfinder', 'body', '"a990058a640b66a571a81fdf6644fc7e"'::jsonb),
  (339, 'skill', 'tooling/subfinder', 'name', '"71f9655f23ee07ffad6f9212f18244e0"'::jsonb),
  (340, 'skill', 'tooling/subfinder', 'description', '"96ab9214aea07e47e6f205992f723bac"'::jsonb),
  (341, 'skill', 'tooling/subfinder', 'body', '"a990058a640b66a571a81fdf6644fc7e"'::jsonb),
  (342, 'skill', 'trufflehog', 'name', '"b0da573967a6bd2b029db52e83d66086"'::jsonb),
  (343, 'skill', 'trufflehog', 'description', '"4de5512555e2256305e8be48c8001bca"'::jsonb),
  (344, 'skill', 'trufflehog', 'body', '"6d093fde2ebbbd32b1350267973f823b"'::jsonb),
  (345, 'skill', 'tooling/trufflehog', 'name', '"b0da573967a6bd2b029db52e83d66086"'::jsonb),
  (346, 'skill', 'tooling/trufflehog', 'description', '"4de5512555e2256305e8be48c8001bca"'::jsonb),
  (347, 'skill', 'tooling/trufflehog', 'body', '"6d093fde2ebbbd32b1350267973f823b"'::jsonb),
  (348, 'skill', 'wafw00f', 'name', '"d4c271a8cd3b03e8bb62423ba2a5a474"'::jsonb),
  (349, 'skill', 'wafw00f', 'description', '"070515cafee9504b28720bd9b64f3a9a"'::jsonb),
  (350, 'skill', 'wafw00f', 'body', '"56e088c5ff7e078d69dbdce58f4a265b"'::jsonb),
  (351, 'skill', 'tooling/wafw00f', 'name', '"d4c271a8cd3b03e8bb62423ba2a5a474"'::jsonb),
  (352, 'skill', 'tooling/wafw00f', 'description', '"070515cafee9504b28720bd9b64f3a9a"'::jsonb),
  (353, 'skill', 'tooling/wafw00f', 'body', '"56e088c5ff7e078d69dbdce58f4a265b"'::jsonb),
  (354, 'skill', 'ysoserial', 'name', '"b5f740c24bb13a917315186d08544eca"'::jsonb),
  (355, 'skill', 'ysoserial', 'description', '"d9c861bce300daf342ebe26b1332da19"'::jsonb),
  (356, 'skill', 'ysoserial', 'body', '"3e9a2d4a5b379e2aa9112481a3765898"'::jsonb),
  (357, 'skill', 'tooling/ysoserial', 'name', '"b5f740c24bb13a917315186d08544eca"'::jsonb),
  (358, 'skill', 'tooling/ysoserial', 'description', '"d9c861bce300daf342ebe26b1332da19"'::jsonb),
  (359, 'skill', 'tooling/ysoserial', 'body', '"3e9a2d4a5b379e2aa9112481a3765898"'::jsonb),
  (360, 'skill', 'brute', 'name', '"98bd98ba41e1216ad3617d890df31075"'::jsonb),
  (361, 'skill', 'brute', 'description', '"ed055fe8a814c29e81c78499254c520b"'::jsonb),
  (362, 'skill', 'brute', 'body', '"d0a7876cc2f3d9c0a7abb6e43807ba2f"'::jsonb),
  (363, 'skill', 'vuln/brute', 'name', '"98bd98ba41e1216ad3617d890df31075"'::jsonb),
  (364, 'skill', 'vuln/brute', 'description', '"ed055fe8a814c29e81c78499254c520b"'::jsonb),
  (365, 'skill', 'vuln/brute', 'body', '"d0a7876cc2f3d9c0a7abb6e43807ba2f"'::jsonb),
  (366, 'skill', 'fi', 'name', '"75778bf8fde7266d416b0089e7b8b793"'::jsonb),
  (367, 'skill', 'fi', 'description', '"6454538bb9b7b2bfc5270fd6fdbad100"'::jsonb),
  (368, 'skill', 'fi', 'body', '"79ef6b060203f2d2ca2fb9b8a8d2c072"'::jsonb),
  (369, 'skill', 'vuln/fi', 'name', '"75778bf8fde7266d416b0089e7b8b793"'::jsonb),
  (370, 'skill', 'vuln/fi', 'description', '"6454538bb9b7b2bfc5270fd6fdbad100"'::jsonb),
  (371, 'skill', 'vuln/fi', 'body', '"79ef6b060203f2d2ca2fb9b8a8d2c072"'::jsonb),
  (372, 'skill', 'sqli', 'name', '"911921841a856fc1a830dfec6e18bca2"'::jsonb),
  (373, 'skill', 'sqli', 'description', '"a2aeffa0f483571848087d7d0a83f431"'::jsonb),
  (374, 'skill', 'sqli', 'body', '"d7a949cf3133d15dd55c841c1ad8dd68"'::jsonb),
  (375, 'skill', 'vuln/sqli', 'name', '"911921841a856fc1a830dfec6e18bca2"'::jsonb),
  (376, 'skill', 'vuln/sqli', 'description', '"a2aeffa0f483571848087d7d0a83f431"'::jsonb),
  (377, 'skill', 'vuln/sqli', 'body', '"d7a949cf3133d15dd55c841c1ad8dd68"'::jsonb),
  (378, 'skill', 'upload', 'name', '"76ee3de97a1b8b903319b7c013d8c877"'::jsonb),
  (379, 'skill', 'upload', 'description', '"9989f887513cfca4a73f6835d12ad52c"'::jsonb),
  (380, 'skill', 'upload', 'body', '"cd9f987279f10a1177d3869478c65515"'::jsonb),
  (381, 'skill', 'vuln/upload', 'name', '"76ee3de97a1b8b903319b7c013d8c877"'::jsonb),
  (382, 'skill', 'vuln/upload', 'description', '"9989f887513cfca4a73f6835d12ad52c"'::jsonb),
  (383, 'skill', 'vuln/upload', 'body', '"cd9f987279f10a1177d3869478c65515"'::jsonb),
  (384, 'skill', 'xss', 'name', '"2c71e977eccffb1cfb7c6cc22e0e7595"'::jsonb),
  (385, 'skill', 'xss', 'description', '"ca005f6c32f4888a347b089f70aa2afb"'::jsonb),
  (386, 'skill', 'xss', 'body', '"bf4daf7de9c5799071c260cc33a7613e"'::jsonb),
  (387, 'skill', 'vuln/xss', 'name', '"2c71e977eccffb1cfb7c6cc22e0e7595"'::jsonb),
  (388, 'skill', 'vuln/xss', 'description', '"ca005f6c32f4888a347b089f70aa2afb"'::jsonb),
  (389, 'skill', 'vuln/xss', 'body', '"bf4daf7de9c5799071c260cc33a7613e"'::jsonb),
  (390, 'skill', 'bac', 'body', '"dd0dd5e58509e2ec7a189ee92cdfb1b5"'::jsonb),
  (391, 'skill', 'vuln/bac', 'body', '"dd0dd5e58509e2ec7a189ee92cdfb1b5"'::jsonb),
  (392, 'skill', 'bac', 'body', '"021cab4995601d978751524e4b5683d5"'::jsonb),
  (393, 'skill', 'vuln/bac', 'body', '"021cab4995601d978751524e4b5683d5"'::jsonb),
  (394, 'skill', 'bac', 'body', '"beac3dc177bf552080c0ebb7a7d7af0f"'::jsonb),
  (395, 'skill', 'vuln/bac', 'body', '"beac3dc177bf552080c0ebb7a7d7af0f"'::jsonb),
  (396, 'skill', 'arjun', 'body', '"49e2e3152385454e07d8f61ce6dbecc0"'::jsonb),
  (397, 'skill', 'tooling/arjun', 'body', '"49e2e3152385454e07d8f61ce6dbecc0"'::jsonb),
  (398, 'skill', 'curl', 'body', '"bdba7327a931a5b15f48c82a77960932"'::jsonb),
  (399, 'skill', 'tooling/curl', 'body', '"bdba7327a931a5b15f48c82a77960932"'::jsonb),
  (400, 'skill', 'dalfox', 'body', '"755e231ca4c893876186aaedd3cd05ca"'::jsonb),
  (401, 'skill', 'tooling/dalfox', 'body', '"755e231ca4c893876186aaedd3cd05ca"'::jsonb),
  (402, 'skill', 'dirsearch', 'body', '"3a25b99f55a366273153f3a06b4d86c1"'::jsonb),
  (403, 'skill', 'tooling/dirsearch', 'body', '"3a25b99f55a366273153f3a06b4d86c1"'::jsonb),
  (404, 'skill', 'ffuf', 'body', '"23b7d0238d7cb856dc5cbefd3e824569"'::jsonb),
  (405, 'skill', 'tooling/ffuf', 'body', '"23b7d0238d7cb856dc5cbefd3e824569"'::jsonb),
  (406, 'skill', 'httpx', 'body', '"1f3358cf73d9d9df8fc800a8433f2272"'::jsonb),
  (407, 'skill', 'tooling/httpx', 'body', '"1f3358cf73d9d9df8fc800a8433f2272"'::jsonb),
  (408, 'skill', 'hydra', 'body', '"30e91994148c3b0e7ae5fa8b67bce5dd"'::jsonb),
  (409, 'skill', 'tooling/hydra', 'body', '"30e91994148c3b0e7ae5fa8b67bce5dd"'::jsonb),
  (410, 'skill', 'jwt_tool', 'body', '"f092fa345c4129902bf4647591e65acb"'::jsonb),
  (411, 'skill', 'tooling/jwt_tool', 'body', '"f092fa345c4129902bf4647591e65acb"'::jsonb),
  (412, 'skill', 'katana', 'body', '"8571bf9137d59cd356b351dce4852d63"'::jsonb),
  (413, 'skill', 'tooling/katana', 'body', '"8571bf9137d59cd356b351dce4852d63"'::jsonb),
  (414, 'skill', 'nmap', 'body', '"e009ca5349db627a4025c6f724eaf45e"'::jsonb),
  (415, 'skill', 'tooling/nmap', 'body', '"e009ca5349db627a4025c6f724eaf45e"'::jsonb),
  (416, 'skill', 'nuclei', 'body', '"d29b10206b3d4c2713cda5101aadbbb1"'::jsonb),
  (417, 'skill', 'tooling/nuclei', 'body', '"d29b10206b3d4c2713cda5101aadbbb1"'::jsonb),
  (418, 'skill', 'python3', 'body', '"46c274713984d1f6e9fcf64b073453c1"'::jsonb),
  (419, 'skill', 'tooling/python3', 'body', '"46c274713984d1f6e9fcf64b073453c1"'::jsonb),
  (420, 'skill', 'sqlmap', 'body', '"f1974566c1c6de910ca6c68d33664114"'::jsonb),
  (421, 'skill', 'tooling/sqlmap', 'body', '"f1974566c1c6de910ca6c68d33664114"'::jsonb),
  (422, 'skill', 'subfinder', 'body', '"386aa685596f6c3044497580efe2d799"'::jsonb),
  (423, 'skill', 'tooling/subfinder', 'body', '"386aa685596f6c3044497580efe2d799"'::jsonb),
  (424, 'skill', 'wafw00f', 'body', '"05dab4d6f61c3d44021f809fccfda3ab"'::jsonb),
  (425, 'skill', 'tooling/wafw00f', 'body', '"05dab4d6f61c3d44021f809fccfda3ab"'::jsonb),
  (426, 'skill', 'hunter', 'body', '"707fcebcea9c4f1eace1ea4fd2dffe6b"'::jsonb),
  (427, 'skill', 'skills/hunter', 'body', '"707fcebcea9c4f1eace1ea4fd2dffe6b"'::jsonb),
  (428, 'skill', 'jq', 'body', '"a3402f09fecd1280402c2216d3db2bdc"'::jsonb),
  (429, 'skill', 'tooling/jq', 'body', '"a3402f09fecd1280402c2216d3db2bdc"'::jsonb),
  (430, 'skill', 'classify-traffic', 'name', '"642a3008a19a6d945b39ffd55913654e"'::jsonb),
  (431, 'skill', 'classify-traffic', 'description', '"7e591ca179ae1cac402ce2b373597108"'::jsonb),
  (432, 'skill', 'classify-traffic', 'body', '"1578b9d8ebf9e3ef1fc900ade62e6c37"'::jsonb),
  (433, 'skill', 'skills/classify-traffic', 'name', '"642a3008a19a6d945b39ffd55913654e"'::jsonb),
  (434, 'skill', 'skills/classify-traffic', 'description', '"7e591ca179ae1cac402ce2b373597108"'::jsonb),
  (435, 'skill', 'skills/classify-traffic', 'body', '"1578b9d8ebf9e3ef1fc900ade62e6c37"'::jsonb),
  (436, 'skill', 'orchestrator', 'name', '"b18cfb906a6c990072c6aa78b52f0a82"'::jsonb),
  (437, 'skill', 'orchestrator', 'description', '"ada6011a18abedb569d40d7889b57e33"'::jsonb),
  (438, 'skill', 'orchestrator', 'body', '"ab7ff5af62fff4f7bb2806f8cdf31c18"'::jsonb),
  (439, 'skill', 'skills/orchestrator', 'name', '"b18cfb906a6c990072c6aa78b52f0a82"'::jsonb),
  (440, 'skill', 'skills/orchestrator', 'description', '"ada6011a18abedb569d40d7889b57e33"'::jsonb),
  (441, 'skill', 'skills/orchestrator', 'body', '"ab7ff5af62fff4f7bb2806f8cdf31c18"'::jsonb),
  (442, 'skill', 'curl', 'name', '"91dd5b8960e58de0372ede12cdd7160b"'::jsonb),
  (443, 'skill', 'curl', 'description', '"b36e19b33ab7d2bd1d0f5d01d73d357b"'::jsonb),
  (444, 'skill', 'curl', 'body', '"e40d8db8a590f65a976952615b9e57f0"'::jsonb),
  (445, 'skill', 'tooling/curl', 'name', '"91dd5b8960e58de0372ede12cdd7160b"'::jsonb),
  (446, 'skill', 'tooling/curl', 'description', '"b36e19b33ab7d2bd1d0f5d01d73d357b"'::jsonb),
  (447, 'skill', 'tooling/curl', 'body', '"e40d8db8a590f65a976952615b9e57f0"'::jsonb),
  (448, 'skill', 'python3', 'name', '"5bf1e77873563536d222ca6382105521"'::jsonb),
  (449, 'skill', 'python3', 'description', '"76796b2b4ddf47fe5b2e73858aba77ac"'::jsonb),
  (450, 'skill', 'python3', 'body', '"9ef7a5cbecfbe77c8191ea76422e7b21"'::jsonb),
  (451, 'skill', 'tooling/python3', 'name', '"5bf1e77873563536d222ca6382105521"'::jsonb),
  (452, 'skill', 'tooling/python3', 'description', '"76796b2b4ddf47fe5b2e73858aba77ac"'::jsonb),
  (453, 'skill', 'tooling/python3', 'body', '"9ef7a5cbecfbe77c8191ea76422e7b21"'::jsonb),
  (454, 'skill', 'sh', 'name', '"b64e67b8ccdc4f1b4f0faeb71f1d34d6"'::jsonb),
  (455, 'skill', 'sh', 'description', '"569b88d30dd89bbd88d38abb0cfb6fea"'::jsonb),
  (456, 'skill', 'sh', 'body', '"0483657ecde4c7ef8e80cc2832e81caa"'::jsonb),
  (457, 'skill', 'tooling/sh', 'name', '"b64e67b8ccdc4f1b4f0faeb71f1d34d6"'::jsonb),
  (458, 'skill', 'tooling/sh', 'description', '"569b88d30dd89bbd88d38abb0cfb6fea"'::jsonb),
  (459, 'skill', 'tooling/sh', 'body', '"0483657ecde4c7ef8e80cc2832e81caa"'::jsonb),
  (460, 'skill', 'sqlmap', 'name', '"06491ee8accde552ac7abc61f1a0c842"'::jsonb),
  (461, 'skill', 'sqlmap', 'description', '"eb76de85d6aa90d35d3ebc0b674cf35f"'::jsonb),
  (462, 'skill', 'sqlmap', 'body', '"fa515a1eecfd1b01a5182be1dfc12ef7"'::jsonb),
  (463, 'skill', 'tooling/sqlmap', 'name', '"06491ee8accde552ac7abc61f1a0c842"'::jsonb),
  (464, 'skill', 'tooling/sqlmap', 'description', '"eb76de85d6aa90d35d3ebc0b674cf35f"'::jsonb),
  (465, 'skill', 'tooling/sqlmap', 'body', '"fa515a1eecfd1b01a5182be1dfc12ef7"'::jsonb),
  (466, 'skill', 'bac', 'name', '"a6449307ca96b01f4199b172eb0c07bf"'::jsonb),
  (467, 'skill', 'bac', 'description', '"d765c56e62df2e6ceebdec52cacde8a7"'::jsonb),
  (468, 'skill', 'bac', 'body', '"5ed8a801db05a27fd98da5975e819af2"'::jsonb),
  (469, 'skill', 'web/bac', 'name', '"a6449307ca96b01f4199b172eb0c07bf"'::jsonb),
  (470, 'skill', 'web/bac', 'description', '"d765c56e62df2e6ceebdec52cacde8a7"'::jsonb),
  (471, 'skill', 'web/bac', 'body', '"5ed8a801db05a27fd98da5975e819af2"'::jsonb),
  (472, 'skill', 'sqli', 'name', '"21b390e5203a3c653a54a957c7f4dbf1"'::jsonb),
  (473, 'skill', 'sqli', 'description', '"d35283e7983191aa9817a7b946c2e74c"'::jsonb),
  (474, 'skill', 'sqli', 'body', '"c053e00e4393fa0a1b42a32d4cbcef2c"'::jsonb),
  (475, 'skill', 'web/sqli', 'name', '"21b390e5203a3c653a54a957c7f4dbf1"'::jsonb),
  (476, 'skill', 'web/sqli', 'description', '"d35283e7983191aa9817a7b946c2e74c"'::jsonb),
  (477, 'skill', 'web/sqli', 'body', '"c053e00e4393fa0a1b42a32d4cbcef2c"'::jsonb),
  (478, 'skill', 'sqlmap', 'body', '"1242b41aae593f4f92bfd6985b06ec2d"'::jsonb),
  (479, 'skill', 'tooling/sqlmap', 'body', '"1242b41aae593f4f92bfd6985b06ec2d"'::jsonb),
  (480, 'skill', 'sqli', 'body', '"7b5ba5ecb793ef0b03df7b517bdcc2e2"'::jsonb),
  (481, 'skill', 'web/sqli', 'body', '"7b5ba5ecb793ef0b03df7b517bdcc2e2"'::jsonb),
  (482, 'skill', 'classify-traffic', 'description', '"f507378bfe64466e2db9c454f2cc8db9"'::jsonb),
  (483, 'skill', 'classify-traffic', 'body', '"371b50c903466caf3f42d9ee455255db"'::jsonb),
  (484, 'skill', 'skills/classify-traffic', 'description', '"f507378bfe64466e2db9c454f2cc8db9"'::jsonb),
  (485, 'skill', 'skills/classify-traffic', 'body', '"371b50c903466caf3f42d9ee455255db"'::jsonb),
  (486, 'skill', 'orchestrator', 'body', '"03d0ded865d562add94ddd66788c93cc"'::jsonb),
  (487, 'skill', 'skills/orchestrator', 'body', '"03d0ded865d562add94ddd66788c93cc"'::jsonb),
  (488, 'skill', 'curl', 'body', '"c87e7cd6cb262214697d36c33a307fd7"'::jsonb),
  (489, 'skill', 'tooling/curl', 'body', '"c87e7cd6cb262214697d36c33a307fd7"'::jsonb),
  (490, 'skill', 'python3', 'body', '"996aadbe1165801cc6ab87943d40423f"'::jsonb),
  (491, 'skill', 'tooling/python3', 'body', '"996aadbe1165801cc6ab87943d40423f"'::jsonb),
  (492, 'skill', 'sh', 'body', '"a650f32b0e54efe8c0d907f2a75441aa"'::jsonb),
  (493, 'skill', 'tooling/sh', 'body', '"a650f32b0e54efe8c0d907f2a75441aa"'::jsonb),
  (494, 'skill', 'bac', 'body', '"a832ae3e3b8e10028e1f6d6a15ee20aa"'::jsonb),
  (495, 'skill', 'web/bac', 'body', '"a832ae3e3b8e10028e1f6d6a15ee20aa"'::jsonb),
  (496, 'skill', 'classify-traffic', 'body', '"44cc3fe1f67c61f5deb06bf94dfcca23"'::jsonb),
  (497, 'skill', 'skills/classify-traffic', 'body', '"44cc3fe1f67c61f5deb06bf94dfcca23"'::jsonb),
  (498, 'skill', 'orchestrator', 'description', '"98db824d28749542b0dd1be4243ce4a2"'::jsonb),
  (499, 'skill', 'orchestrator', 'body', '"8cb2013628a97f88356ab30076f56cd2"'::jsonb),
  (500, 'skill', 'skills/orchestrator', 'description', '"98db824d28749542b0dd1be4243ce4a2"'::jsonb),
  (501, 'skill', 'skills/orchestrator', 'body', '"8cb2013628a97f88356ab30076f56cd2"'::jsonb),
  (502, 'skill', 'vuln-web-bac', 'name', '"bd10f5769b8c9ca7260eaa41c72e7bc8"'::jsonb),
  (503, 'skill', 'vuln-web-bac', 'description', '"d765c56e62df2e6ceebdec52cacde8a7"'::jsonb),
  (504, 'skill', 'vuln-web-bac', 'body', '"8e3ca286fbb14840ea9e7c7235865bc6"'::jsonb),
  (505, 'skill', 'skills/vuln-web-bac', 'name', '"bd10f5769b8c9ca7260eaa41c72e7bc8"'::jsonb),
  (506, 'skill', 'skills/vuln-web-bac', 'description', '"d765c56e62df2e6ceebdec52cacde8a7"'::jsonb),
  (507, 'skill', 'skills/vuln-web-bac', 'body', '"8e3ca286fbb14840ea9e7c7235865bc6"'::jsonb),
  (508, 'skill', 'vuln-web-bac', 'body', '"047b85a6644497006047ff283d0629a7"'::jsonb),
  (509, 'skill', 'skills/vuln-web-bac', 'body', '"047b85a6644497006047ff283d0629a7"'::jsonb),
  (510, 'skill', 'vuln-web-bac', 'description', '"9ee7838d06b3f5c3ad20100e08e8f98e"'::jsonb),
  (511, 'skill', 'vuln-web-bac', 'body', '"25f366fc349ffeda844f65e2dcc76c05"'::jsonb),
  (512, 'skill', 'skills/vuln-web-bac', 'description', '"9ee7838d06b3f5c3ad20100e08e8f98e"'::jsonb),
  (513, 'skill', 'skills/vuln-web-bac', 'body', '"25f366fc349ffeda844f65e2dcc76c05"'::jsonb),
  (514, 'skill', 'vuln-web-bac', 'description', '"6a72e5007454a17f22114386526d8f0c"'::jsonb),
  (515, 'skill', 'vuln-web-bac', 'body', '"1f30d69ef2c090b6be3169148c234014"'::jsonb),
  (516, 'skill', 'skills/vuln-web-bac', 'description', '"6a72e5007454a17f22114386526d8f0c"'::jsonb),
  (517, 'skill', 'skills/vuln-web-bac', 'body', '"1f30d69ef2c090b6be3169148c234014"'::jsonb),
  (518, 'skill', 'vuln-web-bac', 'description', '"5f6e1fe6c413676a34a453016f0d35cb"'::jsonb),
  (519, 'skill', 'vuln-web-bac', 'body', '"788ad559dcc0edfddcface188897faac"'::jsonb),
  (520, 'skill', 'skills/vuln-web-bac', 'description', '"5f6e1fe6c413676a34a453016f0d35cb"'::jsonb),
  (521, 'skill', 'skills/vuln-web-bac', 'body', '"788ad559dcc0edfddcface188897faac"'::jsonb),
  (522, 'skill', 'bac', 'description', '"5f6e1fe6c413676a34a453016f0d35cb"'::jsonb),
  (523, 'skill', 'bac', 'body', '"788ad559dcc0edfddcface188897faac"'::jsonb),
  (524, 'skill', 'web/bac', 'description', '"5f6e1fe6c413676a34a453016f0d35cb"'::jsonb),
  (525, 'skill', 'web/bac', 'body', '"788ad559dcc0edfddcface188897faac"'::jsonb),
  (526, 'skill', 'bac', 'description', '"462cbf44be5261fc3504b4f080301a10"'::jsonb),
  (527, 'skill', 'web/bac', 'description', '"462cbf44be5261fc3504b4f080301a10"'::jsonb),
  (528, 'skill', 'bac', 'body', '"bd91da7d0199d3023aa1cc467f72cfc2"'::jsonb),
  (529, 'skill', 'web/bac', 'body', '"bd91da7d0199d3023aa1cc467f72cfc2"'::jsonb),
  (530, 'skill', 'bac', 'body', '"d1d3bbd0b97ecb51216d038c82f5685b"'::jsonb),
  (531, 'skill', 'web/bac', 'body', '"d1d3bbd0b97ecb51216d038c82f5685b"'::jsonb),
  (532, 'setting', 'compaction', 'evaluator_context_ratio', '0.5'::jsonb),
  (533, 'setting', 'compaction', 'executor_context_ratio', '0.5'::jsonb),
  (534, 'setting', 'compaction', 'monitor_context_ratio', '0.5'::jsonb),
  (535, 'setting', 'compaction', 'planner_context_ratio', '0.6'::jsonb),
  (536, 'setting', 'runtime', 'run_tail_bytes', '8192'::jsonb),
  (537, 'setting', 'runtime', 'step_llm_timeout_seconds', '300'::jsonb),
  (538, 'setting', 'runtime', 'step_tool_timeout_seconds', '1800'::jsonb),
  (539, 'setting', 'runtime', 'tool_output_tail_bytes', '32768'::jsonb),
  (540, 'setting', 'proxy_filter', 'allow_hosts', '[]'::jsonb),
  (541, 'setting', 'proxy_filter', 'exclude_methods', '["OPTIONS","HEAD","CONNECT"]'::jsonb),
  (542, 'setting', 'proxy_filter', 'exclude_hosts', '[]'::jsonb),
  (543, 'setting', 'proxy_filter', 'exclude_suffixes', '[".css",".js",".png",".jpg",".gif",".ico",".woff",".woff2"]'::jsonb),
  (544, 'setting', 'proxy_filter', 'exclude_content_types', '[]'::jsonb),
  (545, 'setting', 'proxy_filter', 'exclude_upgrade_protocols', '["websocket"]'::jsonb),
  (546, 'setting', 'proxy_filter', 'exclude_status_codes', '[400,401,403,404,405,408,409,410,413,414,415,422,429,500,501,502,503,504]'::jsonb),
  (547, 'setting', 'proxy_filter', 'max_request_body_size', '2097152'::jsonb),
  (548, 'setting', 'proxy_filter', 'max_response_body_size', '8388608'::jsonb),
  (549, 'setting', 'proxy_filter', 'allow_hosts', '["vulnapp","host.docker.internal","127.0.0.1"]'::jsonb),
  (550, 'setting', 'proxy_filter', 'allow_hosts', '["vulnapp","host.docker.internal","localhost","127.0.0.1","49.234.23.42"]'::jsonb),
  (551, 'setting', 'proxy_filter', 'exclude_status_codes', '[]'::jsonb),
  (552, 'setting', 'proxy_filter', 'max_request_body_size', '1048576'::jsonb),
  (553, 'setting', 'proxy_filter', 'max_response_body_size', '2097152'::jsonb),
  (554, 'setting', 'proxy_filter', 'allow_hosts', '["vulnapp","host.docker.internal","localhost","127.0.0.1"]'::jsonb),
  (555, 'setting', 'proxy_filter', 'allow_hosts', '["vulnapp","host.docker.internal"]'::jsonb),
  (556, 'setting', 'proxy_filter', 'exclude_methods', '[]'::jsonb),
  (557, 'setting', 'proxy_filter', 'exclude_suffixes', '[]'::jsonb);

CREATE TEMP TABLE migration_setting_fields (group_key text, field text);
INSERT INTO migration_setting_fields (group_key, field) VALUES
  ('compaction', 'evaluator_context_ratio'),
  ('compaction', 'executor_context_ratio'),
  ('compaction', 'monitor_context_ratio'),
  ('compaction', 'planner_context_ratio'),
  ('runtime', 'run_tail_bytes'),
  ('runtime', 'step_llm_timeout_seconds'),
  ('runtime', 'step_tool_timeout_seconds'),
  ('runtime', 'tool_output_tail_bytes'),
  ('proxy_filter', 'allow_hosts'),
  ('proxy_filter', 'exclude_content_types'),
  ('proxy_filter', 'exclude_hosts'),
  ('proxy_filter', 'exclude_methods'),
  ('proxy_filter', 'exclude_status_codes'),
  ('proxy_filter', 'exclude_suffixes'),
  ('proxy_filter', 'exclude_upgrade_protocols'),
  ('proxy_filter', 'max_request_body_size'),
  ('proxy_filter', 'max_response_body_size');

-- ── 判定函数（会话临时，迁移结束即失效）────────────────────────────────

CREATE FUNCTION pg_temp.text_fp(t text) RETURNS jsonb LANGUAGE sql IMMUTABLE AS
$$ SELECT to_jsonb(md5(btrim(t, E' \t\r\n'))) $$;

CREATE FUNCTION pg_temp.is_historical(r text, k text, f text, v jsonb) RETURNS boolean LANGUAGE sql STABLE AS
$$ SELECT EXISTS (SELECT 1 FROM migration_history h WHERE h.resource = r AND h.key = k AND h.field = f AND h.value = v) $$;

-- 整值字段：等于历史出厂值 → NULL（不算自定义），否则 {value}（基线未知）。
CREATE FUNCTION pg_temp.replace_entry(r text, k text, f text, v jsonb, fingerprint jsonb) RETURNS jsonb LANGUAGE sql STABLE AS
$$ SELECT CASE WHEN v IS NULL OR pg_temp.is_historical(r, k, f, fingerprint) THEN NULL
               ELSE jsonb_build_object('value', v) END $$;

CREATE FUNCTION pg_temp.set_norm(a jsonb) RETURNS jsonb LANGUAGE sql IMMUTABLE AS
$$ SELECT COALESCE((SELECT jsonb_agg(DISTINCT e ORDER BY e) FROM jsonb_array_elements(a) e), '[]'::jsonb) $$;

-- 名单字段：等于任一历史出厂名单 → NULL；否则以最接近的历史名单（对称差最小，
-- 同距取较新）为起点，返回 {added, removed}。
CREATE FUNCTION pg_temp.set_entry(r text, k text, f text, cur jsonb) RETURNS jsonb LANGUAGE plpgsql STABLE AS
$$
DECLARE
    c jsonb := pg_temp.set_norm(cur);
    base jsonb;
    best integer;
    h record;
    d integer;
    added jsonb;
    removed jsonb;
BEGIN
    IF cur IS NULL OR jsonb_typeof(cur) <> 'array' THEN
        RETURN NULL;
    END IF;
    FOR h IN SELECT pg_temp.set_norm(value) AS v FROM migration_history
             WHERE resource = r AND key = k AND field = f ORDER BY seq LOOP
        IF h.v = c THEN
            RETURN NULL;
        END IF;
        d := (SELECT count(*) FROM jsonb_array_elements(c) e WHERE NOT h.v @> jsonb_build_array(e))
           + (SELECT count(*) FROM jsonb_array_elements(h.v) e WHERE NOT c @> jsonb_build_array(e));
        IF best IS NULL OR d < best THEN
            base := h.v;
            best := d;
        END IF;
    END LOOP;
    base := COALESCE(base, '[]'::jsonb);
    added := (SELECT jsonb_agg(e) FROM jsonb_array_elements(c) e WHERE NOT base @> jsonb_build_array(e));
    removed := (SELECT jsonb_agg(e) FROM jsonb_array_elements(base) e WHERE NOT c @> jsonb_build_array(e));
    RETURN jsonb_strip_nulls(jsonb_build_object('added', added, 'removed', removed));
END
$$;

-- ── agent ───────────────────────────────────────────────────────────

INSERT INTO public.agent_override (agent_code, fields)
SELECT code, fields FROM (
    SELECT a.code, jsonb_strip_nulls(jsonb_build_object(
        'description',    pg_temp.replace_entry('agent', a.code, 'description', to_jsonb(a.description), pg_temp.text_fp(a.description)),
        'max_iterations', pg_temp.replace_entry('agent', a.code, 'max_iterations', to_jsonb(a.max_iterations), to_jsonb(a.max_iterations)),
        'complexity',     pg_temp.replace_entry('agent', a.code, 'complexity', to_jsonb(a.complexity), to_jsonb(a.complexity)),
        'function_tools', pg_temp.set_entry('agent', a.code, 'function_tools', a.function_tools),
        'cli_tools',      pg_temp.set_entry('agent', a.code, 'cli_tools', a.cli_tools),
        'skills',         pg_temp.set_entry('agent', a.code, 'skills', a.skills)
    )) AS fields
    FROM public.agent a
    WHERE a.code = ANY (ARRAY['planner', 'executor', 'evaluator', 'monitor'])
) x
WHERE fields <> '{}'::jsonb;

INSERT INTO public.agent_prompt_archive (agent_code, prompt)
SELECT a.code, a.system_prompt
FROM public.agent a
WHERE a.code = ANY (ARRAY['planner', 'executor', 'evaluator', 'monitor'])
  AND btrim(a.system_prompt, E' \t\r\n') <> ''
  AND NOT pg_temp.is_historical('agent', a.code, 'system_prompt', pg_temp.text_fp(a.system_prompt));

-- ── 技能 ────────────────────────────────────────────────────────────

-- 仍在出厂内容中的内置技能：改过的字段转为用户层，last_saved 为当时的完整内容。
INSERT INTO public.skill_override (skill_code, fields, last_saved)
SELECT code, fields, last_saved FROM (
    SELECT s.code,
        jsonb_strip_nulls(jsonb_build_object(
            'name',        pg_temp.replace_entry('skill', s.code, 'name', to_jsonb(s.name), pg_temp.text_fp(s.name)),
            'description', pg_temp.replace_entry('skill', s.code, 'description', to_jsonb(s.description), pg_temp.text_fp(s.description)),
            'body',        pg_temp.replace_entry('skill', s.code, 'body', to_jsonb(s.body), pg_temp.text_fp(s.body)),
            'enabled',     CASE WHEN s.enabled THEN NULL ELSE jsonb_build_object('value', false) END
        )) AS fields,
        jsonb_build_object('code', s.code, 'category', s.category, 'name', s.name, 'description', s.description,
                           'body', s.body, 'enabled', s.enabled, 'is_builtin', true) AS last_saved
    FROM public.skill s
    WHERE s.is_builtin AND s.code = ANY (ARRAY['bac', 'browser-use']::text[])
) x
WHERE fields <> '{}'::jsonb;

-- 已不在出厂内容中的内置技能：改过的转为同 code 的自建技能，未改过的随旧表删除。
INSERT INTO public.custom_skill (code, category, name, description, body, enabled, created_at, updated_at)
SELECT s.code, s.category, s.name, s.description, s.body, s.enabled, s.created_at, s.updated_at
FROM public.skill s
WHERE s.is_builtin
  AND NOT (s.code = ANY (ARRAY['bac', 'browser-use']::text[]))
  AND NOT (pg_temp.is_historical('skill', s.code, 'name', pg_temp.text_fp(s.name))
       AND pg_temp.is_historical('skill', s.code, 'description', pg_temp.text_fp(s.description))
       AND pg_temp.is_historical('skill', s.code, 'body', pg_temp.text_fp(s.body)));

-- 自建技能原样迁移。
INSERT INTO public.custom_skill (code, category, name, description, body, enabled, created_at, updated_at)
SELECT s.code, s.category, s.name, s.description, s.body, s.enabled, s.created_at, s.updated_at
FROM public.skill s
WHERE NOT s.is_builtin;

-- ── 系统设置 ────────────────────────────────────────────────────────

-- 只转换当前仍存在的字段（已废弃的旧字段随旧表删除）；空值视为未设置。
INSERT INTO public.setting_override (group_key, fields)
SELECT group_key, fields FROM (
    SELECT st.group_key,
        COALESCE((
            SELECT jsonb_object_agg(e.k, e.entry) FROM (
                SELECT kv.key AS k,
                       CASE WHEN jsonb_typeof(kv.value) = 'array'
                            THEN pg_temp.set_entry('setting', st.group_key, kv.key, kv.value)
                            ELSE pg_temp.replace_entry('setting', st.group_key, kv.key, kv.value, kv.value)
                       END AS entry
                FROM jsonb_each(st.value) kv
                JOIN migration_setting_fields sf ON sf.group_key = st.group_key AND sf.field = kv.key
                WHERE jsonb_typeof(kv.value) <> 'null'
            ) e
            WHERE e.entry IS NOT NULL
        ), '{}'::jsonb) AS fields
    FROM public.system_setting st
) x
WHERE fields <> '{}'::jsonb;

-- ── 删除旧表 ────────────────────────────────────────────────────────

DROP TABLE public.agent;
DROP TABLE public.skill;
DROP TABLE public.system_setting;

DROP TABLE migration_history;
DROP TABLE migration_setting_fields;
