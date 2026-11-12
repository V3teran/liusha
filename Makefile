.PHONY: dev-up dev-down logs migrate prod-build prod-up prod-down publish single-build single-run build image run-api run-runner run-web build-nginx build-sandbox web-install web-build web-test test test-unit test-integration lint fmt tidy vet e2e e2e-xss e2e-full

COMPOSE = docker compose -f compose.yaml
PROD_COMPOSE = docker compose -f compose.yaml -f compose.prod.yaml --profile full

# 镜像仓库/标签（compose.yaml 同默认值——本地 build 即同名 tag，pull 不撞远端）
IMAGE_REPO ?= ghcr.io/v3teran
IMAGE_TAG ?= latest
# 默认对齐 compose 的 dev 宿主端口约定（POSTGRES_HOST_PORT:-55432）
MIGRATE_DSN ?= postgres://liusha:liusha@localhost:55432/liusha?sslmode=disable
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)

# ── 形态 A：本地开发（pg+redis 容器 + 业务进程 host 二进制）────────────
# 完整流程见 scripts/dev/up.sh。
dev-up:
	bash scripts/dev/up.sh

dev-down:
	bash scripts/dev/down.sh

# ── 形态 B：生产全容器（full profile + prod overlay；迁移由 compose 的 migrate 一次性服务随 up 执行）───
prod-build: image build-nginx

# 本机构建 liusha（版本号取自 git describe）
build:
	go build -trimpath -ldflags "-X main.version=$(VERSION)" -o liusha ./cmd/liusha

image:
	docker build --build-arg VERSION=$(VERSION) -t $(IMAGE_REPO)/liusha:$(IMAGE_TAG) .

# 发布：多架构构建并推送 liusha 镜像（需先 docker login ghcr.io）。
# 多平台必须用 container-driver builder（docker driver 不支持，除非开 containerd 镜像存储）——
# PUBLISH_BUILDER 缺省自建专用实例；多平台产物只能推 registry，本地自用走 image（单架构）。
PUBLISH_BUILDER ?= liusha-multiarch
# OCI source label：GHCR 据此把包关联到仓库
IMAGE_SOURCE ?= https://github.com/V3teran/liusha
# IMAGE_TAGS 空格分隔多标签（如 `make publish IMAGE_TAGS="latest 0.10.0"`）
IMAGE_TAGS ?= latest
publish:
	docker buildx inspect $(PUBLISH_BUILDER) >/dev/null 2>&1 || \
	  docker buildx create --name $(PUBLISH_BUILDER)
	docker buildx build --builder $(PUBLISH_BUILDER) --platform linux/amd64,linux/arm64 --push \
	  --build-arg VERSION=$(VERSION) \
	  --label org.opencontainers.image.source=$(IMAGE_SOURCE) \
	  $(foreach t,$(IMAGE_TAGS),-t $(IMAGE_REPO)/liusha:$(t)) .

prod-up:
	$(PROD_COMPOSE) up -d --build

prod-down:
	$(PROD_COMPOSE) down

# ── 形态 C：单镜像（比赛/受限环境；沙盒依赖宿主 docker.sock + sandbox 镜像）──
single-build:
	docker build -f deployments/single/Dockerfile -t liusha/single .

# 目标机 amd64（比赛机常见）：docker buildx build --platform linux/amd64 -t liusha/single -f deployments/single/Dockerfile . --load
single-run:
	docker run -d --restart unless-stopped --name liusha-single \
	  -p 50080:80 -p 58588:8588 -p 58590:8590 \
	  -v $(HOME)/data/liusha/single:/data \
	  -v /var/run/docker.sock:/var/run/docker.sock \
	  --env-file .env \
	  -e LIUSHA_INGEST_PUBLIC_PORT=58590 \
	  liusha/single

logs:
	$(COMPOSE) logs -f --tail=100

migrate:
	LIUSHA_POSTGRES_DSN='$(MIGRATE_DSN)' go run ./cmd/liusha migrate

run-api:
	go run ./cmd/liusha api

run-runner:
	go run ./cmd/liusha runner

# 前端（web/）：源码与后端同仓。dev 用 vite（/api 代理到 Go）；prod 由 nginx 镜像多阶段构建托管。
run-web:
	cd web && pnpm dev

web-install:
	cd web && pnpm install --frozen-lockfile

web-build:
	cd web && pnpm build

web-test:
	cd web && pnpm test

# 前置 nginx 镜像：多阶段 build（node 编前端 dist + nginx 托管 + 反代 api）。
build-nginx:
	docker build -f deployments/nginx/Dockerfile -t $(IMAGE_REPO)/liusha-nginx:$(IMAGE_TAG) .

# sandbox final 镜像（liusha sandbox + browser-use）。
# 分层构建：base（Kali + 90 工具安装）由 CI 双架构构建推送 ghcr.io/v3teran/liusha-sandbox-base，
# 本地按宿主原生架构构建 final（ARG BASE_IMAGE 默认取 base:latest 双架构 tag）。
# 改 Dockerfile.base / 工具清单走 CI。
build-sandbox:
	docker build -t ghcr.io/v3teran/liusha-sandbox:latest \
		-f deployments/tool-images/sandbox/Dockerfile.final .

test: test-unit test-integration

test-unit:
	go test -race -short ./...

test-integration:
	go test -race -tags=integration ./...

lint: lint-terminology
	go vet ./...
	golangci-lint run ./...
	gofmt -l . | grep -v vendor | tee /dev/stderr | (! read)

# 术语契约门禁（scripts/lint-terminology.sh 的 FORBIDDEN 列表）：旧架构命名残留即失败
lint-terminology:
	scripts/lint-terminology.sh

vet:
	go vet ./...

fmt:
	gofmt -w .

# 依赖 vendor 入库（镜像以 -mod=vendor 构建）：tidy 后必须同步 vendor/，否则构建报 inconsistent vendoring
tidy:
	go mod tidy
	go mod vendor

# e2e 触发器（剧本见 cmd/e2e/profiles.go；需 dev 栈已起）：
#   make e2e-xss          # XSS 专项（自然语言 brief 喂 agent LLM）
#   make e2e-full         # 综合扫描（全类型漏洞挖掘，验收 1 目标/10 动作/3 结果）
#   ./scripts/dev/e2e.sh full   # 全套（清库→重启→触发→轮询）
e2e: e2e-xss

e2e-xss:
	go run ./cmd/e2e xss

e2e-full:
	go run ./cmd/e2e full
