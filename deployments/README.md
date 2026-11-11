# 部署形态

liusha 有三种运行形态，各自面向不同环境。**沙盒说明先行**：runner 永远经
**宿主 docker** 拉起沙盒容器（sandbox 镜像，warm pool 自动管理、单例复用），
因此任何形态下 runner 所在位置（host 或容器）都必须能访问 docker daemon，
且宿主上要有 sandbox 镜像：B 形态由 `compose pull` 随业务镜像同版本一并拉取
（compose.yaml 的 `sandbox` 条目，scale 0 只拉不起）；A 形态本地 `make build-sandbox`。

| | A 本地开发 | B 生产全容器 | C 单镜像 |
|---|---|---|---|
| 面向 | 日常开发调试 | 正式部署 | 比赛/受限环境 |

| 容器 | pg、redis、沙盒（3 个） | pg/redis/migrate（一次性）/api/nginx/proxy/runner/沙盒 | liusha/single（1 个）+ 沙盒（宿主侧） |
| 业务进程 | host 二进制（run-svc.sh） | 容器 | 容器内多进程 |
| 迁移 | `make migrate`（host，`make dev-up` 已含） | compose `migrate` 一次性服务随 `up` 自动跑 | 入口脚本自动跑 |

## A 本地开发

```bash
make dev-up        # = scripts/dev/up.sh：起 pg+redis → 等 healthy → migrate
make build-sandbox # 沙盒镜像（首次/工具变更后）
./scripts/dev/run-svc.sh   # host 二进制起 proxy+api+runner（日志 logs/*.log）
# 收工：make dev-down（加 -v 彻底清数据）
```

要点：runner 是 host 进程，直接用本机 docker 拉沙盒（第三个容器）；
改代码即重跑 run-svc.sh，无需重建镜像。

环境文件：仓库根 `.env` 是唯一真值——docker compose（compose.yaml 在仓库根）
插值自动加载，run-svc.sh / e2e.sh 亦 source 同一份；模板见 `.env.example`
（复制为 `.env` 填真实 key）。compose 分层：`compose.yaml` 是中性基座（服务
拓扑，不含任何环境语义；业务服务挂 `full` profile，裸 `up` 只起 postgres/redis 供本地开发），
`compose.prod.yaml` 是生产 overlay——**叠加了它就是生产形态**；密钥缺失即报错
（基座不兜底任何默认值）。

## B 生产全容器

```bash
make prod-up       # 本地构建镜像并 up（compose.yaml + compose.prod.yaml，--profile full）
make prod-down
```

用户侧拉取发布镜像部署的步骤见仓库根 README（检出发布 tag → `pull` → `up`）。

- `full` profile 起全部业务服务：api/nginx/proxy/runner，以及 `migrate` 一次性
  迁移服务（成功退出后业务服务才起，幂等）；`sandbox` 条目 scale 0，只随 `pull` 预拉不运行。
- prod overlay 差异：`LIUSHA_ENV=production`（stdout JSON）、日志格式锁 json；
  文件日志保持开启（logs/ 共享卷是「日志」页的数据面）。
- 密钥与 A 形态同源：仓库根 `.env`（模板 `.env.example`，绝不入 git）。

## C 单镜像

```bash
make single-build  # 构建 liusha/single（amd64 目标机用 buildx --platform linux/amd64）
make single-run    # 或见下方完整 docker run
```

```bash
docker run -d --name liusha-single \
  -p 80:80 -p 8588:8588 -p 8590:8590 \
  -v liusha-single-data:/data \
  -v /var/run/docker.sock:/var/run/docker.sock \
  -e GLM_API_KEY=<key> \
  liusha/single
```

一个容器内跑全家：PostgreSQL(pgvector) + Redis + proxy + runner + api +
nginx。入口脚本进程托管：initdb → migrate → 依次拉起；**任一进程退出即整体
关停**（交给 `docker run --restart` 类编排重启，不做容器内自愈）。

- **必须挂 docker.sock**：沙盒容器由 runner 经宿主 docker 拉起（sandbox 镜像
  须在宿主存在）；沙盒回连 ingest 走 `host.docker.internal:8590`，故 **8590
  必须发布**。
- **必须发布 8588**：浏览器挂 MITM 代理走宿主 8588。
- **数据全在 /data 卷**：PG、Redis（AOF）、CA 证书、首启生成的密钥
  （LLM 加密密钥——env 未给时首启生成并持久化，重启不变）。
- **沙盒工作区在宿主侧**：runner（容器内）拉起的沙盒把宿主目录 bind 进
  `/root/liusha/workspace`。宿主根目录默认按 runner 进程的 HOME 解析
  （root 运行 = `/root/data/liusha/workspace`）；需固定位置时以
  `-e LIUSHA_SANDBOX_WORKSPACE_DIR=<宿主绝对路径>` 覆盖（bind 源由宿主
  docker daemon 解析，必须写宿主路径）。
- 比赛下发：两台镜像 `docker save liusha/single ghcr.io/v3teran/liusha-sandbox
  | gzip > liusha.tar.gz` → 目标机 `docker load`。

## 镜像清单

| 镜像 | Dockerfile | 说明 |
|---|---|---|
| ghcr.io/v3teran/liusha-api | cmd/api/Dockerfile | Go vendor 构建（agent 与技能出厂内容编译在程序里）；兼作 `migrate` 一次性服务 |
| ghcr.io/v3teran/liusha-runner | cmd/runner/Dockerfile | + docker-cli（沙盒拉起） |
| ghcr.io/v3teran/liusha-proxy | cmd/proxy/Dockerfile | 纯 MITM |
| ghcr.io/v3teran/liusha-nginx | deployments/nginx/Dockerfile | 多阶段：node 构建前端 + nginx 托管 |
| liusha/single | deployments/single/Dockerfile | 全家桶（pg+redis+3进程+nginx），仅本地构建 |
| ghcr.io/v3teran/liusha-sandbox | deployments/tool-images/sandbox/ | 沙盒：CI（build-sandbox）在沙盒源码变更时构建（标签为提交 sha 与 `:main`），发版把与该版本源码一致的镜像晋升为 `X.Y.Z` / `X.Y` / `latest`，与业务镜像同版本；本地 `make build-sandbox` |

直接拉取的上游镜像（非本仓构建）：`pgvector/pgvector:pg17`（postgres）、
`redis:8`——由 compose.yaml 引用，不在本地构建清单内。
