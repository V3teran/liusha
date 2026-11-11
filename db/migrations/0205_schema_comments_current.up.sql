-- 0205：schema 注释对齐现行架构。仅改注释，不动结构与数据。
-- 0196 基线与其后迁移遗留的注释仍描述已退役的概念：探索图的旧节点 / 关系集合、
-- repro 的流量引用格式、LLM 路由的别名层与兜底、来源旧名（自主下发 / 代理模式）、
-- 已删除的控制命令与写入工具，以及「旧模式 / 旧数据兼容」等与现行双路径不符的定性。

COMMENT ON TABLE public.audit_log IS '系统敏感操作审计：task 建/中止、凭证写删、登录等';
COMMENT ON COLUMN public.audit_log.action IS '命名空间动词，示例: task.abort / task.create / credential.set / auth.login';
COMMENT ON COLUMN public.audit_log.actor IS '示例: system / runner / user:admin / anonymous';

COMMENT ON TABLE public.cron_schedule IS '定时模板：每次到点克隆一个 assignment（来源 direct）并展开任务；模板本身只有启用/停用两态';

COMMENT ON TABLE public.exploration_edge IS '探索图关系边：generates / triggers / supersedes（见 ck_exploration_edge_rel）';
COMMENT ON COLUMN public.exploration_edge.rel IS '关系类型：generates(推导链) / triggers(结果催生目标或动作) / supersedes(新派生目标吸收旧目标，src=新、dst=旧)';

COMMENT ON TABLE public.exploration_node IS '探索图节点：objective / action / observation / result（见 ck_exploration_node_kind）';
COMMENT ON COLUMN public.exploration_node.kind IS '节点类型：objective(目标) / action(动作) / observation(观察) / result(坐实结果)';
COMMENT ON COLUMN public.exploration_node.state IS 'action 与 objective 的状态：open/running/completed/failed/exhausted/aborted，superseded 仅用于被吸收的派生目标；其余节点为 NULL';

COMMENT ON COLUMN public.finding.dedup_key IS 'task 内去重键（生成列）：host + cwe_id + 规范化 path + method + parameter；target 缺 path 时取 summary 前 40 字';
COMMENT ON COLUMN public.finding.repro IS '通过复现门的复现配方（域信封 {domain, recipe, assert}），供复验与人读';
COMMENT ON COLUMN public.finding.status IS 'triage 处置态：open/confirmed/fixed/false_positive/accepted。交付落库默认 open。';
COMMENT ON COLUMN public.finding.summary IS '漏洞描述（必填）：是什么、怎么验证、推理依据；evidence 为可选的结构化补充';

COMMENT ON TABLE public.llm_provider IS 'LLM provider 注册表（只认协议：openai_compat / anthropic）；DB 为事实源，运行期多级缓存热读';
COMMENT ON COLUMN public.llm_provider.api_key_env IS '部署期密钥路径：环境变量名（encrypted_api_key 为空时生效）';
COMMENT ON COLUMN public.llm_provider.api_key_last4 IS 'API Key 明文末 4 位（脱敏辨识用，非密钥值本身）；密钥走环境变量时为空';
COMMENT ON COLUMN public.llm_provider.encrypted_api_key IS 'AES-256-GCM 密文（nonce||ciphertext），密钥来自 LIUSHA_LLM_KEY_SECRET；非空时优先于 api_key_env';

COMMENT ON TABLE public.llm_role_route IS 'LLM 档位路由：role 存档位（simple/medium/complex）→ provider_code';
COMMENT ON COLUMN public.llm_role_route.provider_code IS '该档位路由到的 provider（llm_provider.code）';

COMMENT ON TABLE public.task IS '任务：assignment 展开的执行单元（人工下发与代理捕获同一结构）';
COMMENT ON COLUMN public.task.brief IS '任务目标的自然语言描述（必填）：人工下发为用户输入的 brief；代理捕获为聚合器生成的分析说明。';
COMMENT ON COLUMN public.task.target_host IS '目标 host：代理捕获建 task 时即填（被分析 host）；人工下发由 runner 从 brief 抽取后回填，可空。';

COMMENT ON TABLE public.task_control_event IS '人工干预事件（控制平面 → planner）';
COMMENT ON COLUMN public.task_control_event.command IS '控制命令：adjust_goal（调整目标）、inject_action（注入动作）；暂停/继续/停止是 task 行上的状态，不走事件';
COMMENT ON COLUMN public.task_control_event.payload IS '命令参数：adjust_goal{new_goal}，inject_action{kind,target,reason,priority}';

COMMENT ON TABLE public.traffic_task IS '代理流量 ↔ 代理捕获 task 多对多消费关系（一条捕获可被多 task 复用）';
