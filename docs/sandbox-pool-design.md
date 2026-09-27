# Liusha Sandbox 容器池架构设计

## 当前问题

### 1. 粒度过细
- **现状**：per assignment（一个测试目标一个容器）
- **问题**：
  - 同一用户的多个任务可能对应多个 assignment
  - 容器数量过多，资源浪费
  - 冷启动延迟（5分钟）严重影响体验

### 2. 无容器池管理
- **现状**：按需创建，引用计数归零后 30 秒延迟销毁
- **问题**：
  - 每次都冷启动（拉镜像、健康检查）
  - 无预热机制
  - 无容器复用

### 3. 用户隔离缺失
- **现状**：Liusha 还没有用户系统
- **影响**：无法实现 per-user 隔离

---

## 新架构设计

### 阶段 1：单容器模式（立即实现）

**背景**：当前 Liusha 没有用户系统，所有任务视为同一"系统用户"

**设计**：
```
┌─────────────────────────────────────────────────┐
│ Runner (所有 Agent)                              │
└─────────────────────────────────────────────────┘
                    ↓
┌─────────────────────────────────────────────────┐
│ Single Sandbox Container (常驻)                  │
│  - 启动时创建，进程结束时销毁                     │
│  - 所有任务共享                                   │
│  - 任务间通过工作目录隔离                         │
└─────────────────────────────────────────────────┘
```

**优点**：
- ✅ **零启动延迟**（容器已预热）
- ✅ 架构简单，易实现
- ✅ 资源利用率高
- ✅ 适合当前单用户场景

**实现**：
```go
type SingletonSandbox struct {
    client    sandbox.Client
    mu        sync.RWMutex
    launcher  sandbox.Launcher
    containerID string
}

func (s *SingletonSandbox) EnsureRunning(ctx context.Context) error {
    s.mu.Lock()
    defer s.mu.Unlock()
    
    if s.client != nil {
        // 健康检查
        if s.client.Healthz(ctx) == nil {
            return nil
        }
    }
    
    // 启动新容器
    client, err := s.launcher.Spawn(ctx, "system-sandbox")
    if err != nil {
        return err
    }
    s.client = client
    return nil
}

func (s *SingletonSandbox) Get(ctx context.Context) (sandbox.Client, error) {
    s.mu.RLock()
    defer s.mu.RUnlock()
    
    if s.client == nil {
        return nil, fmt.Errorf("sandbox not initialized")
    }
    return s.client, nil
}
```

**任务隔离**：
- 每个任务使用独立的工作目录：`/work/task-{task_id}/`
- 工具执行时传入 `--work-dir` 参数
- 临时文件在任务结束后清理

---

### 阶段 2：Per-User 容器池（引入用户系统后）

**背景**：未来 Liusha 支持多用户时

**设计**：
```
┌─────────────────────────────────────────────────┐
│ Runner                                          │
└─────────────────────────────────────────────────┘
         ↓ 按 user_id 获取
┌─────────────────────────────────────────────────┐
│ Per-User Sandbox Pool                           │
│  user-001 → sandbox-pool-001                    │
│  user-002 → sandbox-pool-002                    │
│  user-003 → sandbox-pool-003                    │
└─────────────────────────────────────────────────┘
         ↓ 从用户池中获取
┌─────────────────────────────────────────────────┐
│ User Pool (warm + idle + busy)                  │
│  ├─ warm: 预热容器（立即可用）                   │
│  ├─ idle: 空闲容器（上次使用后保留）              │
│  └─ busy: 使用中的容器                           │
└─────────────────────────────────────────────────┘
```

**策略**：
```yaml
sandbox_pool:
  mode: per-user  # per-user | singleton
  
  per_user:
    warm_size: 1           # 每个用户预热 1 个容器
    max_concurrent: 3      # 每个用户最多 3 个并发容器
    idle_timeout: 600      # 空闲 10 分钟后回收
    max_lifetime: 3600     # 容器最长存活 1 小时
    reset_strategy: soft   # soft(清理文件) | hard(重建容器)
```

**核心逻辑**：
```go
type UserSandboxPool struct {
    userID     string
    warmPool   chan *Sandbox   // 预热池
    busyPool   map[string]*Sandbox  // 使用中
    mu         sync.Mutex
    maxSize    int
    warmSize   int
    launcher   sandbox.Launcher
}

func (p *UserSandboxPool) Acquire(ctx context.Context, taskID string) (*Sandbox, error) {
    p.mu.Lock()
    defer p.mu.Unlock()
    
    // 1. 尝试从预热池获取（快速路径）
    select {
    case sb := <-p.warmPool:
        p.busyPool[taskID] = sb
        return sb, nil
    default:
    }
    
    // 2. 检查是否达到并发上限
    if len(p.busyPool) >= p.maxSize {
        return nil, fmt.Errorf("user %s reached max concurrent sandboxes", p.userID)
    }
    
    // 3. 创建新容器（慢路径）
    sb, err := p.createSandbox(ctx)
    if err != nil {
        return nil, err
    }
    
    p.busyPool[taskID] = sb
    
    // 4. 异步补充预热池
    go p.replenishWarmPool(context.Background())
    
    return sb, nil
}

func (p *UserSandboxPool) Release(taskID string) error {
    p.mu.Lock()
    defer p.mu.Unlock()
    
    sb, exists := p.busyPool[taskID]
    if !exists {
        return fmt.Errorf("task %s not found in busy pool", taskID)
    }
    
    delete(p.busyPool, taskID)
    
    // 软重置：清理工作目录
    if err := sb.SoftReset(); err != nil {
        // 重置失败，销毁容器
        sb.Destroy()
        return err
    }
    
    // 放回预热池
    select {
    case p.warmPool <- sb:
        // 成功放回
    default:
        // 池满，销毁容器
        sb.Destroy()
    }
    
    return nil
}

func (p *UserSandboxPool) replenishWarmPool(ctx context.Context) {
    for {
        select {
        case <-ctx.Done():
            return
        default:
        }
        
        // 检查预热池是否需要补充
        if len(p.warmPool) >= p.warmSize {
            return
        }
        
        sb, err := p.createSandbox(ctx)
        if err != nil {
            log.Warn().Err(err).Msg("failed to replenish warm pool")
            return
        }
        
        select {
        case p.warmPool <- sb:
            log.Info().Msg("warm pool replenished")
        default:
            sb.Destroy()
            return
        }
    }
}
```

---

### 阶段 3：全局容器池 + 动态调度（高级优化）

**背景**：生产环境，多租户，需要全局资源调度

**设计**：
```
┌─────────────────────────────────────────────────┐
│ Global Sandbox Scheduler                        │
│  - 全局容器池管理                                │
│  - 用户配额控制                                   │
│  - 优先级调度                                     │
│  - 自动扩缩容                                     │
└─────────────────────────────────────────────────┘
         ↓
┌─────────────────────────────────────────────────┐
│ Sandbox Pool (共享池)                            │
│  ├─ Warm Pool (10 个预热)                        │
│  ├─ User Pools (按用户分配)                      │
│  └─ Overflow Pool (临时扩容)                     │
└─────────────────────────────────────────────────┘
```

**特性**：
1. **配额管理**：每个用户/租户有并发上限
2. **优先级队列**：付费用户优先获取容器
3. **弹性扩容**：
   - 低负载：保持最小预热池
   - 高负载：自动扩容到上限
   - 超载：排队等待
4. **健康监控**：
   - 容器健康检查
   - 自动重启失败容器
   - 指标上报（Prometheus）

---

## 业界最佳实践参考

### 1. Kubernetes Pod 管理
**策略**：
- **Resource Limits**：CPU/Memory 硬限制
- **Liveness/Readiness Probes**：健康检查
- **PreStop Hook**：优雅关闭
- **Pod Disruption Budget**：最小可用副本数

**借鉴**：
```go
type SandboxConfig struct {
    Resources struct {
        CPULimit    string  // "2.0"
        MemoryLimit string  // "4Gi"
    }
    
    HealthCheck struct {
        Endpoint        string        // "/healthz"
        Interval        time.Duration // 10s
        Timeout         time.Duration // 2s
        FailureThreshold int          // 3
    }
    
    Lifecycle struct {
        PreStopScript   string        // 清理脚本
        TerminationGrace time.Duration // 30s
    }
}
```

### 2. AWS Lambda 冷启动优化
**策略**：
- **Provisioned Concurrency**：预热 N 个实例
- **SnapStart**：快照启动（Java）
- **Keep-Warm**：定期调用防止冷却

**借鉴**：
```go
type WarmingStrategy struct {
    Mode           string // "always" | "scheduled" | "adaptive"
    MinInstances   int    // 最小预热数
    MaxInstances   int    // 最大实例数
    ScaleUpDelay   time.Duration // 扩容延迟
    ScaleDownDelay time.Duration // 缩容延迟
}

// 自适应预热：根据历史负载预测
func (s *Scheduler) AdaptiveWarm(ctx context.Context) {
    stats := s.getHistoricalLoad(time.Now().Hour())
    targetWarm := int(stats.AvgConcurrent * 1.2) // 120% 缓冲
    s.adjustWarmPool(ctx, targetWarm)
}
```

### 3. GitHub Actions Runner
**策略**：
- **Runner Pool**：预注册的 runner 池
- **Job Queue**：任务排队机制
- **Auto-scaling**：根据队列长度扩容
- **Ephemeral Runners**：一次性使用后销毁

**借鉴**：
```go
type RunnerPool struct {
    queue         chan Task       // 任务队列
    availablePool chan *Sandbox   // 可用 runner
    maxQueueSize  int             // 最大排队数
}

func (p *RunnerPool) Dispatch(ctx context.Context, task Task) error {
    // 入队
    select {
    case p.queue <- task:
    case <-ctx.Done():
        return ctx.Err()
    case <-time.After(30 * time.Second):
        return fmt.Errorf("queue full, rejected")
    }
    
    // 后台调度
    go p.schedule(ctx)
    return nil
}

func (p *RunnerPool) schedule(ctx context.Context) {
    for {
        select {
        case task := <-p.queue:
            sb := <-p.availablePool  // 阻塞等待可用容器
            go p.runTask(ctx, sb, task)
        case <-ctx.Done():
            return
        }
    }
}
```

### 4. Docker Swarm Service
**策略**：
- **Replicas**：副本数控制
- **Update Config**：滚动更新
- **Rollback**：自动回滚
- **Placement Constraints**：节点亲和性

**借鉴**：
```go
type PoolUpdateConfig struct {
    Parallelism     int           // 并行更新数
    Delay           time.Duration // 更新间隔
    FailureAction   string        // "pause" | "rollback"
    MonitorDuration time.Duration // 监控窗口
}

// 滚动更新容器池（零停机）
func (p *Pool) RollingUpdate(ctx context.Context, newImage string) error {
    for i := 0; i < p.size; i++ {
        // 1. 创建新容器
        newSb, err := p.launcher.SpawnWithImage(ctx, newImage)
        if err != nil {
            return p.rollback(ctx, i)
        }
        
        // 2. 健康检查
        if err := p.waitHealthy(ctx, newSb); err != nil {
            newSb.Destroy()
            return p.rollback(ctx, i)
        }
        
        // 3. 替换旧容器
        oldSb := p.sandboxes[i]
        p.sandboxes[i] = newSb
        oldSb.Destroy()
        
        // 4. 延迟
        time.Sleep(p.updateConfig.Delay)
    }
    return nil
}
```

---

## 实现路线图

### Phase 1: 单容器模式（本周）

**目标**：解决 5 分钟冷启动问题

**任务**：
- [x] 删除旧镜像
- [ ] 实现 `SingletonSandbox` 管理器
- [ ] 修改 `handler.go`：移除 per-assignment 逻辑
- [ ] Runner 启动时预创建单个 sandbox
- [ ] 任务通过工作目录隔离（`/work/task-{id}/`）
- [ ] E2E 测试验证

**预期效果**：
- 启动延迟：5分钟 → **0秒**（容器已预热）
- 资源占用：N × 15GB → **1 × 15GB**
- 架构复杂度：高 → **低**

---

### Phase 2: 基础容器池（2周内）

**目标**：支持并发任务

**任务**：
- [ ] 实现 `SimplePool` 管理器
  - Warm pool (size: 2)
  - Busy pool (max: 5)
  - Soft reset（清理工作目录）
- [ ] 并发任务测试（10个并发）
- [ ] 容器生命周期管理
  - Max lifetime: 1 hour
  - Idle timeout: 10 min
- [ ] 监控指标
  - Pool 利用率
  - 容器启动耗时
  - 任务等待时间

**预期效果**：
- 并发支持：单容器 → **5个并发**
- 启动延迟：**<1秒**（从 warm pool 获取）
- 资源利用率：**>70%**

---

### Phase 3: Per-User 池（1个月内）

**前提**：Liusha 引入用户系统

**任务**：
- [ ] 用户认证/授权
- [ ] `UserSandboxPool` 实现
- [ ] 用户配额管理
- [ ] 用户间隔离测试
- [ ] 计费/审计（按用户统计资源使用）

**预期效果**：
- 用户隔离：**完全隔离**
- 多租户支持：✅
- 安全性：**高**

---

### Phase 4: 高级调度（3个月内）

**目标**：生产级容器编排

**任务**：
- [ ] 全局调度器
- [ ] 优先级队列
- [ ] 自动扩缩容
- [ ] 健康监控 + 自愈
- [ ] Prometheus 指标
- [ ] Grafana 仪表盘
- [ ] 压力测试（100 并发）

**预期效果**：
- 可扩展性：**支持 100+ 并发**
- 可靠性：**99.9% 可用性**
- 可观测性：**完整监控**

---

## 配置设计

```yaml
# config.yaml
sandbox:
  # 容器池模式
  mode: singleton  # singleton | simple-pool | per-user-pool | global-scheduler
  
  # 镜像配置
  image: ghcr.io/v3teran/liusha-pentools:latest
  
  # 单例模式配置
  singleton:
    auto_restart: true          # 容器崩溃后自动重启
    health_check_interval: 30s  # 健康检查间隔
  
  # 简单池配置
  simple_pool:
    warm_size: 2                # 预热池大小
    max_size: 5                 # 最大并发数
    idle_timeout: 600           # 空闲超时（秒）
    max_lifetime: 3600          # 最长存活时间（秒）
    reset_strategy: soft        # soft | hard
  
  # Per-User 池配置
  per_user_pool:
    warm_size_per_user: 1       # 每个用户预热数
    max_concurrent_per_user: 3  # 每个用户最大并发
    global_max: 20              # 全局最大容器数
    idle_timeout: 600
    max_lifetime: 3600
  
  # 全局调度器配置
  global_scheduler:
    min_warm: 5                 # 最小预热数
    max_warm: 20                # 最大预热数
    scale_up_threshold: 0.8     # 扩容阈值（80% 利用率）
    scale_down_threshold: 0.3   # 缩容阈值（30% 利用率）
    scale_cooldown: 60          # 扩缩容冷却期（秒）
    priority_enabled: true      # 启用优先级调度
  
  # 资源限制
  resources:
    cpu_limit: "2.0"            # CPU 核数
    memory_limit: "4Gi"         # 内存限制
    disk_limit: "20Gi"          # 磁盘限制
  
  # 监控
  monitoring:
    enabled: true
    prometheus_port: 9090
    metrics:
      - pool_size
      - warm_count
      - busy_count
      - acquire_latency_ms
      - task_duration_ms
```

---

## 监控指标设计

```go
// Prometheus metrics
var (
    // 池大小
    poolSizeGauge = prometheus.NewGaugeVec(
        prometheus.GaugeOpts{
            Name: "liusha_sandbox_pool_size",
            Help: "Current size of sandbox pool",
        },
        []string{"pool_type", "state"}, // state: warm/busy/idle
    )
    
    // 容器获取延迟
    acquireLatencyHistogram = prometheus.NewHistogram(
        prometheus.HistogramOpts{
            Name:    "liusha_sandbox_acquire_latency_seconds",
            Help:    "Latency of acquiring a sandbox",
            Buckets: []float64{0.01, 0.1, 0.5, 1, 5, 10, 30},
        },
    )
    
    // 容器利用率
    utilizationGauge = prometheus.NewGauge(
        prometheus.GaugeOpts{
            Name: "liusha_sandbox_utilization_ratio",
            Help: "Ratio of busy sandboxes to total capacity",
        },
    )
    
    // 任务等待时间
    taskWaitHistogram = prometheus.NewHistogram(
        prometheus.HistogramOpts{
            Name:    "liusha_task_wait_seconds",
            Help:    "Time a task waits in queue before getting a sandbox",
            Buckets: []float64{0, 1, 5, 10, 30, 60},
        },
    )
    
    // 容器健康状态
    healthStatusGauge = prometheus.NewGaugeVec(
        prometheus.GaugeOpts{
            Name: "liusha_sandbox_health_status",
            Help: "Health status of sandboxes (1=healthy, 0=unhealthy)",
        },
        []string{"sandbox_id"},
    )
)
```

---

## 下一步行动

### 立即执行（今天）
1. ✅ 删除旧镜像
2. [ ] 基于最新 `pentools-base` 重新构建 `liusha-pentools`
3. [ ] 实现 `SingletonSandbox` 管理器
4. [ ] 修改 `handler.go` 移除 per-assignment 逻辑

### 本周完成
5. [ ] Runner 启动时预创建容器
6. [ ] 任务工作目录隔离
7. [ ] E2E 测试验证
8. [ ] 监控基础指标

### 下周开始
9. [ ] 实现 `SimplePool` 容器池
10. [ ] 并发测试
11. [ ] 压测验证

---

## 总结

**核心思路**：
1. **阶段演进**：单容器 → 简单池 → Per-User 池 → 全局调度
2. **业界借鉴**：K8s + Lambda + GitHub Actions + Docker Swarm
3. **渐进式优化**：先解决燃眉之急（5分钟延迟），再完善长期架构

**关键收益**：
- ✅ 启动延迟：5分钟 → 0秒
- ✅ 资源利用率：低 → 高
- ✅ 可扩展性：单任务 → 多租户
- ✅ 可观测性：无 → 完整监控
