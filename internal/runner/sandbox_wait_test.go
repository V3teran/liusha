package runner

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/V3teran/liusha/internal/sandbox"
)

// inventoryStub 按顺序返回预设错误，用完后返回成功。
type inventoryStub struct {
	sandbox.Client
	errs  []error
	calls int
}

func (s *inventoryStub) Inventory(context.Context) (sandbox.Inventory, error) {
	s.calls++
	if s.calls <= len(s.errs) {
		return sandbox.Inventory{}, s.errs[s.calls-1]
	}
	return sandbox.Inventory{Tools: map[string]bool{"curl": true}}, nil
}

var errRefused = errors.New("dial tcp: connection refused")

// 沙盒短暂不可达（启动中、重启中）在等待上限内恢复：任务照常开始。
func TestAwaitInventory_RidesOutBriefOutage(t *testing.T) {
	stub := &inventoryStub{errs: []error{errRefused, &sandbox.StatusError{Code: http.StatusBadGateway}}}
	inv, err := awaitInventory(context.Background(), stub, time.Minute, time.Millisecond)
	if err != nil {
		t.Fatalf("沙盒恢复后应拿到安装状态，got %v", err)
	}
	if !inv.Tools["curl"] || stub.calls != 3 {
		t.Fatalf("inv=%+v calls=%d", inv, stub.calls)
	}
}

// 等满上限仍不可达：返回不可达错误，由调用方按出错收尾（计入重投上限，不无限重投）。
func TestAwaitInventory_GivesUpAfterWait(t *testing.T) {
	stub := &inventoryStub{errs: make([]error, 1000)}
	for i := range stub.errs {
		stub.errs[i] = errRefused
	}
	begin := time.Now()
	_, err := awaitInventory(context.Background(), stub, 50*time.Millisecond, time.Millisecond)
	if !sandbox.Unavailable(err) {
		t.Fatalf("应返回不可达错误，got %v", err)
	}
	if elapsed := time.Since(begin); elapsed > time.Second {
		t.Fatalf("等待超出上限：%v", elapsed)
	}
	if stub.calls < 2 {
		t.Fatalf("应在上限内重试，calls=%d", stub.calls)
	}
}

// 沙盒拒绝请求（4xx，配置问题不会自愈）：不等待，立即返回。
func TestAwaitInventory_RejectionIsNotRetried(t *testing.T) {
	stub := &inventoryStub{errs: []error{&sandbox.StatusError{Code: http.StatusUnauthorized}}}
	_, err := awaitInventory(context.Background(), stub, time.Minute, time.Millisecond)
	if err == nil || sandbox.Unavailable(err) || stub.calls != 1 {
		t.Fatalf("err=%v calls=%d", err, stub.calls)
	}
}

// 等待期间停机：立即返回，调用方据 ctx 按中断收尾。
func TestAwaitInventory_StopsOnShutdown(t *testing.T) {
	stub := &inventoryStub{errs: make([]error, 1000)}
	for i := range stub.errs {
		stub.errs[i] = errRefused
	}
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(20*time.Millisecond, cancel)
	begin := time.Now()
	_, err := awaitInventory(ctx, stub, time.Minute, 10*time.Millisecond)
	if err == nil || ctx.Err() == nil {
		t.Fatalf("err=%v ctx=%v", err, ctx.Err())
	}
	if elapsed := time.Since(begin); elapsed > time.Second {
		t.Fatalf("停机后仍在等待：%v", elapsed)
	}
}
