.PHONY: up down dev-up dev-down logs migrate migrate-down prod-build prod-up prod-down publish single-build single-run run-api run-runner run-web build-api build-nginx build-proxy build-runner build-sandbox web-install web-build web-test test test-unit test-integration lint fmt tidy vet e2e e2e-active e2e-full

COMPOSE = docker compose -f compose.yaml
PROD_COMPOSE = docker compose -f compose.yaml -f compose.prod.yaml --profile full

# 业务镜像仓库/标签（compose.yaml 同默认值——本地 build 即同名 tag，pull 不撞远端）
IMAGE_REPO ?= ghcr.io/v3teran
IMAGE_TAG ?= latest
# 默认对齐 compose 的 dev 宿主端口约定（POSTGRES_HOST_PORT:-55432）；prod 形态迁移在容器内跑，不经 host make
MIGRATE_DSN ?= postgres://liusha:liusha@localhost:55432/liusha?sslmode=disable
# migrate 走 go run 拉取 golang-migrate；默认全局 GOPROXY（goproxy.io）偶发 EOF，
# 这里用可覆盖的镜像 fallback 链兜底：官方 → 国内镜像 → direct。可 `make migrate MIGRATE_GOPROXY=...` 覆盖。
MIGRATE_GOPROXY ?= https://goproxy.cn,direct
MIGRATE = GOPROXY='$(MIGRATE_GOPROXY)' go run -tags 'postgres' github.com/golang-migrate/migrate/v4/cmd/migrate@v4.19.1

# ── 形态 A：本地开发（pg+redis 容器 + 业务进程 host 二进制）────────────
# up/down 是 dev 流程的别名（历史肌肉记忆兼容）；完整流程见 scripts/dev/up.sh。
up: dev-up
down: dev-down

dev-up:
	bash scripts/dev/up.sh

dev-down:
	bash scripts/dev/down.sh

# ── 形态 B：生产全容器（6 核心服务 + prod overlay；schema 由 host 侧 make migrate 建）───
prod-build: build-api build-proxy build-runner build-nginx

# 发布：构建并推送四个业务镜像到仓库（需先 docker login ghcr.io）。
# 用户侧 docker compose pull 拉到的即这些镜像；改代码后需 publish 才会更新。
# 多架构发布：双平台 manifest list（amd64+arm64），用户 pull 自动取本机架构。
# Go/JS 构建阶段跑构建机原生架构（Dockerfile 内 $BUILDPLATFORM 交叉编译），无 QEMU。
# 前置：docker login ghcr.io。多平台必须用 container-driver builder（docker driver
# 不支持，除非开 containerd 镜像存储）——PUBLISH_BUILDER 缺省自建专用实例。
# 多平台产物只能推 registry（无法 load 进本地 docker）；本地自用栈走 prod-build（单架构）。
PUBLISH_BUILDER ?= liusha-multiarch
# OCI source label：GHCR 据此把包关联到仓库（Packages 页可见、CI 首推自动带，
# 手动 publish 不带——业务镜像此前因此游离在仓库 Packages 列表之外）。
IMAGE_SOURCE ?= https://github.com/V3teran/liusha
# IMAGE_TAGS 空格分隔多标签（如 `make publish IMAGE_TAGS="latest 0.1.0"`）：
# 一次构建、版本与 latest 同推——发布惯例是版本 tag 与滚动 tag 并行。
IMAGE_TAGS ?= latest
publish:
	docker buildx inspect $(PUBLISH_BUILDER) >/dev/null 2>&1 || \
	  docker buildx create --name $(PUBLISH_BUILDER)
	docker buildx build --builder $(PUBLISH_BUILDER) --platform linux/amd64,linux/arm64 --push \
	  --label org.opencontainers.image.source=$(IMAGE_SOURCE) \
	  $(foreach t,$(IMAGE_TAGS),-t $(IMAGE_REPO)/liusha-api:$(t)) \
	  -f cmd/api/Dockerfile .
	docker buildx build --builder $(PUBLISH_BUILDER) --platform linux/amd64,linux/arm64 --push \
	  --label org.opencontainers.image.source=$(IMAGE_SOURCE) \
	  $(foreach t,$(IMAGE_TAGS),-t $(IMAGE_REPO)/liusha-proxy:$(t)) \
	  -f cmd/proxy/Dockerfile .
	docker buildx build --builder $(PUBLISH_BUILDER) --platform linux/amd64,linux/arm64 --push \
	  --label org.opencontainers.image.source=$(IMAGE_SOURCE) \
	  $(foreach t,$(IMAGE_TAGS),-t $(IMAGE_REPO)/liusha-runner:$(t)) \
	  -f cmd/runner/Dockerfile .
	docker buildx build --builder $(PUBLISH_BUILDER) --platform linux/amd64,linux/arm64 --push \
	  --label org.opencontainers.image.source=$(IMAGE_SOURCE) \
	  $(foreach t,$(IMAGE_TAGS),-t $(IMAGE_REPO)/liusha-nginx:$(t)) \
	  -f deployments/nginx/Dockerfile .

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
	$(MIGRATE) -path db/migrations -database '$(MIGRATE_DSN)' up

migrate-down:
	$(MIGRATE) -path db/migrations -database '$(MIGRATE_DSN)' down 1

run-api:
	go run ./cmd/api

# A1 部署方案：runner 跑在 host（开发 + production 当前形态），
# 用 host 的 docker daemon 起 sandbox 容器。需 host 上有 PG/Redis（`make up`）+ sandbox 镜像（`make build-sandbox`）。
run-runner:
	go run ./cmd/runner

# 前端（web/）：源码与后端同仓。dev 用 vite（/api 代理到 Go）；prod 由 nginx 镜像多阶段构建托管。
run-web:
	cd web && pnpm dev

web-install:
	cd web && pnpm install --frozen-lockfile

web-build:
	cd web && pnpm build

web-test:
	cd web && pnpm test

build-api:
	docker build -f cmd/api/Dockerfile -t $(IMAGE_REPO)/liusha-api:$(IMAGE_TAG) .

# 前置 nginx 镜像：多阶段 build（node 编前端 dist + nginx 托管 + 反代 api）。
build-nginx:
	docker build -f deployments/nginx/Dockerfile -t $(IMAGE_REPO)/liusha-nginx:$(IMAGE_TAG) .

build-proxy:
	docker build -f cmd/proxy/Dockerfile -t $(IMAGE_REPO)/liusha-proxy:$(IMAGE_TAG) .

build-runner:
	docker build -f cmd/runner/Dockerfile -t $(IMAGE_REPO)/liusha-runner:$(IMAGE_TAG) .

# sandbox final 镜像（sandbox-server PID 1 + browser-use + 工具清单元数据）。
# 分层构建：base（Kali + 90 工具安装）由 CI 双架构构建推送 ghcr.io/v3teran/liusha-sandbox-base，
# 本地按宿主原生架构构建 final（ARG BASE_IMAGE 默认取 base:latest 双架构 tag）。
# 改 Dockerfile.base/tools.yaml 走 CI。
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

# 种子强制重导：agents/*.md 覆盖写入 DB（术语/提示词升级后同步已初始化的库用）
reseed:
	LIUSHA_POSTGRES_DSN=$$LIUSHA_POSTGRES_DSN LIUSHA_REDIS_PASSWORD=$${REDIS_PASSWORD:-$$LIUSHA_REDIS_PASSWORD} go run ./cmd/reseed -dir $(or $(SEED_DIR),.)

fmt:
	gofmt -w .

tidy:
	go mod tidy

# e2e 触发器（passive profile 已随认知循环重构移除，cmd/e2e 仅支持 active:<name>）：
#   make e2e-active       # active:xss（自然语言 brief 喂 agent LLM）
#   make e2e-full         # active:full（全类型漏洞挖掘，验收 1 目标/10 动作/5 结果）
#   ./scripts/dev/e2e.sh active:full   # 全套（清库→重启→触发→轮询）
e2e: e2e-active

e2e-active:
	go run ./cmd/e2e active:xss

e2e-full:
	go run ./cmd/e2e active:full
