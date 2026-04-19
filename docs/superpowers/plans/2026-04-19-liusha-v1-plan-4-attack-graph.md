# Liusha v1 — Plan 4/8: Attack Graph

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 实现 Attack Graph 双层存储（近 7 天节点 Redis 热缓存 + Postgres 冷存），以及 AnalysisWindow / AgentTask 的 Postgres 持久化层，为 Agent Worker 提供完整的图操作接口。

**Architecture:**
`internal/graph/model/` 定义 Node/Edge 类型（5 种节点、4 种边）；`internal/graph/store/` 实现 Postgres 冷存（pgx CopyFrom 批量写）+ Redis 热缓存（Hash + 7d TTL）；`internal/window/` 实现 AnalysisWindow 和 AgentTask 的 CRUD；数据库迁移新增 `graph_node`、`graph_edge`、`analysis_window`、`agent_task` 四张表。

**Tech Stack（全部最新稳定版）：**
- `github.com/jackc/pgx/v5`（已有）
- `github.com/redis/go-redis/v9`（已有）
- `github.com/google/uuid`（已有）
- `github.com/testcontainers/testcontainers-go`（已有，集成测试）

**Spec Reference:** §2.1（生命周期），§2.2（Attack Graph），§7.1（Postgres Schema），§7.2（Redis Keyspace）。

---

## File Structure

```
liusha/
├── db/migrations/
│   ├── 0006_analysis_window.up.sql      # Task 1
│   ├── 0006_analysis_window.down.sql    # Task 1
│   ├── 0007_agent_task.up.sql           # Task 1
│   ├── 0007_agent_task.down.sql         # Task 1
│   ├── 0008_graph.up.sql                # Task 1
│   └── 0008_graph.down.sql             # Task 1
├── internal/
│   ├── graph/
│   │   ├── model/
│   │   │   ├── node.go                  # Task 2
│   │   │   └── edge.go                  # Task 2
│   │   └── store/
│   │       ├── postgres.go              # Task 3
│   │       ├── postgres_test.go         # Task 3
│   │       ├── redis.go                 # Task 4
│   │       └── redis_test.go            # Task 4
│   └── window/
│       ├── model.go                     # Task 5
│       └── store/
│           ├── postgres.go              # Task 5
│           └── postgres_test.go         # Task 5
```

---

## Task 1: 数据库迁移

**Files:**
- Create: `db/migrations/0006_analysis_window.up.sql`
- Create: `db/migrations/0006_analysis_window.down.sql`
- Create: `db/migrations/0007_agent_task.up.sql`
- Create: `db/migrations/0007_agent_task.down.sql`
- Create: `db/migrations/0008_graph.up.sql`
- Create: `db/migrations/0008_graph.down.sql`

- [ ] **Step 1: 创建迁移文件**

`db/migrations/0006_analysis_window.up.sql`:
```sql
CREATE TABLE analysis_window (
    id             uuid PRIMARY KEY,
    engagement_id  uuid NOT NULL REFERENCES engagement(id),
    trigger_kind   text NOT NULL
                   CHECK (trigger_kind IN ('time','flow_count','manual')),
    flow_count     int,
    started_at     timestamptz NOT NULL DEFAULT now(),
    ended_at       timestamptz
);
CREATE INDEX ON analysis_window (engagement_id, started_at DESC);
```

`db/migrations/0006_analysis_window.down.sql`:
```sql
DROP TABLE IF EXISTS analysis_window;
```

`db/migrations/0007_agent_task.up.sql`:
```sql
CREATE TABLE agent_task (
    id               uuid PRIMARY KEY,
    window_id        uuid NOT NULL REFERENCES analysis_window(id),
    role             text NOT NULL,
    status           text NOT NULL DEFAULT 'pending'
                     CHECK (status IN ('pending','running','succeeded','failed','cancelled')),
    input_node_ids   uuid[],
    output_node_ids  uuid[],
    cost_micro_usd   bigint NOT NULL DEFAULT 0,
    started_at       timestamptz,
    ended_at         timestamptz
);
CREATE INDEX ON agent_task (window_id, status);
```

`db/migrations/0007_agent_task.down.sql`:
```sql
DROP TABLE IF EXISTS agent_task;
```

`db/migrations/0008_graph.up.sql`:
```sql
CREATE TABLE graph_node (
    id              uuid PRIMARY KEY,
    engagement_id   uuid NOT NULL REFERENCES engagement(id),
    kind            text NOT NULL
                    CHECK (kind IN ('surface','hypothesis','finding','exploit_chain','evidence')),
    payload         jsonb NOT NULL,
    vector          vector(1536),
    embedding_model text,
    dedup_key       text,
    created_at      timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX ON graph_node (engagement_id, kind, created_at DESC);
CREATE INDEX ON graph_node USING hnsw (vector vector_cosine_ops)
    WHERE vector IS NOT NULL;
CREATE UNIQUE INDEX ON graph_node (engagement_id, dedup_key)
    WHERE dedup_key IS NOT NULL;

CREATE TABLE graph_edge (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    engagement_id uuid NOT NULL,
    src_id        uuid NOT NULL REFERENCES graph_node(id),
    dst_id        uuid NOT NULL REFERENCES graph_node(id),
    kind          text NOT NULL
                  CHECK (kind IN ('finding','chains_to','evidences','enables')),
    created_at    timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX ON graph_edge (engagement_id, src_id, kind);
CREATE INDEX ON graph_edge (engagement_id, dst_id, kind);
```

`db/migrations/0008_graph.down.sql`:
```sql
DROP TABLE IF EXISTS graph_edge;
DROP TABLE IF EXISTS graph_node;
```

- [ ] **Step 2: 提交**

```bash
git add db/migrations/
git commit -m "feat(db): add analysis_window, agent_task, graph_node, graph_edge migrations"
```

---

## Task 2: Graph 模型

**Files:**
- Create: `internal/graph/model/node.go`
- Create: `internal/graph/model/edge.go`

- [ ] **Step 1: 写失败测试（编译检查）**

```bash
# 确认包不存在
ls internal/graph/ 2>&1
```
期望：`No such file or directory`

- [ ] **Step 2: 实现 node.go**

```go
// internal/graph/model/node.go
package model

import (
    "time"
    "github.com/google/uuid"
)

type NodeKind string

const (
    KindSurface      NodeKind = "surface"
    KindHypothesis   NodeKind = "hypothesis"
    KindFinding      NodeKind = "finding"
    KindExploitChain NodeKind = "exploit_chain"
    KindEvidence     NodeKind = "evidence"
)

type Node struct {
    ID             uuid.UUID
    EngagementID   uuid.UUID
    Kind           NodeKind
    Payload        map[string]any
    Vector         []float32 // nil 表示尚未嵌入
    EmbeddingModel string
    DedupKey       string
    CreatedAt      time.Time
}
```

- [ ] **Step 3: 实现 edge.go**

```go
// internal/graph/model/edge.go
package model

import (
    "time"
    "github.com/google/uuid"
)

type EdgeKind string

const (
    EdgeFinding    EdgeKind = "finding"
    EdgeChainsTo   EdgeKind = "chains_to"
    EdgeEvidences  EdgeKind = "evidences"
    EdgeEnables    EdgeKind = "enables"
)

type Edge struct {
    ID           uuid.UUID
    EngagementID uuid.UUID
    SrcID        uuid.UUID
    DstID        uuid.UUID
    Kind         EdgeKind
    CreatedAt    time.Time
}
```

- [ ] **Step 4: 编译确认**

```bash
go build ./internal/graph/...
```
期望：无输出

- [ ] **Step 5: 提交**

```bash
git add internal/graph/
git commit -m "feat(graph): Node and Edge model types"
```

---

## Task 3: Graph Postgres 存储

**Files:**
- Create: `internal/graph/store/postgres.go`
- Create: `internal/graph/store/postgres_test.go`

接口：`AddNode`、`AddEdge`、`GetNode`、`ListNodes`（按 engagement + kind 过滤）、`ListEdges`（按 src/dst 过滤）、`UpsertNode`（dedup_key 唯一索引冲突时忽略）。

- [ ] **Step 1: 写失败测试**

```go
// internal/graph/store/postgres_test.go
package store_test

import (
    "context"
    "testing"

    "github.com/google/uuid"
    "github.com/jackc/pgx/v5/pgxpool"
    "github.com/stretchr/testify/assert"
    "github.com/stretchr/testify/require"
    "github.com/testcontainers/testcontainers-go/modules/postgres"
    tc "github.com/testcontainers/testcontainers-go"

    graphmodel "github.com/V3teran/liusha/internal/graph/model"
    "github.com/V3teran/liusha/internal/graph/store"
)

func TestIntegration_GraphStore_AddAndGet(t *testing.T) {
    if testing.Short() {
        t.Skip("跳过集成测试")
    }
    ctx := context.Background()

    pgc, err := postgres.Run(ctx,
        "pgvector/pgvector:pg17",
        postgres.WithDatabase("liusha"),
        postgres.WithUsername("liusha"),
        postgres.WithPassword("liusha"),
        postgres.WithInitScripts(
            "../../../db/migrations/0001_engagement.up.sql",
            "../../../db/migrations/0002_http_flow.up.sql",
            "../../../db/migrations/0003_http_flow_partition.up.sql",
            "../../../db/migrations/0004_http_flow_default.up.sql",
            "../../../db/migrations/0005_http_flow_2026.up.sql",
            "../../../db/migrations/0006_analysis_window.up.sql",
            "../../../db/migrations/0007_agent_task.up.sql",
            "../../../db/migrations/0008_graph.up.sql",
        ),
        tc.WithLogger(tc.TestLogger(t)),
    )
    require.NoError(t, err)
    t.Cleanup(func() { _ = pgc.Terminate(ctx) })

    dsn, err := pgc.ConnectionString(ctx, "sslmode=disable")
    require.NoError(t, err)
    pool, err := pgxpool.New(ctx, dsn)
    require.NoError(t, err)
    defer pool.Close()

    // 先插入一个 engagement
    engID := uuid.New()
    _, err = pool.Exec(ctx,
        `INSERT INTO engagement (id, tenant_id, mode, scope, status, version)
         VALUES ($1, $2, 'proxy', '{}', 'active', 1)`,
        engID, uuid.New(),
    )
    require.NoError(t, err)

    st := store.NewPostgres(pool)

    node := graphmodel.Node{
        ID:           uuid.New(),
        EngagementID: engID,
        Kind:         graphmodel.KindSurface,
        Payload:      map[string]any{"url": "http://example.com/login"},
        DedupKey:     "GET:example.com/login",
    }
    require.NoError(t, st.AddNode(ctx, node))

    got, err := st.GetNode(ctx, node.ID)
    require.NoError(t, err)
    assert.Equal(t, node.Kind, got.Kind)
    assert.Equal(t, "http://example.com/login", got.Payload["url"])
}

func TestIntegration_GraphStore_DedupIgnore(t *testing.T) {
    if testing.Short() {
        t.Skip("跳过集成测试")
    }
    ctx := context.Background()

    pgc, err := postgres.Run(ctx,
        "pgvector/pgvector:pg17",
        postgres.WithDatabase("liusha"),
        postgres.WithUsername("liusha"),
        postgres.WithPassword("liusha"),
        postgres.WithInitScripts(
            "../../../db/migrations/0001_engagement.up.sql",
            "../../../db/migrations/0002_http_flow.up.sql",
            "../../../db/migrations/0003_http_flow_partition.up.sql",
            "../../../db/migrations/0004_http_flow_default.up.sql",
            "../../../db/migrations/0005_http_flow_2026.up.sql",
            "../../../db/migrations/0006_analysis_window.up.sql",
            "../../../db/migrations/0007_agent_task.up.sql",
            "../../../db/migrations/0008_graph.up.sql",
        ),
        tc.WithLogger(tc.TestLogger(t)),
    )
    require.NoError(t, err)
    t.Cleanup(func() { _ = pgc.Terminate(ctx) })

    dsn, err := pgc.ConnectionString(ctx, "sslmode=disable")
    require.NoError(t, err)
    pool, err := pgxpool.New(ctx, dsn)
    require.NoError(t, err)
    defer pool.Close()

    engID := uuid.New()
    _, err = pool.Exec(ctx,
        `INSERT INTO engagement (id, tenant_id, mode, scope, status, version)
         VALUES ($1, $2, 'proxy', '{}', 'active', 1)`,
        engID, uuid.New(),
    )
    require.NoError(t, err)

    st := store.NewPostgres(pool)
    n1 := graphmodel.Node{
        ID: uuid.New(), EngagementID: engID,
        Kind: graphmodel.KindSurface, Payload: map[string]any{"url": "http://x.com"},
        DedupKey: "x.com",
    }
    n2 := graphmodel.Node{
        ID: uuid.New(), EngagementID: engID,
        Kind: graphmodel.KindSurface, Payload: map[string]any{"url": "http://x.com/other"},
        DedupKey: "x.com", // 相同 dedup_key
    }
    require.NoError(t, st.AddNode(ctx, n1))
    // 重复 dedup_key 应该静默忽略，不返回错误
    require.NoError(t, st.AddNode(ctx, n2))

    nodes, err := st.ListNodes(ctx, engID, graphmodel.KindSurface)
    require.NoError(t, err)
    assert.Len(t, nodes, 1) // 仅第一个被插入
}
```

- [ ] **Step 2: 运行测试，确认失败**

```bash
go test -run TestIntegration_GraphStore ./internal/graph/store/ 2>&1 | head -5
```

- [ ] **Step 3: 实现 postgres.go**

```go
// internal/graph/store/postgres.go
package store

import (
    "context"
    "encoding/json"
    "fmt"

    "github.com/google/uuid"
    "github.com/jackc/pgx/v5"
    "github.com/jackc/pgx/v5/pgxpool"

    graphmodel "github.com/V3teran/liusha/internal/graph/model"
)

type Postgres struct {
    pool *pgxpool.Pool
}

func NewPostgres(pool *pgxpool.Pool) *Postgres {
    return &Postgres{pool: pool}
}

// AddNode 插入节点；若存在相同 (engagement_id, dedup_key) 则静默忽略。
func (s *Postgres) AddNode(ctx context.Context, n graphmodel.Node) error {
    payload, err := json.Marshal(n.Payload)
    if err != nil {
        return fmt.Errorf("序列化 payload 失败: %w", err)
    }
    var dedupKey *string
    if n.DedupKey != "" {
        dedupKey = &n.DedupKey
    }
    _, err = s.pool.Exec(ctx, `
        INSERT INTO graph_node (id, engagement_id, kind, payload, dedup_key)
        VALUES ($1, $2, $3, $4, $5)
        ON CONFLICT (engagement_id, dedup_key) WHERE dedup_key IS NOT NULL DO NOTHING`,
        n.ID, n.EngagementID, string(n.Kind), payload, dedupKey,
    )
    if err != nil {
        return fmt.Errorf("插入 graph_node 失败: %w", err)
    }
    return nil
}

// AddEdge 插入边；同 (src_id, dst_id, kind) 重复则忽略。
func (s *Postgres) AddEdge(ctx context.Context, e graphmodel.Edge) error {
    _, err := s.pool.Exec(ctx, `
        INSERT INTO graph_edge (id, engagement_id, src_id, dst_id, kind)
        VALUES ($1, $2, $3, $4, $5)
        ON CONFLICT DO NOTHING`,
        e.ID, e.EngagementID, e.SrcID, e.DstID, string(e.Kind),
    )
    if err != nil {
        return fmt.Errorf("插入 graph_edge 失败: %w", err)
    }
    return nil
}

// GetNode 按 ID 查询节点。
func (s *Postgres) GetNode(ctx context.Context, id uuid.UUID) (graphmodel.Node, error) {
    row := s.pool.QueryRow(ctx, `
        SELECT id, engagement_id, kind, payload, COALESCE(dedup_key,''), created_at
        FROM graph_node WHERE id = $1`, id)
    return scanNode(row)
}

// ListNodes 按 engagement_id + kind 查询节点，按 created_at DESC 排序。
func (s *Postgres) ListNodes(ctx context.Context, engID uuid.UUID, kind graphmodel.NodeKind) ([]graphmodel.Node, error) {
    rows, err := s.pool.Query(ctx, `
        SELECT id, engagement_id, kind, payload, COALESCE(dedup_key,''), created_at
        FROM graph_node
        WHERE engagement_id = $1 AND kind = $2
        ORDER BY created_at DESC`, engID, string(kind))
    if err != nil {
        return nil, fmt.Errorf("查询 graph_node 失败: %w", err)
    }
    defer rows.Close()
    var nodes []graphmodel.Node
    for rows.Next() {
        n, err := scanNode(rows)
        if err != nil {
            return nil, err
        }
        nodes = append(nodes, n)
    }
    return nodes, rows.Err()
}

// ListEdges 查询以 nodeID 为起点或终点的所有边。
func (s *Postgres) ListEdges(ctx context.Context, engID uuid.UUID, nodeID uuid.UUID) ([]graphmodel.Edge, error) {
    rows, err := s.pool.Query(ctx, `
        SELECT id, engagement_id, src_id, dst_id, kind, created_at
        FROM graph_edge
        WHERE engagement_id = $1 AND (src_id = $2 OR dst_id = $2)
        ORDER BY created_at DESC`, engID, nodeID)
    if err != nil {
        return nil, fmt.Errorf("查询 graph_edge 失败: %w", err)
    }
    defer rows.Close()
    var edges []graphmodel.Edge
    for rows.Next() {
        var e graphmodel.Edge
        if err := rows.Scan(&e.ID, &e.EngagementID, &e.SrcID, &e.DstID, &e.Kind, &e.CreatedAt); err != nil {
            return nil, err
        }
        edges = append(edges, e)
    }
    return edges, rows.Err()
}

func scanNode(row pgx.Row) (graphmodel.Node, error) {
    var n graphmodel.Node
    var rawPayload []byte
    var kind string
    if err := row.Scan(&n.ID, &n.EngagementID, &kind, &rawPayload, &n.DedupKey, &n.CreatedAt); err != nil {
        return graphmodel.Node{}, fmt.Errorf("扫描 graph_node 失败: %w", err)
    }
    n.Kind = graphmodel.NodeKind(kind)
    if err := json.Unmarshal(rawPayload, &n.Payload); err != nil {
        return graphmodel.Node{}, fmt.Errorf("反序列化 payload 失败: %w", err)
    }
    return n, nil
}
```

- [ ] **Step 4: 运行集成测试**

```bash
go test -race -run TestIntegration_GraphStore ./internal/graph/store/
```
期望：`PASS`（2 个测试）

- [ ] **Step 5: 提交**

```bash
git add internal/graph/store/
git commit -m "feat(graph/store): Postgres cold store with dedup and edge queries"
```

---

## Task 4: Graph Redis 热缓存

**Files:**
- Create: `internal/graph/store/redis.go`
- Create: `internal/graph/store/redis_test.go`

Node 按 `liusha:graph:hot:{engagementID}:{nodeID}` 存为 JSON string，TTL 7 天；`HSet` 写、`HGet` 读、`HGetAll` 扫描整个 engagement 的节点（Redis Hash：key = `liusha:graph:hot:{engID}`，field = nodeID，value = JSON）。

- [ ] **Step 1: 写失败测试**

```go
// internal/graph/store/redis_test.go
package store_test

import (
    "context"
    "testing"

    "github.com/google/uuid"
    "github.com/redis/go-redis/v9"
    "github.com/stretchr/testify/assert"
    "github.com/stretchr/testify/require"
    tc "github.com/testcontainers/testcontainers-go"
    tcredis "github.com/testcontainers/testcontainers-go/modules/redis"

    graphmodel "github.com/V3teran/liusha/internal/graph/model"
    "github.com/V3teran/liusha/internal/graph/store"
)

func TestIntegration_GraphRedis_SetAndGet(t *testing.T) {
    if testing.Short() {
        t.Skip("跳过集成测试")
    }
    ctx := context.Background()

    rc, err := tcredis.Run(ctx, "redis:8", tc.WithLogger(tc.TestLogger(t)))
    require.NoError(t, err)
    t.Cleanup(func() { _ = rc.Terminate(ctx) })

    addr, err := rc.Endpoint(ctx, "")
    require.NoError(t, err)
    rdb := redis.NewClient(&redis.Options{Addr: addr})
    defer rdb.Close()

    st := store.NewRedis(rdb)
    engID := uuid.New()
    node := graphmodel.Node{
        ID:           uuid.New(),
        EngagementID: engID,
        Kind:         graphmodel.KindFinding,
        Payload:      map[string]any{"title": "SQLi in /login"},
    }

    require.NoError(t, st.SetNode(ctx, node))
    got, err := st.GetNode(ctx, engID, node.ID)
    require.NoError(t, err)
    assert.Equal(t, graphmodel.KindFinding, got.Kind)
    assert.Equal(t, "SQLi in /login", got.Payload["title"])

    all, err := st.ListNodes(ctx, engID)
    require.NoError(t, err)
    assert.Len(t, all, 1)
}
```

- [ ] **Step 2: 实现 redis.go**

```go
// internal/graph/store/redis.go
package store

import (
    "context"
    "encoding/json"
    "fmt"
    "time"

    "github.com/google/uuid"
    "github.com/redis/go-redis/v9"

    graphmodel "github.com/V3teran/liusha/internal/graph/model"
)

const (
    graphHotKeyFmt = "liusha:graph:hot:%s"
    graphHotTTL    = 7 * 24 * time.Hour
)

type Redis struct {
    rdb *redis.Client
}

func NewRedis(rdb *redis.Client) *Redis {
    return &Redis{rdb: rdb}
}

// SetNode 将节点写入 Redis Hash，并刷新整个 Hash 的 TTL。
func (s *Redis) SetNode(ctx context.Context, n graphmodel.Node) error {
    b, err := json.Marshal(n)
    if err != nil {
        return fmt.Errorf("序列化 node 失败: %w", err)
    }
    key := fmt.Sprintf(graphHotKeyFmt, n.EngagementID)
    pipe := s.rdb.Pipeline()
    pipe.HSet(ctx, key, n.ID.String(), string(b))
    pipe.Expire(ctx, key, graphHotTTL)
    _, err = pipe.Exec(ctx)
    return err
}

// GetNode 从 Redis 读取单个节点。
func (s *Redis) GetNode(ctx context.Context, engID, nodeID uuid.UUID) (graphmodel.Node, error) {
    key := fmt.Sprintf(graphHotKeyFmt, engID)
    val, err := s.rdb.HGet(ctx, key, nodeID.String()).Result()
    if err != nil {
        return graphmodel.Node{}, fmt.Errorf("HGet graph node 失败: %w", err)
    }
    var n graphmodel.Node
    if err := json.Unmarshal([]byte(val), &n); err != nil {
        return graphmodel.Node{}, fmt.Errorf("反序列化 node 失败: %w", err)
    }
    return n, nil
}

// ListNodes 返回 engagement 在 Redis 中的所有热节点。
func (s *Redis) ListNodes(ctx context.Context, engID uuid.UUID) ([]graphmodel.Node, error) {
    key := fmt.Sprintf(graphHotKeyFmt, engID)
    vals, err := s.rdb.HGetAll(ctx, key).Result()
    if err != nil {
        return nil, fmt.Errorf("HGetAll graph nodes 失败: %w", err)
    }
    nodes := make([]graphmodel.Node, 0, len(vals))
    for _, v := range vals {
        var n graphmodel.Node
        if err := json.Unmarshal([]byte(v), &n); err != nil {
            return nil, fmt.Errorf("反序列化 node 失败: %w", err)
        }
        nodes = append(nodes, n)
    }
    return nodes, nil
}
```

- [ ] **Step 3: 运行集成测试**

```bash
go test -race -run TestIntegration_GraphRedis ./internal/graph/store/
```
期望：`PASS`

- [ ] **Step 4: 提交**

```bash
git add internal/graph/store/redis.go internal/graph/store/redis_test.go
git commit -m "feat(graph/store): Redis hot cache with 7d TTL"
```

---

## Task 5: AnalysisWindow + AgentTask 存储

**Files:**
- Create: `internal/window/model.go`
- Create: `internal/window/store/postgres.go`
- Create: `internal/window/store/postgres_test.go`

- [ ] **Step 1: 写失败测试**

```go
// internal/window/store/postgres_test.go
package store_test

import (
    "context"
    "testing"
    "time"

    "github.com/google/uuid"
    "github.com/jackc/pgx/v5/pgxpool"
    "github.com/stretchr/testify/assert"
    "github.com/stretchr/testify/require"
    "github.com/testcontainers/testcontainers-go/modules/postgres"
    tc "github.com/testcontainers/testcontainers-go"

    "github.com/V3teran/liusha/internal/window"
    winstore "github.com/V3teran/liusha/internal/window/store"
)

func TestIntegration_WindowStore(t *testing.T) {
    if testing.Short() {
        t.Skip("跳过集成测试")
    }
    ctx := context.Background()

    pgc, err := postgres.Run(ctx,
        "pgvector/pgvector:pg17",
        postgres.WithDatabase("liusha"),
        postgres.WithUsername("liusha"),
        postgres.WithPassword("liusha"),
        postgres.WithInitScripts(
            "../../../db/migrations/0001_engagement.up.sql",
            "../../../db/migrations/0002_http_flow.up.sql",
            "../../../db/migrations/0003_http_flow_partition.up.sql",
            "../../../db/migrations/0004_http_flow_default.up.sql",
            "../../../db/migrations/0005_http_flow_2026.up.sql",
            "../../../db/migrations/0006_analysis_window.up.sql",
            "../../../db/migrations/0007_agent_task.up.sql",
            "../../../db/migrations/0008_graph.up.sql",
        ),
        tc.WithLogger(tc.TestLogger(t)),
    )
    require.NoError(t, err)
    t.Cleanup(func() { _ = pgc.Terminate(ctx) })

    dsn, err := pgc.ConnectionString(ctx, "sslmode=disable")
    require.NoError(t, err)
    pool, err := pgxpool.New(ctx, dsn)
    require.NoError(t, err)
    defer pool.Close()

    engID := uuid.New()
    _, err = pool.Exec(ctx,
        `INSERT INTO engagement (id, tenant_id, mode, scope, status, version)
         VALUES ($1, $2, 'proxy', '{}', 'active', 1)`,
        engID, uuid.New(),
    )
    require.NoError(t, err)

    st := winstore.NewPostgres(pool)

    w := window.AnalysisWindow{
        ID:           uuid.New(),
        EngagementID: engID,
        TriggerKind:  window.TriggerTime,
        StartedAt:    time.Now().UTC().Truncate(time.Millisecond),
    }
    require.NoError(t, st.CreateWindow(ctx, w))

    got, err := st.GetWindow(ctx, w.ID)
    require.NoError(t, err)
    assert.Equal(t, window.TriggerTime, got.TriggerKind)

    task := window.AgentTask{
        ID:       uuid.New(),
        WindowID: w.ID,
        Role:     "planner",
        Status:   window.TaskPending,
    }
    require.NoError(t, st.CreateTask(ctx, task))

    tasks, err := st.ListTasks(ctx, w.ID)
    require.NoError(t, err)
    assert.Len(t, tasks, 1)
    assert.Equal(t, "planner", tasks[0].Role)
}
```

- [ ] **Step 2: 实现 model.go**

```go
// internal/window/model.go
package window

import (
    "time"
    "github.com/google/uuid"
)

type TriggerKind string
const (
    TriggerTime      TriggerKind = "time"
    TriggerFlowCount TriggerKind = "flow_count"
    TriggerManual    TriggerKind = "manual"
)

type AnalysisWindow struct {
    ID           uuid.UUID
    EngagementID uuid.UUID
    TriggerKind  TriggerKind
    FlowCount    int
    StartedAt    time.Time
    EndedAt      *time.Time
}

type TaskStatus string
const (
    TaskPending   TaskStatus = "pending"
    TaskRunning   TaskStatus = "running"
    TaskSucceeded TaskStatus = "succeeded"
    TaskFailed    TaskStatus = "failed"
    TaskCancelled TaskStatus = "cancelled"
)

type AgentTask struct {
    ID             uuid.UUID
    WindowID       uuid.UUID
    Role           string
    Status         TaskStatus
    InputNodeIDs   []uuid.UUID
    OutputNodeIDs  []uuid.UUID
    CostMicroUSD   int64
    StartedAt      *time.Time
    EndedAt        *time.Time
}
```

- [ ] **Step 3: 实现 store/postgres.go**

```go
// internal/window/store/postgres.go
package store

import (
    "context"
    "fmt"

    "github.com/google/uuid"
    "github.com/jackc/pgx/v5/pgxpool"

    "github.com/V3teran/liusha/internal/window"
)

type Postgres struct{ pool *pgxpool.Pool }

func NewPostgres(pool *pgxpool.Pool) *Postgres { return &Postgres{pool: pool} }

func (s *Postgres) CreateWindow(ctx context.Context, w window.AnalysisWindow) error {
    _, err := s.pool.Exec(ctx,
        `INSERT INTO analysis_window (id, engagement_id, trigger_kind, flow_count, started_at)
         VALUES ($1, $2, $3, $4, $5)`,
        w.ID, w.EngagementID, string(w.TriggerKind), w.FlowCount, w.StartedAt,
    )
    if err != nil {
        return fmt.Errorf("创建 analysis_window 失败: %w", err)
    }
    return nil
}

func (s *Postgres) GetWindow(ctx context.Context, id uuid.UUID) (window.AnalysisWindow, error) {
    var w window.AnalysisWindow
    var kind string
    err := s.pool.QueryRow(ctx,
        `SELECT id, engagement_id, trigger_kind, COALESCE(flow_count,0), started_at, ended_at
         FROM analysis_window WHERE id = $1`, id,
    ).Scan(&w.ID, &w.EngagementID, &kind, &w.FlowCount, &w.StartedAt, &w.EndedAt)
    if err != nil {
        return window.AnalysisWindow{}, fmt.Errorf("查询 analysis_window 失败: %w", err)
    }
    w.TriggerKind = window.TriggerKind(kind)
    return w, nil
}

func (s *Postgres) CreateTask(ctx context.Context, t window.AgentTask) error {
    _, err := s.pool.Exec(ctx,
        `INSERT INTO agent_task (id, window_id, role, status, cost_micro_usd)
         VALUES ($1, $2, $3, $4, $5)`,
        t.ID, t.WindowID, t.Role, string(t.Status), t.CostMicroUSD,
    )
    if err != nil {
        return fmt.Errorf("创建 agent_task 失败: %w", err)
    }
    return nil
}

func (s *Postgres) ListTasks(ctx context.Context, windowID uuid.UUID) ([]window.AgentTask, error) {
    rows, err := s.pool.Query(ctx,
        `SELECT id, window_id, role, status, cost_micro_usd
         FROM agent_task WHERE window_id = $1 ORDER BY ctid`, windowID,
    )
    if err != nil {
        return nil, fmt.Errorf("查询 agent_task 失败: %w", err)
    }
    defer rows.Close()
    var tasks []window.AgentTask
    for rows.Next() {
        var t window.AgentTask
        var status string
        if err := rows.Scan(&t.ID, &t.WindowID, &t.Role, &status, &t.CostMicroUSD); err != nil {
            return nil, err
        }
        t.Status = window.TaskStatus(status)
        tasks = append(tasks, t)
    }
    return tasks, rows.Err()
}
```

- [ ] **Step 4: 运行集成测试**

```bash
go test -race -run TestIntegration_WindowStore ./internal/window/store/
```
期望：`PASS`

- [ ] **Step 5: 全量测试**

```bash
go vet ./... && go test -race -short ./... && go test -race -run Integration ./...
```

- [ ] **Step 6: 提交并打标签**

```bash
git add internal/window/ db/migrations/
git commit -m "feat(window): AnalysisWindow and AgentTask Postgres store"
git tag v0.4.0-plan4
```
