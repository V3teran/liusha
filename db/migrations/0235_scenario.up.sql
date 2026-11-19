-- 0235 场景（scenario）与技能单表化。
--
-- 技能与场景采用同一模型：一张表一行一个资源，内置（出厂种子）与自建同表、is_builtin 区分。
-- 出厂内容编译在程序里，由迁移之后的出厂同步（internal/builtinsync）写入：没有行就插入；
-- seed_hash 记最近一次写入的出厂内容的哈希，当前内容的哈希与它相等即用户没改过，升级时
-- 跟随新版；改过的不动。内置与自建都可删除：自建删行；内置记 deleted_at（删除标记），出厂
-- 同步不再补回，用户可在页面上恢复。哈希算式与 builtinsync.Hash 一致：
--   encode(sha256(convert_to(name || chr(31) || description || chr(31) || body, 'UTF8')), 'hex')
--
-- 技能存量数据从 0201 的分层结构（skill_override 用户层 + custom_skill 自建）搬入：
--   - 自建技能整行搬入（is_builtin = false）；
--   - 自定义过的内置技能取 last_saved（保存时的完整生效内容）。只改过启停的，seed_hash 记为
--     当时内容的哈希——内容没改，出厂同步时跟随新版且保留停用；改过内容的 seed_hash 为 NULL
--     （基线未知，视为已改）。与自建同 code 的内置自定义本就被遮蔽，丢弃；
--   - 没自定义过的内置技能没有行，由出厂同步插入。

-- ── 技能 ────────────────────────────────────────────────────────────

CREATE TABLE public.skill (
    code text NOT NULL PRIMARY KEY,
    category text DEFAULT ''::text NOT NULL,
    name text NOT NULL,
    description text DEFAULT ''::text NOT NULL,
    body text NOT NULL,
    enabled boolean DEFAULT true NOT NULL,
    is_builtin boolean DEFAULT false NOT NULL,
    seed_hash text,
    deleted_at timestamp with time zone,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT skill_seed_hash_check CHECK ((is_builtin OR (seed_hash IS NULL))),
    CONSTRAINT skill_deleted_check CHECK ((is_builtin OR (deleted_at IS NULL)))
);
COMMENT ON TABLE public.skill IS '技能：内置（出厂种子，迁移后由出厂同步写入）与自建同表；同 code 时自建优先、内置被遮蔽';
COMMENT ON COLUMN public.skill.category IS '知识域：vuln（漏洞知识）或 tool（工具使用知识）；内置技能随出厂内容';
COMMENT ON COLUMN public.skill.is_builtin IS '是否内置：出厂内容的种子，可编辑、停用、删除、恢复出厂';
COMMENT ON COLUMN public.skill.seed_hash IS '最近一次写入的出厂内容的哈希：与当前内容的哈希相等即未改过，升级时跟随新版；NULL = 自建或基线未知';
COMMENT ON COLUMN public.skill.deleted_at IS '内置技能被用户删除的时间：出厂同步不再补回，恢复后清空；自建技能删除即删行';

INSERT INTO public.skill (code, category, name, description, body, enabled, is_builtin, seed_hash, created_at, updated_at)
SELECT code, category, name, description, body, enabled, false, NULL, created_at, updated_at
  FROM public.custom_skill;

INSERT INTO public.skill (code, category, name, description, body, enabled, is_builtin, seed_hash, created_at, updated_at)
SELECT o.skill_code,
       COALESCE(o.last_saved->>'category', ''),
       o.last_saved->>'name',
       COALESCE(o.last_saved->>'description', ''),
       o.last_saved->>'body',
       COALESCE((o.last_saved->>'enabled')::boolean, true),
       true,
       CASE WHEN NOT (o.fields ?| ARRAY['name', 'description', 'body'])
            THEN encode(sha256(convert_to((o.last_saved->>'name') || chr(31) || COALESCE(o.last_saved->>'description', '')
                                          || chr(31) || (o.last_saved->>'body'), 'UTF8')), 'hex')
       END,
       o.updated_at, o.updated_at
  FROM public.skill_override o
 WHERE o.last_saved ? 'name' AND o.last_saved ? 'body'
   AND NOT EXISTS (SELECT 1 FROM public.custom_skill c WHERE c.code = o.skill_code);

DROP TABLE public.skill_override;
DROP TABLE public.custom_skill;

-- ── 场景 ────────────────────────────────────────────────────────────

CREATE TABLE public.scenario (
    code text NOT NULL PRIMARY KEY,
    name text NOT NULL,
    description text DEFAULT ''::text NOT NULL,
    body text NOT NULL,
    enabled boolean DEFAULT true NOT NULL,
    is_builtin boolean DEFAULT false NOT NULL,
    seed_hash text,
    deleted_at timestamp with time zone,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT scenario_seed_hash_check CHECK ((is_builtin OR (seed_hash IS NULL))),
    CONSTRAINT scenario_deleted_check CHECK ((is_builtin OR (deleted_at IS NULL)))
);
COMMENT ON TABLE public.scenario IS '场景：任务所处情境的说明，整份拼在各 agent 系统提示词末尾；内置（出厂种子）与自建同表';
COMMENT ON COLUMN public.scenario.enabled IS '停用 = 不再用于新任务；已建任务读自身快照，不受影响';
COMMENT ON COLUMN public.scenario.is_builtin IS '是否内置：出厂内容的种子，可编辑、停用、删除、恢复出厂';
COMMENT ON COLUMN public.scenario.seed_hash IS '最近一次写入的出厂内容的哈希：与当前内容的哈希相等即未改过，升级时跟随新版；NULL = 自建或基线未知';
COMMENT ON COLUMN public.scenario.deleted_at IS '内置场景被用户删除的时间：出厂同步不再补回，恢复后清空；自建场景删除即删行';

ALTER TABLE public.task
    ADD COLUMN scenario text,
    ADD COLUMN scenario_name text,
    ADD COLUMN scenario_body text,
    ADD CONSTRAINT task_scenario_snapshot_check CHECK (
        ((scenario IS NULL) = (scenario_name IS NULL)) AND ((scenario IS NULL) = (scenario_body IS NULL)));
COMMENT ON COLUMN public.task.scenario IS '建任务时选定的场景 code（NULL = 无场景）；建后不改';
COMMENT ON COLUMN public.task.scenario_name IS '建任务时的场景名称快照';
COMMENT ON COLUMN public.task.scenario_body IS '建任务时的场景正文快照：每个 run 都读它，场景之后被改、停用、删除都不影响本任务';

ALTER TABLE public.cron_schedule ADD COLUMN scenario text;
COMMENT ON COLUMN public.cron_schedule.scenario IS '到点建任务所用的场景 code（NULL = 无场景）；被引用的场景不可删除、停用';

-- 迁移前的任务与定时模板都按渗透测试运行（领域内容在章程的「域指引」里，本次移进场景）。
-- 定时模板沿用渗透测试场景（出厂同步随后写入它）；存量任务写入渗透测试场景的快照——正文为
-- 本迁移时的出厂内容，即这些任务原本看到的领域内容，续跑、续接都保持原来的情境。快照列为空
-- 只表示建任务时显式选了无场景，不与存量任务混淆。
UPDATE public.cron_schedule SET scenario = 'pentest';

UPDATE public.task
   SET scenario = 'pentest', scenario_name = '渗透测试', scenario_body = $pentest$本任务是对授权目标的渗透测试，交付物是经复现坐实的漏洞或安全弱点。

## 可交付的主张

- 可交付的是**漏洞或安全弱点的主张**。安全配置缺陷（缺失安全响应头、Cookie 缺 HttpOnly/Secure、目录列表、调试接口暴露）是可交付的弱点；一眼可见的问题（敏感文件可匿名下载、危险配置、默认凭据）同样可交付，也要经复现门验证才算数。
- 纯资产信息（技术栈、开放端口、页面清单）不是可交付的主张。
- 常见类别：反射型 / 存储型 / DOM 型 XSS、SQL 注入、命令注入、SSTI、路径穿越、任意文件上传、SSRF、认证绕过、访问控制缺陷、敏感信息泄露、安全配置缺陷、弱口令、反序列化、XXE、CSRF 等。

## 指纹 class 词表

坐实时 `finding.class` 取最贴切的一个（小写 snake_case）：

reflected_xss / stored_xss / dom_xss / sql_injection / command_injection / ssti /
path_traversal / arbitrary_file_upload / ssrf / auth_bypass / access_control /
info_disclosure / security_misconfiguration / weak_credential / deserialization / xxe /
csrf / other_<类别名>

## 常见验证模式

- **注入类**：重放原始载荷 → 布尔条件真/假对照 → 延时对照 → 尝试提取数据；能提取数据即坐实。
- **认证绕过**：不带凭据重放 → 检查状态与内容 → 访问受保护资源 → 执行特权操作；能执行特权操作即坐实。
- **命令注入**：重放载荷 → 带外检测（DNS/HTTP 回调）→ 提取命令输出；拿到命令输出即坐实。
- **SSRF**：重放请求 → 检查是否访问了内部地址 → 尝试云元数据端点 → 从内部服务取回数据；取回内部数据即坐实。
- **部分利用**：例如请求伪造成立而远程执行失败——只确认请求伪造。

## 测试流程

1. 形成假设 → 先用正常输入请求观察基线行为。
2. 构造验证请求实测：确认验证响应出现**基线没有的独有特征**（报错回显、泄露的数据、显著延迟）。
3. 把发现整理成终稿 JSON：现象类主张（输入触发异常）用 `http` 配方并带对照，断言写验证响应的独有特征；多步利用或非 HTTP 的用 `steps` 配方。
4. 属性类主张（目标自身的配置或暴露面，没有触发输入）不带对照，断言直接描述该属性本身——配方字段与断言写法见输出契约。

## Web 工具用法

- **http_request**：请求级操作（发请求、改参数、重放、fuzz、普通表单登录）的主力。测未授权访问必须显式传空串 `identity: ""`（匿名），否则会带上凭证与会话，把认证后的访问误当成未授权访问。
- **drive_browser**：仅在以下场景用真实浏览器——① 漏洞本体需要 JS 执行才成立（DOM XSS、原型链污染）；② 目标是强 JS 渲染的 SPA，纯请求看不到内容；③ 需要真实点击/交互触发。其余一律用 http_request，更快、可并发、可精确重放。
- **凭证库**（read_credentials / write_credential）：拿到可用凭证后用 write_credential 录入，`username` 填目标应用真实的登录名，供同任务下其他执行者复用。
- **流量**（list_traffic / view_traffic）：查看执行者的历史 HTTP 请求与完整响应，与重放结果对照。

## 规划与指派

- **阶段参考**：新目标开局时按资产发现 → 信息收集 → 攻击面映射 → 技术识别推进；识别出目标后看已知漏洞、常见配置错误、认证与授权机制、接口；出现漏洞线索时先初步验证，再串成完整利用链，拿到凭据后扩大范围；动作反复失败时分析失败模式、按错误信息调整，并考虑目标已加固。
- **instruction 写法**：写清目标地址、要测的参数或接口、使用的身份、预期现象。例如「以 admin 身份访问 http://target/api/users?id=2，对比 id=1 的响应，确认能否读到其他用户的数据」。
- **凭据**：已确认事实里的认证方式与可用凭据（access 类）直接使用，在 instruction 中指明用哪个身份；需要先登录再访问的，等登录建立会话的观察落图后再提后续动作。
- **新方向的例子**：从一个接口的注入转向另一个功能模块的访问控制、从 Web 层转向暴露的管理服务，都是本质不同的探索面；同一参数换载荷是延续。

## 监察要点

- **否决理由示例**：「与已完成的 act_abc 重复：同一 /api/users 端点、同一越权手法，已证伪（claim_invalid）。」
- **停滞的典型形态**：团队在已加固的目标上反复换载荷打同一个参数。
- **目标已满足的示例**：目标是「拿到后台管理员权限」，已有坐实结果串成登录点注入 → 取得管理员凭据 → 登录后台验证，且无未验证假设。
- **重规划建议示例**：「在 *.target.com 上枚举子域」「转向尚未覆盖的管理接口」。
$pentest$
 WHERE scenario IS NULL;
