package proxystatus

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newStore(t *testing.T) (*Store, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	return New(rdb), mr
}

func TestRecord_Online(t *testing.T) {
	now := time.Now()
	stopped := now.Add(-time.Second)
	cases := []struct {
		name string
		r    Record
		want bool
	}{
		{"心跳新鲜", Record{HeartbeatAt: now.Add(-HeartbeatInterval)}, true},
		{"心跳中断超过三个周期", Record{HeartbeatAt: now.Add(-offlineAfter - time.Second)}, false},
		{"已正常退出", Record{HeartbeatAt: now, StoppedAt: &stopped}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) { assert.Equal(t, c.want, c.r.Online(now)) })
	}
}

func TestStore_PutGet(t *testing.T) {
	store, _ := newStore(t)
	ctx := context.Background()

	_, ok, err := store.Get(ctx)
	require.NoError(t, err)
	assert.False(t, ok, "从未上报过")

	last := time.Now().Add(-time.Minute).UTC().Round(time.Millisecond)
	want := Record{StartedAt: last, HeartbeatAt: last, LastRequestAt: &last, PublicAddr: ":58588", CAPEM: "pem", CAFingerprint: "AB"}
	require.NoError(t, store.Put(ctx, want))
	got, ok, err := store.Get(ctx)
	require.NoError(t, err)
	require.True(t, ok)
	assert.True(t, got.StartedAt.Equal(want.StartedAt))
	assert.True(t, got.LastRequestAt.Equal(last))
	assert.Nil(t, got.StoppedAt)
	assert.Equal(t, ":58588", got.PublicAddr)
	assert.Equal(t, "pem", got.CAPEM)
}

// 记录不设过期：代理停掉后仍能读到最后状态与证书。
func TestStore_RecordDoesNotExpire(t *testing.T) {
	store, mr := newStore(t)
	require.NoError(t, store.Put(context.Background(), Record{HeartbeatAt: time.Now()}))
	assert.Equal(t, time.Duration(0), mr.TTL(key))
}

// 启动即上报；ctx 取消后写入停止时间，记录随之判为离线。
func TestReport_WritesHeartbeatAndStop(t *testing.T) {
	store, _ := newStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		Report(ctx, store, func() Record { return Record{PublicAddr: ":8588", CAPEM: "pem"} }, zerolog.Nop())
		close(done)
	}()

	require.Eventually(t, func() bool {
		r, ok, err := store.Get(context.Background())
		return err == nil && ok && r.Online(time.Now())
	}, 2*time.Second, 10*time.Millisecond, "启动后应立即上报")

	cancel()
	<-done
	r, ok, err := store.Get(context.Background())
	require.NoError(t, err)
	require.True(t, ok)
	require.NotNil(t, r.StoppedAt)
	assert.False(t, r.Online(time.Now()))
	assert.Equal(t, "pem", r.CAPEM, "退出后证书仍可下载")
}
