.PHONY: up down logs migrate migrate-down run-api run-runner run-web build-api build-nginx build-proxy build-runner build-vulnapp build-pentools web-install web-build web-test test test-unit test-integration lint fmt tidy vet e2e e2e-active e2e-full

COMPOSE = docker compose -f deployments/docker-compose.yml
MIGRATE_DSN ?= postgres://liusha:liusha@localhost:5432/liusha?sslmode=disable
# migrate 走 go run 拉取 golang-migrate；默认全局 GOPROXY（goproxy.io）偶发 EOF，
# 这里用可覆盖的镜像 fallback 链兜底：官方 → 国内镜像 → direct。可 `make migrate MIGRATE_GOPROXY=...` 覆盖。
MIGRATE_GOPROXY ?= https://goproxy.cn,direct
MIGRATE = GOPROXY='$(MIGRATE_GOPROXY)' go run -tags 'postgres' github.com/golang-migrate/migrate/v4/cmd/migrate@v4.19.1

up:
	$(COMPOSE) up -d

down:
	$(COMPOSE) down

logs:
	$(COMPOSE) logs -f --tail=100

migrate:
	$(MIGRATE) -path db/migrations -database '$(MIGRATE_DSN)' up

migrate-down:
	$(MIGRATE) -path db/migrations -database '$(MIGRATE_DSN)' down 1

run-api:
	go run ./cmd/api

# A1 部署方案：runner 跑在 host（开发 + production 当前形态），
# 用 host 的 docker daemon 起 sandbox 容器。需 host 上有 PG/Redis（`make up`）+ pentools 镜像（`make build-pentools`）。
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
	docker build -f cmd/api/Dockerfile -t liusha/api .

# 前置 nginx 镜像：多阶段 build（node 编前端 dist + nginx 托管 + 反代 api）。
build-nginx:
	docker build -f deployments/nginx/Dockerfile -t liusha/nginx .

build-proxy:
	docker build -f cmd/proxy/Dockerfile -t liusha/proxy .

build-runner:
	docker build -f cmd/runner/Dockerfile -t liusha/runner .

build-vulnapp:
	docker build -f cmd/vulnapp/Dockerfile -t liusha/vulnapp .

# sandbox final 镜像（sandbox-server PID 1 + browser-use + 工具清单元数据）。
# 分层构建：base（Kali + 90 工具安装）由 CI 构建推送 ghcr.io/v3teran/pentools-base，
# 本地只构建 final（ARG BASE_IMAGE 默认取 base:latest）。改 Dockerfile.base/tools.yaml 走 CI。
build-pentools:
	docker build --platform linux/amd64 -t ghcr.io/v3teran/liusha-pentools:latest -t liusha/pentools:latest \
		-f deployments/tool-images/pentools/Dockerfile.final .

test: test-unit test-integration

test-unit:
	go test -race -short ./...

test-integration:
	go test -race -tags=integration ./...

lint: lint-terminology
	go vet ./...
	gofmt -l . | grep -v vendor | tee /dev/stderr | (! read)

# 术语契约门禁（docs/glossary.md 的机器可执行形态）：旧架构命名残留即失败
lint-terminology:
	scripts/lint-terminology.sh

vet:
	go vet ./...

# 种子强制重导：agents/*.md 覆盖写入 DB（术语/提示词升级后同步已初始化的库用）
reseed:
	LIUSHA_POSTGRES_DSN=$$LIUSHA_POSTGRES_DSN go run ./cmd/reseed -dir $(or $(SEED_DIR),.)

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
