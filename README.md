# Liusha - 自主安全测试系统

基于 LLM 驱动的多 Agent 协作架构，自动发现和验证 Web 应用安全漏洞。

## 核心文档

- **[docs/ARCHITECTURE.md](docs/ARCHITECTURE.md)** - 系统架构文档
  - 组件设计
  - 数据模型
  - 并发控制
  - 部署架构
  - 配置说明

- **[docs/DATAFLOW.md](docs/DATAFLOW.md)** - 数据流文档
  - 任务生命周期
  - 组件间数据流
  - 事件流动
  - 状态转换
  - 性能数据

## 快速开始

### 启动服务

```bash
# 启动数据库
docker-compose up -d postgres

# 运行数据库迁移
make migrate

# 启动 API 服务
go run ./cmd/api

# 启动 Worker（另一个终端）
go run ./cmd/runner
```

### 创建扫描任务

```bash
curl -X POST http://localhost:8080/api/tasks \
  -H "Content-Type: application/json" \
  -d '{
    "target": "https://example.com",
    "scan_type": "active"
  }'
```

## 架构概览

```
Orchestrator (协调中心)
  ├── Planner Agent (规划)
  ├── Monitor Agent (监察)
  ├── Executor Pool (执行)
  ├── Verifier Agent (验证)
  └── EventBus (通信)
       ↓
  WorldModel Store (状态管理)
       ↓
  PostgreSQL (持久化)
```

## 技术特性

- **多 Agent 协作**: 职责清晰、易于扩展
- **事件驱动**: 解耦组件、异步通信
- **CAS 乐观锁**: 并发安全、高性能
- **指纹去重**: 避免重复探索
- **水平扩展**: 支持多实例部署

## 开发

```bash
# 编译
make build

# 测试
make test

# 代码检查
make lint
```

## License

MIT
