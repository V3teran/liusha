# assignment / task / lead 重构设计

> 状态：设计定稿（第 4 版，经对抗性审查修正运行时/迁移/编排缺口），待实现
> 日期：2026-07-05
> 前置：可清库（存量数据可丢，迁移走纯 DDL）
> 第 4 版修正见 §13（分布式聚合、原子性、编排健壮性、阶段边界、dedup 事实更正）

## 0. 背景与动机

本次重构起于一连串架构追问，最终收敛为四件事：

1. **命名不对称 + 多态冗余**：`active_scan`（强调动作）与 `passive_session`（强调时段）命名不齐；4 张共享表（hunter/finding/http_flow/llm_invocation）+ tool_invocation 靠 `(owner_type, owner_id)` 多态挂载，`owner_type` 每加一种就要改一堆地方，且 `owner_id` 无外键约束（裸 uuid）。
2. **缺"下发单元"**：批量扫描（一次提交 N 个系统）、passive 流量聚合（攒批成一个任务）、定时执行都没有载体，只能一个对话一个对话手动发。
3. **note 退役留下的情报共享洞**：子代理之间、passive 同 host 跨 run 之间，缺一块"过程情报"黑板（可疑点 / 既成发现 / 失败死路）。
4. **http_flow 混装了两种性质不同的流量**：`source=external`（代理捕获的真实用户流量，是被分析的输入，属于 host）与 `source=internal`（agent 在 sandbox 自产的流量，是干活副产物，属于 task）本质不同却共表，这是 flow 归属混乱的根因。

## 1. 模型总览（第 2 版）

```
【下发轴：容器】              【执行轴】                      【共享轴】
                              task（mode: active|passive）      按 host（Redis）:
active:                        = 一次扫描 / 一批流量分析            lead ★新（情报便签）
  assignment（下发单元）        │                                  credential（现有）
  1 → N task（fan-out）         ├─ hunter run × N（FollowUp 追加）  按 host（PG）:
  cron_schedule（定时模板）      │                                  lesson（现有）
    → 每次触发克隆 assignment    ├─ agent_traffic（internal，挂 task）★拆分
                                └─ finding（挂 task，无 TTL）
passive:
  proxy_traffic（按 host）★拆分  →  聚合器 20条/10s 切批  →  passive task
```

### 三条轴各司其职

- **下发轴（assignment / cron_schedule）**：只负责"提交了什么、何时提交、拆成几个 task"。纯下发容器，不承担共享。
- **执行轴（task → hunter run）**：task = "一次扫描活 = 一个对话"；hunter run = 每次 ReAct 执行。
- **共享轴（key，不靠容器）**：情报按 `host` 归集（lead / credential / lesson），漏洞按 `task` 归集（finding）。共享不依赖 assignment 容器。

### 四个根本修正（相对第 1 版）

1. **流量拆两张表**：`proxy_traffic`（代理捕获，属 host，被分析的输入）与 `agent_traffic`（agent 自产，属 task，replay/sitemap 弹药）。二者来源/作用/触发/归属/生命周期全不同，不再共用 http_flow。
2. **砍掉 passive_session**：代理流量独立按 host 落 proxy_traffic 后，session 不再需要承接流量；"是否在监控"从 proxy_traffic 派生。passive 变成 `proxy_traffic(host) → 聚合器 → analysis task`。
3. **cron 分两层**：定时是"闹钟模板"（cron_schedule），每次触发**克隆出一个普通 assignment** 去执行（学 K8s CronJob/Job），不与一次性 assignment 混表。
4. **flow 归属矛盾消解**：第 1 版纠结"flow 挂 host 还是 task"，根因是把两种流量当一种。拆表后，代理流量天然挂 host、agent 流量天然挂 task，矛盾消失。

## 1.5 active / passive 完整实体关系对照

两轨在 task 及以下完全对称，只在 task 之上分叉（active fan-out / passive fan-in）。

### active 轨（fan-out：一次下发拆多个网站）

```
assignment (mode=active)
   │ 1—N   fan-out（payload N 个网站条目）
   ▼
task (一个网站的一次扫描)
   ├─ 1—1  conversation
   ├─ 1—N  hunter run（初始 + 每 FollowUp 一个，同对话追加）
   ├─ 1—N  agent_traffic（agent 自产，挂 task_id）
   ├─ 1—N  finding（挂 task_id，去重按 host）
   └─ 1—1  attackgraph（按 task 投影）
```

### passive 轨（fan-in：一批流量汇成一个任务）

```
proxy_traffic (代理捕获，按 host 持续落，先于 task)
   │ N—1   聚合器：同 host 20 条/10s 攒一批
   ▼
assignment (mode=passive, source=auto)
   │ 1—1   fan-in（一批流量只产一个 task）
   ▼
task (这批流量的一次分析)
   ├─ 1—1  conversation
   ├─ 1—N  hunter run（一般 1 个 traffic-analysis）
   ├─ N—1  proxy_traffic（这批回填 consumed_by_task_id 指向本 task）
   ├─ 1—N  finding（挂 task_id）
   └─ 1—1  attackgraph（按 task 投影）
```

### 对照表

| 维度 | active | passive |
|------|--------|---------|
| assignment→task | **1—N**（fan-out 拆网站） | **1—1**（一批流量一个 task） |
| "一个网站"是什么 | `task.target_host`（task 绑一个网站） | `host`（归集 key，非实体；一 host 多 task） |
| task→对话 | 1—1 | 1—1 |
| task→hunter run | 1—N（FollowUp） | 1—N（一般 1） |
| task 的流量 | `agent_traffic`（自产，挂 task） | `proxy_traffic`（捕获，挂 host，回填 consumed_by_task_id） |
| 流量与 task 时序 | task 先、流量后 | **流量先、task 后** |
| finding/attackgraph | 挂 task，按 task | 挂 task，按 task |
| lead/credential/lesson | 按 host 共享 | 按 host 共享 |

**对称层**：task 及以下（对话 1—1、run 1—N、finding/attackgraph 按 task、lead/lesson/credential 按 host）完全一致。
**分叉层**：仅 task 之上——active 是 assignment 拆 N 网站（网站是 task 属性），passive 是流量汇成 1 task（host 是归集 key）。方向相反（fan-out vs fan-in），产出的 task 同构、下游同路。

**对话锚定**：两轨都 task 1—1 conversation。一次下发 20 网站(active)=20 task=20 对话；一批流量(passive)=1 task=1 对话。FollowUp（仅 active）同对话追加 run，不新建对话。

## 2. task：合并 active_scan + passive_session

### 表结构

```sql
CREATE TABLE task (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    mode          text NOT NULL CHECK (mode IN ('active','passive')),
    assignment_id uuid NOT NULL REFERENCES assignment(id) ON DELETE CASCADE,
    brief         text NOT NULL DEFAULT '',   -- active：用户 brief；passive：空
    target_host   text NOT NULL DEFAULT '',   -- active 可空；passive 必填（被分析 host）
    status        text NOT NULL CHECK (status IN ('active','completed','aborted')),
    heartbeat_at  timestamptz NOT NULL DEFAULT now(),
    paused_ms     bigint NOT NULL DEFAULT 0,
    created_at    timestamptz NOT NULL DEFAULT now(),
    ended_at      timestamptz,
    error_message text NOT NULL DEFAULT ''
);
CREATE INDEX task_assignment_idx ON task (assignment_id);
CREATE INDEX task_mode_status_idx ON task (mode, status, created_at DESC);
CREATE INDEX task_host_idx ON task (target_host) WHERE target_host <> '';
```

- 字段 = `active_scan` ∪ `passive_session` 并集，`mode` 区分，无用字段留默认。
- `assignment_id` NOT NULL——一切 task 必属于一个 assignment（§3 "一切皆 assignment"），无孤儿。
- **passive task 语义变化**：不再是"常驻监控会话"，而是"对某 host 的一批捕获流量的一次分析"。同 host 可有多个 passive task（每批流量一个），不再受"1 host 1 active session"唯一约束限制。

### 与 conversation 的关系

- 统一为 `conversation.task_id → task`（对话持有，FK ON DELETE SET NULL），两轨对称。取代现状的 `conversation.scan_id`（active）+ `passive_session.conversation_id`（passive 反向持有）不对称设计。
- 基数：`conversation 0..1 ↔ 1 task`。FollowUp 同对话追加新 hunter run，不新建对话。

## 3. assignment + cron_schedule：下发容器与定时模板分层

### 3.1 一切皆 assignment

所有下发入口后端统一走"建 assignment → 展开 task"，前端表现自由：

| 前端形态 | 后端 |
|----------|------|
| 对话框直发单个网站 | assignment(mode=active, 1 条目) → 1 task |
| 批量提交 20 个网站 | assignment(mode=active, 20 条目) → 20 task（fan-out） |
| 手动勾选/粘贴一批流量 | assignment(mode=passive, 1 批流量) → 1 task（fan-in） |
| 代理流量自动攒批 | 聚合器建 assignment(mode=passive, source=auto) → 1 task |

好处：后端一条路径；`task.assignment_id` 永远非空、强外键；无孤儿 task 脏分支。单发 = 单元素 assignment。

### 3.2 assignment 表（一次性下发实例）

```sql
CREATE TABLE assignment (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    mode         text NOT NULL CHECK (mode IN ('active','passive')),
    source       text NOT NULL CHECK (source IN ('manual','auto')),  -- 谁发：人工/聚合器
    payload      jsonb NOT NULL DEFAULT '[]',  -- active=[{brief,host}...]；passive=[{flow_id...}]
    title        text NOT NULL DEFAULT '',
    -- 由哪个定时模板克隆而来（手动下发为 NULL）
    schedule_id  uuid REFERENCES cron_schedule(id) ON DELETE SET NULL,
    created_at   timestamptz NOT NULL DEFAULT now()
    -- 注意：无 status 列。整体状态由子 task 聚合派生（读时算），不落存储
);
CREATE INDEX assignment_schedule_idx ON assignment (schedule_id) WHERE schedule_id IS NOT NULL;
```

- `source` = 谁发的（manual 人工 / auto 聚合器）。**定时不再是 assignment 的字段**——见 3.3。
- assignment 永远是"一次性实例"：展开成 task 后，它的生命就交给子 task，本身无终态列（终态派生）。

### 3.3 cron_schedule 表（定时模板，学 K8s CronJob/Job）

定时任务是"闹钟模板"，本身无终态，只有启用/停用；每次触发**克隆出一个一次性 assignment** 去执行：

```sql
CREATE TABLE cron_schedule (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    mode         text NOT NULL CHECK (mode IN ('active','passive')),
    cron_expr    text NOT NULL,               -- 标准 5 段 cron
    payload      jsonb NOT NULL DEFAULT '[]', -- 触发时克隆进新 assignment 的清单
    title        text NOT NULL DEFAULT '',
    enabled      boolean NOT NULL DEFAULT true,
    next_run_at  timestamptz,
    last_run_at  timestamptz,
    created_at   timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX cron_schedule_due_idx ON cron_schedule (next_run_at) WHERE enabled;
```

触发链：Scheduler 到点 → 读 cron_schedule → 克隆一个 `assignment(source='manual'..., schedule_id=<模板id>)` → 正常展开 task → 更新 `last_run_at` / `next_run_at`。

这样：一次性 assignment 有终态（派生），定时模板无终态（只有开关）；历史每次触发生成独立 assignment（可回溯"哪天那次跑的哪些 task"），不堆在一个对象上。

## 4. 并发、定时、per-host 限速

### 4.1 全局并发（消费侧，已有）

放 scanner，复用 asynq worker 并发度 + queue（`cmd/scanner/main.go` `Concurrency: AsynqConcurrency`）。阻塞等待、来一个跑一个、满了排队。**assignment / api 层不自写并发**（那是 CSAI 被单体内存逼出的做法；liusha 有 asynq，并发天然属 worker 层）。

### 4.2 定时（生产侧，新增）

api 侧 asynq Scheduler goroutine，扫 `cron_schedule.next_run_at` 到点的模板 → 克隆 assignment → Enqueue。单副本够用；api 多副本时抽 `cmd/scheduler` 独立进程（asynq Scheduler 有 leader 选举）。

### 4.3 per-host 限速（新增，渗透场景硬需求）

全局并发挡不住"20 个 task 里 5 个恰好打同一 host" → 触发目标 WAF 封 IP / 目标过载。业界扫描器都有 per-target rate limit。方案：

- asynq 支持 `Queue` 分组，但 per-host 是动态的（host 值无穷），不能靠静态 queue。
- 用 **asynq 的 `rate.Limiter` 或独立信号量**：worker 领到 task 后，按 `task.target_host` 取一个 per-host 信号量（Redis `INCR`/令牌桶），超过 host 并发上限（如 2）则该 task 短暂 requeue 退避。
- 上限可配（默认同 host 并发 ≤ 2，请求间隔下限可选）。

落点：scanner worker 消费前置一层 per-host gate。这是 P1 就要纳入的，不是可选。

## 5. 流量拆两张表（核心修正）

### 5.1 为何拆

现状 `http_flow` 靠 `source` 字段混装两种性质完全不同的流量：

| | 代理捕获流量（external） | agent 自产流量（internal） |
|---|---|---|
| 来源 | 用户浏览器，外部真实流量 | agent 在 sandbox 打的 |
| 作用 | **被分析的对象**（输入） | **干活副产物**（过程记录/弹药） |
| 触发 | 触发 trafficAnalysis | 明确不触发（防自激震荡） |
| 归属 | 属于 **host**（先于任何 task） | 属于 **task/run** |
| 生命 | 持续流入 | 一次 run 内产生 |

唯一共同点是"长得像 HTTP 请求"。合表是"形状相同就合"的错误。拆分后归属天然清晰，且消解第 1 版"flow 挂 host 还是 task"的阻断级矛盾。

**命名**：两张表都以"流量的产生者"命名（proxy 拦的 / agent 打的），维度统一、对称、不与 mode 绑死。

### 5.2 proxy_traffic（代理捕获，属 host）

```sql
CREATE TABLE proxy_traffic (
    id          bigserial PRIMARY KEY,
    host        text NOT NULL,               -- 归属轴（先于 task 存在）
    method      text NOT NULL,
    scheme      text NOT NULL DEFAULT '',
    uri         text NOT NULL DEFAULT '',
    path        text NOT NULL DEFAULT '',
    status_code int  NOT NULL DEFAULT 0,
    req_headers  jsonb, req_body  bytea,
    resp_headers jsonb, resp_body bytea,
    duration_ms int  NOT NULL DEFAULT 0,
    -- 被哪个 passive task 消费（聚合成 task 时回填；未消费为 NULL）
    consumed_by_task_id uuid REFERENCES task(id) ON DELETE SET NULL,
    captured_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX proxy_traffic_host_idx ON proxy_traffic (host, captured_at DESC);
CREATE INDEX proxy_traffic_unconsumed_idx ON proxy_traffic (host)
    WHERE consumed_by_task_id IS NULL;
```

- 代理流量进来直接按 host 落这里，**不需要先有 task/session**——解决第 1 版"落库时 task 还不存在"的断层。
- `consumed_by_task_id`：聚合器切批建 task 时回填，标记"这批被哪个分析 task 吃了"。既是溯源，也让"未消费流量"可查（`WHERE consumed_by_task_id IS NULL`）。

### 5.3 agent_traffic（agent 自产，属 task）

```sql
CREATE TABLE agent_traffic (
    id          bigserial PRIMARY KEY,
    task_id     uuid NOT NULL REFERENCES task(id) ON DELETE CASCADE,
    hunter_id   uuid REFERENCES hunter(id) ON DELETE SET NULL,  -- 哪个 agent 发的
    identity    text,   -- 身份戳（browser_use 的 identity / 登录账号）
    tool        text,   -- 工具戳（browser / curl...）
    host        text NOT NULL,
    method      text NOT NULL,
    path        text,
    status_code int NOT NULL DEFAULT 0,
    req_headers jsonb, req_body bytea,
    resp_headers jsonb, resp_body bytea,
    duration_ms int NOT NULL DEFAULT 0,
    created_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX agent_traffic_task_idx ON agent_traffic (task_id, created_at);
CREATE INDEX agent_traffic_identity_tool_idx ON agent_traffic (task_id, identity, tool);
```

- 挂 `task_id`（不再是多态 owner），是 `replay_flow` / `list_flows` / `view_flow` 的弹药库、sitemap 派生攻击面的源。
- 保留 identity/tool 戳（现 http_flow 0065 的能力），供 `list_flows(identity=X, tool=browser)` 精确锁定。

### 5.4 两条流量链路（含 internal 侧填充）

**链路一：proxy_traffic（代理捕获，passive）**
```
用户浏览器 → sanitizer 代理 → flow_events stream
  → ingestor.handleExternalSnap → 落 proxy_traffic（按 host）
  → host 窗口累加 → 20 条/10s → 建 passive task → 回填 consumed_by_task_id
```

**链路二：agent_traffic（agent 自产，active）**
```
sandbox 内 agent 的 browser/CLI 打请求
  → POST /internal/v1/flows/ingest（ingest_handler）
  → ingestor.SubmitInternal → handleInternalSnap
  → 【改造点】反查 hunter：现在拿 owner_type/owner_id，改为拿 task_id
  → 落 agent_traffic（按 task_id）
  → 不 enqueue（防自激震荡，不变）
```

**消费侧改造**：
- `replay_flow` / `list_flows` / `view_flow`：从 `agent_traffic` 按 task_id 读（active 用）。
- `sitemap.Projector`：数据源从 `http_flow(source=internal)` 改为 `agent_traffic`（按 task_id），`DistinctRoutesWithRepresentative` 迁过去。**"仅 active"判断改为 `task.mode='active'`**（见 §8.2）。
- passive trafficAnalysis：分析对象从 `proxy_traffic`（按 host + 本批 id 范围）读。
- **credential 抽取不直接碰流量表**：agent 用 `list_flows/view_flow` 看流量后手动 `write_credential`，隔着工具。只要工具改对表（读 agent_traffic），credential 链路自动正确，无需改 credential 包。

## 6. passive 流程（砍 session + 聚合器）

### 6.1 砍掉 passive_session

拆出 proxy_traffic 后，session 的三个职责全部落空或退化：

- 承接流量 → proxy_traffic 按 host 直接落，不需要 session。
- conversation 锚点 → conversation 锚到 analysis task（与 active 对称）。
- "持续监控"状态 → 无需实体表，从 proxy_traffic 派生（见 6.4）。

`passive_session` 表删除，`internal/passivesession` 包删除。

**观察单元 = task，不是 host**（关键定调）：一个 passive task = 一批（20 条）流量 + 一个 agent 分析 + 一个对话，是完整自洽的有界观察单元。用户打开一个 passive task 的对话，看的是"这 20 条流量 agent 怎么分析的"。不追求"一个 host 的连续对话流"——那会让上下文无限膨胀、agent 失焦（passive_session 长命对话的老毛病）。这与流处理的 micro-batch（无界流切有界批）同理：切批是优点不是缺陷。host 是聚合查询维度（见 6.4 + §8.1），不是对话容器。

### 6.2 新数据流

```
用户浏览器 → 代理(sanitizer) → flow_events stream
  → ingestor 逐条落 proxy_traffic（按 host，consumed_by_task_id=NULL）
  → host 内存窗口累加器：host → {计数, 首条时间, id 范围}
  → 触发（20 条 或 距首条 10s，先到先触发）：
       建 assignment(mode=passive, source=auto)
       → 展开 1 个 passive task
       → 把这批 proxy_traffic 的 consumed_by_task_id 回填为该 task
       → Enqueue → 一个 agent 分析这批流量
  → 清零该 host 窗口
```

### 6.3 关键约束

- **流量本体不缓存在聚合器**：已在 proxy_traffic，窗口只攒 id 范围。窗口内存态，进程重启丢窗口无妨（未消费流量仍在表里，`consumed_by_task_id IS NULL` 可被下一轮或补偿逻辑重新捞）。
- 参数 20 条 / 10s 可配。20 条 ≈ 一个用户操作单元的 XHR 量级；100 条太多（撑爆上下文）。
- **手动 passive**（勾选历史流量 / 粘贴 raw）：payload 里给 proxy_traffic 的 id 集合（或 raw 直接建临时记录）→ 建 assignment(source=manual) → 1 task，同样回填 consumed_by_task_id。
- 落点：`internal/ingestor/traffic.go` 的 `handleExternalSnap` 改为"落 proxy_traffic + 喂窗口累加器"（不再 LookupOrCreate session、不再逐条 enqueue）；`handleInternalSnap` 改为落 agent_traffic（反查 hunter→task_id，见 §5.4 链路二）。

### 6.4 "监控中 host 列表" —— 派生，不落表

砍 session 后，前端"哪些 host 正在被动监控"从 proxy_traffic 派生：`SELECT host, max(captured_at) FROM proxy_traffic GROUP BY host HAVING max(captured_at) > now() - interval 'N min'`——最近有流量 = 在监控。与系统"读模型不落表、从真相源派生"范式一致（同 sitemap/attackgraph），不新增标志表。

## 7. lead：情报黑板（补 note 退役的洞）

### 7.1 定位与共享轴

lead = "一次交战内、需跨 agent / 跨 run 同步的过程情报"，承载三类（一对应用户勾选需求）：重大发现/顿悟、失败死路/负面经验、待深挖可疑点。

它补的是 note 退役后未被平替的那半——note 的"agent 自己 scratchpad"已被对话 reasoning 事件正确平替；"跨 agent 情报黑板"没被平替（子代理看不到对话），lead 只承担后者。

**共享轴 = host**（与 credential / lesson 同轴），不挂 assignment。理由：情报本质"关于目标"，不"关于任务"；同 host 不管流量何时来、属哪个 task，情报自动汇一起——天然解决 passive"指不定啥时进同 host 流量"。因此**砍掉更长命的 project 概念**，共享靠 key(host) 不靠容器，概念不增反减。粒度 `host`（非 host:port），全系统统一。

### 7.2 存储：Redis（不是 PG）

lead 是高频写（agent 边做边记）+ 高频读（每轮注入）+ 会过期，特性同当年的 note（note 就在 Redis）。放 Redis：

- key = `liusha:lead:{host}`，value = LIST（每条一个 JSON）。
- **淘汰天然由 Redis 实现**（PG 要自写清理 job）：
  - passive：`LTRIM` 保留最近 200 条（复用 note 退役前 MaxEntries=200 机制，该机制本身正确）。
  - active：task 终态后对该 host 的 lead 设 `EXPIRE`（如 7 天冷却）。

条目结构：

```json
{
  "kind": "clue|fact|deadend",
  "note": "一句人话（位置/细节都在这里说清）",
  "hunter_id": "...",
  "source_task_id": "...",   // 哪次 task 发现的（溯源，前端展示"来自哪次扫描"）
  "created_at": "..."
}
```

补 `source_task_id`（第 1 版遗漏）：lead 跨 task 共享，只记 hunter_id 无法回答"这条线索哪次扫描发现的"，故补。

### 7.3 kind 三类：按 agent 动作分（最少且穷尽）

- **clue（可疑点）** → agent 动作：去验证。
- **fact（既成发现）** → agent 动作：记住并利用。
- **deadend（死路）** → agent 动作：绕开别试。

一条情报能触发的动作只有这三种（跟进/利用/回避），不存在第四种。**不用 CSAI 的九类主题枚举**（按主题分会逼 agent 纠结归类却不改变行动）。

### 7.4 写入 append + 读时去重

抄 finding 的"写诚实、读去重"：写时纯 append（agent 零负担，不编 key 不判重）；读时（注入前）按 `(host, kind)` 分组取最近 N 条（每 kind ≤ 5），新压旧、时序+数量截断，不做语义合并（过重）。

### 7.5 读写工具与注入（解"子代理看不到 orchestrator"）

- 写入工具 `write_lead(kind, note)`：身份值（host/hunter_id/source_task_id）闭包注入，不进 LM 参数（防串库）。LM 只填 kind + note。授予 recon / exploitation / traffic-analysis。
- 读取靠注入，不给 read 工具（与 lesson 一致）：
  - 顶层 agent：`BuildUserPrompt` 加"情报黑板(lead)"段，按 host 捞 + 读时去重后注入。
  - 子代理：orchestrator 派 `task` 时把该 host 的 lead 索引拼进 **task description**（不动 eino `WithFullChatHistoryAsInput`，避免 token 爆炸）。**这就是"子代理看不到 orchestrator user message"的优雅解——共享的是黑板，不是对话。**

### 7.6 lead(fact) 与 finding 的边界

> finding = 有 evidence、可复现 repro_cmd、进交付报告的漏洞。lead(fact) = 尚未坐实成 PoC 的认知/观察。

口诀（写进 prompt）：能给可复现 PoC + evidence → finding；仅观察到事实、没做成 PoC → lead(fact)。升级路径：lead(fact) 验证成 PoC → 写 finding；lead 不删，靠淘汰消失。lead 过期 / finding 永久，两条时间线自然分流（无需 CSAI 的显式 promote 通道）。

## 8. finding host 聚合视图 + 投影器改造

### 8.1 finding host 视图

需求：跨 task 按 host 看漏洞（passive"这个 host 所有漏洞"、active 复盘"目标历史漏洞"）。

不改 finding 存储作用域（保持 task 级，交付完整 + 无 TTL），只加只读方法 `finding.ListByHost(host)`——finding 表本有 host 列 + host 索引，纯 SQL（现有 `ListByOwnerAndHost` 去掉 owner 约束）。不复用 sitemap.Projector（那是攻击面视图，职责单一）。

### 8.1b finding dedup：改为按 task（更正第 3 版事实错误）

**现状核实**（第 4 版更正）：finding 现有约束是 `UNIQUE(owner_id, dedup_key)`（0048/0049，`store.go:77` `ON CONFLICT (owner_id, dedup_key)`），即 **owner（≈单次扫描）级去重**——不是早先文档误写的 "host 级"。同 host 跨扫描的同洞，现状本来就会各报一条。

owner→task_id 后，owner_id 列消失，dedup 键必须重设。**定为按 task**：

- `UNIQUE(task_id, dedup_key)`——保持现状"单次扫描内去重"语义（owner ≈ task，语义等价平移），改动最小。
- `store.go` 的 `ON CONFLICT (owner_id, dedup_key)` → `(task_id, dedup_key)`。
- 与"finding 按 task 出报告""attackgraph 按 task 投影"一致：一次扫描内不重复，跨扫描/跨 task 各自独立成条（报告完整，不会因跨 task 合并而缺洞）。
- 跨 task 看同 host 全景走 `finding.ListByHost`（§8.1），不靠 dedup 合并。
- **迁移必做**：这是键变更（`owner_id`→`task_id`），P0 迁移显式重建 dedup_key 生成列上的 UNIQUE 索引；清库前提下无存量清重负担。

（早先"去重按 host"的结论建立在"现状是 host 级去重"的错误认知上，已收回。）

### 8.2 投影器改造（不是改列名，是改逻辑分支）

`sitemap.Projector` 与 `attackgraph` 现在硬编码了 owner 逻辑，迁移非机械替换：

- `sitemap/projector.go:103`：硬编码"仅 active 模式，passive 报错"（判断 owner_id 是不是 active_scan）。改为判断 `task.mode='active'`；数据源从 `http_flow(source=internal)` 改为 `agent_traffic`（按 task_id）。
- `attackgraph.Project(ownerID, messages, findings)`：
  - `OwnerID` → `TaskID`；投影**范围按 task**（一次扫描一张图，与 finding 报告按 task 对齐，图干净）。
  - 它吃 `conversation.Message`——conversation 关联从 scan_id 改 task_id 后，取 messages 的路径跟着走 task_id。
  - finding 入参按**单 task** 取（`WHERE task_id=`），**不跨 task 补齐**。已知代价：同一 host 被多个 task 打时，跨 task 的漏洞组合链（A task 的洞 depends_on B task 的洞）不在一张图里——这是"按 task 投影"的接受项（跨 task 全景走 §8.1 的 host 视图，不走执行图）。
- 这两处是**带业务分支的改造**，迁移计划单列，不并进"机械改 30 个引用点"。

### 8.3 运维横切链路（合表连带，P0 必碰）

owner→task_id + 合表牵动三条运维链路，易被"happy path 跑通"掩盖，P0 显式覆盖：

- **心跳续命**：`handler_eino_common.go` 现按 `OwnerType` switch 续 active_scan/passive_session 的 `heartbeat_at` → 合表后统一续 `task.heartbeat_at`（简化）。
- **reaper 判活**：现分别扫 active_scan/passive_session 判 stale → 改扫 `task`（按 mode 用不同 staleAfter：active 长、passive 短）。
- **abort 中止**：`activeScans.Abort`/`passiveSessions.Abort` + `watchAbortActive`/passive 轮询 → 统一 `task` 的 abort + 轮询 task.status。
- **流量 body 截断**：现 `flow.Store` Append 时按 32KiB 截断 → 拆表后 proxy_traffic / agent_traffic 两个 store 各自保留截断（机械复制）。

## 9. 迁移（纯 DDL，存量可丢）

已确认可清库，两个大迁移纯 DDL 换表、无数据搬运。

- **A. active_scan + passive_session → task**：建 task 表，旧两表存量丢弃。`internal/activescan` + `internal/passivesession` → 统一 `internal/task` 包；`internal/passivesession` 删除。
- **B. owner 多态 → task_id**：4 张共享表（hunter/finding/llm_invocation/tool_invocation）`owner_type+owner_id` → 单列 `task_id uuid REFERENCES task(id)`。合表后单外键天然成立，`internal/owner` 包删除。
- **C. http_flow 拆表**：删 http_flow，建 `proxy_traffic`（host）+ `agent_traffic`（task_id）。ingestor（external→proxy_traffic、internal→agent_traffic）/ flow store / 消费工具 / sitemap 全部改接新表。
- **D. 新增表**：assignment、cron_schedule、lead（Redis，无 migration）。`conversation.scan_id → task_id`。

顺序：建 task（A）→ 4 表加 task_id（B）→ 拆流量表（C）→ 建 assignment/cron_schedule + 改 conversation（D）→ 删 owner 包 / passivesession 包 → 改 Go 引用 + 投影器分支（§8.2）。

## 10. 实施阶段

### P0：task 合表 + owner 坍缩（地基）
建 task 表、`internal/task` 包；4 表 owner→task_id，删 owner 包；conversation.scan_id→task_id；scanner handler 按 `task.mode` 分流；**改运维横切链路（§8.3）：heartbeat/reaper/abort 全指 task**。验证清单（不止 happy path）：现有 active/passive 端到端跑通 + **中止一个运行中 task 生效** + **reaper 能判死 stale task** + **心跳续命防误杀**。

### P1：流量拆表 + assignment + per-host 限速
http_flow → proxy_traffic + agent_traffic；消费工具/sitemap/attackgraph 改接（含 §8.2 分支改造）；建 assignment 表，api 下发统一走 assignment；scanner 加 per-host gate。验证：单发/批量下发、replay_flow、sitemap 均正常。

### P2：passive 砍 session + 聚合器
删 passive_session；ingestor `handleExternalSnap` 改落 proxy_traffic + host 窗口累加（20 条/10s）→ 建 passive assignment→task→回填 consumed_by；"监控中 host 列表"改派生。验证：模拟同 host 连续流量按窗口聚合，非逐条。

### P3：lead 情报黑板
`internal/lead`（Redis，append + 读时去重 + LTRIM/EXPIRE 淘汰）；`write_lead` 授三角色；`BuildUserPrompt` 加 lead 段 + orchestrator 派 task 注入 task description；prompt 写清 lead/finding 边界。验证：recon 写 lead → exploitation 在 task description 读到。

### P4：cron 定时 + finding host 视图
建 cron_schedule 表；api Scheduler goroutine 扫 due 模板→克隆 assignment；`finding.ListByHost` + 前端 host 漏洞全景。验证：定时模板到点克隆执行；host 视图跨 task 聚合。

## 11. 设计取舍备忘

- **流量拆两表**：代理流量（属 host、被分析）vs agent 流量（属 task、弹药），来源/作用/触发/归属/生命全不同，合表是"形状相同就合"的错误。拆后 flow 归属矛盾消解。
- **砍 session**：拆表后 session 三职责全落空/退化，删之。第 1 版劝留 session 是因未想清流量归属，已修正。
- **cron 分层**：CronJob/Job 模式——cron_schedule 无终态模板，每触发克隆一次性 assignment；不与一次性混表。
- **共享靠 host 不靠容器**：砍 project 概念，assignment 回归纯下发。
- **lead 放 Redis 不放 PG**：高频读写 + 过期特性同 note；Redis TTL/LTRIM 天然实现淘汰。
- **lead 不抄 CSAI**：kind 按 agent 动作分（非可信度/主题）；单 note 字段；无 key（不让 LM 编）；无 target（归 note）；仅借"收敛表 + 索引/详情分离"思路，而这本是你 lesson/tooling-catalog 已有范式。
- **per-host 限速**：渗透硬需求，防封 IP/目标过载，全局并发挡不住同 host 叠打。
- **迁移敢用纯 DDL**：全靠"存量可丢"。将来有真实数据则 B/C 需改双写回填（如当年拆 engagement 0038-0041）。

## 12. 术语对照（旧 → 新）

| 旧 | 新 |
|----|----|
| active_scan / passive_session | task（mode 区分） |
| owner_type + owner_id | task_id |
| http_flow（source 混装） | proxy_traffic（host）+ agent_traffic（task） |
| （无） | assignment（一次性下发）/ cron_schedule（定时模板） |
| note（已退役） | lead（Redis，仅跨 agent 情报共享） |
| internal/owner、internal/passivesession 包 | 删除 |

## 13. 第 4 版修正（对抗性审查结论）

独立审查在运行时/迁移/编排三层揪出 5 个阻断 + 4 个重要问题，逐条修正如下。

### 13.1 🔴 分布式下 host 聚合窗口拆批 → 状态挪 Redis

问题：ingestor 跑在水平扩展的 scanner 内（`cmd/scanner/main.go`），external 流量走 Redis 消费组（组名 `liusha-ingestor`，`XREADGROUP`）——同 host 流量被分给不同实例，进程内内存窗口是**分片非副本**，20 条被拆到 N 个实例各攒各的 → 拆成 N 个残批。且 `ConsumerName` 默认静态 `ingestor-1`（`config.go:515`），多副本用同名进同组会 pending 混乱。

修正：**聚合窗口计数挪到 Redis**（per-host `INCR` + 首条时间戳 key），与 §4.3 per-host 限速同源。任何实例消费到某 host 流量都 `INCR` 同一个 Redis 计数，达阈值/超时的实例抢锁（`SET NX`）建 task。窗口不再是进程内存。ConsumerName 改由 hostname/env 注入唯一值。

### 13.2 🔴 proxy_traffic 建 task + 回填非原子 → 重复消费

问题：§6.2"建 task → 回填 consumed_by_task_id"两步非原子，中间崩溃则这批永远 `NULL`，下轮重新捞 → 重复建 task 重复分析；"补偿逻辑"原文只在括号里许诺、未落地。

修正：
- 回填与建 task 放**同一事务**（task INSERT + `UPDATE proxy_traffic SET consumed_by_task_id WHERE id IN (批) AND consumed_by_task_id IS NULL`）。
- 抢占用 `WHERE consumed_by_task_id IS NULL` 条件更新的行数校验：抢到 0 行说明被别的实例先占，放弃本批（幂等）。
- 补偿逻辑落地为 P2 明确项：定时扫 `consumed_by_task_id IS NULL AND captured_at < now()-interval` 的滞留流量重新入窗口。

### 13.3 🔴 assignment fan-out 部分失败无对账

问题：assignment 无 status、无期望条目数，展开 20 个 task 时第 11 个失败，读时聚合只见 10 个全绿 → 误报整单成功。

修正：assignment 加 `expected_task_count int`；fan-out 尽力展开后，派生状态对比"实际 task 数 vs expected"，缺口标记 `partial`。展开本身尽力而为（单个 task 建失败不回滚已建的），但对账锚点必须有。

### 13.4 🔴 P0/P1 阶段边界重划（半迁移不可运行）

问题：P0 砍 hunter.owner 后，仍在 http_flow 上的 `handleInternalSnap`（`traffic.go:282`）读 `run.OwnerType/OwnerID` 写 flow → P0 末态 internal 流量入库断裂，要等 P1 拆表才恢复。P0 不是独立可运行阶段。

修正：**P0 合并 owner 坍缩 + 流量拆表**（原 P0+P1 合一）。理由：owner→task_id 与 http_flow 拆表在 `handleInternalSnap` 这一点强耦合，拆两阶段必有不可运行窗口。合并后 P0 一步到位：task 合表 + owner→task_id + http_flow 拆 proxy/agent_traffic + ingestor 两条链路改造 + 运维横切（§8.3）。P0 变重但可运行。后续 P1=assignment、P2=passive 聚合器、P3=lead、P4=cron+视图。

### 13.5 🔴 dedup 事实更正 + 改按 task

见 §8.1b：现状是 `UNIQUE(owner_id, dedup_key)`（owner 级，非 host 级）；owner→task 后改 `UNIQUE(task_id, dedup_key)`，语义等价平移。P0 迁移显式重建索引。

### 13.6 🟠 passive 保留 replay 能力

问题：拆表后 replay_flow 只读 agent_traffic（active），passive/traffic-analysis 丧失"改参重发"能力（渗透核心动作：抓到登录请求→改 payload 试注入）。

修正：`replay_flow` 数据源改为**按 task.mode 分流**——active task 读 agent_traffic，passive task 读 proxy_traffic（按 consumed_by_task_id 限定本批）。traffic-analysis 角色保留 replay_flow 工具。

### 13.7 🟠 per-host 限速退避细节

补 §4.3：requeue 用**指数退避 + jitter**（避免同 host 一堆 task 同步空转）、**max retry 上限**、超限进 **DLQ**（asynq 死信）而非无限 requeue。防饥饿。

### 13.8 🟠 lead 注入分 kind 保留，不纯时序截断

补 §7.4：读时截断按 kind 分级——`deadend`/`fact`（负面经验/既成事实，重走代价高）保留更久/更多，`clue`（可疑点，时效性强）可时序滚动淘汰。避免早期关键 deadend 被新 clue 挤出注入窗口导致重走死路。

### 13.9 🟠 编排健壮性（cron / timeout / 幂等 / DLQ）

补成熟编排必备能力：
- **cron concurrencyPolicy**：cron_schedule 加 `concurrency_policy`（allow/forbid/replace）——上次克隆的 assignment 未跑完时，本次触发是并行/跳过/替换。防定时侧同目标叠打（呼应 per-host 限速）。
- **task 墙钟 timeout**：task 加 `deadline_at`（区别于 heartbeat 判活）——agent 一直有心跳也不能无限跑。超 deadline 强制 abort。
- **创建幂等键**：assignment 加幂等键（聚合器用 `host+窗口起始时间戳`、cron 用 `schedule_id+触发时刻`），防聚合器重启/cron 重复触发产生重复 assignment。
- **DLQ**：永久失败的 task 进死信队列，不静默丢弃。
- 不做：任务依赖 DAG（YAGNI，网站间独立；跨 task 漏洞组合走 §8.1 host 视图兜底）。

### 13.10 🟢 保留但加固

- 防自激震荡（internal 不触发分析）：拆表后同构保留；**补显式不变量**："agent 自产流量只进 agent_traffic，绝不进 proxy_traffic"，聚合器/落库处加断言守卫。
- active task 必有 target_host（否则 write_lead 的 host 闭包无值）：P0 约束或明确降级。








