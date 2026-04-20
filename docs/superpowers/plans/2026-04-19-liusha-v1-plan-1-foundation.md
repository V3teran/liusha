# Liusha v1 — Plan 1/8: Foundation & Skeleton

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Stand up a greenfield Go repo with docker-compose infrastructure, versioned DB schema, structured logging, and an `api` binary that can create/list `engagement` rows over HTTP — verified end-to-end by integration tests.

**Architecture:** Single Go module `github.com/V3teran/liusha` following golang-standards/project-layout. Infra (postgres+pgvector, redis, asynqmon) runs in docker-compose. Schema is owned by `db/migrations/` and applied via golang-migrate. `internal/engagement/store` owns the only data access; `internal/httpapi` handles REST. Logs carry W3C `trace_id` end-to-end.

**Tech Stack (latest stable only — no pinned patch versions in this plan):** Go latest stable (via `go-version: 'stable'` in CI and `golang:alpine` day-of-latest tag in Dockerfile), Gin, pgx/v5, golang-migrate/v4, Viper, zerolog, google/uuid, testify, testcontainers-go — all pulled with `go get <pkg>@latest`. Images: `pgvector/pgvector:pg17` (or newer pg tag available on build day), `redis:8` (or newer), `hibiken/asynqmon:latest`. Confirm each tag on the day tasks are executed; if upstream has a newer stable, use it.

**Series Context:** This is Plan 1 of 8 (Foundation → Proxy/Proxify → ~~mcp-server~~ *[DEPRECATED, see Plan 3 banner]* → Agent Runtime → Phase 1 Roles → Chain Matcher → Frontend → E2E+DLQ). Tool execution no longer uses a separate MCP server process; `agent-worker` directly invokes `internal/tool/runner.DockerRunner` via the Docker SDK to launch ephemeral `liusha/vuln-tools` containers. Plan 1 intentionally contains no agent, no Asynq worker, and no LLM wiring — those arrive in later plans. Plan 1 ships a thin but real vertical slice: HTTP → Postgres round-trip with migrations, tracing, and CI.

**Spec Reference:** `docs/superpowers/specs/2026-04-18-liusha-refactor-design.md` §4 (containers), §7 (schema), §8 (layout), §11 (observability), §12 (config).

---

## File Structure

Files created in this plan (in order of appearance):

```
liusha/
├── go.mod                                      # Task 1
├── go.sum                                      # Task 1+
├── .gitignore                                  # Task 1
├── .env.example                                # Task 1
├── Makefile                                    # Task 2
├── config/
│   └── config.yaml                             # Task 3
├── internal/
│   ├── config/
│   │   ├── config.go                           # Task 3
│   │   └── config_test.go                      # Task 3
│   ├── logx/
│   │   ├── logger.go                           # Task 4
│   │   ├── middleware.go                       # Task 4
│   │   └── logger_test.go                      # Task 4
│   ├── engagement/
│   │   ├── model.go                            # Task 10
│   │   ├── model_test.go                       # Task 10
│   │   └── store/
│   │       ├── postgres.go                     # Task 11
│   │       └── postgres_test.go                # Task 11
│   └── httpapi/
│       ├── server.go                           # Task 12
│       ├── engagement_handler.go               # Task 13
│       └── engagement_handler_test.go          # Task 13
├── db/
│   └── migrations/
│       ├── 0001_engagement.up.sql              # Task 5
│       ├── 0001_engagement.down.sql            # Task 5
│       ├── 0002_analysis_window.up.sql         # Task 6
│       ├── 0002_analysis_window.down.sql       # Task 6
│       ├── 0003_agent_task.up.sql              # Task 7
│       ├── 0003_agent_task.down.sql            # Task 7
│       ├── 0004_graph.up.sql                   # Task 8
│       ├── 0004_graph.down.sql                 # Task 8
│       ├── 0005_http_flow.up.sql               # Task 9
│       └── 0005_http_flow.down.sql             # Task 9
├── cmd/
│   └── api/
│       ├── main.go                             # Task 14
│       └── Dockerfile                          # Task 14
├── deployments/
│   └── docker-compose.yml                      # Task 2
└── .github/
    └── workflows/
        └── ci.yml                              # Task 15
```

Each `internal/<domain>` directory owns its own types and store — there is no root `internal/store/`. `db/migrations/` is the sole source of schema truth.

---

## Task 1: Bootstrap Go module

**Files:**
- Create: `go.mod`
- Create: `.gitignore`
- Create: `.env.example`
- Create: `README.md`

- [ ] **Step 1: Initialize module**

Run:
```bash
cd /Users/Xlbula/workspace/programs/go/liusha
go mod init github.com/V3teran/liusha
```

Expected: `go: creating new go.mod: module github.com/V3teran/liusha`

- [ ] **Step 2: Add baseline dependencies (always `@latest`)**

Run:
```bash
go get github.com/gin-gonic/gin@latest
go get github.com/jackc/pgx/v5@latest
go get github.com/jackc/pgx/v5/pgxpool@latest
go get github.com/rs/zerolog@latest
go get github.com/spf13/viper@latest
go get github.com/google/uuid@latest
go get github.com/golang-migrate/migrate/v4@latest
go get github.com/golang-migrate/migrate/v4/database/postgres@latest
go get github.com/golang-migrate/migrate/v4/source/file@latest
go get github.com/stretchr/testify@latest
go get github.com/testcontainers/testcontainers-go@latest
go get github.com/testcontainers/testcontainers-go/modules/postgres@latest
go mod tidy
```

Expected: `go.sum` created; `go.mod` `go` directive set to current latest stable Go (e.g. `go 1.24` or newer). If `go mod tidy` complains about the `go` directive, bump to what the shell reports from `go version`. **Never downgrade a dep to avoid a minor issue — fix the call site instead.**

- [ ] **Step 3: Write `.gitignore`**

Create `.gitignore`:
```
# Binaries
/bin/
cmd/*/dist/
*.exe
*.test
*.out

# Local env
.env
.env.local
.env.*.local

# Editor
.vscode/
.idea/
.DS_Store

# Embedded frontend build artifact (produced by liusha-ui, not checked in)
internal/web/dist/

# Coverage
coverage.out
coverage.html
```

- [ ] **Step 4: Write `.env.example`**

Create `.env.example`:
```
# Copy to .env.development or .env.local and fill in.
# All values below are non-secret placeholders safe for commit.

LIUSHA_POSTGRES_DSN=postgres://liusha:liusha@localhost:5432/liusha?sslmode=disable
LIUSHA_REDIS_ADDR=localhost:6379
LIUSHA_API_ADDR=0.0.0.0:8080
LIUSHA_ENV=development
LIUSHA_LOG_LEVEL=debug
```

- [ ] **Step 5: Write minimal `README.md`**

Create `README.md`:
```markdown
# Liusha

AI pentest platform (greenfield v1).

See `docs/superpowers/specs/2026-04-18-liusha-refactor-design.md` for the design.

## Quickstart

```
cp .env.example .env.development
make up
make migrate
make run-api
```
```

- [ ] **Step 6: Commit**

```bash
git add go.mod go.sum .gitignore .env.example README.md
git commit -m "chore: bootstrap go module and baseline tooling"
```

---

## Task 2: Makefile and docker-compose infrastructure

**Files:**
- Create: `Makefile`
- Create: `deployments/docker-compose.yml`

- [ ] **Step 1: Write `deployments/docker-compose.yml`**

```yaml
services:
  postgres:
    # Use the newest pg tag pgvector publishes on build day (pg17 or newer).
    image: pgvector/pgvector:pg17
    container_name: liusha-postgres
    environment:
      POSTGRES_USER: liusha
      POSTGRES_PASSWORD: liusha
      POSTGRES_DB: liusha
    ports:
      - "5432:5432"
    volumes:
      - liusha-pg:/var/lib/postgresql/data
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U liusha -d liusha"]
      interval: 5s
      timeout: 3s
      retries: 10

  redis:
    # Use current latest major (redis:8 as of 2026; bump on build day if newer exists).
    image: redis:8
    container_name: liusha-redis
    ports:
      - "6379:6379"
    volumes:
      - liusha-redis:/data
    healthcheck:
      test: ["CMD", "redis-cli", "ping"]
      interval: 5s
      timeout: 3s
      retries: 10

  asynqmon:
    image: hibiken/asynqmon:latest
    container_name: liusha-asynqmon
    command: ["--redis-addr=redis:6379"]
    ports:
      - "8081:8080"
    depends_on:
      redis:
        condition: service_healthy

volumes:
  liusha-pg:
  liusha-redis:
```

- [ ] **Step 2: Write `Makefile`**

```makefile
.PHONY: up down logs migrate migrate-down run-api test test-unit test-integration lint fmt tidy

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
```

- [ ] **Step 3: Verify infra boots**

Run:
```bash
make up
docker compose -f deployments/docker-compose.yml ps
```

Expected: `postgres`, `redis`, `asynqmon` all `running` / `healthy`.

- [ ] **Step 4: Verify pgvector extension available**

Run:
```bash
docker exec liusha-postgres psql -U liusha -d liusha -c "CREATE EXTENSION IF NOT EXISTS vector; SELECT extname, extversion FROM pg_extension WHERE extname='vector';"
```

Expected: `vector` row returned with latest published pgvector version.

- [ ] **Step 5: Commit**

```bash
git add Makefile deployments/docker-compose.yml
git commit -m "chore: add docker-compose infra and makefile"
```

---

## Task 3: Config loader with fail-fast validation

**Files:**
- Create: `config/config.yaml`
- Create: `internal/config/config.go`
- Test: `internal/config/config_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/config/config_test.go`:
```go
package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/V3teran/liusha/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoad_FailsFastWhenPostgresDSNMissing(t *testing.T) {
	t.Setenv("LIUSHA_POSTGRES_DSN", "")
	t.Setenv("LIUSHA_REDIS_ADDR", "localhost:6379")

	_, err := config.Load(configPath(t))

	require.Error(t, err)
	assert.Contains(t, err.Error(), "postgres_dsn")
}

func TestLoad_ReadsFromEnvAndYAML(t *testing.T) {
	t.Setenv("LIUSHA_POSTGRES_DSN", "postgres://u:p@localhost:5432/db?sslmode=disable")
	t.Setenv("LIUSHA_REDIS_ADDR", "localhost:6379")
	t.Setenv("LIUSHA_API_ADDR", "0.0.0.0:8080")
	t.Setenv("LIUSHA_ENV", "development")
	t.Setenv("LIUSHA_LOG_LEVEL", "debug")

	cfg, err := config.Load(configPath(t))

	require.NoError(t, err)
	assert.Equal(t, "postgres://u:p@localhost:5432/db?sslmode=disable", cfg.PostgresDSN)
	assert.Equal(t, "localhost:6379", cfg.RedisAddr)
	assert.Equal(t, "0.0.0.0:8080", cfg.APIAddr)
	assert.Equal(t, "development", cfg.Env)
	assert.Equal(t, "debug", cfg.LogLevel)
	assert.Equal(t, 60, cfg.AnalysisWindow.TimeSeconds)
	assert.Equal(t, 500, cfg.AnalysisWindow.FlowCount)
}

func TestLoad_RejectsUnknownEnv(t *testing.T) {
	t.Setenv("LIUSHA_POSTGRES_DSN", "postgres://u:p@localhost/db?sslmode=disable")
	t.Setenv("LIUSHA_REDIS_ADDR", "localhost:6379")
	t.Setenv("LIUSHA_ENV", "staging-yolo")

	_, err := config.Load(configPath(t))

	require.Error(t, err)
	assert.Contains(t, err.Error(), "env")
}

func configPath(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	require.NoError(t, err)
	return filepath.Join(wd, "..", "..", "config", "config.yaml")
}
```

- [ ] **Step 2: Write `config/config.yaml`**

```yaml
analysis_window:
  time_seconds: 60
  flow_count: 500

api:
  read_timeout_seconds: 15
  write_timeout_seconds: 30

postgres:
  max_conns: 20
  min_conns: 2
```

- [ ] **Step 3: Run the test to verify it fails**

Run: `go test ./internal/config/...`
Expected: FAIL with `config: package not found` (package doesn't exist yet).

- [ ] **Step 4: Write `internal/config/config.go`**

```go
package config

import (
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/viper"
)

type Config struct {
	PostgresDSN    string
	RedisAddr      string
	APIAddr        string
	Env            string
	LogLevel       string
	AnalysisWindow AnalysisWindowConfig
	API            APIConfig
	Postgres       PostgresConfig
}

type AnalysisWindowConfig struct {
	TimeSeconds int
	FlowCount   int
}

type APIConfig struct {
	ReadTimeoutSeconds  int
	WriteTimeoutSeconds int
}

type PostgresConfig struct {
	MaxConns int
	MinConns int
}

var validEnvs = map[string]struct{}{
	"development": {},
	"production":  {},
	"test":        {},
}

func Load(yamlPath string) (Config, error) {
	v := viper.New()
	v.SetConfigFile(yamlPath)
	v.SetConfigType("yaml")
	if err := v.ReadInConfig(); err != nil {
		return Config{}, fmt.Errorf("read %s: %w", yamlPath, err)
	}

	v.SetEnvPrefix("LIUSHA")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()

	cfg := Config{
		PostgresDSN: v.GetString("POSTGRES_DSN"),
		RedisAddr:   v.GetString("REDIS_ADDR"),
		APIAddr:     v.GetString("API_ADDR"),
		Env:         v.GetString("ENV"),
		LogLevel:    v.GetString("LOG_LEVEL"),
		AnalysisWindow: AnalysisWindowConfig{
			TimeSeconds: v.GetInt("analysis_window.time_seconds"),
			FlowCount:   v.GetInt("analysis_window.flow_count"),
		},
		API: APIConfig{
			ReadTimeoutSeconds:  v.GetInt("api.read_timeout_seconds"),
			WriteTimeoutSeconds: v.GetInt("api.write_timeout_seconds"),
		},
		Postgres: PostgresConfig{
			MaxConns: v.GetInt("postgres.max_conns"),
			MinConns: v.GetInt("postgres.min_conns"),
		},
	}
	if cfg.APIAddr == "" {
		cfg.APIAddr = "0.0.0.0:8080"
	}
	if cfg.LogLevel == "" {
		cfg.LogLevel = "info"
	}
	if cfg.Env == "" {
		cfg.Env = "development"
	}

	if err := cfg.validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (c Config) validate() error {
	var errs []string
	if c.PostgresDSN == "" {
		errs = append(errs, "postgres_dsn is required (set LIUSHA_POSTGRES_DSN)")
	}
	if c.RedisAddr == "" {
		errs = append(errs, "redis_addr is required (set LIUSHA_REDIS_ADDR)")
	}
	if _, ok := validEnvs[c.Env]; !ok {
		errs = append(errs, fmt.Sprintf("env must be one of development/production/test, got %q", c.Env))
	}
	if c.AnalysisWindow.TimeSeconds <= 0 {
		errs = append(errs, "analysis_window.time_seconds must be > 0")
	}
	if c.AnalysisWindow.FlowCount <= 0 {
		errs = append(errs, "analysis_window.flow_count must be > 0")
	}
	if len(errs) > 0 {
		return errors.New("invalid config: " + strings.Join(errs, "; "))
	}
	return nil
}
```

- [ ] **Step 5: Run the test to verify it passes**

Run: `go test ./internal/config/...`
Expected: PASS, 3/3 tests.

- [ ] **Step 6: Commit**

```bash
git add config/ internal/config/
git commit -m "feat(config): add viper-backed config loader with fail-fast validation"
```

---

## Task 4: Structured logger with enforced trace_id

**Files:**
- Create: `internal/logx/logger.go`
- Create: `internal/logx/middleware.go`
- Test: `internal/logx/logger_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/logx/logger_test.go`:
```go
package logx_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/V3teran/liusha/internal/logx"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFromContext_AttachesTraceID(t *testing.T) {
	var buf bytes.Buffer
	logx.SetWriterForTest(&buf)
	t.Cleanup(func() { logx.SetWriterForTest(os.Stderr) })

	ctx := logx.WithTraceID(context.Background(), "abc-123")
	logx.From(ctx).Info().Str("event", "hello").Send()

	var got map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &got))
	assert.Equal(t, "abc-123", got["trace_id"])
	assert.Equal(t, "hello", got["event"])
}

func TestDevelopmentMode_PanicsWithoutTraceID(t *testing.T) {
	logx.SetDevelopmentStrictForTest(true)
	t.Cleanup(func() { logx.SetDevelopmentStrictForTest(false) })

	assert.Panics(t, func() {
		logx.From(context.Background()).Info().Msg("no trace")
	})
}

func TestTraceMiddleware_PropagatesW3CTraceparent(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(logx.TraceMiddleware())
	var capturedTrace string
	r.GET("/ping", func(c *gin.Context) {
		capturedTrace = logx.TraceIDFromContext(c.Request.Context())
		c.Status(204)
	})

	req := httptest.NewRequest("GET", "/ping", nil)
	req.Header.Set("traceparent", "00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, 204, w.Code)
	assert.Equal(t, "0af7651916cd43dd8448eb211c80319c", capturedTrace)
	assert.True(t, strings.HasPrefix(w.Header().Get("traceparent"), "00-0af7651916cd43dd8448eb211c80319c-"))
}

func TestTraceMiddleware_GeneratesTraceIDWhenMissing(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(logx.TraceMiddleware())
	var capturedTrace string
	r.GET("/ping", func(c *gin.Context) {
		capturedTrace = logx.TraceIDFromContext(c.Request.Context())
		c.Status(204)
	})

	req := httptest.NewRequest("GET", "/ping", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Len(t, capturedTrace, 32)
}
```

- [ ] **Step 2: Run test to confirm failure**

Run: `go test ./internal/logx/...`
Expected: FAIL — package does not exist.

- [ ] **Step 3: Write `internal/logx/logger.go`**

```go
package logx

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"io"
	"os"
	"sync"
	"sync/atomic"

	"github.com/rs/zerolog"
)

type ctxKey int

const traceIDKey ctxKey = iota

var (
	writerMu         sync.RWMutex
	writer io.Writer = os.Stderr
	strict atomic.Bool
)

func Init(level string, developmentStrict bool) {
	lvl, err := zerolog.ParseLevel(level)
	if err != nil {
		lvl = zerolog.InfoLevel
	}
	zerolog.SetGlobalLevel(lvl)
	zerolog.TimeFieldFormat = zerolog.TimeFormatUnixMs
	strict.Store(developmentStrict)
}

func SetDevelopmentStrictForTest(v bool) { strict.Store(v) }

func SetWriterForTest(w io.Writer) {
	writerMu.Lock()
	defer writerMu.Unlock()
	writer = w
}

func WithTraceID(ctx context.Context, traceID string) context.Context {
	return context.WithValue(ctx, traceIDKey, traceID)
}

func TraceIDFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(traceIDKey).(string); ok {
		return v
	}
	return ""
}

func From(ctx context.Context) *zerolog.Logger {
	traceID := TraceIDFromContext(ctx)
	if traceID == "" && strict.Load() {
		panic("logx: attempted to log without trace_id in strict (development) mode")
	}
	writerMu.RLock()
	w := writer
	writerMu.RUnlock()
	l := zerolog.New(w).With().Timestamp().Str("trace_id", traceID).Logger()
	return &l
}

func NewTraceID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "00000000000000000000000000000000"
	}
	return hex.EncodeToString(b[:])
}
```

- [ ] **Step 4: Write `internal/logx/middleware.go`**

```go
package logx

import (
	"crypto/rand"
	"encoding/hex"
	"strings"

	"github.com/gin-gonic/gin"
)

// TraceMiddleware extracts trace_id from W3C `traceparent` header or
// generates a new one, then stashes it into the request context and
// echoes `traceparent` back on the response for downstream hops.
func TraceMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		traceID, traceparent := extractOrMint(c.GetHeader("traceparent"))
		ctx := WithTraceID(c.Request.Context(), traceID)
		c.Request = c.Request.WithContext(ctx)
		c.Header("traceparent", traceparent)
		c.Next()
	}
}

func extractOrMint(header string) (string, string) {
	parts := strings.Split(header, "-")
	if len(parts) == 4 && len(parts[1]) == 32 {
		return parts[1], header
	}
	traceID := NewTraceID()
	spanID := randHex(8)
	return traceID, "00-" + traceID + "-" + spanID + "-01"
}

func randHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
```

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./internal/logx/...`
Expected: PASS, 4/4 tests.

- [ ] **Step 6: Commit**

```bash
git add internal/logx/
git commit -m "feat(logx): zerolog + W3C traceparent middleware with strict dev mode"
```

---

## Task 5: Migration 0001 — engagement table

**Files:**
- Create: `db/migrations/0001_engagement.up.sql`
- Create: `db/migrations/0001_engagement.down.sql`

- [ ] **Step 1: Write `0001_engagement.up.sql`**

```sql
CREATE EXTENSION IF NOT EXISTS vector;

CREATE TABLE engagement (
    id           uuid PRIMARY KEY,
    tenant_id    uuid NOT NULL,
    mode         text NOT NULL CHECK (mode IN ('proxy','browser','ctf')),
    scope        jsonb NOT NULL,
    status       text NOT NULL DEFAULT 'active'
                 CHECK (status IN ('active','paused','archived','aborted')),
    objective    text,
    version      bigint NOT NULL DEFAULT 1,
    started_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now(),
    ended_at     timestamptz,
    meta         jsonb
);

CREATE INDEX engagement_tenant_started_idx ON engagement (tenant_id, started_at DESC);
CREATE INDEX engagement_status_idx ON engagement (status) WHERE status <> 'archived';
```

- [ ] **Step 2: Write `0001_engagement.down.sql`**

```sql
DROP TABLE IF EXISTS engagement;
```

- [ ] **Step 3: Apply and verify**

Run:
```bash
make migrate
docker exec liusha-postgres psql -U liusha -d liusha -c "\d engagement"
```

Expected: table exists with listed columns.

- [ ] **Step 4: Commit**

```bash
git add db/migrations/0001_engagement.*
git commit -m "feat(db): migration 0001 engagement table"
```

---

## Task 6: Migration 0002 — analysis_window table

**Files:**
- Create: `db/migrations/0002_analysis_window.up.sql`
- Create: `db/migrations/0002_analysis_window.down.sql`

- [ ] **Step 1: Write `0002_analysis_window.up.sql`**

```sql
CREATE TABLE analysis_window (
    id             uuid PRIMARY KEY,
    engagement_id  uuid NOT NULL REFERENCES engagement(id) ON DELETE CASCADE,
    trigger_kind   text NOT NULL CHECK (trigger_kind IN ('time','flow_count','manual')),
    flow_count     int,
    started_at     timestamptz NOT NULL DEFAULT now(),
    ended_at       timestamptz
);

CREATE INDEX analysis_window_engagement_idx
    ON analysis_window (engagement_id, started_at DESC);
```

- [ ] **Step 2: Write `0002_analysis_window.down.sql`**

```sql
DROP TABLE IF EXISTS analysis_window;
```

- [ ] **Step 3: Apply and verify**

Run:
```bash
make migrate
docker exec liusha-postgres psql -U liusha -d liusha -c "\d analysis_window"
```

Expected: table exists with FK to `engagement`.

- [ ] **Step 4: Commit**

```bash
git add db/migrations/0002_analysis_window.*
git commit -m "feat(db): migration 0002 analysis_window table"
```

---

## Task 7: Migration 0003 — agent_task table

**Files:**
- Create: `db/migrations/0003_agent_task.up.sql`
- Create: `db/migrations/0003_agent_task.down.sql`

- [ ] **Step 1: Write `0003_agent_task.up.sql`**

```sql
CREATE TABLE agent_task (
    id               uuid PRIMARY KEY,
    window_id        uuid NOT NULL REFERENCES analysis_window(id) ON DELETE CASCADE,
    role             text NOT NULL,
    status           text NOT NULL
                     CHECK (status IN ('pending','running','succeeded','failed','cancelled')),
    input_node_ids   uuid[] NOT NULL DEFAULT '{}',
    output_node_ids  uuid[] NOT NULL DEFAULT '{}',
    cost_micro_usd   bigint NOT NULL DEFAULT 0,
    started_at       timestamptz,
    ended_at         timestamptz
);

CREATE INDEX agent_task_window_idx ON agent_task (window_id, status);
CREATE INDEX agent_task_role_idx ON agent_task (role, status);
```

- [ ] **Step 2: Write `0003_agent_task.down.sql`**

```sql
DROP TABLE IF EXISTS agent_task;
```

- [ ] **Step 3: Apply and verify**

Run:
```bash
make migrate
docker exec liusha-postgres psql -U liusha -d liusha -c "\d agent_task"
```

Expected: table exists.

- [ ] **Step 4: Commit**

```bash
git add db/migrations/0003_agent_task.*
git commit -m "feat(db): migration 0003 agent_task table"
```

---

## Task 8: Migration 0004 — graph_node + graph_edge

**Files:**
- Create: `db/migrations/0004_graph.up.sql`
- Create: `db/migrations/0004_graph.down.sql`

- [ ] **Step 1: Write `0004_graph.up.sql`**

```sql
CREATE TABLE graph_node (
    id              uuid PRIMARY KEY,
    engagement_id   uuid NOT NULL REFERENCES engagement(id) ON DELETE CASCADE,
    kind            text NOT NULL
                    CHECK (kind IN ('surface','hypothesis','finding','exploit_chain','evidence')),
    payload         jsonb NOT NULL,
    vector          vector(1536),
    embedding_model text,
    dedup_key       text,
    created_at      timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX graph_node_eng_kind_created_idx
    ON graph_node (engagement_id, kind, created_at DESC);
CREATE INDEX graph_node_vector_hnsw_idx
    ON graph_node USING hnsw (vector vector_cosine_ops);
CREATE UNIQUE INDEX graph_node_dedup_idx
    ON graph_node (engagement_id, dedup_key)
    WHERE dedup_key IS NOT NULL;

CREATE TABLE graph_edge (
    id            uuid PRIMARY KEY,
    engagement_id uuid NOT NULL REFERENCES engagement(id) ON DELETE CASCADE,
    src_id        uuid NOT NULL REFERENCES graph_node(id) ON DELETE CASCADE,
    dst_id        uuid NOT NULL REFERENCES graph_node(id) ON DELETE CASCADE,
    kind          text NOT NULL
                  CHECK (kind IN ('finding','chains_to','evidences','enables')),
    created_at    timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX graph_edge_src_idx ON graph_edge (engagement_id, src_id, kind);
CREATE INDEX graph_edge_dst_idx ON graph_edge (engagement_id, dst_id, kind);
```

- [ ] **Step 2: Write `0004_graph.down.sql`**

```sql
DROP TABLE IF EXISTS graph_edge;
DROP TABLE IF EXISTS graph_node;
```

- [ ] **Step 3: Apply and verify**

Run:
```bash
make migrate
docker exec liusha-postgres psql -U liusha -d liusha -c "\d graph_node" \
    && docker exec liusha-postgres psql -U liusha -d liusha -c "\d graph_edge"
```

Expected: both tables exist; `graph_node_vector_hnsw_idx` present.

- [ ] **Step 4: Commit**

```bash
git add db/migrations/0004_graph.*
git commit -m "feat(db): migration 0004 graph_node and graph_edge with pgvector hnsw"
```

---

## Task 9: Migration 0005 — http_flow partitioned table

**Files:**
- Create: `db/migrations/0005_http_flow.up.sql`
- Create: `db/migrations/0005_http_flow.down.sql`

- [ ] **Step 1: Write `0005_http_flow.up.sql`**

```sql
CREATE TABLE http_flow (
    id                uuid NOT NULL,
    engagement_id     uuid NOT NULL,
    ts                timestamptz NOT NULL,
    method            text,
    url               text,
    req_content_type  text,
    req_headers       jsonb,
    req_body          bytea,
    resp_status       int,
    resp_content_type text,
    resp_headers      jsonb,
    resp_body         bytea,
    body_truncated    boolean NOT NULL DEFAULT false,
    PRIMARY KEY (id, ts)
) PARTITION BY RANGE (ts);

-- Default partition so writes never fail before the month rollover cron exists.
CREATE TABLE http_flow_default PARTITION OF http_flow DEFAULT;

CREATE INDEX http_flow_default_eng_ts_idx
    ON http_flow_default (engagement_id, ts DESC);
```

- [ ] **Step 2: Write `0005_http_flow.down.sql`**

```sql
DROP TABLE IF EXISTS http_flow_default;
DROP TABLE IF EXISTS http_flow;
```

- [ ] **Step 3: Apply and verify**

Run:
```bash
make migrate
docker exec liusha-postgres psql -U liusha -d liusha -c "\d+ http_flow"
```

Expected: partitioned table with `http_flow_default` child.

- [ ] **Step 4: Commit**

```bash
git add db/migrations/0005_http_flow.*
git commit -m "feat(db): migration 0005 http_flow partitioned table"
```

---

## Task 10: Engagement domain model

**Files:**
- Create: `internal/engagement/model.go`
- Test: `internal/engagement/model_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/engagement/model_test.go`:
```go
package engagement_test

import (
	"testing"

	"github.com/V3teran/liusha/internal/engagement"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNew_SetsDefaults(t *testing.T) {
	e, err := engagement.New(engagement.CreateInput{
		TenantID:  mustUUID(t, "11111111-1111-1111-1111-111111111111"),
		Mode:      engagement.ModeProxy,
		Scope:     map[string]any{"hosts": []string{"example.com"}},
		Objective: "Find RCE in upload flow",
	})

	require.NoError(t, err)
	assert.Equal(t, engagement.StatusActive, e.Status)
	assert.Equal(t, int64(1), e.Version)
	assert.NotEqual(t, e.ID.String(), "00000000-0000-0000-0000-000000000000")
}

func TestNew_RejectsUnknownMode(t *testing.T) {
	_, err := engagement.New(engagement.CreateInput{
		TenantID: mustUUID(t, "11111111-1111-1111-1111-111111111111"),
		Mode:     engagement.Mode("wifi"),
		Scope:    map[string]any{"hosts": []string{"example.com"}},
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "mode")
}

func TestNew_RejectsEmptyScope(t *testing.T) {
	_, err := engagement.New(engagement.CreateInput{
		TenantID: mustUUID(t, "11111111-1111-1111-1111-111111111111"),
		Mode:     engagement.ModeProxy,
		Scope:    map[string]any{},
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "scope")
}

func mustUUID(t *testing.T, s string) (u uuid) { //nolint: unused
	return uuid{} // placeholder, replaced below
}
```

- [ ] **Step 2: Fix test to use real uuid (rewrite `model_test.go`)**

```go
package engagement_test

import (
	"testing"

	"github.com/V3teran/liusha/internal/engagement"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNew_SetsDefaults(t *testing.T) {
	e, err := engagement.New(engagement.CreateInput{
		TenantID:  uuid.MustParse("11111111-1111-1111-1111-111111111111"),
		Mode:      engagement.ModeProxy,
		Scope:     map[string]any{"hosts": []string{"example.com"}},
		Objective: "Find RCE in upload flow",
	})

	require.NoError(t, err)
	assert.Equal(t, engagement.StatusActive, e.Status)
	assert.Equal(t, int64(1), e.Version)
	assert.NotEqual(t, uuid.Nil, e.ID)
	assert.False(t, e.StartedAt.IsZero())
}

func TestNew_RejectsUnknownMode(t *testing.T) {
	_, err := engagement.New(engagement.CreateInput{
		TenantID: uuid.MustParse("11111111-1111-1111-1111-111111111111"),
		Mode:     engagement.Mode("wifi"),
		Scope:    map[string]any{"hosts": []string{"example.com"}},
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "mode")
}

func TestNew_RejectsEmptyScope(t *testing.T) {
	_, err := engagement.New(engagement.CreateInput{
		TenantID: uuid.MustParse("11111111-1111-1111-1111-111111111111"),
		Mode:     engagement.ModeProxy,
		Scope:    map[string]any{},
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "scope")
}
```

- [ ] **Step 3: Run the test to confirm it fails**

Run: `go test ./internal/engagement/...`
Expected: FAIL — package doesn't exist.

- [ ] **Step 4: Write `internal/engagement/model.go`**

```go
package engagement

import (
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

type Mode string

const (
	ModeProxy   Mode = "proxy"
	ModeBrowser Mode = "browser"
	ModeCTF     Mode = "ctf"
)

var validModes = map[Mode]struct{}{
	ModeProxy: {}, ModeBrowser: {}, ModeCTF: {},
}

type Status string

const (
	StatusActive   Status = "active"
	StatusPaused   Status = "paused"
	StatusArchived Status = "archived"
	StatusAborted  Status = "aborted"
)

type Engagement struct {
	ID        uuid.UUID
	TenantID  uuid.UUID
	Mode      Mode
	Scope     map[string]any
	Status    Status
	Objective string
	Version   int64
	StartedAt time.Time
	UpdatedAt time.Time
	EndedAt   *time.Time
	Meta      map[string]any
}

type CreateInput struct {
	TenantID  uuid.UUID
	Mode      Mode
	Scope     map[string]any
	Objective string
	Meta      map[string]any
}

func New(in CreateInput) (Engagement, error) {
	if in.TenantID == uuid.Nil {
		return Engagement{}, errors.New("tenant_id is required")
	}
	if _, ok := validModes[in.Mode]; !ok {
		return Engagement{}, fmt.Errorf("mode must be proxy/browser/ctf, got %q", in.Mode)
	}
	if len(in.Scope) == 0 {
		return Engagement{}, errors.New("scope is required and must be non-empty")
	}
	now := time.Now().UTC()
	return Engagement{
		ID:        uuid.New(),
		TenantID:  in.TenantID,
		Mode:      in.Mode,
		Scope:     in.Scope,
		Status:    StatusActive,
		Objective: in.Objective,
		Version:   1,
		StartedAt: now,
		UpdatedAt: now,
		Meta:      in.Meta,
	}, nil
}
```

- [ ] **Step 5: Run the test to verify it passes**

Run: `go test ./internal/engagement/...`
Expected: PASS, 3/3 tests.

- [ ] **Step 6: Commit**

```bash
git add internal/engagement/model.go internal/engagement/model_test.go
git commit -m "feat(engagement): domain model with validation"
```

---

## Task 11: Engagement Postgres store with optimistic locking

**Files:**
- Create: `internal/engagement/store/postgres.go`
- Test: `internal/engagement/store/postgres_test.go`

- [ ] **Step 1: Write the failing integration test**

Create `internal/engagement/store/postgres_test.go`:
```go
package store_test

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/V3teran/liusha/internal/engagement"
	"github.com/V3teran/liusha/internal/engagement/store"
	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

func TestIntegration_EngagementStore_CreateGetList(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	ctx := context.Background()

	pg, err := postgres.Run(ctx,
		"pgvector/pgvector:pg16",
		postgres.WithDatabase("liusha"),
		postgres.WithUsername("liusha"),
		postgres.WithPassword("liusha"),
		postgres.BasicWaitStrategies(),
		postgres.WithWaitStrategy(wait.ForListeningPort("5432/tcp").WithStartupTimeout(60*time.Second)),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = pg.Terminate(ctx) })

	dsn, err := pg.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)

	m, err := migrate.New("file://"+migrationsDir(t), dsn)
	require.NoError(t, err)
	require.NoError(t, m.Up())

	pool, err := pgxpool.New(ctx, dsn)
	require.NoError(t, err)
	t.Cleanup(pool.Close)

	s := store.NewPostgres(pool)
	e, err := engagement.New(engagement.CreateInput{
		TenantID:  uuid.MustParse("22222222-2222-2222-2222-222222222222"),
		Mode:      engagement.ModeProxy,
		Scope:     map[string]any{"hosts": []string{"example.com"}},
		Objective: "probe upload flow",
	})
	require.NoError(t, err)

	require.NoError(t, s.Create(ctx, e))

	got, err := s.Get(ctx, e.ID)
	require.NoError(t, err)
	assert.Equal(t, e.ID, got.ID)
	assert.Equal(t, e.TenantID, got.TenantID)
	assert.Equal(t, engagement.ModeProxy, got.Mode)
	assert.Equal(t, engagement.StatusActive, got.Status)
	assert.Equal(t, "probe upload flow", got.Objective)

	list, err := s.ListByTenant(ctx, e.TenantID, 10)
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.Equal(t, e.ID, list[0].ID)
}

func TestIntegration_EngagementStore_ArchiveOptimisticLock(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	ctx := context.Background()
	pool, cleanup := newTestPool(ctx, t)
	t.Cleanup(cleanup)

	s := store.NewPostgres(pool)
	e, err := engagement.New(engagement.CreateInput{
		TenantID: uuid.New(),
		Mode:     engagement.ModeProxy,
		Scope:    map[string]any{"hosts": []string{"example.com"}},
	})
	require.NoError(t, err)
	require.NoError(t, s.Create(ctx, e))

	require.NoError(t, s.Archive(ctx, e.ID, e.Version))

	err = s.Archive(ctx, e.ID, e.Version) // stale version now
	require.ErrorIs(t, err, store.ErrVersionMismatch)
}

// newTestPool boots a container + runs migrations. Reused by both tests.
func newTestPool(ctx context.Context, t *testing.T) (*pgxpool.Pool, func()) {
	t.Helper()
	pg, err := postgres.Run(ctx,
		"pgvector/pgvector:pg16",
		postgres.WithDatabase("liusha"),
		postgres.WithUsername("liusha"),
		postgres.WithPassword("liusha"),
		postgres.BasicWaitStrategies(),
	)
	require.NoError(t, err)
	dsn, err := pg.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)
	m, err := migrate.New("file://"+migrationsDir(t), dsn)
	require.NoError(t, err)
	require.NoError(t, m.Up())
	pool, err := pgxpool.New(ctx, dsn)
	require.NoError(t, err)
	return pool, func() {
		pool.Close()
		_ = pg.Terminate(ctx)
	}
}

func migrationsDir(t *testing.T) string {
	t.Helper()
	_, thisFile, _, _ := runtime.Caller(0)
	root := filepath.Join(filepath.Dir(thisFile), "..", "..", "..")
	abs, err := filepath.Abs(filepath.Join(root, "db", "migrations"))
	require.NoError(t, err)
	_, err = os.Stat(abs)
	require.NoError(t, err)
	return abs
}
```

- [ ] **Step 2: Run test to confirm failure**

Run: `go test ./internal/engagement/store/...`
Expected: FAIL — package does not exist.

- [ ] **Step 3: Write `internal/engagement/store/postgres.go`**

```go
package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/V3teran/liusha/internal/engagement"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrNotFound        = errors.New("engagement: not found")
	ErrVersionMismatch = errors.New("engagement: version mismatch (optimistic lock)")
)

type Postgres struct {
	pool *pgxpool.Pool
}

func NewPostgres(pool *pgxpool.Pool) *Postgres { return &Postgres{pool: pool} }

func (s *Postgres) Create(ctx context.Context, e engagement.Engagement) error {
	scope, err := json.Marshal(e.Scope)
	if err != nil {
		return fmt.Errorf("marshal scope: %w", err)
	}
	var meta []byte
	if e.Meta != nil {
		if meta, err = json.Marshal(e.Meta); err != nil {
			return fmt.Errorf("marshal meta: %w", err)
		}
	}
	_, err = s.pool.Exec(ctx, `
		INSERT INTO engagement
			(id, tenant_id, mode, scope, status, objective, version, started_at, updated_at, meta)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
	`, e.ID, e.TenantID, string(e.Mode), scope, string(e.Status),
		nullableString(e.Objective), e.Version, e.StartedAt, e.UpdatedAt, meta)
	return err
}

func (s *Postgres) Get(ctx context.Context, id uuid.UUID) (engagement.Engagement, error) {
	row := s.pool.QueryRow(ctx, `
		SELECT id, tenant_id, mode, scope, status, COALESCE(objective,''),
		       version, started_at, updated_at, ended_at, meta
		FROM engagement WHERE id = $1
	`, id)
	return scanEngagement(row)
}

func (s *Postgres) ListByTenant(ctx context.Context, tenantID uuid.UUID, limit int) ([]engagement.Engagement, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, tenant_id, mode, scope, status, COALESCE(objective,''),
		       version, started_at, updated_at, ended_at, meta
		FROM engagement
		WHERE tenant_id = $1
		ORDER BY started_at DESC
		LIMIT $2
	`, tenantID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]engagement.Engagement, 0)
	for rows.Next() {
		e, err := scanEngagement(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (s *Postgres) Archive(ctx context.Context, id uuid.UUID, expectedVersion int64) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE engagement
		SET status = 'archived', version = version + 1, updated_at = now(), ended_at = now()
		WHERE id = $1 AND version = $2
	`, id, expectedVersion)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		// Could be not found OR version mismatch; caller treats both as mismatch.
		return ErrVersionMismatch
	}
	return nil
}

func scanEngagement(row pgx.Row) (engagement.Engagement, error) {
	var (
		e            engagement.Engagement
		mode, status string
		scope, meta  []byte
		endedAt      *any
	)
	err := row.Scan(&e.ID, &e.TenantID, &mode, &scope, &status, &e.Objective,
		&e.Version, &e.StartedAt, &e.UpdatedAt, &endedAt, &meta)
	if errors.Is(err, pgx.ErrNoRows) {
		return engagement.Engagement{}, ErrNotFound
	}
	if err != nil {
		return engagement.Engagement{}, err
	}
	e.Mode = engagement.Mode(mode)
	e.Status = engagement.Status(status)
	if err := json.Unmarshal(scope, &e.Scope); err != nil {
		return engagement.Engagement{}, fmt.Errorf("unmarshal scope: %w", err)
	}
	if len(meta) > 0 {
		if err := json.Unmarshal(meta, &e.Meta); err != nil {
			return engagement.Engagement{}, fmt.Errorf("unmarshal meta: %w", err)
		}
	}
	return e, nil
}

func nullableString(s string) any {
	if s == "" {
		return nil
	}
	return s
}
```

- [ ] **Step 4: Run integration tests**

Run: `go test -run Integration ./internal/engagement/store/...`
Expected: PASS, 2/2 tests. (Requires Docker running.)

- [ ] **Step 5: Commit**

```bash
git add internal/engagement/store/
git commit -m "feat(engagement): pgx-backed store with optimistic locking and integration tests"
```

---

## Task 12: HTTP server skeleton with trace middleware

**Files:**
- Create: `internal/httpapi/server.go`

- [ ] **Step 1: Write `internal/httpapi/server.go`**

```go
package httpapi

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/V3teran/liusha/internal/config"
	"github.com/V3teran/liusha/internal/engagement"
	engstore "github.com/V3teran/liusha/internal/engagement/store"
	"github.com/V3teran/liusha/internal/logx"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type EngagementStore interface {
	Create(ctx context.Context, e engagement.Engagement) error
	Get(ctx context.Context, id uuid.UUID) (engagement.Engagement, error)
	ListByTenant(ctx context.Context, tenantID uuid.UUID, limit int) ([]engagement.Engagement, error)
	Archive(ctx context.Context, id uuid.UUID, expectedVersion int64) error
}

type Server struct {
	cfg    config.Config
	engSvc EngagementStore
	engine *gin.Engine
	http   *http.Server
}

func New(cfg config.Config, engSvc EngagementStore) *Server {
	if cfg.Env == "production" {
		gin.SetMode(gin.ReleaseMode)
	}
	r := gin.New()
	r.Use(gin.Recovery(), logx.TraceMiddleware())

	s := &Server{cfg: cfg, engSvc: engSvc, engine: r}
	s.routes()
	s.http = &http.Server{
		Addr:              cfg.APIAddr,
		Handler:           r,
		ReadHeaderTimeout: time.Duration(cfg.API.ReadTimeoutSeconds) * time.Second,
		WriteTimeout:      time.Duration(cfg.API.WriteTimeoutSeconds) * time.Second,
	}
	return s
}

func (s *Server) Handler() http.Handler { return s.engine }

func (s *Server) routes() {
	s.engine.GET("/healthz", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})
	v1 := s.engine.Group("/api/v1")
	{
		v1.POST("/engagements", s.createEngagement)
		v1.GET("/engagements", s.listEngagements)
		v1.GET("/engagements/:id", s.getEngagement)
		v1.POST("/engagements/:id/archive", s.archiveEngagement)
	}
}

func (s *Server) Run() error {
	logx.From(context.Background()).Info().Str("addr", s.cfg.APIAddr).Msg("api listening")
	if err := s.http.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func (s *Server) Shutdown(ctx context.Context) error { return s.http.Shutdown(ctx) }

// Shared error response — there is no DTO layer, just Gin JSON.
func respondError(c *gin.Context, status int, code, msg string) {
	logx.From(c.Request.Context()).Warn().
		Int("status", status).
		Str("code", code).
		Msg(msg)
	c.JSON(status, gin.H{"error": gin.H{"code": code, "message": msg}})
}

// Placeholder impls — filled in by Task 13.
func (s *Server) createEngagement(c *gin.Context)  { respondError(c, 501, "not_implemented", "pending task 13") }
func (s *Server) listEngagements(c *gin.Context)   { respondError(c, 501, "not_implemented", "pending task 13") }
func (s *Server) getEngagement(c *gin.Context)     { respondError(c, 501, "not_implemented", "pending task 13") }
func (s *Server) archiveEngagement(c *gin.Context) { respondError(c, 501, "not_implemented", "pending task 13") }

var _ EngagementStore = (*engstore.Postgres)(nil)
```

- [ ] **Step 2: Compile-check**

Run: `go build ./internal/httpapi/...`
Expected: success.

- [ ] **Step 3: Commit**

```bash
git add internal/httpapi/server.go
git commit -m "feat(httpapi): gin server skeleton with trace middleware and v1 routes"
```

---

## Task 13: Engagement handlers with integration tests

**Files:**
- Create: `internal/httpapi/engagement_handler.go`
- Modify: `internal/httpapi/server.go` (remove placeholder handler stubs)
- Test: `internal/httpapi/engagement_handler_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/httpapi/engagement_handler_test.go`:
```go
package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/V3teran/liusha/internal/config"
	"github.com/V3teran/liusha/internal/engagement/store"
	"github.com/V3teran/liusha/internal/httpapi"
	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
)

func TestIntegration_CreateAndGetEngagement(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	ctx := context.Background()
	pool, cleanup := newTestPool(ctx, t)
	t.Cleanup(cleanup)

	srv := httpapi.New(config.Config{
		Env:     "test",
		APIAddr: "127.0.0.1:0",
		API:     config.APIConfig{ReadTimeoutSeconds: 5, WriteTimeoutSeconds: 5},
	}, store.NewPostgres(pool))

	body := []byte(`{
		"tenant_id": "33333333-3333-3333-3333-333333333333",
		"mode": "proxy",
		"scope": {"hosts": ["example.com"]},
		"objective": "probe upload"
	}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/engagements", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	require.Equal(t, http.StatusCreated, w.Code)
	var created struct{ ID string }
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &created))
	require.NotEmpty(t, created.ID)

	getReq := httptest.NewRequest(http.MethodGet, "/api/v1/engagements/"+created.ID, nil)
	getW := httptest.NewRecorder()
	srv.Handler().ServeHTTP(getW, getReq)
	assert.Equal(t, http.StatusOK, getW.Code)

	listReq := httptest.NewRequest(http.MethodGet,
		"/api/v1/engagements?tenant_id=33333333-3333-3333-3333-333333333333", nil)
	listW := httptest.NewRecorder()
	srv.Handler().ServeHTTP(listW, listReq)
	assert.Equal(t, http.StatusOK, listW.Code)
	assert.Contains(t, listW.Body.String(), created.ID)
}

func TestIntegration_CreateEngagement_RejectsUnknownMode(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	ctx := context.Background()
	pool, cleanup := newTestPool(ctx, t)
	t.Cleanup(cleanup)

	srv := httpapi.New(config.Config{Env: "test", APIAddr: "127.0.0.1:0",
		API: config.APIConfig{ReadTimeoutSeconds: 5, WriteTimeoutSeconds: 5}},
		store.NewPostgres(pool))

	body := []byte(`{
		"tenant_id": "33333333-3333-3333-3333-333333333333",
		"mode": "wifi",
		"scope": {"hosts": ["example.com"]}
	}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/engagements", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "invalid_input")
}

func newTestPool(ctx context.Context, t *testing.T) (*pgxpool.Pool, func()) {
	t.Helper()
	pg, err := postgres.Run(ctx,
		"pgvector/pgvector:pg16",
		postgres.WithDatabase("liusha"),
		postgres.WithUsername("liusha"),
		postgres.WithPassword("liusha"),
		postgres.BasicWaitStrategies(),
	)
	require.NoError(t, err)
	dsn, err := pg.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)
	m, err := migrate.New("file://"+migrationsDir(t), dsn)
	require.NoError(t, err)
	require.NoError(t, m.Up())
	ctxPool, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctxPool, dsn)
	require.NoError(t, err)
	return pool, func() {
		pool.Close()
		_ = pg.Terminate(ctx)
	}
}

func migrationsDir(t *testing.T) string {
	t.Helper()
	_, thisFile, _, _ := runtime.Caller(0)
	root := filepath.Join(filepath.Dir(thisFile), "..", "..")
	abs, err := filepath.Abs(filepath.Join(root, "db", "migrations"))
	require.NoError(t, err)
	_, err = os.Stat(abs)
	require.NoError(t, err)
	return abs
}
```

- [ ] **Step 2: Run test to confirm failure**

Run: `go test -run Integration ./internal/httpapi/...`
Expected: FAIL — placeholders return 501.

- [ ] **Step 3: Write `internal/httpapi/engagement_handler.go`**

```go
package httpapi

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/V3teran/liusha/internal/engagement"
	engstore "github.com/V3teran/liusha/internal/engagement/store"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type createEngagementRequest struct {
	TenantID  string         `json:"tenant_id" binding:"required"`
	Mode      string         `json:"mode"      binding:"required"`
	Scope     map[string]any `json:"scope"     binding:"required"`
	Objective string         `json:"objective"`
	Meta      map[string]any `json:"meta"`
}

func (s *Server) createEngagementImpl(c *gin.Context) {
	var in createEngagementRequest
	if err := c.ShouldBindJSON(&in); err != nil {
		respondError(c, http.StatusBadRequest, "invalid_input", err.Error())
		return
	}
	tenantID, err := uuid.Parse(in.TenantID)
	if err != nil {
		respondError(c, http.StatusBadRequest, "invalid_input", "tenant_id must be uuid")
		return
	}
	e, err := engagement.New(engagement.CreateInput{
		TenantID:  tenantID,
		Mode:      engagement.Mode(in.Mode),
		Scope:     in.Scope,
		Objective: in.Objective,
		Meta:      in.Meta,
	})
	if err != nil {
		respondError(c, http.StatusBadRequest, "invalid_input", err.Error())
		return
	}
	if err := s.engSvc.Create(c.Request.Context(), e); err != nil {
		respondError(c, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	c.JSON(http.StatusCreated, marshalEngagement(e))
}

func (s *Server) listEngagementsImpl(c *gin.Context) {
	tenantIDStr := c.Query("tenant_id")
	if tenantIDStr == "" {
		respondError(c, http.StatusBadRequest, "invalid_input", "tenant_id query param required")
		return
	}
	tenantID, err := uuid.Parse(tenantIDStr)
	if err != nil {
		respondError(c, http.StatusBadRequest, "invalid_input", "tenant_id must be uuid")
		return
	}
	limit := 50
	if s := c.Query("limit"); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n > 0 && n <= 500 {
			limit = n
		}
	}
	list, err := s.engSvc.ListByTenant(c.Request.Context(), tenantID, limit)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	out := make([]map[string]any, 0, len(list))
	for _, e := range list {
		out = append(out, marshalEngagement(e))
	}
	c.JSON(http.StatusOK, gin.H{"items": out})
}

func (s *Server) getEngagementImpl(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		respondError(c, http.StatusBadRequest, "invalid_input", "id must be uuid")
		return
	}
	e, err := s.engSvc.Get(c.Request.Context(), id)
	if errors.Is(err, engstore.ErrNotFound) {
		respondError(c, http.StatusNotFound, "not_found", "engagement not found")
		return
	}
	if err != nil {
		respondError(c, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	c.JSON(http.StatusOK, marshalEngagement(e))
}

func (s *Server) archiveEngagementImpl(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		respondError(c, http.StatusBadRequest, "invalid_input", "id must be uuid")
		return
	}
	versionStr := c.Query("version")
	version, err := strconv.ParseInt(versionStr, 10, 64)
	if err != nil || version <= 0 {
		respondError(c, http.StatusBadRequest, "invalid_input", "version query param required (positive int)")
		return
	}
	if err := s.engSvc.Archive(c.Request.Context(), id, version); err != nil {
		if errors.Is(err, engstore.ErrVersionMismatch) {
			respondError(c, http.StatusConflict, "version_mismatch", "engagement version is stale")
			return
		}
		respondError(c, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	c.Status(http.StatusNoContent)
}

func marshalEngagement(e engagement.Engagement) map[string]any {
	out := map[string]any{
		"id":         e.ID,
		"tenant_id":  e.TenantID,
		"mode":       string(e.Mode),
		"scope":      e.Scope,
		"status":     string(e.Status),
		"objective":  e.Objective,
		"version":    e.Version,
		"started_at": e.StartedAt,
		"updated_at": e.UpdatedAt,
	}
	if e.EndedAt != nil {
		out["ended_at"] = *e.EndedAt
	}
	if len(e.Meta) > 0 {
		out["meta"] = e.Meta
	}
	return out
}
```

- [ ] **Step 4: Replace placeholder stubs in `internal/httpapi/server.go`**

Open `internal/httpapi/server.go` and replace the four placeholder methods at the bottom with:

```go
func (s *Server) createEngagement(c *gin.Context)  { s.createEngagementImpl(c) }
func (s *Server) listEngagements(c *gin.Context)   { s.listEngagementsImpl(c) }
func (s *Server) getEngagement(c *gin.Context)     { s.getEngagementImpl(c) }
func (s *Server) archiveEngagement(c *gin.Context) { s.archiveEngagementImpl(c) }
```

- [ ] **Step 5: Run integration tests**

Run: `go test -run Integration ./internal/httpapi/...`
Expected: PASS, 2/2 tests.

- [ ] **Step 6: Commit**

```bash
git add internal/httpapi/
git commit -m "feat(httpapi): engagement CRUD handlers with integration tests"
```

---

## Task 14: cmd/api binary and multi-stage Dockerfile

**Files:**
- Create: `cmd/api/main.go`
- Create: `cmd/api/Dockerfile`
- Modify: `deployments/docker-compose.yml` (add api service)
- Modify: `Makefile` (add `build-api` target)

- [ ] **Step 1: Write `cmd/api/main.go`**

```go
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/V3teran/liusha/internal/config"
	engstore "github.com/V3teran/liusha/internal/engagement/store"
	"github.com/V3teran/liusha/internal/httpapi"
	"github.com/V3teran/liusha/internal/logx"
	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	cfgPath := os.Getenv("LIUSHA_CONFIG")
	if cfgPath == "" {
		cfgPath = "config/config.yaml"
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		panic("config load: " + err.Error())
	}
	logx.Init(cfg.LogLevel, cfg.Env == "development")

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	pool, err := pgxpool.New(ctx, cfg.PostgresDSN)
	if err != nil {
		logx.From(ctx).Fatal().Err(err).Msg("postgres connect")
	}
	defer pool.Close()

	if err := pool.Ping(ctx); err != nil {
		logx.From(ctx).Fatal().Err(err).Msg("postgres ping")
	}

	srv := httpapi.New(cfg, engstore.NewPostgres(pool))

	errCh := make(chan error, 1)
	go func() { errCh <- srv.Run() }()

	select {
	case <-ctx.Done():
		logx.From(context.Background()).Info().Msg("shutdown signal")
	case err := <-errCh:
		logx.From(context.Background()).Error().Err(err).Msg("server exited")
	}

	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelShutdown()
	_ = srv.Shutdown(shutdownCtx)
}
```

- [ ] **Step 2: Write `cmd/api/Dockerfile`**

```dockerfile
# syntax=docker/dockerfile:1
# Use current latest stable Go alpine tag on build day (e.g. golang:1.24-alpine).
# Do not downgrade; if `go.mod` declares a newer `go` directive, bump this tag.
FROM golang:1.24-alpine AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/api ./cmd/api

FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /app
COPY --from=builder /out/api /app/api
COPY config/ /app/config/
COPY db/migrations/ /app/db/migrations/
USER nonroot:nonroot
EXPOSE 8080
ENTRYPOINT ["/app/api"]
```

- [ ] **Step 3: Add `api` service to `deployments/docker-compose.yml`**

Append under `services:` (before `volumes:`):

```yaml
  api:
    image: liusha/api
    build:
      context: ..
      dockerfile: cmd/api/Dockerfile
    container_name: liusha-api
    environment:
      LIUSHA_POSTGRES_DSN: postgres://liusha:liusha@postgres:5432/liusha?sslmode=disable
      LIUSHA_REDIS_ADDR: redis:6379
      LIUSHA_API_ADDR: 0.0.0.0:8080
      LIUSHA_ENV: development
      LIUSHA_LOG_LEVEL: info
      LIUSHA_CONFIG: /app/config/config.yaml
    depends_on:
      postgres:
        condition: service_healthy
      redis:
        condition: service_healthy
    ports:
      - "8080:8080"
```

- [ ] **Step 4: Add `build-api` target to `Makefile`**

Append:
```makefile
build-api:
	docker build -f cmd/api/Dockerfile -t liusha/api .
```

And add `build-api` to the `.PHONY` line.

- [ ] **Step 5: Verify binary builds and boots**

Run:
```bash
make build-api
make up
curl -sf http://localhost:8080/healthz
```

Expected: `{"status":"ok"}`.

- [ ] **Step 6: Smoke-test an engagement round-trip**

Run:
```bash
curl -sf -X POST http://localhost:8080/api/v1/engagements \
  -H 'Content-Type: application/json' \
  -d '{"tenant_id":"44444444-4444-4444-4444-444444444444","mode":"proxy","scope":{"hosts":["example.com"]},"objective":"smoke"}'
```

Expected: 201 with JSON body including an `id` field.

- [ ] **Step 7: Commit**

```bash
git add cmd/api/ deployments/docker-compose.yml Makefile
git commit -m "feat(api): cmd/api main, distroless dockerfile, compose wiring"
```

---

## Task 15: GitHub Actions CI

**Files:**
- Create: `.github/workflows/ci.yml`

- [ ] **Step 1: Write `.github/workflows/ci.yml`**

```yaml
name: ci

on:
  push:
    branches: [main]
  pull_request:

jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version: 'stable'
          cache: true
      - name: vet
        run: go vet ./...
      - name: gofmt
        run: |
          diff=$(gofmt -l .)
          if [ -n "$diff" ]; then
            echo "gofmt diff:" && echo "$diff" && exit 1
          fi
      - name: unit tests
        run: go test -race -short ./...
      - name: integration tests
        run: go test -race -run Integration ./...

  build:
    runs-on: ubuntu-latest
    needs: test
    steps:
      - uses: actions/checkout@v4
      - uses: docker/setup-buildx-action@v3
      - name: build api image
        run: docker build -f cmd/api/Dockerfile -t liusha/api:ci .
```

- [ ] **Step 2: Verify workflow syntax locally**

Run (optional, if `act` is installed):
```bash
act -n -j test
```

Or just visually inspect and push to exercise CI.

- [ ] **Step 3: Commit**

```bash
git add .github/workflows/ci.yml
git commit -m "ci: add github actions workflow for vet/tests/docker build"
```

---

## Task 16: Plan 1 acceptance gate

- [ ] **Step 1: Full local gate**

Run:
```bash
make down
make up
sleep 10
make migrate
make test
make build-api
```

Expected: all green, no failing tests, docker image builds.

- [ ] **Step 2: End-to-end smoke script**

Run (a single shell block):
```bash
docker compose -f deployments/docker-compose.yml up -d api
sleep 5
TENANT=55555555-5555-5555-5555-555555555555

# create
ID=$(curl -sf -X POST http://localhost:8080/api/v1/engagements \
  -H 'Content-Type: application/json' \
  -d "{\"tenant_id\":\"$TENANT\",\"mode\":\"proxy\",\"scope\":{\"hosts\":[\"example.com\"]},\"objective\":\"acceptance\"}" \
  | python3 -c 'import json,sys; print(json.load(sys.stdin)["id"])')
echo "created: $ID"

# get
curl -sf http://localhost:8080/api/v1/engagements/$ID | python3 -m json.tool

# list
curl -sf "http://localhost:8080/api/v1/engagements?tenant_id=$TENANT" | python3 -m json.tool

# archive (version=1)
curl -sf -X POST "http://localhost:8080/api/v1/engagements/$ID/archive?version=1" -o /dev/null -w '%{http_code}\n'

# stale archive should 409
curl -s -X POST "http://localhost:8080/api/v1/engagements/$ID/archive?version=1" -o /dev/null -w '%{http_code}\n'
```

Expected:
- `created: <uuid>`
- Get + List return matching engagement JSON
- First archive returns `204`
- Second archive returns `409`

- [ ] **Step 3: Acceptance checklist**

Verify all checked before calling Plan 1 done:

- [ ] `go.mod` declares `github.com/V3teran/liusha`; `go` directive is latest stable; `go mod tidy` clean; no dep pinned below its current latest stable without a comment explaining why
- [ ] `make up` brings up postgres + redis + asynqmon healthy
- [ ] `make migrate` applies 0001..0005 without errors
- [ ] `internal/config/config.go` fail-fasts on missing `LIUSHA_POSTGRES_DSN`
- [ ] `internal/logx` attaches `trace_id` to every log record and panics in dev if missing
- [ ] `TraceMiddleware` round-trips a W3C `traceparent` unchanged
- [ ] `engagement.New` validates mode, scope, tenant
- [ ] `engagement/store` has optimistic-lock archive path covered by integration test
- [ ] `httpapi` handlers: POST/GET/LIST/Archive green under testcontainers
- [ ] `cmd/api` boots via docker-compose and responds on `/healthz`
- [ ] GitHub Actions workflow covers vet / gofmt / tests / docker build
- [ ] No `pkg/`, no root `internal/store/`, no `roles.yaml`, no DTO suffixes — matches design §8

- [ ] **Step 4: Tag the milestone**

```bash
git tag v0.1.0-plan1
git log --oneline | head -20
```

Plan 1 complete. Plans 2–8 build on this foundation:
- Plan 2: Proxy container + Proxify integration + http_flow ingestion
- Plan 3: ~~mcp-server + vuln-tools:web image + MCP JSON-RPC~~ **[DEPRECATED 2026-04-20]** — 改为 `internal/tool/runner.DockerRunner`（代码层封装 docker SDK + 镜像/工具白名单 + 硬编码加固 HostConfig），已随 Plan 1 代码一并落地；`vuln-tools` 镜像构建（`docker/vuln-tools/Dockerfile`）保留，compose 里走 `profiles: ["build-only"]`
- Plan 4: Agent runtime (Eino + Asynq) + ReAct loop
- Plan 5: Phase 1 Roles (planner + web_recon + 5 hunters + chain_verifier)
- Plan 6: Chain Matcher + 3 patterns (UPLOAD_LFI_RCE, SSRF_METADATA_CREDS, JWT_NONE_PRIVESC)
- Plan 7: liusha-ui refactor (engagement/window/graph APIs, G6 attack graph)
- Plan 8: E2E verification + DLQ + budget guards + tag v1.0.0

---
