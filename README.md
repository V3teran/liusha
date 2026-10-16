# liusha

多智能体自动化渗透测试平台：四个自治 Agent 围绕一张探索图协作，自主规划、执行、验证漏洞假设，产出机器可复现的漏洞报告。

## 架构

```
cmd/
  api      HTTP 服务（前端后端、会话入口、控制平面）
  runner   任务执行进程（四 Agent 认知循环、流量 ingestor、沙箱热池）
  proxy    MITM 代理（全流量落库）
  vulnapp  本地靶场
  e2e      端到端验收触发器（POST /chat → 轮询探索图断言）
  reseed   agents/*.md、skills/**、工具目录覆盖写入 DB
internal/
  framework/            ADK 层（与业务无关的 Agent 开发套件）
    llm/                Provider 抽象、openai-compat/anthropic 实现、错误分类与重试退避
    runtime/            ReAct 循环、工具注册、中间件（防循环/防无进展/目标对齐）、checkpoint
    core/               图存储接口（Postgres / 内存双实现）
    persistence/        Checkpointer（Postgres / 内存）
  explorationgraph/     探索图：objective → action → observation → result 四类节点
  planner/              规划 Agent：读图生成 Action，消费 result 触发新方向
  executor/             执行 Agent：ReAct 执行 Action，收割 Observation（带复现配方）
  evaluator/            验证 Agent：复现门（机器重放 + LLM 终裁）晋升 result
  monitor/              监察 Agent：活动停摆判死、请求重规划
  finding/              漏洞交付表（晋升成功才写入）
  bus/                  进程内事件总线（四 Agent 解耦协作）
  controlplane/         任务控制平面（pause/resume/terminate/adjust_goal/inject）
  sandbox/              沙箱容器热池（CLI 工具与浏览器自动化运行环境）
```

## 认知循环

1. **planner** 读探索图与被证伪假设，提议 action 节点（DAG 依赖）
2. **executor** 领取可执行 action，跑 ReAct 循环（工具：HTTP/浏览器/CLI 沙箱），
   产出 observation——漏洞假设必须携带自包含复现配方（域信封：web/generic）
3. **evaluator** 对假设过复现门：域重放采证（web 机器重放，generic 裁决官自主取证）→
   LLM 终裁 → confirmed 晋升 result（带语义指纹去重）+ 写 finding；
   refuted 按 `refute_reason` 分类回喂 planner（复现失败可换配方重试，语义证伪方向作废）
4. **monitor** 周期评估：杀活动停摆的 action、请求重规划
5. **completion detector** 判定收敛，经控制平面可人工干预

关键不变式：图写权限分离——planner 写 objective/action，executor 写 observation，
只有 evaluator 写 result、翻转 confidence、写 finding；裁决审计全落 exploration_verification。

## 开发

```bash
make up            # 起 postgres/redis 容器
make migrate       # schema 迁移
make run-api       # api :8090（另有 run-runner / run-web / proxy / vulnapp）
make reseed        # agent 章程与 skills 覆盖写入 DB（改 agents/*.md 后必跑）
make lint          # 含术语门禁 scripts/lint-terminology.sh（旧架构概念防回归）
./scripts/dev/e2e.sh active:xss   # 端到端验收（清库重启四服务 → 触发扫描 → 图断言）
```

Agent 行为契约（章程）在 `agents/*.md`（DB 种子，前端可调）；输出 JSON 契约、
裁决 schema、因果硬规则都在章程里，改契约 = 改章程 + `make reseed`。

## 术语契约

`scripts/lint-terminology.sh` 是旧架构概念的门禁（知识图谱/攻击图/世界模型等已废弃
术语禁止回流）。新代码引入命名前先过门禁；豁免与禁用表即脚本内 FORBIDDEN 列表。
