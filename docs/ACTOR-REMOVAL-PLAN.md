# Actor 层删除与适配方案

## 🎯 目标

删除 `actor.Move`、`actor.Landmark` 等抽象，直接使用 `worldmodel.Node`，保留执行引擎的必要组件。

---

## 📊 影响分析

### Actor 包的核心类型

#### 需要删除的（与世界模型重叠）
1. ✅ **actor.Move** → `worldmodel.Node (kind=move)`
2. ✅ **actor.Landmark** → `worldmodel.Node` (通用节点)
3. ✅ **actor.LandmarkKind** → `worldmodel.NodeKind`
4. ✅ **actor.LandmarkRef** → `worldmodel.TargetRef`
5. ✅ **actor.Finding** → `finding.VulnFinding` (已有独立包)

#### 需要保留的（执行引擎核心）
1. ✅ **actor.Complexity** → 移到 `worldmodel.Complexity`
2. ✅ **actor.Budget** → 执行资源配额（steps/tokens/timeout）
3. ✅ **actor.Execution** → LLM 执行结果（steps/conclusion）
4. ✅ **actor.Checkpoint** → 执行断点续传
5. ✅ **actor.Compactor** → 上下文压缩
6. ✅ **actor.LLMCritic** → 执行质量评估
7. ✅ **actor.Campaign** → 扫描策略
8. ✅ **actor.Assignment** → 批量下发（与 DB assignment 表对应）
9. ✅ **actor.Target** → 目标定义（与 DB task 表对应）

---

## 🔄 重构策略

### 阶段 1: 类型迁移

#### 1.1 Move → worldmodel.Node
- **删除**: `actor.Move` 结构体
- **替换**: 所有 `actor.Move` 使用 `worldmodel.Node`
- **适配点**:
  - `dispatcher.Execute(move actor.Move)` → `Execute(node worldmodel.Node)`
  - `nodeToActorMove()` → 删除此转换函数

#### 1.2 Complexity 统一
- **迁移**: `actor.Complexity` → `worldmodel.Complexity`
- **影响**:
  - `internal/actor/types.go` 删除 Complexity 定义
  - `internal/dispatcher` 使用 `worldmodel.Complexity`
  - `internal/provider` 路由使用 `worldmodel.Complexity`

#### 1.3 Landmark 删除
- **删除**: `actor.Landmark` 相关所有代码
- **影响**:
  - `internal/ledger/ledger.go` 需要重写（使用 worldmodel.Node）
  - `internal/ingester/traffic.go` 需要适配

---

### 阶段 2: Dispatcher 重构

#### 当前设计
```go
type Profile struct {
    Complexity    actor.Complexity
    SystemPrompt  string
    Tools         []string
    Budget        actor.Budget
    // ...
}

func (d *Dispatcher) Execute(ctx context.Context, move actor.Move) ([]actor.Execution, error)
```

#### 新设计
```go
type Profile struct {
    Complexity    worldmodel.Complexity  // 使用 worldmodel
    SystemPrompt  string
    Tools         []string
    Budget        Budget  // 保留在 dispatcher 包或新建 execution 包
    // ...
}

func (d *Dispatcher) Execute(ctx context.Context, node worldmodel.Node) ([]Execution, error)
```

---

### 阶段 3: Ledger 重写

#### 当前问题
- `internal/ledger/ledger.go` 使用 `actor.Landmark`
- `worldmodel/ledger_adapter.go.bak` 是旧适配层

#### 新设计：删除 ledger 包
**理由**:
- Ledger 是 Actor 层访问世界模型的适配器
- 删除 Actor 后，直接使用 `worldmodel.Store` 即可
- 简化架构，减少一层抽象

**替换方案**:
```go
// 旧代码
ledger := ledger.New(worldStore.AsLedgerStore())
ledger.Write(landmark)

// 新代码
worldStore.CreateNode(ctx, node)
```

---

### 阶段 4: 文件重组

#### 保留并重构的文件
1. `internal/actor/actor.go` → 重命名为 `internal/execution/engine.go`
   - 保留 Actor 执行引擎
   - 移除 Move/Landmark 引用
   
2. `internal/actor/checkpoint.go` → 移到 `internal/execution/checkpoint.go`
   
3. `internal/actor/types.go` → 拆分
   - Budget/Execution/Campaign → `internal/execution/types.go`
   - 删除 Move/Landmark/Finding

#### 删除的文件
1. `internal/ledger/` 整个包（直接用 worldmodel.Store）
2. `internal/worldmodel/ledger_adapter.go.bak`

---

## 📝 详细实施步骤

### Step 1: 迁移 Complexity (最小影响)
1. 将 `actor.Complexity` 常量移到 `worldmodel/model.go`
2. 全局替换 `actor.Complexity` → `worldmodel.Complexity`
3. 编译验证

### Step 2: 删除 Landmark (中等影响)
1. 删除 `actor.Landmark` 定义
2. 删除 `internal/ledger` 包
3. 修改引用点直接使用 `worldmodel.Node`

### Step 3: 删除 actor.Move (核心重构)
1. 修改 `dispatcher.Execute` 签名
2. 删除 `nodeToActorMove` 转换函数
3. `handler_run.go` 直接传递 `worldmodel.Node`

### Step 4: 重组 execution 包
1. 创建 `internal/execution` 包
2. 移动保留的类型和执行引擎
3. 更新所有 import

### Step 5: 清理测试文件
1. 删除所有旧 NodeKind 引用（KindTarget/KindFinding）
2. 更新集成测试使用新 NodeKind

---

## ⚠️ 风险与缓解

### 风险 1: dispatcher 依赖 actor.Move
- **缓解**: 先统一 Complexity，再逐步替换 Move

### 风险 2: 大量文件需要同时修改
- **缓解**: 分步骤，每步编译验证

### 风险 3: 测试文件大量失败
- **缓解**: 先修改主逻辑，最后批量修复测试

---

## ✅ 验证清单

- [ ] `go build ./...` 编译通过
- [ ] 单元测试通过
- [ ] 集成测试通过
- [ ] `internal/actor` 包不再被引用（除了保留的 execution 部分）
- [ ] `internal/ledger` 包完全删除
- [ ] 所有旧 NodeKind 删除

---

## 🎯 预期结果

### 架构简化
```
删除前:
worldmodel.Node → actor.Move → dispatcher.Execute
                ↓
            actor.Landmark → ledger → worldmodel.Store

删除后:
worldmodel.Node → dispatcher.Execute (直接使用)
worldmodel.Node → worldmodel.Store (直接使用)
```

### 包结构
```
internal/
├── worldmodel/          # 统一的世界模型
│   ├── model.go        # Node + Complexity + NodeKind
│   └── store.go        # 持久化
├── execution/          # 执行引擎（原 actor 核心）
│   ├── engine.go       # LLM Actor 执行循环
│   ├── checkpoint.go   # 断点续传
│   └── types.go        # Budget/Execution/Campaign
├── dispatcher/         # Complexity → Profile 路由
│   ├── dispatcher.go   # 使用 worldmodel.Node
│   └── profile/        # Profile 注册
└── (删除 ledger/)
```

---

## 下一步

等待你的确认，然后开始实施：
1. 确认这个方案是否符合预期？
2. 是否有需要调整的地方？
3. 确认后立即开始执行 Step 1
