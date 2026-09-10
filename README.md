# Liusha - AI Agent Development Kit (ADK)

基于 LLM 的通用 Agent 开发框架，支持复杂任务的规划、执行、评估和验证。

## 🎯 核心特性

- **三 Agent 架构**：Planner（规划）、Executor（执行）、Evaluator（评估）
- **ReAct 模式**：Objective → Action → Observation → Evaluation → Result
- **知识图谱**：所有数据以图节点存储，支持复杂关系和依赖
- **探索式规划**：Roadmap 支持动态调整，适应执行过程中的变化
- **三级缓存**：内存 → Redis → PostgreSQL，多进程配置同步
- **前端可配置**：Agent 配置、System Prompt、工具集均可通过 API 管理

---

## 📐 架构设计

### 核心概念（ReAct 模式）

```
Objective（目标）
    ↓
Action（动作） ← Planner 规划
    ↓
Observation（观察） ← Executor 执行
    ↓
Evaluation（评估） ← Evaluator 验证
    ↓
Result（结果）
```

### 三个 Agent

| Agent | 职责 | 输入 | 输出 |
|-------|------|------|------|
| **Planner** | 规划和分解任务 | Objective | Action 序列 + Roadmap |
| **Executor** | 执行具体动作 | Action | Observation（原始结果）|
| **Evaluator** | 评估和验证 | Observation | Evaluation + Result |

### 知识图谱（KnowledgeGraph）

所有数据以图节点存储：

```
节点类型：
- Objective: 目标
- Action: 动作
- Observation: 观察
- Evaluation: 评估
- Result: 结果
- Insight: 共享洞察

关系边：
- GENERATES: action → observation
- CONFIRMS: evaluation → result
- REFUTES: evaluation → observation
- DEPENDS_ON: action → action
- ENABLES: result → action
```

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

### 核心文档

- **[架构设计](docs/ARCHITECTURE_FINAL_REVIEW_COMPLETE.md)** - 完整架构说明
- **[配置指南](docs/CONFIGURATION_GUIDE.md)** - Agent 配置和管理
- **[重构报告](docs/REFACTOR_COMPLETE.md)** - 架构重构过程
- **[数据流](docs/DATAFLOW.md)** - 任务生命周期和数据流动

### 开发文档

- **[Cache 包](internal/cache/README.md)** - 三级缓存机制
- **[HTTP API](docs/API.md)** - API 接口文档

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
│   ├── planner/        # Planner Agent
│   ├── executor/       # Executor Agent
│   ├── evaluator/      # Evaluator Agent
│   ├── orchestrator/   # 任务编排
│   ├── knowledgegraph/ # 知识图谱
│   ├── insight/        # 共享洞察
│   ├── config/         # 配置管理
│   │   ├── agent/      # Agent 配置
│   │   ├── llm/        # LLM 配置
│   │   ├── cache/      # 三级缓存
│   │   └── seed/       # 种子数据
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

## 🤝 贡献

欢迎贡献！请遵循以下步骤：

1. Fork 仓库
2. 创建功能分支 (`git checkout -b feature/amazing-feature`)
3. 提交变更 (`git commit -m 'feat: add amazing feature'`)
4. 推送到分支 (`git push origin feature/amazing-feature`)
5. 创建 Pull Request

---

## 📝 变更日志

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

- **文档站点**: [docs.liusha.dev](https://docs.liusha.dev)
- **问题反馈**: [GitHub Issues](https://github.com/V3teran/liusha/issues)
- **讨论**: [GitHub Discussions](https://github.com/V3teran/liusha/discussions)

---

## 👥 致谢

感谢所有贡献者！

---

**Built with ❤️ by V3teran**
