# liusha

LLM 智能化渗透测试平台：多智能体（planner / executor / evaluator / monitor）协同，被动代理流量逐批分析 + 自主下发主动扫描，产出漏洞台账（findings）与可复现证据。内测中。

## 形态

三个进程 + 两个存储 + 一个前置：

- **api**（:8090）控制面 HTTP + React 前端（经 nginx :80 同源反代 `/api`）
- **proxy**（:8888）MITM 流量入口，浏览器指过来即被动抓流
- **runner** Asynq worker：ReAct 执行引擎（LLM 调用、沙盒工具、扫描编排）
- postgres（pgvector）+ redis（队列/缓存/事件流）

## 快速开始（本地开发）

```bash
cp .env.example .env          # 填 GLM_API_KEY、LIUSHA_LLM_KEY_SECRET 等
make up                       # 起 postgres + redis 并应用迁移
./scripts/dev/run-svc.sh      # host 跑 proxy / api / runner（依赖 .env）
make run-web                  # 前端 dev server（vite 默认端口，/api 已代理到本机 api）
```

登录：`admin` / `LIUSHA_ADMIN_INITIAL_PASSWORD` 的值（run-svc.sh 开发默认 `changeme-admin-pass`）。该变量**仅在账号表为空的首次启动时生效**用于建号，之后改密走前端「头像菜单 → 修改密码」。

## 生产部署（全容器）

```bash
# .env 需额外设置：LIUSHA_ADMIN_INITIAL_PASSWORD、LIUSHA_LLM_KEY_SECRET、
# GLM_API_KEY、POSTGRES_PASSWORD、LIUSHA_COOKIE_SECURE=true（HTTPS 反代后）
make prod-build && make prod-up     # 六容器全起（nginx :80）
```

数据落在 `~/data/liusha/`（`HOST_DATA_DIR` 可改）。单镜像形态（比赛/受限环境）见 `make single-build` 与 `deployments/single/`。

## 使用

1. **代理模式**：浏览器代理指向 `:8888` 浏览目标 → 流量按 host 成批（~30s 窗口）进入分析 → 「对话 → 代理模式」实时看 agent 推理与漏洞产出。
2. **自主下发**：「对话 → 自主下发 → 新对话」，描述目标与测试方向（URL、账号、意图）即可发起。
3. 漏洞台账在「漏洞」页 triage；原始请求证据在「代理流量」页。

## 常用命令

| 命令 | 作用 |
|---|---|
| `make migrate` / `migrate-down` | 应用 / 回退一个迁移 |
| `make logs` | 跟随各进程日志 |
| `make test` / `lint` | 单测 / vet + golangci-lint + gofmt |
| `go run ./cmd/reseed` | 种子（agents/skills 提示词）改动后强制同步已初始化的库 |
| `./scripts/dev/e2e.sh` | 端到端验收（DVWA 靶场） |

更多细节：`deployments/README.md`（部署）、`agents/`（智能体提示词种子）、`skills/`（漏洞/工具知识库种子）。
