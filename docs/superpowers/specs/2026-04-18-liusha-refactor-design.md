# Liusha AI 渗透测试平台 — 绿地重建设计

**日期**: 2026-04-18
**状态**: Draft v1 (Greenfield)
**负责**: V3teran

---

## 0. 基线 (Greenfield)

**用户已手动清空 `/Users/Xlbula/workspace/programs/go/liusha/` 目录，包含 `.git/` 与所有源代码。** 本方案不继承任何老架构、老模块、老命名、老依赖、老数据模型。唯一保留:

- 独立子项目 `liusha-ui` (位于 `/Users/Xlbula/workspace/programs/typescript/liusha-ui`，不在本仓库下)
- 本设计文档本身 (`docs/superpowers/specs/2026-04-18-liusha-refactor-design.md`)

老项目残留参考价值的部分 (例如 BAC agent、orchestrator、inspection 等 Python 代码、Temporal workflow、旧 Go handler) **全部放弃**，不做增量迁移。新代码以此 spec 为唯一真源从零编写。

---

## 1. 背景与动机

### 1.1 现状问题 (被推翻的老设计)

- 多技术栈拼接: Python agents (LangGraph) + Go API + Temporal workflow
- Agent 决策"线性无意义": 漏洞扫描按固定顺序跑，不会组合推理
- 流量流式异步却被硬塞进批次任务模型
- 前后端容器边界混乱

### 1.2 业界标杆参考

- **XBOW** (商业): 自治 Agent + Attack Graph + 组合漏洞推理
- **Strix** / **CyberStrikeAI** (OSS): ReAct Agent + MCP 工具沙箱
- **HexStrike AI**: 按漏洞类别拆分的 Specialist Agent 集群
- **Burp Suite Enterprise**: Scope 管控、会话重放、被动扫描

### 1.3 重建原则

- **绿地**: 不沿用任何老架构、变量名、数据表
- **单一技术栈**: Go 为主，Python 不参与
- **双模入口**: v1 主打 Proxy 模式，v2 接入 Visual 模式，CTF 模式结构预留
- **组合漏洞推理**: Attack Graph + Combo Reasoner 解决 "单点扫不出、组合才能出" 的漏洞
- **分布式**: 每类服务独立容器，可水平扩展

---

## 2. 核心抽象模型

### 2.1 三级生命周期: Engagement → ProcessingWindow → SpecialistTask

解决 "代理流量是异步流式，但扫描需要批次任务" 的核心矛盾。

```
Engagement (长生命周期, 用户创建)
   │
   ├── ProcessingWindow #1 (60s / 500 flow / 手动触发)
   │      └── SpecialistTask ×N (Asynq 派发给 Specialist Role)
   │
   ├── ProcessingWindow #2
   │      └── SpecialistTask ×N
   │
   └── ... Engagement 归档后入 pgvector 跨项目记忆
```

| 层级 | 生命周期 | 触发方式 | 所属实体 |
|---|---|---|---|
| **Engagement** | 数小时 ~ 数周 | 用户手动创建 (选模式+配 scope) | tenant |
| **ProcessingWindow** | 秒级~分钟级 | 时间窗 60s / 流量窗 500 flow / 手动 | engagement |
| **SpecialistTask** | 秒级~分钟级 | Window 触发或 Combo Reasoner 触发 | window |

**关键决策**:
- Attack Graph 绑定到 `engagement_id`，**不随 window/task 重置**，单调增长
- Engagement 结束 → 归档快照 + 图摘要 ingest 进 pgvector
- Proxy 模式用 Window 切片，Visual/CTF 模式整个 Engagement 一个隐式 Window

### 2.2 Attack Graph (DAG)

**有向无环图**。节点 5 类，边 4 类。

#### Node Kinds

| Kind | 含义 | 示例 |
|---|---|---|
| `Surface` | 攻击面 (发现的 URL/endpoint/参数/服务) | `POST /api/upload?type=avatar` |
| `Hypothesis` | 漏洞假设 (orchestrator 产生) | "此 endpoint 可能存在文件上传漏洞" |
| `Finding` | 已验证的单点漏洞 | "确认存在任意后缀文件上传" |
| `ExploitChain` | 组合攻击链 (Combo Reasoner 产生) | "Upload + LFI → RCE" |
| `Evidence` | 不可变证据 (请求/响应/载荷) | `{request, response, payload_hash}` |

`ExploitChain` 是一等节点，可以再参与更大的 chain (支持 "组合+组合=组合"，`max_chain_depth=4`)。

#### Edge Kinds

| Kind | 语义 |
|---|---|
| `finding` | Surface/Hypothesis → Finding (验证成功) |
| `chains_to` | Finding → ExploitChain 或 Chain → Chain |
| `evidences` | Finding/Chain → Evidence |
| `enables` | Finding → Hypothesis (已有漏洞催生新假设) |

#### 去重与蒸馏 (防图膨胀)

- **URL 模板化**: `/user/1`、`/user/2` → 同一 Surface 节点 `GET /user/{id}`
- **热冷分层**: 近 7 天节点在 Redis，历史节点 Postgres
- **周期蒸馏**: Engagement 结束时合并同类节点为 `SummaryNode`
- **向量去重**: `Finding.Vector` 余弦相似度 > 0.95 视为重复

### 2.3 ModeProfile 模式

三模式 (Proxy / Visual / CTF) 的统一抽象。每个 Mode 实现三个接口:

```go
type Perception interface {
    Observe(ctx context.Context, target Target) (Observation, error)
}

type ActionSpace interface {
    AvailableActions(obs Observation) []Action
    Execute(ctx context.Context, action Action) (ActionResult, error)
}

type StopPolicy interface {
    ShouldStop(state LoopState) (bool, string)
}

type ModeProfile struct {
    Name       string
    Perception Perception
    Actions    ActionSpace
    Stop       StopPolicy
}
```

| Mode | Perception | ActionSpace | StopPolicy |
|---|---|---|---|
| Proxy | 解析 HTTP flow | payload 替换重放、主动发包、调 recon 工具 | Window 结束 / 命中 Finding / 超步数 |
| Visual | 截屏+DOM 解析 | 点击、填表、截图、重放 | 页面遍历完 / 无新 Surface |
| CTF | fetch 页面/附件 | shell 命令、exploit 脚本 | Flag 拿到 / 超时 |

---

## 3. Agent 体系

### 3.1 Role 设计哲学

**Role 不按模式分，按 kill-chain 阶段 + 漏洞类别分**。模式差异由 ModeProfile 承担。

### 3.2 Phase 1 Role 清单 (v1)

| Role | 类别 | 用途 |
|---|---|---|
| `orchestrator` | 决策层 | 从 Surface 产生 Hypothesis、派发 Specialist |
| `recon_web` | 侦察层 | 子域/目录/技术栈指纹、产生 Surface |
| `sqli_hunter` | 注入类 | SQL 注入验证+利用 |
| `file_access_hunter` | 文件类 | LFI/RFI/路径穿越 |
| `file_upload_hunter` | 文件类 | 任意文件上传 |
| `auth_hunter` | 鉴权类 | JWT/Session/BAC |
| `combo_specialist` | 组合层 | Combo Reasoner 触发，验证组合攻击链 |

### 3.3 Phase 2 扩展 Role (v1.1+)

- 注入扩展: `xss_hunter`, `ssti_hunter`, `cmd_injection_hunter`, `xxe_hunter`
- 请求伪造: `ssrf_hunter`, `csrf_hunter`
- 反序列化: `deser_hunter`
- 信息泄露: `info_disclosure_hunter`
- 基础设施: `network_scanner`, `port_hunter`

### 3.4 Role Builder DSL

```go
var SQLIHunter = role.New("sqli_hunter").
    Model(model.Sonnet46).
    FallbackModels(model.DeepSeekV3, model.QwenMax).
    Prompt(embed.Load("prompts/sqli_hunter.md")).
    Tools(
        tools.SQLMap,
        tools.PayloadsSQLi,
        tools.HTTPClient,
    ).
    Skills(
        skills.ErrorBasedSQLi,
        skills.BooleanBlindSQLi,
        skills.TimeBlindSQLi,
    ).
    StopPolicy(
        policy.MaxSteps(15),
        policy.OnFinding(severity.High),
        policy.OnBudget(money.USD(2.0)),
    ).
    Build()
```

### 3.5 Specialist 执行循环 (ReAct)

```
loop:
  observe  → 从 graph 拉上下文
  think    → LLM 生成 action (tool_use)
  act      → 调 tool (via tool-executor / MCP)
  write    → 结果写回 graph (新 Node + Edge)
  check    → StopPolicy.ShouldStop() ? 退出 : 继续
```

### 3.6 Combo Reasoner

独立常驻 goroutine，监听 graph 变化事件:

```go
type ComboReasoner struct {
    patterns []ComboPattern
    graph    GraphStore
}

func (r *ComboReasoner) OnFindingAdded(engagementID uuid.UUID, newFinding Node) {
    candidates := r.matchPatterns(engagementID, newFinding)
    for _, c := range candidates {
        r.spawnComboSpecialist(c)
    }
}
```

**Reasoner 只做图模式匹配**，LLM 推理由 `combo_specialist` Role 承担 (ReAct 循环验证组合)。

**Phase 1 Combo Patterns**:
- `UPLOAD_LFI_RCE`: 任意上传 + LFI → 上传 shell + include 执行
- `SSRF_METADATA_CREDS`: SSRF + 云环境 → 读 `169.254.169.254` 拿凭证
- `JWT_NONE_PRIVESC`: JWT alg=none + 用户可控 sub → 越权

---

## 4. 容器拓扑 (v1: 7 容器)

```
┌──────────────────────────────────────────────────────────┐
│  liusha-ui (外部独立部署，不在本 compose)                   │
└──────────────────────────────────────────────────────────┘
           │ HTTP/WS
           ▼
┌──────────┐   ┌──────────────┐   ┌──────────────────┐
│   api    │──▶│ agent-worker │──▶│  tool-executor   │
│  (Gin)   │   │ (Eino+Asynq) │   │ (docker.sock)    │
└──────────┘   └──────────────┘   └──────────────────┘
     │                │                    │
     │                │                    ▼
     │                │            ephemeral tool container
     │                │            (sqlmap/nuclei/...)
     ▼                ▼
┌──────────┐   ┌──────────────┐
│  redis   │   │  postgres    │   ┌─────────┐
│ (热层)    │   │  (冷+向量)    │   │  proxy  │
└──────────┘   └──────────────┘   │(Proxify)│
                                  └─────────┘
                                       │
                                       ▼
                               (被测目标流量)

┌────────────┐
│ asynqmon   │ (内部运维)
└────────────┘
```

| # | 容器 | 镜像 | 职责 |
|---|---|---|---|
| 1 | `api` | `liusha/api` (Go/Gin) | REST+WS、前端静态 `go:embed` 兜底 |
| 2 | `agent-worker` | `liusha/worker` (Go/Eino) | 所有 Role 合一、消费 Asynq |
| 3 | `tool-executor` | `liusha/tool-executor` (Go) | MCP 调度 + `docker.sock` 拉临时工具容器 |
| 4 | `proxy` | `liusha/proxy` (基于 Proxify) | HTTPS MITM、flow 吐到 Redis 队列 |
| 5 | `redis` | `redis:7` | Asynq 队列 + 热缓存 + 流量热层 |
| 6 | `postgres` | `pgvector/pgvector:pg16` | 冷存储 + 向量 |
| 7 | `asynqmon` | `hibiken/asynqmon` | 运维监控 |

**安全边界**: 只有 `tool-executor` 挂 `docker.sock`，其他容器拿不到。Specialist 被 prompt injection 也无法直接拉容器。

---

## 5. 工具沙箱: tool-executor + DooD

### 5.1 执行流程

```
agent-worker → MCP call "sqlmap" → tool-executor
                                        │
                                        ▼
                             docker.sock: run
                               image: liusha/vuln-tools:latest
                               cmd:   sqlmap -u {target} ...
                               rm:    true
                               net:   pentest_isolated
                                        │
                                        ▼
                             stdout/stderr → agent-worker
```

### 5.2 vuln-tools 镜像分类

| 分类 | 工具 |
|---|---|
| 网络扫描 | nmap, masscan, rustscan |
| Web 指纹/爬 | httpx, subfinder, amass, feroxbuster, katana |
| 漏洞模板 | nuclei, wpscan, wafw00f |
| 注入 | sqlmap, dalfox, xsser, commix, tplmap |
| 文件类 | dirsearch, gobuster, dotdotpwn |
| 鉴权 | jwt_tool, hashcat, john |
| SSRF/XXE | gopherus, ssrfmap |
| 反序列化 | ysoserial, marshalsec |

**v1 先打 `vuln-tools:web` 单镜像**，按需拆 `vuln-tools:net` / `vuln-tools:auth`。

### 5.3 MCP Server 抽象

```go
type ToolServer interface {
    ListTools() []ToolSpec
    Call(ctx context.Context, name string, args json.RawMessage) (ToolResult, error)
}
```

单个 tool-executor 进程实现 `ToolServer`，对 agent-worker 暴露统一 MCP 协议。

---

## 6. 代理与流量

### 6.1 选型: Proxify (projectdiscovery)

理由: Go 原生、支持 flow 输出 JSON、支持 match-replace 重放、社区活跃。

### 6.2 流量流水线 (2 层)

```
Proxify flow → Redis stream (热, 1h) → Window 触发时批量 flush
                                            │
                                            ▼
                                   Postgres 冷存 (永久, body > 32KB 截断)
                                            │
                                            ▼
                                   Window 内 orchestrator 消费
                                   → 产生 Surface + Hypothesis Node
```

### 6.3 Scope 管控

```go
type ScopePolicy interface {
    InScope(url string) bool
    Violation(url string) *ScopeViolation
}
```

**三层强制**:
1. Proxy 层: out-of-scope 直接透传不落库
2. Agent 层: Specialist action 前过 ScopePolicy 校验
3. Tool 层: tool-executor 启动容器前校验 target 参数

---

## 7. 数据模型

### 7.1 Postgres Schema

```sql
CREATE TABLE engagement (
    id           uuid PRIMARY KEY,
    tenant_id    uuid NOT NULL,
    mode         text NOT NULL CHECK (mode IN ('proxy','visual','ctf')),
    scope        jsonb NOT NULL,
    status       text NOT NULL DEFAULT 'active',
    goal         text,
    started_at   timestamptz NOT NULL DEFAULT now(),
    ended_at     timestamptz,
    meta         jsonb
);

CREATE TABLE processing_window (
    id             uuid PRIMARY KEY,
    engagement_id  uuid NOT NULL REFERENCES engagement(id),
    trigger        text NOT NULL,
    flow_count     int,
    started_at     timestamptz NOT NULL,
    ended_at       timestamptz
);

CREATE TABLE specialist_task (
    id              uuid PRIMARY KEY,
    window_id       uuid NOT NULL REFERENCES processing_window(id),
    role            text NOT NULL,
    status          text NOT NULL,
    input_node_ids  uuid[],
    output_node_ids uuid[],
    cost_usd        numeric(10,4),
    started_at      timestamptz,
    ended_at        timestamptz
);

CREATE TABLE graph_node (
    id             uuid PRIMARY KEY,
    engagement_id  uuid NOT NULL REFERENCES engagement(id),
    kind           text NOT NULL,
    payload        jsonb NOT NULL,
    vector         vector(1536),
    dedup_key      text,
    created_at     timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX ON graph_node (engagement_id, kind, created_at DESC);
CREATE INDEX ON graph_node USING hnsw (vector vector_cosine_ops);
CREATE UNIQUE INDEX ON graph_node (engagement_id, dedup_key) WHERE dedup_key IS NOT NULL;

CREATE TABLE graph_edge (
    id            uuid PRIMARY KEY,
    engagement_id uuid NOT NULL,
    src_id        uuid NOT NULL REFERENCES graph_node(id),
    dst_id        uuid NOT NULL REFERENCES graph_node(id),
    kind          text NOT NULL,
    created_at    timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX ON graph_edge (engagement_id, src_id, kind);
CREATE INDEX ON graph_edge (engagement_id, dst_id, kind);

CREATE TABLE traffic_log (
    id             uuid,
    engagement_id  uuid NOT NULL,
    ts             timestamptz NOT NULL,
    method         text,
    url            text,
    req_headers    jsonb,
    req_body       bytea,
    resp_status    int,
    resp_headers   jsonb,
    resp_body      bytea,
    body_truncated bool DEFAULT false,
    PRIMARY KEY (id, ts)
) PARTITION BY RANGE (ts);
```

### 7.2 Redis Keyspace

```
asynq:*                     Asynq 队列
traffic:hot:{engagement}    Redis Stream, 1h TTL
graph:hot:{engagement}      近 7 天节点缓存
rate:{role}:{target}        速率限流
lock:engagement:{id}        Window 切片互斥
```

---

## 8. 项目目录结构 (空仓从零搭建)

```
liusha/
├── cmd/
│   ├── api/               main.go (Gin + 前端 embed)
│   ├── worker/            main.go (Eino + Asynq consumer)
│   ├── tool-executor/     main.go (MCP server + docker client)
│   └── proxy/             main.go (Proxify wrapper)
├── internal/
│   ├── agent/
│   │   ├── role/          role 定义 (Go 构造器)
│   │   ├── mode/          Proxy/Visual/CTF ModeProfile
│   │   ├── memory/        pgvector 检索层
│   │   └── runtime/       ReAct 循环引擎
│   ├── graph/
│   │   ├── model/         Node/Edge 类型
│   │   ├── store/         Postgres + Redis 双层
│   │   └── combo/         ComboReasoner + patterns
│   ├── tool/              MCP client/server
│   ├── proxy/             Proxify 集成
│   ├── scope/             ScopePolicy 实现
│   ├── ratelimit/         Redis 令牌桶
│   ├── api/               HTTP handlers
│   └── store/             Postgres/Redis 访问层
├── pkg/mcp/               公开 MCP 协议
├── roles/                 role prompt markdown (go:embed)
├── skills/                skill markdown (go:embed)
├── patterns/              combo pattern 定义
├── images/
│   └── vuln-tools/        Dockerfile + tool inventory
├── config/
│   ├── config.yaml
│   ├── rate_limit.yaml
│   └── roles.yaml
├── deployments/
│   └── docker-compose.yml
├── docs/
│   └── superpowers/specs/ (本文件)
├── web/dist/              liusha-ui 构建产物 (go:embed, 不提交到 git)
├── go.mod
├── go.sum
├── .gitignore
├── .env.example
├── Makefile
└── README.md
```

---

## 9. LLM 多 Provider 策略

### 9.1 抽象层: Eino ChatModel

Go 生态里最接近 Python litellm 的是 **CloudWeGo Eino**，原生支持:
- Anthropic / OpenAI / Gemini / Ollama / DeepSeek / Qwen
- Tool calling 统一抽象
- 失败回退链 (primary → fallback)

### 9.2 模型分层策略

| 场景 | 推荐 |
|---|---|
| 复杂推理 (orchestrator, combo_specialist) | Sonnet 4.6 primary, DeepSeek-V3 fallback |
| 常规 Specialist (sqli/lfi/upload/auth) | Sonnet 4.6 primary, Qwen-Max fallback |
| 轻量分类/指纹 (recon 辅助) | Haiku 4.5 primary, Qwen-Turbo fallback |

Provider 选择通过 `config/config.yaml` 覆盖。

---

## 10. 前端集成

**前端独立项目**: `/Users/Xlbula/workspace/programs/typescript/liusha-ui`
**栈**: Vue 3 + Vite + AntD Vue + Pinia + echarts + G6 (DAG 渲染)

### 10.1 需要新增的 API 模块

- `src/api/engagement.ts` — create/list/end/archive
- `src/api/window.ts` — list windows of engagement
- `src/api/specialist.ts` — list tasks
- `src/api/graph.ts` — query graph by engagement, subgraph by node

**旧 API 模块 (task, vulnerability, worker, inspection, benign, worker-log, service-node, decision, traffic...) 全部弃用**，不保留向后兼容。liusha-ui 需要并行改造以对齐新后端。

### 10.2 部署

- 开发期: 前端独立 `vite dev`，API 走 CORS
- 生产期: `vue-tsc && vite build` 产出到 `liusha/web/dist/`，由 `api` 容器 `go:embed` 兜底
- **决定: 不要 Caddy/edge 容器**

---

## 11. 可观测性 (v1 最小集)

- `asynqmon` 看队列
- Postgres 查 engagement/window/task 生命周期
- 结构化日志 (zerolog) 输出到 stdout
- v1.1 计划接 Langfuse

---

## 12. 配置与 Secrets

- `.env.local` / `.env.prod`: LLM API key、Postgres/Redis DSN
- `config/config.yaml`: 模型路由、role 开关、Window 切片参数
- **不允许硬编码 secret**，启动期 `fail-fast` 校验

---

## 13. 取舍清单 (明确拒绝)

| 拒绝 | 理由 |
|---|---|
| Temporal | 过重、批次模型不匹配流式代理 |
| Python agents + LangGraph | 跨栈维护成本高，统一 Go |
| go-mitmproxy | 社区活跃度不如 Proxify |
| Caido | 偏桌面端，不适合容器化 |
| River | Asynq 与 Redis 已配套，避免再引一种 DB |
| Caddy/edge 容器 | 前端独立部署，api 自带静态 embed 足够 |
| Verification Lane 独立车道 | 过度工程，Specialist 自己做 end-to-end |
| NDJSON 温层 | 2 层已够，3 层复杂度不成比例 |
| 每流量一个任务 | 换成 Window 批次语义 |
| 老 Python agent 代码移植 | 全部推翻，不做 incremental migration |
| 老数据库 schema 迁移 | 全新建库，旧表不保留 |
| 老 API handler / DTO / 变量名 | 全部丢弃，按新模型从零命名 |

---

## 14. 实施阶段

### v1 (MVP, greenfield)
- 空仓初始化 `go mod init github.com/<owner>/liusha`
- 7 容器 docker-compose 跑起来
- Phase 1 Roles (7 个) + 3 个 Combo Pattern
- Proxy Mode 端到端: Proxify 拦流量 → Redis → Window 切片 → orchestrator → Specialist → Graph
- liusha-ui 改造 engagement/window/graph API，老视图下线

### v1.1
- 扩展 Specialist Role (xss/ssti/cmd_injection/ssrf/csrf 等)
- Langfuse 可观测接入
- 更多 Combo Pattern

### v2
- Visual Mode 接入 (Playwright + 视觉识别 Role)
- Cross-Engagement Memory RAG 完整化

### v3
- CTF Mode
- 分布式多 agent-worker 负载均衡

---

## 15. 已知延后项 (用户确认 v1 先不处理)

- Replay Safety / 破坏性动作护栏 (SafetyGate)
- Human-in-the-Loop 审批闸门
- LLM 可观测 (Langfuse 容器)
- LLM 结构化输出校验 + self-repair
- Tool Sandbox 出网隔离 (iptables + pentest_isolated network)
- Evidence 可复现 (payload_hash + replay_script)
- 成本治理 (per-engagement budget cap)
- 多租户 RLS / key 前缀
- Graph 查询性能索引调优
- Combo Pattern 评分过滤
- Engagement 完成判定
- 报告生成 (`report_composer` role)
- Model Evaluation benchmark
- ClickHouse 流量日志替代
- Qdrant 向量替代

---

## 16. 术语表

- **Engagement**: 一次渗透活动的顶层容器
- **ProcessingWindow**: Engagement 下的批次切片
- **SpecialistTask**: 单次 Role 执行的 Asynq 任务
- **Attack Graph**: Engagement 级别的 DAG，存储所有节点和边
- **ModeProfile**: 模式抽象 (Perception/ActionSpace/StopPolicy)
- **Role**: 具备特定技能的 Agent 角色
- **Combo Reasoner**: 图模式匹配器，不做 LLM 推理
- **combo_specialist**: 验证组合链的 LLM Role
- **DooD**: Docker-outside-of-Docker，通过挂 `docker.sock` 在宿主拉临时容器

---

*本设计文档为 greenfield v1 Draft，待用户 review 后进入 writing-plans 阶段。*
