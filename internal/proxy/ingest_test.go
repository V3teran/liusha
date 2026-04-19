package proxy_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/V3teran/liusha/internal/flow"
	"github.com/V3teran/liusha/internal/proxy"
	"github.com/V3teran/liusha/internal/scope"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

type stubRedis struct {
	mu     sync.Mutex
	pushed []flow.Flow
}

func (s *stubRedis) Push(_ context.Context, f flow.Flow) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pushed = append(s.pushed, f)
	return nil
}

func (s *stubRedis) snapshot() []flow.Flow {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]flow.Flow(nil), s.pushed...)
}

type stubPostgres struct {
	mu      sync.Mutex
	batches [][]flow.Flow
}

func (s *stubPostgres) BatchInsert(_ context.Context, flows []flow.Flow) error {
	if len(flows) == 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.batches = append(s.batches, append([]flow.Flow(nil), flows...))
	return nil
}

func (s *stubPostgres) snapshot() [][]flow.Flow {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([][]flow.Flow(nil), s.batches...)
}

func TestIngestor_FiltersAndPushes(t *testing.T) {
	policy, _ := scope.NewPolicy(scope.Rules{
		Domains: []string{"example.com"},
		Methods: []string{"GET"},
	})
	rdb := &stubRedis{}
	pg := &stubPostgres{}

	ch := make(chan proxy.ParsedFlow, 10)
	ing := proxy.NewIngestor(policy, rdb, pg, proxy.IngestConfig{
		BatchSize:     5,
		FlushInterval: 50 * time.Millisecond,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	go ing.Run(ctx, ch)

	engID := uuid.New()
	now := time.Now().UTC()
	ch <- proxy.ParsedFlow{EngagementID: engID, Timestamp: now, Method: "GET", URL: "https://example.com/ok", RespStatus: 200}
	ch <- proxy.ParsedFlow{EngagementID: engID, Timestamp: now, Method: "POST", URL: "https://example.com/ok", RespStatus: 201}
	ch <- proxy.ParsedFlow{EngagementID: engID, Timestamp: now, Method: "GET", URL: "https://evil.com/x", RespStatus: 200}

	time.Sleep(200 * time.Millisecond)

	pushed := rdb.snapshot()
	require.Len(t, pushed, 1, "只有 1 条 in-scope 流量应被推入 Redis")
	require.Equal(t, "https://example.com/ok", pushed[0].URL)
	batches := pg.snapshot()
	require.Len(t, batches, 1, "1 条流量应被批量写入 Postgres")
}

func TestIngestor_BatchFlushOnCount(t *testing.T) {
	policy, _ := scope.NewPolicy(scope.Rules{Domains: []string{"example.com"}})
	rdb := &stubRedis{}
	pg := &stubPostgres{}

	ch := make(chan proxy.ParsedFlow, 20)
	ing := proxy.NewIngestor(policy, rdb, pg, proxy.IngestConfig{
		BatchSize:     3,
		FlushInterval: 10 * time.Second,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	go ing.Run(ctx, ch)

	now := time.Now().UTC()
	for i := 0; i < 3; i++ {
		ch <- proxy.ParsedFlow{EngagementID: uuid.New(), Timestamp: now, Method: "GET", URL: "https://example.com/x", RespStatus: 200}
	}

	time.Sleep(200 * time.Millisecond)
	batches := pg.snapshot()
	require.Len(t, batches, 1)
	require.Len(t, batches[0], 3)
}
