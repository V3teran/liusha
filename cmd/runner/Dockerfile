#（无 syntax= 指令：仅用基础语法，内置 BuildKit 前端足够，构建免连 Docker Hub）
# liusha runner 镜像：go vendor 构建 + alpine runtime + 8590 healthz。
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
RUN GOOS=$TARGETOS GOARCH=$TARGETARCH go build -mod=vendor -o /out/runner ./cmd/runner

FROM alpine:3.20
RUN apk add --no-cache ca-certificates wget docker-cli
WORKDIR /app
COPY --from=build /out/runner /app/runner
COPY config /app/config
# skill 运行时事实源 = DB（经三级缓存），不再挂载 skills/ 目录——种子由 api 进程负责。
COPY deployments/tool-images/sandbox/tools.yaml /app/deployments/tool-images/sandbox/tools.yaml
EXPOSE 8590
ENTRYPOINT ["/app/runner"]
