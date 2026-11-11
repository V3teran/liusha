-- 0205 回卷：还原 0205 之前的 schema 注释原文。

COMMENT ON TABLE public.audit_log IS '系统敏感操作审计：owner abort/create、credential 写删等';
COMMENT ON COLUMN public.audit_log.action IS '命名空间动词，示例: owner.abort / credential.set';
COMMENT ON COLUMN public.audit_log.actor IS '示例: system / sweep / api_user:abcd1234 / scanner';
COMMENT ON TABLE public.cron_schedule IS '定时模板（P1 空表，P4 加 Scheduler）；每次触发克隆一个 assignment';
COMMENT ON TABLE public.exploration_edge IS '世界模型关系边表（5种关系）：GENERATES/CONFIRMS/REFUTES/ENABLES/DEPENDS_ON';
COMMENT ON COLUMN public.exploration_edge.rel IS '关系类型（全大写）：GENERATES(生成)/CONFIRMS(确认)/REFUTES(反驳)/ENABLES(使能)/DEPENDS_ON(依赖)';
COMMENT ON TABLE public.exploration_node IS '世界模型节点表（5种类型）：objective/action/hypothesis/evidence/finding';
COMMENT ON COLUMN public.exploration_node.kind IS '节点类型：objective(目标)/action(动作)/hypothesis(假设)/evidence(证据)/finding(发现)';
COMMENT ON COLUMN public.exploration_node.state IS 'action 专用：执行状态 open/blocked/running/done/failed/exhausted/aborted';
COMMENT ON COLUMN public.finding.dedup_key IS 'v3 公式：host + cwe_id + normalize_path(path) + method + parameter（防止假阳性）';
COMMENT ON COLUMN public.finding.repro IS '机器可验的复现配方（web={traffic_id,modifications,assert}）；喂 L4 Verifier 复现门自动晋升成世界模型节点。NULL=未产配方（仅人读记录，不自动晋升）';
COMMENT ON COLUMN public.finding.status IS 'triage 处置态：open/confirmed/fixed/false_positive/accepted。write_finding 落库默认 open。';
COMMENT ON COLUMN public.finding.summary IS '漏洞自由文本描述（LLM 必填）：是什么、怎么验证、推理依据。替代旧的"主要靠 evidence jsonb 表达"模式，evidence 现退化为可选结构化补充。';
COMMENT ON TABLE public.llm_provider IS 'LLM provider 部署注册表（取代 config.yaml providers:）；DB 为事实源，运行期多级缓存热读';
COMMENT ON COLUMN public.llm_provider.api_key_env IS '旧模式：环境变量名（仅当 encrypted_api_key 为空时生效的回退路径）';
COMMENT ON COLUMN public.llm_provider.api_key_last4 IS 'API Key 明文末 4 位（脱敏辨识用，非密钥值本身）；为空表示未知（旧行或仅 ENV 回退）';
COMMENT ON COLUMN public.llm_provider.encrypted_api_key IS 'AES-256-GCM 密文（nonce||ciphertext），密钥来自 LIUSHA_LLM_KEY_SECRET；为空时回退 api_key_env 环境变量（旧数据兼容）';
COMMENT ON TABLE public.llm_role_route IS 'LLM 角色路由表：role（agent code 或 complexity 档位）→ provider_key';
COMMENT ON COLUMN public.llm_role_route.provider_code IS 'provider 部署 key（直连，取代原 alias 间接层）';
COMMENT ON TABLE public.task IS '统一扫描任务，不再区分场景';
COMMENT ON COLUMN public.task.brief IS '任务目标的自然语言描述（必填）：自主下发为用户输入的 brief；代理模式为聚合器生成的分析说明。';
COMMENT ON COLUMN public.task.target_host IS '目标 host：代理模式建 task 时即填（被分析 host）；自主下发由 runner 从 brief 抽取后回填，可空。';
COMMENT ON TABLE public.task_control_event IS '人工干预事件（控制平面 → Planner Agent）';
COMMENT ON COLUMN public.task_control_event.command IS '控制命令：adjust_goal（调整目标）、inject_move（注入 Move）、pause（暂停）、resume（恢复）、terminate（终止）';
COMMENT ON COLUMN public.task_control_event.payload IS '命令参数：adjust_goal{new_goal}, inject_move{move_kind,target_ref,reason,priority}';
COMMENT ON TABLE public.traffic_task IS '代理流量↔代理模式 task 多对多消费关系（一条捕获可被多 task 复用）';
