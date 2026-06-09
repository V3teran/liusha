# liusha → 对话式多场景安全平台 规划

状态：规划中（2026-06-07）
业界参考：CyberStrikeAI（已逐文件读其架构）

## 1. 目标

把 liusha 从「全自动后台扫描器」演进为「对话式多场景安全平台」，同时保留并增强其独有的 passive 流量驱动能力。

**两条产品线并存：**
- **active（对话驱动）**：用户在前端对话框选场景（CTF / Web 扫描 / 渗透…）、聊天发起扫描，agent 扫描过程经 SSE 实时流式展示在对话框。
- **passive（自动驱动，liusha 独有）**：proxy 抓到流量自动触发 agent 扫描，**不走对话**；过程/结果可在 viewer 或对话历史查看。

## 2. liusha 现状（基线）

| 维度 | 现状 |
|---|---|
| HTTP | gin REST API（internal/httpapi）：credential/scan/session/sitemap/llm/agent_runs |
| 前端 | web/viewer 静态页（embed），只读看 sitemap/finding |
| agent | 已迁 eino（spawn_exploitation 自定义 swarm），已转默认；react 留退路 |
| passive | proxy → ingestor → scanner（asynq）→ traffic-analysis 单 agent 挖洞 → 落库 |
| active | scanner → orchestrator+exploitation（当前 spawn_exploitation） |
| 缺失 | 无对话、无 SSE、无 role/场景、无 deep 编排 |

## 3. 关键设计决策（已与用户对齐 + 源码/实测支撑）

1. **agent 编排改用 eino `deep` prebuilt**（替换 spawn_exploitation）。
   - 实测：共享 ChatModel 并发安全 → **per-hunter 独立 model「铁律」作废**（spike/eino-sharedmodel）。
   - deep 的 sub-agent 之间串行 → **符合杀伤链流程本就串行**；sub-agent **内部**多工具并行（eino ToolsNode 原生）。用户已认可。
2. **两层角色（照 CyberStrikeAI 的正交结构，但实现自研）**：
   - **role（场景）**：CTF / Web 扫描 / 渗透… → 换**主代理（orchestrator/orchestrator）**的人设 prompt + 工具集。用户前端选。
   - **agents（杀伤链阶段 sub-agent）**：recon / 攻击面枚举 / 漏洞分诊 / 渗透利用 / 提权 / 横向… → deep.Config.SubAgents。**不随 role 变**，所有场景共用。
   - 维度判定：sub-agent 按**杀伤链阶段/职能**切（不是单漏洞、不是漏洞类型）——这是 deep「预定义固定模板」模型的唯一契合维度。
3. **角色动态加载（自研，非抄 CyberStrikeAI）**：markdown + frontmatter，运行时扫目录装配。
   - frontmatter 字段是 **liusha 自有**（绑定哪些 einotools、owner 注入、sandbox 配置），解析/装配接 liusha 的 einoagent/einotools，与 CyberStrikeAI 的 einomcp 字段无关。
   - 概念（目录扫 markdown）是行业通用模式（Claude Code subagent 等），非 CyberStrikeAI 独创。
4. **passive 形态**：仍是**单 agent traffic-analysis**（一条流量 → 一个 traffic-analysis，单 agent 够，不需 deep 多代理）。「passive 作为 role」= 一种**不需要对话、proxy 自动触发**的扫描模式，复用现有 scanner 链路，agent 用 eino。
5. **文件隔离（deep 模式）**：`run_command` 每次开独立临时目录（deep 无法 per-sub-agent 注入隔离 key；实测+源码确认 AgentTool 固定 checkpoint）。代价：同 exploitation 跨命令 cwd 不连续（少数「下载→后用」链路需写完整路径）——可接受。
6. **SSE 流式**：eino agent 事件（EmitInternalEvents + callbacks）→ progressCallback → SSE（text/event-stream）。事件类型：progress / tool_call / tool_result / thinking_stream / done / error。**并发回调需加锁**（eino parallelRunToolCall 多 goroutine 回调）。

## 4. 目标架构（模块）

```
前端对话 UI（新）─── POST /chat/stream (SSE) ──┐
                                              ▼
                            对话 handler（新，gin SSE）
                              │ role 处理（场景人设+工具）
                              │ agent mode 分发
                              ▼
                    deep 编排器（einoagent，改造）
                      orchestrator(orchestrator) + 动态加载的杀伤链 sub-agents
                              │ EmitInternalEvents + callbacks
                              ▼
                    progressCallback → SSE 事件
                              │
                    conversation 存储（新，PG）

proxy → ingestor → scanner（asynq，复用）
                              ▼
                    passive：单 agent traffic-analysis（eino，复用+换路）
                              ▼
                    过程/结果落库 → viewer / 对话历史查看
```

## 5. 分阶段路线图（依赖顺序 + 风险）

> 原则：每阶段独立可验收、可 commit；底层先行；前端最后。

### 阶段 A：agent 编排换 deep + 动态角色加载（后端核心）
- 内容：orchestrator+exploitation（spawn_exploitation）→ deep；定义杀伤链 sub-agent（起步最小：recon + exploitation/渗透利用，可后续加）；markdown+frontmatter 动态加载；run_command 改每命令临时目录。
- 不碰：前端 / SSE / role 场景层。
- 验收：active e2e（deep 编排）跑通，sub-agent 按阶段派活、出 finding；passive traffic-analysis 不受影响。
- 风险：中。有 spike（eino-deep-swarm 已验证 deep 动态派+共享 model）+ 实测基础。

### 阶段 B：对话 API + SSE + eino 事件桥接（让 active 能对话+流式）
- 内容：POST /chat/stream（SSE handler）；conversation/message PG 存储；progressCallback（eino 事件→SSE，含并发加锁）；active 改为「对话发起」可选入口（保留 scanner 自动入口）。
- 验收：curl/前端发起对话，SSE 实时收到 agent 每步事件，落库可回看。
- 风险：高。全新交互层（SSE / 对话管理 / 事件桥接），CyberStrikeAI 的 createProgressCallback 有几百行复杂度可参照。

### 阶段 C：role 多场景系统（用户选场景）
- 内容：role 定义（场景人设+工具集，动态加载）；主代理按 role 换 prompt+工具；API 暴露 role 列表供前端选。
- 验收：选不同 role 发起，orchestrator 人设/工具随之变，sub-agents 不变。
- 风险：中。

### 阶段 D：前端对话 UI
- 内容：对话框 + role 选择 + SSE 事件渲染（agent 思考/工具调用/结果时间线）。基于现有 web/viewer 扩展或新建。
- 验收：完整对话扫描体验。
- 风险：中高（前端工作量大；liusha 现 viewer 是只读静态页，对话 UI 是新东西）。

### 阶段 E：passive 接 deep（可选）+ 过程可视
- 内容：评估 passive 是否需要多代理（默认单 traffic-analysis 够）；passive 扫描过程接入对话历史/viewer 可视。
- 验收：passive 自动扫描过程可在 UI 回看。
- 风险：低-中。

## 6. 风险与开放问题

- **deep 串行 vs 真并发**：deep 的 sub-agent 串行（杀伤链可接受），但若将来某场景需要真并发（多攻击面同时挖），deep 满足不了——届时要么接受慢、要么对该场景保留 spawn_exploitation。**记录在案，当前按 deep 走。**
- **eino 版本锁定**：deep / AgentTool 是 alpha（v0.8.13），升级前必回归。
- **react 旧码**：阶段 A 把 active+passive 都迁 deep 稳定后，才删 internal/react + subtask。
- **前端选型**：web/viewer 现是原生静态页；对话 UI 是否引入框架（待阶段 D 定）。
- **passive 与 role 的语义**：passive 不走对话，但用户希望「作为一种 role」——需在 UI 概念上统一（passive 是「自动模式」，active 各 role 是「对话模式」）。
- **HITL（工具执行前人工批准）**：CyberStrikeAI 有，liusha 是否需要（自动扫描场景通常不需要）——待定。

## 7. 建议的起步

**阶段 A 最该先做**：它是 active+passive 共同的 agent 基础，是 eino 迁移的正宗收尾（deep 替 spawn_exploitation），且纯后端、风险可控、有 spike 基础。B/C/D（对话平台）依赖 A 的 agent 层稳定后再上。
