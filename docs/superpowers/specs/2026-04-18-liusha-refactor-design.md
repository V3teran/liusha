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
- **HexStrike AI**: 按漏洞类别拆分的 Hunter Agent 集群
- **Burp Suite Enterprise**: Scope 管控、会话重放、被动扫描

### 1.3 重建原则

- **绿地**: 不沿用任何老架构、变量名、数据表
- **单一技术栈**: Go 为主，Python 不参与
- **多模入口**: v1 主打 Proxy 模式，v2 接入 Browser 模式 (浏览器自动化 + 视觉模型)，v3 接入 CTF 模式
- **组合漏洞推理**: Attack Graph + Chain Matcher 解决 "单点扫不出、组合才能出" 的漏洞
- **分布式**: 有状态组件 (postgres/redis/proxy) 独立容器；无状态服务 (`agent-worker` / `mcp-server`) 通过多副本消费 Asynq 队列实现水平扩展，不按 Role 物理拆分容器

---

## 2. 核心抽象模型

### 2.1 三级生命周期: Engagement → AnalysisWindow → AgentTask

解决 "代理流量是异步流式，但扫描需要批次任务" 的核心矛盾。

```
Engagement (顶层测试容器, 生命周期由模式决定)
   │
   ├── AnalysisWindow #1 (60s / 500 flow / 手动触发)
   │      └── AgentTask ×N (Asynq 派发给任意 Role)
   │
   ├── AnalysisWindow #2
   │      └── AgentTask ×N
   │
   └── ... Engagement 归档后入 pgvector 跨项目记忆
```

| 层级 | 生命周期 | 触发方式 | 所属实体 |
|---|---|---|---|
| **Engagement** | 由模式决定 (Proxy: 数小时~数周; Browser: 单次会话; CTF: 单挑战) | 用户手动创建 (选模式+配 scope) | tenant |
| **AnalysisWindow** | 秒级~分钟级 | 时间窗 60s / 流量窗 500 flow / 手动 | engagement |
| **AgentTask** | 秒级~分钟级 | Window 触发或 Chain Matcher 触发 | window |

**关键决策**:
- Attack Graph 绑定到 `engagement_id`，**不随 window/task 重置**，单调增长
- Engagement 结束 → 归档快照 + 图摘要 ingest 进 pgvector
- Proxy 模式用 Window 切片，Browser/CTF 模式整个 Engagement 一个隐式 Window

### 2.2 Attack Graph (DAG)

**有向无环图**。节点 5 类，边 4 类。

#### Node Kinds

| Kind | 含义 | 示例 |
|---|---|---|
| `Surface` | 攻击面 (发现的 URL/endpoint/参数/服务) | `POST /api/upload?type=avatar` |
| `Hypothesis` | 漏洞假设 (planner 产生) | "此 endpoint 可能存在文件上传漏洞" |
| `Finding` | 已验证的单点漏洞 | "确认存在任意后缀文件上传" |
| `ExploitChain` | 组合攻击链 (Chain Matcher 产生, `chain_verifier` 验证) | "Upload + LFI → RCE" |
| `Evidence` | 不可变证据 (请求/响应/载荷) | `{request, response, payload_hash}` |

`ExploitChain` 是一等节点，可以再参与更大的 chain (支持 "组合+组合=组合"，`max_chain_depth=6`)。

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

三模式 (Proxy / Browser / CTF) 的统一抽象。每个 Mode 实现三个接口:

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
| Browser | 截屏 (vision model) + DOM 解析 | 点击、填表、截图、重放 | 页面遍历完 / 无新 Surface |
| CTF | fetch 页面/附件 | shell 命令、exploit 脚本 | Flag 拿到 / 超时 |

---

## 3. Agent 体系

### 3.1 Role 设计哲学

**Role 不按模式分，按 kill-chain 阶段 + 漏洞类别分**。模式差异由 ModeProfile 承担。

### 3.2 Phase 1 Role 清单 (v1, 共 8 个)

| Role | 类别 | 用途 |
|---|---|---|
| `planner` | 决策层 | 从 Surface 产生 Hypothesis、派发 Hunter (业界通用术语, 不沿用旧 `orchestrator`) |
| `web_recon` | 侦察层 | 子域/目录/技术栈指纹、产生 Surface |
| `sqli_hunter` | 注入类 | SQL 注入验证+利用 |
| `lfi_hunter` | 文件类 | LFI/RFI/路径穿越 |
| `upload_hunter` | 文件类 | 任意文件上传 |
| `auth_hunter` | 身份认证 (AuthN) | JWT/Session/口令/MFA 绕过 (OWASP A07) |
| `access_control_hunter` | 授权 (AuthZ) | IDOR/BAC/越权 (OWASP A01) |
| `chain_verifier` | 组合层 | Chain Matcher 触发，LLM 验证组合攻击链 (exploit chain) |

### 3.3 Phase 2 扩展 Role (v1.1+)

命名统一为漏洞缩写 + `_hunter`；侦察类用 `_scanner`：

- 注入扩展: `xss_hunter`, `ssti_hunter`, `cmdi_hunter`, `xxe_hunter`
- 请求伪造: `ssrf_hunter`, `csrf_hunter`
- 反序列化: `deser_hunter`
- 信息泄露: `infoleak_hunter`
- 基础设施: `network_scanner`, `port_scanner`

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
        policy.OnBudget(money.USDMicros(2_000_000)), // 整数 micro-USD，避免浮点金钱
    ).
    Build()
```

### 3.5 Agent 执行循环 (ReAct)

对齐 ReAct 论文 (Yao et al., 2022) 的标准三步法:

```
loop:
  Think    → LLM 接收 graph 最新上下文，输出 thought + action (tool_use)
  Act      → 执行 action (via mcp-server / MCP JSON-RPC)
  Observe  → 接收 tool result 作为 observation，并写回 graph (新 Node + Edge)

退出条件: StopPolicy.ShouldStop()
```

上下文加载隐含在 Think 的 prompt 构造阶段；graph 持久化作为 Observe 的副作用发生。

### 3.6 Chain Matcher

独立常驻 goroutine，监听 graph 变化事件。**不做 LLM 推理，只做图模式匹配**；LLM 验证由 `chain_verifier` Role 承担。

```go
type ChainMatcher struct {
    patterns []ChainPattern
    graph    GraphStore
}

func (m *ChainMatcher) OnFindingObserved(engagementID uuid.UUID, newFinding Node) {
    candidates := m.matchPatterns(engagementID, newFinding)
    for _, c := range candidates {
        m.enqueueChainVerifier(c) // 对齐 Asynq `Client.Enqueue` 官方语义
    }
}
```

**Phase 1 Chain Patterns**:
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
│   api    │──▶│ agent-worker │──▶│   mcp-server     │
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
| 1 | `api` | `liusha/api` (Go/Gin) | HTTP + WS、前端静态 `go:embed` 兜底 |
| 2 | `agent-worker` | `liusha/agent-worker` (Go/Eino) | 所有 Role 合一、消费 Asynq，多副本横向扩展 |
| 3 | `mcp-server` | `liusha/mcp-server` (Go) | 暴露标准 MCP (JSON-RPC) 协议给 `agent-worker`；内部经 `docker.sock` 拉临时工具容器 (DooD) |
| 4 | `proxy` | `liusha/proxy` (基于 Proxify) | HTTPS MITM、flow 吐到 Redis 队列 |
| 5 | `redis` | `redis:7` | Asynq 队列 + 热缓存 + 流量热层 |
| 6 | `postgres` | `pgvector/pgvector:pg16` | 冷存储 + 向量 |
| 7 | `asynqmon` | `hibiken/asynqmon` | 运维监控 |

**安全边界**: 只有 `mcp-server` 挂 `docker.sock`，其他容器拿不到。Agent 被 prompt injection 也无法直接拉容器。

---

## 5. 工具沙箱: mcp-server + DooD

### 5.1 执行流程

```
agent-worker → MCP JSON-RPC "tools/call" → mcp-server
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
// 对齐 MCP 规范: tools/list + tools/call (JSON-RPC 2.0)
type MCPServer interface {
    ListTools(ctx context.Context) ([]ToolSpec, error)
    CallTool(ctx context.Context, name string, args json.RawMessage) (ToolResult, error)
}
```

单个 `mcp-server` 进程实现 `MCPServer`，对 `agent-worker` 暴露标准 MCP 协议 (JSON-RPC 2.0 over stdio 或 HTTP SSE)，工具目录与实现解耦，未来可增量接入外部 MCP 生态 (如官方 filesystem/github server)。

---

## 6. 代理与流量

### 6.1 选型: Proxify (projectdiscovery)

理由: Go 原生、支持 flow 输出 JSON、支持 match-replace 重放、社区活跃。

### 6.2 流量流水线 (2 层)

```
Proxify flow → Redis stream (热, 1h) → AnalysisWindow 触发时批量 flush
                                            │
                                            ▼
                                   Postgres 冷存 (永久, body > 32KB 截断)
                                            │
                                            ▼
                                   Window 内 planner 消费
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
2. Agent 层: Role action 前过 ScopePolicy 校验
3. Tool 层: `mcp-server` 启动容器前校验 target 参数

---

## 7. 数据模型

### 7.1 Postgres Schema

```sql
-- 注: 所有金额字段使用整数 micro-USD (1e-6 USD)，避免浮点金钱反模式
-- 注: 所有 status 字段加 CHECK 约束，状态机非法转移在应用层用 version 乐观锁拒绝

CREATE TABLE engagement (
    id           uuid PRIMARY KEY,
    tenant_id    uuid NOT NULL,
    mode         text NOT NULL CHECK (mode IN ('proxy','browser','ctf')),
    scope        jsonb NOT NULL,
    status       text NOT NULL DEFAULT 'active'
                 CHECK (status IN ('active','paused','archived','aborted')),
    objective    text,                              -- 业务术语: Rules of Engagement → Objective
    version      bigint NOT NULL DEFAULT 1,         -- 乐观锁
    started_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now(),
    ended_at     timestamptz,
    meta         jsonb
);

CREATE TABLE analysis_window (
    id             uuid PRIMARY KEY,
    engagement_id  uuid NOT NULL REFERENCES engagement(id),
    trigger_kind   text NOT NULL                    -- trigger 是 SQL 保留字，避开
                   CHECK (trigger_kind IN ('time','flow_count','manual')),
    flow_count     int,
    started_at     timestamptz NOT NULL,
    ended_at       timestamptz
);

CREATE TABLE agent_task (
    id               uuid PRIMARY KEY,
    window_id        uuid NOT NULL REFERENCES analysis_window(id),
    role             text NOT NULL,
    status           text NOT NULL
                     CHECK (status IN ('pending','running','succeeded','failed','cancelled')),
    input_node_ids   uuid[],
    output_node_ids  uuid[],
    cost_micro_usd   bigint NOT NULL DEFAULT 0,    -- 整数 micro-USD
    started_at       timestamptz,
    ended_at         timestamptz
);

CREATE TABLE graph_node (
    id              uuid PRIMARY KEY,
    engagement_id   uuid NOT NULL REFERENCES engagement(id),
    kind            text NOT NULL
                    CHECK (kind IN ('surface','hypothesis','finding','exploit_chain','evidence')),
    payload         jsonb NOT NULL,
    vector          vector(1536),                  -- v1 默认 1536 维 (text-embedding-3-small)
    embedding_model text,                          -- 显式记录 provider/model，禁止跨模型混查
    dedup_key       text,
    created_at      timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX ON graph_node (engagement_id, kind, created_at DESC);
CREATE INDEX ON graph_node USING hnsw (vector vector_cosine_ops);
CREATE UNIQUE INDEX ON graph_node (engagement_id, dedup_key) WHERE dedup_key IS NOT NULL;

CREATE TABLE graph_edge (
    id            uuid PRIMARY KEY,
    engagement_id uuid NOT NULL,
    src_id        uuid NOT NULL REFERENCES graph_node(id),
    dst_id        uuid NOT NULL REFERENCES graph_node(id),
    kind          text NOT NULL
                  CHECK (kind IN ('finding','chains_to','evidences','enables')),
    created_at    timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX ON graph_edge (engagement_id, src_id, kind);
CREATE INDEX ON graph_edge (engagement_id, dst_id, kind);

-- http_flow: 对齐 Proxify / mitmproxy / Wireshark 的业界术语 "flow"
CREATE TABLE http_flow (
    id                uuid,
    engagement_id     uuid NOT NULL,
    ts                timestamptz NOT NULL,
    method            text,
    url               text,
    req_content_type  text,                        -- 回放需要 mime
    req_headers       jsonb,
    req_body          bytea,
    resp_status       int,
    resp_content_type text,
    resp_headers      jsonb,
    resp_body         bytea,
    body_truncated    bool DEFAULT false,
    PRIMARY KEY (id, ts)
) PARTITION BY RANGE (ts);
```

**Embedding 模型策略**: v1 固定一个 embedding 模型 (默认 `text-embedding-3-small`, 1536 维)。换模型必须新建一列或新表，不允许在同一列混存不同模型的向量 (余弦距离无意义)。

### 7.2 Redis Keyspace

所有 key 强制 `liusha:` 前缀，便于多服务共享 Redis 时的命名空间隔离与 `SCAN` 清理。

```
liusha:asynq:*                     Asynq 队列 (Asynq 自身 key 也走前缀)
liusha:flow:hot:{engagement}       Redis Stream, 1h TTL (http_flow 热层)
liusha:graph:hot:{engagement}      近 7 天节点缓存
liusha:rate:{role}:{target}        速率限流
liusha:lock:engagement:{id}        AnalysisWindow 切片互斥
```

---

## 8. 项目目录结构 (空仓从零搭建)

遵循 [golang-standards/project-layout](https://github.com/golang-standards/project-layout):
- `cmd/` 仅放 main 入口；`internal/` 域内代码；`build/` 镜像构建；`deployments/` 编排。
- 每个领域 (graph/agent/flow...) 自带 store 子包，**不再有根 `internal/store/`**。
- prompt/skill/chain pattern markdown 收进 `internal/prompts/` 和 `internal/agent/chain/`，**不污染仓库根**。

```
liusha/
├── cmd/
│   ├── api/                    main.go (Gin + 前端 embed)
│   ├── agent-worker/           main.go (Eino + Asynq consumer)
│   ├── mcp-server/             main.go (MCP JSON-RPC server + docker client)
│   └── proxy/                  main.go (Proxify wrapper)
├── internal/
│   ├── httpapi/                HTTP + WebSocket handlers (REST 覆盖不到 WS, 避开 cmd/api 混淆)
│   ├── agent/
│   │   ├── role/               role Go DSL 定义
│   │   ├── mode/               Proxy/Browser/CTF ModeProfile
│   │   ├── memory/             pgvector 检索层
│   │   ├── runtime/            ReAct 循环引擎
│   │   └── chain/
│   │       ├── matcher.go      ChainMatcher (图模式匹配, 非 LLM)
│   │       └── patterns/       chain pattern Go 定义 (go:embed yaml/md)
│   ├── graph/
│   │   ├── model/              Node/Edge 类型
│   │   └── store/              Postgres + Redis 双层 (graph 自有 store)
│   ├── flow/
│   │   └── store/              http_flow 持久化 (自有 store)
│   ├── engagement/
│   │   └── store/              engagement/window/task 持久化 (自有 store)
│   ├── mcp/                    MCP client (agent-worker 侧) + server (mcp-server 侧) 共享实现
│   ├── proxy/                  Proxify 集成
│   ├── scope/                  ScopePolicy 实现
│   ├── ratelimit/              Redis 令牌桶
│   ├── prompts/
│   │   ├── roles/*.md          role prompt (go:embed)
│   │   └── skills/*.md         skill prompt (go:embed)
│   └── web/
│       └── dist/               liusha-ui 构建产物 (go:embed 要求模块内路径, 不提交到 git)
├── db/
│   └── migrations/             golang-migrate/goose SQL 迁移文件 (版本化, 禁止手改线上 schema)
├── build/
│   └── images/
│       └── vuln-tools/         Dockerfile + tool inventory
├── config/
│   ├── config.yaml             模型路由、AnalysisWindow 参数、role feature flag
│   └── rate_limit.yaml
├── deployments/
│   └── docker-compose.yml
├── docs/
│   └── superpowers/specs/      (本文件)
├── go.mod
├── go.sum
├── .gitignore
├── .env.example
├── Makefile
└── README.md
```

**注**:
- role 的**结构**由 §3.4 Go DSL 定义 (代码即真源)；`config.yaml` 仅承载运行时开关 (启用哪些 role、模型路由覆盖)。**没有 `roles.yaml`** —— role 不是数据，是代码。
- **一切实现放 `internal/`**，含 web 静态资源与 MCP 实现。**无 `pkg/`**：本项目不对外发布 Go 库，不需要公开 API 边界；避免 `pkg/` 沦为内部代码自由区 (project-layout 主流解读)。
- **Schema 迁移统一走 `db/migrations/`**，`CREATE TABLE` 不在应用代码里用字符串拼接，生产变更由 CI 执行 migrate up/down。

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
| 最深推理 (`chain_verifier` 组合链验证、跨 engagement memory 综合) | **Opus 4.7** primary, Sonnet 4.6 fallback, DeepSeek-V3 二级 fallback |
| 常规决策 (`planner`, `sqli/lfi/upload/auth/access_control` Hunter) | **Sonnet 4.6** primary, Qwen-Max fallback |
| 轻量分类/指纹 (`web_recon` 辅助、URL 模板化判定) | **Haiku 4.5** primary, Qwen-Turbo fallback |

Provider 选择通过 `config/config.yaml` 覆盖。

---

## 10. 前端集成

**前端独立项目**: `/Users/Xlbula/workspace/programs/typescript/liusha-ui`
**栈**: Vue 3 + Vite + AntD Vue + Pinia + echarts + G6 (DAG 渲染)

### 10.1 需要新增的 API 模块

避免与 JS 全局 (`window`) 或泛词 (`graph`) 撞名:

- `src/api/engagement.ts` — create/list/end/archive
- `src/api/analysis-window.ts` — list windows of engagement (避开 `window` 全局)
- `src/api/agent-task.ts` — list tasks of window
- `src/api/attack-graph.ts` — query graph by engagement, subgraph by node

**旧 API 模块 (task, vulnerability, worker, inspection, benign, worker-log, service-node, decision, traffic...) 全部弃用**，不保留向后兼容。liusha-ui 需要并行改造以对齐新后端。

### 10.2 部署

- 开发期: 前端独立 `vite dev`，API 走 CORS
- 生产期: `vue-tsc && vite build` 产出到 `liusha/internal/web/dist/`，由 `api` 容器 `go:embed` 兜底
- **决定: 不要 Caddy/edge 容器**

---

## 11. 可观测性 (v1 最小集)

- `asynqmon` 看队列 + 死信队列 (DLQ)
- Postgres 查 engagement/window/task 生命周期
- 结构化日志 (zerolog) 输出到 stdout
- **强制日志字段**: `trace_id` (W3C traceparent 透传, 跨 `api` → `agent-worker` → `mcp-server` → vuln-tools 全链路串联)、`engagement_id`、`window_id`、`task_id`、`role`。无 `trace_id` 的日志在 dev 环境直接 panic
- **Asynq 可靠性**: 指数退避重试 (最多 5 次)，超限进 `retry` 队列人工处理；`UniqueOpt` 去重相同 `(role, input_node_ids_hash)` 任务 30min 内不重复入队
- v1.1 计划接 Langfuse + OpenTelemetry tracing exporter (OTLP)

---

## 12. 配置与 Secrets

- `.env.development` / `.env.production`: LLM API key、Postgres/Redis DSN (对齐 Vite/Next.js/12-Factor 社区惯例)
- `.env.local`: 本地个人覆盖，加入 `.gitignore`，不提交
- `config/config.yaml`: 模型路由、role 开关、AnalysisWindow 切片参数
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
| Verification Lane 独立车道 | 过度工程，Hunter 自己做 end-to-end |
| NDJSON 温层 | 2 层已够，3 层复杂度不成比例 |
| 每流量一个任务 | 换成 AnalysisWindow 批次语义 |
| 老 Python agent 代码移植 | 全部推翻，不做 incremental migration |
| 老数据库 schema 迁移 | 全新建库，旧表不保留 |
| 老 API handler / 响应类型 / 变量名 | 全部丢弃，按新模型从零命名 (不使用 Java 风格的 DTO 概念) |

---

## 14. 实施阶段

### v1 (MVP, greenfield)
- 空仓初始化 `go mod init github.com/<owner>/liusha`
- 7 容器 docker-compose 跑起来
- Phase 1 Roles (8 个) + 3 个 Chain Pattern
- Proxy Mode 端到端: Proxify 拦流量 → Redis → AnalysisWindow 切片 → `planner` → Hunter Role → Graph
- liusha-ui 改造 engagement/window/graph API，老视图下线

### v1.1
- 扩展 Hunter Role (xss/ssti/cmdi/ssrf/csrf 等)
- Langfuse 可观测接入
- 更多 Chain Pattern

### v2
- Browser Mode 接入 (Playwright + Vision Model Role)
- Cross-Engagement Memory RAG 完整化

### v3
- CTF Mode
- 分布式多 `agent-worker` 负载均衡

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
- Chain Pattern 评分过滤
- Engagement 完成判定
- 报告生成 (`report_composer` role)
- Model Evaluation benchmark
- ClickHouse 流量日志替代
- Qdrant 向量替代

---

## 16. 术语表

- **Engagement**: 顶层测试容器，生命周期由模式决定 (Proxy: 数小时~数周; Browser: 单会话; CTF: 单挑战)
- **AnalysisWindow**: Engagement 下的流量分析窗口 (时间/流量数/手动触发)
- **AgentTask**: 单次 Role 执行的 Asynq 任务 (涵盖 planner/recon/hunter/verifier 所有 role)
- **Attack Graph**: Engagement 级别的 DAG，存储所有节点和边
- **ModeProfile**: 模式抽象 (Perception/ActionSpace/StopPolicy)
- **Role**: 具备特定技能的 Agent 角色 (planner/hunter/scanner/verifier)
- **Hunter**: 针对某漏洞类别的 Specialist Role 家族 (命名 `*_hunter`)
- **Scanner**: 侦察类 Role 家族 (命名 `*_scanner` 或 `*_recon`)
- **Chain Matcher**: 图模式匹配器，不做 LLM 推理
- **chain_verifier**: 用 LLM 验证组合链的 Role
- **ChainPattern**: 组合漏洞的图模式定义 (如 `UPLOAD_LFI_RCE`)
- **DooD**: Docker-outside-of-Docker，通过挂 `docker.sock` 在宿主拉临时容器

---

*本设计文档为 greenfield v1 Draft，待用户 review 后进入 writing-plans 阶段。*
