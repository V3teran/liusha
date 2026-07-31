# liusha 前端 React 重写实施方案

## 目标与决策

把 `web/` 前端从 **Vue 3 + Vite + ant-design-vue** 全量重写为 **React + Vite + shadcn/ui**，视觉设计推倒重来，功能与后端契约 1:1 保留。

**已敲定的决策：**
- 框架：**Vite + React**（不用 Next.js/SSR — 登录后 SPA，无 SEO 需求）
- 组件底座：**shadcn/ui + Radix + Tailwind**
- 设计方向：**石墨深空 (Graphite)** — zinc-950 深底 + emerald-400 单一强调色 + Geist Sans/Mono，Linear/Vercel 气质；**深色/浅色可切换**（Tailwind CSS 变量 + `class="dark"`）
- 状态：**Zustand**（客户端）+ **TanStack Query**（服务端状态/缓存/轮询，补上现在缺的这层）
- 执行图 DAG：**React Flow (@xyflow/react)** + dagre.js 布局
- 路由：**React Router**
- 测试：**Vitest + React Testing Library**，全部 82 个测试移植 + 新逻辑补测到 80%+
- 迁移方式：**直接在 `web/` 上推倒重写**，不留兼容代码，不做并存（重写期间前端不可用）

## 目标目录结构

```
web/
├── index.html
├── package.json / pnpm-lock.yaml / .npmrc
├── vite.config.ts            # 保留 /api proxy + rewrite（后端契约不变）
├── tsconfig*.json
├── tailwind.config.ts        # 新增
├── components.json           # shadcn 配置
├── src/
│   ├── main.tsx / App.tsx
│   ├── index.css             # Tailwind + 设计 token（明暗两套 CSS 变量）
│   ├── router.tsx            # React Router 路由表（对齐现有路由）
│   ├── layout/
│   │   └── AppShell.tsx      # 侧栏导航 + 顶栏 + 主题切换
│   ├── pages/                # = 现 views/
│   │   ├── ConversationsPage.tsx   (active/passive 两 tab)
│   │   ├── FindingsPage.tsx
│   │   ├── AttackGraphPage.tsx     (React Flow)
│   │   ├── LlmAuditPage.tsx
│   │   ├── SitemapPage.tsx
│   │   └── PlaceholderPage.tsx
│   ├── features/             # 按领域分组的复杂组件
│   │   ├── conversation/     # List/Detail/Composer/ChatThread/TimelineThread/cards
│   │   ├── finding/          # FindingDrawer + 表格
│   │   ├── llm-audit/        # InvocationDrawer + 表格
│   │   └── attack-graph/     # React Flow 节点/边/布局
│   ├── components/ui/        # shadcn 生成的原子组件（button/select/drawer/...）
│   ├── stores/               # Zustand: conversation store
│   ├── hooks/                # = 现 composables/
│   │   ├── useEventStream.ts        # SSE（最高风险，1:1 移植逻辑）
│   │   ├── useTypewriter.ts
│   │   ├── useTheme.ts
│   │   └── useCodeCopy.tsx
│   ├── api/                  # client.ts + types.ts（契约不变，几乎直接沿用）
│   └── lib/                  # 纯逻辑模块（见下）
```

## 关键契约（重写不可破坏）

### API 层（`api/client.ts` + `types.ts`）
- **几乎原样保留** — 这层是纯 TS，无框架依赖，与 React/Vue 无关。直接沿用现有实现。
- 保留：`/api` 前缀、`X-API-Key`（sessionStorage）、`bootstrapApiKey` dev autofill
- 特殊语义全保留：`listMessages` 自动分页（500/页游标）、`followUp` 409 `.busy`、`deleteConversation` 409 `SCAN_ACTIVE`、`authStream` `credentials:include`、LLM 审计 keyset 分页 + stat/list 筛选口径一致（stat 不带 after、空 end 不发）
- types.ts 保留 PascalCase（Message/ScanEvent/Conversation）vs snake_case（其余 DTO）的区分

### SSE（`hooks/useEventStream.ts`）— 最高风险
1:1 移植现有逻辑，改成 React 惯用形态（返回 `{ status, close }`，在组件 `useEffect` 内起停）：
- 每次（重）连前先 `authStream` 刷 HttpOnly cookie（EventSource 不能带 header）
- `withCredentials: true`，URL 带 `/api` 前缀
- 三事件：`onmessage`→`store.ingest`、`delta`→`store.appendReasoningDelta`、`onerror`→自管重连
- 指数退避上限 15s，连上重置；`close()` 阻止后续重连
- 三态 `connecting/open/reconnecting`

### Store（`stores/conversation.ts` → Zustand）
- 保留：二分插入维持 seq 升序 + seqSet 去重、`liveReasoning` 缓冲在最终 reasoning 帧清空、`lastSeq`
- action：`ingest` / `appendReasoningDelta` / `reset`

### lib/ 纯逻辑（直接移植，与框架无关）
`markdown.ts`(marked+highlight.js+DOMPurify，XSS 安全关键)、`toolCalls.ts`、`threadRows.ts`、`messageKind.ts`、`format.ts`、`llmTiming.ts`、`severity.ts`、`findingStatus.ts`、`scanStatus.ts`、`agentColor.ts`、`clipboard.ts` — 逻辑几乎照搬，仅 markdown 的 v-html→dangerouslySetInnerHTML + useCodeCopy 事件委托改 React 形态。

## 路由表（对齐现有）
- `/` → redirect `/conversations/active`
- `/conversations/{active,passive}` → ConversationsPage（mode 参数）
- `/findings` `/sitemap` `/attack-graph` `/llm-audit` → 对应页
- `/dashboard` `/credentials` `/settings` → Placeholder

## 实施阶段

**Phase 0 — 脚手架**
清空 web/src，建 Vite+React+TS 骨架，装依赖，配 Tailwind + shadcn（components.json），写设计 token（明暗两套 CSS 变量 + Geist 字体）、AppShell 布局 + 主题切换。保留 vite.config 的 /api proxy。

**Phase 1 — 无框架依赖层（可先并行、风险低）**
移植 `api/`（近乎照搬）、`lib/` 全部纯逻辑模块、`stores/conversation`（Zustand）、`hooks/`（useEventStream/useTypewriter/useTheme/useCodeCopy）。**同步移植对应测试**（client.test、conversation store test、useEventStream test、useTypewriter test、lib 4 个 test）。这步完成后核心契约就有测试护栏。

**Phase 2 — 消息渲染体系（对话页的心脏）**
cards/（UserBubble/AssistantText/ReasoningCard/ToolCallCard/ToolResultCard/FindingCard/SpawnCard/CompactionCard/Avatar）、MessageItem 分发器、StepTools 折叠组、ChatThread + TimelineThread（含 useTypewriter 流式气泡、自动滚动/未读/跳到最新）。移植 MessageItem.test、ChatThread.test。

**Phase 3 — 对话页整合**
ConversationList（轮询、右键菜单、内联重命名乐观更新、删除 409 守卫）、ConversationDetail（SSE 生命周期、usage 轮询、停止扫描）、Composer（新建/追问、409 busy、follow-up seq 快照竞态修复）、RolePicker/OwnerPicker。ConversationsPage 双 tab。

**Phase 4 — 其余页面**
FindingsPage + FindingDrawer（证据分类渲染、triage 五态表单、复制）、LlmAuditPage + LlmInvocationDrawer（inputDelta 增量显示、分节复制、keyset 分页、facets 筛选、时间范围）、SitemapPage（树 + 严重度标签）。

**Phase 5 — 执行图**
AttackGraphPage 用 React Flow：dagre.js 算 TB 布局坐标，自定义节点（按 kind 造型 + severity/status 着色 + on_path 高亮）、自定义边（flow/depends_on/evidence）、缩放/拖拽/fitView、里程碑按需加载、live/simplified 开关、轮询。

**Phase 6 — 收尾验证**
`pnpm build` 通过；`pnpm test` 全绿且覆盖 ≥80%；nginx 镜像多阶段构建验证（node 构建新 React dist → nginx 托管）；明暗主题切换验证；SSE 实时链路端到端手测（起 Go 后端，发起扫描，看流式输出）。

## 依赖变更（package.json）
**移除**：vue、vue-router、pinia、ant-design-vue、@vicons/tabler、@antv/g2、@antv/g6、marked/marked-highlight（换 react-markdown 或保留 marked 纯函数用法）、@vitejs/plugin-vue、vue-tsc、@vue/*
**新增**：react、react-dom、react-router-dom、zustand、@tanstack/react-query、@xyflow/react、dagre、tailwindcss、shadcn 相关（radix-ui、class-variance-authority、clsx、tailwind-merge、lucide-react）、@vitejs/plugin-react、@testing-library/react、geist（字体）
**保留**：vite、vitest、typescript、highlight.js、dompurify、marked（作为纯函数库仍可用）

## 风险与对策
1. **SSE 契约** — Phase 1 先移植并用移植的 test 锁死行为，再往上搭 UI。
2. **markdown XSS** — DOMPurify 必须保留，markdown.test 先过。
3. **推倒重写期间前端不可用** — 已确认接受；按阶段推进，每阶段可独立 `pnpm build`/`test` 验证，降低"全写完才发现问题"的风险。
4. **React Flow 布局** — G6 的 antv-dagre 换成 dagre.js 手算坐标，需仔细核对节点造型与 severity/on_path 视觉语义。
5. **PascalCase/snake_case 混用** — types.ts 原样保留，不做归一化（后端契约）。

## 验收标准
- [ ] `pnpm build` 通过
- [ ] `pnpm test` 全绿，覆盖 ≥80%
- [ ] 所有原路由/页面功能对齐，无功能缺失
- [ ] SSE 实时流式端到端可用（含重连）
- [ ] 明暗主题可切换并持久化
- [ ] nginx 镜像构建 + 托管新 dist 验证通过
- [ ] 石墨深空设计方向落地，非模板脸
