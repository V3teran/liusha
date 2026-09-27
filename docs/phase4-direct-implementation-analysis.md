# 直接实现 Phase 4 全局调度器的可行性分析

## Phase 4 vs Phase 1 对比

### 复杂度对比

| 维度 | Phase 1: 单容器 | Phase 4: 全局调度器 | 差异 |
|------|----------------|-------------------|------|
| **代码量** | ~200 行 | ~2000 行 | **10x** |
| **组件数** | 1 个 (SingletonSandbox) | 8+ 个 (Scheduler, Pool, Monitor, Scaler...) | **8x** |
| **依赖** | 无新增 | Redis/etcd (分布式锁), Prometheus, 消息队列 | **新增多个** |
| **测试复杂度** | 简单 (单元测试) | 高 (集成测试、压力测试、混沌测试) | **复杂** |
| **调试难度** | 低 | 高 (分布式系统调试) | **困难** |
| **上线风险** | 低 | 高 (涉及核心调度逻辑) | **高风险** |

---

## 直接实现 Phase 4 的收益分析

### 1. 当前 Liusha 的实际需求

**用户规模**：
- 当前：**0 用户**（还没有用户系统）
- 6个月内预期：**1-10 用户**（内部团队 + 少量早期用户）
- 1年内预期：**10-100 用户**

**并发需求**：
- 当前：**1 个任务**（手动触发 E2E 测试）
- 近期：**3-5 个并发任务**（开发测试）
- 未来：**10-50 个并发任务**（小规模生产）

**结论**：Phase 1 的单容器模式完全满足当前需求，Phase 4 是 **over-engineering**。

---

### 2. Phase 4 带来的额外价值

#### 2.1 性能提升
| 指标 | Phase 1 | Phase 4 | 提升幅度 | 当前是否需要？ |
|------|---------|---------|---------|---------------|
| 启动延迟 | 0 秒（预热） | 0 秒（预热） | **无差异** | ❌ |
| 并发能力 | 1-5 任务 | 100+ 任务 | **20x** | ❌ (当前只有 1 任务) |
| 资源利用率 | 70% | 90% | +20% | 🔸 (单容器已够用) |
| 故障恢复 | 手动重启 | 自动自愈 | **质的提升** | 🔸 (开发阶段可接受) |

#### 2.2 功能增强
| 功能 | Phase 1 | Phase 4 | 当前价值 |
|------|---------|---------|---------|
| 多租户隔离 | ❌ | ✅ | ❌ (无多用户) |
| 配额管理 | ❌ | ✅ | ❌ (无计费需求) |
| 优先级调度 | ❌ | ✅ | ❌ (无优先级区分) |
| 动态扩缩容 | ❌ | ✅ | ❌ (负载稳定) |
| 完整监控 | 基础日志 | Prometheus + Grafana | 🔸 (Nice to have) |

**结论**：Phase 4 的大部分高级功能在当前阶段 **用不上**。

---

### 3. 实现 Phase 4 的代价

#### 3.1 开发时间
```
Phase 1: 2-3 天
  - SingletonSandbox: 1 天
  - Handler 改造: 1 天
  - 测试: 0.5 天

Phase 4: 3-4 周
  - 调度器核心: 1 周
  - 容器池管理: 1 周
  - 监控/指标: 1 周
  - 测试/调试: 1 周
```

**时间成本**：Phase 4 是 Phase 1 的 **10x**。

#### 3.2 维护成本
- **Phase 1**：简单，1 个组件，易理解
- **Phase 4**：复杂，多个子系统，需要专人维护

#### 3.3 技术债务风险
**过度设计的风险**：
1. **需求变化**：未来可能不需要全局调度器（例如改用 Kubernetes）
2. **技术选型**：现在选的技术栈可能过时（Redis → etcd → Consul）
3. **重构困难**：复杂系统难以重构

**YAGNI 原则**（You Aren't Gonna Need It）：不要为未来可能的需求过度设计。

---

## 但是！Phase 4 的一些设计值得现在借鉴

虽然不建议直接实现完整的 Phase 4，但可以在 Phase 1 中 **预留扩展点**，采用 **Phase 4 的设计思想**。

### 4.1 接口设计：面向 Phase 4
```go
// 定义统一的 SandboxManager 接口（Phase 1 和 Phase 4 都实现它）
type SandboxManager interface {
    // 获取 sandbox（Phase 1: 返回单例; Phase 4: 从池中调度）
    Acquire(ctx context.Context, req AcquireRequest) (*Sandbox, error)
    
    // 释放 sandbox（Phase 1: 空操作; Phase 4: 放回池）
    Release(ctx context.Context, sandbox *Sandbox) error
    
    // 健康检查（Phase 1: 单容器检查; Phase 4: 全局健康）
    Healthz(ctx context.Context) error
    
    // 监控指标（Phase 1: 基础指标; Phase 4: 完整指标）
    Metrics() Metrics
}

// Phase 1 实现：单例
type SingletonManager struct {
    sandbox *Sandbox
}

// Phase 4 实现：全局调度器
type GlobalScheduler struct {
    pools      map[string]*Pool
    queue      *PriorityQueue
    scaler     *AutoScaler
    monitor    *HealthMonitor
}

// 切换只需改配置，代码无感知
func NewSandboxManager(cfg Config) SandboxManager {
    switch cfg.Mode {
    case "singleton":
        return NewSingletonManager(cfg)
    case "global":
        return NewGlobalScheduler(cfg)
    default:
        panic("unknown mode")
    }
}
```

### 4.2 配置驱动：从 Phase 1 平滑升级到 Phase 4
```yaml
sandbox:
  # 当前用 singleton，未来改成 global 即可
  mode: singleton  # singleton | simple-pool | per-user | global
  
  # 各模式独立配置，不用的配置留空
  singleton:
    image: ghcr.io/v3teran/liusha-pentools:latest
    auto_restart: true
  
  global:
    min_warm: 5
    max_concurrent: 50
    # ... (Phase 4 配置，当前不启用)
```

### 4.3 监控指标：从 Phase 1 开始埋点
```go
// Phase 1 就定义好指标接口（即使现在只有简单实现）
type Metrics struct {
    // 基础指标（Phase 1 实现）
    TotalSandboxes    int
    HealthyCount      int
    AcquireLatencyMs  float64
    
    // 高级指标（Phase 4 实现，Phase 1 留 0）
    WarmPoolSize      int
    BusyPoolSize      int
    QueueLength       int
    UtilizationRatio  float64
}

// Phase 1 简单实现
func (m *SingletonManager) Metrics() Metrics {
    return Metrics{
        TotalSandboxes:   1,
        HealthyCount:     m.isHealthy(),
        AcquireLatencyMs: 0,  // 单例无延迟
        // 其他字段 Phase 1 不支持
    }
}

// Phase 4 完整实现
func (s *GlobalScheduler) Metrics() Metrics {
    return Metrics{
        TotalSandboxes:   s.totalCount(),
        HealthyCount:     s.healthyCount(),
        AcquireLatencyMs: s.avgLatency(),
        WarmPoolSize:     len(s.warmPool),
        BusyPoolSize:     len(s.busyPool),
        QueueLength:      s.queue.Len(),
        UtilizationRatio: s.utilization(),
    }
}
```

### 4.4 测试框架：从 Phase 1 建立
```go
// 定义 SandboxManager 的标准测试套件
type ManagerTestSuite struct {
    manager SandboxManager
}

// Phase 1 和 Phase 4 都跑同一套测试
func (s *ManagerTestSuite) TestAcquireRelease() { ... }
func (s *ManagerTestSuite) TestHealthCheck() { ... }
func (s *ManagerTestSuite) TestConcurrency() { ... }

// Phase 1 跑基础测试
func TestSingletonManager(t *testing.T) {
    suite := &ManagerTestSuite{
        manager: NewSingletonManager(cfg),
    }
    suite.TestAcquireRelease()
    suite.TestHealthCheck()
}

// Phase 4 跑完整测试（包括压力测试）
func TestGlobalScheduler(t *testing.T) {
    suite := &ManagerTestSuite{
        manager: NewGlobalScheduler(cfg),
    }
    suite.TestAcquireRelease()
    suite.TestHealthCheck()
    suite.TestConcurrency()      // Phase 4 专属
    suite.TestAutoScaling()      // Phase 4 专属
    suite.TestFailover()         // Phase 4 专属
}
```

---

## 推荐方案：渐进式实现（借鉴 Phase 4 设计）

### 方案：Phase 1+ (增强版 Phase 1)

**核心思想**：
- **立即实现**：Phase 1 的简单功能（解决燃眉之急）
- **接口设计**：按 Phase 4 的标准设计（预留扩展空间）
- **逐步演进**：根据实际需求，平滑升级到 Phase 4

### 实现策略

#### Week 1: Phase 1 核心（解决 5 分钟延迟）
```go
// 1. 定义接口（面向 Phase 4）
type SandboxManager interface { ... }

// 2. 实现 Singleton（Phase 1）
type SingletonManager struct {
    sandbox *Sandbox
    mu      sync.RWMutex
}

func (m *SingletonManager) Acquire(ctx context.Context, req AcquireRequest) (*Sandbox, error) {
    m.mu.RLock()
    defer m.mu.RUnlock()
    
    if m.sandbox == nil {
        return nil, fmt.Errorf("sandbox not initialized")
    }
    
    // Phase 1: 直接返回单例
    return m.sandbox, nil
}

func (m *SingletonManager) Release(ctx context.Context, sb *Sandbox) error {
    // Phase 1: 空操作（不销毁）
    return nil
}
```

#### Week 2-3: 监控 + 隔离（为 Phase 4 打基础）
```go
// 3. 添加基础监控
func (m *SingletonManager) Metrics() Metrics {
    return Metrics{
        TotalSandboxes:   1,
        HealthyCount:     m.healthCheck(),
        AcquireLatencyMs: 0,
    }
}

// 4. 任务工作目录隔离
type Sandbox struct {
    client Client
}

func (s *Sandbox) PrepareWorkDir(taskID string) (string, error) {
    workDir := filepath.Join("/work", taskID)
    return workDir, s.client.Exec(ctx, "mkdir", "-p", workDir)
}

func (s *Sandbox) CleanupWorkDir(taskID string) error {
    workDir := filepath.Join("/work", taskID)
    return s.client.Exec(ctx, "rm", "-rf", workDir)
}
```

#### Week 4+: 按需扩展（需求驱动）
```go
// 5. 当并发需求增加时，升级到简单池
type SimplePoolManager struct {
    warmPool chan *Sandbox
    busyPool map[string]*Sandbox
    mu       sync.Mutex
}

func (m *SimplePoolManager) Acquire(ctx context.Context, req AcquireRequest) (*Sandbox, error) {
    // 优先从预热池获取
    select {
    case sb := <-m.warmPool:
        m.mu.Lock()
        m.busyPool[req.TaskID] = sb
        m.mu.Unlock()
        return sb, nil
    default:
        // 池空，创建新容器
        return m.createNew(ctx)
    }
}
```

#### Month 3+: 逐步完善（用户增长后）
- 引入用户系统 → 实现 Per-User Pool
- 并发增加 → 实现 Auto-Scaling
- 监控需求 → 集成 Prometheus
- 最终形态 → Phase 4 Global Scheduler

---

## 对比总结

### 直接实现 Phase 4
**优点**：
- ✅ 一步到位，架构完整
- ✅ 支持高并发、多租户
- ✅ 生产级可靠性

**缺点**：
- ❌ 开发周期长（3-4 周）
- ❌ 复杂度高，调试困难
- ❌ **过度设计**（当前用不上 90% 功能）
- ❌ 维护成本高
- ❌ 技术债务风险

### Phase 1+ 渐进式方案
**优点**：
- ✅ 快速解决当前问题（2-3 天）
- ✅ 接口设计面向未来（易扩展）
- ✅ 复杂度低，易维护
- ✅ 根据实际需求演进（避免浪费）
- ✅ 风险可控

**缺点**：
- 🔸 需要多次迭代（但每次迭代都有价值）
- 🔸 初期功能简单（但满足需求）

---

## 最终建议

### 推荐：Phase 1+（借鉴 Phase 4 设计的增强版 Phase 1）

**理由**：
1. **YAGNI 原则**：不为未来可能不需要的功能过度设计
2. **快速迭代**：3 天解决 5 分钟延迟问题，立即产生价值
3. **预留扩展**：接口设计面向 Phase 4，未来升级无缝
4. **风险可控**：简单系统易调试、易维护
5. **需求驱动**：根据实际用户增长决定何时升级

### 实施路径

```
Week 1:     Phase 1 核心（解决延迟问题） ← 立即开始
Week 2-3:   监控 + 隔离（打好基础）
Week 4+:    按需扩展（需求驱动）
Month 2-3:  SimplePool（并发增加时）
Month 6+:   Per-User Pool（多用户时）
Year 1+:    Global Scheduler（大规模生产时）
```

### 关键原则

**「Just Enough Architecture」**
- 解决当前问题
- 预留扩展空间
- 根据需求演进
- 避免过度设计

---

## 决策矩阵

| 场景 | 推荐方案 | 理由 |
|------|---------|------|
| **当前**（0 用户，1 并发） | Phase 1+ | 快速解决问题 |
| **3个月内**（<10 用户，<5 并发） | SimplePool | 支持基本并发 |
| **6个月内**（<50 用户，<20 并发） | Per-User Pool | 多租户隔离 |
| **1年后**（>100 用户，>50 并发） | Global Scheduler | 生产级可靠性 |

---

## 行动计划

### 立即执行（今天）
1. 实现 `SandboxManager` 接口（面向 Phase 4）
2. 实现 `SingletonManager`（Phase 1 核心）
3. 修改 `handler.go` 使用新接口

### 本周完成
4. Runner 启动时预创建容器
5. 任务工作目录隔离
6. 基础监控指标
7. E2E 测试验证

### 根据需求决定
- 并发增加 → 升级到 SimplePool
- 用户增长 → 升级到 Per-User Pool
- 规模扩大 → 升级到 Global Scheduler

**结论**：不建议直接跳到 Phase 4，推荐 **Phase 1+ 渐进式方案**。
