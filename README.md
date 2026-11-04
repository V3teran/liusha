# liusha

LLM 智能化渗透测试平台：多智能体协同（planner / executor / evaluator / monitor），被动代理流量逐批分析 + 自主下发主动扫描，产出漏洞台账与可复现证据。内测中。

## 形态

- **nginx**（:80）托管 React 前端，同源反代 `/api` → **api**（:8090）控制面
- **proxy**（:8888）MITM 流量入口，浏览器代理指过来即被动抓流
- **runner** ReAct 执行引擎（LLM 调用、扫描编排），按需拉起 Kali 沙盒容器（`ghcr.io/v3teran/liusha-sandbox`）
- **postgres**（pgvector）+ **redis**（队列 / 缓存 / 事件流）

## 部署

### 用户（全容器）

前置：Docker。

```bash
git clone https://github.com/V3teran/liusha.git && cd liusha
cp .env.example .env   # 必填：GLM_API_KEY、LIUSHA_LLM_KEY_SECRET、POSTGRES_PASSWORD、
                       # LIUSHA_ADMIN_INITIAL_PASSWORD（登录初始密码，仅首启建号生效）
docker compose -f compose.yaml -f compose.prod.yaml --profile full pull
docker compose -f compose.yaml -f compose.prod.yaml --profile full up -d
```

数据库迁移随 `up` 自动执行（幂等）。打开 http://localhost，`admin` / `.env` 里的初始密码登录；HTTPS 反代后设 `LIUSHA_COOKIE_SECURE=true`。

单镜像（比赛/受限环境）：`make single-build && make single-run`。

### 开发

```bash
cp .env.example .env && make up   # 起 pg+redis 并应用迁移
./scripts/dev/run-svc.sh          # host 跑 proxy/api/runner
make run-web                      # 前端 dev server
```

### 发布镜像（维护者）

```bash
docker login ghcr.io
make publish   # 构建并推送 api/proxy/runner/nginx（IMAGE_REPO/IMAGE_TAG 可覆盖）
```

用户 `pull` 到的就是这些镜像——改代码后需 `make publish` 才会更新到用户侧。
