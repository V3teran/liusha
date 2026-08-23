# liusha → eino 全面迁移蓝图（原生重构，非薄封装）

> 〔2026-06-19 修订〕web/viewer 前端已迁出至独立仓 liusha-ui；本文原 viewer 相关表述已按现状更新，当时决策原文见 git 历史。

状态：规划中（2026-06-07 起）
决策依据：两个最小 spike 实测通过（`spike/eino-traffic-analysis` passive、`spike/eino-deep` active 并发），eino 在 liusha 走得通。
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
| `internal/subtask/`（spawn_exploitation + asynq 派 exploitation）| — | **删分布式派活** → eino `deep` 进程内编排 |
| `internal/builder/agent/skill.go`（装配 react.Config）| — | **重写** → 装配 eino agent |

## 2. 目标架构（原生 eino）

```
scanner worker
  ├─ passive 一条流量 → eino ChatModelAgent（traffic-analysis）
  └─ active 一个 run   → eino deep（planner）+ sub-agents（exploitation）
                          全进程内并发 + 共享同一 sandbox 容器（本就共享）
                          asynq 派 exploitation 删除 → deep task 委派替代

每个 agent：
  Model        = eino-ext openai ChatModel（指国产 provider，OpenAI 兼容）
                 ★ 每 agent 一个独立实例（spike 实测：共享会被串行化）
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
- **traffic-analysis**（passive 单 agent）→ `adk.NewChatModelAgent` + `adk.NewRunner`。spike `eino-traffic-analysis` 已验证。
- **planner+exploitation**（active）→ `deep.New`（planner）+ `[]adk.Agent`（exploitations 作 sub-agent）。spike `eino-deep` 已验证并发。
- **MaxSteps/MaxTokens 预算** → `MaxIterations` + middleware 内自管 token。

### 3.3 工具（Actions → eino tool）
- 每个工具：入参 struct（jsonschema tag）+ invoke 函数 → `utils.InferTool(name, desc, fn)`。**schema 自动生成,省掉手写 ParametersJSON**。
- 注入式 OwnerID/Host（防 LLM 串库）：闭包捕获,不进 LLM 可见参数（同现状语义）。
- `done` 工具 → eino `Exit` tool 或 `ReturnDirectly`。
- MCP 工具 → eino MCP 接入,与原生工具并列挂上。

### 3.4 分布式 swarm → 进程内 deep
- active：planner deep 一轮多 `task` 委派 exploitation → 并发（spike 实测 window 重叠）。
- asynq 派 exploitation 删除。**asynq 派 traffic-analysis 的活**（被动流量分发）→ 进程内 worker pool 消费 redis `flow_events` 流（proxy 仍独立进程则保留这条流）。
- 共享 sandbox 容器不变（planner+exploitation 本就共用一个）。

## 4. swarm 语义 → eino 原生映射（关键，别丢）

| liusha 控制语义 | eino 落点 |
|---|---|
| **PreDoneCheck**（planner 不能在 exploitation 没完时 done）| deep 的 `task` 委派**天然阻塞等子代理返回**——"等 exploitation"内建,不需独立闸。done 前自检（重读 finding/覆盖度）→ planner instruction + 可选 middleware |
| **Inspector**（每 5 步 LLM 判官注 hint,不强中断）| `ChatModelAgentMiddleware`（model 调用前查 state.Messages,注 hint）|
| **OnNoToolCall**（想收口但有 exploitation running）| 大部分被 deep 的 task 收集替代;残留逻辑入 middleware |
| **历史/图压缩** | summarization-style `ChatModelAgentMiddleware`（model 调用前压缩 messages）|
| **凭证两路协议**（read/write_credential）| 工具不变（包成 eino tool）;redis 凭证池保留或迁 Postgres |
| **identity/tool 戳**（http_flow）| 与 agent 框架正交,捕获/ingest 层不动 |
| **per-agent 独立 model 实例** ★ | **spike 抓到的硬坑**：每个 ChatModelAgent/sub-agent 用独立 `ChatModel`,共享会被串行化(BindTools 改内部状态)。装配处铁律 |

## 5. 可观测（interceptor/instrument → eino callbacks）
- eino `callbacks` 切面 hook model/tool 调用前后 → 写 `llm_invocation` / `tool_invocation` 表（保留 liusha 落库可观测,比 eino 默认更适合）。
- 替换 `internal/toolruntime/interceptor` + `internal/llm/instrument`。

## 6. 不变的部分（域 + 基建）
- **域数据/逻辑**：finding/lesson/note/sitemap/http_flow 模型、vuln skills、prompt（角色段）、BAC 方法论、凭证协议措辞。
- **基建**：sandbox/launcher、browser-svc.py、mitm-capture.py、proxy/ingest、Postgres（必留）。
- **prompt**：traffic-analysis/planner/exploitation 的 instruction 直接作 eino agent 的 `Instruction`（措辞复用,机制换）。

## 7. 分阶段计划（每阶段独立 e2e 可验 / 可回退）

| 阶段 | 内容 | 验收 |
|---|---|---|
| **P0**（done）| 两个 spike 验证 eino passive/active 可行 + 抓 per-model 坑 | ✅ 已完成 |
| **P1** provider | eino-ext ChatModel 工厂（按 role 选 provider，国产全覆盖 + anthropic 确认 + 项目修复保住）| 各 provider curl/单测 |
| **P2** 工具 | liusha 工具 → eino tool.BaseTool（InferTool）;OwnerID 注入;done→Exit;callbacks 落库 | 工具单测 + 1 个 agent 调通 |
| **P3** traffic-analysis | passive → ChatModelAgent + Runner;Inspector/压缩 → middleware | e2e passive 各 profile PASS |
| **P4** active | planner+exploitation → deep;**per-agent 独立 model**;asynq 派 exploitation 删 | e2e active:bac PASS + 并发验证 |
| **P5** 被动分发 | asynq 派 traffic-analysis → 进程内 worker pool 消费 flow_events | passive 全链路 PASS |
| **P6** 增量 | MCP / 流式（接前端）/ 攻击链 / RAG（knowledge 包,pgvector 或 eino retriever）| 各自独立验 |

## 8. 风险 / 决策点
- **版本锁定**：eino@v0.8.13 + eino-ext openai@v0.1.13 钉死（v0.x 抖动;eino ADK 子包是 alpha,升级前必回归）。
- **eino ADK alpha**：deep/ChatModelAgent 接口可能变,P3/P4 前确认版本、锁定。
- **anthropic 覆盖**：P1 确认 eino-ext claude 是否够用,否则保留一条手写或走兼容。
- **Redis 去留**：迁移后 asynq 可删;flow_events 流看 proxy 是否合并;credentials/notes 可迁 Postgres（独立决定,不与本迁移捆绑）。
- **每 agent 独立 model 的资源开销**：active 多 exploitation = 多 model 实例,确认无连接/内存问题（实测）。

## 9. 已验证产物（P0）
- `spike/eino-traffic-analysis/`：passive ChatModelAgent + 小米 mimo + 自动 schema，一次跑通。
- `spike/eino-deep/`：active deep + 2 exploitation 并发（独立 model 后 window 重叠），抓到 per-model 坑。
- 两者独立 module、throwaway，未碰主代码。正式迁移时作 P3/P4 的参照（自己写的,非抄）。

## 10. 已知待办（e2e 实测暴露）

### TODO-1：active 截图视觉回灌（P6 / 后续）
**现状（止血）**：run_command 不再把截图作 tool-role image part 回灌（commit f31c7413）。
**根因**：eino 把图片放 tool-role message，但 OpenAI 标准里图片只能在 user message；
小米 mimo 严格执行 → 400「Param Incorrect: `text` is not set」（image_url part 无 text 字段）。
react 旧路径同样把图放 tool message **却不报 400** —— 序列化细节不同（待查清照搬）。
**影响（实测比预想严重）**：截图缺失把 **active 的 browser-use 登录流打瘫**——planner 看不到页面截图、靠 `browser-use state` 文本盲打登录，2026-06-07 active e2e 实测 planner 42 轮里 33 次 browser-use / 19 次 login 尝试，困在 recon 爬不出、没 spawn 任何 exploitation。
- 严重受影响：**active 登录**（active 入口）+ DOM-XSS / 渲染验证 / 视觉化 BAC。
- 不受影响：passive 纯 HTTP 类（SQLi/LFI/upload/RCE，本就靠文本响应——passive e2e 已实打实通过）。
- ★ **优先级上调**：截图回灌不是"DOM-XSS 才需要的后续项"，而是 **active 路径能正常工作的前提**。

**根因已查清 + 根治方案锁定（照搬 react，方案 1=方案 3 殊途同归）**：
react 用 `SupportsVision` 开关（internal/llm/openai_compat.go:32 + 151-159）：
- vision provider：tool 结果含图 → **文本留 tool message（保 tool_call_id 关联），图累积 pendingImages → flush 成紧随的 user message**（OpenAI 标准位置，mimo 不 400）。
- 非 vision provider：`stripImagesToText` 降级文本占位。
**eino 缺的就是"图转投 user message"这一步**——eino EnhancedTool 把图放 ToolResult（→tool message），无 flush。
**实现路径（standalone）**：eino ChatModelAgent 消息流框架管，需找注入点把工具产出的图转成下一条 user message。候选：
- AgentMiddleware.AfterChatModel / WrapToolCall 后处理，往 state.Messages 插 user message 带 image MultiContent；
- 或自定义 GenModelInput；
- 按 provider SupportsVision 开关（config.Providers[key].SupportsVision 已有字段）——vision 才回灌图，非 vision 走当前文本止血。
run_command 需恢复产出 image 数据（当前 f31c7413 已删 image part，改由 middleware 在消息层回灌）。
**用户决策（2026-06-07）**：听 Claude 的——active 登录瘫痪证明视觉是 active 前提，TODO-1 提前做（不再"后续"）。下一步实现此方案。
