# 术语表（Terminology Glossary）

单一事实源：本表是全仓术语的唯一权威映射。`make lint-terminology` 按本表执行禁用词门禁，
CI 与本地 `make lint` 均会执行；新增术语变更必须先改本表再改代码。

## 现行术语（canonical）

| 术语 | 中文 | 含义 |
|---|---|---|
| ExplorationGraph / exploration graph | 探索图 | 智能体共享的世界状态图：objective/action/observation/result 节点 + 关系边，持久化于 wm_* 表。规划、执行、验证、验收断言的唯一事实源 |
| Agent / agent | 智能体/操作员 | `agent` 表中的角色定义（planner/executor/evaluator），运行实例为 agent_run |
| Assignment | 测试任务单 | 一次测试委派；一切 task 皆属某 assignment |
| Task | 任务 | assignment 下的一次执行单元（active/passive） |
| Insight | 情报 | assignment 级情报黑板（internal/insight） |
| Finding | 漏洞结论 | 复现晋升门验证通过后的落库漏洞（finding 表） |

## 已废弃术语（deprecated → 现行术语）

| 废弃名 | 现行名 | 备注 |
|---|---|---|
| 知识图谱 / KnowledgeGraph / knowledge graph / knowledgegraph | 探索图 / ExplorationGraph | 2026-09 命名统一；web 路由同步为 /exploration-graph |
| 认知图 | 探索图 | 同一概念的第三种叫法，废弃 |
| 世界模型 / WorldModel / worldmodel | 探索图 / ExplorationGraph | 旧包名 worldmodel 已删；概念上探索图即世界状态的唯一事实源 |
| scenario / 场景 / ScenarioPicker | Assignment / 测试任务单 | scenario 概念于迁移 0126 全面删除；/findings/s 死端点与 DistinctScenarios 死方法随之移除 |
| swarm / solo（引擎模式） | 统一认知循环（四智能体） | 引擎模式选择已删；agent_run.planner_id 列保留作父子任务链数据 |
| 猎手 / hunter（作为代码标识符） | Agent / 智能体 | 猎手为 hunter 中文残留；env 契约 LIUSHA_AGENT_ID 例外（见保留区） |
| planned_move / Move（作为代码标识符） | Action | 0121 已并入探索图；控制命令字面量 inject_move 例外（见保留区） |
| agent_task / executor_run / react_run | agent_run | 运行记录表历次改名终名 |

## 契约保留区（wire contract，禁止顺手改名）

以下标识符源自旧命名，但属于**跨边界契约**，按"契约稳定优先"原则保留，
任何变更需要版本化/协调式迁移（前端 + 容器镜像 + DB 同步），禁止在纯重命名提交中修改：

| 标识符 | 边界 | 说明 |
|---|---|---|
| `LIUSHA_AGENT_ID` 环境变量 | runner ↔ 沙箱容器镜像 | launcher 注入、pentools 镜像内脚本消费；改名需重建镜像 |
| `inject_move` 控制命令 | HTTP API（control plane） | 前端 `types.ts` 与后端 `controlplane.Command` 共享的字面量 |
| `wm_node` / `wm_edge` / `wm_verification` / `wm_roadmap_step` 表与列 | PostgreSQL schema | 改名需数据迁移，且历史迁移文件不可编辑 |
| HTTP 路径 `/tasks/:id/graph` `/nodes` `/stats` | HTTP API | e2e 与前端共用；"graph" 为中性词，无歧义 |

## 允许的英文领域用语

`engagement`（一次测试交战）、`recon`、`exploitation` 等安全行业通用英语允许出现在
自然语言注释与提示词中；禁止作为代码标识符引入。
