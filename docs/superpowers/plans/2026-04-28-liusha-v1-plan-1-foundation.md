# Liusha v1 Plan 1: Foundation 实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 建 v1 底座：Postgres schema（engagement memory 三层）、全部 domain store、通用 lib（credential/replay/heuristic）、AgentRuntime（ReAct + Observer + LoopDetector + DoneValidate + Budget + LLM Router/Instrument，5 个 provider 走 eino-ext 真实 adapter）、worker、httpapi、cmd 骨架。Plan 2 才装 proxify_consumer + sniffer + BAC skill 跑 e2e。

**Architecture:**
- 单 `AgentRuntime`：sniffer / operator / 子任务全跑同一 ReAct 循环
- LLM multi-provider：v1 实装 DeepSeek + Anthropic，OpenAI/Moonshot/Qwen stub
- Postgres 一次性 0001_init schema（8 表）+ Redis 热层（凭证 / 流量 stream）+ Asynq 任务队列
- Action 全显式注册（dependency injection），不藏依赖

**Tech Stack:** Go 1.25.6 / Postgres 17 (+ pgvector) / Redis 8 / Asynq / Gin / Eino LLM / zerolog / viper / golang-migrate / testcontainers-go.

**前置假设：** spec 见 `docs/superpowers/specs/2026-04-28-liusha-v1-design.md`。仓库当前为 clean-slate（仅 `go.mod`、`Makefile`、`config/`、`deployments/`、`docker/`、`docs/`、`README.md`、`vendor/`，无 `internal/` 与 `cmd/`）。

---

## 黑客松借鉴增量（2026-04-29 加入，详见 docs/hks2.md）

吸收 5 共识 + 4 创新（共 9 项），plan 1 范围内的修改清单：

| 编号 | 改动类型 | 具体内容 | 影响 Task |
|---|---|---|---|
| A | 替换 | Reflexion → Observer Sidecar；新增 LoopDetector | T23 |
| B | schema 拆分 | engagement.memory → memory_facts/ideas/hints 三字段 | T4（0001_init.sql）+ T6（engagement store） |
| C | 系统层校验 | done 调用前过 DoneValidator | T22（registry 注册） + T24（done action） |
| D | 中间件 | result_compress（>2KB 落盘+引用） | T22（middleware 链） |
| E | 中间件 | LoopDetector 中间件 + retry（429/529 + fallback） | T21（instrument 链）+ T23 |
| 6 | 配置 | config.yaml 加 llm.light_provider / fallback_provider / routes | T1 |
| 7 | 模板 | docs/skills/_template/cognitive_map.md | T25（skill loader 校验 6 槽位） |
| 8 | 系统流程 | finding 写库后异步触发 distill | T11（finding store 暴露 hook）+ T24（write_finding action 调 hook） |
| 11 | 路由 | LLM Router（default/light/fallback 三组路由） | 新 Task 21.5（Router 装饰器） + T19（factory 暴露 For(role) 方法） |

**新增 Task**：
- **T21.5 LLM Router**（在 part 3，紧跟 T21 instrument）：装饰 Generator → For(role) 路由 + retry+fallback；签到读 spec §8.4/§8.5。
- **T22.5 Action 中间件链**（在 part 3，紧跟 T22 registry）：result_compress + loop_detect + done_validate 三层中间件，按序套到 ActionRegistry.Execute。
- **T23.5 Observer + Distill**（在 part 3，紧跟 T23 runtime）：observer.go（每 N 步触发 light LLM 评估）+ distill.go（finding 命中后异步 light LLM 总结写 memory_hints）。

**修改清单逐 Task 见各 part 文件顶部"增量章节"。**

---

## 任务依赖

```
T1 skeleton + .env.example
  ↓
T2 logx ── T3 config
  ↓
T4 0001_init.sql ── T5 internal/db connect
  ↓
T6 engagement store
T7 window store
T8 task store
T9 graph store
T10 flow store
T11 finding store (UNIQUE + ON CONFLICT)
T12 llm_call store
  ↓
T13 credential (Redis Provider)
T14 replay engine
T15 heuristic (rules + similarity)
  ↓
T17 llm Generator iface + DeepSeek adapter（eino-ext）
T18 llm Claude adapter（eino-ext/components/model/claude）
T19 llm factory（5 provider 全 eino-ext 实装，Moonshot 走 openai adapter）
T20 observability/pricing 表
T21 llm Instrument 装饰器
T21.5 llm Router (default/light/fallback + retry 429/529)  ← 新增（创新 11）
  ↓
T22 ActionRegistry
T22.5 Action 中间件链 (result_compress + loop_detect + done_validate)  ← 新增（共识 D/E）
T23 AgentRuntime (ReAct + Budget；Reflexion 删，由 T23.5 Observer 替代)
T23.5 Observer + Distill (light LLM 评估 + finding 后蒸馏写 hint)  ← 新增（共识 A + 创新 8）
T24 通用 actions (read_state + write_fact/idea/hint + write_finding + write_graph + load_skill + spawn_subtask + done(系统校验))
  ↓
T25 skill loader + Card（含 cognitive_map 6 槽位校验 + done_validator 注册校验）
T26 worker (Asynq enqueue/handler 骨架)
T27 httpapi (auth + healthz + credential + abort)
  ↓
T28 cmd/api main
T30 cmd/agent-worker main（role 分发，仅注册 sniffer；proxify_consumer 在 plan 2 装）
T31 cmd/vulnapp（BAC e2e 靶机，端口 8001 + zerolog）
T32 compose smoke：build all + healthz + go test ./... 全绿

注：T16（proxy_filter）和 T29（cmd/proxy）已删除——流量过滤交给 proxify DSL，proxy 实体替换为 proxify 容器（plan 2 装 consumer）。
```

---

## Task 1: 项目骨架 + 全套配置文件（.env.example / Makefile / config.yaml）

**Files:**
- Create: `.env.example`（仓库已 git rm，本任务从零写）
- Create: `Makefile`（仓库已 git rm，本任务从零写）
- Create: `config/config.yaml`（仓库已 git rm，本任务从零写）
- Create: `internal/.gitkeep`
- Create: `cmd/.gitkeep`
- Modify: `go.mod`（清理 v1 不用的死依赖）

> 仓库前置 commit 已删掉这 4 个旧配置文件（含 v0 / v1.5 杂质），本任务**从 v1 设计干净写一遍，绝不复用旧文件**。

- [ ] **Step 1: 写 `.env.example`**

```bash
# ============================================================================
# Liusha v1 环境变量模板
#   - 复制为 .env.local（本机）或 .env.docker（compose）并填入真实 key
#   - .env.local / .env.*.local 已在 .gitignore，绝不提交
# ============================================================================

# ---- 基础 ----
LIUSHA_POSTGRES_DSN=postgres://liusha:liusha@localhost:5432/liusha?sslmode=disable
LIUSHA_REDIS_ADDR=localhost:6379
LIUSHA_API_ADDR=0.0.0.0:8080
LIUSHA_API_KEY=changeme-dev-key
LIUSHA_ENV=development
LIUSHA_LOG_LEVEL=info
LIUSHA_CONFIG=./config/config.yaml

# ---- LLM 默认路由 ----
LIUSHA_LLM_DEFAULT_PROVIDER=deepseek
LIUSHA_LLM_VISION_PROVIDER=anthropic

# ---- Provider API Keys（按需填，不用的留空）----
DEEPSEEK_API_KEY=sk-your-deepseek-key
ANTHROPIC_API_KEY=sk-ant-your-anthropic-key
OPENAI_API_KEY=
MOONSHOT_API_KEY=
QWEN_API_KEY=

# ---- proxify_consumer 读取 JSONL 路径（容器内挂共享 volume）----
LIUSHA_PROXIFY_JSONL=/data/flows.jsonl
```

- [ ] **Step 2: 写 `Makefile`**

```makefile
.PHONY: up down logs migrate migrate-down run-api build-api build-agent-worker build-vulnapp test test-unit test-integration lint fmt tidy vet e2e-bac

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
		-path db/migrations -database '$(MIGRATE_DSN)' up

migrate-down:
	go run -tags 'postgres' github.com/golang-migrate/migrate/v4/cmd/migrate \
		-path db/migrations -database '$(MIGRATE_DSN)' down 1

run-api:
	go run ./cmd/api

build-api:
	docker build -f cmd/api/Dockerfile -t liusha/api .

build-agent-worker:
	docker build -f cmd/agent-worker/Dockerfile -t liusha/agent-worker .

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
```

- [ ] **Step 3: 写 `config/config.yaml`**

```yaml
# ============================================================================
# Liusha v1 主配置
#   - 仅放非敏感、可入库的开关与默认值
#   - 所有 *_API_KEY 走 env，本文件不出现密钥
#   - env 用 LIUSHA_ 前缀，支持二级覆盖
#     例 LIUSHA_LLM_DEFAULT_PROVIDER 覆盖 llm.default_provider
# ============================================================================

api:
  read_timeout_seconds: 15
  write_timeout_seconds: 30

postgres:
  max_conns: 20
  min_conns: 2

# ---- LLM 多 provider 路由（黑客松借鉴：创新 11）----
# default_provider:  主 ReAct（sniffer / 子任务）共用，重模型
# light_provider:    Observer / Compaction / Distill 走的轻模型，省成本
# vision_provider:   v1.5 的 browser.screenshot 用，v1 仅作为备用主 LLM
# fallback_provider: 429/529 退避耗尽后切到，retry 中间件用
# routes:            按角色路由到 provider；未列角色用 default_provider
llm:
  default_provider: deepseek
  light_provider: anthropic       # 实装 anthropic 时把 model 切到 claude-haiku-4-5
  vision_provider: anthropic
  fallback_provider: openai
  max_steps: 30                 # ReAct 单任务步数上限，被 Role.Budget 覆盖
  max_tokens_per_call: 4096
  routes:
    react.main: default_provider
    observer:   light_provider
    compaction: light_provider
    distill:    light_provider
    vision:     vision_provider

# 每个 provider 一节；只填要用的，其它留空即可
# api_key_env 是从 env 读 key 的变量名
providers:
  deepseek:
    base_url: https://api.deepseek.com
    default_model: deepseek-chat
    api_key_env: DEEPSEEK_API_KEY
    max_tokens: 4096
    supports_tools: true
    supports_vision: false

  anthropic:
    base_url: https://api.anthropic.com
    default_model: claude-sonnet-4-6
    vision_model: claude-haiku-4-5
    api_key_env: ANTHROPIC_API_KEY
    max_tokens: 8192
    supports_tools: true
    supports_vision: true

  openai:
    base_url: https://api.openai.com/v1
    default_model: gpt-4o
    api_key_env: OPENAI_API_KEY
    max_tokens: 4096
    supports_tools: true
    supports_vision: true

  moonshot:
    base_url: https://api.moonshot.cn/v1
    default_model: kimi-k2-0905-preview
    api_key_env: MOONSHOT_API_KEY
    max_tokens: 4096
    supports_tools: true
    supports_vision: false

  qwen:
    base_url: https://dashscope.aliyuncs.com/compatible-mode/v1
    default_model: qwen3-max
    api_key_env: QWEN_API_KEY
    max_tokens: 4096
    supports_tools: true
    supports_vision: false

# ---- proxify_consumer 切片参数 ----
# proxify 容器自己处理 listen + filter，liusha 这边只切窗
proxy:
  window_batch: 20              # TrafficWindow 批大小上限
  window_max_age_seconds: 30    # 窗口存活上限
  allow_hosts:
    - vulnapp                   # v1 e2e 靶机
    - host.docker.internal      # 让 BAC replay 能从 agent-worker 出去打宿主机

# ---- engagement 懒创建 + 24h 归档 ----
engagement:
  idle_timeout_hours: 24
  sweeper_interval_seconds: 600

# ---- skills 外部目录 ----
skills:
  root: ./skills
```

> 不再写 `browser:` / `docker_runner:` / `proxy.addr` / `proxy.flow_file` / `proxy.batch_size` 等 v1 用不到的键，避免代码读到误导值。v1.5 加 SQLi / browser 时再扩。

- [ ] **Step 4: 占位目录**

```bash
mkdir -p internal cmd
touch internal/.gitkeep cmd/.gitkeep
```

- [ ] **Step 5: 清理 go.mod 死依赖**

```bash
go mod edit -droprequire github.com/docker/docker
go mod edit -droprequire github.com/moby/moby/client
go mod edit -droprequire github.com/opencontainers/image-spec
go mod edit -droprequire github.com/pgvector/pgvector-go
go mod tidy
go mod vendor
```

- [ ] **Step 6: 验证**

```bash
go vet ./... 2>&1 | grep -v "no Go files" || true
test "$(grep -c 'flow_file\|batch_size\|flush_seconds\|browser:\|docker_runner:' config/config.yaml)" -eq 0
```

预期：无报错；grep 检查返回 0（确认配置已彻底清干净）。

- [ ] **Step 7: Commit**

```bash
git add .env.example Makefile config/config.yaml internal/.gitkeep cmd/.gitkeep go.mod go.sum vendor
git commit -m "chore(skeleton): 重写 .env.example + Makefile + config.yaml（v1 干净版，无 v0/v1.5 残留）"
```

---

## Task 2: internal/logx — zerolog 包装

**Files:**
- Create: `internal/logx/logger.go`
- Create: `internal/logx/logger_test.go`

- [ ] **Step 1: 写失败测试 `internal/logx/logger_test.go`**

```go
package logx

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestNew_JSONIncludesComponent(t *testing.T) {
	var buf bytes.Buffer
	l := newWith(&buf, "json").With().Str("component", "test_pkg").Logger()
	l.Info().Str("k", "v").Msg("hello")

	var got map[string]any
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("expect JSON, got %q: %v", buf.String(), err)
	}
	if got["component"] != "test_pkg" || got["message"] != "hello" || got["k"] != "v" {
		t.Fatalf("missing fields: %v", got)
	}
}

func TestNew_DevelopmentIsConsole(t *testing.T) {
	var buf bytes.Buffer
	l := newWith(&buf, "development")
	l.Info().Msg("plain")
	if !strings.Contains(buf.String(), "plain") {
		t.Fatalf("console writer should contain message verbatim, got %q", buf.String())
	}
	if json.Valid(buf.Bytes()) {
		t.Fatalf("development writer should not be JSON, got %q", buf.String())
	}
}
```

- [ ] **Step 2: 跑测试预期失败**

```bash
go test ./internal/logx/...
```

预期：`undefined: newWith`。

- [ ] **Step 3: 实现 `internal/logx/logger.go`**

```go
package logx

import (
	"io"
	"os"
	"strings"

	"github.com/rs/zerolog"
)

func New(component string) zerolog.Logger {
	env := strings.ToLower(os.Getenv("LIUSHA_ENV"))
	return newWith(os.Stdout, env).With().Str("component", component).Logger()
}

func newWith(w io.Writer, env string) zerolog.Logger {
	level := parseLevel(os.Getenv("LIUSHA_LOG_LEVEL"))
	zerolog.SetGlobalLevel(level)
	zerolog.MessageFieldName = "message"
	if env == "development" {
		return zerolog.New(zerolog.ConsoleWriter{Out: w, TimeFormat: "15:04:05"}).
			With().Timestamp().Logger()
	}
	return zerolog.New(w).With().Timestamp().Logger()
}

func parseLevel(s string) zerolog.Level {
	switch strings.ToLower(s) {
	case "debug":
		return zerolog.DebugLevel
	case "warn":
		return zerolog.WarnLevel
	case "error":
		return zerolog.ErrorLevel
	default:
		return zerolog.InfoLevel
	}
}
```

- [ ] **Step 4: 跑测试预期通过**

```bash
go test ./internal/logx/... -race
```

- [ ] **Step 5: Commit**

```bash
git add internal/logx
git commit -m "feat(logx): zerolog 工厂（dev=console, prod=JSON），按 component 分子 logger"
```

---

## Task 3: internal/config — viper 加载 + 校验

**Files:**
- Create: `internal/config/config.go`
- Create: `internal/config/config_test.go`

- [ ] **Step 1: 写失败测试**

```go
package config

import (
	"os"
	"path/filepath"
	"testing"
)

const minimalYAML = `
api: {read_timeout_seconds: 15, write_timeout_seconds: 30}
postgres: {max_conns: 20, min_conns: 2}
llm:
  default_provider: deepseek
  vision_provider: anthropic
  max_steps: 30
  max_tokens_per_call: 4096
providers:
  deepseek:  {base_url: https://api.deepseek.com,  default_model: deepseek-chat,    api_key_env: DEEPSEEK_API_KEY,  max_tokens: 4096, supports_tools: true,  supports_vision: false}
  anthropic: {base_url: https://api.anthropic.com, default_model: claude-sonnet-4-6, vision_model: claude-haiku-4-5, api_key_env: ANTHROPIC_API_KEY, max_tokens: 8192, supports_tools: true, supports_vision: true}
proxy: {window_batch: 20, window_max_age_seconds: 30, allow_hosts: [vulnapp]}
engagement: {idle_timeout_hours: 24, sweeper_interval_seconds: 600}
skills: {root: ./skills}
`

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoad_MissingDefaultProviderKey(t *testing.T) {
	t.Setenv("DEEPSEEK_API_KEY", "")
	t.Setenv("ANTHROPIC_API_KEY", "k-anth")
	if _, err := Load(writeConfig(t, minimalYAML)); err == nil {
		t.Fatalf("expected error when DEEPSEEK_API_KEY empty")
	}
}

func TestLoad_OK(t *testing.T) {
	t.Setenv("DEEPSEEK_API_KEY", "k-deep")
	t.Setenv("ANTHROPIC_API_KEY", "k-anth")
	cfg, err := Load(writeConfig(t, minimalYAML))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.LLM.DefaultProvider != "deepseek" || cfg.Providers["deepseek"].DefaultModel != "deepseek-chat" {
		t.Fatalf("unexpected: %+v", cfg)
	}
}
```

- [ ] **Step 2: 跑测试预期失败**

```bash
go test ./internal/config/...
```

- [ ] **Step 3: 实现 `internal/config/config.go`**

```go
package config

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/viper"
)

type Config struct {
	API        APIConfig                 `mapstructure:"api"`
	Postgres   PostgresConfig            `mapstructure:"postgres"`
	LLM        LLMConfig                 `mapstructure:"llm"`
	Providers  map[string]ProviderConfig `mapstructure:"providers"`
	Proxy      ProxyConfig               `mapstructure:"proxy"`
	Engagement EngagementConfig          `mapstructure:"engagement"`
	Skills     SkillsConfig              `mapstructure:"skills"`
}

type APIConfig struct {
	ReadTimeoutSeconds  int `mapstructure:"read_timeout_seconds"`
	WriteTimeoutSeconds int `mapstructure:"write_timeout_seconds"`
}
type PostgresConfig struct {
	MaxConns int `mapstructure:"max_conns"`
	MinConns int `mapstructure:"min_conns"`
}
type LLMConfig struct {
	DefaultProvider  string `mapstructure:"default_provider"`
	VisionProvider   string `mapstructure:"vision_provider"`
	MaxSteps         int    `mapstructure:"max_steps"`
	MaxTokensPerCall int    `mapstructure:"max_tokens_per_call"`
}
type ProviderConfig struct {
	BaseURL        string `mapstructure:"base_url"`
	DefaultModel   string `mapstructure:"default_model"`
	VisionModel    string `mapstructure:"vision_model"`
	APIKeyEnv      string `mapstructure:"api_key_env"`
	MaxTokens      int    `mapstructure:"max_tokens"`
	SupportsTools  bool   `mapstructure:"supports_tools"`
	SupportsVision bool   `mapstructure:"supports_vision"`
}
// ProxyConfig 仅含 proxify_consumer 关心的字段。
// proxify 自身的 listen / DSL filter 在 docker-compose 里配，不入代码。
type ProxyConfig struct {
	WindowBatch         int      `mapstructure:"window_batch"`
	WindowMaxAgeSeconds int      `mapstructure:"window_max_age_seconds"`
	AllowHosts          []string `mapstructure:"allow_hosts"`
}
type EngagementConfig struct {
	IdleTimeoutHours       int `mapstructure:"idle_timeout_hours"`
	SweeperIntervalSeconds int `mapstructure:"sweeper_interval_seconds"`
}
type SkillsConfig struct {
	Root string `mapstructure:"root"`
}

func Load(path string) (Config, error) {
	v := viper.New()
	v.SetConfigFile(path)
	v.SetEnvPrefix("LIUSHA")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()
	if err := v.ReadInConfig(); err != nil {
		return Config{}, fmt.Errorf("read config %s: %w", path, err)
	}
	var c Config
	if err := v.Unmarshal(&c); err != nil {
		return Config{}, fmt.Errorf("unmarshal config: %w", err)
	}
	if err := validate(c); err != nil {
		return Config{}, err
	}
	return c, nil
}

func validate(c Config) error {
	check := func(name, role string) error {
		p, ok := c.Providers[name]
		if !ok {
			return fmt.Errorf("%s %q not in providers", role, name)
		}
		if p.APIKeyEnv == "" || os.Getenv(p.APIKeyEnv) == "" {
			return fmt.Errorf("env %s empty (required for %s=%s)", p.APIKeyEnv, role, name)
		}
		return nil
	}
	if c.LLM.DefaultProvider == "" {
		return fmt.Errorf("llm.default_provider required")
	}
	if err := check(c.LLM.DefaultProvider, "default_provider"); err != nil {
		return err
	}
	if c.LLM.VisionProvider != "" {
		return check(c.LLM.VisionProvider, "vision_provider")
	}
	return nil
}
```

- [ ] **Step 4: 跑测试预期通过**

```bash
go test ./internal/config/... -race
```

- [ ] **Step 5: Commit**

```bash
git add internal/config
git commit -m "feat(config): viper 加载 + ENV 覆盖 + provider key 校验"
```

---

## Task 4: db/migrations/0001_init.sql — 一次性建表

**Files:**
- Create: `db/migrations/0001_init.up.sql`
- Create: `db/migrations/0001_init.down.sql`

- [ ] **Step 1: 写 `db/migrations/0001_init.up.sql`**

```sql
CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE TABLE engagement (
    id              uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id       text        NOT NULL DEFAULT 'default',
    mode            text        NOT NULL CHECK (mode IN ('proxy','browser')),
    scope_host      text        NOT NULL,
    status          text        NOT NULL CHECK (status IN ('active','aborted','archived')),
    memory_facts    jsonb       NOT NULL DEFAULT '{}'::jsonb,  -- {evidence:[], boundaries:[]}
    memory_ideas    jsonb       NOT NULL DEFAULT '{}'::jsonb,  -- {hypotheses:[{direction,status,ts}]}
    memory_hints    jsonb       NOT NULL DEFAULT '{}'::jsonb,  -- {hints:[{from_skill,content,priority,ts}]}
    created_at      timestamptz NOT NULL DEFAULT now(),
    last_activity_at timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX engagement_active_uniq
    ON engagement (tenant_id, scope_host)
    WHERE status = 'active';

CREATE TABLE traffic_window (
    id            uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    engagement_id uuid        NOT NULL REFERENCES engagement(id) ON DELETE CASCADE,
    flows         jsonb       NOT NULL DEFAULT '[]'::jsonb,
    status        text        NOT NULL CHECK (status IN ('open','closed','consumed')),
    started_at    timestamptz NOT NULL DEFAULT now(),
    closed_at     timestamptz
);
CREATE INDEX traffic_window_engagement_status_idx
    ON traffic_window (engagement_id, status, started_at);

CREATE TABLE agent_task (
    id              uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    engagement_id   uuid        NOT NULL REFERENCES engagement(id) ON DELETE CASCADE,
    parent_task_id  uuid        REFERENCES agent_task(id) ON DELETE SET NULL,
    role            text        NOT NULL,
    skill           text        NOT NULL DEFAULT '',
    input           jsonb       NOT NULL DEFAULT '{}'::jsonb,
    budget          jsonb       NOT NULL DEFAULT '{}'::jsonb,
    result          jsonb       NOT NULL DEFAULT '{}'::jsonb,
    status          text        NOT NULL DEFAULT 'pending'
                    CHECK (status IN ('pending','running','done','aborted','error')),
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX agent_task_engagement_idx ON agent_task (engagement_id, created_at);

CREATE TABLE graph_node (
    id            uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    engagement_id uuid        NOT NULL REFERENCES engagement(id) ON DELETE CASCADE,
    kind          text        NOT NULL,
    payload       jsonb       NOT NULL DEFAULT '{}'::jsonb,
    dedup_key     text        NOT NULL,
    created_at    timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX graph_node_uniq ON graph_node (engagement_id, kind, dedup_key);

CREATE TABLE graph_edge (
    id            uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    engagement_id uuid        NOT NULL REFERENCES engagement(id) ON DELETE CASCADE,
    from_id       uuid        NOT NULL REFERENCES graph_node(id) ON DELETE CASCADE,
    to_id         uuid        NOT NULL REFERENCES graph_node(id) ON DELETE CASCADE,
    kind          text        NOT NULL,
    payload       jsonb       NOT NULL DEFAULT '{}'::jsonb,
    created_at    timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX graph_edge_uniq ON graph_edge (engagement_id, from_id, to_id, kind);

CREATE TABLE finding (
    id            uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    engagement_id uuid        NOT NULL REFERENCES engagement(id) ON DELETE CASCADE,
    task_id       uuid        REFERENCES agent_task(id) ON DELETE SET NULL,
    kind          text        NOT NULL,
    severity      text        NOT NULL CHECK (severity IN ('info','low','medium','high','critical')),
    title         text        NOT NULL,
    target        jsonb       NOT NULL DEFAULT '{}'::jsonb,
    evidence      jsonb       NOT NULL DEFAULT '{}'::jsonb,
    payload       jsonb       NOT NULL DEFAULT '{}'::jsonb,
    tool          text        NOT NULL DEFAULT '',
    confidence    text        NOT NULL DEFAULT 'unverified'
                  CHECK (confidence IN ('unverified','verified','rejected')),
    dedup_key     text        NOT NULL,
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX finding_uniq ON finding (engagement_id, dedup_key);

CREATE TABLE http_flow (
    id              bigserial   PRIMARY KEY,
    engagement_id   uuid        NOT NULL REFERENCES engagement(id) ON DELETE CASCADE,
    ts              timestamptz NOT NULL DEFAULT now(),
    method          text        NOT NULL,
    url             text        NOT NULL,
    request_headers jsonb       NOT NULL DEFAULT '{}'::jsonb,
    request_body    bytea,
    request_truncated boolean   NOT NULL DEFAULT false,
    status_code     int,
    response_headers jsonb      NOT NULL DEFAULT '{}'::jsonb,
    response_body   bytea,
    response_truncated boolean  NOT NULL DEFAULT false
);
CREATE INDEX http_flow_engagement_ts_idx ON http_flow (engagement_id, ts);

CREATE TABLE llm_call (
    id            bigserial   PRIMARY KEY,
    task_id       uuid        REFERENCES agent_task(id) ON DELETE SET NULL,
    engagement_id uuid        REFERENCES engagement(id) ON DELETE SET NULL,
    provider      text        NOT NULL,
    model         text        NOT NULL,
    in_tokens     int         NOT NULL DEFAULT 0,
    out_tokens    int         NOT NULL DEFAULT 0,
    cached_tokens int         NOT NULL DEFAULT 0,
    cost_usd      numeric(12,6) NOT NULL DEFAULT 0,
    latency_ms    int         NOT NULL DEFAULT 0,
    finish_reason text        NOT NULL DEFAULT '',
    error         text        NOT NULL DEFAULT '',
    created_at    timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX llm_call_task_idx ON llm_call (task_id);
CREATE INDEX llm_call_engagement_idx ON llm_call (engagement_id, created_at);
```

- [ ] **Step 2: 写 `db/migrations/0001_init.down.sql`**

```sql
DROP TABLE IF EXISTS llm_call;
DROP TABLE IF EXISTS http_flow;
DROP TABLE IF EXISTS finding;
DROP TABLE IF EXISTS graph_edge;
DROP TABLE IF EXISTS graph_node;
DROP TABLE IF EXISTS agent_task;
DROP TABLE IF EXISTS traffic_window;
DROP TABLE IF EXISTS engagement;
```

- [ ] **Step 3: 起 postgres 跑 migrate**

```bash
make up
make migrate
docker exec -i liusha-postgres psql -U liusha -d liusha -c '\dt'
```

预期：列出 8 张业务表 + `schema_migrations`。

- [ ] **Step 4: down + up 验证幂等**

```bash
make migrate-down
make migrate
```

- [ ] **Step 5: Commit**

```bash
git add db/migrations
git commit -m "feat(db): 0001_init schema（8 表 + UNIQUE 约束）"
```

---

## Task 5: internal/db — pgxpool + redis client 工厂

**Files:**
- Create: `internal/db/pg.go`
- Create: `internal/db/redis.go`
- Create: `internal/db/db_integration_test.go`

- [ ] **Step 1: 写集成测试 `internal/db/db_integration_test.go`**

```go
//go:build integration

package db

import (
	"context"
	"strings"
	"testing"
	"time"

	tcredis "github.com/testcontainers/testcontainers-go/modules/redis"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
)

func TestNewPgPool_Ping(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	c, err := tcpostgres.Run(ctx, "pgvector/pgvector:pg17",
		tcpostgres.WithDatabase("liusha"),
		tcpostgres.WithUsername("liusha"),
		tcpostgres.WithPassword("liusha"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Terminate(ctx) })
	dsn, err := c.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	pool, err := NewPgPool(ctx, dsn, 5, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err := pool.Ping(ctx); err != nil {
		t.Fatalf("ping: %v", err)
	}
}

func TestNewRedis_Ping(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	c, err := tcredis.Run(ctx, "redis:8")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Terminate(ctx) })
	addr, err := c.ConnectionString(ctx)
	if err != nil {
		t.Fatal(err)
	}
	r, err := NewRedis(ctx, strings.TrimPrefix(addr, "redis://"))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if err := r.Ping(ctx).Err(); err != nil {
		t.Fatalf("ping: %v", err)
	}
}
```

- [ ] **Step 2: 实现 `internal/db/pg.go`**

```go
package db

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

func NewPgPool(ctx context.Context, dsn string, maxConns, minConns int) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse pg dsn: %w", err)
	}
	if maxConns > 0 {
		cfg.MaxConns = int32(maxConns)
	}
	if minConns >= 0 {
		cfg.MinConns = int32(minConns)
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("new pgxpool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping pg: %w", err)
	}
	return pool, nil
}
```

- [ ] **Step 3: 实现 `internal/db/redis.go`**

```go
package db

import (
	"context"
	"fmt"

	"github.com/redis/go-redis/v9"
)

func NewRedis(ctx context.Context, addr string) (*redis.Client, error) {
	c := redis.NewClient(&redis.Options{Addr: addr})
	if err := c.Ping(ctx).Err(); err != nil {
		_ = c.Close()
		return nil, fmt.Errorf("ping redis %s: %w", addr, err)
	}
	return c, nil
}
```

- [ ] **Step 4: 跑集成测试**

```bash
go test -tags=integration ./internal/db/... -race -count=1
```

预期：两用例 PASS（首次拉镜像 ~30s）。

- [ ] **Step 5: Commit**

```bash
git add internal/db
git commit -m "feat(db): pgxpool + redis client 工厂 + testcontainers 集成测试"
```

---

> Plan 1 后续任务（T6-T32）见同目录 `2026-04-28-liusha-v1-plan-1-foundation-part2.md`。
