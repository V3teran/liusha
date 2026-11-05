#（无 syntax= 指令：仅用基础语法，内置 BuildKit 前端足够，构建免连 Docker Hub）
# liusha api 镜像：go vendor 构建 + alpine runtime。
# 附带 migrations + migrate CLI（与单镜像形态同款）：compose 的 migrate 一次性
# 服务用本镜像跑 `migrate up`，用户部署无需本地 Go 工具链。
FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
COPY vendor ./vendor
COPY . .
ENV CGO_ENABLED=0
RUN go build -mod=vendor -o /out/api ./cmd/api

# migrate CLI：与 Makefile / 单镜像形态同版本同 tag（构建期联网装一次）
FROM golang:1.25-alpine AS migratebuild
RUN go install -mod=mod -tags "postgres" github.com/golang-migrate/migrate/v4/cmd/migrate@v4.19.1

FROM alpine:3.20
RUN apk add --no-cache ca-certificates wget
WORKDIR /app
COPY --from=build /out/api /app/api
COPY --from=migratebuild /go/bin/migrate /app/migrate
COPY db/migrations /app/migrations
COPY config /app/config
# tools.yaml：供 GET /tooling/tools 给 AgentAdmin cli_tools 白名单候选。cmd/api 默认从
# ./deployments/tool-images/sandbox/tools.yaml 读，漏拷则该端点不注册、配置页候选为空。
COPY deployments/tool-images/sandbox/tools.yaml /app/deployments/tool-images/sandbox/tools.yaml
# 种子目录（agents/*.md + skills/**/SKILL.md）：api 启动期 seed.Import 的数据源——
# DB 空库首填 agent/skill 配置。漏拷则种子静默跳过，配置表为空、runner 装配出空 agent。
COPY agents /app/agents
COPY skills /app/skills
# non-root 运行（密码文件无 shell 登录；app 目录属主移交）
RUN addgroup -S app && adduser -S app -G app -h /app
RUN chown -R app:app /app
USER app
EXPOSE 4080
ENTRYPOINT ["/app/api"]
