# liusha 架构文档

**最后更新**: 2026-08-27  
**版本**: v2.0（架构一致性重构后）

---

## 📐 核心架构

### 隔离边界

liusha 有 3 个关键隔离边界：

```
┌─────────────────────────────────────────┐
│  Assignment (租户隔离)                   │
│  - 一次批量下发的测试任务                 │
│  - Lead 黑板按 assignment 隔离           │
│  - 同一批测试的多个 task 共享情报         │
├─────────────────────────────────────────┤
│  ├─ Task A (世界模型隔离)                │
│  │  - 一个测试目标                       │
│  │  - 独立的认知图 (wm_node + wm_edge)  │
│  │  - 完整的追溯链                       │
│  ├─ Task B                              │
│  └─ Task C                              │
└─────────────────────────────────────────┘
```

**数据模型**：
- 1 Assignment → N Task（批量下发多个目标）
- 1 Task → 1 世界模型（wm_node + wm_edge 按 task_id 隔离）
- 1 Task → 1 目标（Target），完整的认知过程独立追溯

---

## 🧠 世界模型（World Model）

### 统一图模型

所有认知单元存储在 `wm_node` 表，通过 `kind` 字段区分：

```
wm_node (统一节点表)
├─ objective   : 用户设定的目标
├─ move        : Planner 生成的执行计划
├─ observation : Executor 产出的观察记录
└─ discovery   : 重要的安全发现（漏洞等）

wm_edge (关系边表)
├─ produces    : Move → Observation/Discovery
├─ supports    : Observation → Discovery
├─ blocks      : 阻塞关系
├─ enables     : 使能关系
└─ depends_on  : 依赖关系
```

### 节点字段

| 字段 | 类型 | 说明 | 适用节点 |
|------|------|------|----------|
| `id` | text | 节点 ID（UUID） | 全部 |
| `task_id` | text | 所属 task（隔离边界） | 全部 |
| `kind` | node_kind | 节点类型（4 种枚举） | 全部 |
| `content` | jsonb | 灵活载荷 | 全部 |
| `state` | node_state | open/running/done | **Move 专用** |
| `complexity` | complexity_level | trivial/simple/moderate/complex/extreme | **Move 专用** |
| `confidence` | confidence_level | unverified/verified | **Observation/Discovery 专用** |
| `priority` | int | 优先级 | 全部 |
| `depends_on` | text[] | Move 依赖的其他 Move ID | Move 可选 |
| `source_type` | text | 溯源类型（user/planner/executor/verifier） | 全部 |
| `source_id` | text | 溯源 ID | 全部 |
| `created_at` | timestamptz | 创建时间 | 全部 |
| `updated_at` | timestamptz | 更新时间 | 全部 |

### 类型安全

数据库约束保证：
- `state` 和 `complexity` 只能在 `kind=move` 时非空
- `confidence` 只能在 `kind IN (observation, discovery)` 时非空
- 互斥性由数据库层强制

---

## 🎯 Actor 层（执行单元）

### Complexity 驱动

**废除 MoveKind**，统一使用 `Complexity` 决定资源配额：

```go
type Move struct {
    Instruction string      // 执行指令（原 Objective）
    Complexity  Complexity  // 复杂度（决定预算/工具集/模型）
}
```

### Complexity 级别

| Complexity | 预算 | 工具集 | 典型场景 |
|------------|------|--------|----------|
| `trivial` | <5 步 | 基础工具 | 单个工具调用 |
| `simple` | ~10 步 | 常用工具 | 简单扫描 |
| `moderate` | ~30 步 | 丰富工具 | 中等测试 |
| `complex` | ~50 步 | 全工具集 | 复杂利用 |
| `extreme` | ~100 步 | 全工具集 | 深度挖掘 |

### Profile 注册

```go
// dispatcher/profile/profiles.go
func RegisterAll(d *dispatcher.Dispatcher) {
    d.Register(provider.ComplexityTrivial, trivialProfile())
    d.Register(provider.ComplexitySimple, simpleProfile())
    d.Register(provider.ComplexityModerate, moderateProfile())
    d.Register(provider.ComplexityComplex, complexProfile())
    d.Register(provider.ComplexityExtreme, extremeProfile())
}
```

---

## 📝 Lead 黑板（情报共享）

### Assignment 级别隔离

```sql
CREATE TABLE lead (
    id uuid PRIMARY KEY,
    assignment_id text NOT NULL,  -- 隔离边界
    kind text NOT NULL,            -- clue/observation/deadend
    detail text NOT NULL,          -- 一句话描述
    executor_id text,              -- 产出者
    source_task_id text,           -- 来源 task
    created_at timestamptz NOT NULL
);
```

### Kind 类型

| Kind | 说明 | 示例 |
|------|------|------|
| `clue` | 可疑点待验证 | "发现目录 /admin 返回 200" |
| `observation` | 既成发现 | "确认 /admin 无鉴权" |
| `deadend` | 死路绕开 | "/api/v1 全是 404，不用试了" |

### 隔离逻辑

```
✅ 按 assignment 隔离：
   - 同一批测试的多个 task 共享情报
   - 不同客户的测试完全隔离（租户隔离）
   
✅ 跨 task 情报同步：
   - task-1 的发现可以给 task-2 提供线索
   - 子代理看不到父代理上下文时使用

❌ 不按 host 隔离：
   - host 不是隔离维度
   - host 是数据维度（detail 字段包含 host 信息）
```

---

## 🔄 数据流

### 完整认知循环

```
用户目标 (objective)
    ↓
PlannerAgent 生成计划 (move, state=open)
    ↓
ExecutionLoop 轮询执行 (state: open → running)
    ↓
Executor 调用 LLM + 工具 (产出 observation)
    ↓
Verifier 复现验证 (晋升为 discovery, confidence=verified)
    ↓
Move 完成 (state: running → done)
    ↓
EventBus 触发事件
    ↓
PlannerAgent 重新规划（循环）
```

### Move 状态转换

```
open (待执行)
  ↓
running (执行中)
  ↓
done (已完成)
```

### Confidence 晋升

```
Executor 产出
  → observation (confidence=unverified)
     ↓
Verifier 复现坐实
  → discovery (confidence=verified)
```

---

## 🧩 组件职责

### PlannerAgent
- 订阅 EventBus 事件
- 调用 LLM 工具（observe_state / propose_moves / evaluate_progress）
- 生成新的 Move 节点

### ExecutionLoop
- 轮询 `state=open` 的 Move
- 依赖调度（拓扑排序）
- 状态转换：open → running → done
- 创建溯源边：move --produces--> observation

### Executor
- 调用 LLM Agent（基于 eino）
- 执行工具调用（web 扫描/利用）
- 产出 Attempt（待验证的发现）

### Verifier
- 复现验证（Replay）
- 记录验证结果（wm_verification 表）
- 坐实后晋升为 verified 节点

---

## 📊 API 接口

### 攻击图查询

```
GET /attack_graph/:task_id

返回：
{
  "task_id": "task-123",
  "scan_id": "assignment-456",
  "nodes": [
    {
      "id": "obj-1",
      "kind": "objective",
      "content": {...},
      "priority": 5
    },
    {
      "id": "move-1",
      "kind": "move",
      "content": {...},
      "state": "done",
      "complexity": "moderate",
      "priority": 8
    }
  ],
  "edges": [
    {
      "source": "move-1",
      "rel": "produces",
      "target": "obs-1"
    }
  ]
}
```

---

## 🔐 安全边界

### 租户隔离

- **Assignment 级别**：Lead 黑板隔离，不同客户不泄露
- **Task 级别**：世界模型隔离，每个目标独立追溯

### 数据溯源

- 每个节点有 `source_type` + `source_id`
- 可追溯到：user/planner/executor/verifier
- wm_verification 表记录完整验证证据链

---

## 📁 目录结构

```
internal/
├── actor/             # 执行单元（Move + Complexity）
├── cognition/         # 认知循环（ExecutionLoop + EventBus）
├── dispatcher/        # Complexity → Profile 路由
├── executor/          # 域适配器（web/binary/cloud）
├── lead/              # 情报黑板（PostgreSQL）
├── planneragent/      # 规划代理（LLM 工具调用）
├── verifier/          # 验证门（Replay 复现）
└── worldmodel/        # 世界模型（统一图存储）

cmd/runner/
├── main.go            # 主入口
├── handler.go         # 任务处理
├── handler_run.go     # 执行逻辑
├── cognition.go       # 认知循环初始化
└── planner_manager.go # PlannerAgent 生命周期

db/migrations/
├── 0120_*.sql         # 索引重命名
├── 0121_*.sql         # Move → Node 合并
└── 0122_*.sql         # Lead 表创建
```

---

## 🚀 部署

### 数据库 Migrations

```bash
migrate -path db/migrations -database "$DATABASE_URL" up
```

### 环境变量

```bash
LIUSHA_POSTGRES_DSN=postgres://user:pass@host:5432/liusha
LIUSHA_REDIS_ADDR=localhost:6379
LIUSHA_CONFIG=./config/config.yaml
```

### 启动服务

```bash
./cmd/runner/runner
```

---

## 📖 参考

- [COMPLETION-SUMMARY.md](./COMPLETION-SUMMARY.md) - 世界模型重构总结
- [REFACTOR-SUMMARY-20260826.md](./REFACTOR-SUMMARY-20260826.md) - 架构一致性重构总结
- [P2 运行时验证](../scripts/p2_runtime_verification.sh) - 集成测试脚本
