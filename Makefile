.PHONY: up down logs migrate migrate-down run-api build-api test test-unit test-integration lint fmt tidy

COMPOSE = docker compose -f deployments/docker-compose.yml
MIGRATE_DSN ?= postgres://liusha:liusha@localhost:5432/liusha?sslmode=disable

up:
	$(COMPOSE) up -d

down:
	$(COMPOSE) down

logs:
	$(COMPOSE) logs -f --tail=100

migrate:
	go run -tags 'postgres' github.com/golang-migrate/migrate/v4/cmd/migrate \
		-path db/migrations \
		-database '$(MIGRATE_DSN)' up

migrate-down:
	go run -tags 'postgres' github.com/golang-migrate/migrate/v4/cmd/migrate \
		-path db/migrations \
		-database '$(MIGRATE_DSN)' down 1

run-api:
	go run ./cmd/api

build-api:
	docker build -f cmd/api/Dockerfile -t liusha/api .

test: test-unit test-integration

test-unit:
	go test -race -short ./...

test-integration:
	go test -race -run Integration ./...

lint:
	go vet ./...
	gofmt -l . | tee /dev/stderr | (! read)

fmt:
	gofmt -w .

tidy:
	go mod tidy
