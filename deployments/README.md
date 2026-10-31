# 部署形态

liusha 有三种运行形态，各自面向不同环境。**沙盒说明先行**：runner 永远经
**宿主 docker** 拉起沙盒容器（pentools 镜像，warm pool 自动管理、单例复用），
因此任何形态下 runner 所在位置（host 或容器）都必须能访问 docker daemon，
且宿主上要有 pentools 镜像（`make build-pentools`）。

| | A 本地开发 | B 生产全容器 | C 单镜像 |
|---|---|---|---|
| 面向 | 日常开发调试 | 正式部署 | 比赛/受限环境 |

| 容器 | pg、redis、沙盒（3 个） | pg/redis/api/nginx/proxy/runner/沙盒 | liusha/single（1 个）+ 沙盒（宿主侧） |
| 业务进程 | host 二进制（run-svc.sh） | 容器 | 容器内多进程 |
| 迁移 | `make migrate`（host） | `make migrate`（host，对映射到宿主的 5432 执行） | 入口脚本自动跑 |

## A 本地开发

```bash
make dev-up        # = scripts/dev/up.sh：起 pg+redis → 等 healthy → migrate
make build-pentools # 沙盒镜像（首次/工具变更后）
./scripts/dev/run-svc.sh   # host 二进制起 proxy+api+runner（日志 logs/*.log）
# 收工：make dev-down（加 -v 彻底清数据）
```

要点：runner 是 host 进程，直接用本机 docker 拉沙盒（第三个容器）；
改代码即重跑 run-svc.sh，无需重建镜像。

环境文件：仓库根 `.env` 是唯一真值——docker compose（docker-compose.yml 在
仓库根）插值自动加载，run-svc.sh / e2e.sh 亦 source 同一份；模板见
`.env.example`（复制为 `.env` 填真实 key）。

## B 生产全容器

```bash
make migrate       # schema 先建（host 侧 go run golang-migrate，对映射到宿主的 5432）
make prod-up       # 6 核心服务 up（含 prod overlay）
make prod-down
```

- 核心 6 服务（postgres/redis/api/nginx/proxy/runner）默认全起，无 profile 门控。
- prod overlay 差异：`LIUSHA_ENV=production`（stdout JSON）、日志格式锁 json；
  文件日志保持开启（logs/ 共享卷是「日志」页的数据面）。
- 密钥经仓库根 `.env.prod` 注入（`--env-file .env.prod`；与 dev 真值根
  `.env` 隔离，绝不入 git）。

## C 单镜像

```bash
make single-build  # 构建 liusha/single（amd64 目标机用 buildx --platform linux/amd64）
make single-run    # 或见下方完整 docker run
```

```bash
docker run -d --name liusha-single \
  -p 80:80 -p 8888:8888 -p 9090:9090 \
  -v liusha-single-data:/data \
  -v /var/run/docker.sock:/var/run/docker.sock \
  -e GLM_API_KEY=<key> \
  liusha/single
```

一个容器内跑全家：PostgreSQL(pgvector) + Redis + proxy + runner + api +
nginx。入口脚本进程托管：initdb → migrate → 依次拉起；**任一进程退出即整体
关停**（交给 `docker run --restart` 类编排重启，不做容器内自愈）。

- **必须挂 docker.sock**：沙盒容器由 runner 经宿主 docker 拉起（pentools 镜像
  须在宿主存在）；沙盒回连 ingest 走 `host.docker.internal:9090`，故 **9090
  必须发布**。
- **必须发布 8888**：浏览器挂 MITM 代理走宿主 8888。
- **数据全在 /data 卷**：PG、Redis（AOF）、CA 证书、首启生成的密钥
  （API key / SSE secret / LLM 加密密钥——env 未给时首启生成并持久化，重启
  不变；API key 启动日志会打印）。
- 比赛下发：两台镜像 `docker save liusha/single ghcr.io/v3teran/liusha-pentools
  | gzip > liusha.tar.gz` → 目标机 `docker load`。

## 镜像清单

| 镜像 | Dockerfile | 说明 |
|---|---|---|
| liusha/api | cmd/api/Dockerfile | Go vendor 构建 + 种子目录 |
| liusha/runner | cmd/runner/Dockerfile | + docker-cli（沙盒拉起） |
| liusha/proxy | cmd/proxy/Dockerfile | 纯 MITM |
| liusha/nginx | deployments/nginx/Dockerfile | 多阶段：node 构建前端 + nginx 托管 |
| liusha/single | deployments/single/Dockerfile | 全家桶（pg+redis+3进程+nginx） |
| pentools | deployments/tool-images/pentools/ | 沙盒（base 由 CI 推 GHCR，final 本地构建） |

直接拉取的上游镜像（非本仓构建）：`pgvector/pgvector:pg17`（postgres）、
`redis:8`——由 docker-compose.yml 引用，不在本地构建清单内。
