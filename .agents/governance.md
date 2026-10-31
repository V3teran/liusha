# liusha 治理配置

full-governance / legacy-purge 技能运行时读取本文件，作为领域锚点、波次划分与验证命令的单一事实源。架构大变后请同步更新本文件——它本身就是「文档即现状」的一部分。

## 领域锚点（阶段一事实基线必须覆盖）

- **整体架构与数据流**：api / proxy / runner 三个服务 + internal/ 下各业务域
- **agent 框架**：agents/ 下四个 agent 定义（planner / executor / monitor / evaluator）+ internal/agent、internal/cognition、internal/framework 等实现
- **探索图**：internal/explorationgraph + 前端对应页面
- **前端**：web/src/features 各 feature 域（React 19 + TS + Vite）

## 波次划分参考

- 后端：internal/ 按业务域分波（单域文件过多再拆）；cmd/ 各入口、proxy、runner 各自成波
- 前端：web/src 单独成波

## 验证门

```bash
go build ./... && go vet ./... && go test ./...
golangci-lint run            # 仓库已有 .golangci.yml
make web-build && make web-test   # tsc -b && vite build；vitest
```

- dbtest 类集成测试依赖外部数据库，起不来时明确列出跳过项与原因，禁止静默跳过
- 语言技能基准：已安装 samber golang-* 五件套（lint / error-handling / testing / concurrency / security）与 vercel-react-best-practices
