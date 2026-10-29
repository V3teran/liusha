#（无 syntax= 指令：仅用基础语法，内置 BuildKit 前端足够，构建免连 Docker Hub）
# liusha 单镜像（比赛/受限环境形态）：一个容器内跑全家——
# PostgreSQL(pgvector) + Redis + proxy + runner + api + nginx(前端)。
# 入口 entrypoint.sh 做进程托管：initdb → migrate → 依次拉起，任一子进程退出即整体关停
# （单容器语义：进程共生共死，由编排器重启，不做容器内自愈）。
#
# 依赖（运行时，挂载进容器）：
#   - /var/run/docker.sock：runner 经宿主 docker 拉起沙盒容器（pentools 镜像须在宿主存在）
#   - 沙盒回连：host.docker.internal:9090（launcher 已内置 DinD 兼容），容器须 -p 发布 9090
#
# 构建：make single-build（目标机 amd64 时用 docker buildx --platform linux/amd64）。
# 数据持久化：VOLUME /data（PG/Redis/证书/密钥/日志都在其下）。

# ── Stage 0a：Go 三进程（vendor 构建，免联网）──
FROM golang:1.25-alpine AS gobuild
WORKDIR /src
COPY go.mod go.sum ./
COPY vendor ./vendor
COPY . .
ENV CGO_ENABLED=0
RUN go build -mod=vendor -o /out/api ./cmd/api \
 && go build -mod=vendor -o /out/runner ./cmd/runner \
 && go build -mod=vendor -o /out/proxy ./cmd/proxy

# migrate CLI 独立构建层：与 Makefile / migrate 镜像同版本同 tag（构建期联网，
# 不在 vendor 内）——单独分层，改它不触发上面三个二进制重编。
FROM golang:1.25-alpine AS migratebuild
RUN go install -mod=mod -tags "postgres" github.com/golang-migrate/migrate/v4/cmd/migrate@v4.19.1

# ── Stage 0b：前端 dist（与 nginx 镜像同源逻辑）──
FROM node:22-alpine AS webbuild
RUN corepack enable
# CI=true：无 TTY 环境下 pnpm 依赖状态检查会交互确认失败，显式声明非交互。
ENV CI=true
WORKDIR /web
COPY web/package.json web/pnpm-lock.yaml web/pnpm-workspace.yaml web/.npmrc ./
RUN pnpm install --frozen-lockfile
COPY web/ ./
RUN pnpm build

# ── Stage 1：运行时基座 = pgvector PG17（自带 vector 扩展）+ redis + nginx ──
FROM pgvector/pgvector:pg17
RUN apt-get update \
 && apt-get install -y --no-install-recommends redis-server nginx ca-certificates \
 && rm -rf /var/lib/apt/lists/*
# docker CLI（静态拷自官方 cli 镜像，免加第三方 apt 源）：runner 经宿主 docker.sock
# 拉沙盒容器需要本机有 CLI；sock 由运行时挂载。
COPY --from=docker:27-cli /usr/local/bin/docker /usr/bin/docker

WORKDIR /app
COPY --from=gobuild /out/api /out/runner /out/proxy /app/
COPY --from=migratebuild /go/bin/migrate /app/migrate
COPY config /app/config
COPY db/migrations /app/migrations
# api 启动期种子（agents/skills）与工具清单（tools.yaml）——漏拷则配置表空、cli_tools 候选空
COPY agents /app/agents
COPY skills /app/skills
COPY deployments/tool-images/pentools/tools.yaml /app/deployments/tool-images/pentools/tools.yaml
COPY --from=webbuild /web/dist /usr/share/nginx/html
COPY deployments/single/nginx.conf /etc/nginx/liusha-single.conf
COPY deployments/single/entrypoint.sh /app/entrypoint.sh
RUN chmod +x /app/entrypoint.sh /app/api /app/runner /app/proxy /app/migrate

VOLUME ["/data"]
EXPOSE 80 8888 9090
ENTRYPOINT ["/app/entrypoint.sh"]
