#（无 syntax= 指令：仅用基础语法，内置 BuildKit 前端足够，构建免连 Docker Hub）
# liusha 镜像：一个程序，compose 以不同命令运行各组件（api / proxy / runner / migrate）。
# 多架构（amd64+arm64）：编译阶段钉在构建机原生架构（$BUILDPLATFORM），经 GOOS/GOARCH
# 交叉编译出目标架构的纯静态二进制，零 QEMU；运行时层按目标架构拉官方多架构镜像。
FROM --platform=$BUILDPLATFORM golang:1.25-alpine AS build
ARG TARGETOS TARGETARCH
ARG VERSION=dev
WORKDIR /src
COPY go.mod go.sum ./
COPY vendor ./vendor
COPY . .
ENV CGO_ENABLED=0
RUN GOOS=$TARGETOS GOARCH=$TARGETARCH go build -mod=vendor -trimpath \
      -ldflags "-s -w -X main.version=${VERSION}" -o /out/liusha ./cmd/liusha

FROM alpine:3.20
# wget：compose 健康检查探 healthz
RUN apk add --no-cache ca-certificates wget \
 && addgroup -S liusha && adduser -S liusha -G liusha -h /home/liusha \
 && mkdir -p /app/logs && chown -R liusha:liusha /app
COPY --from=build /out/liusha /usr/local/bin/liusha
WORKDIR /app
USER liusha
ENTRYPOINT ["liusha"]
CMD ["serve"]
