.PHONY: dev-up dev-down logs migrate up down publish single-build single-run build image run-api run-runner run-web build-sandbox web-install web-build web-test test test-unit test-integration lint fmt tidy vet e2e e2e-xss e2e-full

COMPOSE = docker compose

# make image / publish 构建的镜像仓库与标签（命令行参数；部署时 compose 按 .env 的
# LIUSHA_IMAGE_REPO / LIUSHA_IMAGE_TAG 选用镜像）
IMAGE_REPO ?= ghcr.io/v3teran
IMAGE_TAG ?= latest
# 版本号与发布物同一写法：不带 v（标签 v0.10.0 → 0.10.0；标签之后的提交带 -N-g<提交>）
VERSION ?= $(patsubst v%,%,$(shell git describe --tags --always --dirty 2>/dev/null || echo dev))

# ── 本地开发：依赖（pg / redis / 沙盒）跑在容器里，组件在宿主上直接运行 ──
# 完整流程见 scripts/dev/up.sh 与 scripts/dev/run-svc.sh。宿主进程与容器读同一份配置文件
# ${LIUSHA_DATA_DIR}/config.yaml（缺省 ~/data/liusha/config.yaml），连接串由 scripts/dev/env.sh 按 .env 拼出。
dev-up:
	bash scripts/dev/up.sh

dev-down:
	bash scripts/dev/down.sh

# ── 多镜像部署：全部服务；数据库迁移由一次性的 migrate 服务随 up 执行 ──
up:
	$(COMPOSE) up -d --build

down:
	$(COMPOSE) down

# ── 二进制：本机构建 liusha（先构建前端，再带 webdist 标签编入；版本号取自 git describe）──
build: web-build
	go build -trimpath -tags webdist -ldflags "-X main.version=$(VERSION)" -o liusha ./cmd/liusha

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

# ── 单镜像：以沙盒镜像为底，一个容器运行全部 ──
# 目标机架构不同时加 --platform（如 linux/amd64）。
single-build: build-sandbox
	docker build --build-arg VERSION=$(VERSION) -t liusha-single:local -f deployments/single/Dockerfile .

# .env 与多镜像同一份（仓库根）：控制台 / 代理的宿主端口与对外监听段都取自其中，与多镜像同名同义。
# 回连地址等其余配置在 ~/data/liusha/config.yaml。
single-run:
	@test -f .env || { echo "缺少 .env：cp .env.example .env 并填写"; exit 1; }
	env_val() { sed -n "s/^$$1=//p" .env; }; \
	ports=$$(env_val LIUSHA_SANDBOX_LISTENER_PORTS); \
	test -n "$$ports" || { echo ".env 缺少 LIUSHA_SANDBOX_LISTENER_PORTS"; exit 1; }; \
	console=$$(env_val LIUSHA_CONSOLE_HOST_PORT); proxy=$$(env_val LIUSHA_PROXY_HOST_PORT); \
	docker run -d --restart unless-stopped --stop-timeout 60 --name liusha-single --env-file .env \
	  -p $${console:-50080}:8580 -p $${proxy:-58588}:8588 -p $$ports:$$ports \
	  -v $(HOME)/data/liusha/single:/data \
	  --mount type=bind,src=$(HOME)/data/liusha/config.yaml,dst=/data/config.yaml,readonly \
	  liusha-single:local

logs:
	$(COMPOSE) logs -f --tail=100

# 对本地开发库执行迁移（连接串由 scripts/dev/env.sh 按 .env 拼出）
migrate:
	bash -c '. scripts/dev/env.sh && go run ./cmd/liusha migrate'

run-api:
	bash -c '. scripts/dev/env.sh && go run ./cmd/liusha api'

run-runner:
	bash -c '. scripts/dev/env.sh && go run ./cmd/liusha runner'

# 前端（web/）：源码与后端同仓。开发用 vite（/api 代理到 api）；发布时编入 liusha（webdist 标签）。
run-web:
	cd web && pnpm dev

web-install:
	cd web && pnpm install --frozen-lockfile

web-build:
	cd web && pnpm build

web-test:
	cd web && pnpm test

# 沙盒镜像：Kali + 工具清单中的工具 + liusha sandbox。首次构建较久，工具层之后命中缓存。
build-sandbox:
	docker build --build-arg VERSION=$(VERSION) -t liusha-sandbox:local -f deployments/sandbox/Dockerfile .

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
	bash -c '. scripts/dev/env.sh && go run ./cmd/e2e xss'

e2e-full:
	bash -c '. scripts/dev/env.sh && go run ./cmd/e2e full'
