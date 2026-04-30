.PHONY: up down logs migrate migrate-down run-api build-api build-proxy build-scanner build-vulnapp test test-unit test-integration lint fmt tidy vet e2e-bac

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

build-api:
	docker build -f cmd/api/Dockerfile -t liusha/api .

build-proxy:
	docker build -f cmd/proxy/Dockerfile -t liusha/proxy .

build-scanner:
	docker build -f cmd/scanner/Dockerfile -t liusha/scanner .

build-vulnapp:
	docker build -f cmd/vulnapp/Dockerfile -t liusha/vulnapp .

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

e2e-bac:
	go run ./cmd/e2e-bac
