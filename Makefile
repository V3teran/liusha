.PHONY: up down logs migrate migrate-down run-api run-runner run-web build-api build-nginx build-proxy build-runner build-vulnapp build-pentools web-install web-build web-test test test-unit test-integration lint fmt tidy vet e2e e2e-bac e2e-sqli e2e-active

COMPOSE = docker compose -f deployments/docker-compose.yml
MIGRATE_DSN ?= postgres://liusha:liusha@localhost:5432/liusha?sslmode=disable
# migrate 走 go run 拉取 golang-migrate；默认全局 GOPROXY（goproxy.io）偶发 EOF，
# 这里用可覆盖的镜像 fallback 链兜底：官方 → 国内镜像 → direct。可 `make migrate MIGRATE_GOPROXY=...` 覆盖。
MIGRATE_GOPROXY ?= https://proxy.golang.org,https://goproxy.cn,direct
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

# sandbox 镜像（all-in-one：sandbox-server PID 1 + browser-use + pentest 工具集）。
# multi-stage build：stage 0 编译 sandbox-server Go 二进制；stage 1 装 chrome/python/工具/拷贝二进制。
# 体积 ~1.5GB；首次 build ~10-20 分钟（拉 ubuntu/golang base + apt + pip + wget releases）。
build-pentools:
	docker build -t ghcr.io/v3teran/liusha-pentools:latest -t liusha/pentools:latest -f deployments/tool-images/pentools/Dockerfile .

test: test-unit test-integration

test-unit:
	go test -race -short ./...

test-integration:
	go test -race -tags=integration ./...

lint:
	go vet ./...
	gofmt -l . | tee /dev/stderr | (! read)

vet:
	go vet ./...

fmt:
	gofmt -w .

tidy:
	go mod tidy

# e2e 通用触发器（args 用前缀区分 passive/active 两种模式）：
#   make e2e              # 不带参 = 跑全部 passive profile
#   make e2e-bac          # 仅 passive bac
#   make e2e-sqli         # 仅 passive sqli
#   make e2e-active       # 仅 active:xss（自然语言 brief 喂 hunter LLM）
#   go run ./cmd/e2e bac sqli active:xss   # 混合（直接调 binary）
e2e:
	go run ./cmd/e2e

e2e-bac:
	go run ./cmd/e2e bac

e2e-sqli:
	go run ./cmd/e2e sqli

e2e-active:
	go run ./cmd/e2e active:xss
