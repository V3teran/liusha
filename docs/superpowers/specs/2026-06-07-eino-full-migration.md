# liusha → eino 全面迁移蓝图（原生重构，非薄封装）

状态：规划中（2026-06-07 起）
决策依据：两个最小 spike 实测通过（`spike/eino-tracker` passive、`spike/eino-deep` active 并发），eino 在 liusha 走得通。
框架选型：eino（CloudWeGo/字节）—— 国内生态、国产 provider 原生、概念开箱对应、依赖轻（39 vs ADK 93）。
**原则（用户拍板）**：不考虑迁移成本；需要就大刀阔斧改；**绝不图省事做薄封装**；原生拥抱 eino。

---

## 0. 核心原则

- **替换，不包装**：删 `internal/react/runtime.go`（手写 ReAct 循环），换成 eino ADK `Runner` + `ChatModelAgent`/`deep`。删 `internal/llm` 手写 provider 适配，换 eino-ext。
- **域逻辑保留、重新表达**：swarm 角色、vuln skills、凭证协议、http_flow/identity 模型、sitemap、工具的真实行为 —— 这些是 liusha 的价值，**不丢**，只是落到 eino 原语上。
- **框架接管通用件**：agent 循环、provider、中间件、流式、callbacks、tool schema —— 交给 eino。
- **不抄 CyberStrikeAI**：照 eino 官方 API 自己设计（spike 已验证这条可行）。

## 1. 现状（被替换的部分）

| liusha 现有 | 行数 | 去向 |
|---|---|---|
| `internal/react/runtime.go`（手写 ReAct + Inspector/OnNoToolCall/PreDoneCheck/压缩）| 359 | **删** → eino ADK Runner + middleware |
| `internal/llm/`（Generator + anthropic/openai_compat/factory/router/retry/pool/instrument）| ~3000 | **大改** → eino-ext ChatModel + callbacks |
| `internal/tools/**`（Actions: Name/Description/ParametersJSON/Execute）| — | **重构** → eino `tool.BaseTool`（InferTool 自动 schema）|
| `internal/subtask/`（spawn_striker + asynq 派 striker）| — | **删分布式派活** → eino `deep` 进程内编排 |
| `internal/builder/hunter/skill.go`（装配 react.Config）| — | **重写** → 装配 eino agent |

## 2. 目标架构（原生 eino）

```
scanner worker
  ├─ passive 一条流量 → eino ChatModelAgent（tracker）
  └─ active 一个 run   → eino deep（commander）+ sub-agents（striker）
                          全进程内并发 + 共享同一 sandbox 容器（本就共享）
                          asynq 派 striker 删除 → deep task 委派替代

每个 agent：
  Model        = eino-ext openai ChatModel（指国产 provider，OpenAI 兼容）
                 ★ 每 hunter 一个独立实例（spike 实测：共享会被串行化）
  Tools        = liusha 工具 → eino tool.BaseTool（InferTool 自动 schema）+ MCP 工具
  Middleware   = swarm 语义映射（见 §4）
  Callbacks    = tool_invocation / llm_invocation 落库（替 interceptor/instrument）
```

## 3. 组件逐项映射

### 3.1 Provider（`internal/llm` → eino-ext）
- `openai.NewChatModel(ctx, &ChatModelConfig{APIKey, BaseURL, Model})` 接所有 OpenAI 兼容 provider（DeepSeek/小米/通义/GLM/Moonshot/豆包-Ark）。
- anthropic：用 eino-ext claude adapter（或 OpenAI 兼容层），迁移时确认覆盖。
- **保留 liusha 的多 provider 路由语义**（按 role 选 provider）：一个工厂按 role 返回对应 `ChatModel`，底层 eino-ext，不再手写协议适配。
- **项目特有修复**（openai_compat 截图拆 user message、xiaomi 严格）→ 确认 eino-ext 是否覆盖；不覆盖则用自定义 HTTPClient 包装（eino-ext 支持注入）。

### 3.2 Agent 循环（`react.Run` → ADK）
- **tracker**（passive 单 agent）→ `adk.NewChatModelAgent` + `adk.NewRunner`。spike `eino-tracker` 已验证。
- **commander+striker**（active）→ `deep.New`（commander）+ `[]adk.Agent`（strikers 作 sub-agent）。spike `eino-deep` 已验证并发。
- **MaxSteps/MaxTokens 预算** → `MaxIterations` + middleware 内自管 token。

### 3.3 工具（Actions → eino tool）
- 每个工具：入参 struct（jsonschema tag）+ invoke 函数 → `utils.InferTool(name, desc, fn)`。**schema 自动生成,省掉手写 ParametersJSON**。
- 注入式 OwnerID/Host（防 LLM 串库）：闭包捕获,不进 LLM 可见参数（同现状语义）。
- `done` 工具 → eino `Exit` tool 或 `ReturnDirectly`。
- MCP 工具 → eino MCP 接入,与原生工具并列挂上。

### 3.4 分布式 swarm → 进程内 deep
- active：commander deep 一轮多 `task` 委派 striker → 并发（spike 实测 window 重叠）。
- asynq 派 striker 删除。**asynq 派 tracker 的活**（被动流量分发）→ 进程内 worker pool 消费 redis `flow_events` 流（proxy 仍独立进程则保留这条流）。
- 共享 sandbox 容器不变（commander+striker 本就共用一个）。

## 4. swarm 语义 → eino 原生映射（关键，别丢）

| liusha 控制语义 | eino 落点 |
|---|---|
| **PreDoneCheck**（commander 不能在 striker 没完时 done）| deep 的 `task` 委派**天然阻塞等子代理返回**——"等 striker"内建,不需独立闸。done 前自检（重读 finding/覆盖度）→ commander instruction + 可选 middleware |
| **Inspector**（每 5 步 LLM 判官注 hint,不强中断）| `ChatModelAgentMiddleware`（model 调用前查 state.Messages,注 hint）|
| **OnNoToolCall**（想收口但有 striker running）| 大部分被 deep 的 task 收集替代;残留逻辑入 middleware |
| **历史/图压缩** | summarization-style `ChatModelAgentMiddleware`（model 调用前压缩 messages）|
| **凭证两路协议**（read/write_credential）| 工具不变（包成 eino tool）;redis 凭证池保留或迁 Postgres |
| **identity/tool 戳**（http_flow）| 与 agent 框架正交,捕获/ingest 层不动 |
| **per-hunter 独立 model 实例** ★ | **spike 抓到的硬坑**：每个 ChatModelAgent/sub-agent 用独立 `ChatModel`,共享会被串行化(BindTools 改内部状态)。装配处铁律 |

## 5. 可观测（interceptor/instrument → eino callbacks）
- eino `callbacks` 切面 hook model/tool 调用前后 → 写 `llm_invocation` / `tool_invocation` 表（保留 liusha 落库可观测,比 eino 默认更适合）。
- 替换 `internal/toolruntime/interceptor` + `internal/llm/instrument`。

## 6. 不变的部分（域 + 基建）
- **域数据/逻辑**：finding/lesson/note/sitemap/http_flow 模型、vuln skills、prompt（角色段）、BAC 方法论、凭证协议措辞。
- **基建**：sandbox/launcher、browser-svc.py、mitm-capture.py、proxy/ingest、Postgres（必留）。
- **prompt**：tracker/commander/striker 的 instruction 直接作 eino agent 的 `Instruction`（措辞复用,机制换）。

## 7. 分阶段计划（每阶段独立 e2e 可验 / 可回退）

| 阶段 | 内容 | 验收 |
|---|---|---|
| **P0**（done）| 两个 spike 验证 eino passive/active 可行 + 抓 per-model 坑 | ✅ 已完成 |
| **P1** provider | eino-ext ChatModel 工厂（按 role 选 provider，国产全覆盖 + anthropic 确认 + 项目修复保住）| 各 provider curl/单测 |
| **P2** 工具 | liusha 工具 → eino tool.BaseTool（InferTool）;OwnerID 注入;done→Exit;callbacks 落库 | 工具单测 + 1 个 agent 调通 |
| **P3** tracker | passive → ChatModelAgent + Runner;Inspector/压缩 → middleware | e2e passive 各 profile PASS |
| **P4** active | commander+striker → deep;**per-hunter 独立 model**;asynq 派 striker 删 | e2e active:bac PASS + 并发验证 |
| **P5** 被动分发 | asynq 派 tracker → 进程内 worker pool 消费 flow_events | passive 全链路 PASS |
| **P6** 增量 | MCP / 流式（接 viewer）/ 攻击链 / RAG（knowledge 包,pgvector 或 eino retriever）| 各自独立验 |

## 8. 风险 / 决策点
- **版本锁定**：eino@v0.8.13 + eino-ext openai@v0.1.13 钉死（v0.x 抖动;eino ADK 子包是 alpha,升级前必回归）。
- **eino ADK alpha**：deep/ChatModelAgent 接口可能变,P3/P4 前确认版本、锁定。
- **anthropic 覆盖**：P1 确认 eino-ext claude 是否够用,否则保留一条手写或走兼容。
- **Redis 去留**：迁移后 asynq 可删;flow_events 流看 proxy 是否合并;credentials/notes 可迁 Postgres（独立决定,不与本迁移捆绑）。
- **每 hunter 独立 model 的资源开销**：active 多 striker = 多 model 实例,确认无连接/内存问题（实测）。

## 9. 已验证产物（P0）
- `spike/eino-tracker/`：passive ChatModelAgent + 小米 mimo + 自动 schema，一次跑通。
- `spike/eino-deep/`：active deep + 2 striker 并发（独立 model 后 window 重叠），抓到 per-model 坑。
- 两者独立 module、throwaway，未碰主代码。正式迁移时作 P3/P4 的参照（自己写的,非抄）。
