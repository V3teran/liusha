# 命名规范（项目宪法）

> 经 v1.3 三轮命名整改（migration 0017–0019 + 6 次包重命名）后定型。
> 任何新增 skill / 表 / 工具 / 包，**必须**遵守本文档。
> 任何与本文档冲突的旧代码，遇到即修。

---

## TL;DR — 黄金规则 7 条

1. **名实相符**：包名 ≈ 表名 ≈ 主类型名（`internal/finding` ↔ 表 `finding` ↔ 类型 `Finding`）
2. **无 stutter**：`pkg.PkgType` 是 anti-pattern（`traffic.ClassifyTraffic` ❌ → `traffic.Classifier` ✅）
3. **DB 列含义清晰**：外键 `<table>_id`，时间 `*_at`，错误 `error_message`，单位带后缀（`cost_usd`、`latency_ms`）
4. **agentic 抽象稳定**：暴露语义不暴露实现（`agent_run` ✅，不是 `react_run` ❌）
5. **工具名动+宾**：LLM 看到的工具名永远是 `verb_object`（`run_replay` ✅，不是 `replay_matrix` ❌）
6. **Redis key 极简**：无项目前缀，靠 redis db number 隔离；asynq 的 `{}` hash-tag 不要动
7. **YAGNI**：不为假想的未来功能预留字段；除非**今天就有用**或**改动成本极低**

---

## 1. 目录结构

```
liusha/
├── cmd/                    # 二进制入口（每个子目录 = 1 个 main package）
│   ├── api/
│   ├── e2e/
│   ├── proxy/
│   ├── scanner/
│   └── vulnapp/            # e2e 测试靶机（缩写约定：vulnerable app）
├── internal/               # 项目私有包，不暴露给外部 module
├── skills/                 # SKILL.md 集合（CC 风格自动发现）
│   ├── orchestrator/       # 主 ReAct system prompt
│   ├── classify-traffic/   # 内部 prompt（不可 delegate）
│   ├── tooling/            # 工具手册（curl/sqlmap/python3/sh）
│   └── vuln/web/           # 漏洞检测子 ReAct（bac/sqli）
├── db/migrations/          # golang-migrate up/down 对
├── deployments/            # docker-compose
├── config/                 # config.yaml
├── scripts/dev/            # 开发脚本（up/down/run-svc/e2e）
└── docs/                   # 设计文档与本规范
```

### 规则

- **顶层目录单字小写名**：`cmd/`、`internal/`、`skills/`
- **cmd 子目录 = 二进制名**：`cmd/scanner` → 编译为 `scanner`
- **internal 包名单数**（`builder` 不是 `builders`，`tool` 不是 `tools`——例外见下）
- **skills 路径 = skill name**：`skills/vuln/web/bac` → frontmatter `name: vuln/web/bac`

### 例外

- `internal/tools/`：复数，因为下面是多个独立工具子包（`common`/`probe`/`traffic`/`delegate`/`external`），与 `internal/builder` 单数表达"装配工厂"不同
- `skills/tooling/`：复数集合命名空间，含多个独立工具手册

---

## 2. Go 包命名

### 规则

| 维度 | 规则 | ✅ 例 | ❌ 反例 |
|---|---|---|---|
| 风格 | 全小写、单字优先 | `finding`, `lesson`, `flow` | `flowDecision`, `flow_facts` |
| 单复数 | 单数（除非真集合） | `builder`, `worker` | `builders`, `workers` |
| 暴露层 | 名实相符稳定语义 | `agentrun`（agent 抽象） | `reactrun`（暴露 ReAct 实现） |
| 与表对齐 | 包名 ≈ 表名 | 包 `finding` / 表 `finding` | 包 `vulnfinding` / 表 `finding` |
| stutter | 严禁 `pkg.PkgXxx` | `finding.Store` | `vulnfinding.VulnFinding` |

### 已知历史变更（v1.3）

| 旧名 | 新名 | 原因 |
|---|---|---|
| `flowdecision` | `flowfacts` | 表 `flow_decision → flow_facts`；本表只存事实不做决策 |
| `reactrun` | `agentrun` | ReAct 是实现细节，agent 是稳定抽象 |
| `vulnfinding` | `finding` | 系统只有一种 finding，`vuln_` 前缀冗余 |
| `clip` | `clipper` | 单字"clip"语义模糊；clipper 是动作主体 |
| `toolfx` | `toolruntime` | "fx" 后缀含糊 |
| `builders` | `builder` | Go idiom 单数 |

---

## 3. Go 类型/接口/函数命名

### 类型（struct / interface）

- **PascalCase**
- **接口用 `+er` 模式**：`Reader`、`Checker`、`Counter`、`Adder`、`Toucher`、`Filter`、`Generator`、`Observer`
- **Acronym 全大写**：`URL`、`HTTP`、`JSON`、`LLM`、`BAC`、`SQL`（`URLParser` ✅，不是 `UrlParser`）
- **不要 stutter**：`finding.Finding` 在领域内可接受（finding 是核心名词），但 `traffic.ClassifyTraffic` 必须改 `traffic.Classifier`

### 构造函数

- `NewXxx` 标准模式
- 链式注入用 `WithXxx`：`store.WithCounter(c)`
- 工厂返回接口时用 `New<Producer>` + `Produce/Build/Create`

### 方法 receiver 命名

- 短而一致：`s *Store`、`a *Action`、`f *Filter`、`t *Task`
- 同一类型在所有方法用同一个名字

### 例

```go
// ✅ 好
type FindingStore interface { Save(...) }
func NewStore(pool *pgxpool.Pool) *Store
func (s *Store) Save(ctx, f Finding) (Finding, bool, error)

// ❌ 反例
type IFinding interface{...}        // 不要 I 前缀
type FindingManager struct{...}      // "Manager" 通常多余
type FindingFactory struct{...}      // "Factory" 通常多余
type FindingHelper struct{...}       // "Helper" 通常多余
```

---

## 4. 数据库命名

### 表名

- **全小写、snake_case、单数**
- **业务实体单词即可**：`finding`、`engagement`、`agent_run`、`flow_facts`
- **不要前缀**：`vuln_finding` ❌、`host_lesson` ❌

### 列名

| 类别 | 模式 | 例 |
|---|---|---|
| 主键 | `id`（uuid 或 bigserial） | `id` |
| 外键 | `<referenced_table>_id` | `engagement_id`、`agent_run_id`、`source_flow_id` |
| 时间 | `*_at`（timestamptz） | `created_at`、`updated_at`、`ended_at` |
| 计数 | `<thing>_count`（integer） | `flow_count`、`finding_count`、`agent_run_count` |
| 错误 | `error_message`（text） | 不要简写为 `error` |
| 状态 | `status`（text + CHECK） | enum 值用 snake_case：`pending`/`running`/`done` |
| 单位 | 带后缀 | `cost_usd` (numeric)、`latency_ms` (integer) |
| jsonb | 直接列名（无 `_json` 后缀） | `messages`、`result`、`memory_notes` |
| 作用域 | 实义动词 | `target_host`（不是 `scope_host`） |

### 索引/约束命名

- 索引：`<table>_<purpose>` 例：`finding_engagement_idx`、`engagement_active_uniq`
- 主键：`<table>_pkey`（PostgreSQL 默认）
- 外键：`<table>_<column>_fkey` 例：`finding_engagement_id_fkey`
- CHECK：`<table>_<column>_check`

### 历史变更（v1.3 migration 0017-0019）

| 旧 | 新 |
|---|---|
| 表 `flow_decision` | `flow_facts` |
| 表 `react_run` | `agent_run` |
| 表 `vuln_finding` | `finding` |
| 表 `host_lesson` | `lesson` |
| 列 `flow_facts.attack_surfaces` | `param_locations` |
| 列 `flow_facts.required_skills` | （DROP，agentic 整改） |
| 列 `*.task_id` | `agent_run_id`（finding/flow_facts/llm_invocation）|
| 列 `engagement.react_run_count` | `agent_run_count` |
| 列 `engagement.scope_host` | `target_host` |
| 列 `http_flow.ts` | `created_at` |
| 列 `host_lesson.payload` | `lesson.structured_payload` |
| 列 `llm_invocation.role` | `call_purpose` |
| 列 `llm_invocation.error` | `error_message` |
| 列 `llm_invocation.messages_json` / `result_json` | `messages` / `result`（去 `_json` 后缀）|

---

## 5. Redis Key 命名

### 自有 key

```
credentials:<host>      # hash，host 维度凭证
flow_events             # stream，proxy → ingestor 流水
```

### 规则

- **无项目前缀**（不要 `liusha:`）
- **多租户隔离用 redis db number**（`asynq.RedisClientOpt.DB`），不用 key 前缀
- **冒号分层**：`<namespace>:<id>`（`credentials:localhost`）

### asynq 内部 key（不要碰）

```
asynq:queues
asynq:servers
asynq:workers
asynq:{<queue>}:processed     # ← {} 是 Redis Cluster Hash Tag，cluster-safe 设计
asynq:{<queue>}:active        # 同 hash tag → 同 slot → 同事务可写
asynq:servers:{<host:pid:uuid>}
```

`{}` 不是 bug，是故意保留——给未来 Redis Cluster 留余地。

---

## 6. 工具命名（LLM 看到的字符串）

### 规则

- **snake_case 动+宾**：动词 + 名词
- 与 Go 类型名**可以不同**（两层接口）

### 现有工具一览

| 工具 Name() | Go 类型 | 备注 |
|---|---|---|
| `read_state` | `common.ReadState` | 读 engagement memory |
| `take_note` | `common.TakeNote` | 写 memory |
| `write_finding` | `common.WriteFinding` | 写漏洞 finding |
| `write_graph` | `common.WriteGraph` | 写知识图谱 |
| `done` | `common.Done` | 终结 ReAct |
| `classify_traffic` | `traffic.Classifier` | 流量事实提取（曾 `ClassifyTraffic`，stutter 已修） |
| `get_findings` | `traffic.GetFindings` | 复核 engagement findings |
| `delegate` | `delegate.Tool` | 派子 ReAct（曾 `Delegate`，stutter 已修） |
| `fetch_credentials` | `probe.FetchCredentials` | 拉 host 凭证 |
| `run_replay` | `probe.ReplayMatrix` | 多变体重放（Go 类型名保留 Matrix 语义）|
| `check_heuristics` | `probe.HeuristicCheck` | 启发式短路 |
| `compute_similarity` | `probe.ComputeSimilarity` | 响应相似度 |
| `run_command` | `external.RunCommand` | 沙箱容器执行 |

---

## 7. 工具入参 / 出参字段命名

### 规则

- **全 snake_case**
- **位置/类型枚举值统一**：`query/path_param/body_json/body_form/body_xml/body_file`（与 OpenAPI `in:` 对齐）
- **阈值带 `_threshold` 后缀**：`min_threshold`、`high_threshold`
- **超时带 `_seconds` 后缀**：`timeout_seconds`
- **必填用 JSON Schema `required`**

### 已固化的字段

```
flow_id, host, hosts, names, roles, kind, severity, confidence, title, target,
evidence, dedup_key, content, status, command, timeout_seconds, tag, payload,
nodes, edges, from, to, name, mutation, where, field, value, mode, rules,
min_threshold, high_threshold, concurrency, variants, items, type, summary, reason
```

---

## 8. SKILL.md frontmatter

### 强制字段

```yaml
name: vuln/web/bac           # 与目录路径一致；kebab-case 或 path 式
description: |
  多行说明；LLM 看此进 catalog 自动发现
```

### 可选触发元数据（v1.3 引入）

```yaml
requires_auth: true                              # carries_auth=false 流量跳过
applicable_param_locations: [query, body_json]   # 流量 param_locations 必须有交集
```

### 规则

- 不写隐式元数据（builder 决定工具集，不在 frontmatter 重复）
- 新增触发元数据需先动 `internal/skill/card.go::Card` 字段

---

## 9. 枚举值（agent role / status / kind）

### agent_run.role

```
orchestrator    # 主 ReAct（调度员）
hunter          # 子 ReAct（漏洞猎手）
```

### agent_run.status

```
pending → running → done | error | aborted
```

### finding.kind

```
bac.unauthorized_access
bac.vertical_priv_esc
bac.horizontal_priv_esc
sqli.error_based
sqli.boolean_based
sqli.union_based
sqli.time_based
```

格式：`<category>.<subkind>`（点分两级）。新增漏洞类型遵守此模式。

### finding.severity / confidence

```
severity:   info / low / medium / high / critical
confidence: low / medium / high
```

### llm_invocation.call_purpose

```
orchestrator / hunter / observer / classify_traffic / lesson_extract
```

### resource_scope

```
private / public / unknown
```

### param_locations

```
query / path_param / body_json / body_form / body_xml / body_file
```

### graph_node.kind / graph_edge.kind

LLM 自由产出（schema-less），不 enum 约束。常见值参考已落库数据。

---

## 10. asynq queue / task type

```go
QueueOrchestrator = "orchestrator"   // 无 liusha: 前缀
QueueDispatch     = "dispatch"
TaskTypeRun       = "agent.run"      // dot 分隔，避免与 asynq 内部 ":" 层级冲突
```

---

## 11. 配置 / 环境变量

### config.yaml

- 顶层节用单短词：`api`/`postgres`/`llm`/`proxy`/`engagement`/`skills`/`scanner`
- 字段 snake_case：`max_conns`、`window_batch`、`flow_max_request_body`

### 环境变量

- 全部 `LIUSHA_` 前缀
- 二级覆盖路径：`LIUSHA_LLM_DEFAULT_PROVIDER` 覆盖 `llm.default_provider`
- 敏感信息（API key）只走 env，不入 yaml

---

## 12. 加新东西时怎么办

### 加新 skill（如 XSS）

1. 创建 `skills/vuln/web/xss/SKILL.md`
2. frontmatter 写 `name: vuln/web/xss`、按需写 `requires_auth` / `applicable_param_locations`
3. 装 builder 在 `internal/builder/vuln/xss/skill.go`，注册到 `cmd/scanner/main.go`
4. **不需要**改 orchestrator SKILL.md（catalog 自动发现）

### 加新 DB 表

1. 表名单数 + snake_case：如 `incident`
2. Go 包名同名：`internal/incident`
3. 主类型同名（领域名词）：`incident.Incident` 或 `incident.Record`
4. 列名按 §4 规则
5. 写 0020+ migration up/down 对
6. e2e.sh 的 TRUNCATE 清单加上新表

### 加新工具

1. Name() 用 snake_case 动+宾：`extract_<x>`、`run_<y>`
2. Go 类型名领域名词（不必跟 LLM 工具名同名）
3. 入参字段 snake_case，必填进 `required`
4. 注册到 `cmd/scanner/main.go::registerActions` 或 builder

---

## 13. 反模式速查（看到立刻改）

| ❌ 反模式 | ✅ 改成 |
|---|---|
| `package vulnfinding` | `package finding` |
| `traffic.ClassifyTraffic` | `traffic.Classifier` |
| `delegate.Delegate` | `delegate.Tool` |
| 表 `vuln_finding` | 表 `finding` |
| 列 `task_id`（指向 agent_run） | 列 `agent_run_id` |
| 列 `error`（错误消息） | 列 `error_message` |
| 列 `messages_json`（已是 jsonb） | 列 `messages` |
| 列 `ts` | 列 `created_at` |
| 列 `scope_host` | 列 `target_host` |
| Redis key `liusha:flow_events` | Redis key `flow_events` |
| 工具 `replay_matrix`（名词） | 工具 `run_replay`（动+宾） |
| frontmatter `applicable_attack_surfaces` | `applicable_param_locations` |
| LLM 输出 `attack_surfaces` | `param_locations` |
| asynq queue `liusha:orchestrator` | `orchestrator` |
| `manager` / `helper` / `factory` 滥用后缀 | 直接领域名词 |

---

## 14. 变更记录

- **v1.3.0**（2026-05-07）：三轮命名整改完成，本规范定型
  - migration 0017：表名整改（flow_decision/react_run/vuln_finding → flow_facts/agent_run/finding）+ 列 attack_surfaces → param_locations 等
  - migration 0018：跨表外键 task_id → agent_run_id；时间 ts → created_at；error → error_message
  - migration 0019：scope_host → target_host；host_lesson → lesson
  - 6 个包重命名（flowdecision→flowfacts；reactrun→agentrun；vulnfinding→finding；clip→clipper；toolfx→toolruntime；builders→builder）
  - asynq queue 去 `liusha:` 前缀；redis 自有 key 去前缀

---

## 附录 A：当前所有命名对照表

### 9 张业务表

`engagement` / `http_flow` / `flow_facts` / `agent_run` / `finding` / `lesson` / `graph_node` / `graph_edge` / `llm_invocation`

### 28 个 internal 包

`agentrun` / `builder` / `clipper` / `config` / `credential` / `db` / `dbtest` / `engagement` / `filter` / `finding` / `flow` / `flowfacts` / `graph` / `heuristic` / `httpapi` / `ingestor` / `lesson` / `llm` / `llminvocation` / `logx` / `observability` / `proxy` / `react` / `replay` / `skill` / `toolruntime` / `tools/*` / `worker`

### 5 个二进制

`api` / `e2e` / `proxy` / `scanner` / `vulnapp`

### 8 个 SKILL.md

`orchestrator` / `classify-traffic` / `vuln/web/bac` / `vuln/web/sqli` / `tooling/curl` / `tooling/sqlmap` / `tooling/python3` / `tooling/sh`

### 13 个工具

`read_state` / `take_note` / `write_finding` / `write_graph` / `done` / `classify_traffic` / `get_findings` / `delegate` / `fetch_credentials` / `run_replay` / `check_heuristics` / `compute_similarity` / `run_command`
