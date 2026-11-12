#（无 syntax= 指令：仅用基础语法，内置 BuildKit 前端足够）
# liusha 单镜像：一个容器运行全部——PostgreSQL 18（pgvector）+ Redis + liusha serve（含沙盒）。
# 以沙盒镜像为底（工具都在其中），先构建沙盒镜像：
#
#   docker build -t liusha-sandbox:local -f deployments/sandbox/Dockerfile .
#   docker build -t liusha-single:local -f deployments/single/Dockerfile .
#
# 沙盒镜像里的 liusha 只跑 sandbox 子命令、不编入前端；这里另行构建编入前端的 liusha 覆盖它。
# 进程托管见 entrypoint.sh；数据（库、队列、工作区、日志、证书、密钥）都在 /data。
ARG SANDBOX_IMAGE=liusha-sandbox:local

# ── 前端：web/ → web/dist ──
FROM --platform=$BUILDPLATFORM node:22-alpine AS web
RUN corepack enable
# CI=true：无 TTY 时 pnpm 不再尝试交互确认。
ENV CI=true
WORKDIR /web
COPY web/package.json web/pnpm-lock.yaml web/pnpm-workspace.yaml web/.npmrc ./
RUN pnpm install --frozen-lockfile
COPY web/ ./
RUN pnpm build
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

FROM ${SANDBOX_IMAGE}

# PostgreSQL 主版本显式钉住：数据目录跨主版本不兼容，发行版升级主版本时宁可构建失败也不
# 静默换版本。
RUN apt-get update \
 && apt-get install -y --no-install-recommends postgresql-18 postgresql-18-pgvector redis-server tini \
 && rm -rf /var/lib/apt/lists/*

COPY --from=build /out/liusha /usr/local/bin/liusha
COPY LICENSE NOTICE /usr/share/doc/liusha/
COPY --from=build /out/THIRD_PARTY_NOTICES /usr/share/doc/liusha/
COPY deployments/single/entrypoint.sh /usr/local/bin/liusha-single
RUN chmod +x /usr/local/bin/liusha-single

# 沙盒只供同容器的 runner 调用：收回到回环（沙盒镜像默认监听全部网卡）。
ENV LIUSHA_SANDBOX_LISTEN_ADDR=127.0.0.1:8591 \
    LIUSHA_ENV=production

VOLUME ["/data"]
# 8580 控制台与 API；8588 MITM 代理；对外监听段（缺省 21000-21099）按需 -p 发布
EXPOSE 8580 8588
ENTRYPOINT ["/usr/bin/tini", "--", "/usr/local/bin/liusha-single"]
CMD []
