# Liusha - AI Agent Development Kit (ADK)

基于 LLM 的通用 Agent 开发框架，支持复杂任务的规划、执行、评估和验证。

## 🎯 核心特性

- **三 Agent 架构**：Planner（规划）、Executor（执行）、Evaluator（评估）
- **ReAct 模式**：Objective → Action → Observation → Evaluation → Result
- **知识图谱**：所有数据以图节点存储，支持复杂关系和依赖
- **探索式规划**：Roadmap 支持动态调整，适应执行过程中的变化
- **三级缓存**：内存 → Redis → PostgreSQL，多进程配置同步
- **前端可配置**：Agent 配置、System Prompt、工具集均可通过 API 管理
- **Framework 层**：通用的 Agent 运行时，支持状态管理、Checkpoint、流式传输

---

## 📐 架构设计

### 双层架构

```
Framework 层（通用 Agent 运行时）
    ↓ 提供基础设施能力
业务层（Liusha 工作流）
    ↓ 实现特定业务逻辑
最终用户
```

**Framework 层**（`internal/framework`）：
- 核心抽象：Agent 接口、State 管理、Graph 执行引擎
- 运行时：Orchestrator、GraphExecutor
- 持久化：Checkpoint（Memory/Postgres）
- 中间件：流式传输、拦截器、恢复机制

**业务层**（`internal/orchestrator` + Agents）：
- 4 个业务 Agent：Planner、Executor、Monitor、Evaluator
- 业务编排器：Plan → Execute → Monitor → Evaluate 闭环
- KnowledgeGraph：业务数据的事实源

详见：[架构可视化](docs/ARCHITECTURE_VISUAL.md)

---

## 🚀 快速开始

### 前置要求

- Go 1.21+
- PostgreSQL 14+
- Redis 6+

### 安装

```bash
# 克隆仓库
git clone https://github.com/V3teran/liusha.git
cd liusha

# 安装依赖
go mod download

# 配置
cp config.example.yaml config.yaml
# 编辑 config.yaml，填入数据库和 Redis 配置
```

### 启动服务

```bash
# 1. 启动数据库（Docker）
docker-compose up -d postgres redis

# 2. 运行数据库迁移
make migrate-up

# 3. 启动 API 服务
make run-api
# 或
go run ./cmd/api

# 4. 启动 Runner（另一个终端）
make run-runner
# 或
go run ./cmd/runner
```

### 创建任务

```bash
# 创建一个 Assignment
curl -X POST http://localhost:8080/api/assignments \
  -H "Content-Type: application/json" \
  -d '{
    "objective": "分析用户留存数据",
    "context": "需要分析最近30天的用户行为"
  }'
```

---

## 📚 文档

### 架构文档

- **[架构可视化](docs/ARCHITECTURE_VISUAL.md)** - 层次关系图和数据流图
- **[架构设计](docs/ARCHITECTURE_FINAL_REVIEW_COMPLETE.md)** - 完整架构说明
- **[集成总结](docs/INTEGRATION_SUMMARY.md)** - Framework 与业务层的关系

### 集成指南

- **[Phase 10-13 集成计划](docs/PHASE_10_13_INTEGRATION_PLAN.md)** - 完整的实施方案
- **[Phase 10 快速启动](docs/QUICK_START_PHASE10.md)** - 立即开始的操作指南

### 开发文档

- **[配置指南](docs/CONFIGURATION_GUIDE.md)** - Agent 配置和管理
- **[重构报告](docs/REFACTOR_COMPLETE.md)** - 架构重构过程
- **[数据流](docs/DATAFLOW.md)** - 任务生命周期和数据流动
- **[Cache 包](internal/cache/README.md)** - 三级缓存机制

---

## 🔧 配置管理

### Agent 配置

三个 Agent 的配置均可通过 API 管理：

```bash
# 列出所有 Agent
curl http://localhost:8080/api/agents

# 获取单个 Agent
curl http://localhost:8080/api/agents/planner

# 更新 Agent 配置
curl -X PUT http://localhost:8080/api/agents/planner \
  -H "Content-Type: application/json" \
  -d '{
    "system_prompt": "新的 system prompt...",
    "max_iterations": 50
  }'

# 重置为默认配置
curl -X POST http://localhost:8080/api/agents/planner/reset
```

详细配置指南：[CONFIGURATION_GUIDE.md](docs/CONFIGURATION_GUIDE.md)

---

## 🏗️ 项目结构

```
liusha/
├── cmd/
│   ├── api/            # API 服务
│   └── runner/         # 任务执行服务
│
├── internal/
│   ├── framework/      # Framework 层（通用 Agent 运行时）
│   │   ├── core/       # 核心抽象（Agent 接口、State、Graph）
│   │   ├── runtime/    # 运行时（Orchestrator、GraphExecutor）
│   │   ├── middleware/ # 中间件（流式传输、拦截器）
│   │   └── persistence/# 持久化（Checkpoint）
│   │
│   ├── planner/        # Planner Agent
│   ├── executor/       # Executor Agent
│   ├── evaluator/      # Evaluator Agent
│   ├── monitor/        # Monitor Agent
│   ├── orchestrator/   # 业务编排器
│   │
│   ├── knowledgegraph/ # 知识图谱
│   ├── provider/       # LLM Provider 抽象
│   ├── cache/          # 三级缓存
│   └── httpapi/        # HTTP API
│
├── db/
│   └── migrations/     # 数据库迁移
│
└── docs/               # 文档
```

---

## 🧪 测试

```bash
# 运行所有测试
make test

# 运行单元测试
go test ./...

# 运行集成测试
make test-integration

# 运行特定包的测试
go test ./internal/planner -v
```

---

## 📊 监控

### 健康检查

```bash
# API 健康检查
curl http://localhost:8080/health

# Runner 状态
curl http://localhost:8080/api/runner/status
```

### 日志

```bash
# 查看 API 日志
tail -f logs/api.log

# 查看 Runner 日志
tail -f logs/runner.log

# 查看 Agent 执行日志
grep "planner" logs/runner.log
```

---

## 🚧 当前状态

### 已完成（v2.0）

- ✅ 三 Agent 架构（Planner/Executor/Evaluator）
- ✅ ReAct 模式实现
- ✅ 知识图谱替代 worldmodel
- ✅ Evaluator Agent（验证和评估）
- ✅ 通用 Agent API（前端可配置）
- ✅ 三级缓存（多进程同步）
- ✅ Roadmap 探索式规划

### Framework 层（v3.0 - Phase 1-9 已完成）

- ✅ Phase 1-9: 核心抽象、图执行、状态管理、持久化、中间件
- ⬜ Phase 10: Agent 适配器（**当前任务**）
- ⬜ Phase 11: Runtime 集成
- ⬜ Phase 12: Checkpoint 集成
- ⬜ Phase 13: 流式传输集成

详见：[集成总结](docs/INTEGRATION_SUMMARY.md)

---

## 🤝 贡献

欢迎贡献！请遵循以下步骤：

1. Fork 仓库
2. 创建功能分支 (`git checkout -b feature/amazing-feature`)
3. 提交变更 (`git commit -m 'feat: add amazing feature'`)
4. 推送到分支 (`git push origin feature/amazing-feature`)
5. 创建 Pull Request

---

## 📝 变更日志

### v3.0.0 (开发中)

**Framework 层：**
- ✅ Phase 1-9: 通用 Agent 运行时
- ⬜ Phase 10-13: 业务层集成

### v2.0.0 (2024-09-10)

**架构重构：**
- ✅ 重构为通用 ADK（从安全专用 → 通用框架）
- ✅ 三 Agent 架构（Planner/Executor/Evaluator）
- ✅ ReAct 模式实现
- ✅ 知识图谱替代 worldmodel
- ✅ 中性命名（移除安全术语）

**新功能：**
- ✅ Evaluator Agent（验证和评估）
- ✅ 通用 Agent API（前端可配置）
- ✅ 三级缓存（多进程同步）
- ✅ Roadmap 探索式规划

**优化：**
- ✅ Priority 统一为字符串枚举
- ✅ 目录命名优化（llmcfg→llm, configstore→cache）
- ✅ 完善文档体系

---

## 📄 许可证

[MIT License](LICENSE)

---

## 🔗 相关链接

- [GitHub 仓库](https://github.com/V3teran/liusha)
- [架构设计文档](docs/ARCHITECTURE_VISUAL.md)
- [集成指南](docs/INTEGRATION_SUMMARY.md)
- [快速启动](docs/QUICK_START_PHASE10.md)

---

**立即开始 Phase 10，让 Liusha 进入 v3.0 时代！** 🚀
