#（无 syntax= 指令：仅用基础语法，内置 BuildKit 前端足够，构建免连 Docker Hub）
# liusha migrate 镜像：golang-migrate CLI + db/migrations 全量脚本。
# 一次性任务（compose profile=migrate），跑完即退——全容器生产形态的迁移通道，
# 与 host 侧 `make migrate` 同一 CLI 同一版本（MIGRATE_VERSION 与 Makefile 对齐）。
FROM golang:1.25-alpine AS build
ARG MIGRATE_VERSION=v4.19.1
# migrate CLI 不在 vendor 内（host 侧走 go run），此处构建期联网安装。
# -tags postgres：CLI 默认不含数据库驱动（与 Makefile 的 go run -tags 同口径）
RUN go install -mod=mod -tags "postgres" github.com/golang-migrate/migrate/v4/cmd/migrate@${MIGRATE_VERSION}

FROM alpine:3.20
RUN apk add --no-cache ca-certificates
COPY --from=build /go/bin/migrate /usr/local/bin/migrate
# build context = 仓库根（compose 里 context: ..），迁移脚本随镜像走，
# 容器不依赖仓库检出——生产机无需源码即可迁移。
COPY db/migrations /migrations
ENV MIGRATE_DSN=postgres://liusha:liusha@postgres:5432/liusha?sslmode=disable
ENTRYPOINT ["/bin/sh", "-c", "exec migrate -path /migrations -database \"$MIGRATE_DSN\" up"]
