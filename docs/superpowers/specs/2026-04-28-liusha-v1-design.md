# Liusha v1 设计

**日期**: 2026-04-28（2026-04-29 增补 9 项黑客松借鉴）
**作用域**: v1 = Plan 1（底座）+ Plan 2（代理模式 + BAC），v1.5 = SQLi + 整站模式
**借鉴源**: 参见 `docs/hks2.md` —— 第二届智能渗透黑客松前 10 名 PPT 综合分析；本 spec 已纳入 5 共识 + 6/7/8/11 共 9 项采纳点

---

## 1. 目标

代理模式优先的 AI 渗透测试系统。用户挂代理浏览目标，系统自动检测：

- **BAC 类**（v1 / Plan 2）：未授权访问 / 垂直越权 / 水平越权
- **SQLi 类**（v1.5）：经典参数注入

用户预存活凭证（按 host 索引）。重放时按位置替换原始流量凭证。

**v1 不做**：SQLi 检测、整站模式、多租户认证、Web UI、按漏洞类型拆角色、RAG/向量、非幂等请求保护。

---

## 2. 架构

```
用户浏览器 (HTTP_PROXY=:8888)
    │
    ▼
┌──────────────────────────────────────┐
│ proxify 容器 (projectdiscovery)       │
│  ├─ DSL 过滤 (-match-condition)       │
│  └─ flows.jsonl (共享 volume)         │
└──────────────┬───────────────────────┘
               │ tail -f /data/flows.jsonl
               ▼
┌──────────────────────────────────────┐
│ proxify_consumer (内嵌 agent-worker)  │
│  ├─ engagement.LookupOrCreate(host)   │
│  ├─ flow.Append → http_flow           │
│  └─ slicer.OnFlow (batch=20/30s)      │
└──────────────┬───────────────────────┘
               │
               ▼ Asynq agent:sniffer
┌──────────────────────────────────────┐
│ Sniffer ReAct (主任务)                │
│  read_window → 看可疑 → spawn skill  │
└──────────────┬───────────────────────┘
               │
        ┌──────┴───────┐
        ▼              ▼
    BAC skill     SQLi skill
   (ReAct 7-8步) (ReAct 多步)
        │              │
        ▼              ▼
   write_finding (UNIQUE dedup_key, ON CONFLICT DO UPDATE)
   write_graph
```

**核心抽象**：

- 一份 `AgentRuntime`，所有 skill 都跑 ReAct
- 通用能力（凭证 / 重放 / 启发式 / 相似度）下沉 lib，业务编排在 skill 的 system prompt
- skill 的 system prompt 强约束步骤顺序；Observer Sidecar + LoopDetector + DoneValidator + 守护栏防失控（详见 §4.2 / §6.5）

---

## 3. 数据模型

### 3.1 Postgres 表（一次性 0001_init 建完）

| 表 | 主要字段 | 备注 |
|---|---|---|
| `engagement` | id, tenant_id, mode, scope.host, status, memory_facts jsonb, memory_ideas jsonb, memory_hints jsonb, last_activity_at | (tenant, host) 懒创建，24h 归档；memory 三层见 §3.4 |
| `traffic_window` | id, engagement_id, flows jsonb, status, started_at, closed_at | open/closed/consumed |
| `agent_task` | id, engagement_id, parent_task_id, role, skill, input/budget/result jsonb | spawn 关系 + summary |
| `graph_node` | id, engagement_id, kind, payload jsonb, dedup_key | 部分 UNIQUE 索引 |
| `graph_edge` | id, engagement_id, from_id, to_id, kind, payload | 全字段 UNIQUE |
| `finding` | id, engagement_id, task_id, kind, severity, title, target/evidence jsonb, payload, tool, confidence, **dedup_key** | UNIQUE(engagement_id, dedup_key)；ON CONFLICT DO UPDATE evidence |
| `http_flow` | id, engagement_id, ts, method, url, headers/body jsonb/bytea, body_truncated | proxy 冷层 |
| `llm_call` | id, task_id, engagement_id, provider, model, in/out/cached_tokens, cost_usd numeric(12,6), latency_ms, finish_reason, error | 每次 LLM 一行 |

`finding.kind` 取值：

- `sqli`
- `bac.unauthorized_access` / `bac.vertical_priv_esc` / `bac.horizontal_priv_esc`

`dedup_key` 格式：

- SQLi: `sqli:<host>:<method>:<path>#<param>`
- BAC: `<kind>:<host>:<method>:<path>`

时间统一 `timestamptz` UTC。

### 3.2 Redis 数据

| Key | TTL | 用途 |
|---|---|---|
| `liusha:credential:{host}:{name}` | 用户指定（0=永久） | 活凭证 |
| `liusha:flow:hot:{engagement_id}` | 1h | 流量热层（XADD MAXLEN 1000） |
| Asynq queues `agent:sniffer` / `agent:operator` (v1.5) | - | 任务队列 |

### 3.3 凭证 JSON 格式

```json
{
  "name": "admin",
  "role": "admin",
  "credentials": [
    {"type": "headers", "key": "Cookie", "value": "session=admin_sess_a1b2c3"}
  ]
}
```

`type ∈ {headers, query, body}`。`anonymous` 身份不录入，系统自动提供（空 credentials）。

### 3.4 Memory 三层结构

借鉴黑客松前 10 名共识（详见 `docs/hks2.md`），engagement.memory 拆三层，避免 LLM 自记忆漂移：

| 字段 | 结构 | 写入者 | 读取时机 |
|---|---|---|---|
| `memory_facts` | `{evidence: [{key, content, ts}], boundaries: [{rule, ts}]}` | LLM 通过 `write_fact` action | `read_state` 一次性带入 |
| `memory_ideas` | `{hypotheses: [{direction, status, ts}]}`，status ∈ `pending\|testing\|verified\|failed` | LLM 通过 `write_idea` action | `read_state` 一次性带入 |
| `memory_hints` | `{hints: [{from_skill, content, priority, ts}]}` | 系统：Observer 摘要 / DoneValidator 反馈 / Distill 经验沉淀 | `read_state` 一次性带入；Skill 启动时注入 system prompt |

只追加不修改（追溯性）；超过 100 条按 ts 滚动淘汰最旧。

---

## 4. AgentRuntime

### 4.1 配置

```go
type AgentRuntimeConfig struct {
    Role         Role            // sniffer | operator
    Trigger      Trigger         // EventDriven | OneShot
    Termination  Termination     // InputConsumed | DoneOrBudget
    Budget       Budget          // MaxSteps / MaxTokens / Watchdog
    Actions      *ActionRegistry
    LLM          llm.Generator
    Skill        *skill.Card     // 子任务非 nil
    SystemPrompt string
}
```

### 4.2 ReAct 主循环

```
loop {
    if termination | budget | aborted -> stop
    think := LLM.Generate(system + state)
    action, args := parse(think)
    obs := actions.Execute(ctx, action, args)
    state.Append(thought, action, obs)
    state.Record(action, latency)
    if action == "done" -> stop
}
```

**Observer Sidecar**（替代原 Reflexion，借鉴黑客松第 1 名 + 第 5 名）：
独立上下文的轻量评估器，主循环每 N=5 步调一次：
- **输入**：最近 5 轮 `(action_name, args, obs_summary)` 滑动窗 + `read_state()` 摘要
- **输出 Verdict**：`{decision: keep_going | steer_with_hint | abort_low_value, hint?: string}`
- `steer_with_hint` → 把 hint 注入下一轮 user message（"提示：你在 X 类 endpoint 已重复尝试 Y 次，建议改向 Z"）
- `abort_low_value` → 直接终止任务，task.result.terminate_by = `observer_abort`
- 走 `light_provider`（见 §8.4），成本 ≪ 主 LLM
- Observer 写入 `memory_hints`，便于跨 skill 复用

**LoopDetector**（细粒度代码识别，独立于 Observer）：
四维特征 `hash(action_name + args_sha1 + caller_skill + step_idx_modN)` 滑动窗，连续 ≥3 次同 hash → 直接 `abort_low_value`，不依赖 LLM 自评。

子任务（含 BAC、SQLi）走同一循环。skill 的 system prompt 写明步骤顺序，LLM 跟着执行。

### 4.3 守护栏

| 项 | 阈值 |
|---|---|
| MaxSteps | sniffer 30 / operator 80 / 子任务由 skill 收紧（10-15） |
| MaxTokens | sniffer 50K / operator 200K / 子任务 30K |
| Watchdog | 单步 60s |
| Memory 压缩 | 80% 预算触发；走 light_provider，仅压缩 obs 历史，memory_facts/ideas/hints 不动 |
| Observer 评估 | 每 5 步触发一次（subtask 每 3 步） |
| LoopDetector | 连续 3 次相同四维 hash → abort_low_value |
| DoneValidator | done action 调用前必经；不通过则注入 user message 继续 |
| Spawn depth | ≤ 1 |
| Spawn 在飞 | ≤ 10 per parent，≤ 20 per engagement |
| Engagement abort | 每步检查 status，非 active 立即 break |

---

## 5. 角色 + Skill

| Role | Trigger | Termination | 主要 Action |
|---|---|---|---|
| `sniffer` | EventDriven | InputConsumed | read_window / read_state / write_fact / write_idea / write_hint / load_skill / spawn_subtask / done |
| `operator` (v1.5) | OneShot | DoneOrBudget | + browser.* |

子任务继承父角色。

### 5.1 SKILL.md frontmatter

```yaml
---
name: vuln/web/bac
description: 访问控制失效（未授权/垂直/水平越权）
applies_to: [{role: sniffer}]
budget: {max_steps: 10, max_tokens: 15000}
required_actions: [...]                 # loader 启动校验
done_validator: bac_v1                  # 注册到 internal/agent/actions/done_validator/registry.go
cognitive_map: docs/skills/bac/cognitive_map.md  # 6 槽位认知地图（见下）
---
（正文：步骤指引、触发线索、判定标准、常见坑、cognitive map 片段）
```

**Skill 认知地图模板**（借鉴黑客松第 6/8 名 Path Map，统一存 `docs/skills/_template/cognitive_map.md`）：

| 槽位 | 含义 | BAC 示例 |
|---|---|---|
| 1. 检测点 | 哪类请求 / 流量特征 | 含 uid/orderid 参数 / 路径 含 /admin /sys /:id |
| 2. 类型矩阵 | 漏洞子类型 | unauthorized_access / vertical / horizontal |
| 3. 能力矩阵 | 已注册的 Action | fetch_credentials / replay_multi_identity / heuristic_check / compute_similarity |
| 4. 有效 Payload | 替换什么 | Cookie / Authorization / Body 中位置敏感字段 |
| 5. 判定规则 | similarity 阈值 / heuristic 命中条件 | threshold=0.3；any of all_denied / all_empty / all_auth_error |
| 6. 失败方向 | 已知不该再走 | all_similar / heuristic_skip / scope_block / dedup_hit |

未来加 Skill（SQLi/XSS）按同模板填即可，Loader 启动时校验 6 槽位齐全。

### 5.2 BAC skill 步骤指引（写在 SKILL.md 正文，作为 system prompt 注入）

```
你是 BAC 检测器,严格按以下顺序行动,不允许跳步：

0. read_state() → 读 memory_hints（含上次 distill 的经验提示），写一条 write_idea(direction=本次目标 endpoint, status=pending)
1. fetch_credentials(host) → 拿全部身份(含 anonymous)
2. replay_multi_identity(strategy=bac, concurrency=5) → N 份响应
   write_fact(evidence, "endpoint X 的 N 身份重放结果摘要")
3. heuristic_check(rules=[all_denied, all_empty, all_auth_error])
   - 命中 → write_fact(boundary, "heuristic 命中 RULE")；write_idea(status=failed)；done(reason=heuristic_skip)
4. compute_similarity(threshold=0.3) → 相似度矩阵
   - 全部低于阈值 → write_fact(boundary, "similarity 全低于阈值")；write_idea(status=failed)；done(reason=all_similar)
5. 基于相似度矩阵判定漏洞类型(不允许直接看原始响应):
   - anonymous 能成功访问 → bac.unauthorized_access (最高优先级)
   - 低权限用户访问 admin/sys 路径 → bac.vertical_priv_esc
   - 多个同级用户访问相同私有资源 → bac.horizontal_priv_esc
6. 命中 → write_finding(dedup_key=...) + write_graph → write_idea(status=verified) → done(reason=finding_written)
7. 否则（未命中任何类型，但已过完 step 1-5）→ write_idea(status=failed) → done(reason=no_pattern_match)

**done 系统校验**（不通过则被注入 user message 继续）：
- 必须已调用：fetch_credentials + replay_multi_identity + heuristic_check + compute_similarity 全套
- done.reason 必须 ∈ {finding_written, all_similar, heuristic_skip, no_pattern_match}
- reason=finding_written 时 finding 表必须存在对应 dedup_key
```

### 5.3 Sniffer 触发哪个 skill 的线索

| 信号 | spawn |
|---|---|
| URL 含 `/admin/`、`/sys/`，或参数含 `?uid=`、`/:user_id/` | `vuln/web/bac` |
| URL 参数为数字且拼到 `?id=`、`?page=` 或上下文像 `ORDER BY` | `vuln/web/sqli`（v1.5） |
| 错误回显含 `syntax error near`、`unclosed quotation mark` 等 | `vuln/web/sqli`（v1.5，高优先） |

---

## 6. Action 空间

### 6.1 共享

| Action | 类型 | 说明 |
|---|---|---|
| `read_state` | det | 读 engagement memory 三层（facts/ideas/hints）+ Observer 摘要 |
| `write_fact(category, content)` | det | category ∈ `evidence \| boundary`；追加到 memory_facts |
| `write_idea(direction, status)` | det | status ∈ `pending\|testing\|verified\|failed`；追加到 memory_ideas |
| `write_hint(content, priority)` | det | 追加到 memory_hints；同时供 LLM / Observer / DoneValidator / Distill 使用 |
| `write_finding` | det | UNIQUE(engagement_id, dedup_key)，ON CONFLICT DO UPDATE evidence；写库后触发 Distill（见 §8.6） |
| `write_graph` | det | node + edges |
| `load_skill(path)` | det | 拼 skill 进 system prompt |
| `spawn_subtask(skill, budget)` | det | 异步同角色子任务 |
| `done(reason?)` | meta | **系统层校验**：调用前过 DoneValidator（每 Skill 注册一份），不通过则注入"未达终止条件 [missing: ...]，继续工作"user message，不真正退出 |

### 6.2 Sniffer 专属

| Action | 说明 |
|---|---|
| `read_window(window_id)` | 读 traffic_window，自动 MarkConsumed |

### 6.3 BAC 专属

| Action | 类型 | 说明 |
|---|---|---|
| `fetch_credentials(host)` | det | 从 Redis 拿全部 identity（含 anonymous） |
| `replay_multi_identity(strategy, concurrency)` | det | N 身份并发重放 |
| `heuristic_check(rules)` | det | 命中即提前 skip |
| `compute_similarity(algorithm, threshold)` | det | 相似度矩阵 |

### 6.4 SQLi 专属（v1.5）

| Action | 说明 |
|---|---|
| `docker_run(image, cmd, env, timeout)` | image 白名单（`liusha/vuln-tools:latest`） |

v1 不实装；v1.5 加 sqli skill 时再开。

### 6.5 Action 中间件（统一管线）

借鉴黑客松第 2/4/5 名"工具结果落盘 + 引用"模式：

| 中间件 | 触发 | 行为 |
|---|---|---|
| `result_compress` | Action 返回值 size > 2KB | 落盘 `engagement-store/<eid>/<action>-<seq>.txt`；result 替换为 `{path, size, sha256, snippet[:400]}` 引用 |
| `loop_detect` | 每次 Action 调用前 | 计算四维 hash 写滑动窗；连续 ≥3 次同 hash → 拒绝执行并触发 `abort_low_value` |
| `done_validate` | Action 名 = `done` | 调用对应 Skill 注册的 DoneValidator；不通过则把 missing 列表注入下一轮 user message，本轮不算 done |

中间件按顺序执行：`loop_detect` → `done_validate`（仅 done） → 业务执行 → `result_compress`。

### 6.6 Explorer 前置侦察原则（强约束）

借鉴黑客松第 2 名"不烧 LLM token 在确定性任务"：
- 一切可代码确定性完成的工作（流量解析、候选 endpoint 生成、Headers 模板填充、凭证替换位置定位）必须由 Action 内部代码完成，不通过 LLM 推理
- LLM 仅在"语义判断"（候选去重、参数语义识别、相似度阈值边界判定、漏洞类型归类）介入
- **禁止**把 HTTP 流量原文 / 大型工具输出直接塞 LLM context；必须先经 explorer-style 代码侦察压缩到 ≤ 2KB（违反则被 §6.5 `result_compress` 拦截）

---

## 7. 通用 lib

### 7.1 `internal/credential/` — 活凭证 CRUD

```go
type Identity struct {
    Name        string
    Role        string
    Credentials []Credential
}

type Credential struct{ Type, Key, Value string }

type Provider interface {
    GetIdentitiesByHost(ctx, host) ([]Identity, error)
    BatchSave(ctx, map[string][]Identity, ttl int) error
    List(ctx, host) (map[string][]Identity, error)
    Delete(ctx, host) error
}
```

Redis 实现，Provider 接口可后续换（如 Vault）。

### 7.2 `internal/replay/` — 凭证替换 + 并发重放

```go
type Engine struct{ /* http.Client */ }

// 替换 raw 中的指定位置（type+key）凭证为 identity 的对应值
func (e *Engine) ReplayWithIdentity(ctx, raw RawRequest, id Identity) (Response, error)

// 并发重放 N 身份
func (e *Engine) ReplayMultiIdentity(ctx, raw, ids []Identity, concurrency int) ([]Response, error)
```

### 7.3 `internal/heuristic/` — 通用规则 + 相似度

```go
type Rule func(responses []Response) (skip bool, reason string)

var (
    AllDeniedByStatus  Rule  // 全部 4xx/5xx
    AllEmptyResponse   Rule  // 全部 body 为空 / "{}" / "[]"
    AllAuthError       Rule  // 全部含鉴权关键词（默认 25 个中英词，yaml 可扩）
)

func StructuralSimilarity(a, b string) float64
```

### 7.4 流量过滤 — 走 proxify DSL

不在 liusha 写 filter 责任链。流量过滤交给 proxify 自己的 DSL（`-match-condition`），liusha 只负责消费已过滤的 flows。

启动示例（docker-compose）：

```yaml
proxify:
  image: projectdiscovery/proxify:latest
  command:
    - "-output"
    - "/data/flows.jsonl"
    - "-match-condition"
    - "request.method != 'OPTIONS' && request.method != 'HEAD' && !request.path.endsWith('.css') && !request.path.endsWith('.js') && !request.path.endsWith('.png')"
  ports: ["8888:8888"]
  volumes:
    - liusha-proxify:/data
```

改完重启 proxify 容器即可。v1.5 再考虑热更新。

---

## 8. LLM Multi-Provider

### 8.1 配置

```yaml
llm:
  default_provider: deepseek
  vision_provider: anthropic

providers:
  deepseek:  {base_url, default_model: deepseek-chat,    api_key_env: DEEPSEEK_API_KEY,  max_tokens: 4096}
  anthropic: {base_url, default_model: claude-sonnet-4-6, vision_model: claude-haiku-4-5, api_key_env: ANTHROPIC_API_KEY, max_tokens: 8192}
  openai:    {... api_key_env: OPENAI_API_KEY}    # v1 配置预留，代码 stub
  moonshot:  {... api_key_env: MOONSHOT_API_KEY}  # 同上
  qwen:      {... api_key_env: QWEN_API_KEY}      # 同上
```

| Provider | v1 实装 | adapter |
|---|---|---|
| deepseek | ✅ 主 LLM | `eino-ext/components/model/deepseek` |
| anthropic | ✅ vision 备用 | `eino-ext/components/model/claude` |
| openai | ✅ | `eino-ext/components/model/openai` |
| moonshot | ✅ | `eino-ext/components/model/openai` + `base_url=https://api.moonshot.cn/v1`（OpenAI-compatible） |
| qwen | ✅ | `eino-ext/components/model/qwen` |

全部走 eino-ext 真实 adapter，无 stub。

### 8.2 装饰器 + 单价表

```go
func Instrument(g Generator, sink CallSink, meta CallMeta, pricing Pricing) Generator
```

每次调用埋点写 `llm_call`。`internal/observability/pricing.go` 内置单价表：

| Provider | Model | Input ($/1M) | Output ($/1M) | Cache 折扣 |
|---|---|---|---|---|
| deepseek | deepseek-chat | 0.27 | 1.10 | 0.10× |
| anthropic | claude-sonnet-4-6 | 3.00 | 15.00 | 0.30× |
| anthropic | claude-haiku-4-5 | 1.00 | 5.00 | 0.30× |

### 8.3 任务结束写 result

```json
{"terminate_by":"done","total_steps":12,"total_tokens":18234,
 "total_cost_usd":0.0234,
 "action_stats":{"docker_run":{"count":2,"avg_ms":3200}}}
```

`terminate_by` 取值：`done | budget | watchdog | observer_abort | loop_detector_abort | engagement_aborted | error`

### 8.4 多模型路由（Router）

借鉴黑客松第 1/4/10 名"轻重任务分流"。

```yaml
llm:
  default_provider: deepseek          # 主 ReAct（重）
  light_provider:   anthropic_haiku   # Observer / Compaction / Distill（轻）
  vision_provider:  anthropic         # 截图判断（v1.5）
  fallback_provider: openai           # 429/529 切换

  routes:
    react.main:  default_provider
    observer:    light_provider
    compaction:  light_provider
    distill:     light_provider
    vision:      vision_provider
```

**调用方式**：`llm.For("observer").Chat(ctx, ...)`，Router 自动路由 + Instrument 埋点。
**Fallback 触发**：retry 中间件捕获 429/529 → 切 `fallback_provider` 重试一次 → 仍失败则向上抛错。

### 8.5 错误码退避表

借鉴黑客松第 10 名"L2 异常自动恢复"。

| 错误码 / 类型 | 重试 | 退避 | Fallback |
|---|---|---|---|
| 429 (rate limit) | 3 次 | 指数退避 1s/4s/16s | 第 4 次切 fallback_provider |
| 529 (overloaded) | 1 次 | 立即 | 第 2 次切 fallback_provider |
| 500/502/503/504 | 2 次 | 1s/4s | 否 |
| 网络超时 / connection reset | 2 次 | 1s/3s | 否 |
| 其他 4xx | 0 次 | - | 否（直接抛） |

实现位置：`internal/agent/llm/retry.go`，作为 Generator 装饰器，套在 Instrument 外层。

### 8.6 经验自蒸馏（Distill）

借鉴黑客松第 2 名"成功后回写知识库自我进化"，最小落地：

**触发**：每次 `write_finding` 命中（实际写入或 ON CONFLICT DO UPDATE）→ 异步触发一次 distill。

**输入**：`{skill, replay_args_summary, heuristic_signal, similarity_score, finding.kind, finding.dedup_key}`

**走 light_provider** 生成 ≤ 200 字 hint，写入当前 engagement 的 `memory_hints`：

```json
{"from_skill": "vuln/web/bac",
 "content": "管理员路径 /admin/* 在 anonymous 401 + body_hint 含 'unauthorized' → 高概率提前 heuristic_skip 类似 endpoint",
 "priority": 7,
 "ts": "2026-04-29T..."}
```

**消费**：BAC Skill 启动时 `read_state()` 读到 hints，注入 system prompt 顶部。

---

## 9. HTTP API

所有路由（除 healthz）校验 `X-API-Key` header（env `LIUSHA_API_KEY`）。

| 方法 | 路径 | 说明 |
|---|---|---|
| POST | `/credential/batch` | 批量录入 host 凭证 |
| GET | `/credential?host=X` | 列出凭证 |
| DELETE | `/credential` | 删凭证（按 host） |
| POST | `/engagement/{id}/abort` | 终止运行中的 engagement |
| POST | `/engagement/browser` | v1.5 整站模式触发 |
| GET | `/healthz` | 健康检查（无 auth） |

---

## 10. 端到端流程

### 10.1 启动准备

```bash
make up && make migrate
# 起 proxify + agent-worker + api（profile=e2e）
LIUSHA_DEEPSEEK_API_KEY=$KEY \
  docker compose -f deployments/docker-compose.yml --profile e2e up -d --build
# 一次性录入凭证
curl -X POST http://localhost:8080/credential/batch \
  -H "X-API-Key: $LIUSHA_API_KEY" \
  -d @credentials.json
```

`credentials.json` 含 vulnapp 三个 session（admin/test/m233241）。

### 10.2 BAC e2e（Plan 2，全自动）

```bash
make e2e-bac
```

程序通过代理 curl vulnapp 8 个接口，等待 finding 落库。

期望落 5 条 finding：

| 接口 | kind | violating_identities |
|---|---|---|
| GET /api/user/info?uid=X | bac.horizontal_priv_esc | test, m233241 |
| GET /api/order/:oid | bac.horizontal_priv_esc | test, m233241 |
| POST /api/order/cancel | bac.horizontal_priv_esc | test, m233241 |
| GET /api/admin/users | bac.vertical_priv_esc | test, m233241 |
| POST /api/admin/user/delete | bac.unauthorized_access | anonymous + ... |

### 10.3 SQLi e2e（v1.5）

留位，v1.5 加 sqli skill 时再开。

---

## 11. 成功指标

| 指标 | 验证 SQL / 命令 | 预期 |
|---|---|---|
| BAC 5 finding | `SELECT count(*) FROM finding WHERE kind LIKE 'bac.%'` | ≥5 |
| BAC 三类齐全 | `SELECT DISTINCT kind FROM finding WHERE kind LIKE 'bac.%'` | 含 3 个 subtype |
| 单 Runtime | `grep -c 'AgentRuntimeConfig{' internal/agent/runtime/*.go` | 1 |
| Memory 三层均有写入 | `SELECT count(*) FROM engagement WHERE memory_facts != '{}'::jsonb AND memory_ideas != '{}'::jsonb AND memory_hints != '{}'::jsonb` | >0 |
| Observer 触发记录 | `SELECT count(*) FROM agent_task WHERE result->>'terminate_by' = 'observer_abort'` | ≥0（功能存在即可） |
| Distill 写入 hint | `SELECT memory_hints FROM engagement WHERE id = <eid>` 含至少一条 hint | true |
| 多模型路由生效 | `SELECT DISTINCT provider FROM llm_call WHERE task_id = <obs_task>` | 含 light_provider |
| 成本可查 | `SELECT SUM(cost_usd) FROM llm_call` | >0 |
| Skill 扩展 | 加 XSS skill 不改 Go 代码 → `go test ./internal/skill/...` | 通过 |

v1.5 验收：SQLi finding `SELECT count(*) FROM finding WHERE kind='sqli' AND confidence='verified'` ≥1。

---

## 12. Plan 拆分

### v1
| Plan | 名称 | 核心交付 |
|---|---|---|
| 1 | foundation | 0001_init schema、internal/{config,logx,engagement,window,task,graph,flow,finding,credential,replay,heuristic,agent/{runtime,action,llm(5 provider via eino-ext)},observability,worker,httpapi}、cmd/{api,agent-worker,vulnapp} |
| 2 | proxify + BAC | proxify 容器 + `internal/proxify_consumer`、sniffer 装配、`vuln/web/bac` skill（含步骤指引）、vulnapp e2e 跑出 5 finding |

### v1.5（v1 完成后再写 plan）
- SQLi：`vuln/web/sqli` skill、docker_run sqlmap、DVWA e2e
- 整站模式：operator + browser.* actions

---

## 13. 风险与缓解

借鉴黑客松踩坑经验（详见 `docs/hks2.md` §一.第 8 名警示）。

| 风险 | 来源 | 缓解 |
|---|---|---|
| Observer 误判（false-positive 中断） | 第 1 名借鉴 | Observer 仅 hint 不强制；abort_low_value 需 LoopDetector 同时命中 |
| Distill 写入低质量 hint 污染下次 | 自身 | hint priority < 5 不注入 system prompt；100 条按 ts 滚动 |
| Done 永远校验失败导致死循环 | 自身 | DoneValidator 注入 ≤ 3 次后强制放行，记录 task.result.terminate_by = "done_force" |
| 多模型路由 provider 全挂 | 黑客松 10 名 | retry → fallback → 仍失败抛 task 错误，不阻塞 engagement，由调度层重启 |
| LLM fuzz 提交接口被限流 | 黑客松 8 名 | 我方 `LIUSHA_API_KEY` 强校验 + 服务端 finding 写库走 dedup，重复提交无副作用 |
| 容器爆内存 / containerd 挂掉 | 黑客松 8 名 | v1 不启 browser；v1.5 单 engagement Chromium 数量上限 = 2 |
