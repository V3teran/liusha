# 场景-打法架构重构 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 把当前焊死在 `mode(active/passive)` 上的单场景架构，重构为「场景 Scenario → 引用一个可复用的打法 Playbook（猎手组合）→ 猎手 Hunter 是离散执行体；引擎 engine(solo/swarm) 与打法正交、任意场景可切」的模型。四类配置（scenario/playbook/hunter/关系）**以数据库为事实源、前端可增删改**，本地文件降级为首次导入的种子。彻底删除 mode 概念，为云攻击/二进制/CTF/域渗透等多场景扩展打底。

**Architecture:**
- 用户在前端选 **Scenario**（顶层入口）。Scenario 引用**唯一一个 Playbook**（多对一，Playbook 可跨场景复用），并**独立选择 `engine`（solo|swarm）**——engine 与 playbook 正交，任意场景任意打法都能在单代理/多代理间来回切。
- **Playbook** = 可复用、可命名、可预设、可调整的**猎手组合**（多对多引用 Hunter）。
- **Hunter** 是唯一执行体，离散原子、可被任意 playbook 自由组合。`kind='orchestrator'` 是特殊猎手：engine=swarm 时由系统自动注入做派活编排，不进 playbook 组合池；`kind='domain'` 是领域猎手（recon/exploitation/traffic-analysis/…）。
- **engine=swarm**：orchestrator + 各领域猎手（deep swarm，orchestrator 经 deep 自带 `task` 工具按 `hunter.description` 动态派活，不硬编码猎手名）。**engine=solo**：把 playbook 内各领域猎手的 `body` 拼进单个 ChatModelAgent 顺序执行。
- **配置流（configstore 多级）**：内存 L1 → redis L2/失效总线 → DB 事实源；本地种子文件仅首次导入。第一期即上 redis 失效总线（多进程 api+runner 需跨进程失效）。此模式后续复用到 config 等其它配置。
- Source（manual/auto）保持不变，仅作审计，正交于执行。task/assignment/cron_schedule 的持久化判别键由 `mode` 改为 `scenario_id`。

**Tech Stack:** Go 1.x（github.com/V3teran/liusha）、cloudwego/eino（adk + deep prebuilt）、hibiken/asynq、golang-migrate v4、PostgreSQL、redis（失效总线）、React 19 + TS + Vite + zustand + react-router v7。

## Global Constraints

- **参考业界最佳实践**：正交分层（scenario/playbook/engine/tools/source）、数据驱动编排（引擎选择从硬编码 switch 移入配置）、装配清单模式（scenario 一次性定人设+装备+打法）。
- **不考虑变更成本**：不为平滑迁移保留过渡层。
- **不保留兼容代码**：删除的字段/类型/迁移不留 deprecated 别名、不留 `omitempty` 兼容旧 payload、不留双读回退。
- **命名变更彻底、零残留**：`role→hunter`、`scanner→runner`、`mode→scenario_id`（持久化判别键）、`运行记录表 hunter→hunter_run`（见 D0）四处改名必须覆盖代码/测试/配置/迁移/脚本/CI/docker/前端/文档，grep 校验零命中旧名。
- **不抄袭**：Playbook/Scenario/Hunter 概念与表结构自主设计，不复制 CyberStrikeAI 的文件结构或命名（CSAI 无 playbook/kill-chain 概念，用 file-based role + 可选 workflow 图；本设计为 DB 事实源 + 多级 configstore，形态不同）。
- **DB 为事实源，文件降级为种子**：4 张配置表（scenario/playbook/playbook_hunter/hunter）由 DB 承载、前端 CRUD；`scenarios/*.md`、`hunters/*.md`、`playbooks/*` 仅作首次导入的种子，导入后 DB 是唯一真相。
- **configstore 多级**：内存 L1 → redis L2/失效总线 → DB。第一期即上 redis 失效总线。
- **中文注释**：新增/修改代码沿用仓库现有中文注释风格与密度。
- **每个任务 TDD**：先写失败测试 → 跑挂 → 最小实现 → 跑过 → 提交。
- **迁移用 golang-migrate**：`db/migrations/NNNN_*.up.sql` + `.down.sql` 成对，序号顺延当前最大号（当前 0084，本计划从 0085 起）。

---

## 关键设计决策（审阅时可逐条否决）

> 这一节把所有具体取舍摆出来。**动手前请逐条确认**；任一条否决都会改变下方任务。

**D0. 运行记录表 `hunter` → `hunter_run` 腾名（前置）**
- 现有 `internal/hunter` 包 + `hunter` 表是**运行记录**（每次 ReAct 运行一行，0054 从 agent_task 改名而来），与本设计新建的**配置表 `hunter`**（离散猎手配置）同名冲突。
- 决策：把运行记录腾名让给配置表——DB 表 `hunter`→`hunter_run`，Go 包 `internal/hunter`→`internal/hunterrun`（包名 `hunterrun`，遵 Go 无下划线惯例；表名 `hunter_run` 带下划线符合 SQL 惯例）。索引/约束 `hunter_pkey`/`hunter_orchestrator_id_idx`/`hunter_owner_idx`/`hunter_owner_type_check`/`hunter_status_check`/`hunter_role_check`/`hunter_orchestrator_id_fkey` 一并改 `hunter_run_*` 前缀。
- 此腾名是**一切的前置**（M0），先做完才能让 M1 的配置表安全占用 `hunter` 名。下游引用点（`cmd/api/scheduler.go`、`cmd/api/main.go`、`cmd/scanner/handler.go`、`cmd/scanner/main.go`、`cmd/e2e/runner.go`、`internal/ingestor/traffic.go` 及各测试）同步改包路径。

**D1. 数据模型：4 张配置表（DB 事实源）**
```sql
-- ① hunter：离散领域猎手（原子，可被任意 playbook 组合）
CREATE TABLE hunter (
    id             uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    code           text        NOT NULL UNIQUE,              -- 稳定引用名（orchestrator/recon/exploitation/traffic-analysis）；代码与种子按 code 引用，不引用随机 uuid
    kind           text        NOT NULL CHECK (kind IN ('orchestrator','domain')),
    name           text        NOT NULL,                     -- 显示名
    description    text        NOT NULL DEFAULT '',          -- 派活摘要：swarm 时注入 deep task 工具，编排者据此判断派给谁（对标 CSAI role.Description 的派活用途，非给人看的简介）
    body           text        NOT NULL DEFAULT '',          -- 方法论正文（charter）：该猎手跑起来时的 system 指令，前端可编辑
    tools          jsonb       NOT NULL DEFAULT '[]'::jsonb, -- 本猎手工具集（tool code 列表）
    max_iterations int         NOT NULL DEFAULT 40,
    enabled        boolean     NOT NULL DEFAULT true,
    created_at     timestamptz NOT NULL DEFAULT now(),
    updated_at     timestamptz NOT NULL DEFAULT now()
);
-- kind='orchestrator'：engine=swarm 时自动注入，不进 playbook 组合池，body 前端可编辑（改成不点名、按 hunter.description 动态派活）
-- kind='domain'      ：领域猎手，可被 playbook 自由组合

-- ② playbook：可复用的猎手组合（有名字、可预设、可调整）
CREATE TABLE playbook (
    id           uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    code         text        NOT NULL UNIQUE,
    name         text        NOT NULL,
    description  text        NOT NULL DEFAULT '',
    enabled      boolean     NOT NULL DEFAULT true,
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now()
);

-- ③ playbook_hunter：组合关系（多对多 + 顺序）
CREATE TABLE playbook_hunter (
    playbook_id  uuid  NOT NULL REFERENCES playbook(id) ON DELETE CASCADE,
    hunter_id    uuid  NOT NULL REFERENCES hunter(id)   ON DELETE RESTRICT,
    position     int   NOT NULL DEFAULT 0,                  -- solo 时=body 拼接序；swarm 时=展示默认序
    PRIMARY KEY (playbook_id, hunter_id)
);
CREATE INDEX playbook_hunter_playbook_idx ON playbook_hunter (playbook_id, position);

-- ④ scenario：场景（引用一个 playbook + 独立选 engine）
CREATE TABLE scenario (
    id           uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    code         text        NOT NULL UNIQUE,
    name         text        NOT NULL,                       -- 显示名
    description  text        NOT NULL DEFAULT '',            -- UI 副标题（给用户看，不注入 AI）
    instruction  text        NOT NULL DEFAULT '',            -- 场景领域侧重（注入 AI，旧 scenario md 正文）
    domain       text        NOT NULL DEFAULT 'web',         -- 交战域（web/ctf/cloud/…）：CLI 扫描工具目录按此过滤可见性，粗粒度、多场景共用；见 D11
    engine       text        NOT NULL CHECK (engine IN ('solo','swarm')),
    playbook_id  uuid        NOT NULL REFERENCES playbook(id) ON DELETE RESTRICT,
    enabled      boolean     NOT NULL DEFAULT true,
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX scenario_playbook_idx ON scenario (playbook_id);
```
关系链：`scenario --playbook_id--> playbook --playbook_hunter(多对多)--> hunter`；`scenario.engine ∈ {solo,swarm}` 与 playbook/hunter 正交。

**D2. engine 与 playbook 正交（核心）**
- engine 是 scenario 上的字段，**任意场景可选任意 engine**，与它引用的 playbook 无关、可来回切。
- `swarm`：orchestrator（kind=orchestrator 的猎手，系统自动注入）+ playbook 内各 domain 猎手做子代理；orchestrator 经 deep `task` 工具按 `hunter.description` **动态派活，不硬编码猎手名**（改写 orchestrator.body：去掉「先派 reconnaissance 再派 exploitation」的点名，改为「按 task 工具列出的 subagent_type 及其 description 选合适子代理派活」）。
- `solo`：不用 orchestrator，把 playbook 内各 domain 猎手的 `body` 按 `position` 拼进单个 ChatModelAgent 的 system 指令，顺序执行。
- 依据：deep swarm 的 `task` 工具本就按传入 SubAgents 的 name+Description 动态注册（`deep_swarm.go:82-96`），框架层天然支持离散+自由组合；唯一障碍是 orchestrator.body 的散文硬编码了猎手名，改文案即可。

**D3. mode 彻底删除，判别键换 scenario_id**
- 删 `task.Mode`、`assignment.Mode`、`cronschedule` 的 mode、runner payload 的 `"mode"`、`TrafficAnalysisToolParams.Mode` 字段、三个死 `Mode` 类型。
- `task`/`assignment`/`cron_schedule` 表 **`mode` 列 → `scenario_id` 列**（裸 `text`，存 scenario 的 **`code`**，不加 CHECK/FK，应用层校验；见 D1 line 46「按 code 引用，不引用随机 uuid」与审计快照理由——task 记录跑的是哪个具名场景，删场景不影响历史可读）。所有 `List(mode)`/`ReapStale(mode)`/JOIN 派生 mode 改为按 `scenario_id`。
- `sitemap/projector.go` 原「只给 active 建图」的判断改为不看 mode（第一期不做 builds_sitemap 能力位，projector 跨场景统一建图或按需后置；见 M4）。

**D4. Source 原地不动**
- `assignment.Source(manual/auto)` 保留，只作审计，不参与任何执行/输入决策。**不改名、不改值。**

**D5. 输入统一为一段 brief（消息模型），host 派生，附件为未来扩展点**
- 变更前 `mode` 决定 active→brief / passive→flow 两种输入形态。变更后**输入统一为一段 brief 文本**（对标 ChatGPT/Claude 的「一条消息 = 文本 + 可选附件」）：所有场景都只喂一段 brief，场景差异体现在 `scenario.instruction + playbook`（AI 拿到 brief 后「做什么」），而非输入长什么样。
- `task.brief`（主，用户输入，必填）；`task.target_host`（派生列，runner 从 brief 抽取后 `SetTargetHost` 回填，不是用户手填的第二形态）。**无 `input`/`mode` 字段，无 brief/flow 二分。**
- 附件（文件/图片/压缩包）是未来多模态扩展点：**第一期不落地**。设计上附件永远存**引用**（对象存储 uri + mime/size/name）不存字节；图片走多模态模型，压缩包/二进制走 sandbox 落地消费，绝不塞进 prompt 或 DB。未来加 `task.attachments jsonb` 引用清单即可，不动主干。
- 校验：`task.Create` 只校验 `scenario_id` + `brief` 非空，无按场景分支。

**D6. 种子文件 → DB 导入**
- 本地种子：`hunters/*.md`（拍平，删 active/passive 子目录）、`playbooks/*.yaml`、`scenarios/*.md`。首次启动/迁移时导入 DB（按 code upsert），之后 DB 是事实源。
- 种子内容映射：hunter md 的 frontmatter→`hunter`（code/kind/name/description/tools/max_iterations）、正文→`hunter.body`；scenario md 的 frontmatter→`scenario`（code/name/description/domain/engine/playbook）、正文→`scenario.instruction`；playbook yaml→`playbook` + `playbook_hunter` 组合。
- orchestrator 作为 `kind='orchestrator'` 的一条 hunter 种子，不出现在任何 playbook 的组合里。

**D7. configstore 多级（内存→redis→DB）**
- 读路径：内存 L1 命中即返回 → 未命中查 redis L2 → 再未命中查 DB 回填。写路径：写 DB → redis 发布失效消息 → 各进程（api/runner）清本地 L1。
- 第一期即上 redis 失效总线（多进程需跨进程失效）。抽象为可复用的 `configstore` 包，后续用于 config 等其它配置。

**D8. role→hunter 命名（einoagent 包）**
- `RoleKind→HunterKind`、`RoleDef→HunterDef`、`RoleOrchestrator→HunterOrchestrator`、`RoleSubAgent→HunterSubAgent`、`LoadRoles→LoadHunters`、`BuildRoleTools→BuildHunterTools`。新增 `HunterSolo`（替代原 passive 单代理的隐式 kind）。文件 `role.go→hunter.go` 等。
- `internal/scenario` 的 `Role`/`LoadRoles` 是**独立系统**（场景），随本重构改为 `Scenario` + DB store，不与 einoagent 混淆。

**D9. scanner→runner 改名（彻底）**
- 目录 `cmd/scanner→cmd/runner`；logger `logx.New("scanner")→"runner"`；audit `ActorScanner="scanner"→ActorRunner="runner"`；config `ScannerConfig→RunnerConfig`、mapstructure/yaml key `scanner→runner`；Makefile/Dockerfile/docker-compose/CI/脚本全改；binary `scanner→runner`。
- **不动** pgx `type scanner interface`（行扫描器，假阳性）、`sql.Scanner`、asynq 队列枚举（与进程名无关）。

**D10. 引擎数据驱动分发**
- 删 `cmd/runner/handler.go` 的 `switch input.Mode`。改为：payload 带 `scenario_id` → 加载 scenario → 按 `scenario.engine` 分发（`swarm`→注入 orchestrator + playbook domain 猎手 → BuildDeepSwarm+RunDeepSwarm；`solo`→拼 body → RunSolo）。
- 超时不再看 mode：按 engine 或配置取（现有两个超时配置值改名为按引擎键 `SoloAgentRunTimeoutSeconds`/`SwarmAgentRunTimeoutSeconds`）。

**D11. tools.yaml 交战域过滤接线**
- **两条正交的「工具」轴，勿混**：
  - **函数工具轴（`hunter.tools`）**：D1 的 `hunter.tools jsonb` 列，值是 `toolRegistry` 里的**内部 Go 函数工具名**（如 `run_command`/`http_request`），经 `BuildHunterTools` 建成 eino `tool.BaseTool` 绑给 LM 做 function-calling。由 hunter 独立决定，scenario 不插手。
  - **CLI 扫描工具目录轴（`manifest.Tool.Scenarios`）**：tools.yaml 里每个**外部安全扫描 CLI**（sqlmap/nuclei 等，装在 pentools 镜像、agent 经 shell 调用）带 `scenarios` 标签，语义为「该扫描工具在哪些交战场景可见」。
- 两轴喂不同消费者、命名空间不重叠（sqlmap 绝不出现在 `hunter.tools` 里），故不存在"按 hunter.tools 过滤 manifest"一说。
- **本决策接的是 CLI 扫描工具目录轴**：`buildToolingCatalog` 渲染的工具索引文本按**当次 scenario 的 `domain`** 过滤（`manifest.FilterByDomain(scen.Domain)`，空 `scenarios`=通用工具全域可见）。
- **过滤键是交战域 `domain`（web/ctf/cloud），不是 scenario code**：tools.yaml 里 `tool.scenarios: [web]` 标的是粗粒度交战域，多个具体场景（web-pentest-killchain、web-api-scan…）共享同一 `domain=web`，故用 `scenario.domain`（D1 新增列）而非 `scenario.code` 匹配。这样新增场景无需回头给每个工具补标签，工具与场景解耦（O(域) 维护量而非 O(场景×工具)）。

**D12. 前端**
- `RolePicker` 改为 `ScenarioPicker`，删 `mode` 过滤（`filter(r => r.mode === mode)`），列出所有 enabled scenario。
- `Composer.tsx` 删 `mode="active"` 硬编码，`startChat(brief, scenarioID)` 发 `scenario_id`。
- 保留 `对话/流量分析` 两个 tab 作**来源/输入形态**维度（非 scenario），每个 tab 内放 ScenarioPicker（AskUserQuestion 已确认「选 1：两 tab 是来源」）。
- 新增 scenario/playbook/hunter 的配置管理 UI（CRUD，见 M8）。

---

## 里程碑总览（按依赖顺序）

| # | 里程碑 | 交付物 | 依赖 |
|---|---|---|---|
| M0 | 运行记录表 hunter→hunter_run 腾名 | 迁移 0085（表/索引/约束 rename）+ 包 `internal/hunter`→`internal/hunterrun` + 下游引用改名 | — |
| M1 | 4 配置表迁移 + store + 种子导入 + configstore 多级 | 迁移 0086（hunter/playbook/playbook_hunter/scenario）+ store 层 + 种子 importer + configstore(内存/redis/DB) | M0 |
| M2 | einoagent 命名重构 role→hunter | HunterDef/HunterKind/LoadHunters/BuildHunterTools + HunterSolo | M1 |
| M3 | 引擎数据驱动分发 + Solo 泛化 | RunSolo 泛化 + orchestrator 动态派活文案改写 | M2 |
| M4 | DB 迁移 mode→scenario_id（task/assignment/cron_schedule）+ store 改造 | 迁移 0087/0088 + store 层改造 + projector 去 mode | M1 |
| M5 | runner 进程装配 + engine 数据驱动派发 + mode 删除收尾 | cmd/runner handler 按 scenario.engine 装配，删所有 mode | M3,M4 |
| M6 | scanner→runner 全量改名 | 目录/config/logger/audit/构建/脚本/CI | M5 |
| M7 | tools.yaml 交战域过滤接线 | buildToolingCatalog 按 scenario.domain 过滤 CLI 扫描工具目录（`manifest.FilterByDomain`，见 D11） | M2 |
| M8 | 前端 ScenarioPicker + scenario/playbook/hunter 配置 UI | RolePicker→ScenarioPicker + 配置管理 CRUD，删 mode | M1,M5 |

每个里程碑结束跑 `go build ./... && go test ./...`（后端）或 `pnpm build && pnpm test`（前端）作为门禁。

---

## M0 — 运行记录表 hunter→hunter_run 腾名（对应 D0）

**目标**：把现有「运行记录」`hunter` 表 + `internal/hunter` 包腾名为 `hunter_run` / `internal/hunterrun`，让出 `hunter` 名给 M1 的配置表。纯机械改名（表/索引/约束/包路径/下游引用），行为不变，测试全绿。**这是整条链的第一步，M1 依赖它。**

**改名映射（全仓一致）**：DB 表 `hunter`→`hunter_run`；索引 `hunter_pkey`→`hunter_run_pkey`、`hunter_orchestrator_id_idx`→`hunter_run_orchestrator_id_idx`、`hunter_owner_idx`→`hunter_run_owner_idx`；约束 `hunter_owner_type_check`→`hunter_run_owner_type_check`、`hunter_status_check`→`hunter_run_status_check`、`hunter_role_check`→`hunter_run_role_check`、`hunter_orchestrator_id_fkey`→`hunter_run_orchestrator_id_fkey`；Go 包 `internal/hunter`（package `hunter`）→`internal/hunterrun`（package `hunterrun`）。

**涉及文件（来自代码核查）**：
- `internal/hunter/{model.go,store.go,store_integration_test.go}` → `internal/hunterrun/`（含 `store.go` 内所有 `FROM/INTO/UPDATE hunter` SQL 字面量改 `hunter_run`）
- 下游 import `internal/hunter`：`cmd/api/scheduler.go`、`cmd/api/scheduler_integration_test.go`、`cmd/api/main.go`、`cmd/scanner/handler.go`、`cmd/scanner/main.go`、`cmd/e2e/runner.go`、`internal/ingestor/traffic.go`

### Task 0.1: 迁移 0085 — 表/索引/约束 rename

**Files:**
- Create: `db/migrations/0085_rename_hunter_to_hunter_run.up.sql`
- Create: `db/migrations/0085_rename_hunter_to_hunter_run.down.sql`

**Interfaces:**
- Produces: 运行记录表更名 `hunter`→`hunter_run`，7 个索引/约束同步改前缀。

> 迁移惯例：序号顺延当前最大 0084，本表占 0085。此迁移**必须先于** M1 的 0086（配置表建 `hunter`），否则新旧 `hunter` 同名冲突。

- [ ] **Step 1: 写 up 迁移**

`0085_rename_hunter_to_hunter_run.up.sql`（按 **当前真实 schema** rename——`\d hunter` 实测：0074 已删 owner 列/索引/约束、新建 task_idx 与 task_id_fkey，故对象清单以现状为准，非 0054 旧名）：
```sql
-- 0085: 运行记录表 hunter → hunter_run，让出 hunter 名给配置表（见 plan D0/M1）。
-- 对象清单按当前 \d hunter 实测：3 索引 + 4 约束（owner_* 在 0074 已随列删除，task_* 是 0074 新增）。
ALTER TABLE hunter RENAME TO hunter_run;

ALTER INDEX hunter_pkey                 RENAME TO hunter_run_pkey;
ALTER INDEX hunter_orchestrator_id_idx  RENAME TO hunter_run_orchestrator_id_idx;
ALTER INDEX hunter_task_idx             RENAME TO hunter_run_task_idx;

ALTER TABLE hunter_run RENAME CONSTRAINT hunter_role_check           TO hunter_run_role_check;
ALTER TABLE hunter_run RENAME CONSTRAINT hunter_status_check         TO hunter_run_status_check;
ALTER TABLE hunter_run RENAME CONSTRAINT hunter_orchestrator_id_fkey TO hunter_run_orchestrator_id_fkey;
ALTER TABLE hunter_run RENAME CONSTRAINT hunter_task_id_fkey         TO hunter_run_task_id_fkey;
```

> 注：其他表指向 hunter 的 FK（`agent_traffic_hunter_id_fkey`、`finding_agent_run_id_fkey`、`llm_invocation_agent_run_id_fkey`、`tool_invocation_agent_task_id_fkey`）名字**不随本次改**——它们挂在各自表上，rename 被引用表不改约束名，PG 会自动更新引用目标。这些历史命名残留属既存技术债，不在 M0 腾名范围。

- [ ] **Step 2: 写 down 迁移**

`0085_rename_hunter_to_hunter_run.down.sql`：逐条反向 rename（`hunter_run`→`hunter`，7 个索引/约束还原原名）。

- [ ] **Step 3: 跑迁移验证 up/down 可逆**

Run: `make migrate-up && make migrate-down && make migrate-up`（或 `migrate -path db/migrations -database "$DATABASE_URL" up` / `down 1`）
Expected: up 后 `\d hunter_run` 见表与 7 个改名后的索引/约束、`hunter` 不存在；down 后还原为 `hunter`。

- [ ] **Step 4: 提交**

```bash
git add db/migrations/0085_rename_hunter_to_hunter_run.up.sql db/migrations/0085_rename_hunter_to_hunter_run.down.sql
git commit -m "feat(db): 0085 运行记录表 hunter→hunter_run 腾名"
```

### Task 0.2: 包改名 internal/hunter→internal/hunterrun + SQL 字面量

**Files:**
- Modify→Rename: `internal/hunter/` → `internal/hunterrun/`（`model.go`/`store.go`/`store_integration_test.go`）

**Interfaces:**
- Produces: package `hunterrun`；`store.go` 内 7 处 SQL（`INSERT INTO hunter`、4 条 `UPDATE hunter`、2 条 `SELECT ... FROM hunter`）改表名 `hunter_run`。
- 类型名（如 `Run`/`Store`）**不改**（本就叫 Run，与新配置表无冲突），仅改包名与 SQL 表名。

- [ ] **Step 1: 改集成测试驱动改名**

`store_integration_test.go`：包声明改 `package hunterrun`；若测试内直接建表/查表用了 `hunter` 字面量，改 `hunter_run`。

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/hunterrun/ 2>&1 | head`
Expected: 目录尚未存在或包名不符，FAIL。

- [ ] **Step 3: 重命名目录并改包名/SQL**

```bash
git mv internal/hunter internal/hunterrun
```
在三文件内：`package hunter`→`package hunterrun`；`model.go` 顶部注释「Package hunter …」→「Package hunterrun …」；`store.go` 6 处 SQL 表名 `hunter`→`hunter_run`。

- [ ] **Step 4: 验证包内测试绿**

Run: `go test ./internal/hunterrun/`
Expected: PASS。

- [ ] **Step 5: 提交**

```bash
git add internal/hunterrun
git commit -m "refactor: internal/hunter→internal/hunterrun（运行记录腾名）"
```

### Task 0.3: 下游 import 路径改名

**Files:**
- Modify: `cmd/api/scheduler.go`、`cmd/api/scheduler_integration_test.go`、`cmd/api/main.go`、`cmd/scanner/handler.go`、`cmd/scanner/main.go`、`cmd/e2e/runner.go`、`internal/ingestor/traffic.go`

**Interfaces:**
- Consumes: `github.com/V3teran/liusha/internal/hunterrun`（原 `.../internal/hunter`）。

> `cmd/scanner/*` 本轮仍是 scanner（M6 才改 runner），此处只改 import 路径，不动进程名。

- [ ] **Step 1: 全量替换 import 路径与包限定符**

把各文件的 `"github.com/V3teran/liusha/internal/hunter"` 改为 `.../internal/hunterrun`；包限定符 `hunter.`（指向该包的调用，如 `hunter.NewStore`/`hunter.Run`）改 `hunterrun.`。**注意区分**：`internal/einoagent` 里未来的 `hunter.HunterDef`（M2）是另一个包，本任务不涉及；此处只改指向运行记录包的引用。

- [ ] **Step 2: 编译 + 全测试**

Run: `go build ./... && go test ./...`
Expected: 全绿，无 `internal/hunter"` 残留引用。

- [ ] **Step 3: grep 校验零残留**

Run: `grep -rn "internal/hunter\"" --include="*.go" . ; grep -rnE "\bTABLE hunter\b|INTO hunter\b|FROM hunter\b|UPDATE hunter\b" db/migrations/0085*.sql`
Expected: 第一条无输出（旧包路径清零）；迁移仅命中 `hunter_run` 相关。

- [ ] **Step 4: 提交**

```bash
git add cmd/ internal/ingestor/traffic.go
git commit -m "refactor: 下游改引 internal/hunterrun"
```

---

## M1 — 4 配置表迁移 + store + 种子导入 + configstore 多级

**目标**：迁移 0086 建 4 张配置表（hunter/playbook/playbook_hunter/scenario，DDL 见 D1）；建 store 层（按 `id` 与 `code` 双路 CRUD）；建种子导入器（首次启动把 `hunters/*.md` + `playbooks/*.yaml` + `scenarios/*.md` 按 `code` **insert-only** 首填进 DB——已存在的 code 跳过不更新，并改写 orchestrator 种子正文为动态派活）；建可复用 `configstore` 包（内存 L1 → redis L2/失效总线 → DB）。此里程碑不碰 einoagent、不改 mode。**DB 是事实源，本地文件仅首次导入的种子。**

### Task 1.1: 迁移 0086 — 4 配置表

**Files:**
- Create: `db/migrations/0086_scenario_playbook.up.sql`
- Create: `db/migrations/0086_scenario_playbook.down.sql`

**Interfaces:**
- Produces: 4 张配置表 `hunter`/`playbook`/`playbook_hunter`/`scenario`（DDL 逐字取自 D1）+ 索引 `playbook_hunter_playbook_idx`、`scenario_playbook_idx`。

> 迁移惯例：`db/migrations/NNNN_*.up.sql` + `.down.sql` 成对。M0 已占 0085（运行记录腾名），本表组占 **0086**。DDL 必须与 D1 完全一致，不得增删字段。

- [ ] **Step 1: 写 up 迁移**

`0086_scenario_playbook.up.sql`：把 D1 的四段 `CREATE TABLE`（hunter → playbook → playbook_hunter → scenario）+ 两条 `CREATE INDEX` 逐字落入。顺序需满足 FK 依赖：先 `hunter`、`playbook`，再 `playbook_hunter`（引用两者），最后 `scenario`（引用 playbook）。文件头加注释指回本 plan D1。

- [ ] **Step 2: 写 down 迁移**

`0086_scenario_playbook.down.sql`：按 FK 反序 `DROP TABLE IF EXISTS`：
```sql
DROP TABLE IF EXISTS scenario;
DROP TABLE IF EXISTS playbook_hunter;
DROP TABLE IF EXISTS playbook;
DROP TABLE IF EXISTS hunter;
```
（索引随表 DROP 自动清除，无需单列。）

- [ ] **Step 3: 跑迁移验证 up/down 可逆**

Run: `make migrate-up && make migrate-down && make migrate-up`（或 `migrate -path db/migrations -database "$DATABASE_URL" up` / `down 1`）
Expected: 无错误；up 后四表存在、`\d hunter` 见 `code text UNIQUE`、`kind CHECK`；down 后四表消失。

- [ ] **Step 4: 提交**

```bash
git add db/migrations/0086_scenario_playbook.up.sql db/migrations/0086_scenario_playbook.down.sql
git commit -m "feat(db): 0086 建 4 配置表 hunter/playbook/playbook_hunter/scenario"
```

### Task 1.2: config store 层 — hunter/playbook/scenario 双路 CRUD

**Files:**
- Create: `internal/config/hunter/{model.go,store.go,store_integration_test.go}`（配置猎手，区别于 M0 的 `internal/hunterrun` 运行记录）
- Create: `internal/config/playbook/{model.go,store.go,store_integration_test.go}`
- Create: `internal/config/scenario/{model.go,store.go,store_integration_test.go}`

> 包路径用 `internal/config/{hunter,playbook,scenario}`，与运行记录 `internal/hunterrun`、旧场景 role 包 `internal/scenario`（M1 结束前仍在，M5 Task 5.5 Step 4b 整包删）物理隔离，import 时用别名 `cfghunter`/`cfgplaybook`/`cfgscenario` 避免与 einoagent 的 `hunter`（M2）冲突。

**Interfaces:**
- Produces（各 store 沿用仓库 `NewStore(pool *pgxpool.Pool) *Store` 惯例，均按 `id`(uuid) 与 `code`(text) 双路读）：
  - `cfghunter`：`type Hunter struct { ID, Code, Kind, Name, Description, Body string; Tools []string; MaxIterations int; Enabled bool; CreatedAt, UpdatedAt time.Time }`；`Create/Update/Delete/GetByID/GetByCode/List(onlyEnabled bool)`、`GetOrchestrator(ctx) (Hunter, error)`（按 `kind='orchestrator' AND enabled` 取全局唯一编排猎手，见 D1；命中多条或零条均报错，保证全局唯一）。`Kind` 校验 `orchestrator|domain`（应用层，与 DB CHECK 双保险）。`Tools` 走 jsonb ↔ `[]string`。
  - `cfgplaybook`：`type Playbook struct { ID, Code, Name, Description string; Enabled bool; ... }` + `type PlaybookHunter struct { PlaybookID, HunterID string; Position int }`；`Create/Update/Delete/GetByID/GetByCode/List`；组合关系 `SetHunters(ctx, playbookID string, items []PlaybookHunter)`（事务内先删后插，按 position）、`ListHunters(ctx, playbookID) ([]Hunter, error)`（JOIN `playbook_hunter` 按 position 排序，用于 solo 拼 body / swarm 列子代理）。
  - `cfgscenario`：`type Scenario struct { ID, Code, Name, Description, Instruction, Domain, Engine, PlaybookID string; Enabled bool; ... }` + 引擎常量 `const EngineSolo = "solo"` / `const EngineSwarm = "swarm"`；`Create/Update/Delete/GetByID/GetByCode/List(onlyEnabled bool)`。`Engine` 校验 `∈ {EngineSolo, EngineSwarm}`；`Domain` 非空（默认 `web`），作 CLI 扫描工具目录过滤键（见 D11/M7）。

- [ ] **Step 1: 写集成测试（先挂）**

各包 `store_integration_test.go` 沿用仓库现有集成测试骨架（pgxpool 连测试库、`t.Cleanup` 清表）。覆盖：
- hunter：Create 后 GetByCode 回读一致；`kind` 非法值 Create 报错；`List(onlyEnabled=true)` 过滤 `enabled=false`；`Tools` jsonb 往返。
- playbook：`SetHunters` 后 `ListHunters` 按 position 有序返回；重复 `SetHunters` 幂等（先删后插）；删 playbook 级联清 `playbook_hunter`（DB `ON DELETE CASCADE`）。
- scenario：Create 引用不存在 playbook_id 报 FK 错；`Engine` 非法值 Create 报错。

Run: `go test ./internal/config/...`　Expected: FAIL（包未建）。

- [ ] **Step 2: 实现三个 store**

按 D1 DDL 列映射写 SQL（`colsSelect` 常量 + `QueryRow`/`Query` 扫描，与 `internal/hunterrun/store.go` 同风格）。`Create` 用 `RETURNING` 回读 uuid+timestamps。`updated_at` 由 `Update` 显式 `now()`。所有写操作参数化（`$1..$n`），无字符串拼接。

- [ ] **Step 3: 跑测试确认通过**

Run: `go test ./internal/config/...`　Expected: PASS。

- [ ] **Step 4: 提交**

```bash
git add internal/config
git commit -m "feat(config): hunter/playbook/scenario store 双路 CRUD"
```


### Task 1.3: 种子导入器 — md/yaml → DB（insert-only 首填）

**Files:**
- Create: `internal/config/seed/{seed.go,seed_test.go}`
- Modify: 种子文件本体——拍平 `hunters/active/*.md` + `hunters/passive/*.md` → `hunters/*.md`（删子目录），补 orchestrator 一条 `kind=orchestrator` 种子；新增 `playbooks/*.yaml`；`scenarios/*.md` frontmatter 去 `mode` 加 `engine`+`playbook`。

**Interfaces:**
- Produces: `func Import(ctx context.Context, dir string, h *cfghunter.Store, p *cfgplaybook.Store, s *cfgscenario.Store) error`——首次启动扫种子目录，按 `code` **仅插入不存在项**（insert-only），已存在的 code 一律跳过，**绝不更新**（DB 是事实源，种子只负责空库首填，见 D6/A3）。

> **insert-only 语义（关键）**：不是 upsert。用 `INSERT ... ON CONFLICT (code) DO NOTHING` 或先 `GetByCode` 判存在再插。理由：DB 是事实源，用户在前端改过的配置绝不能被重启时的种子覆盖。

- [ ] **Step 1: 改种子文件**

- `git mv hunters/active/*.md hunters/passive/*.md hunters/`，删空目录。每个 hunter md frontmatter：`kind` 由旧 `subagent`→`domain`（recon/exploitation/traffic-analysis），去掉 `mode` 相关字段。
- 新增 `hunters/orchestrator.md`：`kind: orchestrator`，`description` 写派活摘要；**body 改写为动态派活文案**（去掉「先派 reconnaissance 再派 exploitation」的点名，改为「按 `task` 工具列出的 subagent_type 及其 description 选合适子代理派活」，见 D2/M3）。
- 新增 `playbooks/web-pentest.yaml`（`code/name/description` + `hunters:` 列表带 position）等，对应现有场景所需组合。
- `scenarios/*.md`：frontmatter 去 `mode`，加 `engine: swarm|solo` + `playbook: <code>` + `domain: web`（交战域，缺省 `web`）；正文即 `instruction`。

- [ ] **Step 2: 写测试（先挂）**

`seed_test.go`：
- 建临时种子目录 + 空测试库，`Import` 后三表按 code 存在、hunter.body/tools 正确、playbook_hunter 组合有序。
- **insert-only 断言**：先 `Import` 一次，改某 hunter DB 里的 body，再 `Import` 第二次，断言 body **未被种子覆盖**（证明不是 upsert）。
- orchestrator 种子 `kind=orchestrator` 且不出现在任何 playbook 组合里。

Run: `go test ./internal/config/seed/`　Expected: FAIL。

- [ ] **Step 3: 实现 Import**

复用 einoagent frontmatter 解析风格（`splitHunterFrontmatter` 思路）解析 md；yaml 用仓库现有 yaml 库解析 playbook。逐类 `GetByCode` 判存在→不存在才 `Create`。playbook 组合经 `SetHunters` 落 `playbook_hunter`。全程一个 `ctx`，任一类失败返回 wrap 错误。

- [ ] **Step 4: 跑测试确认通过 + 提交**

Run: `go test ./internal/config/seed/`　Expected: PASS。
```bash
git add internal/config/seed hunters playbooks scenarios
git commit -m "feat(config): 种子导入器（insert-only 首填）+ 拍平 hunters + orchestrator 动态派活文案"
```


### Task 1.4: configstore 多级缓存包（内存 L1 → redis L2 → DB）

**Files:**
- Create: `internal/configstore/{store.go,cache.go,invalidation.go,store_test.go}`

**Interfaces:**
- Produces: `func New(pool *pgxpool.Pool, rdb *redis.Client) *Store`——可复用多级缓存，包裹 Task 1.2 的三个底层 store。
  - **单条读**（缓存键按各自访问路径定，都是多级 L1→L2→DB 全程生效）：
    - scenario **双路**：`ScenarioByCode(ctx, code)`（**运行期派发热路径**——`task.scenario_id` 存的是 code，见 D3；缓存键 `scenario:code:{code}`）+ `ScenarioByID(ctx, id)`（admin CRUD `:id` 用；缓存键 `scenario:id:{id}`）。两路命中同一份 entry（code-map 与 id-map 各建一张映射指向同值，写失效时两张一起清）。
    - playbook / hunter **仅 by-id**：`PlaybookByID(ctx, id)`、`HunterByID(ctx, id)`。它们不经 code 访问——派发时经 `scenario.playbook_id`(uuid FK) 拿 playbook、经 `playbook_hunter`(uuid FK) 拿猎手，CRUD 走 `:id`，无 by-code 消费者，故不设 by-code 读（避免死代码，见 Global Constraints）。
    - `PlaybookHunters(ctx, playbookID) ([]cfghunter.Hunter, error)`（按 position 有序的 domain 猎手，缓存键 `playbook_hunters:{playbookID}`）、`Orchestrator(ctx) (cfghunter.Hunter, error)`（按 `kind='orchestrator' AND enabled` 取全局唯一编排猎手，包裹 `cfghunter.GetOrchestrator`，见 D1；缓存于固定哨兵键 `hunter:orchestrator`）。
    - 读流程：L1 命中即返；未命中查 redis L2（json，键同 L1）；再未命中查 DB 回填 L1+L2。
  - **列表读（不缓存，直穿底层 store）**：`ListScenarios(ctx, onlyEnabled)`、`ListPlaybooks(ctx)`、`ListHunters(ctx, onlyEnabled)`——仅 Task 1.5 的 admin CRUD `GET` 列表页低频调用，集合结果缓存的失效成本（任一成员增删改都要废整表）远超收益，故直穿 DB，不落 L1/L2。
  - 写（前端 CRUD 走这里，保证跨进程一致）：`SaveScenario/SavePlaybook/SaveHunter/SetHunters/Delete*`——写 DB → 删本地 L1 → redis `PUBLISH` 失效消息（channel `configstore:invalidate`，payload `{kind,id,code}`）。**scenario 失效必须同时带 id 与 code**（它有 code-map 与 id-map 两张，只带一个会残留另一张脏条目）；playbook/hunter 只有 id-map，`code` 可空。`SetHunters(ctx, playbookID, items)` 改 playbook 组合，额外失效 `playbook_hunters:{playbookID}`；改动任一 `kind='orchestrator'` 猎手额外失效哨兵键 `hunter:orchestrator`。
  - `Subscribe(ctx)`：后台 goroutine 订阅失效 channel，收到即清对应 L1 条目并删同键 L2（让下次回填）——scenario 按 id 与 code 两张映射一起清，playbook/hunter 按 id 清；派生键（`playbook_hunters:{id}`、`hunter:orchestrator`）按上述规则一并清。api/runner 进程各自 `go store.Subscribe(ctx)`。

> **为何第一期即上 redis 失效总线**：api 与 runner 是**多进程**，前端在 api 改了配置，runner 的 L1 必须被动失效，否则 runner 用旧配置装配。单进程内存缓存不够（见 D7）。

- L1 用 `sync.RWMutex` + `map[string]entry`（entry 带值，无 TTL，靠失效消息驱逐）。L2 redis 键与 L1 同键：scenario 两张 `configstore:scenario:code:{code}` 与 `configstore:scenario:id:{id}` 指向同值；playbook/hunter 单张 `configstore:{kind}:id:{id}`；派生键 `configstore:playbook_hunters:{id}` / `configstore:hunter:orchestrator`。均设保守 TTL（如 10min）兜底防订阅漏消息。多级读同键贯通：L1 miss → L2（同键）→ DB 回填 L1+L2，L2 层真正生效（不再有只写不读的死层）。scenario DB 回填时 code 与 id 两张一并写，供两路复用。

- [ ] **Step 1: 写单元测试（先挂）**

`store_test.go`（redis 用 miniredis 或测试实例，DB 用测试库或 mock 底层 store 接口）：
- 读穿透回填：首次读打 DB，二次读命中 L1（DB 调用计数不增）。
- 写失效：进程 A 写 → PUBLISH → 进程 B（第二个 Store 实例订阅同 redis）L1 被清，下次读拿到新值。
- L2 命中：清 L1 后读，命中 redis 不打 DB。

Run: `go test ./internal/configstore/`　Expected: FAIL。

- [ ] **Step 2: 实现多级读写 + 订阅**

`cache.go` 管 L1；`invalidation.go` 管 redis pub/sub（沿用 `internal/proxy/publisher.go` 的 go-redis v9 风格）；`store.go` 编排读写路径。json 编解码复用标准库。

- [ ] **Step 3: 跑测试确认通过 + 提交**

Run: `go test ./internal/configstore/`　Expected: PASS。
```bash
git add internal/configstore
git commit -m "feat(configstore): 内存L1→redisL2/失效总线→DB 多级缓存"
```


### Task 1.5: httpapi CRUD handlers — scenario/playbook/hunter

**Files:**
- Create: `internal/httpapi/config_handler.go`（scenario/playbook/hunter 三资源 CRUD handler）
- Create: `internal/httpapi/config_handler_test.go`
- Modify: `internal/httpapi/server.go`（注册路由 + `Deps` 加 `ConfigStore` 字段）

**Interfaces:**
- Produces（沿用仓库 gin handler 工厂 + 窄接口 `d.Xxx` 惯例，路由挂在现有 `/api` group 下，与 M8 前端契约逐字对齐）：
  - scenario：`GET /api/scenarios`（enabled 列表，返回 `{id,code,name,description}`，供 ScenarioPicker）、`GET /api/scenarios/:id`、`POST /api/scenarios`、`PUT /api/scenarios/:id`、`DELETE /api/scenarios/:id`
  - playbook：`GET /api/playbooks`、`GET /api/playbooks/:id`（含 `hunters:[{hunter_id,position}]`）、`POST`、`PUT /api/playbooks/:id`、`DELETE /api/playbooks/:id`
  - hunter：`GET /api/hunters`、`GET /api/hunters/:id`、`POST /api/hunters`、`PUT /api/hunters/:id`、`DELETE /api/hunters/:id`
- handler 依赖窄接口 `ConfigAPI`（`*configstore.Store` 满足）：读走 configstore 缓存，写走 configstore（自动 DB + 失效广播，见 Task 1.4）。scenario 的 `GET/PUT/DELETE :id` 单条读经 `ScenarioByID`（id-map 那一路，见 Task 1.4）——这是 `ScenarioByID` 的唯一消费者，与运行期热路径 `ScenarioByCode` 分工（code 派发 vs id 管理），二者都非死代码。

> **写路径必须走 configstore 而非底层 store**，否则前端改配置后 runner 的 L1 不失效（见 D7）。GET 列表用 configstore 读；DELETE scenario 若被 task 引用不阻断（scenario_id 是裸 text 无 FK，见 D3），但 DELETE playbook 若被 scenario 引用会撞 DB `ON DELETE RESTRICT`，handler 捕获并返 409 + 中文提示。

- [ ] **Step 1: 写 handler 测试（先挂）**

`config_handler_test.go` 用 gin test recorder + mock `ConfigAPI`：
- `GET /api/scenarios` 只返 enabled、字段裁剪为 `{id,code,name,description}`。
- `POST /api/hunters` 体缺 `code`/`kind` 非法 → 400 中文错误；`kind=foo` → 400。
- `PUT /api/playbooks/:id` 带 `hunters` 组合 → 调 `SavePlaybook` + `SetHunters`。
- `DELETE /api/playbooks/:id` 底层返 RESTRICT 冲突 → 409。

Run: `go test ./internal/httpapi/ -run Config`　Expected: FAIL。

- [ ] **Step 2: 实现 handler + 注册路由**

按 `finding_handler.go` 风格写工厂函数；请求体用 gin `ShouldBindJSON` + 应用层校验（engine/kind 白名单、code/name 非空）。`server.go` 的 `Deps` 加 `ConfigStore ConfigAPI`，在 `/api` group 内注册上述 15 条路由。

- [ ] **Step 3: 跑测试确认通过 + 提交**

Run: `go test ./internal/httpapi/ -run Config`　Expected: PASS。
```bash
git add internal/httpapi/config_handler.go internal/httpapi/config_handler_test.go internal/httpapi/server.go
git commit -m "feat(httpapi): scenario/playbook/hunter CRUD 端点（走 configstore）"
```


---

## M2 — einoagent 命名重构 role→hunter

**目标**：把 `internal/einoagent` 包内 role 命名彻底改为 hunter，新增 `HunterSolo` kind。纯机械改名 + 一个新常量，行为不变；测试同步改名后全绿。此里程碑不碰 DB、不碰 runner 装配逻辑。

**改名映射（全包一致）**：`RoleKind→HunterKind`、`RoleDef→HunterDef`、`RoleOrchestrator→HunterOrchestrator`、`RoleSubAgent→HunterSubAgent`、`LoadRoles→LoadHunters`、`BuildRoleTools→BuildHunterTools`、`Orchestrator()→Orchestrator()`（保留名，语义不变）、`SubAgents()→SubAgents()`（保留）。新增 `HunterSolo HunterKind = "solo"`。

**涉及文件（来自代码核查）**：
- `internal/einoagent/role.go` → `hunter.go`（含 `role.go:28,32,34,38-46,51,86,116,133,164` 等全部标识符 + 内部 `roleFrontDelim`/`splitRoleFrontmatter`→`hunterFrontDelim`/`splitHunterFrontmatter`）
- `internal/einoagent/role_tools.go` → `hunter_tools.go`（`BuildRoleTools`@118 → `BuildHunterTools`）
- `internal/einoagent/deep_swarm.go`（`DeepSwarmConfig.Orchestrator/SubAgents` 的类型 `RoleDef`@30,31；`BuildRoleTools`@84,122）
- `internal/einoagent/role_test.go` → `hunter_test.go`
- `internal/einoagent/deep_swarm_test.go`（14,17,22,33,34,50,51,62,63,65,90-96,103）
- `internal/einoagent/dep_swarm_internal_test.go`（83,92,97）
- `internal/einoagent/role_files_test.go` → `hunter_files_test.go`（21,33,40,63）
- `internal/einoagent/traffic_analysis_tools.go:84`（注释引用）
- 下游（M5 再改调用点，但类型改名会连带编译错，本里程碑一并改）：`cmd/scanner/main.go:221,235,239`、`cmd/scanner/handler.go:58,62`、`cmd/scanner/handler_active_eino.go:58,62,187,194`、`internal/builder/hunter/skill.go:28`（注释）

### Task 2.1: 改名 hunter.go 核心类型 + 新增 HunterSolo

**Files:**
- Modify→Rename: `internal/einoagent/role.go` → `internal/einoagent/hunter.go`
- Modify→Rename: `internal/einoagent/role_test.go` → `internal/einoagent/hunter_test.go`

**Interfaces:**
- Produces:
  - `type HunterKind string`
  - `const HunterOrchestrator HunterKind = "orchestrator"`
  - `const HunterSubAgent HunterKind = "subagent"`
  - `const HunterSolo HunterKind = "solo"`（新增）
  - `type HunterDef struct { ID/Name/Description string; Kind HunterKind; Tools []string; MaxIterations int; SystemPrompt string \`yaml:"-"\`; SourceFile string \`yaml:"-"\` }`
  - `func LoadHunters(dir string) ([]HunterDef, error)`
  - `func Orchestrator(hs []HunterDef) (HunterDef, error)`
  - `func SubAgents(hs []HunterDef) []HunterDef`

- [ ] **Step 1: 改测试文件（先改测试，令其驱动改名）**

在 `hunter_test.go` 中，把所有 `RoleKind/RoleDef/RoleOrchestrator/RoleSubAgent/LoadRoles` 替换为对应 hunter 名。新增一条断言 `HunterSolo` 存在且值为 `"solo"`：

```go
func TestHunterSoloKindExists(t *testing.T) {
	if HunterSolo != "solo" {
		t.Fatalf("HunterSolo = %q, want solo", HunterSolo)
	}
}
```

并把 `parseRole` 对 kind 的合法性断言扩展为接受 `solo`（见 Step 3）。

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/einoagent/ -run 'Hunter' -v`
Expected: FAIL（`undefined: HunterDef` 等）

- [ ] **Step 3: 重命名文件并改名所有标识符**

```bash
git mv internal/einoagent/role.go internal/einoagent/hunter.go
git mv internal/einoagent/role_test.go internal/einoagent/hunter_test.go
```

在 `hunter.go` 内执行改名映射（见里程碑头），并：
- 新增 `HunterSolo HunterKind = "solo"`。
- `parseRole`（→保留函数名 `parseHunter`）对 kind 的校验从「只允许 orchestrator|subagent」扩展为「orchestrator|subagent|solo」。默认值逻辑：kind 空时若历史默认 subagent，保持不变。
- 内部私有标识符 `roleFrontDelim/roleNewline/splitRoleFrontmatter/sortRolesByID` → `hunterFrontDelim/hunterNewline/splitHunterFrontmatter/sortHuntersByID`。

具体 kind 校验片段：
```go
switch h.Kind {
case HunterOrchestrator, HunterSubAgent, HunterSolo:
	// ok
default:
	return HunterDef{}, fmt.Errorf("%s: kind 非法 %q（应为 orchestrator|subagent|solo）", path, h.Kind)
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/einoagent/ -run 'Hunter' -v`
Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add internal/einoagent/hunter.go internal/einoagent/hunter_test.go
git commit -m "refactor: einoagent role→hunter 改名，新增 HunterSolo kind"
```

### Task 2.2: 改名 hunter_tools.go + deep_swarm.go + 其余测试

**Files:**
- Modify→Rename: `internal/einoagent/role_tools.go` → `internal/einoagent/hunter_tools.go`
- Modify: `internal/einoagent/deep_swarm.go`
- Modify: `internal/einoagent/deep_swarm_test.go`
- Modify: `internal/einoagent/dep_swarm_internal_test.go`
- Modify→Rename: `internal/einoagent/role_files_test.go` → `internal/einoagent/hunter_files_test.go`
- Modify: `internal/einoagent/traffic_analysis_tools.go`（注释）

**Interfaces:**
- Consumes: `HunterDef`（Task 2.1）
- Produces: `func BuildHunterTools(h HunterDef, c ToolBuildCtx) ([]tool.BaseTool, error)`；`DeepSwarmConfig{ Orchestrator HunterDef; SubAgents []HunterDef }`

- [ ] **Step 1: 改所有测试引用为 hunter 名**

在 `deep_swarm_test.go`、`dep_swarm_internal_test.go`、`hunter_files_test.go` 中把 `RoleDef/RoleOrchestrator/RoleSubAgent/BuildRoleTools/LoadRoles` 全部替换为 hunter 对应名。

- [ ] **Step 2: 跑测试确认失败**

Run: `go build ./internal/einoagent/ 2>&1 | head`
Expected: 编译错误（`BuildRoleTools` 未定义等）

- [ ] **Step 3: 改名实现**

```bash
git mv internal/einoagent/role_tools.go internal/einoagent/hunter_tools.go
git mv internal/einoagent/role_files_test.go internal/einoagent/hunter_files_test.go
```
- `hunter_tools.go`：`func BuildRoleTools(role RoleDef,...)` → `func BuildHunterTools(h HunterDef,...)`，函数体内 `role.Tools`→`h.Tools`、`role.ID`→`h.ID`。
- `deep_swarm.go`：`DeepSwarmConfig.Orchestrator RoleDef`→`HunterDef`（L30）、`SubAgents []RoleDef`→`[]HunterDef`（L31）、循环变量 `role`→`h`、`BuildRoleTools(...)`→`BuildHunterTools(...)`（L84,122）。
- `traffic_analysis_tools.go:84` 注释 `BuildRoleTools`→`BuildHunterTools`。

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/einoagent/ -v`
Expected: PASS（全包）

- [ ] **Step 5: 修下游编译点（类型改名连带）**

改 `cmd/scanner/main.go`（`einoagent.LoadRoles`→`LoadHunters`@221,235；`einoagent.RoleDef`→`HunterDef`@239）、`cmd/scanner/handler.go`（`[]einoagent.RoleDef`→`[]HunterDef`@58；`einoagent.RoleDef`→`HunterDef`@62）、`cmd/scanner/handler_active_eino.go`（`composeOrchestratorInstruction(role einoagent.RoleDef)`→`HunterDef`@187；`composeSubAgentInstruction(role einoagent.RoleDef)`→`HunterDef`@194）、`internal/builder/hunter/skill.go:28` 注释。

> 注：这些文件在 M5/M6 还会大改（装配逻辑 + scanner→runner），此处仅做类型改名以恢复编译。

- [ ] **Step 6: 跑全量构建 + 测试**

Run: `go build ./... && go test ./internal/einoagent/ ./cmd/scanner/`
Expected: PASS

- [ ] **Step 7: 提交**

```bash
git add -A
git commit -m "refactor: BuildHunterTools/HunterDef 改名贯通 deep_swarm 与下游调用点"
```

---

## M3 — 引擎数据驱动分发 + Solo 泛化

**目标**：把 `runSingleAgent` 的薄封装 `RunTrafficAnalysis`（名字带 passive/traffic 语义）泛化为 `RunSolo`，脱去主被动/流量语义；结果类型 `TrafficAnalysisResult` 改名 `AgentResult`。行为不变，纯泛化改名。引擎按 `scenario.Engine` 分发的**装配逻辑**（engine 在 scenario 上、与 playbook 正交，见 D2）在 M5 的 runner handler 里落地，本里程碑只把 einoagent 侧的可复用入口备好。

**背景事实（代码核查）**：`runSingleAgent`（traffic_analysis.go:72）已是通用单代理跑法，`RunTrafficAnalysis`（:54）仅薄封装它并硬编码 name=`"traffic-analysis"`/desc 含「passive」。`TrafficAnalysisResult`（:36）被 swarm 与 solo 两路共用，`RunDeepSwarm`（deep_swarm.go:161）也返回它。

### Task 3.1: TrafficAnalysisResult → AgentResult 改名

**Files:**
- Modify: `internal/einoagent/traffic_analysis.go`（结构体 `:36`、`runSingleAgent`/`drainAgentEvents` 返回类型 `:72,88,100,101`、`RunTrafficAnalysis` 返回 `:54`）
- Modify: `internal/einoagent/deep_swarm.go`（`RunDeepSwarm` 返回类型 `:161`）
- Modify: 引用 `TrafficAnalysisResult` 的测试文件（`traffic_analysis` 相关 test）

**Interfaces:**
- Produces: `type AgentResult struct { FinalText string; ToolCalls []string }`（替代 `TrafficAnalysisResult`，字段不变）

- [ ] **Step 1: 全包 grep 定位引用**

Run: `grep -rn 'TrafficAnalysisResult' internal/ cmd/`
Expected: 列出所有引用点（traffic_analysis.go / deep_swarm.go + 测试）

- [ ] **Step 2: 改测试引用为 AgentResult，跑挂**

把测试中 `TrafficAnalysisResult` 替换为 `AgentResult`。
Run: `go build ./internal/einoagent/ 2>&1 | head`
Expected: 编译错误（`AgentResult` 未定义）

- [ ] **Step 3: 改名结构体与所有返回类型**

`traffic_analysis.go`：
```go
// AgentResult 是一次单/多代理运行的产物摘要（swarm 与 solo 共用）。
type AgentResult struct {
	FinalText string   // 最终 assistant 文字输出
	ToolCalls []string // 按顺序调用过的工具名
}
```
把 `runSingleAgent`、`drainAgentEvents`、`RunTrafficAnalysis` 的返回类型 `TrafficAnalysisResult` 全改 `AgentResult`；`deep_swarm.go` 的 `RunDeepSwarm` 返回类型同改。

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/einoagent/`
Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add internal/einoagent/
git commit -m "refactor: TrafficAnalysisResult→AgentResult，脱去 passive 语义"
```

### Task 3.2: RunTrafficAnalysis → RunSolo 泛化

**Files:**
- Modify: `internal/einoagent/traffic_analysis.go` → 重命名为 `internal/einoagent/solo.go`（`RunTrafficAnalysis`→`RunSolo`，参数 `flowText`→`userText`，agentSpec.name/desc 改为入参）
- Modify: 调用点 `cmd/scanner/handler_passive_eino.go:156`（M5 会重写此文件，此处仅改函数名保编译）
- Modify: 相关测试

**Interfaces:**
- Consumes: 无新增
- Produces: `func RunSolo(ctx context.Context, name, desc string, m model.ToolCallingChatModel, tools []tool.BaseTool, instruction, userText string, maxIters int, middlewares []adk.AgentMiddleware, handlers []adk.ChatModelAgentMiddleware, logger zerolog.Logger, opts ...adk.AgentRunOption) (AgentResult, error)`

- [ ] **Step 1: 写失败测试**

新增 `internal/einoagent/solo_test.go`（若已有 traffic_analysis 测试则改名迁入），断言 RunSolo 以传入 name 装配代理。用假 ChatModel（仓库已有 fake，参考 deep_swarm_test.go 的构造）：

```go
func TestRunSolo_usesProvidedName(t *testing.T) {
	// Arrange：假 model 直接产出终态文字、不调工具
	ctx := context.Background()
	m := newFakeChatModel("done: 分析完成") // 复用测试内既有 fake 构造
	// Act
	res, err := RunSolo(ctx, "traffic-analysis", "分析一条流量", m, nil,
		"你是分析师", "GET / HTTP/1.1", 5, nil, nil, zerolog.Nop())
	// Assert
	if err != nil {
		t.Fatalf("RunSolo: %v", err)
	}
	if res.FinalText == "" {
		t.Error("FinalText 为空")
	}
}
```

> 若仓库无 `newFakeChatModel`，改用 deep_swarm_test.go 中现有的 fake model 构造方式（实现者对齐既有测试 helper）。

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/einoagent/ -run TestRunSolo -v`
Expected: FAIL（`undefined: RunSolo`）

- [ ] **Step 3: 泛化实现**

```bash
git mv internal/einoagent/traffic_analysis.go internal/einoagent/solo.go
```
把 `RunTrafficAnalysis` 改为：
```go
// RunSolo 用 eino ChatModelAgent 跑单代理（solo 引擎）。name/desc 由调用方传入
// （不再硬编码 traffic-analysis）；maxIters<=0 回退 defaultSoloMaxIters。
func RunSolo(ctx context.Context, name, desc string, m model.ToolCallingChatModel, tools []tool.BaseTool, instruction, userText string, maxIters int, middlewares []adk.AgentMiddleware, handlers []adk.ChatModelAgentMiddleware, logger zerolog.Logger, opts ...adk.AgentRunOption) (AgentResult, error) {
	if maxIters <= 0 {
		maxIters = defaultSoloMaxIters
	}
	return runSingleAgent(ctx, agentSpec{name: name, desc: desc, maxIters: maxIters},
		m, tools, instruction, userText, middlewares, handlers, logger, opts...)
}
```
把常量 `defaultTrafficAnalysisMaxIters`→`defaultSoloMaxIters`（值不变）。文件内注释里的 "passive 流量"/"trafficAnalysis" 措辞改为中性 "solo 单代理"。

- [ ] **Step 4: 改调用点保编译**

`cmd/scanner/handler_passive_eino.go:156` 的 `einoagent.RunTrafficAnalysis(...)` → `einoagent.RunSolo(...)`，补 name/desc 实参（`"traffic-analysis"`, `"分析一条流量挖漏洞"`）。

- [ ] **Step 5: 跑测试 + 构建确认通过**

Run: `go build ./... && go test ./internal/einoagent/`
Expected: PASS

- [ ] **Step 6: 提交**

```bash
git add -A
git commit -m "refactor: RunTrafficAnalysis→RunSolo 泛化，name/desc 参数化"
```

---

## M4 — DB 迁移 mode→scenario_id + store 层改造

**目标**：把 `task`/`assignment`/`cron_schedule` 三表的 `mode` 列改为 `scenario_id`（裸 text，无 CHECK 无外键，应用层校验，见 D2 + 已确认决策）；`finding`/`conversation` 的 JOIN 派生 mode 改为派生 scenario_id；所有 store 层 `List(mode)`/`ReapStale(mode)`/校验/scan 改为 scenario_id。删除三个 `Mode` 类型定义。

**真实 DDL（代码核查）**：
- `task`（0073）：`mode text NOT NULL CHECK (mode IN ('active','passive'))`；索引 `task_mode_status_idx (mode,status,created_at DESC)`、`task_heartbeat_idx (mode,heartbeat_at) WHERE status='active'`；表注释含 mode。
- `assignment`（0078:30）+ `cron_schedule`（0078:16）：各 `mode text NOT NULL CHECK (...)`。
- `finding`/`conversation` 无 mode 列，经 `JOIN task t` 取 `t.mode`。

**索引取舍（已在设计说明中定）**：`task_heartbeat_idx` 去掉 mode 前导列 → `(heartbeat_at) WHERE status='active'`（reaper 跨场景巡逻，不需按场景分片）；`task_mode_status_idx` → `task_scenario_status_idx (scenario_id,status,created_at DESC)`（保留按场景列表能力）。

**迁移序号**：M0 占 0085、M1 占 0086，本里程碑顺延占 **0087**（task）与 **0088**（assignment+cron_schedule）。

### Task 4.1: 迁移 0087 — task 表 mode→scenario_id

**Files:**
- Create: `db/migrations/0087_task_scenario_id.up.sql`
- Create: `db/migrations/0087_task_scenario_id.down.sql`

> 前提：仓库迁移惯例是空库重建、不搬存量（见 0078:40 `DELETE FROM task`）。本迁移遵循同惯例：不保留 active/passive 存量映射，直接改列。

- [ ] **Step 1: 写 up 迁移**

`0087_task_scenario_id.up.sql`:
```sql
-- 0087: task.mode → scenario_id（删除主被动概念，改场景判别键）。
-- 见 docs/superpowers/plans/2026-08-01-scenario-playbook-architecture.md D2。
-- scenario_id 为裸 text（配置驱动，无 CHECK 无外键，应用层校验）。
-- 空库惯例：不搬存量，直接重置。

DELETE FROM task;  -- 空库前提，避免 NOT NULL 无默认值失败

-- 删依赖 mode 的索引
DROP INDEX IF EXISTS task_mode_status_idx;
DROP INDEX IF EXISTS task_heartbeat_idx;

-- 换列
ALTER TABLE task DROP COLUMN mode;
ALTER TABLE task ADD COLUMN scenario_id text NOT NULL;

-- 重建索引（heartbeat 去 mode 前导；status 索引以 scenario_id 前导）
CREATE INDEX task_scenario_status_idx ON task (scenario_id, status, created_at DESC);
CREATE INDEX task_heartbeat_idx ON task (heartbeat_at) WHERE status = 'active';

COMMENT ON TABLE task IS '统一扫描任务；scenario_id 标识所属场景（配置驱动，应用层校验）';
COMMENT ON COLUMN task.scenario_id IS '所属场景 id（如 web-pentest-killchain）；裸 text，无 DB 约束';
```

- [ ] **Step 2: 写 down 迁移**

`0087_task_scenario_id.down.sql`:
```sql
-- 回滚 0087：scenario_id → mode。
DELETE FROM task;
DROP INDEX IF EXISTS task_scenario_status_idx;
DROP INDEX IF EXISTS task_heartbeat_idx;
ALTER TABLE task DROP COLUMN scenario_id;
ALTER TABLE task ADD COLUMN mode text NOT NULL CHECK (mode IN ('active','passive'));
CREATE INDEX task_mode_status_idx ON task (mode, status, created_at DESC);
CREATE INDEX task_heartbeat_idx ON task (mode, heartbeat_at) WHERE status = 'active';
COMMENT ON TABLE task IS '统一扫描任务（合并 active_scan + passive_session）；mode 区分主动/被动';
```

- [ ] **Step 3: 跑迁移验证 up/down 可逆**

Run: `make migrate-up && make migrate-down && make migrate-up`（或仓库实际迁移命令；若无，用 `migrate -path db/migrations -database "$DATABASE_URL" up` / `down 1`）
Expected: 无错误，task 表最终有 scenario_id 列、无 mode 列

- [ ] **Step 4: 提交**

```bash
git add db/migrations/0087_task_scenario_id.up.sql db/migrations/0087_task_scenario_id.down.sql
git commit -m "feat(db): 0087 task.mode→scenario_id，重建索引"
```

### Task 4.2: 迁移 0088 — assignment + cron_schedule mode→scenario_id

**Files:**
- Create: `db/migrations/0088_assignment_scenario_id.up.sql`
- Create: `db/migrations/0088_assignment_scenario_id.down.sql`

- [ ] **Step 1: 写 up 迁移**

`0088_assignment_scenario_id.up.sql`:
```sql
-- 0088: assignment + cron_schedule 的 mode → scenario_id。
-- source 列保持不变（manual/auto，审计用，见 D3）。
DELETE FROM task;        -- FK CASCADE 会连带；先清子表再改父
DELETE FROM assignment;
DELETE FROM cron_schedule;

ALTER TABLE assignment DROP COLUMN mode;
ALTER TABLE assignment ADD COLUMN scenario_id text NOT NULL;

ALTER TABLE cron_schedule DROP COLUMN mode;
ALTER TABLE cron_schedule ADD COLUMN scenario_id text NOT NULL;

COMMENT ON COLUMN assignment.scenario_id IS '本次下发所属场景 id（裸 text，应用层校验）';
COMMENT ON COLUMN cron_schedule.scenario_id IS '定时触发时下发的场景 id（裸 text，应用层校验）';
```

- [ ] **Step 2: 写 down 迁移**

`0088_assignment_scenario_id.down.sql`:
```sql
DELETE FROM task;
DELETE FROM assignment;
DELETE FROM cron_schedule;
ALTER TABLE assignment DROP COLUMN scenario_id;
ALTER TABLE assignment ADD COLUMN mode text NOT NULL CHECK (mode IN ('active','passive'));
ALTER TABLE cron_schedule DROP COLUMN scenario_id;
ALTER TABLE cron_schedule ADD COLUMN mode text NOT NULL CHECK (mode IN ('active','passive'));
```

- [ ] **Step 3: 跑迁移验证可逆**

Run: `make migrate-up && make migrate-down && make migrate-up`
Expected: 无错误

- [ ] **Step 4: 提交**

```bash
git add db/migrations/0088_assignment_scenario_id.up.sql db/migrations/0088_assignment_scenario_id.down.sql
git commit -m "feat(db): 0088 assignment/cron_schedule mode→scenario_id"
```

### Task 4.3: task 包 store + model 改造（mode→ScenarioID）

**Files:**
- Modify: `internal/task/model.go`（删 `Mode` 类型/常量 `:19-24`；`Task.Mode`→`Task.ScenarioID string` `:42`；`NewParams.Mode`→`NewParams.ScenarioID` + 删 `NewParams.Mode` `:59`；Create 只校验 scenario_id + brief 非空，见下）
- Modify: `internal/task/store.go`（`colsSelect` `:19` mode→scenario_id；`Create` switch `:34-45` 改为 ScenarioID 非空校验；INSERT `:47-49`；`List(mode)`→`List(scenarioID)` `:58-70`；`scan` `:108,114`）
- Modify: `internal/task/lifecycle.go`（`ReapStale(mode)`→按需去 mode 参数 `:80-90`）
- Modify: `internal/task/store_test.go` 等测试

**Interfaces:**
- Consumes: —
- Produces:
  - `Task.ScenarioID string`（替代 `Task.Mode Mode`）
  - `NewParams { ScenarioID string; AssignmentID string; Brief string; TargetHost string }`
  - `func (s *Store) Create(ctx, p NewParams) (Task, error)`（校验：AssignmentID 非空、ScenarioID 非空、Brief 非空；TargetHost 可空，由 runner 抽取回填，Create 不再按 mode 分支）
  - `func (s *Store) List(ctx context.Context, scenarioID string, limit int) ([]Task, error)`（scenarioID 空=不过滤）
  - `func (s *Store) ReapStale(ctx context.Context, staleAfter time.Duration) (int, error)`（去掉 mode 参数，跨场景巡逻）

> **输入校验归属变更（对应 D5）**：旧 `Create` 按 mode 分支强制 active→brief / passive→host。新世界输入统一为一段 brief 文本：Create 只校验 `ScenarioID` 非空 + `Brief` 非空；`TargetHost` 可空，由 runner 从 brief 抽取后 `SetTargetHost` 回填（派生列，非用户输入的第二形态）。不再有 brief/flow 二分，无任何按场景的输入分支。

- [ ] **Step 1: 改测试（先驱动）**

`internal/task/store_test.go` 中：`NewParams{Mode: ModeActive, ...}` → `NewParams{ScenarioID: "web-pentest-killchain", ...}`；`List(ctx, ModeActive, ...)` → `List(ctx, "web-pentest-killchain", ...)`；断言 `t.ScenarioID`。新增用例：

```go
func TestCreate_requiresScenarioID(t *testing.T) {
	// Arrange
	st := newTestStore(t) // 复用既有 test helper
	// Act
	_, err := st.Create(context.Background(), NewParams{AssignmentID: mustAssignment(t), Brief: "x"})
	// Assert
	if err == nil {
		t.Fatal("want error when scenario_id empty, got nil")
	}
}

func TestListFiltersByScenario(t *testing.T) {
	st := newTestStore(t)
	aid := mustAssignment(t)
	_, _ = st.Create(context.Background(), NewParams{ScenarioID: "sA", AssignmentID: aid, Brief: "a"})
	_, _ = st.Create(context.Background(), NewParams{ScenarioID: "sB", AssignmentID: aid, Brief: "b"})
	got, err := st.List(context.Background(), "sA", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ScenarioID != "sA" {
		t.Fatalf("want 1 task scenario sA, got %+v", got)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go build ./internal/task/ 2>&1 | head`
Expected: 编译错误（`Mode` 未定义、`ScenarioID` 字段缺失）

- [ ] **Step 3: 改 model.go**

删除 `:19-24` 的 `type Mode` + 常量。`Task` 结构 `Mode Mode`（:42）→ `ScenarioID string`。`NewParams`（:58-63）删 `Mode Mode`，加 `ScenarioID string`。更新包头注释：把「mode 区分主动/被动」措辞改为「scenario_id 标识所属场景」。

- [ ] **Step 4: 改 store.go**

- `colsSelect`（:19）：`"id, mode, ..."` → `"id, scenario_id, ..."`
- `Create`（:34-49）：删 switch，改为：
```go
if p.ScenarioID == "" {
	return Task{}, fmt.Errorf("create task: scenario_id 必填")
}
if p.Brief == "" {
	return Task{}, fmt.Errorf("create task: brief 必填")
}
row := s.pool.QueryRow(ctx, `
	INSERT INTO task (scenario_id, assignment_id, brief, target_host, status)
	VALUES ($1, $2, $3, $4, 'active')
	RETURNING `+colsSelect, p.ScenarioID, p.AssignmentID, p.Brief, p.TargetHost)
```
（`target_host` 允许空串，runner 抽取后回填。）
- `List`（:58-70）：签名 `mode Mode`→`scenarioID string`；`if mode != ""`→`if scenarioID != ""`；`WHERE mode=$1`→`WHERE scenario_id=$1`。
- `scan`（:108-114）：局部 `var mode`→`var scenarioID`；`&mode`→`&scenarioID`；`t.Mode = Mode(mode)`→`t.ScenarioID = scenarioID`（顺序对齐 colsSelect 第二列）。

- [ ] **Step 5: 改 lifecycle.go ReapStale**

`ReapStale(ctx, mode, staleAfter)`（:80-90）删 mode 参数，SQL 去掉 `AND mode=$1`（reaper 跨场景巡逻）。调用点 `cmd/scanner/main.go:356` 的 `[]task.Mode{...}` reaper 循环删除，改为单次 `ReapStale(ctx, staleAfter)`（M5/M6 处理调用点）。

- [ ] **Step 6: 跑测试确认通过**

Run: `go test ./internal/task/`
Expected: PASS

- [ ] **Step 7: 提交**

```bash
git add internal/task/
git commit -m "refactor(task): mode→ScenarioID，Create 去 mode 分支，ReapStale 跨场景"
```

### Task 4.4: assignment 包 store + model 改造

**Files:**
- Modify: `internal/assignment/model.go`（删 `Mode` 类型/常量 `:16-22`；`Assignment.Mode`→`ScenarioID string` `:48`；`NewParams.Mode`→`ScenarioID` `:65`；**保留 `Source` 类型与常量 `:24-30` 不动**）
- Modify: `internal/assignment/store.go`（`colsSelect` `:20`；`Create` 校验 `:30-34`（删 mode 校验，加 ScenarioID 非空，**保留 source 校验 `:35-39`**）；INSERT `:49,52`；`List(mode)`→`List(scenarioID)` `:71-83`；`scan` `:141,145`）
- Modify: `internal/assignment/store_test.go`

**Interfaces:**
- Produces:
  - `Assignment { ID string; ScenarioID string; Source Source; Payload []byte; Title string; ScheduleID *string; CreatedAt time.Time }`
  - `NewParams { ScenarioID string; Source Source; Items []Item; Title string; ScheduleID *string }`
  - `func (s *Store) List(ctx context.Context, scenarioID string, limit int) ([]Assignment, error)`
  - `Source`/`SourceManual`/`SourceAuto` **不变**

- [ ] **Step 1: 改测试**

`NewParams{Mode: ModeActive, Source: SourceManual, ...}` → `NewParams{ScenarioID: "web-pentest-killchain", Source: SourceManual, ...}`；`List(ctx, ModeActive, ...)` → `List(ctx, "web-pentest-killchain", ...)`。保留所有 Source 相关断言不变。

- [ ] **Step 2: 跑测试确认失败**

Run: `go build ./internal/assignment/ 2>&1 | head`
Expected: 编译错误

- [ ] **Step 3: 改 model.go**

删 `:16-22` `type Mode` + 常量。`Assignment.Mode`（:48）→`ScenarioID string`。`NewParams.Mode`（:65）→`ScenarioID string`。**`Source` 段（:24-30）原样保留**，包头注释「区分主动/被动下发」改「scenario_id 标识下发场景」。

- [ ] **Step 4: 改 store.go**

- `colsSelect`（:20）：`"id, mode, source, ..."` → `"id, scenario_id, source, ..."`（source 保留）
- `Create`：删 mode 的 switch 校验，加 `if p.ScenarioID == "" { return ..., error }`；**保留 source 的 switch 校验（:35-39）**；INSERT 列 `mode, source` → `scenario_id, source`，绑值 `string(p.Mode)`→`p.ScenarioID`。
- `List`（:71-83）：`mode Mode`→`scenarioID string`；`WHERE mode=$1`→`WHERE scenario_id=$1`。
- `scan`（:139-146）：`var mode` → `var scenarioID`；`a.Mode = Mode(mode)`→`a.ScenarioID = scenarioID`；**source 扫描逻辑不变**。

- [ ] **Step 5: 跑测试确认通过**

Run: `go test ./internal/assignment/`
Expected: PASS

- [ ] **Step 6: 提交**

```bash
git add internal/assignment/
git commit -m "refactor(assignment): mode→ScenarioID，保留 Source 审计轴不动"
```

### Task 4.5: cronschedule 包改造

**Files:**
- Modify: `internal/cronschedule/model.go`（`CronSchedule.Mode assignment.Mode`→`ScenarioID string` `:17`；`NewParams.Mode`→`ScenarioID` `:29`）
- Modify: `internal/cronschedule/store.go`（`Create` 校验 `:42-45`（删 `case assignment.ModeActive,...`，加 ScenarioID 非空）；INSERT `:60,63`；`List(mode)`→`List(scenarioID)` `:82-93`；scan `:174,178`）
- Modify: `internal/cronschedule/store_test.go`

**Interfaces:**
- Produces:
  - `CronSchedule { ...; ScenarioID string; ... }`（替代 `Mode assignment.Mode`）
  - `NewParams { ScenarioID string; ... }`
  - `func (s *Store) List(ctx, scenarioID string, limit int) ([]CronSchedule, error)`

- [ ] **Step 1: 改测试**

`NewParams{Mode: assignment.ModeActive, ...}` → `NewParams{ScenarioID: "web-pentest-killchain", ...}`；`List` 同改。

- [ ] **Step 2: 跑测试确认失败**

Run: `go build ./internal/cronschedule/ 2>&1 | head`
Expected: 编译错误

- [ ] **Step 3: 改 model.go + store.go**

model：`Mode assignment.Mode`→`ScenarioID string`（两处）。若 import assignment 仅为 Mode，删除该 import。
store：`Create` 校验 `case assignment.ModeActive, assignment.ModePassive:` → `if p.ScenarioID == "" { error }`；INSERT `mode`→`scenario_id`；`List` `WHERE mode=$1`→`WHERE scenario_id=$1`；scan `c.Mode`→`c.ScenarioID`。

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/cronschedule/`
Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add internal/cronschedule/
git commit -m "refactor(cronschedule): mode→ScenarioID"
```

### Task 4.6: finding + conversation JOIN 派生改造

**Files:**
- Modify: `internal/finding/store.go`（`LedgerRow.Mode`→`ScenarioID` `:272`；`LedgerFilter.Mode`→`ScenarioID` `:280`；JOIN SELECT `t.mode`→`t.scenario_id` `:313-314`；filter `t.mode=$`→`t.scenario_id=$` `:309-310`；scan `:404`）
- Modify: `internal/conversation/model.go`（`Conversation.Mode`→`ScenarioID string` `:51-53`）
- Modify: `internal/conversation/store.go`（`ListConversations(.., mode)`→`(.., scenarioID)` `:145-161`；`COALESCE(t.mode,'')`→`COALESCE(t.scenario_id,'')` `:156`；filter `:160-161`；scan `:310-312`）
- Modify: 相关测试

**Interfaces:**
- Produces:
  - `LedgerFilter { ...; ScenarioID string }`；`LedgerRow { ...; ScenarioID string }`
  - `func (s *Store) ListConversations(ctx, ..., scenarioID string) (...)`
  - `Conversation.ScenarioID string`

- [ ] **Step 1: 改测试**

finding/conversation 测试里 `Mode` 字段与 `mode` 过滤参数改为 `ScenarioID`/`scenarioID`。

- [ ] **Step 2: 跑测试确认失败**

Run: `go build ./internal/finding/ ./internal/conversation/ 2>&1 | head`
Expected: 编译错误

- [ ] **Step 3: 改实现**

finding/store.go：`LedgerRow.Mode string`→`ScenarioID string`；`LedgerFilter.Mode`→`ScenarioID`；`JOIN task t` 的 `SELECT ... t.mode`→`t.scenario_id`；`WHERE ... t.mode=$N`→`t.scenario_id=$N`；scan `&out.Mode`→`&out.ScenarioID`。
conversation/store.go：`ListConversations` 参数 `mode string`→`scenarioID string`；`SELECT COALESCE(t.mode,'') AS mode`→`COALESCE(t.scenario_id,'') AS scenario_id`；`WHERE ($3='' OR t.mode=$3)`→`t.scenario_id=$3`；scan 读入 `c.ScenarioID`。conversation/model.go：`Conversation.Mode string`→`ScenarioID string`。

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/finding/ ./internal/conversation/`
Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add internal/finding/ internal/conversation/
git commit -m "refactor(finding,conversation): JOIN 派生 mode→scenario_id"
```

### Task 4.7: sitemap projector 去 mode 分支（跨场景统一建图）

**Files:**
- Modify: `internal/sitemap/projector.go`（`:106-107` `if t.Mode != task.ModeActive { error }` → 删除该判断，跨场景统一建图）
- Modify: `internal/sitemap/projector_test.go`

**Interfaces:**
- Consumes: `task.Task.ScenarioID`（Task 4.3，仅字段跟随改名，不参与建图判定）
- Produces: projector 不再按 mode/场景 gating，对所有 task 统一建图

> **对应 D3（第一期不引 builds_sitemap 能力位）**：旧逻辑「只给 active 建图」是 mode 二分的残留。第一期不引入 `builds_sitemap` 字段、不做谓词注入——直接删掉 gating，跨场景统一建图。流量类场景即使建了图也是空图，无害；真需要「某场景不建图」的能力位时，未来加 scenario 字段再说（YAGNI）。projector 不需要新增对 scenario 包的依赖。

- [ ] **Step 1: 改测试（删「非 active 报错」用例，加「任意场景都建图」用例）**

删除原先断言「非 active 场景 projector 报错/跳过」的测试。改为断言任意 scenario_id 的 task 都能正常投影：

```go
func TestProject_buildsForAnyScenario(t *testing.T) {
	// Arrange：两个不同 scenario_id 的 task
	p := NewProjector(pool) // 构造签名不新增参数
	tk := task.Task{ID: "t1", ScenarioID: "web-traffic-analysis"}
	// Act
	err := p.Project(context.Background(), tk /* ...含可投影的 finding/http 数据... */)
	// Assert：正常建图，不因场景而 gating
	if err != nil {
		t.Fatalf("want build, got %v", err)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/sitemap/ -run TestProject_buildsForAnyScenario -v`
Expected: FAIL（旧代码仍有 `t.Mode` 引用，编译错）

- [ ] **Step 3: 改 projector**

删除 `:106-107` 的 `if t.Mode != task.ModeActive { return ... }` 整段。projector 不再读 `t.Mode`（该字段已随 Task 4.3 改名/删除），也不新增任何 scenario 依赖。`NewProjector` 签名**不变**。

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/sitemap/`
Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add internal/sitemap/
git commit -m "refactor(sitemap): 删 mode 建图 gating，跨场景统一建图"
```

## M5：runner 装配层 — engine 数据驱动派发 + mode 词汇清除

> **本里程碑是架构核心落点（对应 D10）**：把 `switch input.Mode {case "passive"/"active"}`（`cmd/scanner/handler.go:186-194`）替换为「载入 task 的 scenario → 按 `scenario.engine` 派发（engine 在 scenario 上，与 playbook 正交，见 D1/D2）」。装配规则（对应 D2）：
> - **swarm**：主代理取 `hunter` 配置表中 `kind='orchestrator'` 且 enabled 的**全局唯一**那条（不从 playbook 取）；子代理 = scenario 引用的 playbook 内各 `kind='domain'` 猎手。orchestrator 经 deep `task` 工具按各子代理的 name+description 动态派活。
> - **solo**：不用 orchestrator；把 playbook 内各 domain 猎手的 `body` 按 `playbook_hunter.position` 拼成单个 ChatModelAgent 的 system 指令，`tools` 取各猎手工具集的并集（去重）。
>
> 同时清除 payload 里的 `"mode"` 字符串、`TrafficAnalysisToolParams.Mode` 字段、两个 eino handler 的 active/passive 命名。

### Task 5.1: worker payload 去 mode，改载 ScenarioID 驱动

**Files:**
- Modify: `cmd/scanner/handler.go`（`input` 结构 `:166-169` 删 `Mode string`；`:174-179` 按 mode 选 timeout 的逻辑改为按 engine/scenario；`switch input.Mode` `:186-194` → engine 派发）
- Modify: `cmd/api/main.go`（`expandActiveItem` payload marshal `:343-346` 删 `"mode":"active"`）
- Modify: `cmd/api/scheduler.go`（passive 侧 payload marshal，删 `"mode":"passive"`）

**Interfaces:**
- Consumes（配置一律经 `h.cfgStore *configstore.Store` 按需读，不缓存全量切片；类型来自 M1 Task 1.2，别名 `cfgscenario`/`cfgplaybook`/`cfghunter`）：
  - `cfgStore.ScenarioByCode(ctx, p.ScenarioID) (cfgscenario.Scenario, error)`——派发键 `task.scenario_id` 存的是 scenario **code**（见 D3），走 code 路读（configstore scenario 双路缓存，见 Task 1.4）
  - `cfgscenario.Scenario.Engine string`（"solo"|"swarm"，**engine 在 scenario 上，见 D1/D2**）
  - `cfgscenario.Scenario.PlaybookID string` → `cfgStore.PlaybookByID(ctx, scen.PlaybookID)`（按 id 读 playbook）
  - `cfgStore.PlaybookHunters(ctx, pb.ID) ([]cfghunter.Hunter, error)`——有序 domain 猎手（按 playbook id）
  - `cfgStore.Orchestrator(ctx) (cfghunter.Hunter, error)`——swarm 全局唯一编排猎手（按 `kind='orchestrator'`，非按 code）
  - `worker.Payload.ScenarioID`（已存在 `internal/worker/handler.go:26`）
- Produces:
  - handler 派发按 `scen.Engine` 分发，不读 payload 的 `mode`；payload 直接带 `brief` 文本

> **configstore 读键说明**：派发入口拿到的是 `task.scenario_id`，存的是 scenario **code**（见 D3）。故派发链首跳走 code 路（`ScenarioByCode`），拿到 scenario 后转 uuid FK 内部引用：`ScenarioByCode(code)`→`PlaybookByID(scen.PlaybookID)`→`PlaybookHunters(pb.ID)`。scenario 是唯一 by-code 读的资源（因外部 task 表按 code 引用它）；playbook/hunter 全走 uuid FK，无 by-code 读。

> **payload 形态（对应 D5）**：旧 payload 双字段 `{mode, entrypoint}`。新 payload 只留 `{brief}`——输入统一为一段 brief 文本（附件为未来扩展点，本期不做）。`target_host` 不进 payload：它是 runner 从 brief 抽取后回填 task 的派生列，不是用户输入的第二形态。不再需要 mode，也不需要 entrypoint 抽象。

- [ ] **Step 1: 改 handler.go 的 input 结构与派发**

`input` 结构删 `Mode string` 与 `Entrypoint`，仅留 `Brief string`。派发段重写为（**engine 取自 scenario，不取自 playbook**）：

```go
scen, err := h.cfgStore.ScenarioByCode(ctx, p.ScenarioID) // scenario_id 存的是 code（见 D3）
if err != nil {
	return h.failTask(ctx, p.HunterID, fmt.Errorf("加载 scenario %s 失败: %w", p.ScenarioID, err))
}
pb, err := h.cfgStore.PlaybookByID(ctx, scen.PlaybookID)
if err != nil {
	return h.failTask(ctx, p.HunterID, fmt.Errorf("scenario %s 引用的 playbook %s 加载失败: %w", scen.Code, scen.PlaybookID, err))
}
hunters, err := h.cfgStore.PlaybookHunters(ctx, pb.ID) // 有序 domain 猎手（按 playbook id）
if err != nil {
	return h.failTask(ctx, p.HunterID, fmt.Errorf("playbook %s 组合猎手加载失败: %w", pb.Code, err))
}

// timeout 按 engine 取（swarm 用长超时，solo 用常规）——语义等价旧的 active/passive 分支。
// 本里程碑配置字段仍是旧名（M6 才 rename）：AgentRunTimeoutSeconds→Solo、ActiveAgentRunTimeoutSeconds→Swarm。
timeout := h.scannerCfg.AgentRunTimeoutSeconds
if scen.Engine == cfgscenario.EngineSwarm {
	timeout = h.scannerCfg.ActiveAgentRunTimeoutSeconds
}
if timeout > 0 {
	var cancel context.CancelFunc
	ctx, cancel = context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
	defer cancel()
}

switch scen.Engine {
case cfgscenario.EngineSolo:
	return h.handleSoloEino(ctx, p, scen, pb, hunters, input.Brief)
case cfgscenario.EngineSwarm:
	return h.handleSwarmEino(ctx, p, scen, pb, hunters, input.Brief)
default:
	return h.failTask(ctx, p.HunterID, fmt.Errorf("unknown engine: %s", scen.Engine))
}
```

> handler 不缓存全量配置切片，而是持有 `h.cfgStore *configstore.Store`（Task 5.4 注入）。装配时按需读（全 id 路）：`cfgStore.PlaybookByID(ctx, scen.PlaybookID)` 取 playbook、`cfgStore.PlaybookHunters(ctx, pb.ID)` 取有序 domain 猎手、`cfgStore.Orchestrator(ctx)` 取全局唯一编排猎手（按 `kind='orchestrator'`，非按 code）。配置字段 `SwarmAgentRunTimeoutSeconds` 在 M6 定名（去 active 残留）。别名约定见 M1 Task 1.2：`cfgscenario`/`cfgplaybook`/`cfghunter`（避免与 einoagent 的 `hunter` 包撞）。

- [ ] **Step 2: 改 payload marshal（去 mode 字符串，直发 brief）**

`cmd/api/main.go` expandActiveItem `:343-346`：
```go
payloadInput, err := json.Marshal(map[string]any{
	"brief": brief, // 一段目标描述文本；host 由 runner 抽取回填，不进 payload
})
```
`cmd/api/scheduler.go` 定时侧同理删 `"mode":"passive"`，只留 `"brief"`。

- [ ] **Step 3: build**

Run: `go build ./cmd/scanner/ ./cmd/api/ 2>&1 | head -30`
Expected: 因 handleSolo/SwarmEino 未定义、`h.cfgStore` 字段未定义而报错（Task 5.2 补 handler、Task 5.4 补 cfgStore 字段与装配）

### Task 5.2: 两个 eino handler 改名 active/passive→swarm/solo，去 Mode 参数

**Files:**
- Rename+Modify: `cmd/scanner/handler_active_eino.go` → `handler_swarm_eino.go`（`handleActiveEino`→`handleSwarmEino`；签名加 `scen cfgscenario.Scenario, pb cfgplaybook.Playbook, hunters []cfghunter.Hunter`）
- Rename+Modify: `cmd/scanner/handler_passive_eino.go` → `handler_solo_eino.go`（`handlePassiveEino`→`handleSoloEino`）
- Modify: `cmd/scanner/handler_eino_common.go`（`TrafficAnalysisToolParams{Mode:"active"}` 处理见下）

**Interfaces:**
- Consumes（类型来自 M1 Task 1.2，import 用别名 `cfgscenario`/`cfgplaybook`/`cfghunter`）：`cfgscenario.Scenario`（含 Code/Name/Description/Instruction/Domain/Engine/PlaybookID）、`cfgplaybook.Playbook`（含 Code/Name/Description）、`cfghunter.Hunter`（配置猎手：Code/Kind/Name/Description/Body/Tools/MaxIterations）。playbook 的组合猎手不在 Playbook 结构内联，而由 `cfgStore.PlaybookHunters(ctx, pb.ID)` 返回**按 position 有序**的 `[]cfghunter.Hunter`（仅 domain 猎手）。
- Produces（`hunters` 为该 playbook 的有序 domain 猎手，装配前由 Task 5.1 经 `cfgStore.PlaybookHunters` 取好传入）：
  - `func (h handler) handleSwarmEino(ctx, p worker.Payload, scen cfgscenario.Scenario, pb cfgplaybook.Playbook, hunters []cfghunter.Hunter, brief string) error`
  - `func (h handler) handleSoloEino(ctx, p worker.Payload, scen cfgscenario.Scenario, pb cfgplaybook.Playbook, hunters []cfghunter.Hunter, brief string) error`

- [ ] **Step 1: git mv 两文件**

```bash
git mv cmd/scanner/handler_active_eino.go cmd/scanner/handler_swarm_eino.go
git mv cmd/scanner/handler_passive_eino.go cmd/scanner/handler_solo_eino.go
```

- [ ] **Step 2: 改 handler_swarm_eino.go（B1：orchestrator 从 hunter 表取，playbook 只供 domain 子代理）**

- 函数名 `handleActiveEino`→`handleSwarmEino`，签名加 `scen cfgscenario.Scenario, pb cfgplaybook.Playbook, hunters []cfghunter.Hunter`，入参 `flowText`/`entrypoint`→`brief`。
- **主代理装配**：orchestrator 由 Task 5.1 经 `cfgStore.Orchestrator(ctx)` 取全局唯一那条（按 `kind='orchestrator'` 且 enabled，非按 code，不从 playbook 取）传入或就地取；缺失即 `failTask`（swarm 必须有 orchestrator）。
- **子代理装配**：`hunters` 参数已是该 playbook 按 position 有序的 domain 猎手（`cfgStore.PlaybookHunters` 返回，仅 domain）；逐个做子代理，name+description 注入 deep `task` 工具供 orchestrator 动态派活（不硬编码猎手名）。
- 场景侧重注入：`scen.Instruction` 非空时并入 orchestrator 的 system 指令（场景领域侧重）。删掉旧的基于文件态 `h.scenarioRoles` 的场景查找（scen 已由 Task 5.1 从 cfgStore 取好并传入）。
- `:99` `TrafficAnalysisToolParams{... Mode: "active" ...}` → 见 Task 5.3（Mode 字段本身要删）。
- `:128` `BuildUserPrompt` 的 `Mode: "active"` → 见 Task 5.3；userText 传 `brief`。
- `:169` out marshal 的 `"engine":"eino-deep"` → `"engine": string(scen.Engine)`（数据驱动，engine 取自 scenario）。
- 顶部 doc 注释「active 扫描的唯一入口」→「swarm 引擎入口（orchestrator + playbook domain 猎手动态派活）」。
- `composeOrchestratorInstruction`/`composeSubAgentInstruction` 的 `RoleDef` → `HunterDef`（M2 已改类型，此处跟随）；由 `cfghunter.Hunter` 映射成 einoagent 的 `HunterDef`：`ID→ID`、`Name→Name`、`Description→Description`、`Body→SystemPrompt`、`Tools→Tools`、`MaxIterations→MaxIterations`；`Kind` 需**翻译**——配置表 `kind='domain'` → `HunterSubAgent`、`kind='orchestrator'` → `HunterOrchestrator`（两侧词汇不同：配置层用 orchestrator/domain，einoagent 用 orchestrator/subagent/solo，见 D1 与 M2）。`cfghunter.Hunter.Code` 不进 `HunterDef`（HunterDef 无 Code 字段）；`SourceFile` 在 DB 事实源下留空。抽出一个 `hunterDefFromConfig(cfghunter.Hunter) einoagent.HunterDef` 映射函数集中处理，避免散落。

- [ ] **Step 3: 改 handler_solo_eino.go（B2：拼 body、tools 取并集）**

- 函数名 `handlePassiveEino`→`handleSoloEino`，签名加 scen/pb，入参改 `brief`。
- **单代理装配**：`hunters` 参数已是按 position 有序的 domain 猎手；`body` 按序拼成一份 system 指令（`scen.Instruction` 作为领域侧重置于其前），`tools` 取各猎手 `Tools` 的并集（去重）。不注入 orchestrator。
- 调 `einoagent.RunSolo(ctx, scen.Code, scen.Description, model, tools, instruction, brief, maxIters, ...)`（M3 已泛化）。maxIters 取所选猎手 MaxIterations 的最大值或场景级配置。
- 内部 `Mode:"passive"` 相关全部按 Task 5.3 处理。
- watchAbort/finalize 等逻辑不变。
- doc 注释 passive→solo（单代理，playbook 猎手压扁）措辞更新。

- [ ] **Step 4: build（预期仍缺 Task 5.3/5.4）**

Run: `go build ./cmd/scanner/ 2>&1 | head -30`
Expected: 因 `TrafficAnalysisToolParams.Mode`、`h.cfgStore`、`BuilderParams.Mode` 未处理而报错

### Task 5.3: 清除工具装配层的 Mode 字段

**Files:**
- Modify: `internal/einoagent/`（`TrafficAnalysisToolParams.Mode` 字段定义 + 所有读取处）
- Modify: `internal/skill/`（`BuilderParams.Mode` 字段 + 读取处）
- Modify: `internal/builder/hunter/`（BuildUserPrompt 里对 Mode 的使用）

**Interfaces:**
- Produces: `TrafficAnalysisToolParams` 与 `BuilderParams` 去掉 `Mode` 字段

> **调查前置**：`TrafficAnalysisToolParams.Mode` 与 `BuilderParams.Mode` 当前被谁读？先 grep 确认语义再删。若某工具用 Mode 区分「主动扫描 vs 被动分析」的提示词/行为，该分支应改由 hunter 的 `body`/`tools`（装配层已按引擎决定给哪些猎手/工具）承载，而非直接删后丢失语义——因为「该做什么」已内化进所选 domain 猎手的方法论正文，不需要 Mode 旗标。

- [ ] **Step 1: 调查 Mode 读取点**

Run: `grep -rn "\.Mode\b\|Mode:" internal/einoagent/ internal/skill/ internal/builder/ | grep -iv "scenario\|comment"`
分析每个读取点：是纯透传（可删）还是有行为分支（改 scenario 驱动）。

- [ ] **Step 2: 按调查结果改字段与读取点**

删 `TrafficAnalysisToolParams.Mode` 与 `BuilderParams.Mode`。handler 调用处（swarm/solo eino）相应删 `Mode:"active"`/`Mode:"passive"` 实参。有行为分支的改 scenario 能力位。

- [ ] **Step 3: build**

Run: `go build ./internal/einoagent/ ./internal/skill/ ./internal/builder/... 2>&1 | head`
Expected: PASS（或仅剩 handler 侧未改的调用点，随 Task 5.2 收敛）

### Task 5.4: main.go 装配注入 configstore + reaper 去 mode 循环

**Files:**
- Modify: `cmd/scanner/main.go`（删三块文件态加载 `:221-230`（active `roles`）/`:233-247`（`passiveRole`）/`:250-261`（`scenarioRoles`）及其日志 id 拼接；装配期构造 `cfgStore` 并注入；reaper 循环 `:356` 去 mode）
- Modify: `cmd/scanner/handler.go`（`handler` 结构体删三个文件态字段、加 `cfgStore` 只读句柄）

**Interfaces:**
- Consumes: `configstore.Store`（M1 Task 1.4，读 scenario/playbook/hunter，DB 事实源 + 内存/redis 缓存）
- Produces: handler 通过 configstore 按需读 scenario/playbook/hunter，供 Task 5.1/5.2 派发

> **DB 事实源（对应 D6/D7）**：handler **不从文件 `Load(dir)`**，而是持有 configstore 只读句柄，运行时 `ScenarioByCode(scenario_id)` 取 scenario（`scenario_id` 存 code，见 D3）、`PlaybookByID(scen.PlaybookID)` 取 playbook、`PlaybookHunters(pb.ID)` 取有序 domain 猎手（swarm 另按 `kind='orchestrator'` 取全局编排者）。文件仅是首次导入的种子（M1 Task 1.3），进程运行期一律走 DB/缓存。

- [ ] **Step 1: handler 结构改字段**

`cmd/scanner/handler.go` 的 `handler` struct 加 `cfgStore *configstore.Store`；**删除三个文件态字段**——`roles []einoagent.RoleDef`（active deep，已被 swarm handler 的 `cfgStore.Orchestrator`+`PlaybookHunters` 取代）、`passiveRole einoagent.RoleDef`（已被 solo handler 的 `hunters` 参数 + `RunSolo` 取代）、`scenarioRoles []scenario.Role`（scen 由 Task 5.1 从 cfgStore 取好传入）。相应删掉指向 `internal/scenario` 文件态包的 import（该包在 M5 Task 5.5 Step 4b 整包删）。

- [ ] **Step 2: main.go 删文件加载、构造 cfgStore、改注入**

- 删三块启动加载及其日志：`:221-230`（`einoagent.LoadRoles(activeDir)` + roleIDs 拼接）、`:233-247`（passive `LoadRoles` + `passiveRole` 选取 + fail-fast）、`:250-261`（`scenario.LoadRoles` + `string(r.Mode)+":"+r.ID` 日志拼接）。这些文件态加载在 DB 事实源下全部废弃。
- 装配 handler 处构造 `cfgStore := configstore.New(pool, redisClient)`（M1 Task 1.4）；`handler{...}` 字面量删 `roles`/`passiveRole`/`scenarioRoles` 三个字段赋值，加 `cfgStore: cfgStore`。configstore 内部完成内存 L1 + redis 失效订阅；DB 连接失败即 fatal（配置源不可用不该静默）。

- [ ] **Step 3: reaper 循环去 mode**

`:356` `for _, mode := range []task.Mode{task.ModeActive, task.ModePassive} { ReapStale(ctx, mode, ...) }` → 单次 `n, err := ts.ReapStale(ctx, staleAfter)`（Task 4.3 已改签名，跨场景巡逻）。

- [ ] **Step 4: 全量 build**

Run: `go build ./... 2>&1 | head -30`
Expected: 逐步收敛；剩余错误应仅在 cmd/api（Task 5.5）与 scanner→runner 改名（M6）范围

> **注**：本 Task 文件路径仍写 `cmd/scanner`，M6 才整体改名 `cmd/runner`；此处按当前目录名描述，M6 统一 git mv。

### Task 5.5: cmd/api 侧 mode 触点清理

**Files:**
- Modify: `cmd/api/main.go`（删 `scenario.LoadRoles` 加载块 `:100-106`（含 `string(r.Mode)+":"+r.ID` 日志）；`activeScanAdapter.roles []scenario.Role` 字段 `:280` + 字面量 `roles: scenarioRoles` `:115` 删除；`ListRoles()` 方法 `:292-293` 删除；`defaultActiveRoleID`+`DefaultForMode` `:295-301` 删除；`:597` `roleID = a.defaultActiveRoleID()` 兜底删除；`:244,530` `t.Mode == task.ModePassive` scope 分支；`:254` `Mode: string(t.Mode)` TaskSummary；`:316,339` assignment/task Create 的 `Mode:` 实参）
- Modify: `cmd/api/scheduler.go`（`:84,96-102` sched.Mode 派发；`:123` task Create `Mode: task.ModePassive`）
- Modify: `internal/httpapi/conversation_handler.go`（**整块删除老 `GET /roles` 链**：`RolesAPI` 接口 `:46-49`、`roleDTO`（含 `Mode` 字段）`:51-57`、`rolesHandler` `:59-70`——由 M1 Task 1.5 的 `GET /api/scenarios` 取代）
- Modify: `internal/httpapi/server.go`（`Deps.Roles RolesAPI` 字段 `:45` 删除；`GET /roles` 注册 `:102-104` 删除）
- Modify: `internal/httpapi/`（`TaskSummary.Mode` 字段 → `ScenarioID`；相关 request/response DTO）

**Interfaces:**
- Consumes: `task.NewParams.ScenarioID`、`assignment.NewParams.ScenarioID`（M4）、`scenario`（M1）
- Produces:
  - `httpapi.TaskSummary { ...; ScenarioID string }`（替代 `Mode string`）
  - createScan/expandActiveItem/scheduler 用 `ScenarioID` 建 task/assignment

> **`scenario.DefaultForMode` 处理（`:297`）**：配置事实源已由 M1 新建的 `internal/config/scenario`（cfgscenario，DB store）承担，老 `internal/scenario` 的 `Role/Mode/DefaultForMode/ModeActive` 一律作废（老包在本 Task Step 4b 整包删）。此处「用户未选 role 时兜底默认 active 场景」的语义改为：前端现在必选 scenario（D12/M8），API 校验 `scenario_id` 非空即可，不再需要 mode 兜底默认。删除 `defaultActiveRoleID` 与 `DefaultForMode` 依赖（不保留兼容兜底）。

- [ ] **Step 1: 改 httpapi DTO**

`TaskSummary.Mode string` → `ScenarioID string`。grep `httpapi` 内所有 `Mode` 字段（request body、response）逐个改 `ScenarioID` 或删除（若前端不再需要）。

- [ ] **Step 2: 改 scheduler.go**

`:96-102` `switch sched.Mode { case ModeActive: ...; case ModePassive: ... }` 展开逻辑 → 按 `sched.ScenarioID` 建 assignment/task（不再分 active/passive 两套展开）。`expandActiveItem`/passive 展开若因 engine 不同而装配不同，改由「建 task 时带 scenarioID，具体 engine 在 scanner handler 侧解析」——scheduler 只管建 task，不关心 engine（职责下沉到 runner，符合数据驱动）。`:123` task Create `Mode: task.ModePassive` → `ScenarioID: sched.ScenarioID`。

- [ ] **Step 3: 改 main.go createScan/List/TaskSummary**

- createScan/expandActiveItem：`assignment.NewParams{Mode: ModeActive,...}` → `{ScenarioID: scenarioID, Source: SourceManual, ...}`；`task.NewParams{Mode: ModeActive,...}` → `{ScenarioID: scenarioID,...}`。scenarioID 从请求参数取（前端传入）。
- List（`:244-248`）：删 `if t.Mode == task.ModePassive` 的 scope 分支。输入已统一（brief 主 + host 派生，见 D5），TaskSummary 始终输出 `brief` + `target_host` 两字段，不再按场景分形状。
- `:254` `Mode: string(t.Mode)` → `ScenarioID: t.ScenarioID`。
- 删 `defaultActiveRoleID` + `DefaultForMode` 调用，及 `:597` 用其兜底的 `roleID` 赋值。

- [ ] **Step 3b: 删除老 `GET /roles` 链（由 `GET /api/scenarios` 取代）**

老世界前端会话选择场景走 `GET /roles`（`httpapi.RolesAPI`→`activeScanAdapter.ListRoles`→文件态 `scenario.LoadRoles`）。新世界前端 ScenarioPicker 改走 M1 Task 1.5 的 `GET /api/scenarios`（读 configstore），故整条老链删除（不保留兼容）：
- `internal/httpapi/conversation_handler.go`：删 `RolesAPI` 接口、`roleDTO`（含 `Mode` 字段）、`rolesHandler`。
- `internal/httpapi/server.go`：删 `Deps.Roles` 字段与 `GET /roles` 注册。
- `cmd/api/main.go`：删 `scenario.LoadRoles` 加载块、`activeScanAdapter.roles` 字段与 `roles: scenarioRoles` 字面量、`ListRoles()` 方法；`Deps{...}` 字面量去掉 `Roles:` 赋值。
- 若删后 `cmd/api`/`httpapi` 不再 import `internal/scenario`，一并删除该 import（`internal/scenario` 整包在本 Task Step 4b 删）。

- [ ] **Step 4: 改测试**

`cmd/api/scheduler_integration_test.go` 全量 `Mode: assignment.ModeActive/ModePassive` → `ScenarioID: "<scenario-id>"`；`List(ctx, task.ModeActive/Passive, ...)` → `List(ctx, "<scenario-id>", ...)`。`cmd/e2e/runner.go:149` 同改。

- [ ] **Step 4b: 删除死掉的 `internal/scenario` 文件态包**

Task 5.4 清完 `cmd/scanner`、本 Task 清完 `cmd/api`+`httpapi` 后，`internal/scenario`（`role.go`/`role_test.go`：文件态 `Role`/`LoadRoles`/`Mode`/`DefaultForMode`）再无 importer——配置事实源已整体迁到 DB（`internal/config/*` + configstore）。先 grep 确认零 importer 再整包删：

```bash
grep -rln '"github.com/V3teran/liusha/internal/scenario"' --include="*.go" .   # 期望：空
git rm -r internal/scenario
```
（`internal/config/scenario` 是新配置包，别名 `cfgscenario`，与被删的 `internal/scenario` 不同物，勿混。）

- [ ] **Step 5: 全量 build + test**

Run: `go build ./... 2>&1 | head -20 && go test ./cmd/... ./internal/... 2>&1 | tail -30`
Expected: 剩余错误应仅属 M6（scanner→runner 改名）范围

- [ ] **Step 6: 提交**

```bash
git add cmd/ internal/einoagent/ internal/skill/ internal/builder/ internal/httpapi/
git commit -m "refactor(runner): engine 数据驱动派发替代 switch mode，清除 active/passive 词汇"
```

## M6：scanner→runner 全量改名（对应 D9）

> **命名彻底、无残留**（用户明令）。旧名 scanner 已名不副实——它是通用 hunter 执行进程，不止「扫描」。改名 runner 覆盖：目录、Go 标识符、配置键、审计常量、基础设施文件、脚本、CI。
>
> **假阳性排除**：`.go` 里大量 `scanner`/`Scanner` 是 pgx 的 `type scanner interface { Scan(...) }` 抽象（`internal/task/store.go`、`internal/finding/store.go` 等）与 `rows.Scan`——这些是数据库行扫描语义，**不在改名范围**。只改指代「cmd/scanner 进程」的标识符。

### Task 6.1: 目录与包改名 cmd/scanner→cmd/runner

**Files:**
- Rename: `cmd/scanner/` → `cmd/runner/`（含 handler.go/main.go/两个 eino handler/Dockerfile/ingest_handler.go/distill.go/event_sink.go/conversation_context.go 等全部）

- [ ] **Step 1: git mv 目录**

```bash
git mv cmd/scanner cmd/runner
```

- [ ] **Step 2: 包声明确认**

包名本就是 `package main`，无需改。但文件内注释「cmd/scanner」字样在后续 step 统一 sed 前先人工过一遍语义（见 Step 3）。

- [ ] **Step 3: build 确认目录移动无破坏**

Run: `go build ./cmd/runner/ 2>&1 | head`
Expected: 与 M5 结束时相同的待补错误（不因目录改名新增错误）

### Task 6.2: 配置结构 ScannerConfig→RunnerConfig

**Files:**
- Modify: `internal/config/config.go`（`ScannerConfig` `:210`→`RunnerConfig`；`Config.Scanner` 字段 `:32`→`Runner`；`mapstructure:"scanner"`→`"runner"`；`applyScannerDefaults` `:586`→`applyRunnerDefaults`；**两个超时按引擎键改名（对齐 D10）**：`AgentRunTimeoutSeconds` `:211`→`SoloAgentRunTimeoutSeconds`、`ActiveAgentRunTimeoutSeconds` `:212`→`SwarmAgentRunTimeoutSeconds`（去 active/passive 词汇）；新增 `PlaybookDir`/`ScenarioDir`/`HunterDir` 字段，见 M1）
- Modify: `config/config.yaml`（`:364` `scanner:` 块 → `runner:`；`:365` `agent_run_timeout_seconds` → `solo_agent_run_timeout_seconds`；`:366` `active_agent_run_timeout_seconds` → `swarm_agent_run_timeout_seconds`；`:335,337,340,363` 注释 `cmd/scanner`→`cmd/runner`；`:14` 注释 proxy/scanner→proxy/runner；`:412` 注释里 `agent_run_timeout_seconds` 引用同步改名）
- Modify: `cmd/runner/handler.go`（`scannerCfg config.ScannerConfig` `:46`→`runnerCfg config.RunnerConfig`；`:112` 注释；派发块两处超时字段——M5 Task 5.1 引入的 `h.scannerCfg.AgentRunTimeoutSeconds`→`h.runnerCfg.SoloAgentRunTimeoutSeconds`（solo 分支）与 `h.scannerCfg.ActiveAgentRunTimeoutSeconds`→`h.runnerCfg.SwarmAgentRunTimeoutSeconds`（swarm 分支）**全部改名**；同步删掉 M5 里「配置字段仍是旧名，M6 才 rename」那条过渡注释）
- Modify: `cmd/runner/main.go`（`scannerCfg` 局部变量 → `runnerCfg`；`cfg.Scanner`→`cfg.Runner`；`:266` `hostSemTTL := time.Duration(scannerCfg.ActiveAgentRunTimeoutSeconds)...`→`runnerCfg.SwarmAgentRunTimeoutSeconds`（per-host 信号量 TTL 取最长任务时长，即 swarm 超时，语义不变仅改名））
- Modify: `cmd/api/main.go`（`:115,286` `cfg.Scanner.ActiveAgentRunTimeoutSeconds`→`cfg.Runner.SwarmAgentRunTimeoutSeconds`）

**Interfaces:**
- Produces:
  - `type RunnerConfig struct { SoloAgentRunTimeoutSeconds int; SwarmAgentRunTimeoutSeconds int; PlaybookDir string; ScenarioDir string; HunterDir string; ... }`
  - `Config.Runner RunnerConfig` (`mapstructure:"runner"`)

- [ ] **Step 1: 改 config.go**

结构体 + 字段 + defaults 函数 + mapstructure tag 全改。`SoloAgentRunTimeoutSeconds` 默认值 3600 保留（注释去 passive，改「solo 引擎 agent_run 整体超时」）；`SwarmAgentRunTimeoutSeconds` 默认值 14400 保留（注释去 active，改「swarm 引擎 agent_run 整体超时」）。

- [ ] **Step 2: 改 config.yaml**

`runner:` 块 + `swarm_agent_run_timeout_seconds` + PlaybookDir/ScenarioDir/HunterDir 默认路径项 + 所有 `cmd/scanner` 注释。

- [ ] **Step 3: 改引用点**

handler.go/main.go（cmd/runner）+ cmd/api/main.go 的 `cfg.Scanner`/`scannerCfg`/`AgentRunTimeoutSeconds`（→Solo）/`ActiveAgentRunTimeoutSeconds`（→Swarm）全改。

- [ ] **Step 4: build**

Run: `go build ./... 2>&1 | head`
Expected: 剩余仅 audit/logx service-name 与基础设施（Step 6.3/6.4）

### Task 6.3: 审计常量与 service name

**Files:**
- Modify: `internal/audit/model.go`（`:37` `ActorScanner = "scanner"` → `ActorRunner = "runner"`；改所有引用 `ActorScanner` 的处）
- Modify: `internal/logx/logger.go`（`:140` 注释 `./bin/scanner → "scanner"`→`./bin/runner → "runner"`；logx 按二进制名自动取 service，改名后自动变 "runner"，无代码改动，仅注释）

**Interfaces:**
- Produces: `audit.ActorRunner = "runner"`

- [ ] **Step 1: grep ActorScanner 全引用**

Run: `grep -rn "ActorScanner" --include="*.go" .`
（当前仅定义处 1 处，但改名后需确认无调用点遗漏——runner 写审计事件处若用字面量 "scanner" 也要改）

- [ ] **Step 2: 改常量 + 引用**

`ActorScanner`→`ActorRunner`，值 `"scanner"`→`"runner"`。grep 字面量 `"scanner"` 作为 actor 传入的隐式点。

- [ ] **Step 3: build + 相关测试**

Run: `go build ./... && go test ./internal/audit/ ./internal/logx/ 2>&1 | tail`
Expected: PASS

### Task 6.4: 基础设施 — Makefile / scripts / docker-compose / CI / Dockerfile

**Files:**
- Modify: `Makefile`（`:29` `go run ./cmd/scanner`→`./cmd/runner`；`:55` `docker build -f cmd/scanner/Dockerfile -t liusha/scanner .`→`cmd/runner/Dockerfile -t liusha/runner`）
- Modify: `scripts/dev/run-svc.sh`（`:65` 注释；`:114` `go run -mod=mod ./cmd/scanner 2>logs/scanner.stderr`→`./cmd/runner 2>logs/runner.stderr`；`:144,152` `logs/scanner.log`→`logs/runner.log`）
- Modify: `scripts/dev/e2e.sh`（`:110` `pkill -9 -f 'exe/scanner'`→`'exe/runner'`；`:111` `'go run.*cmd/scanner'`→`'cmd/runner'`；`:185` 注释 `logs/scanner.log`）
- Modify: `scripts/dev/up.sh`（`:3` 注释 `vulnapp/api/scanner`→`vulnapp/api/runner`）
- Modify: `deployments/docker-compose.yml`（`:2,3,6,63,64,152,201` 注释；`:154` service `scanner:`→`runner:`；`:155` `image: liusha/scanner`→`liusha/runner`；`:158` `dockerfile: cmd/scanner/Dockerfile`→`cmd/runner/Dockerfile`；`:159` `container_name: liusha-scanner`→`liusha-runner`）
- Modify: `deployments/docker-compose.prod.yml`（`:29` service `scanner:`→`runner:` + image/dockerfile/container_name 同 yml）
- Modify: `.github/workflows/ci.yml`（`:40` step 名 `build scanner image`→`build runner image`；`:41` `docker build -f cmd/scanner/Dockerfile -t liusha/scanner:ci`→`cmd/runner/Dockerfile -t liusha/runner:ci`）
- Verify: `deployments/tool-images/pentools/Dockerfile`（grep 命中确认是否真指进程 scanner，还是安全工具名——若是 pentools 里的扫描工具名，**不改**）

**Interfaces:**
- Produces: 服务名/镜像名/容器名/日志文件/构建命令统一 runner

- [ ] **Step 1: Makefile**

改 `:29`、`:55`。grep Makefile 全文 `scanner` 兜底。

- [ ] **Step 2: scripts 三个**

run-svc.sh / e2e.sh / up.sh 的进程名、日志文件、pkill 匹配串、注释全改。

- [ ] **Step 3: docker-compose × 2**

service key、image、dockerfile 路径、container_name、注释全改。注意 depends_on / 其他 service 若引用 `scanner` service 名也要改（grep 确认 api/nginx 段是否 depends_on scanner）。

- [ ] **Step 4: CI**

ci.yml step 名 + build 命令。

- [ ] **Step 5: 核实 pentools Dockerfile**

Run: `grep -n scanner deployments/tool-images/pentools/Dockerfile`
判断：若是安全扫描工具（如 nikto/nuclei 类）的名字，**保留不改**；若指本项目进程，改 runner。

- [ ] **Step 6: 全量验证**

```bash
go build ./... 2>&1 | head
grep -rn "cmd/scanner\|liusha/scanner\|liusha-scanner\|ScannerConfig\|scannerCfg\|ActorScanner\|cfg\.Scanner\b\|ActiveAgentRunTimeoutSeconds" --include="*.go" --include="*.yml" --include="*.yaml" --include="*.sh" --include="Makefile" . | grep -v "cmd/runner"
```
Expected: 第一条 build PASS；第二条 grep **零输出**（无 scanner 进程残留；pgx `Scan`/工具名等假阳性不在此模式内）

- [ ] **Step 7: 提交**

```bash
git add -A
git commit -m "refactor: cmd/scanner→cmd/runner 全量改名（目录/配置/审计/基础设施/CI 无残留）"
```

## M7：tools.yaml 交战域过滤接线（对应 D11）

> **现状**：`manifest.Tool.Scenarios []string`（`internal/tools/manifest/manifest.go:28`）字段已落地被解析，但**过滤逻辑未接**（注释明写「单场景=no-op」）。`buildToolingCatalog(m)`（`internal/builder/hunter/user_prompt.go:172`）当前渲染工具全集，不看当次场景。
>
> **本里程碑**：把场景可见工具集接进 catalog 渲染 —— hunter 的工具索引段只列「本场景圈定 + 该 hunter 携带」的工具（对应四层模型「场景圈可见 + hunter 挑用」）。

### Task 7.1: manifest 增按交战域过滤方法

**Files:**
- Modify: `internal/tools/manifest/manifest.go`（新增 `FilterByDomain(domain string) *Manifest`；更新 `Tool.Scenarios` 注释去掉「过滤逻辑未接」措辞，改述为「交战域可见性标签」）
- Modify: `internal/tools/manifest/manifest_test.go`

**Interfaces:**
- Produces:
  - `func (m *Manifest) FilterByDomain(domain string) *Manifest`（返回仅含 `Scenarios` 包含 domain（或 Scenarios 为空=通用工具，所有域可见）的工具子集的新 Manifest；不可变，不改原 m）

> **可见性语义**：工具的 `scenarios: [web, ctf]` 是**交战域**标签，表示该工具在 web 与 ctf 域可见（字段名沿用 yaml 既有 `scenarios`，语义即 domain；不为省一次改名而动 yaml schema）。空 `scenarios` 视为通用工具（所有域可见，如 curl/jq 这类 utility）。入参是 `scenario.domain`（如 `web`），非 scenario code。

- [ ] **Step 1: 写失败测试**

```go
func TestFilterByDomain(t *testing.T) {
	// Arrange
	m := &Manifest{Tools: []Tool{
		{Name: "sqlmap", Category: "injection", Scenarios: []string{"web"}},
		{Name: "pwntools", Category: "runtime", Scenarios: []string{"ctf"}},
		{Name: "curl", Category: "utility", Scenarios: nil}, // 通用
	}}
	// Act
	web := m.FilterByDomain("web")
	// Assert：web 域可见 sqlmap + curl（通用），不含 pwntools
	if len(web.Tools) != 2 {
		t.Fatalf("want 2 tools for web, got %d: %+v", len(web.Tools), web.Tools)
	}
	names := web.Names()
	if names[0] != "curl" || names[1] != "sqlmap" {
		t.Fatalf("want [curl sqlmap], got %v", names)
	}
	// 原 manifest 不被修改（不可变）
	if len(m.Tools) != 3 {
		t.Fatal("FilterByDomain 不应修改原 Manifest")
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/tools/manifest/ -run TestFilterByDomain -v`
Expected: FAIL（方法未定义）

- [ ] **Step 3: 实现 FilterByDomain**

```go
// FilterByDomain 返回仅含当次交战域可见工具的新 Manifest（不可变，不改原 m）。
// 工具 Scenarios 为空 = 通用工具（所有域可见）；否则需包含 domain。
func (m *Manifest) FilterByDomain(domain string) *Manifest {
	out := make([]Tool, 0, len(m.Tools))
	for _, t := range m.Tools {
		if len(t.Scenarios) == 0 || contains(t.Scenarios, domain) {
			out = append(out, t)
		}
	}
	return &Manifest{Tools: out}
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/tools/manifest/`
Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add internal/tools/manifest/
git commit -m "feat(manifest): 接入 FilterByDomain 交战域工具可见性过滤"
```

### Task 7.2: catalog 渲染按当次交战域过滤

**Files:**
- Modify: `internal/builder/hunter/skill.go`（`BuilderParams`/`hunterDeps` 增 `Domain string` 传递路径 —— catalog 需知当次交战域）
- Modify: `internal/builder/hunter/user_prompt.go`（`:116` `buildToolingCatalog(deps.ToolsManifest)` → 先按 p.Domain 过滤再渲染）
- Modify: `internal/skill/params.go`（若 BuilderParams 定义在此，加 Domain）
- Modify: 调用 BuildUserPrompt 的 handler（cmd/runner swarm/solo eino：`BuilderParams{... Domain: scen.Domain}`）

**Interfaces:**
- Consumes: `cfgscenario.Scenario.Domain`（handler 已由 `ScenarioByCode` 载入 scenario，见 M5）、`manifest.FilterByDomain`（Task 7.1）
- Produces: catalog 仅渲染当次交战域可见工具

> **M5 遗留衔接**：M5 删了 `BuilderParams.Mode`（Task 5.3）。M7 加 `BuilderParams.Domain`。两者同一结构体，M5 删旧、M7 加新，先后到位。
>
> **为何传 domain 而非 scenario_id**：过滤键是交战域（web/ctf，见 D11），builder 侧只认 domain。domain 由 handler 从 `ScenarioByCode(p.ScenarioID)` 载入的 `scen.Domain` 取得，不下沉 scenario code 到 builder。

- [ ] **Step 1: 加 Domain 到 BuilderParams**

`skill.BuilderParams`（Task 5.3 已删 Mode）加 `Domain string`。

- [ ] **Step 2: user_prompt.go 过滤渲染**

```go
if catalog := buildToolingCatalog(domainManifest(deps.ToolsManifest, p.Domain)); catalog != "" {
	b.WriteString("\n\n")
	b.WriteString(catalog)
}
```
其中 `domainManifest` 小helper：`p.Domain` 空则返回原 manifest（不过滤），否则 `m.FilterByDomain(p.Domain)`。也可直接内联判空。

- [ ] **Step 3: handler 传 Domain**

cmd/runner swarm/solo eino 里 `BuildUserPrompt(ctx, h.hunterDeps, skill.BuilderParams{... Domain: scen.Domain})`（scen 是 handler 已 `ScenarioByCode` 载入的当次 scenario）。

- [ ] **Step 4: 测试**

`skill_test.go` 的 `TestBuildToolingCatalog` 补一个「按交战域过滤后只渲染子集」用例。

- [ ] **Step 5: build + test**

Run: `go build ./... && go test ./internal/builder/... ./internal/tools/... 2>&1 | tail`
Expected: PASS

- [ ] **Step 6: 提交**

```bash
git add internal/builder/ internal/skill/ cmd/runner/
git commit -m "feat(runner): 工具索引段按当次交战域过滤渲染（domain 圈可见）"
```

## M8：前端 — ScenarioPicker + scenario/playbook/hunter 配置 CRUD

> **对应 D12**。两件事：(1) 用户侧把 `RolePicker`（按 mode 过滤角色）换成 `ScenarioPicker`（列全部 enabled scenario，不过滤 mode）；(2) 新增配置管理台，对 scenario/playbook/hunter 三类配置做增删改查（DB 事实源、前端可编辑，见 D6/D7）。技术栈沿用 React 19 + TS + Vite + zustand + react-router v7 + pnpm。
>
> **命名彻底**：前端不留 `mode`/`role` 残留（`mode==='active'`、`RolePicker`、`r.mode` 等全清），grep 校验零命中。

### Task 8.1: RolePicker → ScenarioPicker（删 mode 过滤）

**Files:**
- Rename+Modify: `web/src/features/conversation/RolePicker.tsx` → `ScenarioPicker.tsx`
- Rename+Modify: `web/src/features/conversation/RolePicker.test.tsx` → `ScenarioPicker.test.tsx`
- Modify: 引用 `RolePicker` 的父组件（grep 确认，至少 `Composer.tsx`）

**Interfaces:**
- Consumes: `GET /api/scenarios`（返回 enabled scenario 列表：`{id, code, name, description}`）
- Produces: `<ScenarioPicker value={scenarioCode} onChange={(code)=>...} />`，列出所有 enabled scenario，**不再按 mode 过滤**。**value/onChange 用 scenario `code` 而非 uuid**——因为它最终喂 `POST /api/scans` → `task.scenario_id`，该列存 code（见 D3）；派发时 runner `ScenarioByCode` 用它查。`GET /api/scenarios` 返回体已含 `code` 字段，直接取用。

- [ ] **Step 1: 改测试（先驱动）**

`ScenarioPicker.test.tsx`：断言渲染出接口返回的全部 scenario、点击回调带 scenario **code**（非 id）。删掉原 `filter(r => r.mode === mode)` 相关的 mode 断言。

```tsx
test('lists all enabled scenarios without mode filtering', async () => {
  // Arrange：mock GET /api/scenarios 返回 web-pentest + traffic-analysis 两条
  // Act：渲染 <ScenarioPicker value="" onChange={onChange} />
  // Assert：两条都出现（不因 mode 被过滤掉）；点击后 onChange 收到 scenario.code（如 "web-pentest-killchain"），非 uuid
});
```

- [ ] **Step 2: 跑测试确认失败**

Run: `cd web && pnpm test ScenarioPicker`
Expected: FAIL（组件/接口未就位）

- [ ] **Step 3: 实现 ScenarioPicker**

```bash
git mv web/src/features/conversation/RolePicker.tsx web/src/features/conversation/ScenarioPicker.tsx
git mv web/src/features/conversation/RolePicker.test.tsx web/src/features/conversation/ScenarioPicker.test.tsx
```
组件内：删 `mode` prop 与 `filter(r => r.mode === mode)`；数据源从 `roles` 改 `scenarios`（`useScenarios()` hook 拉 `GET /api/scenarios`）；选项 label 用 `scenario.name`、副标题用 `scenario.description`、**选中值用 `scenario.code`**（onChange 回传 code，喂给 `POST /api/scans` 的 `scenario_id`，见 D3）。

- [ ] **Step 4: 跑测试确认通过**

Run: `cd web && pnpm test ScenarioPicker`
Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add web/src/features/conversation/
git commit -m "feat(web): RolePicker→ScenarioPicker，删 mode 过滤，列全部 enabled 场景"
```

### Task 8.2: Composer 删 mode 硬编码，startChat 发 scenario_id

**Files:**
- Modify: `web/src/features/conversation/Composer.tsx`（删 `mode="active"` 硬编码；`startChat(brief)` → `startChat(brief, scenarioId)`）
- Modify: `web/src/features/conversation/Composer.test.tsx`
- Modify: `web/src/api/client.ts` + `web/src/api/types.ts`（请求体 `mode` → `scenario_id`）
- Modify: `web/src/stores/`（若 store 存了 `mode`，改 `scenarioId`）

**Interfaces:**
- Consumes: `ScenarioPicker`（Task 8.1）、`POST /api/scans`（请求体 `{brief, scenario_id}`）
- Produces: Composer 提交时带当前选中的 `scenario_id`；请求/响应 DTO 无 `mode` 字段

> **`scenario_id` 载的是 code**（对应 D3）：前端变量虽名 `scenarioId`，值取自 `scenario.code`（ScenarioPicker onChange 回传的就是 code）。`POST /api/scans` body 的 `scenario_id` 字段直接落到 `task.scenario_id`(text，存 code)。全链不出现 scenario uuid。

> **输入统一 brief（对应 D5）**：Composer 只有一个 brief 文本输入框，不再有「主动/被动」切换。附件为未来扩展点，本期不做输入控件。

- [ ] **Step 1: 改测试**

`Composer.test.tsx`：删所有 `mode` 断言；断言提交时 payload 含 `scenario_id`、`brief`，无 `mode`。

- [ ] **Step 2: 跑测试确认失败**

Run: `cd web && pnpm test Composer`
Expected: FAIL

- [ ] **Step 3: 实现**

- `Composer.tsx`：删 `mode="active"` 及任何 `mode` 相关 state/prop；引入 `ScenarioPicker`，把选中的 `scenarioId` 提交给 `startChat(brief, scenarioId)`。
- `api/types.ts`：`CreateScanRequest { brief: string; scenario_id: string }`（删 `mode`）；`TaskSummary`/会话 DTO 的 `mode` → `scenario_id`（对齐 M5 后端 DTO）。
- `api/client.ts`：`createScan` body `{ brief, scenario_id }`。
- `stores/`：会话相关 store 若持 `mode`，改 `scenarioId`。

- [ ] **Step 4: 跑测试确认通过**

Run: `cd web && pnpm test Composer`
Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add web/src/
git commit -m "feat(web): Composer 删 mode 硬编码，提交带 scenario_id，输入统一 brief"
```

### Task 8.3: 配置管理台 — scenario/playbook/hunter CRUD UI

**Files:**
- Create: `web/src/features/config/` 目录（新 feature）
  - `ScenarioAdmin.tsx` — scenario 列表 + 编辑表单（name/description/instruction/domain/engine/playbook 选择/enabled）
  - `PlaybookAdmin.tsx` — playbook 列表 + 编辑（name/description/enabled + 组合 hunter 多选与排序 position）
  - `HunterAdmin.tsx` — hunter 列表 + 编辑（code/kind/name/description/body/tools/max_iterations/enabled）
  - `config.api.ts` — 三类资源的 CRUD 客户端
  - `config.store.ts` — zustand store（可选，列表缓存）
  - `*.test.tsx` — 每个 admin 组件的渲染 + 提交测试
- Modify: `web/src/router.tsx`（加 `/config/scenarios`、`/config/playbooks`、`/config/hunters` 路由）
- Modify: `web/src/layout/`（导航加「配置」入口）

**Interfaces:**
- Consumes（后端 M1 configstore + M5 装配已就绪，此处对接 REST）:
  - scenario: `GET/POST /api/scenarios`、`GET/PUT/DELETE /api/scenarios/:id`
  - playbook: `GET/POST /api/playbooks`、`GET/PUT/DELETE /api/playbooks/:id`（含 `hunters:[{hunter_id,position}]`）
  - hunter: `GET/POST /api/hunters`、`GET/PUT/DELETE /api/hunters/:id`
- Produces: 三类配置的可视化 CRUD；保存即写 DB（后端经 configstore 发失效总线，见 D7）

> **表单契约**：engine 是下拉 `solo|swarm`（对应 D2 正交，任意 scenario 可切）。domain 是交战域文本/下拉（`web`/`ctf`/`cloud`…，缺省 `web`，决定 CLI 扫描工具目录可见性，见 D11）。hunter.kind 下拉 `orchestrator|domain`；`kind=orchestrator` 的 hunter 全局唯一、不出现在 playbook 组合选择列表里（前端在 PlaybookAdmin 的 hunter 多选中过滤掉 orchestrator，对齐 D2）。tools 是 tool code 多选（数据源 `GET /api/tools` 或静态清单）。
>
> **后端 REST 前置**：本 Task 假定 M1/M5 已暴露上述 CRUD 端点。若端点未就位，需在 M1 的 store 层任务后补一个 httpapi handler 子任务（见 M1 Task 1.5）。此处只负责前端。

- [ ] **Step 1: 写 config.api.ts + 三个 admin 的失败测试**

每个 admin 一个测试：mock 列表接口 → 断言渲染出行；mock 编辑提交 → 断言 PUT 带正确 body。示例（HunterAdmin）：

```tsx
test('renders hunter list and submits edit', async () => {
  // Arrange：mock GET /api/hunters 返回 orchestrator + recon 两条
  // Act：渲染 <HunterAdmin/>，点 recon 编辑，改 body，保存
  // Assert：PUT /api/hunters/:id body 含改后的 body、kind=domain
});
```

- [ ] **Step 2: 跑测试确认失败**

Run: `cd web && pnpm test config`
Expected: FAIL

- [ ] **Step 3: 实现三个 admin + api + 路由 + 导航**

- `config.api.ts`：封装三资源 CRUD（复用 `api/client.ts` 的 fetch 基座）。
- 三个 admin：列表用表格（列：name/code/enabled/操作），编辑用抽屉或表单页。PlaybookAdmin 的组合编辑支持 hunter 多选 + 拖拽/序号设 position。
- `router.tsx`：注册三路由。
- 导航：加「配置」分区，三个子项。
- 遵循前端设计规范（语义化 HTML、hover/focus 态、无 mode 残留）。

- [ ] **Step 4: 跑测试确认通过**

Run: `cd web && pnpm test config`
Expected: PASS

- [ ] **Step 5: 前端全量门禁**

Run: `cd web && pnpm build && pnpm test`
Expected: PASS

- [ ] **Step 6: grep 校验前端无 mode/role 残留**

Run: `cd web && grep -rn "RolePicker\|mode\s*===\|mode\s*:\s*['\"]active\|r\.mode\|\.mode\b" src/ | grep -iv "scenario\|comment\|// "`
Expected: 零命中（会话/配置侧无 mode、role 残留；attack-graph 等无关模块的同名字段若存在需人工确认非本主题）

- [ ] **Step 7: 提交**

```bash
git add web/src/
git commit -m "feat(web): 新增 scenario/playbook/hunter 配置管理 CRUD 台"
```



<!-- END-OF-PLAN -->











