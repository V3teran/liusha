package subtask

import (
	"context"
	"sync"
)

// Registry 是父任务持有的子任务句柄集合（线程安全）。
//
// 一个父 active task 一个 Registry 实例——由 hunter builder 闭包构造，
// 注入到 spawn_child / list_children 工具及 done PreDoneCheck。
//
// wg 跟踪子 goroutine 数量，supports WaitAll —— 父 react.Run 退出（含 max_steps）
// 后 handleActive cancel 父 ctx + WaitAll 等子全退，再 Destroy sandbox，
// 防止子 goroutine 在容器销毁时孤儿。
type Registry struct {
	mu       sync.Mutex
	children []*Handle
	wg       sync.WaitGroup
}

// NewRegistry 构造空 Registry。
func NewRegistry() *Registry {
	return &Registry{}
}

// Register 创建并入栈一个 Handle，返回供 spawner runChild goroutine 持有
// （用于 MarkDone / MarkFailed）。
func (r *Registry) Register(taskID, brief string) *Handle {
	h := newHandle(taskID, brief)
	r.mu.Lock()
	r.children = append(r.children, h)
	r.mu.Unlock()
	return h
}

// RunningCount 返回当前 running 子任务数——spawn_child 用此查 max_children 闸。
// max_children 是"同时并发上限"：子 done 后名额立即释放，与 max_concurrent 直觉一致。
func (r *Registry) RunningCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, h := range r.children {
		if h.IsRunning() {
			n++
		}
	}
	return n
}

// HasRunning 检查是否仍有 running 子任务——done PreDoneCheck 用。
func (r *Registry) HasRunning() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, h := range r.children {
		if h.IsRunning() {
			return true
		}
	}
	return false
}

// Snapshot 返回所有子任务的不可变视图（list_children 工具用）。
// 按 spawn 顺序返回——SpawnedAt 单调递增，LLM 看得到先后关系。
func (r *Registry) Snapshot() []ChildSnapshot {
	r.mu.Lock()
	children := make([]*Handle, len(r.children))
	copy(children, r.children)
	r.mu.Unlock()
	// 在锁外调 h.Snapshot（每个 Handle 内部自己加锁），避免嵌套锁。
	out := make([]ChildSnapshot, len(children))
	for i, h := range children {
		out[i] = h.Snapshot()
	}
	return out
}

// trackGoroutine 在 spawner go runChild 前调，wg.Add(1)。
// 与 untrackGoroutine 严格配对——后者在 runChild 最末 defer 调，
// 任何子退出路径（成功 / panic / ctx cancel）都会触发。
func (r *Registry) trackGoroutine() { r.wg.Add(1) }

// untrackGoroutine 由 runChild 最外层 defer 调，wg.Done。
func (r *Registry) untrackGoroutine() { r.wg.Done() }

// WaitAll 阻塞等所有子 goroutine 退出，或 ctx 超时。
// 返 true 表示全退；false 表示 ctx 超时仍有 goroutine。
// handleActive 在 react.Run 返回后调，确保 Destroy sandbox 前子全退避免孤儿。
func (r *Registry) WaitAll(ctx context.Context) bool {
	done := make(chan struct{})
	go func() {
		r.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return true
	case <-ctx.Done():
		return false
	}
}
