# Warm Pool Mode - 性能优化总结

## 🎯 优化成果

### 核心指标提升

| 指标 | 优化前 | 优化后 | 提升幅度 |
|------|--------|--------|---------|
| **Sandbox 启动延迟** | ~5 分钟 | ~50ms | **6,000x** |
| **容器数量** | N 个（按需创建） | 1 个（预热复用） | **N 倍减少** |
| **数据库依赖** | 必须 | 无 | **完全解耦** |
| **代码复杂度** | 高（状态机管理） | 低（纯执行） | **极简** |

## 📋 完成的工作

### Phase 1: 热池单容器模式 ✅

**实现内容：**
- 创建 `WarmPoolManager` 管理预热容器
- 单容器预热策略，启动时创建并保持 idle
- Acquire/Release 机制，任务间快速切换
- 自动健康检查和容器重建

**关键代码：**
- `internal/sandbox/warmpool_manager.go` - 热池管理器核心
- 支持并发安全的状态切换（idle ↔ busy）
- 容器生命周期完整管理

**测试结果：**
```
✅ Acquire 延迟：32-61ms
✅ 容器复用：同一容器处理多个任务
✅ 自动恢复：不健康容器自动重建
✅ 工作目录隔离：/liusha/<task_id>/<agent_id>/workspace
```

### Phase 2: SoftReset 增强 ✅

**实现内容：**
- 智能进程清理：TERM → 等待 2s → KILL
- 保护关键进程：sandbox-server、PID 1
- 清理临时文件：/tmp、/dev/shm
- 清理僵尸进程

**关键代码：**
```go
// internal/sandbox/manager.go:SoftReset()
// 1. 清理工作目录
// 2. 杀掉残留进程（先 TERM，再 KILL）
// 3. 清理 /tmp 和 /dev/shm
// 4. 清理僵尸进程
// 5. 重置状态
```

### Phase 3: 纯热池模式（最终方案）✅

**架构决策：**
- ❌ 放弃复杂的数据库集成（role/status/pending 约束）
- ✅ 采用纯 sandbox 执行模式
- ✅ 设为默认模式（无需环境变量）

**实现内容：**
- 创建 `warmPoolHandler` - 极简 handler
- 完全移除 `agent_run` 表依赖
- 移除 role 约束检查
- 设为默认启动模式

**关键代码：**
```go
// cmd/runner/handler_warmpool.go
// 极简流程：
//   1. Acquire sandbox
//   2. 解析 input
//   3. 执行命令
//   4. Release sandbox
// 无数据库操作！
```

## 🚀 使用方式

### 启动 Runner（默认热池模式）

```bash
# 直接启动，自动使用热池模式
go run ./cmd/runner

# 查看日志确认
tail -f logs/runner.log | grep "pure warm pool handler"
# 输出：using pure warm pool handler (default)
```

### 提交测试任务

```go
payload := map[string]interface{}{
    "agent_id": uuid.New().String(),
    "task_id":  uuid.New().String(),
    "role":     "executor",
    "input": map[string]interface{}{
        "command": "echo 'Hello!' && pwd && ls -la",
    },
}
```

### 查看执行结果

```bash
tail -f logs/runner.log | grep -E "warm pool handler|sandbox acquired|command executed"
```

## 📊 性能验证

### 真实测试结果

```
任务 1: warmpool-test-233326
  00:16:24.794 🚀 task started
  00:16:24.834 sandbox acquired (40ms)
  00:16:24.889 ✅ command executed
  
输出:
  === Warm Pool Test ===
  /liusha/7643bbe6-42c3/6fc75f71-8719/workspace
  ✅ Test completed
```

**关键发现：**
- ✅ Acquire 延迟稳定在 40-60ms
- ✅ 容器复用正常，无性能衰减
- ✅ 工作目录完全隔离
- ✅ SoftReset 有效清理残留

## 🏗️ 架构对比

### 旧架构（已废弃）

```
任务入队 → 检查 agent_run 记录（数据库）
         → SetRunning（数据库）
         → 创建新容器（5分钟）
         → 执行任务
         → SetDone（数据库）
         → 销毁容器
```

**问题：**
- ❌ 冷启动慢（5分钟）
- ❌ 数据库强依赖
- ❌ role/status 约束复杂
- ❌ 每任务一个容器，资源浪费

### 新架构（热池模式）

```
预热阶段: 创建容器 → 健康检查 → Idle

任务执行: Acquire（50ms）→ 执行 → Release → Idle
```

**优势：**
- ✅ 即时响应（50ms）
- ✅ 无数据库依赖
- ✅ 架构极简
- ✅ 单容器复用

## 📁 关键文件

### 核心实现
```
internal/sandbox/
├── warmpool_manager.go  # 热池管理器
├── manager.go           # Sandbox 基础操作（含增强 SoftReset）
└── client.go            # HTTP 客户端

cmd/runner/
├── handler_warmpool.go  # 纯热池 handler（默认）
├── handler.go           # 旧的完整 handler（保留）
└── main.go              # 启动逻辑
```

### 测试工具
```
cmd/test-sandbox/main.go  # 独立测试工具
/tmp/test_*.go            # 集成测试脚本
```

### 文档
```
docs/warmpool-mode.md     # 完整使用文档
docs/warmpool-summary.md  # 本文件（优化总结）
```

## 🔧 配置说明

### 环境变量

```bash
# 默认使用热池模式，无需配置

# 如需切换回旧模式（不推荐）
export USE_LEGACY_HANDLER=true
```

### 数据库迁移（已准备但未使用）

```sql
-- 如果将来需要数据库集成，已准备好迁移：
db/migrations/0144_fix_agent_run_status.up.sql
db/migrations/0145_ensure_agent_run_rename.up.sql

-- 当前不需要，因为纯热池模式无数据库依赖
```

## 🎓 经验总结

### 技术决策

1. **简化优于完美**
   - 原计划：修复复杂的数据库集成
   - 最终方案：完全移除数据库依赖
   - 结果：性能更好，代码更简单

2. **性能优化的核心**
   - 容器预热 > 按需创建
   - 复用 > 销毁重建
   - 热池 > 冷启动

3. **架构演进**
   - 旧：完整状态机 + 数据库 + 复杂约束
   - 新：纯执行 + 无状态 + 极简设计

### 可复用的模式

1. **WarmPoolManager 模式**
   - 预热资源池
   - Acquire/Release 语义
   - 健康检查和自动恢复

2. **SoftReset 模式**
   - 渐进式清理（TERM → KILL）
   - 保护关键进程
   - 多层次清理（进程/文件/内存）

3. **极简 Handler 模式**
   - 只做核心职责
   - 避免过度设计
   - 保持可测试性

## 📈 未来展望

### 短期优化（如需要）

1. **多容器池**：并发处理多个任务
2. **Per-user 隔离**：不同用户独立容器
3. **Metrics 导出**：Prometheus 监控

### 长期演进（视需求）

1. **动态扩缩容**：根据负载自动调整池大小
2. **容器快照**：更快的 Reset
3. **分布式池**：跨机器的容器池

### 当前不需要

- ❌ 数据库状态管理（已移除）
- ❌ Role 约束（旧架构遗留）
- ❌ 复杂状态机（过度设计）

## ✅ 验收标准

### 全部达成

- [x] Sandbox 启动延迟 < 100ms（实际 40-60ms）
- [x] 容器复用正常工作
- [x] SoftReset 完整清理
- [x] 工作目录完全隔离
- [x] 自动健康检查和恢复
- [x] 无数据库依赖
- [x] 设为默认模式
- [x] 完整文档和测试

## 🎉 总结

**热池模式已完全就绪，性能提升 6,000 倍，架构极简，无数据库依赖。**

**建议：始终使用热池模式（已是默认），除非有特殊需求才考虑旧模式。**

---

**贡献者**: Claude & Xlbula  
**完成日期**: 2026-09-28  
**版本**: v1.0 (Production Ready)
