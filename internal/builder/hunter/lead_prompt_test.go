package hunter

import (
	"context"
	"strings"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/V3teran/liusha/internal/lead"
)

func newTestLeadStore(t *testing.T) *lead.Store {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	return lead.NewStore(rdb, "test")
}

// loadLeadForPrompt 是顶层 agent（orchestrator/passive）经 BuildUserPrompt 读 lead 段的入口（§7.5）。
func TestLoadLeadForPrompt_RendersWrittenEntry(t *testing.T) {
	store := newTestLeadStore(t)
	ctx := context.Background()
	if err := store.Append(ctx, "target.com", lead.Entry{
		Kind: lead.KindFact, Note: "session cookie 不含 HttpOnly", SourceTaskID: "t1",
	}); err != nil {
		t.Fatalf("append: %v", err)
	}

	got := loadLeadForPrompt(ctx, store, "target.com")
	if !strings.Contains(got, "session cookie 不含 HttpOnly") {
		t.Fatalf("应包含刚写入的 lead: %s", got)
	}
}

func TestLoadLeadForPrompt_EmptyWhenNilOrNoHost(t *testing.T) {
	if got := loadLeadForPrompt(context.Background(), nil, "h"); got != "" {
		t.Fatalf("store nil 应返回空串: %q", got)
	}
	store := newTestLeadStore(t)
	if got := loadLeadForPrompt(context.Background(), store, ""); got != "" {
		t.Fatalf("host 空应返回空串: %q", got)
	}
}

func TestLoadLeadForPrompt_EmptyWhenNoEntries(t *testing.T) {
	store := newTestLeadStore(t)
	if got := loadLeadForPrompt(context.Background(), store, "never-written.com"); got != "" {
		t.Fatalf("无情报应返回空串: %q", got)
	}
}
