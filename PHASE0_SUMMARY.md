# Phase 0 紧急修复总结

## 问题诊断

### 1. API Key 配置问题
**现象**：e2e 测试无法调用 LLM，因为 GLM_API_KEY 未传递给子进程

**根因**：
- `scripts/dev/e2e.sh` 从 `.env.local` source 环境变量（行33-38）
- 但 `go run ./cmd/e2e` 子进程不继承未 export 的变量
- 旧代码只 export 了 `LIUSHA_API_KEY`，没有 export `GLM_API_KEY`

### 2. 数据库节点类型
**确认**：数据库已支持完整的 **5 个**节点类型（migration 0134）
```sql
kind IN (
    'objective',      -- 1. 目标
    'action',         -- 2. 动作
    'observation',    -- 3. 观察
    'evaluation',     -- 4. 评估 ✅ 之前遗漏
    'result'          -- 5. 结果
)
```

### 3. 执行流程确认
**完整链路**：
```
用户 POST /chat {brief}
  ↓
httpapi.chatHandler (conversation_handler.go:76)
  ↓
api.StartChatScan(brief)
  ├─ conversations.CreateConversation()        // 建会话
  ├─ conversations.AppendMessage()             // 落用户消息
  ├─ msgclass.Classify()                       // 意图分流（light LLM）
  │  ├─ qa → chat.Answer() 纯聊天
  │  └─ action → 继续下面
  └─ createScan(brief, conversationID)
     ├─ assignments.Create()                   // 建 assignment
     └─ expandItem()
        ├─ tasks.Create()                      // 建 task
        ├─ executors.Create()                  // 建 agent_run (planner)
        └─ enq.Enqueue(Payload{ConversationID}) // 入队（带会话ID）
           ↓
Runner 消费队列
  ↓
handler.Run() → handleCognition()
  ↓
runCognition(assignmentID, taskID, virtualHost)
  ├─ 初始化：registry + knowledgegraph + eventBus
  ├─ 启动四Agent（并发）
  │  ├─ PlannerAgent → objective
  │  ├─ ExecutorAgent → action + observation
  │  ├─ EvaluatorAgent → evaluation
  │  └─ MonitorAgent
  └─ detector.Start() 等待完成
     └─ 验证通过的 observation → result → finding 表
```

## 已完成修复

### 修改文件：`scripts/dev/e2e.sh`

**新增代码**（行47-50）：
```bash
# LLM Provider API Keys（从 .env.local 继承，显式 export 给子进程）
export GLM_API_KEY="${GLM_API_KEY}"
export LIUSHA_LLM_FALLBACK="${LIUSHA_LLM_FALLBACK:-glm}"
export LIUSHA_LLM_KEY_SECRET="${LIUSHA_LLM_KEY_SECRET}"
```

**作用**：
1. 显式 export `GLM_API_KEY`，子进程能读到
2. 默认 fallback provider 设为 `glm`（不再用 MIMO）
3. export 加密密钥（用于解密 DB 存储的 API Key）

## 验收步骤

### 1. 确认 .env.local 配置
```bash
# 检查配置文件
cat .env.local | grep -E "GLM_API_KEY|LIUSHA_LLM_FALLBACK|LIUSHA_LLM_KEY_SECRET"

# 预期输出：
# GLM_API_KEY=your-actual-glm-key-here
# LIUSHA_LLM_FALLBACK=glm
# LIUSHA_LLM_KEY_SECRET=your-32-bytes-hex
```

如果缺失，添加：
```bash
# 生成加密密钥
openssl rand -hex 32

# 编辑 .env.local
echo "GLM_API_KEY=your-actual-glm-key-here" >> .env.local
echo "LIUSHA_LLM_FALLBACK=glm" >> .env.local
echo "LIUSHA_LLM_KEY_SECRET=$(openssl rand -hex 32)" >> .env.local
```

### 2. 测试单个 Profile
```bash
# 跑最简单的 active profile（自然语言任务）
./scripts/dev/e2e.sh active:xss

# 预期：
# - 创建会话
# - 意图分流判断为 action
# - 下发 task
# - 四Agent 启动
# - 至少发现 3 个 XSS 漏洞（minFindings=3）
# - 输出 "🎉 e2e PASS"
```

### 3. 查看执行日志
```bash
# Runner 日志（看四Agent协作）
tail -f logs/runner.log

# API 日志（看对话流程）
tail -f logs/api.log

# 查看数据库（验证知识图谱节点）
docker exec liusha-postgres psql -U liusha -d liusha -c \
  "SELECT kind, COUNT(*) FROM wm_node GROUP BY kind ORDER BY kind;"

# 预期输出：
#     kind     | count
# -------------+-------
#  action      |     8
#  evaluation  |     5
#  objective   |     2
#  observation |     8
#  result      |     3
```

## 下一步计划

### Phase 1: 知识图谱 API（1 周）
实现 3 个新端点：
- `GET /api/v1/tasks/{taskId}/graph` - 完整图谱
- `GET /api/v1/tasks/{taskId}/nodes?kind=objective` - 按类型筛选
- `GET /api/v1/tasks/{taskId}/stats` - 快速统计（e2e 专用）

### Phase 2: E2E 测试重构（1-2 周）
- 重构验收标准：从 `minFindings` 改为 `minObjectives/Actions/Results`
- 新增 `mustHavePaths` 验证推理链完整性
- 更新所有 18 个 profiles（13 passive + 5 active）

### Phase 3: 向后兼容（可选，3 天）
- 旧 `/api/v1/tasks/{taskId}/findings` 适配新数据源
- 前端无感知切换

## 关键设计决策

### 1. 为什么用对话作为入口？
- **用户视角**：系统是"安全助手"，对话是最自然的交互方式
- **意图分流**：避免把闲聊/答疑误判成扫描任务（省钱）
- **上下文管理**：会话绑定 task，支持多轮续接（追加指令）

### 2. 为什么验收标准要看知识图谱？
- **旧标准盲区**：只看 finding 数量，LLM 可能"猜"对但没推理链
- **新标准优势**：验证完整认知循环 `objective → action → observation → evaluation → result`
- **可调试性**：出错时能追溯到具体哪一步卡住（Planner 没生成目标？Executor 没执行？）

### 3. 为什么是 5 个节点类型而不是 4 个？
- `objective` - Planner 生成的目标
- `action` - Executor 执行的动作
- `observation` - Executor 记录的观察结果
- `evaluation` - Evaluator 的验证评估 ⭐ 关键环节
- `result` - 确认的发现（写入 finding 表）

**evaluation 的作用**：
- 晋升门机制：只有通过 Evaluator 验证的 observation 才能晋升为 result
- 防止误报：避免 Executor "猜"出的漏洞直接进 finding 表
- 可追溯：每个 result 都能追溯到对应的 evaluation 和 observation

## 参考文档

- [完整迁移计划](docs/E2E_MIGRATION_PLAN_UPDATED.md)
- [原始迁移计划](docs/E2E_MIGRATION_PLAN.md)
- [知识图谱架构](docs/KNOWLEDGE_GRAPH.md)
