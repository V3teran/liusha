# Warm Pool Mode（热池模式）

## 概述

热池模式是 liusha runner 的默认 sandbox 执行模式，通过预热单个容器实现极低延迟的任务执行。

## 架构特点

### ✅ 优势

1. **极低延迟**：从 5 分钟冷启动优化到 ~50ms
2. **无数据库依赖**：纯 sandbox 执行，不需要复杂的状态管理
3. **资源高效**：单容器复用，内存占用低
4. **完全隔离**：每个任务独立的工作目录
5. **自动恢复**：容器不健康时自动重建

### 📊 性能对比

| 指标 | 旧架构（按需创建） | 热池模式 | 提升 |
|------|------------------|---------|------|
| Sandbox 启动 | ~5 分钟 | ~50ms | **6,000x** |
| 资源占用 | N个容器 | 1个容器 | **N倍优化** |
| 数据库依赖 | 必须 | 无 | **完全解耦** |
| 架构复杂度 | 高 | 低 | **极简** |

## 工作原理

### 1. 启动阶段
```
runner 启动 → 创建预热容器 → 健康检查 → 进入 idle 状态
```

### 2. 任务执行
```
任务入队 → Acquire(50ms) → 执行命令 → Release → 回到 idle
```

### 3. 容器生命周期
- **Idle**：等待任务
- **Busy**：执行任务中
- **Unhealthy**：健康检查失败，自动销毁重建

## 使用方式

### 默认启动（推荐）

```bash
# 默认使用热池模式，无需额外配置
go run ./cmd/runner
```

### 切换到旧模式（不推荐）

```bash
# 只在需要完整数据库集成时使用
export USE_LEGACY_HANDLER=true
go run ./cmd/runner
```

## 任务提交

### 基本示例

```go
import (
    "github.com/google/uuid"
    "github.com/hibiken/asynq"
)

client := asynq.NewClient(asynq.RedisClientOpt{Addr: "localhost:6379"})

payload := map[string]interface{}{
    "agent_id": uuid.New().String(),
    "task_id":  uuid.New().String(),
    "role":     "executor",
    "input": map[string]interface{}{
        "command": "echo 'Hello Warm Pool!' && ls -la",
    },
}

data, _ := json.Marshal(payload)
task := asynq.NewTask("agent.run", data, asynq.Queue("executor"))
client.Enqueue(task)
```

### Input 格式

支持两种输入方式：

1. **直接命令**：
```json
{
    "command": "echo 'test' && pwd"
}
```

2. **Brief 描述**（自动生成测试命令）：
```json
{
    "brief": "Test warm pool functionality"
}
```

## 工作目录隔离

每个任务获得独立的工作目录：
```
/liusha/<task_id>/<agent_id>/workspace/
```

例如：
```
/liusha/d4c78d4e-140a/38f97a0e-3949/workspace/
```

## SoftReset 增强

任务完成后，容器通过 SoftReset 恢复到干净状态：

1. **清理工作目录**：删除 `/liusha/<task_id>/`
2. **杀掉残留进程**：
   - 先发送 TERM 信号
   - 等待 2 秒
   - 强制 KILL 仍存活的进程
   - 保护 PID 1 和 sandbox-server
3. **清理临时文件**：`/tmp/*` 和 `/dev/shm/*`
4. **清理僵尸进程**

## 健康检查

WarmPoolManager 定期检查容器健康：

- **检查方式**：调用 `/health` 端点
- **失败处理**：自动销毁并重建容器
- **恢复时间**：~2 秒

## 监控

### 日志关键字

```bash
# 启动成功
tail -f logs/runner.log | grep "using pure warm pool handler"

# 任务执行
tail -f logs/runner.log | grep "warm pool handler"

# Sandbox 获取
tail -f logs/runner.log | grep "sandbox acquired"

# 容器健康
tail -f logs/runner.log | grep "sandbox unhealthy"
```

### 性能指标

- **Acquire 延迟**：通常 30-60ms
- **Reset 时间**：5-10 秒
- **重建时间**：1-2 秒

## 故障排查

### 容器频繁重建

**现象**：日志频繁出现 "sandbox unhealthy"

**原因**：
1. 端口映射不稳定
2. sandbox-server 进程崩溃
3. Docker 资源不足

**解决**：
```bash
# 检查容器状态
docker ps -a | grep warm-sandbox

# 查看容器日志
docker logs liusha-sandbox-warm-sandbox

# 重启 runner
pkill -f "go run.*cmd/runner"
go run ./cmd/runner
```

### 任务卡住不执行

**现象**：任务入队但没有执行日志

**原因**：
1. Redis 连接断开
2. asynq worker 没有启动
3. 队列名称不匹配

**解决**：
```bash
# 检查 Redis
redis-cli -h localhost -p 6379 PING

# 检查队列
redis-cli -h localhost -p 6379 LLEN asynq:{executor}:pending

# 检查 runner 日志
tail -100 logs/runner.log | grep -i error
```

### Acquire 超时

**现象**："acquire sandbox: timeout"

**原因**：前一个任务的 SoftReset 还未完成

**解决**：
- 等待当前任务完成
- 或重启 runner 强制重置

## 配置参数

### 环境变量

| 变量 | 默认值 | 说明 |
|------|--------|------|
| `USE_LEGACY_HANDLER` | `false` | 是否使用旧的完整 handler |
| `DOCKER_NETWORK` | `liusha-net` | Docker 网络名称 |

### WarmPoolManager 配置

```go
// 在 cmd/runner/main.go 中
sandboxMgr := sandbox.NewWarmPoolManager(
    ctx,
    sandbox.WarmPoolConfig{
        ImageName: cfg.SandboxImage,
        Network:   cfg.DockerNetwork,
    },
    logger,
)
```

## 测试

### 单元测试

```bash
cd internal/sandbox
go test -v -run TestWarmPoolManager
```

### 集成测试

```bash
# 使用测试工具
go run ./cmd/test-sandbox

# 或使用示例脚本
go run /tmp/test_default_warmpool.go
```

## 未来优化

### 可能的增强（按需实现）

1. **多容器池**：支持并发执行多个任务
2. **Per-user 隔离**：不同用户使用独立容器
3. **资源限制**：CPU/内存配额控制
4. **Metrics**：Prometheus 指标导出
5. **容器预热策略**：根据负载动态调整池大小

### 当前不需要的功能

- ❌ 数据库状态管理（已移除，降低复杂度）
- ❌ Role 约束检查（旧架构遗留）
- ❌ Pending/Running/Done 状态机（不需要）

## 相关文件

- `internal/sandbox/warmpool_manager.go` - 热池管理器
- `internal/sandbox/manager.go` - Sandbox 基础操作（包含增强的 SoftReset）
- `cmd/runner/handler_warmpool.go` - 纯热池 handler
- `cmd/runner/handler.go` - 旧的完整 handler（保留以供参考）
- `cmd/test-sandbox/main.go` - 独立测试工具

## 总结

热池模式是 liusha 的默认推荐方案，提供：
- ✅ 极致性能（50ms vs 5分钟）
- ✅ 简单架构（无数据库依赖）
- ✅ 高可靠性（自动恢复）
- ✅ 资源高效（单容器复用）

**除非有特殊需求，否则始终使用热池模式。**
