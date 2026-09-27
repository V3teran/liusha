# Sandbox 架构分析：Liusha vs Artex vs 业界实践

## 当前状况

### Liusha 架构
```
┌─────────────────────────────────────────────────┐
│ Runner (主进程)                                  │
│  ├─ Planner Agent                                │
│  ├─ Executor Agent                               │
│  ├─ Evaluator Agent                              │
│  └─ Monitor Agent                                │
└─────────────────────────────────────────────────┘
                    ↓ per assignment
┌─────────────────────────────────────────────────┐
│ Sandbox Container (9.43GB pentools)             │
│  ├─ nmap, sqlmap, nuclei, nikto...              │
│  ├─ chromium + playwright                        │
│  └─ mitmproxy (流量捕获)                         │
└─────────────────────────────────────────────────┘
```

**启动流程**：
1. 任务进入 handler (09:47:26)
2. 等待 sandbox acquire (????)
3. Sandbox acquired (09:52:20) - **延迟 5 分钟**
4. 四 Agent 启动，开始执行

**资源粒度**：`assignment` (一个测试目标对应一个 sandbox)

### Artex 架构
```
┌─────────────────────────────────────────────────┐
│ Artex Container (单容器)                         │
│  ├─ Artex 二进制 (main agent + planner +worker) │
│  ├─ nmap, curl, playwright (直接安装)            │
│  └─ exec.Command 直接执行工具                    │
└─────────────────────────────────────────────────┘
```

**启动流程**：
1. 容器启动一次（预装所有工具）
2. 任务直接执行，无额外隔离

**资源粒度**：所有任务共享一个容器

## 三种架构模式对比

### 模式 1：共享容器（Artex）
**优点**：
- ✅ 启动快（工具已预装）
- ✅ 资源利用率高（多任务共享）
- ✅ 架构简单

**缺点**：
- ❌ 隔离性差（任务间可能互相干扰）
- ❌ 安全风险（恶意工具输出可能影响系统）
- ❌ 并发受限（单容器 CPU/内存瓶颈）
- ❌ 不适合**多租户**场景

**适用场景**：
- 单用户自托管
- 开发测试环境
- 信任输入目标

---

### 模式 2：Per-Assignment 隔离（Liusha 当前）
**优点**：
- ✅ 隔离性强（不同 assignment 互不影响）
- ✅ 安全性高（容器级隔离）
- ✅ 支持多租户
- ✅ 可限制单个任务资源（--memory, --cpus）

**缺点**：
- ❌ 启动慢（需创建容器 + 健康检查）
- ❌ 资源开销大（9.43GB × N 个并发任务）
- ❌ 复杂度高（生命周期管理、网络、流量捕获）

**适用场景**：
- 多租户 SaaS
- 生产环境
- 需要严格隔离

**问题**：
- ⚠️ **assignment 粒度过细**：一个用户的多个任务可能共享 assignment
- ⚠️ **启动延迟未优化**：5 分钟不正常，应该 <30 秒

---

### 模式 3：Per-Task 隔离（理论方案）
**设计**：每个 task 一个独立 sandbox

**优点**：
- ✅ 隔离性最强
- ✅ 任务完全独立

**缺点**：
- ❌❌ 资源开销极大（并发 10 个任务 = 94GB 内存）
- ❌❌ 启动延迟 × 任务数
- ❌❌ 容器数量爆炸

**适用场景**：几乎不适用（除非极端安全需求）

---

### 模式 4：容器池 + 复用（推荐方案）
**设计**：
```
┌─────────────────────────────────────────────────┐
│ Runner                                          │
└─────────────────────────────────────────────────┘
         ↓ 从池中获取
┌─────────────────────────────────────────────────┐
│ Sandbox Pool (预热 3-5 个容器)                   │
│  ├─ sandbox-1 (idle / busy)                     │
│  ├─ sandbox-2 (idle / busy)                     │
│  ├─ sandbox-3 (idle / busy)                     │
│  └─ ...                                         │
└─────────────────────────────────────────────────┘
```

**策略**：
1. **预热**：启动时预创建 N 个容器（warm pool）
2. **按需扩容**：池不足时创建新容器
3. **用后清理**：任务完成后重置容器状态（清理临时文件、kill 残留进程）
4. **定期回收**：容器运行 X 分钟后销毁重建（防止状态污染）

**优点**：
- ✅ **启动快**：从预热池获取，<1 秒
- ✅ 隔离性：每个任务独立容器（任务并发时）
- ✅ 资源可控：池大小可配置
- ✅ **兼顾性能与安全**

**缺点**：
- 🔸 需要容器重置逻辑
- 🔸 预热池占用内存（但可配置）

**适用场景**：
- **生产 SaaS**（推荐）
- 需要快速响应
- 中等并发量

---

## 问题诊断

### 为什么 Liusha 启动慢？

**可能原因分析**：

#### 1. 镜像拉取延迟？
❌ **排除**：`docker inspect` 显示容器 1.5 秒启动完成

#### 2. Healthz 超时？
❌ **排除**：`healthzMaxWait = 30 秒`，远小于 5 分钟

#### 3. Per-host 并发限制？
❌ **排除**：前一个任务已在 17 小时前退出

#### 4. Asynq 队列积压？
❌ **排除**：`Concurrency = 6`，只有 1 个任务在队列

#### 5. 🔴 **最可能**：Handler 中的阻塞操作
查看 `handler.go:158 → cognition.go:54` 之间的代码：
- `GetByID(ctx, p.AgentID)` - 查询 agent_run 表
- `hostSem.Acquire(ctx, tk.TargetHost)` - 获取 per-host 信号量
- `executors.SetRunning(ctx, p.AgentID)` - 更新数据库
- `tasks.GetByID(ctx, taskID)` - 查询 task 表
- `profiles.Onboard(ctx, ...)` - 解析 brief

**需要排查**：
- 数据库查询是否慢？
- 是否有死锁？
- Redis 连接是否正常？

#### 6. 🔴 **日志缺失**
5 分钟内**完全没有日志**，说明：
- 要么卡在某个无日志的阻塞点
- 要么日志级别过滤掉了关键信息

**建议**：
1. 在 handler 的每个步骤加 `logger.Debug()` 跟踪
2. 检查数据库连接池状态
3. 监控 Redis 响应时间

---

## 推荐架构调整

### 短期优化（保持现有架构）

1. **诊断 5 分钟延迟**
   - 在 handler 的关键路径加日志
   - 检查数据库/Redis 性能
   - 监控系统资源

2. **优化 sandbox 启动**
   - 减小镜像大小（移除不常用工具到按需安装）
   - 使用本地 registry 缓存
   - 预热容器池（启动时创建 1-2 个）

3. **调整隔离粒度**
   - 当前：per assignment
   - 建议：per user（同一用户的任务共享 sandbox）
   - 理由：用户内任务互相信任，减少容器数量

### 长期重构（容器池方案）

```go
type SandboxPool struct {
    warmPool  chan *Sandbox  // 预热池（idle 容器）
    busyPool  map[string]*Sandbox  // 使用中的容器
    maxSize   int  // 最大容器数
    warmSize  int  // 预热池大小
}

func (p *SandboxPool) Acquire(ctx context.Context) (*Sandbox, error) {
    select {
    case sb := <-p.warmPool:
        // 从预热池获取（<1 秒）
        return sb, nil
    default:
        // 池空，创建新容器（慢路径）
        return p.createNew(ctx)
    }
}

func (p *SandboxPool) Release(sb *Sandbox) {
    // 重置容器状态
    sb.Reset()
    
    // 放回预热池
    select {
    case p.warmPool <- sb:
    default:
        // 池满，销毁容器
        sb.Destroy()
    }
}
```

**配置示例**：
```yaml
sandbox:
  pool:
    warm_size: 3  # 预热 3 个容器
    max_size: 10  # 最多 10 个并发
    reset_after_tasks: 20  # 每 20 个任务后重建容器
    max_lifetime: 3600  # 容器最长存活 1 小时
```

---

## 与 Artex 的对比结论

### Artex 的选择
- **单容器共享**：简单、快速
- **适用场景**：个人自托管、开发环境
- **牺牲**：隔离性、多租户支持

### Liusha 的定位
- **分布式隔离**：安全、可扩展
- **适用场景**：SaaS、生产环境、多租户
- **代价**：复杂度、启动延迟

### 结论
✅ **Liusha 的架构方向正确**（相比 Artex 更适合生产）

❌ **实现存在问题**：
1. 5 分钟启动延迟**不正常**（需诊断）
2. Per-assignment 粒度**可能过细**（建议改为 per-user）
3. **缺少容器池**（预热机制）

🎯 **建议**：
1. **立即**：诊断 5 分钟延迟的根因
2. **短期**：实现容器预热池
3. **中期**：调整隔离粒度为 per-user
4. **长期**：完善容器池管理（自动扩缩容、健康检查、状态重置）

---

## 业界参考

### 1. GitHub Actions
- **模式**：容器池 + 预热
- **粒度**：per job
- **优化**：runner 预装常用工具，容器秒级启动

### 2. AWS Lambda
- **模式**：容器复用 + 冷启动优化
- **粒度**：per invocation（冷启动）/ 复用（热启动）
- **优化**：Provisioned Concurrency（预热）

### 3. Kubernetes Pods
- **模式**：按需创建 + 资源限制
- **粒度**：per workload
- **优化**：image pull policy（Always/IfNotPresent/Never）

### 共同点
- ✅ 都有**预热/缓存**机制
- ✅ 都有**资源限制**
- ✅ 都有**健康检查**
- ✅ 冷启动时间都优化到 **<10 秒**

---

## 行动计划

### Phase 1：诊断延迟（本周）
- [ ] 在 handler 关键路径加 debug 日志
- [ ] 监控数据库查询耗时
- [ ] 检查 Redis 连接状态
- [ ] 复现并定位 5 分钟卡点

### Phase 2：快速优化（下周）
- [ ] 实现简单的预热池（启动时创建 2 个容器）
- [ ] 优化镜像大小（拆分 base + tools 层）
- [ ] 调整 assignment 粒度为 per-user

### Phase 3：容器池重构（2 周后）
- [ ] 实现完整的 SandboxPool
- [ ] 容器状态重置逻辑
- [ ] 自动扩缩容
- [ ] 监控指标（启动耗时、池利用率）

### Phase 4：压测验证（1 个月后）
- [ ] 并发 10 个任务测试
- [ ] 启动延迟 < 5 秒
- [ ] 资源利用率 > 70%
