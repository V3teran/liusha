#（无 syntax= 指令：仅用基础语法，内置 BuildKit 前端足够，构建免连 Docker Hub）
# liusha api 镜像：go vendor 构建 + alpine runtime。
FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
COPY vendor ./vendor
COPY . .
ENV CGO_ENABLED=0
RUN go build -mod=vendor -o /out/api ./cmd/api

FROM alpine:3.20
RUN apk add --no-cache ca-certificates wget
WORKDIR /app
COPY --from=build /out/api /app/api
COPY config /app/config
# tools.yaml：供 GET /tooling/tools 给 AgentAdmin cli_tools 白名单候选。cmd/api 默认从
# ./deployments/tool-images/pentools/tools.yaml 读，漏拷则该端点不注册、配置页候选为空。
COPY deployments/tool-images/pentools/tools.yaml /app/deployments/tool-images/pentools/tools.yaml
# 种子目录（agents/*.md + skills/**/SKILL.md）：api 启动期 seed.Import 的数据源——
# DB 空库首填 agent/skill 配置。漏拷则种子静默跳过，配置表为空、runner 装配出空 agent。
COPY agents /app/agents
COPY skills /app/skills
EXPOSE 8080
ENTRYPOINT ["/app/api"]
