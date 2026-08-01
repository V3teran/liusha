# liusha-ui 重建方案（权威进度文件）

> **重启先读这个文件 + 记忆 `reference_liusha_ui.md`。** 每完成一阶段就 git commit，进度永不丢。
> 最后更新：2026-06-11（重写：上一轮噪声会话的改动全部未落盘，从 commit 84c9c50 干净重启）

## 目标
把 liusha-ui 从「单页对话 + 土样式」升级成**高大上的安全作战控制台**：深色为主双主题、侧栏多页、对接后端**全部** ~18 个 API。参考 CyberStrikeAI 的观感（深色终端风 + ECharts 仪表盘），但做得更克制、更现代。Vue（**不是 React**）。

## 技术栈（全部 @latest）
- 现有保留：Vue 3.5 + Pinia + Vite + TS + Vitest
- 新增：`vue-router@4`、`naive-ui`、`echarts` + `vue-echarts`、`@vicons/tabler`（图标）
- 设计：深色主色双主题（`data-theme` 切换），设计 token 在 `style.css`

## 后端真实 API（权威，源自 internal/httpapi/server.go，全部 X-API-Key）
| 域 | 方法 路径 | client.ts 已封装 |
|---|---|---|
| 对话 | POST /chat | ✅ startChat |
| 对话 | GET /conversations | ✅ listConversations |
| 对话 | GET /conversations/:id/messages | ✅ listMessages |
| 对话 | POST /conversations/:id/messages | ✅ followUp |
| 对话 | POST /conversations/:id/abort | ✅ abortScan |
| 对话 | GET /conversations/:id/stream (SSE) | ✅ useEventStream |
| 角色 | GET /roles | ✅ listRoles |
| 被动会话 | POST /scan/passive | ❌ |
| 被动会话 | GET /session | ❌ |
| 被动会话 | POST /session/:id/abort | ❌ |
| 主动扫描 | POST /scan/active | ❌ |
| 攻击面 | GET /sitemap/:owner_id | ❌ |
| LLM审计 | GET /llm/invocations/:owner_id | ❌ |
| Agent运行 | GET /agent_runs/:owner_id | ❌ |
| 凭证 | GET /credential | ❌ |
| 凭证 | POST /credential/batch | ❌ |
| 凭证 | DELETE /credential | ❌ |

> 注意：`/login` `/admin/*` `/profile` `/order` 是 **cmd/vulnapp（被测靶场）** 的路由，**不是** liusha API，前端不接。

## 路由 / 页面（侧栏）
- `/` Dashboard —— 汇总卡片 + ECharts（findings 分级、扫描态、LLM 调用量）
- `/chat` 对话 —— 复用现有 ChatThread/Composer/ConversationList（核心资产，丝滑保留）
- `/findings` 漏洞发现 —— 从 agent_runs/会话聚合（按 owner）
- `/sitemap` 攻击面树 —— GET /sitemap/:owner_id
- `/sessions` 被动会话 —— GET /session + 发起/停止
- `/scans` 主动扫描 —— POST /scan/active 发起 + 列表
- `/agent-runs` Agent 任务树 —— GET /agent_runs/:owner_id（swarm 子任务可观测）
- `/llm-audit` LLM 审计 —— GET /llm/invocations/:owner_id
- `/credentials` 凭证库 —— GET/POST/DELETE credential
- `/settings` 设置 —— API key、主题

## 执行阶段（每阶段一 commit）
- [x] **阶段0 地基**：装依赖；router + Layout 外壳（侧栏+顶栏+主题切换）；把现有对话挂到 /chat；main.ts 接 router+naive provider。✅ tsc/build/27 测试绿。（鉴权门按用户要求暂移除，ApiKeyGate.vue 留存待复用）
- [x] **阶段1 client 补全**：api/client.ts 补齐 10 个端点（passive/session/abort/active/sitemap/llm/agent_runs/credential×3）+ post/del 辅助 + types.ts 镜像类型 + 11 个新测试。✅ tsc/38 测试绿。
- [x] **阶段2 数据页**：findings / sitemap / agent-runs / llm-audit 四页 + 复用 OwnerPicker + lib/echarts(按需注册) + lib/severity。✅ tsc/build/38 测试绿。ECharts 懒加载独立块不进首屏。
- [x] **核心对话链路：打通 + 打磨（用户重定向，优先级最高）** —— 2026-06-11
  - **能跑通已实测**：起真后端栈（run-svc 8090 + scanner + pg/redis + pentools），curl 走 `/chat`→拿 conversation_id+stream cookie→SSE 实时流（user→tool_call→tool_result 逐条到）→`/messages` 回放一致。鉴权、派单、SSE、数据接口全绿。
  - **修了删登录门造成的硬阻断**：`bootstrapApiKey()` 启动自动拉 `/dev-config.json` 的 dev key（后端无 keyless 模式，空 key 必 401）。
  - **修了 FindingCard 真 bug**：write_finding 的 Result 只有 {id}，真数据在 Args → 改解析 Args 出 severity/summary/cwe/target。
  - **体验打磨**：ChatThread 自动滚底（贴底才滚）；ChatView 空态引导 + "agent 工作中"脉冲指示；ToolCall/ToolResult 折叠+美化 JSON+状态点；Composer 重设计(NSelect 角色+发送态)；AssistantText 保留换行。
  - 注：消息 schema 无 agent 身份字段 → 三角色视觉区分做不了（不杜撰）。✅ tsc/38 测试/build 全绿。
- [ ] **阶段3 管理页**：sessions / scans / credentials（含发起/停止/增删）。
- [ ] **阶段4 Dashboard + 精修**：汇总页、空态/加载态、响应式、双主题打磨。
- [ ] **可选**：assistant 文本 markdown 渲染（需 marked+DOMPurify，暂缓避免 XSS/依赖面）。

## 后端起栈速记（dev 验证用）
- docker pg/redis 已常驻；schema 已是最新（`make migrate` 会因 goproxy.io 拉 migrate 工具失败，但表已在，可跳过）。
- 起服务：`./scripts/dev/run-svc.sh`（api:8090 / scanner:9090 / proxy:9091 / vulnapp:8001；自动设 LIUSHA_API_KEY=changeme-dev-key + LIUSHA_DEV_AUTOFILL=1）。需 .env.local 里 DEEPSEEK_API_KEY。
- 前端对接：`VITE_API_TARGET=http://localhost:8090 pnpm dev`（默认 8080 是错的，dev 用 8090）。
- 验证扫描：`./scripts/dev/e2e.sh active:bac`（或直接 curl POST /chat）。

## 不破坏铁律
- 对话链路（ChatThread/Composer/ConversationList/stores/useEventStream/api 现有 7 函数）是已验证资产，只重组不重写逻辑。
- 改动前 git 干净；每阶段 commit；vitest 现有测试保持绿。
