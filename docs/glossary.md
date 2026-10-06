# 术语契约（glossary）

本文件是 `scripts/lint-terminology.sh`（术语门禁）的契约正文：定义命名口径、
禁止术语与豁免区。改名/新表/新列前先对照本文；门禁脚本的模式列表与本文必须同步维护。

## 核心名词：实体 vs 执行实例

| 术语 | 含义 | 承载物 |
|---|---|---|
| **agent** | 实体：配置好的角色定义（planner/executor/evaluator/monitor 四个 code） | `agent` 表、`internal/agent` |
| **agent_run** | 执行实例：一次认知循环（一个 task 的一轮，四个 agent 共用；api 以 Role=planner 创建） | `agent_run` 表、`internal/agentrun` |
| **role** | 角色维度：本轮由哪个角色驱动（`worker.RoleExecutor`、`agent_run.role`、`llm_invocation.role`） | 字符串 code，非外键 |

## 外键列命名三规则（防摇摆，本列曾三轮改名 agent_id→agent_run_id→agent_task_id→agent_run_id）

1. **外键列名 = 引用表名单数 + `_id`**，指到哪张表就叫什么：
   `agent_run_id` 引 `agent_run(id)`。
2. **`agent_id` 永久保留**给 `agent` 实体表外键——即使当前无表引用。
   任何值为 run id 的列/字段/JSON 键/日志键**禁止**叫 `agent_id`（历史上
   `insight.source_agent_id` 装的实为 run id，即此歧义的活例，0156 已纠正）。
3. **执行实例统一 `agent_run_id`**（业界同型：Temporal `run_id`、Airflow `dag_run`、
   GitHub Actions workflow run、MLflow run）。同义词 `execution`/`executor`/`task_id`
   不用于引用列。

## "executor" 词汇的合法边界

- **合法**：角色维度——`worker.RoleExecutor`、`llm_invocation.role="executor"`、
  `agents/executor.md` 角色章程、agent 表 code。
- **历史保留区**（存量契约，不新增使用）：
  - REST `/executors` 路由族 + `configstore.ExecutorByID/ByCode`（agent 配置实体
    的 CRUD 旧称，web 前端已依赖，改名属跨栈独立工程）；
  - 沙箱 ingest wire 的 `executor_id` JSON 键（发送端在容器镜像，仓外契约）。
- **非法**：任何新代码用 executor 命名 run 引用列、store 字段、日志/JSON 键。

## 已知双义字段（有意保留，勿"顺手统一"）

- `sandbox.ExecRequest.AgentID`：执行者身份标签，通常为 run id，但清扫/健康检查
  等 housekeeping 调用传合成标签（"cleanup"/"warmpool-manager"）——不是纯外键。
- `worker.Payload.AgentRunID`：api→runner 队列消息的幂等键 = run id（见 client.go）。

## 废弃术语（门禁脚本禁用清单，此处为语义备忘）

知识图谱 / 认知图 / 世界模型 / knowledge graph / world model / scenario / swarm /
solo / 猎手——旧架构概念，详见 `scripts/lint-terminology.sh` 的 FORBIDDEN 列表。
