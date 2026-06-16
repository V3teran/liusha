.PHONY: up down logs migrate migrate-down run-api run-scanner build-api build-proxy build-scanner build-vulnapp build-pentools test test-unit test-integration lint fmt tidy vet e2e e2e-bac e2e-sqli e2e-active

COMPOSE = docker compose -f deployments/docker-compose.yml
MIGRATE_DSN ?= postgres://liusha:liusha@localhost:5432/liusha?sslmode=disable

up:
	$(COMPOSE) up -d

down:
	$(COMPOSE) down

logs:
	$(COMPOSE) logs -f --tail=100

migrate:
	go run -mod=mod -tags 'postgres' github.com/golang-migrate/migrate/v4/cmd/migrate \
		-path db/migrations -database '$(MIGRATE_DSN)' up

migrate-down:
	go run -mod=mod -tags 'postgres' github.com/golang-migrate/migrate/v4/cmd/migrate \
		-path db/migrations -database '$(MIGRATE_DSN)' down 1

run-api:
	go run ./cmd/api

# A1 部署方案：scanner 跑在 host（开发 + production 当前形态），
# 用 host 的 docker daemon 起 sandbox 容器。需 host 上有 PG/Redis（`make up`）+ pentools 镜像（`make build-pentools`）。
run-scanner:
	go run ./cmd/scanner

build-api:
	docker build -f cmd/api/Dockerfile -t liusha/api .

build-proxy:
	docker build -f cmd/proxy/Dockerfile -t liusha/proxy .

build-scanner:
	docker build -f cmd/scanner/Dockerfile -t liusha/scanner .

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
