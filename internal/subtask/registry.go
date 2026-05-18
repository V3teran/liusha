package subtask

import "sync"

// Registry 是父任务持有的子任务句柄集合（线程安全）。
//
// 一个父 active task 一个 Registry 实例——由 hunter builder 闭包构造，
// 注入到 spawn_child / list_children 工具及 done PreDoneCheck。
type Registry struct {
	mu       sync.Mutex
	children []*Handle
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

// Count 返回已注册子任务总数（含终态）——spawn_child 用此查 max_children 闸。
func (r *Registry) Count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.children)
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
