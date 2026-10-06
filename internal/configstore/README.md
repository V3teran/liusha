# Cache 包 - 多级配置缓存层

## 概述

`cache` 包实现了配置的三级缓存架构：**内存 L1** → **Redis L2** → **PostgreSQL**。

## 架构设计

```
┌─────────────────────────────────────────────────────────────┐
│                     API 进程                                  │
│  ┌──────────┐   ┌──────────┐   ┌──────────────────────┐   │
│  │ 内存 L1  │ → │ Redis L2 │ → │ PostgreSQL（事实源） │   │
│  └──────────┘   └──────────┘   └──────────────────────┘   │
│       ↑              ↑                                       │
│       └──────────────┴─ Redis Pub/Sub 失效总线               │
└─────────────────────────────────────────────────────────────┘
                         ↓ 失效广播
┌─────────────────────────────────────────────────────────────┐
│                    Runner 进程                                │
│  ┌──────────┐   ┌──────────┐   ┌──────────────────────┐   │
│  │ 内存 L1  │ → │ Redis L2 │ → │ PostgreSQL（事实源） │   │
│  └──────────┘   └──────────┘   └──────────────────────┘   │
│       ↑              ↑                                       │
│       └──────────────┴─ 被动失效（收到广播）                 │
└─────────────────────────────────────────────────────────────┘
```

## 为什么需要三级缓存？

### 问题
Liusha 是多进程架构：
- **API 进程**：处理前端请求，可能修改配置
- **Runner 进程**：执行任务，需要读取配置

如果 API 进程修改配置后，Runner 进程的本地缓存不失效，会导致：
- ❌ Runner 使用旧配置装配 Agent
- ❌ 配置不一致
- ❌ 行为不符合预期

### 解决方案：失效广播

1. **写入流程**：
   - API 进程更新 PostgreSQL
   - 通过 Redis Pub/Sub 广播失效消息
   - 所有进程收到消息后清除本地 L1 + L2

2. **读取流程**：
   - 先查内存 L1 → 命中直接返回
   - 未命中查 Redis L2 → 命中回填 L1
   - 未命中查 PostgreSQL → 回填 L2 + L1

## 使用方式

### 初始化

```go
import (
    "github.com/V3teran/liusha/internal/cache"
    "github.com/V3teran/liusha/internal/cachestore"
)

// 1. 创建 cachestore 内核
cacheCore := cachestore.New(redisClient, 0)

// 2. 启动失效订阅（后台goroutine）
go func() {
    if err := cacheCore.Subscribe(ctx); err != nil {
        log.Error("cachestore 订阅失败")
    }
}()

// 3. 创建 cache Store
store := cache.New(pgPool, cacheCore)
```

### 读取配置

```go
// 按 code 读取 Agent（直穿 DB，不缓存）
agent, err := store.GetAgentByCode(ctx, "planner")

// 按 ID 读取（走缓存）
agent, err := store.ExecutorByID(ctx, agentID)
```

### 更新配置

```go
updates := cfgagent.UpdateParams{
    SystemPrompt: &newPrompt,
    MaxIterations: &newMax,
}

// 更新会自动：
// 1. 写入 PostgreSQL
// 2. 广播失效消息
// 3. 所有进程清除缓存
agent, err := store.UpdateAgent(ctx, agentID, updates)
```

## 缓存策略

### 缓存的内容

| 方法 | 缓存键 | 说明 |
|------|--------|------|
| `ExecutorByID` | `cache:executor:id:{id}` | 单条读，走缓存 |
| `ComplexityByCode` | `cache:executor:complexity:code:{code}` | 复杂度映射，走缓存 |
| `ListExecutors` | `cache:executors:list:all/enabled` | 全量列表，走缓存 |

### 不缓存的内容

| 方法 | 原因 |
|------|------|
| `ExecutorByCode` | 直穿 DB，保证最新 |
| `ListExecutorsPaged` | 键空间无限增长（搜索词 × 分页），会内存泄漏 |
| `CountExecutors` | 低频操作，缓存收益低 |

## 失效机制

### 写入时失效

```go
func (s *Store) UpdateAgent(ctx context.Context, id string, p UpdateParams) {
    // 1. 更新数据库
    agent := s.executors.Update(ctx, id, p)
    
    // 2. 失效相关缓存键
    keys := []string{
        "cache:executor:id:" + agent.ID,
        "cache:executor:code:" + agent.Code,
        "cache:executors:list:all",
        "cache:executors:list:enabled",
    }
    s.cache.Invalidate(ctx, keys...)
}
```

### 跨进程失效

```
API 进程                          Runner 进程
   │                                 │
   │ 1. UpdateAgent()                │
   │ 2. 写 PostgreSQL                 │
   │ 3. cache.Invalidate()           │
   │    ↓                             │
   │ 4. Redis PUBLISH "cache:invalidate" │
   │    ├─────────────────────────────→ │
   │                                 │ 5. 收到消息
   │                                 │ 6. 清除 L1 + L2
   │                                 │
```

## 性能优化

### L1（内存）命中率
- 单进程内重复读取
- 无网络开销
- **命中延迟：< 1μs**

### L2（Redis）命中率
- 跨进程共享
- 网络开销小
- **命中延迟：< 1ms**

### L3（PostgreSQL）
- 事实源
- 只在缓存未命中时访问
- **延迟：5-10ms**

## 注意事项

### ✅ 推荐做法

1. **通过 cache 包修改配置**
   ```go
   store.UpdateAgent(ctx, id, updates)  // ✅ 自动失效缓存
   ```

2. **读取高频数据走缓存方法**
   ```go
   store.ExecutorByID(ctx, id)  // ✅ 走缓存
   ```

### ❌ 避免的做法

1. **直接修改数据库**
   ```go
   db.Exec("UPDATE agent SET ...")  // ❌ 缓存不会失效
   ```

2. **绕过 cache 包**
   ```go
   cfgagent.Store.Update(...)  // ❌ 缓存不会失效
   ```

## 故障排查

### 问题：配置修改后未生效

**可能原因：**
1. 缓存未失效
2. Redis 连接断开
3. Subscribe goroutine 未运行

**排查步骤：**
```bash
# 1. 检查 Redis 连接
redis-cli PING

# 2. 检查订阅状态
redis-cli PUBSUB CHANNELS "cache:*"

# 3. 检查日志
grep "cachestore" logs/app.log
```

### 问题：不同进程看到的配置不一致

**可能原因：**
- 失效广播未送达

**解决方案：**
```go
// 手动清除缓存
store.cache.Invalidate(ctx, "cache:executor:id:xxx")

// 或重启进程
```

## 监控指标

建议监控：
- L1 命中率
- L2 命中率
- 失效消息延迟
- 缓存键数量

## 相关文档

- [Configuration Guide](../../docs/CONFIGURATION_GUIDE.md)
- [Architecture](../../docs/ARCHITECTURE_FINAL_REVIEW_COMPLETE.md)
