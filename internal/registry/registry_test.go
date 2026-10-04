package registry

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/V3teran/liusha/internal/framework/llm"
)

// fakeTool 是可编程的最小 Tool：记录调用次数，返回预设结果。
type fakeTool struct {
	BaseTool
	name   string
	calls  int
	mu     sync.Mutex
	output string
	resErr string
	err    error
	sleep  time.Duration // >0 时模拟慢工具（配合 Timeout 链）
}

func (f *fakeTool) Name() string            { return f.name }
func (f *fakeTool) ShortDesc() string       { return "fake " + f.name }
func (f *fakeTool) Desc() string            { return "fake " + f.name + " long" }
func (f *fakeTool) Schema() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }

func (f *fakeTool) Execute(ctx context.Context, _ json.RawMessage) (ToolResult, error) {
	f.mu.Lock()
	f.calls++
	f.mu.Unlock()
	if f.sleep > 0 {
		// 模拟真实 ctx-aware 工具：超时/取消时返回 ctx.Err()（如 http 的 RequestWithContext）
		select {
		case <-time.After(f.sleep):
		case <-ctx.Done():
			return ToolResult{}, ctx.Err()
		}
	}
	return ToolResult{Output: f.output, Error: f.resErr}, f.err
}

// Register + Get + Schemas：注册可见、重名后写者胜。
func TestRegistry_RegisterAndGet(t *testing.T) {
	r := New()
	a := &fakeTool{name: "tool_a", output: "A"}
	b := &fakeTool{name: "tool_b", output: "B"}
	r.Register(a)
	r.Register(b)

	if got, ok := r.Get("tool_a"); !ok || got.Name() != "tool_a" {
		t.Fatalf("Get(tool_a) 应命中, got %v %v", got, ok)
	}
	if _, ok := r.Get("missing"); ok {
		t.Fatal("Get(missing) 不应命中")
	}

	// 重复注册：后写者胜（Map 语义），不 panic 不报错
	a2 := &fakeTool{name: "tool_a", output: "A2"}
	r.Register(a2)
	if got, _ := r.Get("tool_a"); got != Tool(a2) {
		t.Fatalf("重复注册应后写者胜")
	}

	if schemas := r.Schemas(); len(schemas) != 2 {
		t.Fatalf("Schemas 应含 2 个工具, got %d", len(schemas))
	}
}

// Interceptor 链按注册顺序执行（先注册在外层），且能改写结果。
// interceptor 必须沿链透传 tool 参数——wrappedTool 的 final 闭包用它执行真实工具。
func TestRegistry_InterceptorChain(t *testing.T) {
	r := New()
	var order []string
	var mu sync.Mutex
	record := func(s string) { mu.Lock(); order = append(order, s); mu.Unlock() }

	r.AddInterceptor(func(_ context.Context, tool Tool, args []byte, next ExecuteFunc) (ToolResult, error) {
		record("first-in")
		res, err := next(context.Background(), tool, args)
		record("first-out")
		res.Output += "|wrapped"
		return res, err
	})
	r.AddInterceptor(func(_ context.Context, tool Tool, args []byte, next ExecuteFunc) (ToolResult, error) {
		record("second-in")
		return next(context.Background(), tool, args)
	})
	inner := &fakeTool{name: "x", output: "core"}

	res, err := r.WrapTool(inner).Execute(context.Background(), nil)
	if err != nil {
		t.Fatalf("执行不应报错: %v", err)
	}
	if res.Output != "core|wrapped" {
		t.Fatalf("最外层 interceptor 应能改写输出, got %q", res.Output)
	}
	want := []string{"first-in", "second-in", "first-out"}
	if len(order) != 3 || strings.Join(order, ",") != strings.Join(want, ",") {
		t.Fatalf("链顺序应为 first→second→回程, got %v", order)
	}
	if inner.calls != 1 {
		t.Fatalf("真实工具应被链末端执行 1 次, got %d", inner.calls)
	}
}

// WrapTool 语义：工具 Go error 经默认 ErrorMask 转为 res.Error 字符串
// （ReAct 观察通道只认字符串；error 直返会在 runtime 层丢工具语义）。
func TestRegistry_WrapToolMasksError(t *testing.T) {
	r := New()
	boom := &fakeTool{name: "boom", err: errors.New("disk on fire")}

	res, err := r.WrapTool(boom).Execute(context.Background(), nil)
	if err != nil {
		t.Fatalf("WrapTool 应吞 err 转 res.Error, got %v", err)
	}
	if res.Error == "" || !strings.Contains(res.Error, "disk on fire") {
		t.Fatalf("res.Error 应携带原始错误语义, got %q", res.Error)
	}
	if boom.calls != 1 {
		t.Fatalf("底层工具应被调用 1 次, got %d", boom.calls)
	}
}

// 默认链的 Timeout interceptor：工具执行超过其超时应产生错误而非挂死。
func TestRegistry_TimeoutInterceptor(t *testing.T) {
	r := New()
	slow := &fakeTool{name: "slow", output: "late"}
	slow.sleep = 300 * time.Millisecond
	r.Register(slow)

	// 链的 TimeoutInterceptor 从 ctx 读超时（WithToolTimeout 注入），非 tool.Timeout()。
	// deadline 触发时 ErrorMask 刻意上抛 Go error（中断 ReAct）而非转 res.Error。
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := r.WrapTool(slow).Execute(ctx, nil)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("超时应上抛 context.DeadlineExceeded, got %v", err)
	}
}

// ExecuteParallel：并发执行多个调用，结果按入参顺序返回。
func TestRegistry_ExecuteParallel(t *testing.T) {
	r := New()
	a := &fakeTool{name: "a", output: "A"}
	b := &fakeTool{name: "b", resErr: "B-bad"}
	r.Register(a)
	r.Register(b)

	calls := []llm.ToolCall{
		{ID: "c1", Name: "a", Arguments: json.RawMessage(`{}`)},
		{ID: "c2", Name: "b", Arguments: json.RawMessage(`{}`)},
		{ID: "c3", Name: "missing", Arguments: json.RawMessage(`{}`)},
	}
	results := r.ExecuteParallel(context.Background(), calls)
	if len(results) != 3 {
		t.Fatalf("应有 3 个结果, got %d", len(results))
	}
	if results[0].Output != "A" {
		t.Fatalf("结果应按入参顺序: results[0]=%q", results[0].Output)
	}
	if results[1].Error == "" {
		t.Fatal("工具级错误应进 results[1].Error")
	}
	if results[2].Error == "" {
		t.Fatal("未注册工具应产生错误而非静默")
	}
}
