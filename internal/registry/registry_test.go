package registry

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
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

	// 链的 TimeoutInterceptor 取工具声明的 Timeout（未声明用默认 120s）。
	// deadline 触发时 ErrorMask 刻意上抛 Go error（中断 ReAct）而非转 res.Error。
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := r.WrapTool(slow).Execute(ctx, nil)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("超时应上抛 context.DeadlineExceeded, got %v", err)
	}
}

// timeoutTool 声明 30ms 超时：拦截器派生的截止期必须真正传到工具执行，
// 否则外层未设截止期时慢工具会一直跑下去。
type timeoutTool struct{ fakeTool }

func (t *timeoutTool) Timeout() time.Duration { return 30 * time.Millisecond }

func TestRegistry_TimeoutInterceptorContextReachesTool(t *testing.T) {
	r := New()
	slow := &timeoutTool{fakeTool{name: "slow-declared", output: "late", sleep: 2 * time.Second}}
	r.Register(slow)

	start := time.Now()
	_, err := r.WrapTool(slow).Execute(context.Background(), nil) // 外层无截止期
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("工具声明的超时应生效并上抛 DeadlineExceeded，得 %v", err)
	}
	if time.Since(start) > time.Second {
		t.Fatal("工具未被拦截器的截止期取消")
	}
}

// TestAllows 锁定 function_tools / cli_tools / skills 三类名单共用的唯一判定。
func TestAllows(t *testing.T) {
	cases := []struct {
		name      string
		allowlist []string
		item      string
		want      bool
	}{
		{"nil 默认拒绝", nil, "a", false},
		{"空名单为空集", []string{}, "a", false},
		{"通配放行全部", []string{"*"}, "a", true},
		{"含通配即全部", []string{"b", "*"}, "a", true},
		{"名单内放行", []string{"a", "b"}, "a", true},
		{"名单外拒绝", []string{"b"}, "a", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Allows(tc.allowlist, tc.item); got != tc.want {
				t.Fatalf("Allows(%v, %q) = %v, want %v", tc.allowlist, tc.item, got, tc.want)
			}
		})
	}
}

// 工具目录里出现的每个函数工具分类都有中文显示名（未命名会让工具页显示原始分类码）。
func TestFunctionToolCategoriesHaveNames(t *testing.T) {
	for _, meta := range FunctionToolCatalog {
		if meta.Category.Name() == string(meta.Category) {
			t.Errorf("分类 %q 缺中文显示名（工具 %s）", meta.Category, meta.Name)
		}
	}
}
