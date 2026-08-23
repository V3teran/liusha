# 多轮对话设计（ChatGPT 式续接）

状态：设计已确认（2026-06-10）
依赖：阶段 B/C/D 已完成（对话 + SSE + 场景 role + liusha-ui 前端）
上游：[2026-06-07-conversational-platform.md](2026-06-07-conversational-platform.md)

## 1. 目标

把对话式扫描从「单轮（一条 brief → agent 自主跑到 done 就结束）」升级为「多轮」：用户在**同一对话**里追加消息，系统按意图分流——「动作」消息让 agent 接续已有上下文继续扫描，「问答」消息用便宜 LLM 读已挖结果回答。

## 2. 核心模型（关键洞察）

**一个对话 = 一个 active_scan（owner），所有轮次共享同一持久化黑板**（finding / notes / lesson / flow / credential，均按 owner 持久化）。

- agent 的"记忆"= owner 作用域的黑板，**不是 LLM token 上下文**。已验证：`internal/builder/agent/user_prompt.go` 的 `BuildUserPrompt` 注入"该 host 已有 finding（限本次 owner）"+ notes/lesson，故同一 active_scan 上的新 agent run 自动看到所有先前产出。
- 多轮 = 同一 owner 上的「多次 agent run（动作）」+「便宜 LLM 读黑板回答（问答）」。
- 优势：无 token 窗口限制，比 ChatGPT 的 token 续接更稳——记忆是结构化持久数据，不是会被压缩的对话历史。

## 3. 意图路由

新消息进对话 → 分流：

- **问答**（"解释下那个 SQLi" / "哪个端点最危险" / "目前挖到啥了"）→ 便宜 LLM（light provider）读该 scan 的 finding/notes + 问题 → 流式回 assistant 消息，**不触发扫描**。
- **动作**（"再测下上传表单" / "深挖那个 IDOR"）→ 在**同一 active_scan** 上跑一次新 agent run，seed = 新指令 + 对话历史 user 消息；黑板由 `BuildUserPrompt` 自动注入。

**路由方式**：LLM 轻量分类（light provider），prompt 给出最新 user 消息 + 简短上下文，输出 `action | qa`。**模糊时偏 qa**（便宜、安全；避免把提问误判成动作而白烧一次扫描）。分类失败/超时 → 默认 qa。

## 4. 时机与并发

- **问答**：任何时候都允许（包括扫描进行中——只读当前黑板，可答"目前挖到啥"）。
- **动作 + 当前有 active run 在跑**：消息存为 **pending action**，当前 run 到 done 后**自动调度**。不中断进行中的 deep swarm，也不拒绝。
- **一个对话同时只有一个 active agent run**。
- 配 **「停止扫描」** 能力（后端 abort 机制已存在：`watchAbortActive` 轮询 active_scan 状态，非 active 即 cancel）。用户想立刻改方向就停掉当前 run，pending action 随即调度。

## 5. 后端改动

### 5.1 追加消息端点
`POST /conversations/:id/messages`（新）——往已有对话追加 user 消息。流程：
1. 校验 conversation 存在
2. AppendMessage（user / KindMessage）
3. 意图路由 → qa 路径 或 action 路径
4. 返回 `{intent: "qa"|"action", queued: bool}`，前端据此提示

### 5.2 问答路径（新模块 `internal/qa` 或 httpapi 内）
- 取该 conversation 关联 scan 的 finding + notes（owner 作用域，复用现有 store）
- 组 prompt：系统设定（"你是渗透助手，只依据已有 finding/notes 回答，不编造"）+ 黑板摘要 + 用户问题
- 调 light provider，流式 token → 落 assistant 消息（KindMessage）+ publish SSE（复用 scanstream 管道）
- 成本可控：单次便宜 LLM 调用，无 sandbox、无工具

### 5.3 动作重跑路径
- active_scan 若为 completed → **重开**为 active（新增 store 方法 `Reopen` 或复用 status 更新）
- 入队新 agent run（同 owner，复用 `createScan` 的入队逻辑，但不新建 active_scan）：
  - entrypoint brief = 最新 user 动作消息
  - planner user prompt 额外注入**对话历史 user 消息**（让 agent 知道原始目标 + 本次细化）——在 scanner 侧 BuildUserPrompt 调用处加一段，或把历史拼进 brief
- run 跑完 → active_scan 回 completed → 若有 pending action 则调度下一个

### 5.4 排队
- conversation 或一个新 pending 表/字段记录待执行的 action 消息
- 当前 run 转 done 时（scanner 收尾处 / 或 api 轮询）检查 pending → 自动 enqueue 下一个
- MVP 可只支持 1 个 pending（后来的动作消息覆盖或拒绝，避免队列膨胀）

## 6. 前端改动（liusha-ui）

- **Composer**：对话已打开时，发送走 `POST /conversations/:id/messages`（追加）；不再每次新建
- **「+ 新对话」按钮**：显式新建才走旧的 `POST /chat`
- **状态展示**：扫描中 / 排队中（pending action）/ 问答回答中；assistant 回答与 agent 过程事件在时间线区分渲染
- **「停止扫描」按钮**：调 abort，停当前 run
- 意图提示：发送后按返回的 `intent` 显示"已触发扫描" / "正在回答"

## 7. 测试

- 后端单元：意图路由分类（action/qa/模糊→qa）、问答 prompt 组装、active_scan 重开状态流转、pending 调度
- 后端集成：POST /conversations/:id/messages → qa 出 assistant 消息；action 入队新 run
- 前端单元：Composer 追加 vs 新建分支、状态渲染、停止按钮
- E2E（可选）：发起 → 追加问答得到回答 → 追加动作触发二次扫描

## 8. 非目标（YAGNI）

- 不做真正的「LLM token 上下文续接」（用黑板记忆，更稳）
- 不做 agent run **中途注入**消息（动作走排队/重跑，不打断进行中的 swarm）
- 不做多个 pending action 的复杂队列（MVP 单 pending）
- 不做对话归档/删除（与本期正交，可后续单独做）
- passive 扫描不进多轮（passive 自动驱动，无对话）

## 9. 开放问题

- 路由分类的 prompt 与阈值留实现期调；先用 light provider 默认
- 「对话历史 user 消息」注入 planner 的具体形式（拼进 brief vs 独立 prompt 段）留实现期定
- active_scan「重开」是复用 status 字段还是加新状态，留实现期看 store 现状定
- pending action 存哪（conversation 表加字段 vs 新表）留实现期定
