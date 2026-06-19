# liusha-ui：对话式前端 UI 设计（阶段 D）

状态：设计已确认（2026-06-10）
依赖：阶段 B/C 已完成（POST /chat、GET /roles、GET /conversations、GET /conversations/:id/messages、GET /conversations/:id/stream(SSE)）
上游计划：[2026-06-07-conversational-platform.md](2026-06-07-conversational-platform.md) 阶段 D

## 1. 目标

把 liusha 的对话式扫描能力暴露成一个真正可用的前端：用户选场景（role）、用自然语言发起扫描、agent 跑扫描的过程（思考 / 工具调用 / 结果 / finding）经 SSE 实时流式展示在对话框，并可回看历史对话。

前端统一在 liusha-ui（Vue3）实现 Sitemap/LLM/Tasks 等视图；早期内嵌的只读静态页（vanilla JS / d3 sitemap）已迁出删除。

## 2. 关键决策（已与用户对齐）

| 维度 | 决策 | 理由 |
|------|------|------|
| 前端项目 | 独立新仓 `/Users/Xlbula/workspace/programs/typescript/liusha-ui` | 用户选 B：前后端语言/工具彻底分离 |
| 技术栈 | Vue 3 + Vite + TypeScript + pnpm | 现代工程化，SSE 流式 + 对话状态用响应式框架更顺 |
| 范围 | 仅对话体验 | 阶段 D 本职；不港现有只读三视图 |
| SSE 认证 | 原生 EventSource + 短时效 httpOnly cookie | 白嫖原生重连 + Last-Event-ID（后端已支持）；token 不进 URL |
| 同源策略 | 反代/proxy 收敛到同源（dev Vite proxy，prod 反代） | 绕开跨源 cookie 的 SameSite=None;Secure + CORS 痛苦 |
| 视觉方向 | 技术终端风 | 暗底 + 等宽字体点缀 + finding 语义色，契合安全工具气质 |

## 3. 架构与"同源"收敛（关键）

选独立仓后，原生 EventSource + cookie 会撞 SameSite：跨站 cookie 必须 `SameSite=None; Secure`（要求 HTTPS），本地 `http://localhost` 开发别扭。

**解法：dev 与 prod 都让浏览器看到"同源"**，liusha-ui 仍是独立构建/部署产物，但靠反代/proxy 收敛：

- **Dev**：Vite dev server 配 `server.proxy`，把 `/api/*` 与 SSE 转发到 Go API（默认 `http://localhost:8080`）。浏览器视角同源 → cookie 走 `SameSite=Lax`，**无需 CORS**。
- **Prod**：反向代理（nginx/caddy）把 liusha-ui 静态资源与 Go API 挂同一域名不同路径（`/` → ui dist，`/api/*` → Go API）。仍同源。

因此 **Go 后端不必开 CORS**，cookie 用最稳的 `SameSite=Lax`。

> 备选（不推荐）：真不同源跨域 → 回到 `SameSite=None; Secure` + CORS `Allow-Credentials`，dev 需 HTTPS。更折腾，本设计不走。

```
浏览器
  │  同源（dev: Vite proxy / prod: 反代）
  ▼
/api/* ──────────────► liusha Go API（cmd/api，gin）
  ├─ POST   /chat                         发起：建 conversation + active_scan，下发短 cookie
  ├─ GET    /roles                        场景列表
  ├─ GET    /conversations                对话列表
  ├─ GET    /conversations/:id/messages   历史回看（after_seq 增量）
  └─ GET    /conversations/:id/stream     SSE：补历史 + 实时事件（cookie 鉴权）
/ ──────────────────► liusha-ui 静态资源（Vue dist）
```

## 4. 后端改动（阶段 D 连带，改 liusha Go 仓）

范围极小，不动现有 SSE 时序逻辑：

1. **`POST /chat` 成功后下发短时效签名 cookie** `liusha_stream`：
   - 属性：`HttpOnly` + `Secure` + `SameSite=Lax` + `Max-Age≈1800`（30min）
   - 值：签名 token（HMAC，绑定可选 conversation 作用域 + 过期时间），密钥取环境变量
   - 作用：让 `EventSource(..., {withCredentials:true})` 能自动带、完成 stream 端点鉴权
2. **`streamHandler` 增加 cookie 鉴权分支**：原有 X-API-Key header 仍可用（curl/调试）；新增校验 `liusha_stream` cookie 有效则放行。两路任一通过即可。
3. 其余端点（`/chat`、`/roles`、`/conversations`、`/messages`）前端 fetch 照常带 `X-API-Key` header。

> 安全：cookie 短时效 + HttpOnly + 签名，避免 token 进 URL/日志；不引入长生命周期会话。密钥缺失时 fail-fast。

## 5. 前端组件与数据流

```
App
├─ ApiKeyGate         首次输入 X-API-Key，存 sessionStorage（仅本会话）
├─ ConversationList   侧栏：GET /conversations，点击切换 / 历史回看
├─ RolePicker         GET /roles，发起前选场景（web-pentest / passive-recon…）
├─ ChatThread         主区：消息 + 事件时间线
│   ├─ UserBubble        用户 brief（KindMessage, role=user）
│   ├─ ToolCallCard      tool_call：工具名 + 入参（可折叠）
│   ├─ ToolResultCard    tool_result：结果预览 + 耗时；Err 态标红
│   ├─ FindingCard       write_finding 结果 → 语义色严重度卡片
│   └─ AssistantText     普通 KindMessage（assistant/system）
└─ Composer           输入 brief + 选 role → POST /chat → 订阅 SSE
```

数据流：
1. `Composer` 提交 → `POST /chat {brief, role_id}` → 拿 `conversation_id`（后端同时下发 cookie）
2. `new EventSource('/api/conversations/{id}/stream', {withCredentials:true})`
3. 每帧 `data:` 是一条 `conversation.Message`（JSON）；按 `Kind`（message/event）+ Metadata 事件类型分发到对应卡片组件
4. `id:`（seq）做去重；断线原生重连自动带 `Last-Event-ID`，后端补历史续读

状态管理：轻量 store（Pinia 或 composable）持有当前 conversation 的消息数组（按 seq 有序、去重）；切换对话时先 `GET /messages` 拉历史再订阅 stream。

## 6. SSE 事件 → 卡片映射

后端事件形态（`internal/einoagent/scan_event.go` + `cmd/scanner/event_sink.go`）：`KindEvent` 消息，Metadata 区分 `tool_call`（ToolName/Args）与 `tool_result`（Result/DurationMs/Err）。

| 后端 message | 前端卡片 |
|--------------|----------|
| KindMessage, role=user | UserBubble |
| KindMessage, role=assistant/system | AssistantText |
| KindEvent tool_call | ToolCallCard（工具名 + args 折叠） |
| KindEvent tool_result | ToolResultCard（结果预览 + 耗时） |
| KindEvent tool_result + Err | ToolResultCard 红色错误态 |
| write_finding 相关事件 | FindingCard（按严重度语义色） |

## 7. 视觉：技术终端风

- 暗底为主；工具名 / 命令 / 结果用等宽字体（mono）点缀，正文用易读无衬线
- finding 严重度语义色：critical 红 / high 橙 / medium 黄 / low 灰
- agent 思考 / 工具流像控制台滚动；克制的边框与层次
- 设计 token（颜色/间距/字号）用 CSS 自定义属性集中定义
- 具体视觉落地在实现期用 frontend-design skill，避免模板脸

## 8. 测试

- **前端单元**（Vitest）：SSE 帧解析、Metadata→卡片映射、seq 去重、重连续读、store 有序合并
- **前端组件**（Vue Test Utils）：各卡片渲染 + 折叠/错误/严重度态
- **后端单元**：cookie 签发（属性/过期/签名）+ streamHandler cookie 鉴权分支
- **E2E（可选，Playwright）**：发起对话 → 收到流式 tool_call/tool_result → 出 finding 主流程

## 9. 非目标（YAGNI）

- 不迁移现有 Sitemap / LLM / Tasks 三视图
- 不做多用户 / 登录系统（沿用 X-API-Key 单密钥）
- 不做 passive 扫描的对话化（passive 仍自动驱动，本期不进对话 UI）
- 不引入 HITL（工具执行前人工批准）

## 10. 开放问题

- 反代 prod 部署的具体形态（nginx vs caddy）留到部署期定，不阻塞前端开发
- liusha-ui 是否需要自己的 CI/lint/release 流程——新仓建立时按需补
