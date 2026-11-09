#（无 syntax= 指令：仅用基础语法，内置 BuildKit 前端足够，构建免连 Docker Hub）
# liusha api 镜像：go vendor 构建 + alpine runtime。
# 附带 migrations + migrate CLI（与单镜像形态同款）：compose 的 migrate 一次性
# 服务用本镜像跑 `migrate up`，用户部署无需本地 Go 工具链。
# 多架构（amd64+arm64）：编译阶段钉在构建机原生架构（$BUILDPLATFORM），经
# GOOS/GOARCH 交叉编译产出目标架构纯静态二进制——Go 交叉编译零 QEMU、原生速度；
# 运行时层按目标架构拉官方多架构镜像。
FROM --platform=$BUILDPLATFORM golang:1.25-alpine AS build
ARG TARGETOS TARGETARCH
WORKDIR /src
COPY go.mod go.sum ./
COPY vendor ./vendor
COPY . .
ENV CGO_ENABLED=0
RUN GOOS=$TARGETOS GOARCH=$TARGETARCH go build -mod=vendor -o /out/api ./cmd/api

# migrate CLI：与 Makefile / 单镜像形态同版本同 tag（构建期联网装一次）。
# 跨平台安装的落盘位置在 Go 里有两种布局（$GOBIN 或 $GOPATH/bin/$GOOS_$GOARCH），
# 显式归拢到 /out，COPY 不猜路径。
FROM --platform=$BUILDPLATFORM golang:1.25-alpine AS migratebuild
ARG TARGETOS TARGETARCH
RUN mkdir -p /out \
 && CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go install -mod=mod -tags "postgres" github.com/golang-migrate/migrate/v4/cmd/migrate@v4.19.1 \
 && { cp "/go/bin/$TARGETOS"_"$TARGETARCH/migrate" /out/migrate 2>/dev/null || cp /go/bin/migrate /out/migrate; }

FROM alpine:3.20
RUN apk add --no-cache ca-certificates wget
WORKDIR /app
COPY --from=build /out/api /app/api
COPY --from=migratebuild /out/migrate /app/migrate
COPY db/migrations /app/migrations
COPY config /app/config
# tools.yaml：供 GET /tooling/tools 给 AgentAdmin cli_tools 白名单候选。cmd/api 默认从
# ./deployments/tool-images/sandbox/tools.yaml 读，漏拷则该端点不注册、配置页候选为空。
COPY deployments/tool-images/sandbox/tools.yaml /app/deployments/tool-images/sandbox/tools.yaml
# non-root 运行（密码文件无 shell 登录；app 目录属主移交）
RUN addgroup -S app && adduser -S app -G app -h /app
RUN chown -R app:app /app
USER app
EXPOSE 8580
ENTRYPOINT ["/app/api"]
