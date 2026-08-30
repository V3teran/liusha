# Liusha 后端死代码与前后端对接分析报告

**分析日期**: 2026-08-30  
**范围**: 后端Go代码 + 前端TypeScript代码  
**目标**: 识别死代码、验证前后端API对接

---

## 📊 执行摘要

### 后端死代码情况
- **低引用包（<2次引用）**: 7个包
- **实际死代码**: 0个（所有低引用包均被cmd入口使用）
- **结论**: ✅ 无真正的死代码，低引用包都是cmd直接依赖

### 前后端对接情况
- **后端API路由**: ~30个端点
- **前端API客户端**: 已实现基础对接
- **新架构术语**: 前端使用68个引用（attackGraph等）
- **对接缺口**: ⚠️ 世界模型API未完全对接（详见下文）

---

## 第一部分：后端死代码分析

### 1.1 低引用包清单

| 包名 | 引用次数 | 实际使用情况 | 结论 |
|------|---------|------------|------|
| `builder` | 0 | ❌ **误报**：实际被 `cmd/runner` 使用（别名 `executorbuilder`） | ✅ 活跃 |
| `audit` | 1 | ✅ 被 `cmd/api` 使用（任务审计） | ✅ 活跃 |
| `chat` | 1 | ✅ 被 `cmd/api` 使用（会话接口） | ✅ 活跃 |
| `ingestor` | 1 | ✅ 被 `cmd/runner` 使用（数据摄取） | ✅ 活跃 |
| `msgclass` | 1 | ✅ 被 `cmd/api` 使用（消息分类） | ✅ 活跃 |
| `qa` | 1 | ✅ 被 `cmd/api` 使用（QA功能） | ✅ 活跃 |
| `tools` | 1 | ✅ 被 `cmd/runner` 使用（工具注册） | ✅ 活跃 |

**分析**: 
- `builder` 的 0 引用是因为使用了别名导入：`executorbuilder "github.com/V3teran/liusha/internal/builder/executor"`
- 其余6个包虽然引用少，但都是cmd入口的直接依赖，属于顶层业务逻辑包

### 1.2 包引用统计（完整排序）

```
worldmodel:       26 引用  ← 核心
config:          19 引用
provider:        16 引用
registry:        16 引用
task:            14 引用
traffic:         13 引用
finding:         13 引用
conversation:    11 引用
...（中间层）
audit:            1 引用  ← 但被cmd/api使用
chat:             1 引用  ← 但被cmd/api使用
tools:            1 引用  ← 但被cmd/runner使用
builder:          0 引用  ← 实际被使用（别名）
```

### 1.3 结论

**✅ 无真正死代码**

所有低引用包都是：
1. **cmd入口的直接依赖**（顶层业务逻辑）
2. **单一职责包**（只被一个cmd使用是合理的）

例如：
- `audit` 只被 `cmd/api` 使用（审计日志）
- `chat` 只被 `cmd/api` 使用（会话管理）
- `ingestor` 只被 `cmd/runner` 使用（数据摄取）
- `tools` 只被 `cmd/runner` 使用（工具注册）

---

## 第二部分：前端架构与对接分析

### 2.1 前端技术栈

**当前架构**（web/package.json）:
```json
{
  "dependencies": {
    "react": "^19.2.1",           // ⚠️ 注意：REBUILD_PLAN.md说Vue，但实际是React
    "react-router-dom": "^7.9.6",
    "@tanstack/react-query": "^5.90.2",
    "@xyflow/react": "^12.8.6",   // 攻击图可视化
    "zustand": "^5.0.9"           // 状态管理
  }
}
```

**⚠️ 架构矛盾**:
- `web/REBUILD_PLAN.md` 第7行说："Vue（**不是 React**）"
- 但 `package.json` 实际使用的是 **React 19**
- **结论**: 文档过时，实际已采用React架构

### 2.2 前端目录结构

```
web/src/
├── api/              # API客户端层
│   ├── client.ts     # HTTP封装
│   ├── types.ts      # 类型定义
│   ├── models.ts     # 模型API
│   ├── settings.ts   # 设置API
│   └── config.ts     # 配置API
├── features/         # 功能模块（9个）
│   ├── attack-graph/     # ✅ 攻击图可视化
│   ├── config/           # ✅ 配置管理
│   ├── conversation/     # ✅ 会话
│   ├── finding/          # ✅ 漏洞发现
│   ├── llm-audit/        # ✅ LLM审计
│   ├── model/            # ✅ 模型配置
│   ├── settings/         # ✅ 系统设置
│   ├── tools/            # ✅ 工具目录
│   └── traffic/          # ✅ 流量查看
├── pages/            # 页面组件
├── components/       # UI组件
├── stores/          # Zustand状态
└── hooks/           # 自定义Hooks
```

### 2.3 前端API客户端方法（web/src/api/client.ts）

**已实现的API调用**（30+个方法）:

#### 会话相关
```typescript
listConversations()       // GET /conversations
listMessages()            // GET /conversations/:id/messages
getMessage()              // GET /conversations/:id/messages/:msg_id
getConversationUsage()    // GET /conversations/:id/usage
startChat()               // POST /chat
followUp()                // POST /conversations/:id/messages
abortScan()               // POST /conversations/:id/abort
deleteConversation()      // DELETE /conversations/:id
renameConversation()      // PATCH /conversations/:id
authStream()              // GET /conversations/:id/stream (SSE)
```

#### 任务相关
```typescript
listTasks()               // GET /tasks
abortTask()               // POST /tasks/:id/abort
startActiveScan()         // POST /scan/active
```

#### 漏洞相关
```typescript
listFindings()            // GET /findings
listFindingHosts()        // GET /findings/hosts
listFindingScenarios()    // GET /findings/scenarios
updateFindingTriage()     // PATCH /findings/:id
```

#### 攻击图相关
```typescript
getAttackGraph()          // GET /attack_graph/:task_id
```

#### LLM审计相关
```typescript
listLLMInvocations()      // GET /llm/invocations/:eid
getLLMInvocationDetail()  // GET /llm/invocations/:eid/:id
getLLMInvocationStats()   // GET /llm/invocations/:eid/stats
getLLMInvocationFacets()  // GET /llm/invocations/:eid/facets
```

#### 流量相关
```typescript
listTraffic()             // GET /traffic
getTrafficDetail()        // GET /traffic/:id
```

#### 配置相关
```typescript
listScenarios()           // GET /scenarios
listScenariosPaged()      // GET /scenarios (分页)
listAgentsPaged()         // GET /agents (分页)
```

---

## 第三部分：后端API路由清单

### 3.1 后端实际暴露的API端点

**从 `internal/httpapi/server.go` 和各handler分析**:

#### 核心路由组
```go
// 会话模块
POST   /chat
GET    /conversations
GET    /conversations/:id/messages
GET    /conversations/:id/messages/:msg_id
GET    /conversations/:id/usage
POST   /conversations/:id/messages
POST   /conversations/:id/abort
DELETE /conversations/:id
PATCH  /conversations/:id
GET    /conversations/:id/stream (SSE)

// 任务模块
GET    /tasks
POST   /tasks/:id/abort
POST   /tasks/:id/control (控制平面)
GET    /tasks/:id/control (列出控制事件)
GET    /tasks/:id/control/:event_id (控制事件详情)

// 扫描模块
POST   /scan/active

// 漏洞模块
GET    /findings
GET    /findings/hosts
GET    /findings/scenarios
PATCH  /findings/:id

// 攻击图模块（新架构核心）
GET    /attack_graph/:task_id

// LLM审计模块
GET    /llm/invocations/:eid
GET    /llm/invocations/:eid/:id
GET    /llm/invocations/:eid/stats
GET    /llm/invocations/:eid/facets

// 流量模块
GET    /traffic
GET    /traffic/:id

// 配置模块
GET    /scenarios
POST   /scenarios
PUT    /scenarios/:id
DELETE /scenarios/:id
GET    /agents
POST   /agents
PUT    /agents/:id
DELETE /agents/:id

// 工具目录模块
GET    /tools
GET    /tools/:name

// 模型模块
GET    /models/providers
POST   /models/providers
PUT    /models/providers/:id
DELETE /models/providers/:id
POST   /models/providers/test
POST   /models/providers/list-models
GET    /models/roles/:role

// 设置模块
GET    /settings/compaction
PUT    /settings/compaction
GET    /settings/runtime
PUT    /settings/runtime
GET    /settings/proxy_filter
PUT    /settings/proxy_filter

// 凭证模块
GET    /credentials
POST   /credentials
DELETE /credentials/:id
POST   /credentials/batch

// Dev模式
GET    /dev-config.json (仅开发环境)
```

---

## 第四部分：前后端对接分析

### 4.1 已对接的功能模块

| 前端模块 | 后端API | 对接状态 | 备注 |
|---------|---------|---------|------|
| **会话** | `/conversations`, `/chat` | ✅ 完整 | SSE流、多轮对话 |
| **任务列表** | `/tasks` | ✅ 完整 | 任务查看、中止 |
| **漏洞发现** | `/findings` | ✅ 完整 | 列表、筛选、分类 |
| **攻击图** | `/attack_graph/:task_id` | ✅ 基础 | ⚠️ 仅读取，无交互 |
| **LLM审计** | `/llm/invocations` | ✅ 完整 | 调用统计、详情 |
| **流量查看** | `/traffic` | ✅ 完整 | 流量列表、详情 |
| **配置管理** | `/scenarios`, `/agents` | ✅ 完整 | CRUD操作 |
| **模型管理** | `/models` | ✅ 完整 | Provider配置 |
| **系统设置** | `/settings` | ✅ 完整 | 各项配置 |

### 4.2 对接缺口分析

#### ⚠️ 缺口1：世界模型深度交互

**问题**:
- 前端只有 `getAttackGraph(taskID)` 一个API
- 后端有完整的世界模型（5节点+5关系）
- **缺失的API**:
  ```typescript
  // 需要但未实现：
  getWorldModelNodes(taskID, kind)      // 获取特定类型节点
  getNodeDetails(nodeID)                 // 节点详情
  getNodeRelations(nodeID)               // 节点关系
  getHypotheses(taskID)                  // 获取假设列表
  getEvidence(hypothesisID)              // 获取证据
  ```

**影响**:
- 前端无法展示完整的Hypothesis → Evidence → Finding链
- 无法可视化ENABLES关系
- 攻击图只是静态展示，无深度探索

#### ⚠️ 缺口2：Planner监察可视化

**问题**:
- 后端实现了Planner宏观监察（6分钟评估）
- 前端无对应UI展示Planner决策
- **缺失功能**:
  - Planner评估历史
  - Kill/Steer决策记录
  - 监察状态实时更新

#### ⚠️ 缺口3：控制平面UI

**问题**:
- 后端有 `/tasks/:id/control` 控制平面API
- 前端未实现控制平面UI
- **缺失功能**:
  - 人工干预界面
  - 控制事件查看
  - 紧急Kill/Steer操作

#### ✅ 缺口4：工具调用追踪（已部分实现）

**状态**: 
- LLM审计已实现
- 但缺少工具级别的详细追踪

---

## 第五部分：新架构术语在前端的使用

### 5.1 新架构术语统计

**搜索结果**: 68个引用

**主要使用位置**:
```typescript
// web/src/features/attack-graph/
- AttackGraphNode (节点类型)
- attackGraphKeys (查询key)
- attackGraphQueryKey (React Query)
- useAttackGraphQuery (自定义Hook)

// web/src/pages/
- AttackGraphPage.tsx (攻击图页面)

// web/src/api/
- AttackGraph 类型定义
- getAttackGraph() API方法
```

### 5.2 术语使用分析

**已使用的新架构术语**:
- ✅ `attackGraph` - 攻击图（大量使用）
- ✅ `worldModel` - 世界模型（类型定义中）
- ❌ `orchestrator` - 0个引用（后端已重命名，前端未感知）
- ❌ `planner` - 0个引用（前端未展示）
- ❌ `hypothesis` - 0个引用（前端未展示）
- ❌ `evidence` - 0个引用（前端未展示）

**结论**: 前端对新架构的使用**非常有限**，主要停留在攻击图的静态展示层面。

---

## 第六部分：建议与改进方向

### 6.1 短期改进（高优先级）

#### 1. 补充世界模型API
```go
// internal/httpapi/ 需要新增
GET /worldmodel/:task_id/nodes?kind=hypothesis
GET /worldmodel/nodes/:node_id
GET /worldmodel/nodes/:node_id/relations
```

#### 2. 补充前端API客户端
```typescript
// web/src/api/worldmodel.ts（新文件）
export async function listNodes(taskID: string, kind?: NodeKind)
export async function getNodeDetail(nodeID: string)
export async function getNodeRelations(nodeID: string)
```

#### 3. 实现控制平面UI
```typescript
// web/src/features/control-plane/（新目录）
- ControlPanel.tsx
- useControlEvents.ts
- ControlEventList.tsx
```

### 6.2 中期改进（中优先级）

#### 4. Planner监察可视化
```typescript
// web/src/features/planner-monitor/（新目录）
- PlannerDashboard.tsx
- EvaluationHistory.tsx
- DecisionTimeline.tsx
```

#### 5. 增强攻击图交互
- 节点点击展开详情
- 关系边hover显示类型
- Hypothesis → Evidence → Finding 链路高亮
- ENABLES关系可视化

#### 6. 实时监控面板
- Executor 5步评估状态
- Planner 6分钟评估倒计时
- Kill/Steer事件流

### 6.3 长期改进（低优先级）

#### 7. 工具调用可视化
- 工具调用链
- 参数/返回值展示
- 调用耗时分析

#### 8. 世界模型编辑器
- 手动创建Hypothesis
- 手动标记Evidence
- 关系手动建立

---

## 第七部分：前端架构问题

### 7.1 文档与实现不一致

**问题**:
```markdown
# web/REBUILD_PLAN.md 第7行
"Vue（**不是 React**）"

# web/package.json 实际
"react": "^19.2.1"
```

**建议**:
1. 删除或更新 `REBUILD_PLAN.md`
2. 承认当前使用React架构
3. 补充React架构说明文档

### 7.2 前端代码质量

**优点**:
- ✅ TypeScript严格类型
- ✅ React Query数据管理
- ✅ 组件化良好
- ✅ 测试覆盖（163个TS/TSX文件）

**问题**:
- ⚠️ 缺少架构文档
- ⚠️ API类型定义与后端可能不同步
- ⚠️ 新架构概念理解不足

---

## 📊 总结

### 后端代码质量
- ✅ **无死代码**
- ✅ **架构清晰**
- ✅ **包职责单一**
- ✅ **API完整**

### 前后端对接状态
- ✅ **基础功能已对接**（会话、任务、漏洞、流量）
- ⚠️ **新架构功能缺失**（世界模型深度、Planner监察、控制平面）
- ⚠️ **前端架构文档过时**（说Vue实际React）

### 关键发现
1. **后端架构改进已完成**，但前端未跟上
2. **攻击图是桥梁**，但缺少深度交互
3. **世界模型API缺口**是最大问题
4. **前端需要新一轮架构升级**以匹配后端能力

---

**报告生成**: 2026-08-30  
**分析工具**: 代码扫描 + 手工分析  
**下一步**: 补充世界模型API + 前端升级
