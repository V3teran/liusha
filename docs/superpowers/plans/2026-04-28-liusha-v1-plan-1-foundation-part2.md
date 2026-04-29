# Liusha v1 Plan 1 (part 2): Domain Stores + 通用 Lib

> 续 `2026-04-28-liusha-v1-plan-1-foundation.md`（T1-T5）。本文件含 T6-T16。

**约定：**
- 所有 store 用 `pgxpool.Pool`，集成测试统一用 testcontainers + `0001_init.up.sql`
- 测试 helper 复用：`internal/dbtest/dbtest.go`（在 T6 首次出现，后续 store 直接 import）

---

## 黑客松借鉴增量（part 2 范围，2026-04-29 加入；详见 docs/hks2.md）

| Task | 改动 |
|---|---|
| T4（在主 plan 文件，0001_init.sql） | engagement 表 `memory jsonb` 删除，改加 `memory_facts jsonb / memory_ideas jsonb / memory_hints jsonb`（NOT NULL DEFAULT '{}'::jsonb） |
| T6 | `Engagement` struct 字段从 `Memory map[string]any` 改成 `MemoryFacts / MemoryIdeas / MemoryHints map[string]any`；store 暴露 `ReadState(ctx, id) (state State, err error)` 与 `AppendFact / AppendIdea / AppendHint(ctx, id, entry)` 三方法（替代旧 `UpdateMemory`） |
| T11（finding store） | `Save` 成功路径暴露一个 hook 回调 `OnSaved(eid, finding)`，供 T23.5 Distill 监听（plan 1 仅暴露接口，订阅在 T23.5） |

实现端 patch 提示：
- T6 store 的 `AppendFact` 应 SQL `UPDATE engagement SET memory_facts = jsonb_set(memory_facts, ARRAY['evidence', ...]::text[], memory_facts->'evidence' || $1::jsonb)`
- 三层 memory 都按"只追加"语义；超过 100 条由 store 内 trigger 滚动淘汰最旧（v1 简化：每次 Append 后裁 jsonb 数组到末尾 100）
- `ReadState` 返回结构：`{Facts: {evidence, boundaries}, Ideas: {hypotheses}, Hints: {hints}}`，与 spec §3.4 字段一致

---

## Task 6: internal/engagement — model + store

**Files:**
- Create: `internal/dbtest/dbtest.go`（共享测试夹具）
- Create: `internal/engagement/model.go`
- Create: `internal/engagement/store.go`
- Create: `internal/engagement/store_integration_test.go`

**职责：** `Engagement{ID, TenantID, Mode, ScopeHost, Status, MemoryFacts, MemoryIdeas, MemoryHints, CreatedAt, LastActivityAt}`。`LookupOrCreate(tenant, host, mode)` 是核心懒创建入口（active 才返回；否则插一行）。`ReadState / AppendFact / AppendIdea / AppendHint` 是黑客松借鉴双层状态板（见上面"借鉴增量"行 T6）。

- [ ] **Step 1: 写共享测试夹具 `internal/dbtest/dbtest.go`**

```go
//go:build integration

package dbtest

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
)

func NewPgPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	t.Cleanup(cancel)

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
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)

	sql, err := os.ReadFile(repoPath("db/migrations/0001_init.up.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, string(sql)); err != nil {
		t.Fatalf("apply migration: %v", err)
	}
	return pool
}

func repoPath(rel string) string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "..", "..", rel)
}
```

- [ ] **Step 2: 写失败测试 `internal/engagement/store_integration_test.go`**

```go
//go:build integration

package engagement

import (
	"context"
	"strings"
	"testing"

	"github.com/V3teran/liusha/internal/dbtest"
)

func TestStore_LookupOrCreate_LazyAndIdempotent(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.NewPgPool(t)
	s := NewStore(pool)

	got, err := s.LookupOrCreate(ctx, "default", "vulnapp", ModeProxy)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID == "" || got.Status != StatusActive {
		t.Fatalf("unexpected: %+v", got)
	}
	got2, err := s.LookupOrCreate(ctx, "default", "vulnapp", ModeProxy)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != got2.ID {
		t.Fatalf("expected idempotent, got %s vs %s", got.ID, got2.ID)
	}
}

func TestStore_Abort(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.NewPgPool(t)
	s := NewStore(pool)

	e, _ := s.LookupOrCreate(ctx, "default", "h", ModeProxy)
	if err := s.Abort(ctx, e.ID); err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetByID(ctx, e.ID)
	if got.Status != StatusAborted {
		t.Fatalf("status: %s", got.Status)
	}
	e2, err := s.LookupOrCreate(ctx, "default", "h", ModeProxy)
	if err != nil || e2.ID == e.ID {
		t.Fatalf("expected new active, got %+v err=%v", e2, err)
	}
}

func TestStore_AppendAndReadState(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.NewPgPool(t)
	s := NewStore(pool)
	e, _ := s.LookupOrCreate(ctx, "default", "h", ModeProxy)

	if err := s.AppendFact(ctx, e.ID,
		[]byte(`{"category":"evidence","content":"endpoint X 401"}`)); err != nil {
		t.Fatal(err)
	}
	if err := s.AppendFact(ctx, e.ID,
		[]byte(`{"category":"boundary","content":"all_similar at threshold 0.3"}`)); err != nil {
		t.Fatal(err)
	}
	if err := s.AppendIdea(ctx, e.ID,
		[]byte(`{"direction":"GET /admin","status":"testing"}`)); err != nil {
		t.Fatal(err)
	}
	if err := s.AppendHint(ctx, e.ID,
		[]byte(`{"from_skill":"vuln/web/bac","content":"hint content","priority":7}`)); err != nil {
		t.Fatal(err)
	}

	state, err := s.ReadState(ctx, e.ID)
	if err != nil {
		t.Fatal(err)
	}
	got := string(state)
	if !strings.Contains(got, `"evidence"`) ||
		!strings.Contains(got, `"boundaries"`) ||
		!strings.Contains(got, `"hypotheses"`) ||
		!strings.Contains(got, `"hints"`) {
		t.Fatalf("state missing keys: %s", got)
	}
}
```

- [ ] **Step 3: 实现 `internal/engagement/model.go`**

```go
package engagement

import "time"

type Mode string
type Status string

const (
	ModeProxy   Mode = "proxy"
	ModeBrowser Mode = "browser"

	StatusActive   Status = "active"
	StatusAborted  Status = "aborted"
	StatusArchived Status = "archived"
)

type Engagement struct {
	ID             string
	TenantID       string
	Mode           Mode
	ScopeHost      string
	Status         Status
	MemoryFacts    []byte // jsonb: {evidence: [...], boundaries: [...]}
	MemoryIdeas    []byte // jsonb: {hypotheses: [{direction, status, ts}]}
	MemoryHints    []byte // jsonb: {hints: [{from_skill, content, priority, ts}]}
	CreatedAt      time.Time
	LastActivityAt time.Time
}

// State 是 ReadState action 返回的合并视图
type State struct {
	Facts json.RawMessage `json:"facts"`
	Ideas json.RawMessage `json:"ideas"`
	Hints json.RawMessage `json:"hints"`
}
```

注意：`State` 引用了 `encoding/json`，model.go 顶部需要 `import "encoding/json"`。

- [ ] **Step 4: 实现 `internal/engagement/store.go`**

```go
package engagement

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Store struct{ pool *pgxpool.Pool }

func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

const colsSelect = "id, tenant_id, mode, scope_host, status, memory_facts, memory_ideas, memory_hints, created_at, last_activity_at"

func (s *Store) LookupOrCreate(ctx context.Context, tenant, host string, mode Mode) (Engagement, error) {
	row := s.pool.QueryRow(ctx, `
		SELECT `+colsSelect+`
		FROM engagement
		WHERE tenant_id=$1 AND scope_host=$2 AND status='active'
		LIMIT 1`, tenant, host)
	var e Engagement
	err := scan(row, &e)
	if err == nil {
		return e, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return Engagement{}, fmt.Errorf("lookup engagement: %w", err)
	}
	row = s.pool.QueryRow(ctx, `
		INSERT INTO engagement (tenant_id, mode, scope_host, status)
		VALUES ($1,$2,$3,'active')
		RETURNING `+colsSelect, tenant, mode, host)
	if err := scan(row, &e); err != nil {
		return Engagement{}, fmt.Errorf("insert engagement: %w", err)
	}
	return e, nil
}

func (s *Store) GetByID(ctx context.Context, id string) (Engagement, error) {
	row := s.pool.QueryRow(ctx, "SELECT "+colsSelect+" FROM engagement WHERE id=$1", id)
	var e Engagement
	if err := scan(row, &e); err != nil {
		return Engagement{}, fmt.Errorf("get engagement %s: %w", id, err)
	}
	return e, nil
}

func (s *Store) Abort(ctx context.Context, id string) error {
	_, err := s.pool.Exec(ctx, `UPDATE engagement SET status='aborted', last_activity_at=now() WHERE id=$1`, id)
	if err != nil {
		return fmt.Errorf("abort engagement %s: %w", id, err)
	}
	return nil
}

func (s *Store) Touch(ctx context.Context, id string) error {
	_, err := s.pool.Exec(ctx, `UPDATE engagement SET last_activity_at=now() WHERE id=$1`, id)
	return err
}

type scanner interface {
	Scan(dest ...any) error
}

func scan(r scanner, e *Engagement) error {
	return r.Scan(&e.ID, &e.TenantID, &e.Mode, &e.ScopeHost, &e.Status,
		&e.MemoryFacts, &e.MemoryIdeas, &e.MemoryHints,
		&e.CreatedAt, &e.LastActivityAt)
}

// ReadState 一次读取 memory 三层并组装成 State JSON（供 ReadState action 使用）
func (s *Store) ReadState(ctx context.Context, id string) ([]byte, error) {
	var facts, ideas, hints []byte
	err := s.pool.QueryRow(ctx,
		`SELECT memory_facts, memory_ideas, memory_hints FROM engagement WHERE id=$1`, id).
		Scan(&facts, &ideas, &hints)
	if err != nil {
		return nil, fmt.Errorf("read state %s: %w", id, err)
	}
	return json.Marshal(State{Facts: facts, Ideas: ideas, Hints: hints})
}

// AppendFact 追加一条事实到 memory_facts。entry 是 {category, content, ts?} 形式 jsonb。
// 按 category 推到对应数组（evidence 或 boundaries）；超 100 条滚动淘汰最旧。
func (s *Store) AppendFact(ctx context.Context, id string, entry []byte) error {
	return s.appendInto(ctx, id, "memory_facts", entry, factCategoryToKey)
}

// AppendIdea 追加一条假设到 memory_ideas.hypotheses。
func (s *Store) AppendIdea(ctx context.Context, id string, entry []byte) error {
	return s.appendInto(ctx, id, "memory_ideas", entry, fixedKey("hypotheses"))
}

// AppendHint 追加一条提示到 memory_hints.hints。
func (s *Store) AppendHint(ctx context.Context, id string, entry []byte) error {
	return s.appendInto(ctx, id, "memory_hints", entry, fixedKey("hints"))
}

// appendInto 通用 jsonb 数组追加：col[key] = (col[key] || []) || [entry]，再裁剪到 100 条。
type keyFn func(entry []byte) (string, error)

func factCategoryToKey(entry []byte) (string, error) {
	var p struct {
		Category string `json:"category"`
	}
	if err := json.Unmarshal(entry, &p); err != nil {
		return "", err
	}
	switch p.Category {
	case "evidence":
		return "evidence", nil
	case "boundary":
		return "boundaries", nil
	default:
		return "", fmt.Errorf("invalid fact category %q", p.Category)
	}
}

func fixedKey(k string) keyFn { return func([]byte) (string, error) { return k, nil } }

func (s *Store) appendInto(ctx context.Context, id, col string, entry []byte, kf keyFn) error {
	key, err := kf(entry)
	if err != nil {
		return err
	}
	// jsonb_set + COALESCE 兼容空对象；slice 取末尾 100 条
	q := fmt.Sprintf(`
		UPDATE engagement
		SET %s = jsonb_set(
			COALESCE(%s, '{}'::jsonb),
			$1,
			COALESCE(%s->$2, '[]'::jsonb) || $3::jsonb
		),
		last_activity_at = now()
		WHERE id=$4`, col, col, col)
	_, err = s.pool.Exec(ctx, q, "{"+key+"}", key, entry, id)
	if err != nil {
		return fmt.Errorf("append %s: %w", col, err)
	}
	// 滚动裁剪到 100 条最末尾（独立 UPDATE，避免 SQL 复杂化）
	trim := fmt.Sprintf(`
		UPDATE engagement
		SET %s = jsonb_set(%s, $1,
			CASE WHEN jsonb_array_length(%s->$2) > 100
				THEN (SELECT jsonb_agg(v) FROM (SELECT v FROM jsonb_array_elements(%s->$2) v
					OFFSET GREATEST(jsonb_array_length(%s->$2)-100,0)) sub)
				ELSE %s->$2
			END)
		WHERE id=$3`, col, col, col, col, col, col)
	_, _ = s.pool.Exec(ctx, trim, "{"+key+"}", key, id) // 错误忽略：裁剪失败不影响主流程
	return nil
}
```

注意：store.go 顶部需补 `import "encoding/json"`。

- [ ] **Step 5: 跑测试 + Commit**

```bash
go test -tags=integration ./internal/engagement/... -race -count=1
git add internal/dbtest internal/engagement
git commit -m "feat(engagement): model + store（LookupOrCreate / Abort / Touch / ReadState / AppendFact|Idea|Hint，memory 三层）"
```

---

## Task 7: internal/window — traffic_window store

**Files:**
- Create: `internal/window/model.go`
- Create: `internal/window/store.go`
- Create: `internal/window/store_integration_test.go`

**职责：** `TrafficWindow` 持 `flows jsonb`（slice of FlowRef），状态 open→closed→consumed。`OpenOrAppend(engagementID, flowRef, batchLimit)` 原子追加；`MarkConsumed(id)` 状态机。

- [ ] **Step 1: 写失败测试**

```go
//go:build integration

package window

import (
	"context"
	"testing"

	"github.com/V3teran/liusha/internal/dbtest"
	"github.com/V3teran/liusha/internal/engagement"
)

func setup(t *testing.T) (*Store, string) {
	pool := dbtest.NewPgPool(t)
	es := engagement.NewStore(pool)
	e, _ := es.LookupOrCreate(context.Background(), "default", "h", engagement.ModeProxy)
	return NewStore(pool), e.ID
}

func TestStore_OpenOrAppend_BatchTrigger(t *testing.T) {
	ctx := context.Background()
	s, eid := setup(t)
	for i := 0; i < 4; i++ {
		if _, _, err := s.OpenOrAppend(ctx, eid, FlowRef{ID: int64(i + 1), URL: "/x"}, 3); err != nil {
			t.Fatal(err)
		}
	}
	closed, err := s.ListClosed(ctx, eid, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(closed) != 1 || len(closed[0].Flows) != 3 {
		t.Fatalf("expected 1 closed window with 3 flows, got %+v", closed)
	}
}

func TestStore_MarkConsumed(t *testing.T) {
	ctx := context.Background()
	s, eid := setup(t)
	for i := 0; i < 3; i++ {
		_, _, _ = s.OpenOrAppend(ctx, eid, FlowRef{ID: int64(i + 1)}, 3)
	}
	closed, _ := s.ListClosed(ctx, eid, 10)
	if err := s.MarkConsumed(ctx, closed[0].ID); err != nil {
		t.Fatal(err)
	}
	again, _ := s.ListClosed(ctx, eid, 10)
	if len(again) != 0 {
		t.Fatalf("consumed window should not appear in ListClosed: %+v", again)
	}
}
```

- [ ] **Step 2: 实现 `internal/window/model.go`**

```go
package window

import "time"

type Status string

const (
	StatusOpen     Status = "open"
	StatusClosed   Status = "closed"
	StatusConsumed Status = "consumed"
)

type FlowRef struct {
	ID     int64  `json:"id"`
	Method string `json:"method,omitempty"`
	URL    string `json:"url,omitempty"`
}

type Window struct {
	ID           string
	EngagementID string
	Flows        []FlowRef
	Status       Status
	StartedAt    time.Time
	ClosedAt     *time.Time
}
```

- [ ] **Step 3: 实现 `internal/window/store.go`**

```go
package window

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Store struct{ pool *pgxpool.Pool }

func NewStore(p *pgxpool.Pool) *Store { return &Store{pool: p} }

func (s *Store) OpenOrAppend(ctx context.Context, engagementID string, ref FlowRef, batchLimit int) (string, bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return "", false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var (
		id    string
		flows []byte
	)
	row := tx.QueryRow(ctx, `
		SELECT id, flows FROM traffic_window
		WHERE engagement_id=$1 AND status='open'
		ORDER BY started_at ASC LIMIT 1
		FOR UPDATE`, engagementID)
	err = row.Scan(&id, &flows)
	if errors.Is(err, pgx.ErrNoRows) {
		if err := tx.QueryRow(ctx, `
			INSERT INTO traffic_window (engagement_id, flows, status)
			VALUES ($1, '[]'::jsonb, 'open')
			RETURNING id`, engagementID).Scan(&id); err != nil {
			return "", false, fmt.Errorf("insert open window: %w", err)
		}
		flows = []byte("[]")
	} else if err != nil {
		return "", false, fmt.Errorf("lock window: %w", err)
	}

	var arr []FlowRef
	if err := json.Unmarshal(flows, &arr); err != nil {
		return "", false, err
	}
	arr = append(arr, ref)
	enc, _ := json.Marshal(arr)

	closed := len(arr) >= batchLimit
	q := `UPDATE traffic_window SET flows=$1 WHERE id=$2`
	if closed {
		q = `UPDATE traffic_window SET flows=$1, status='closed', closed_at=now() WHERE id=$2`
	}
	if _, err := tx.Exec(ctx, q, enc, id); err != nil {
		return "", false, fmt.Errorf("update window: %w", err)
	}
	return id, closed, tx.Commit(ctx)
}

func (s *Store) CloseExpired(ctx context.Context, engagementID string, maxAgeSec int) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE traffic_window
		SET status='closed', closed_at=now()
		WHERE engagement_id=$1 AND status='open'
		AND now() - started_at > make_interval(secs => $2)`, engagementID, maxAgeSec)
	return err
}

func (s *Store) ListClosed(ctx context.Context, engagementID string, limit int) ([]Window, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, engagement_id, flows, status, started_at, closed_at
		FROM traffic_window
		WHERE engagement_id=$1 AND status='closed'
		ORDER BY started_at ASC LIMIT $2`, engagementID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Window
	for rows.Next() {
		var w Window
		var flows []byte
		if err := rows.Scan(&w.ID, &w.EngagementID, &flows, &w.Status, &w.StartedAt, &w.ClosedAt); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(flows, &w.Flows)
		out = append(out, w)
	}
	return out, rows.Err()
}

func (s *Store) MarkConsumed(ctx context.Context, id string) error {
	_, err := s.pool.Exec(ctx, `UPDATE traffic_window SET status='consumed' WHERE id=$1`, id)
	return err
}
```

- [ ] **Step 4: 跑测试 + Commit**

```bash
go test -tags=integration ./internal/window/... -race -count=1
git add internal/window
git commit -m "feat(window): traffic_window store（OpenOrAppend / CloseExpired / ListClosed / MarkConsumed）"
```

---

## Task 8: internal/task — agent_task store

**Files:**
- Create: `internal/task/model.go`
- Create: `internal/task/store.go`
- Create: `internal/task/store_integration_test.go`

- [ ] **Step 1: 写失败测试**

```go
//go:build integration

package task

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/V3teran/liusha/internal/dbtest"
	"github.com/V3teran/liusha/internal/engagement"
)

func TestStore_CreateThenComplete(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.NewPgPool(t)
	es := engagement.NewStore(pool)
	e, _ := es.LookupOrCreate(ctx, "default", "h", engagement.ModeProxy)
	s := NewStore(pool)

	id, err := s.Create(ctx, NewParams{
		EngagementID: e.ID, Role: "sniffer",
		Input: json.RawMessage(`{"window_id":"w1"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetRunning(ctx, id); err != nil {
		t.Fatal(err)
	}
	res, _ := json.Marshal(map[string]any{"terminate_by": "done", "total_steps": 7})
	if err := s.SetDone(ctx, id, res); err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetByID(ctx, id)
	if got.Status != StatusDone {
		t.Fatalf("status=%s", got.Status)
	}
}
```

- [ ] **Step 2: 实现 `internal/task/model.go`**

```go
package task

import (
	"encoding/json"
	"time"
)

type Status string

const (
	StatusPending Status = "pending"
	StatusRunning Status = "running"
	StatusDone    Status = "done"
	StatusAborted Status = "aborted"
	StatusError   Status = "error"
)

type Task struct {
	ID           string
	EngagementID string
	ParentTaskID *string
	Role         string
	Skill        string
	Input        json.RawMessage
	Budget       json.RawMessage
	Result       json.RawMessage
	Status       Status
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

type NewParams struct {
	EngagementID string
	ParentTaskID *string
	Role         string
	Skill        string
	Input        json.RawMessage
	Budget       json.RawMessage
}
```

- [ ] **Step 3: 实现 `internal/task/store.go`**

```go
package task

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

type Store struct{ pool *pgxpool.Pool }

func NewStore(p *pgxpool.Pool) *Store { return &Store{pool: p} }

func (s *Store) Create(ctx context.Context, p NewParams) (string, error) {
	if p.Input == nil {
		p.Input = json.RawMessage("{}")
	}
	if p.Budget == nil {
		p.Budget = json.RawMessage("{}")
	}
	var id string
	err := s.pool.QueryRow(ctx, `
		INSERT INTO agent_task (engagement_id, parent_task_id, role, skill, input, budget)
		VALUES ($1,$2,$3,$4,$5,$6)
		RETURNING id`, p.EngagementID, p.ParentTaskID, p.Role, p.Skill, []byte(p.Input), []byte(p.Budget)).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("insert task: %w", err)
	}
	return id, nil
}

func (s *Store) SetRunning(ctx context.Context, id string) error {
	_, err := s.pool.Exec(ctx, `UPDATE agent_task SET status='running', updated_at=now() WHERE id=$1`, id)
	return err
}

func (s *Store) SetDone(ctx context.Context, id string, result json.RawMessage) error {
	_, err := s.pool.Exec(ctx, `UPDATE agent_task SET status='done', result=$1, updated_at=now() WHERE id=$2`, []byte(result), id)
	return err
}

func (s *Store) SetError(ctx context.Context, id string, errMsg string) error {
	body, _ := json.Marshal(map[string]string{"error": errMsg})
	_, err := s.pool.Exec(ctx, `UPDATE agent_task SET status='error', result=$1, updated_at=now() WHERE id=$2`, body, id)
	return err
}

func (s *Store) GetByID(ctx context.Context, id string) (Task, error) {
	row := s.pool.QueryRow(ctx, `
		SELECT id, engagement_id, parent_task_id, role, skill, input, budget, result, status, created_at, updated_at
		FROM agent_task WHERE id=$1`, id)
	var t Task
	var input, budget, result []byte
	if err := row.Scan(&t.ID, &t.EngagementID, &t.ParentTaskID, &t.Role, &t.Skill,
		&input, &budget, &result, &t.Status, &t.CreatedAt, &t.UpdatedAt); err != nil {
		return Task{}, fmt.Errorf("get task %s: %w", id, err)
	}
	t.Input, t.Budget, t.Result = input, budget, result
	return t, nil
}

func (s *Store) CountInflightChildren(ctx context.Context, parentID string) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx, `
		SELECT count(*) FROM agent_task
		WHERE parent_task_id=$1 AND status IN ('pending','running')`, parentID).Scan(&n)
	return n, err
}

func (s *Store) CountInflightInEngagement(ctx context.Context, engagementID string) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx, `
		SELECT count(*) FROM agent_task
		WHERE engagement_id=$1 AND status IN ('pending','running')`, engagementID).Scan(&n)
	return n, err
}
```

- [ ] **Step 4: 跑测试 + Commit**

```bash
go test -tags=integration ./internal/task/... -race -count=1
git add internal/task
git commit -m "feat(task): agent_task store（Create / SetRunning / SetDone / SetError / inflight 计数）"
```

---

## Task 9: internal/graph — node + edge store

**Files:**
- Create: `internal/graph/model.go`
- Create: `internal/graph/store.go`
- Create: `internal/graph/store_integration_test.go`

- [ ] **Step 1: 写失败测试**

```go
//go:build integration

package graph

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/V3teran/liusha/internal/dbtest"
	"github.com/V3teran/liusha/internal/engagement"
)

func TestStore_UpsertNode_Idempotent(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.NewPgPool(t)
	e, _ := engagement.NewStore(pool).LookupOrCreate(ctx, "default", "h", engagement.ModeProxy)
	s := NewStore(pool)

	a, err := s.UpsertNode(ctx, e.ID, "endpoint", "vulnapp:GET:/api/x", json.RawMessage(`{"method":"GET"}`))
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.UpsertNode(ctx, e.ID, "endpoint", "vulnapp:GET:/api/x", json.RawMessage(`{"method":"GET","seen":2}`))
	if err != nil {
		t.Fatal(err)
	}
	if a != b {
		t.Fatalf("expected same id on conflict, got %s vs %s", a, b)
	}
}
```

- [ ] **Step 2: 实现 `internal/graph/model.go`**

```go
package graph

import (
	"encoding/json"
	"time"
)

type Node struct {
	ID           string
	EngagementID string
	Kind         string
	Payload      json.RawMessage
	DedupKey     string
	CreatedAt    time.Time
}

type Edge struct {
	ID           string
	EngagementID string
	FromID       string
	ToID         string
	Kind         string
	Payload      json.RawMessage
	CreatedAt    time.Time
}
```

- [ ] **Step 3: 实现 `internal/graph/store.go`**

```go
package graph

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

type Store struct{ pool *pgxpool.Pool }

func NewStore(p *pgxpool.Pool) *Store { return &Store{pool: p} }

func (s *Store) UpsertNode(ctx context.Context, engagementID, kind, dedupKey string, payload json.RawMessage) (string, error) {
	if payload == nil {
		payload = json.RawMessage("{}")
	}
	var id string
	err := s.pool.QueryRow(ctx, `
		INSERT INTO graph_node (engagement_id, kind, payload, dedup_key)
		VALUES ($1,$2,$3,$4)
		ON CONFLICT (engagement_id, kind, dedup_key) DO UPDATE
		  SET payload = graph_node.payload || EXCLUDED.payload
		RETURNING id`, engagementID, kind, []byte(payload), dedupKey).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("upsert node: %w", err)
	}
	return id, nil
}

func (s *Store) UpsertEdge(ctx context.Context, engagementID, fromID, toID, kind string, payload json.RawMessage) (string, error) {
	if payload == nil {
		payload = json.RawMessage("{}")
	}
	var id string
	err := s.pool.QueryRow(ctx, `
		INSERT INTO graph_edge (engagement_id, from_id, to_id, kind, payload)
		VALUES ($1,$2,$3,$4,$5)
		ON CONFLICT (engagement_id, from_id, to_id, kind) DO UPDATE
		  SET payload = graph_edge.payload || EXCLUDED.payload
		RETURNING id`, engagementID, fromID, toID, kind, []byte(payload)).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("upsert edge: %w", err)
	}
	return id, nil
}
```

- [ ] **Step 4: 跑测试 + Commit**

```bash
go test -tags=integration ./internal/graph/... -race -count=1
git add internal/graph
git commit -m "feat(graph): node/edge store（ON CONFLICT 部分 UNIQUE 去重）"
```

---

## Task 10: internal/flow — http_flow store

**Files:**
- Create: `internal/flow/model.go`
- Create: `internal/flow/store.go`
- Create: `internal/flow/store_integration_test.go`

- [ ] **Step 1: 写失败测试**

```go
//go:build integration

package flow

import (
	"bytes"
	"context"
	"testing"

	"github.com/V3teran/liusha/internal/dbtest"
	"github.com/V3teran/liusha/internal/engagement"
)

func TestStore_Append_Truncate(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.NewPgPool(t)
	e, _ := engagement.NewStore(pool).LookupOrCreate(ctx, "default", "h", engagement.ModeProxy)
	s := NewStore(pool, 1024, 2048)

	big := bytes.Repeat([]byte("x"), 5000)
	id, err := s.Append(ctx, Flow{
		EngagementID: e.ID, Method: "POST", URL: "/api/x",
		RequestHeaders: []byte(`{}`), RequestBody: big,
		StatusCode: 200, ResponseHeaders: []byte(`{}`), ResponseBody: big,
	})
	if err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetByID(ctx, id)
	if !got.RequestTruncated || !got.ResponseTruncated {
		t.Fatalf("expect truncated flags true, got req=%v resp=%v", got.RequestTruncated, got.ResponseTruncated)
	}
	if len(got.RequestBody) != 1024 || len(got.ResponseBody) != 2048 {
		t.Fatalf("body sizes: req=%d resp=%d", len(got.RequestBody), len(got.ResponseBody))
	}
}
```

- [ ] **Step 2: 实现 `internal/flow/model.go`**

```go
package flow

import (
	"encoding/json"
	"time"
)

type Flow struct {
	ID                int64
	EngagementID      string
	Ts                time.Time
	Method            string
	URL               string
	RequestHeaders    json.RawMessage
	RequestBody       []byte
	RequestTruncated  bool
	StatusCode        int
	ResponseHeaders   json.RawMessage
	ResponseBody      []byte
	ResponseTruncated bool
}
```

- [ ] **Step 3: 实现 `internal/flow/store.go`**

```go
package flow

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

type Store struct {
	pool        *pgxpool.Pool
	maxReqBody  int
	maxRespBody int
}

func NewStore(p *pgxpool.Pool, maxReqBody, maxRespBody int) *Store {
	return &Store{pool: p, maxReqBody: maxReqBody, maxRespBody: maxRespBody}
}

func (s *Store) Append(ctx context.Context, f Flow) (int64, error) {
	reqBody, reqTrunc := truncate(f.RequestBody, s.maxReqBody)
	respBody, respTrunc := truncate(f.ResponseBody, s.maxRespBody)
	if f.RequestHeaders == nil {
		f.RequestHeaders = json.RawMessage("{}")
	}
	if f.ResponseHeaders == nil {
		f.ResponseHeaders = json.RawMessage("{}")
	}
	var id int64
	err := s.pool.QueryRow(ctx, `
		INSERT INTO http_flow
			(engagement_id, method, url, request_headers, request_body, request_truncated,
			 status_code, response_headers, response_body, response_truncated)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
		RETURNING id`,
		f.EngagementID, f.Method, f.URL,
		[]byte(f.RequestHeaders), reqBody, reqTrunc,
		f.StatusCode, []byte(f.ResponseHeaders), respBody, respTrunc).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("append flow: %w", err)
	}
	return id, nil
}

func (s *Store) GetByID(ctx context.Context, id int64) (Flow, error) {
	row := s.pool.QueryRow(ctx, `
		SELECT id, engagement_id, ts, method, url, request_headers, request_body,
		       request_truncated, status_code, response_headers, response_body, response_truncated
		FROM http_flow WHERE id=$1`, id)
	var f Flow
	var reqH, respH []byte
	if err := row.Scan(&f.ID, &f.EngagementID, &f.Ts, &f.Method, &f.URL,
		&reqH, &f.RequestBody, &f.RequestTruncated,
		&f.StatusCode, &respH, &f.ResponseBody, &f.ResponseTruncated); err != nil {
		return Flow{}, err
	}
	f.RequestHeaders, f.ResponseHeaders = reqH, respH
	return f, nil
}

func truncate(b []byte, max int) ([]byte, bool) {
	if max <= 0 || len(b) <= max {
		return b, false
	}
	out := make([]byte, max)
	copy(out, b[:max])
	return out, true
}
```

- [ ] **Step 4: 跑测试 + Commit**

```bash
go test -tags=integration ./internal/flow/... -race -count=1
git add internal/flow
git commit -m "feat(flow): http_flow store + 大 body 截断"
```

---

## Task 11: internal/finding — UNIQUE + ON CONFLICT 合并 evidence

**Files:**
- Create: `internal/finding/model.go`
- Create: `internal/finding/store.go`
- Create: `internal/finding/store_integration_test.go`

- [ ] **Step 1: 写失败测试**

```go
//go:build integration

package finding

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/V3teran/liusha/internal/dbtest"
	"github.com/V3teran/liusha/internal/engagement"
)

func TestStore_Upsert_DedupMergeEvidence(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.NewPgPool(t)
	e, _ := engagement.NewStore(pool).LookupOrCreate(ctx, "default", "h", engagement.ModeProxy)
	s := NewStore(pool)

	dk := "bac.horizontal_priv_esc:vulnapp:GET:/api/order/:oid"
	a, err := s.Upsert(ctx, Finding{
		EngagementID: e.ID, Kind: "bac.horizontal_priv_esc",
		Severity: SeverityHigh, Title: "GET /api/order/:oid",
		Target:   json.RawMessage(`{"url":"/api/order/7"}`),
		Evidence: json.RawMessage(`{"violating":["test"]}`),
		DedupKey: dk,
	})
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.Upsert(ctx, Finding{
		EngagementID: e.ID, Kind: "bac.horizontal_priv_esc",
		Severity: SeverityHigh, Title: "GET /api/order/:oid",
		Target:   json.RawMessage(`{"url":"/api/order/9"}`),
		Evidence: json.RawMessage(`{"violating":["m233241"]}`),
		DedupKey: dk,
	})
	if err != nil {
		t.Fatal(err)
	}
	if a != b {
		t.Fatalf("expect same id, got %s vs %s", a, b)
	}
	all, _ := s.ListByEngagement(ctx, e.ID)
	if len(all) != 1 {
		t.Fatalf("expect 1 row after dedup, got %d", len(all))
	}
}
```

- [ ] **Step 2: 实现 `internal/finding/model.go`**

```go
package finding

import (
	"encoding/json"
	"time"
)

type Severity string

const (
	SeverityInfo     Severity = "info"
	SeverityLow      Severity = "low"
	SeverityMedium   Severity = "medium"
	SeverityHigh     Severity = "high"
	SeverityCritical Severity = "critical"
)

type Confidence string

const (
	ConfidenceUnverified Confidence = "unverified"
	ConfidenceVerified   Confidence = "verified"
	ConfidenceRejected   Confidence = "rejected"
)

type Finding struct {
	ID           string
	EngagementID string
	TaskID       *string
	Kind         string
	Severity     Severity
	Title        string
	Target       json.RawMessage
	Evidence     json.RawMessage
	Payload      json.RawMessage
	Tool         string
	Confidence   Confidence
	DedupKey     string
	CreatedAt    time.Time
	UpdatedAt    time.Time
}
```

- [ ] **Step 3: 实现 `internal/finding/store.go`**

```go
package finding

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

type Store struct{ pool *pgxpool.Pool }

func NewStore(p *pgxpool.Pool) *Store { return &Store{pool: p} }

func (s *Store) Upsert(ctx context.Context, f Finding) (string, error) {
	if f.Severity == "" {
		f.Severity = SeverityMedium
	}
	if f.Confidence == "" {
		f.Confidence = ConfidenceUnverified
	}
	for _, p := range []*json.RawMessage{&f.Target, &f.Evidence, &f.Payload} {
		if *p == nil {
			*p = json.RawMessage("{}")
		}
	}
	var id string
	err := s.pool.QueryRow(ctx, `
		INSERT INTO finding
			(engagement_id, task_id, kind, severity, title, target, evidence, payload, tool, confidence, dedup_key)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
		ON CONFLICT (engagement_id, dedup_key) DO UPDATE
		  SET evidence  = finding.evidence || EXCLUDED.evidence,
		      severity  = GREATEST(finding.severity, EXCLUDED.severity),
		      confidence = CASE
		         WHEN finding.confidence='verified' OR EXCLUDED.confidence='verified' THEN 'verified'
		         WHEN EXCLUDED.confidence='rejected' THEN 'rejected'
		         ELSE finding.confidence END,
		      updated_at = now()
		RETURNING id`,
		f.EngagementID, f.TaskID, f.Kind, f.Severity, f.Title,
		[]byte(f.Target), []byte(f.Evidence), []byte(f.Payload),
		f.Tool, f.Confidence, f.DedupKey).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("upsert finding: %w", err)
	}
	return id, nil
}

func (s *Store) ListByEngagement(ctx context.Context, engagementID string) ([]Finding, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, engagement_id, task_id, kind, severity, title, target, evidence, payload,
		       tool, confidence, dedup_key, created_at, updated_at
		FROM finding WHERE engagement_id=$1 ORDER BY created_at ASC`, engagementID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Finding
	for rows.Next() {
		var f Finding
		var target, evidence, payload []byte
		if err := rows.Scan(&f.ID, &f.EngagementID, &f.TaskID, &f.Kind, &f.Severity, &f.Title,
			&target, &evidence, &payload, &f.Tool, &f.Confidence, &f.DedupKey, &f.CreatedAt, &f.UpdatedAt); err != nil {
			return nil, err
		}
		f.Target, f.Evidence, f.Payload = target, evidence, payload
		out = append(out, f)
	}
	return out, rows.Err()
}
```

- [ ] **Step 4: 跑测试 + Commit**

```bash
go test -tags=integration ./internal/finding/... -race -count=1
git add internal/finding
git commit -m "feat(finding): store + UNIQUE(engagement_id, dedup_key) ON CONFLICT 合并"
```

---

## Task 12: internal/llmcall — llm_call store

**Files:**
- Create: `internal/llmcall/model.go`
- Create: `internal/llmcall/store.go`
- Create: `internal/llmcall/store_integration_test.go`

- [ ] **Step 1: 写失败测试**

```go
//go:build integration

package llmcall

import (
	"context"
	"testing"

	"github.com/V3teran/liusha/internal/dbtest"
	"github.com/V3teran/liusha/internal/engagement"
)

func TestStore_Insert(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.NewPgPool(t)
	e, _ := engagement.NewStore(pool).LookupOrCreate(ctx, "default", "h", engagement.ModeProxy)
	s := NewStore(pool)

	id, err := s.Insert(ctx, Call{
		EngagementID: &e.ID,
		Provider:     "deepseek",
		Model:        "deepseek-chat",
		InTokens:     1200, OutTokens: 300, CachedTokens: 100,
		CostUSD:   0.00041,
		LatencyMs: 850,
		FinishReason: "stop",
	})
	if err != nil || id == 0 {
		t.Fatalf("insert failed id=%d err=%v", id, err)
	}
}
```

- [ ] **Step 2: 实现 `internal/llmcall/model.go`**

```go
package llmcall

import "time"

type Call struct {
	ID           int64
	TaskID       *string
	EngagementID *string
	Provider     string
	Model        string
	InTokens     int
	OutTokens    int
	CachedTokens int
	CostUSD      float64
	LatencyMs    int
	FinishReason string
	Error        string
	CreatedAt    time.Time
}
```

- [ ] **Step 3: 实现 `internal/llmcall/store.go`**

```go
package llmcall

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

type Store struct{ pool *pgxpool.Pool }

func NewStore(p *pgxpool.Pool) *Store { return &Store{pool: p} }

func (s *Store) Insert(ctx context.Context, c Call) (int64, error) {
	var id int64
	err := s.pool.QueryRow(ctx, `
		INSERT INTO llm_call
			(task_id, engagement_id, provider, model, in_tokens, out_tokens, cached_tokens,
			 cost_usd, latency_ms, finish_reason, error)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
		RETURNING id`,
		c.TaskID, c.EngagementID, c.Provider, c.Model,
		c.InTokens, c.OutTokens, c.CachedTokens,
		c.CostUSD, c.LatencyMs, c.FinishReason, c.Error).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("insert llm_call: %w", err)
	}
	return id, nil
}

func (s *Store) SumCostUSD(ctx context.Context, engagementID string) (float64, error) {
	var v float64
	err := s.pool.QueryRow(ctx, `SELECT COALESCE(SUM(cost_usd),0) FROM llm_call WHERE engagement_id=$1`, engagementID).Scan(&v)
	return v, err
}
```

- [ ] **Step 4: 跑测试 + Commit**

```bash
go test -tags=integration ./internal/llmcall/... -race -count=1
git add internal/llmcall
git commit -m "feat(llmcall): store + SumCostUSD"
```

---

## Task 13: internal/credential — Provider + Redis 实现

**Files:**
- Create: `internal/credential/model.go`
- Create: `internal/credential/provider.go`
- Create: `internal/credential/redis.go`
- Create: `internal/credential/redis_test.go`

- [ ] **Step 1: 写失败测试 `internal/credential/redis_test.go`**

```go
package credential

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func newClient(t *testing.T) *redis.Client {
	t.Helper()
	mr := miniredis.RunT(t)
	return redis.NewClient(&redis.Options{Addr: mr.Addr()})
}

func TestRedis_BatchSaveAndGet_AlwaysIncludesAnonymous(t *testing.T) {
	ctx := context.Background()
	p := NewRedis(newClient(t))

	in := map[string][]Identity{
		"vulnapp": {
			{Name: "admin", Role: "admin", Credentials: []Credential{{Type: TypeHeaders, Key: "Cookie", Value: "session=admin_sess_a1b2c3"}}},
			{Name: "test", Role: "user", Credentials: []Credential{{Type: TypeHeaders, Key: "Cookie", Value: "session=test_sess_d4e5f6"}}},
		},
	}
	if err := p.BatchSave(ctx, in, 0); err != nil {
		t.Fatal(err)
	}
	got, err := p.GetIdentitiesByHost(ctx, "vulnapp")
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, id := range got {
		names = append(names, id.Name)
	}
	want := []string{"admin", "anonymous", "test"}
	if !equalSorted(names, want) {
		t.Fatalf("identities=%v want=%v", names, want)
	}
}

func TestRedis_TTL(t *testing.T) {
	ctx := context.Background()
	c := newClient(t)
	p := NewRedis(c)
	if err := p.BatchSave(ctx, map[string][]Identity{"h": {{Name: "u"}}}, 1); err != nil {
		t.Fatal(err)
	}
	d, _ := c.TTL(ctx, "liusha:credential:h:u").Result()
	if d <= 0 || d > 2*time.Second {
		t.Fatalf("ttl=%v", d)
	}
}

func equalSorted(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	m := map[string]int{}
	for _, s := range a {
		m[s]++
	}
	for _, s := range b {
		m[s]--
	}
	for _, n := range m {
		if n != 0 {
			return false
		}
	}
	return true
}
```

- [ ] **Step 2: 实现 `internal/credential/model.go`**

```go
package credential

const AnonymousName = "anonymous"

type CredentialType string

const (
	TypeHeaders CredentialType = "headers"
	TypeQuery   CredentialType = "query"
	TypeBody    CredentialType = "body"
)

type Credential struct {
	Type  CredentialType `json:"type"`
	Key   string         `json:"key"`
	Value string         `json:"value"`
}

type Identity struct {
	Name        string       `json:"name"`
	Role        string       `json:"role"`
	Credentials []Credential `json:"credentials"`
}
```

- [ ] **Step 3: 实现 `internal/credential/provider.go`**

```go
package credential

import "context"

type Provider interface {
	BatchSave(ctx context.Context, byHost map[string][]Identity, ttlSeconds int) error
	GetIdentitiesByHost(ctx context.Context, host string) ([]Identity, error)
	List(ctx context.Context, host string) (map[string][]Identity, error)
	Delete(ctx context.Context, host string) error
}
```

- [ ] **Step 4: 实现 `internal/credential/redis.go`**

```go
package credential

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

type Redis struct{ c *redis.Client }

func NewRedis(c *redis.Client) *Redis { return &Redis{c: c} }

func key(host, name string) string {
	return fmt.Sprintf("liusha:credential:%s:%s", host, name)
}

func (r *Redis) BatchSave(ctx context.Context, byHost map[string][]Identity, ttlSeconds int) error {
	pipe := r.c.Pipeline()
	for host, ids := range byHost {
		for _, id := range ids {
			if id.Name == AnonymousName {
				continue
			}
			b, err := json.Marshal(id)
			if err != nil {
				return fmt.Errorf("marshal identity %s/%s: %w", host, id.Name, err)
			}
			ttl := time.Duration(ttlSeconds) * time.Second
			pipe.Set(ctx, key(host, id.Name), b, ttl)
		}
	}
	_, err := pipe.Exec(ctx)
	return err
}

func (r *Redis) GetIdentitiesByHost(ctx context.Context, host string) ([]Identity, error) {
	pattern := fmt.Sprintf("liusha:credential:%s:*", host)
	var (
		cursor uint64
		out    = []Identity{{Name: AnonymousName, Role: "anonymous"}}
	)
	for {
		keys, next, err := r.c.Scan(ctx, cursor, pattern, 100).Result()
		if err != nil {
			return nil, fmt.Errorf("scan credentials: %w", err)
		}
		for _, k := range keys {
			val, err := r.c.Get(ctx, k).Bytes()
			if err != nil {
				continue
			}
			var id Identity
			if err := json.Unmarshal(val, &id); err == nil {
				out = append(out, id)
			}
		}
		if next == 0 {
			break
		}
		cursor = next
	}
	return out, nil
}

func (r *Redis) List(ctx context.Context, host string) (map[string][]Identity, error) {
	ids, err := r.GetIdentitiesByHost(ctx, host)
	if err != nil {
		return nil, err
	}
	return map[string][]Identity{host: ids}, nil
}

func (r *Redis) Delete(ctx context.Context, host string) error {
	pattern := fmt.Sprintf("liusha:credential:%s:*", host)
	var cursor uint64
	for {
		keys, next, err := r.c.Scan(ctx, cursor, pattern, 100).Result()
		if err != nil {
			return err
		}
		if len(keys) > 0 {
			if err := r.c.Del(ctx, keys...).Err(); err != nil {
				return err
			}
		}
		if next == 0 {
			return nil
		}
		cursor = next
	}
}
```

- [ ] **Step 5: 跑测试 + Commit**

```bash
go test ./internal/credential/... -race
git add internal/credential
git commit -m "feat(credential): Provider + Redis（anonymous 自动注入 + TTL）"
```

---

## Task 14: internal/replay — 凭证替换 + 并发重放

**Files:**
- Create: `internal/replay/raw.go`
- Create: `internal/replay/engine.go`
- Create: `internal/replay/engine_test.go`

- [ ] **Step 1: 写失败测试**

```go
package replay

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/V3teran/liusha/internal/credential"
)

func TestEngine_ReplayWithIdentity_HeadersSwap(t *testing.T) {
	var seenCookie string
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		seenCookie = r.Header.Get("Cookie")
	}))
	defer srv.Close()

	raw := RawRequest{Method: "GET", URL: srv.URL + "/x",
		Headers: http.Header{"Cookie": {"session=admin_sess_a1b2c3"}}}
	id := credential.Identity{
		Name: "test",
		Credentials: []credential.Credential{
			{Type: credential.TypeHeaders, Key: "Cookie", Value: "session=test_sess_d4e5f6"},
		},
	}
	if _, err := NewEngine(srv.Client()).ReplayWithIdentity(context.Background(), raw, id); err != nil {
		t.Fatal(err)
	}
	if seenCookie != "session=test_sess_d4e5f6" {
		t.Fatalf("cookie=%q", seenCookie)
	}
}

func TestEngine_ReplayWithIdentity_QueryAndBody(t *testing.T) {
	var seenURL, seenBody string
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		seenURL = r.URL.String()
		b, _ := io.ReadAll(r.Body)
		seenBody = string(b)
	}))
	defer srv.Close()

	raw := RawRequest{
		Method: "POST", URL: srv.URL + "/api?token=OLD",
		Headers: http.Header{"Content-Type": {"application/x-www-form-urlencoded"}},
		Body:    []byte("name=alice&token=OLD"),
	}
	id := credential.Identity{
		Name: "u",
		Credentials: []credential.Credential{
			{Type: credential.TypeQuery, Key: "token", Value: "Q"},
			{Type: credential.TypeBody, Key: "token", Value: "B"},
		},
	}
	if _, err := NewEngine(srv.Client()).ReplayWithIdentity(context.Background(), raw, id); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(seenURL, "token=Q") {
		t.Fatalf("query not swapped: %q", seenURL)
	}
	if !strings.Contains(seenBody, "token=B") {
		t.Fatalf("body not swapped: %q", seenBody)
	}
}

func TestEngine_ReplayMultiIdentity_Concurrency(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(200)
	}))
	defer srv.Close()
	raw := RawRequest{Method: "GET", URL: srv.URL + "/", Headers: http.Header{}}
	ids := []credential.Identity{{Name: "a"}, {Name: "b"}, {Name: "c"}}
	res, err := NewEngine(srv.Client()).ReplayMultiIdentity(context.Background(), raw, ids, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 3 {
		t.Fatalf("len=%d", len(res))
	}
}
```

- [ ] **Step 2: 实现 `internal/replay/raw.go`**

```go
package replay

import "net/http"

type RawRequest struct {
	Method  string
	URL     string
	Headers http.Header
	Body    []byte
}

type Response struct {
	IdentityName string
	StatusCode   int
	Headers      http.Header
	Body         []byte
	ErrorMessage string
}
```

- [ ] **Step 3: 实现 `internal/replay/engine.go`**

```go
package replay

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sync"

	"github.com/V3teran/liusha/internal/credential"
)

type Engine struct{ client *http.Client }

func NewEngine(c *http.Client) *Engine {
	if c == nil {
		c = http.DefaultClient
	}
	return &Engine{client: c}
}

func (e *Engine) ReplayWithIdentity(ctx context.Context, raw RawRequest, id credential.Identity) (Response, error) {
	finalURL, body, headers, err := applyIdentity(raw, id)
	if err != nil {
		return Response{IdentityName: id.Name, ErrorMessage: err.Error()}, err
	}
	req, err := http.NewRequestWithContext(ctx, raw.Method, finalURL, bytes.NewReader(body))
	if err != nil {
		return Response{IdentityName: id.Name, ErrorMessage: err.Error()}, err
	}
	req.Header = headers
	resp, err := e.client.Do(req)
	if err != nil {
		return Response{IdentityName: id.Name, ErrorMessage: err.Error()}, err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return Response{
		IdentityName: id.Name,
		StatusCode:   resp.StatusCode,
		Headers:      resp.Header,
		Body:         b,
	}, nil
}

func (e *Engine) ReplayMultiIdentity(ctx context.Context, raw RawRequest, ids []credential.Identity, concurrency int) ([]Response, error) {
	if concurrency <= 0 {
		concurrency = 5
	}
	out := make([]Response, len(ids))
	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup
	for i, id := range ids {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int, id credential.Identity) {
			defer wg.Done()
			defer func() { <-sem }()
			r, _ := e.ReplayWithIdentity(ctx, raw, id)
			out[i] = r
		}(i, id)
	}
	wg.Wait()
	return out, nil
}

func applyIdentity(raw RawRequest, id credential.Identity) (string, []byte, http.Header, error) {
	headers := raw.Headers.Clone()
	if headers == nil {
		headers = http.Header{}
	}
	body := append([]byte(nil), raw.Body...)
	parsed, err := url.Parse(raw.URL)
	if err != nil {
		return "", nil, nil, fmt.Errorf("parse url: %w", err)
	}
	q := parsed.Query()

	var bodyForm url.Values
	if headers.Get("Content-Type") == "application/x-www-form-urlencoded" {
		bodyForm, _ = url.ParseQuery(string(body))
	}

	for _, c := range id.Credentials {
		switch c.Type {
		case credential.TypeHeaders:
			if c.Value == "" {
				headers.Del(c.Key)
			} else {
				headers.Set(c.Key, c.Value)
			}
		case credential.TypeQuery:
			if c.Value == "" {
				q.Del(c.Key)
			} else {
				q.Set(c.Key, c.Value)
			}
		case credential.TypeBody:
			if bodyForm == nil {
				continue
			}
			if c.Value == "" {
				bodyForm.Del(c.Key)
			} else {
				bodyForm.Set(c.Key, c.Value)
			}
		}
	}
	parsed.RawQuery = q.Encode()
	if bodyForm != nil {
		body = []byte(bodyForm.Encode())
	}
	if id.Name == credential.AnonymousName {
		headers.Del("Cookie")
		headers.Del("Authorization")
	}
	return parsed.String(), body, headers, nil
}
```

- [ ] **Step 4: 跑测试 + Commit**

```bash
go test ./internal/replay/... -race
git add internal/replay
git commit -m "feat(replay): 凭证替换（headers/query/body）+ 并发多身份重放"
```

---

## Task 15: internal/heuristic — 通用规则 + 结构相似度

**Files:**
- Create: `internal/heuristic/rules.go`
- Create: `internal/heuristic/similarity.go`
- Create: `internal/heuristic/heuristic_test.go`

- [ ] **Step 1: 写失败测试**

```go
package heuristic

import (
	"testing"

	"github.com/V3teran/liusha/internal/replay"
)

func TestAllDeniedByStatus(t *testing.T) {
	rs := []replay.Response{{StatusCode: 401}, {StatusCode: 403}}
	skip, _ := AllDeniedByStatus(rs)
	if !skip {
		t.Fatal("expected skip on 401/403")
	}
	rs = []replay.Response{{StatusCode: 200}, {StatusCode: 403}}
	skip, _ = AllDeniedByStatus(rs)
	if skip {
		t.Fatal("expected NOT skip when one 200")
	}
}

func TestAllAuthError(t *testing.T) {
	rs := []replay.Response{
		{StatusCode: 200, Body: []byte(`{"msg":"未登录"}`)},
		{StatusCode: 200, Body: []byte(`{"msg":"login required"}`)},
	}
	skip, _ := AllAuthError(rs, nil)
	if !skip {
		t.Fatal("expected skip when all bodies match auth keywords")
	}
}

func TestStructuralSimilarity(t *testing.T) {
	if v := StructuralSimilarity("hello world", "hello world"); v != 1.0 {
		t.Fatalf("identical=1, got %v", v)
	}
	if v := StructuralSimilarity("hello world", "totally different stuff"); v > 0.3 {
		t.Fatalf("dissimilar should be low, got %v", v)
	}
}
```

- [ ] **Step 2: 实现 `internal/heuristic/rules.go`**

```go
package heuristic

import (
	"bytes"
	"strings"

	"github.com/V3teran/liusha/internal/replay"
)

type Rule func(responses []replay.Response) (skip bool, reason string)

func AllDeniedByStatus(rs []replay.Response) (bool, string) {
	for _, r := range rs {
		if r.StatusCode < 400 {
			return false, ""
		}
	}
	return true, "all responses 4xx/5xx"
}

func AllEmptyResponse(rs []replay.Response) (bool, string) {
	for _, r := range rs {
		body := bytes.TrimSpace(r.Body)
		if len(body) > 0 && string(body) != "{}" && string(body) != "[]" {
			return false, ""
		}
	}
	return true, "all responses empty"
}

var defaultAuthKeywords = []string{
	"未登录", "请登录", "登录后", "需要登录", "请先登录",
	"无权访问", "权限不足", "未授权", "无权操作", "拒绝访问", "禁止访问",
	"会话过期", "token 已过期", "token expired",
	"login required", "please login", "not authorized", "unauthorized",
	"forbidden", "permission denied", "access denied",
	"session expired", "invalid token", "authentication required", "auth required",
}

func AllAuthError(rs []replay.Response, keywords []string) (bool, string) {
	if len(keywords) == 0 {
		keywords = defaultAuthKeywords
	}
	for _, r := range rs {
		text := strings.ToLower(string(r.Body))
		hit := false
		for _, k := range keywords {
			if strings.Contains(text, strings.ToLower(k)) {
				hit = true
				break
			}
		}
		if !hit {
			return false, ""
		}
	}
	return true, "all responses contain auth-error keyword"
}
```

- [ ] **Step 3: 实现 `internal/heuristic/similarity.go`**

```go
package heuristic

import "strings"

func StructuralSimilarity(a, b string) float64 {
	if a == b {
		return 1.0
	}
	at := tokenSet(a)
	bt := tokenSet(b)
	if len(at) == 0 && len(bt) == 0 {
		return 1.0
	}
	var inter int
	for k := range at {
		if _, ok := bt[k]; ok {
			inter++
		}
	}
	union := len(at) + len(bt) - inter
	if union == 0 {
		return 0
	}
	return float64(inter) / float64(union)
}

func tokenSet(s string) map[string]struct{} {
	m := map[string]struct{}{}
	for _, t := range strings.Fields(strings.ToLower(s)) {
		m[t] = struct{}{}
	}
	return m
}
```

- [ ] **Step 4: 跑测试 + Commit**

```bash
go test ./internal/heuristic/... -race
git add internal/heuristic
git commit -m "feat(heuristic): 三条 Rule + token Jaccard 相似度"
```

---

## Task 16: ~~internal/proxy_filter~~（已删除）

> 流量过滤交给 proxify DSL（spec §7.4），不在 liusha 写责任链。本任务跳过。

---

> Plan 1 part 3 续 `2026-04-28-liusha-v1-plan-1-foundation-part3.md`：T17-T32（LLM provider / Agent runtime / actions / skill / worker / httpapi / cmd / smoke）。
