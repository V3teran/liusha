# Sandbox 生命周期最佳实践分析

## 问题：Sandbox 应该常驻还是按需创建？

### 当前实现（常驻模式）
- Runner 启动时创建 1 个容器
- 容器一直运行，直到 Runner 关闭
- 所有任务共享此容器

### 问题点
1. **长时间运行的容器积累状态污染**
   - 临时文件累积
   - 进程残留
   - 内存泄漏
   - 网络连接未释放

2. **安全隐患**
   - 前一个任务的数据可能被后续任务读取
   - 容器内工具版本无法更新（需重启 Runner）
   - 攻击面持续存在

3. **无法应对容器失败**
   - 虽然有健康检查和自动重启，但重启窗口期任务会失败

---

## 业界最佳实践对比

### 1. GitHub Actions Runner
**生命周期策略**：
```
┌─────────────────────────────────────────────────┐
│ Runner Pool (常驻)                               │
│  - Runner 进程常驻                               │
│  - 每个 job 使用独立的 Docker 容器               │
│  - Job 完成后立即销毁容器                        │
└─────────────────────────────────────────────────┘
```

**关键设计**：
- **Runner 进程**：常驻（类似 Liusha 的 runner）
- **Job 容器**：**按需创建，用后即焚**
- **清理时机**：每个 job 结束后立即 `docker rm -f`

**生命周期**：
```
Job Start → Pull Image (cached) → Create Container → Run Steps → Destroy Container
                 ↓                                                      ↑
              缓存命中 (秒级)                                    立即清理
```

**优点**：
- ✅ 隔离性强（每个 job 全新环境）
- ✅ 无状态污染
- ✅ 容器更新及时（下次 job 拉最新镜像）

**Liusha 借鉴**：
- Liusha 的 **runner 进程常驻**（类比 GitHub Actions Runner）
- Liusha 的 **sandbox 容器应该是短生命周期**（类比 GitHub Actions Job Container）

---

### 2. AWS Lambda
**生命周期策略**：
```
┌─────────────────────────────────────────────────┐
│ Execution Environment Lifecycle                 │
│  1. Init (冷启动): 创建环境 + 初始化 (~100-3000ms)│
│  2. Invoke: 处理请求                            │
│  3. Idle: 保持热待机 (5-15分钟)                 │
│  4. Shutdown: 超时后销毁                        │
└─────────────────────────────────────────────────┘
```

**关键设计**：
- **冷启动优化**：Provisioned Concurrency（预热池）
- **热复用**：请求结束后保持容器 5-15 分钟，供下次调用复用
- **自动回收**：空闲超时后自动销毁

**生命周期**：
```
Request 1 → Cold Start (3s) → Execute (2s) → Idle (5-15min) → Destroy
                                                  ↓
Request 2 (within 15min) ──→ Warm Start (10ms) → Execute (2s) → Idle
```

**优点**：
- ✅ 兼顾性能（热复用）和资源（自动回收）
- ✅ 有限的状态污染窗口（最多 15 分钟）

**Liusha 借鉴**：
- **空闲超时自动回收**（而非无限常驻）
- **热复用**（减少频繁创建销毁的开销）

---

### 3. Kubernetes Pods
**生命周期策略**：
```
┌─────────────────────────────────────────────────┐
│ Pod Lifecycle                                   │
│  1. Pending: 调度中                             │
│  2. Running: 执行任务                           │
│  3. Succeeded/Failed: 任务完成                  │
│  4. Terminating: 优雅关闭 (30s grace period)     │
└─────────────────────────────────────────────────┘
```

**关键设计**：
- **Job/CronJob**：任务完成后 Pod 进入 `Succeeded` 状态，但**不立即删除**
- **ttlSecondsAfterFinished**：完成后保留 N 秒（用于日志查看），然后自动清理
- **Liveness/Readiness Probes**：持续健康检查

**生命周期**：
```
Create Pod → Running → Completed → TTL (e.g. 3600s) → Delete
                           ↓
                     日志仍可查看
```

**优点**：
- ✅ 任务完成后有缓冲期（便于调试）
- ✅ 自动清理（防止资源泄漏）

**Liusha 借鉴**：
- **TTL 机制**：任务完成后保留容器一段时间（如 5 分钟），然后自动清理
- **持续健康检查**

---

### 4. Databricks Jobs
**生命周期策略**：
```
┌─────────────────────────────────────────────────┐
│ Cluster Lifecycle                               │
│  - 按需集群: Job 开始创建，结束后立即销毁        │
│  - 交互式集群: 手动创建，空闲 N 分钟后自动终止   │
│  - 池: 预热一批虚拟机，按需分配                  │
└─────────────────────────────────────────────────┘
```

**关键设计**：
- **按需模式**：成本优先
- **交互式模式**：性能优先（开发阶段）
- **池模式**：兼顾性能和成本

**Liusha 借鉴**：
- **开发模式**：Sandbox 常驻（快速迭代）
- **生产模式**：按需创建 + 空闲回收

---

## Liusha 的实际情况分析

### 当前使用场景
1. **开发测试**：手动触发 E2E 测试
2. **任务特点**：
   - 耗时长（分钟到小时级别）
   - 并发少（当前只有 1 个）
   - 频率低（每天几次到几十次）
3. **目标延迟**：0 秒启动（冷启动 5 分钟不可接受）

### 问题诊断
- **5 分钟延迟的根因**：不在容器创建（1.5 秒），而在 handler 的某个阻塞操作
- **即使容器按需创建，5 分钟延迟仍然存在**（因为问题在 handler，不在容器）

### 容器生命周期的真实需求

#### 场景 1：开发阶段（当前）
- **并发**：1-3 个任务
- **频率**：低（每小时几次）
- **优先级**：快速启动 > 资源效率

**推荐策略**：**热池模式**（类似 Lambda）
```
┌─────────────────────────────────────────────────┐
│ Task 1 → Acquire Sandbox → Execute → Release    │
│            ↓ (0ms, 从池获取)          ↓          │
│                                  放回池 (idle)   │
│                                       ↓          │
│ Task 2 (5min later) → Acquire ← 复用 idle 容器  │
│                                       ↓          │
│                                  空闲 20min      │
│                                       ↓          │
│                                    自动销毁      │
└─────────────────────────────────────────────────┘
```

**配置**：
```yaml
sandbox:
  mode: warm-pool
  warm_size: 1              # 预热 1 个容器
  idle_timeout: 1200        # 空闲 20 分钟后回收
  max_lifetime: 7200        # 最长存活 2 小时（防止状态污染）
```

**优点**：
- ✅ **0 秒启动**（从预热池获取）
- ✅ **自动清理**（空闲 20 分钟后回收，释放资源）
- ✅ **定期刷新**（2 小时强制重建，避免状态污染）

---

#### 场景 2：生产阶段（未来）
- **并发**：10-50 个任务
- **频率**：中等（每小时数十次）
- **优先级**：隔离性 > 启动速度

**推荐策略**：**池 + TTL**（类似 K8s Job）
```
┌─────────────────────────────────────────────────┐
│ Pool (3 个预热容器)                              │
│   idle-1, idle-2, idle-3                        │
│                                                 │
│ Task Start → Acquire (idle-1) → Execute         │
│                                    ↓            │
│                                 Completed       │
│                                    ↓            │
│                            TTL (5min) 保留      │
│                                    ↓            │
│                            SoftReset + 放回池   │
└─────────────────────────────────────────────────┘
```

**配置**：
```yaml
sandbox:
  mode: pool-with-ttl
  warm_size: 3              # 预热 3 个容器
  max_size: 10              # 最多 10 个并发
  task_ttl: 300             # 任务完成后保留 5 分钟
  idle_timeout: 600         # 空闲 10 分钟后回收
  max_lifetime: 3600        # 最长存活 1 小时
  reset_strategy: soft      # 软重置（清理文件+进程）
```

**流程**：
1. Task 完成 → 容器进入 `completed` 状态（保留 5 分钟，便于查看日志）
2. TTL 到期 → SoftReset（清理工作目录、kill 进程）
3. 放回 `idle` 池 → 等待下次任务
4. 空闲 10 分钟无任务 → 销毁容器
5. 存活超过 1 小时 → 强制销毁重建

**优点**：
- ✅ **任务隔离**（每次 SoftReset 清理干净）
- ✅ **快速启动**（从预热池获取）
- ✅ **自动清理**（TTL + Idle Timeout）
- ✅ **防止污染**（Max Lifetime 强制刷新）

---

## 推荐方案：渐进式生命周期策略

### Phase 1A: 热池模式（立即实现）

**代码修改**：在 `SingletonManager` 基础上增加生命周期管理

```go
type WarmPoolManager struct {
	launcher Launcher
	logger   zerolog.Logger

	mu       sync.Mutex
	sandbox  *Sandbox         // 当前活跃容器
	lastUsed time.Time        // 最后使用时间
	createdAt time.Time       // 创建时间

	// 配置
	idleTimeout  time.Duration // 空闲超时
	maxLifetime  time.Duration // 最长存活时间
}

func (m *WarmPoolManager) backgroundMaintenance() {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-m.ctx.Done():
			return
		case <-ticker.C:
			m.checkAndRecycle()
		}
	}
}

func (m *WarmPoolManager) checkAndRecycle() {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.sandbox == nil {
		return
	}

	now := time.Now()
	
	// 检查 1: 空闲超时
	if now.Sub(m.lastUsed) > m.idleTimeout {
		m.logger.Info().
			Dur("idle_duration", now.Sub(m.lastUsed)).
			Msg("sandbox idle timeout, destroying")
		m.destroySandbox()
		return
	}

	// 检查 2: 最长存活时间
	if now.Sub(m.createdAt) > m.maxLifetime {
		m.logger.Info().
			Dur("lifetime", now.Sub(m.createdAt)).
			Msg("sandbox max lifetime reached, destroying")
		m.destroySandbox()
		return
	}
}

func (m *WarmPoolManager) Acquire(ctx context.Context, req AcquireRequest) (*Sandbox, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// 如果容器不存在或已过期，创建新的
	if m.sandbox == nil || m.shouldRecreate() {
		if err := m.createSandbox(ctx); err != nil {
			return nil, err
		}
	}

	// 更新最后使用时间
	m.lastUsed = time.Now()

	// 准备任务工作目录
	sb := &Sandbox{
		Client:    m.sandbox.Client,
		ID:        m.sandbox.ID,
		TaskID:    req.TaskID,
		WorkDir:   req.WorkDir,
		CreatedAt: m.sandbox.CreatedAt,
	}

	if sb.WorkDir == "" {
		sb.WorkDir = "/work/" + req.TaskID
	}

	if err := sb.PrepareWorkDir(ctx); err != nil {
		return nil, err
	}

	return sb, nil
}

func (m *WarmPoolManager) Release(ctx context.Context, sb *Sandbox) error {
	// 更新最后使用时间（表示容器现在空闲）
	m.mu.Lock()
	m.lastUsed = time.Now()
	m.mu.Unlock()

	// 清理任务工作目录
	return sb.CleanupWorkDir(ctx)
}
```

**配置**：
```yaml
# config.yaml
runner:
  sandbox:
    mode: warm-pool
    idle_timeout: 1200      # 20 分钟
    max_lifetime: 7200      # 2 小时
    health_check_period: 30 # 30 秒
```

---

### Phase 1B: 完整池管理（2 周后）

增加以下特性：
1. **预热池**：启动时预创建 N 个容器
2. **SoftReset**：任务完成后清理状态
3. **并发支持**：支持多个任务同时运行

```go
type PoolManager struct {
	warmPool chan *Sandbox   // 预热池
	busyPool map[string]*Sandbox  // 使用中
	
	idleTimeout  time.Duration
	maxLifetime  time.Duration
	taskTTL      time.Duration
}
```

---

## 最终建议

### 立即实现（本周）
**策略**：热池模式
- ✅ 保留当前的 `SingletonManager`（快速启动）
- ✅ 增加生命周期管理：
  - **Idle Timeout**: 20 分钟
  - **Max Lifetime**: 2 小时
  - **Auto Recycle**: 后台定期检查

**代码改动**：小（在 SingletonManager 基础上加 50 行）

**收益**：
- 启动延迟：0 秒（预热）
- 资源回收：自动（20 分钟空闲后）
- 状态刷新：2 小时强制重建

---

### 2 周后（需求增长时）
**策略**：池 + TTL
- 预热 3 个容器
- 支持 10 个并发
- 任务完成后 SoftReset

---

### 核心原则

1. **容器不应该无限常驻**（状态污染、资源浪费）
2. **但也不应该每次都重建**（性能损失）
3. **折中方案**：热池 + 空闲回收 + 定期刷新

**类比**：
- ❌ 不是常驻进程（daemon）
- ✅ 是热待机服务（warm standby）
- ⏰ 有 TTL 的缓存（cached with expiration）

---

## 行动计划

### 今天
1. 修改 `SingletonManager` → `WarmPoolManager`
2. 增加 `idleTimeout` 和 `maxLifetime` 逻辑
3. 添加后台维护 goroutine

### 配置建议
```yaml
# 开发环境
sandbox:
  idle_timeout: 1200   # 20min（本地测试间隔较长）
  max_lifetime: 7200   # 2h（防止状态污染）

# 生产环境（未来）
sandbox:
  idle_timeout: 600    # 10min（流量更密集）
  max_lifetime: 3600   # 1h（更激进的刷新）
```

---

## 对比总结

| 模式 | 启动延迟 | 资源利用率 | 状态污染风险 | 适用场景 |
|------|---------|-----------|------------|---------|
| **无限常驻** | 0s | 低（一直占用） | 高（无清理） | ❌ 不推荐 |
| **按需创建** | 高（1-5s） | 高（用后即焚） | 无 | 高并发、强隔离 |
| **热池+回收** ✅ | 0s | **中等** | **低**（定期刷新） | **Liusha 当前** |
| **完整池管理** | 0s | 高（动态扩缩） | 低（SoftReset） | 生产环境 |

**结论**：**热池模式（温和复用 + 自动回收）** 是 Liusha 当前的最佳选择。
