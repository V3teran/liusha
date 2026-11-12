#（无 syntax= 指令：仅用基础语法，内置 BuildKit 前端足够，构建免连 Docker Hub）
# liusha 镜像：一个程序（控制台前端已编入），compose 以不同命令运行各组件
# （api / proxy / runner / migrate）。
# 多架构（amd64+arm64）：构建阶段钉在构建机原生架构（$BUILDPLATFORM）——前端产物与架构
# 无关，Go 经 GOOS/GOARCH 交叉编译出目标架构的纯静态二进制，零 QEMU；运行时层按目标
# 架构拉官方多架构镜像。

# ── 前端：web/ → web/dist ──
FROM --platform=$BUILDPLATFORM node:22-alpine AS web
RUN corepack enable
# CI=true：无 TTY 时 pnpm 不再尝试交互确认（否则 ERR_PNPM_ABORTED_REMOVE_MODULES_DIR_NO_TTY）。
ENV CI=true
WORKDIR /web
COPY web/package.json web/pnpm-lock.yaml web/pnpm-workspace.yaml web/.npmrc ./
RUN pnpm install --frozen-lockfile
COPY web/ ./
RUN pnpm build

# ── liusha：带 webdist 标签编入前端 ──
FROM --platform=$BUILDPLATFORM golang:1.25-alpine AS build
ARG TARGETOS TARGETARCH
ARG VERSION=dev
WORKDIR /src
COPY go.mod go.sum ./
COPY vendor ./vendor
COPY . .
COPY --from=web /web/dist ./web/dist
ENV CGO_ENABLED=0
RUN GOOS=$TARGETOS GOARCH=$TARGETARCH go build -mod=vendor -trimpath -tags webdist \
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
