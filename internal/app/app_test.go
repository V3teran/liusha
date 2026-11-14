package app

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

// recorder 记录组件的停止顺序。
type recorder struct {
	mu      sync.Mutex
	stopped []string
}

func (r *recorder) add(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.stopped = append(r.stopped, name)
}

func (r *recorder) order() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.stopped...)
}

// blocking 返回一个阻塞到 ctx 取消的组件；停止前等 linger，模拟排空在途任务。
func blocking(name string, rec *recorder, linger time.Duration) Component {
	return Component{Name: name, Run: func(ctx context.Context) error {
		<-ctx.Done()
		time.Sleep(linger)
		rec.add(name)
		return nil
	}}
}

// 父 ctx 取消：先停后面的层并等其完全退出，再停前面的层。
func TestServe_StopsLayersInReverseOrder(t *testing.T) {
	rec := &recorder{}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- Serve(ctx,
			[]Component{blocking("sandbox", rec, 0)},
			[]Component{blocking("runner", rec, 50*time.Millisecond), blocking("api", rec, 0)},
		)
	}()
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("正常停止不应报错: %v", err)
	}
	got := rec.order()
	if len(got) != 3 || got[2] != "sandbox" {
		t.Fatalf("sandbox 应最后停止（runner 排空期间仍在服务），停止顺序 = %v", got)
	}
}

// 任一组件出错：整体停止并返回带组件名的错误。
func TestServe_ComponentErrorStopsAll(t *testing.T) {
	rec := &recorder{}
	boom := errors.New("listen: address already in use")
	err := Serve(context.Background(),
		[]Component{blocking("sandbox", rec, 0)},
		[]Component{
			blocking("runner", rec, 0),
			{Name: "api", Run: func(context.Context) error { return boom }},
		},
	)
	if !errors.Is(err, boom) || !strings.Contains(err.Error(), "api") {
		t.Fatalf("应返回带组件名的错误, got %v", err)
	}
	if got := rec.order(); len(got) != 2 {
		t.Fatalf("其余组件都应被停止, got %v", got)
	}
}

// 组件未被要求停止却返回 nil：视为异常退出，整体停止。
func TestServe_UnexpectedExitIsError(t *testing.T) {
	rec := &recorder{}
	err := Serve(context.Background(),
		[]Component{blocking("runner", rec, 0)},
		[]Component{{Name: "proxy", Run: func(context.Context) error { return nil }}},
	)
	if err == nil || !strings.Contains(err.Error(), "proxy 意外退出") {
		t.Fatalf("应报告异常退出, got %v", err)
	}
}
