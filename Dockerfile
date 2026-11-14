#（无 syntax= 指令：仅用基础语法，内置 BuildKit 前端足够，构建免连 Docker Hub）
# liusha 镜像：一个程序（控制台前端已编入），compose 以不同命令运行各组件
# （api / proxy / runner / migrate）。许可证与第三方声明在 /usr/share/doc/liusha/。
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
# 前端生产依赖的第三方声明（与 Go 部分在下一阶段拼接）
COPY scripts/release/web-notices.mjs /tmp/web-notices.mjs
RUN node /tmp/web-notices.mjs /web > /web/THIRD_PARTY_WEB

# ── liusha：带 webdist 标签编入前端 ──
FROM --platform=$BUILDPLATFORM golang:1.25-alpine AS build
ARG TARGETOS TARGETARCH
ARG VERSION=dev
WORKDIR /src
COPY go.mod go.sum ./
COPY vendor ./vendor
COPY . .
COPY --from=web /web/dist ./web/dist
COPY --from=web /web/THIRD_PARTY_WEB /tmp/THIRD_PARTY_WEB
ENV CGO_ENABLED=0
RUN GOOS=$TARGETOS GOARCH=$TARGETARCH go build -mod=vendor -trimpath -tags webdist \
      -ldflags "-s -w -X main.version=${VERSION}" -o /out/liusha ./cmd/liusha \
 && go run -mod=vendor ./scripts/release/notices -binary /out/liusha -web /tmp/THIRD_PARTY_WEB > /out/THIRD_PARTY_NOTICES

FROM alpine:3.20
# wget：compose 健康检查探 healthz
RUN apk add --no-cache ca-certificates wget
COPY --from=build /out/liusha /usr/local/bin/liusha
COPY LICENSE NOTICE /usr/share/doc/liusha/
COPY --from=build /out/THIRD_PARTY_NOTICES /usr/share/doc/liusha/
# 以 root 运行：api 与必须以 root 运行的沙盒共享任务工作区（双方都要读写对方建的目录），
# 日志、证书目录是宿主上由 docker 以 root 创建的挂载点。隔离边界是容器本身。
WORKDIR /app
LABEL org.opencontainers.image.source="https://github.com/V3teran/liusha" \
      org.opencontainers.image.licenses="Apache-2.0"
ENTRYPOINT ["liusha"]
CMD ["serve"]
