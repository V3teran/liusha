// Executor Pool 实现
package executor

import (
	"context"
	"sync"

	"github.com/V3teran/liusha/internal/knowledgegraph"
)

// Pool 是 Executor 对象池，复用 Executor 实例以减少创建开销。
//
// 设计原则：
// - 预创建固定数量的 workers（默认 10 个）
// - 通过 channel 管理 worker 的借用和归还
// - 并发安全：多个 goroutine 可同时调用 Execute
//
// 架构参考：
// - Go 标准库的 sync.Pool
// - Database connection pool
type Pool struct {
	workers   chan *Coordinator // worker 池
	size      int               // pool 大小
	factory   func() *Coordinator
	closeOnce sync.Once
	closed    chan struct{}
}

// NewPool 创建 Executor Pool。
//
// 参数：
// - size: pool 大小（worker 数量）
// - factory: 创建 Executor 的工厂函数
func NewPool(size int, factory func() *Coordinator) *Pool {
	if size <= 0 {
		size = 10 // 默认 10 个 workers
	}

	p := &Pool{
		workers: make(chan *Coordinator, size),
		size:    size,
		factory: factory,
		closed:  make(chan struct{}),
	}

	// 预创建所有 workers
	for i := 0; i < size; i++ {
		p.workers <- factory()
	}

	return p
}

// Execute 执行一个 Action。
//
// 流程：
// 1. 从 pool 获取 worker（阻塞直到有可用 worker）
// 2. 执行 Action
// 3. 归还 worker 到 pool
//
// 并发安全：多个 goroutine 可同时调用。
func (p *Pool) Execute(ctx context.Context, action knowledgegraph.Node) *Report {
	select {
	case <-p.closed:
		return &Report{
			StopWhy: "pool closed",
		}
	case <-ctx.Done():
		return &Report{
			StopWhy: ctx.Err().Error(),
		}
	case worker := <-p.workers:
		// 执行完毕后归还 worker
		defer func() {
			select {
			case p.workers <- worker:
			case <-p.closed:
				// pool 已关闭，不归还
			}
		}()

		// 执行 Action
		attempts, err := worker.Execute(ctx, action)
		if err != nil {
			return &Report{
				Attempts: 0,
				StopWhy:  err.Error(),
			}
		}

		return &Report{
			Steps:    1,
			Attempts: len(attempts),
			Promoted: 0, // TODO: 实际晋升数量
		}
	}
}

// Close 关闭 Pool，释放所有 workers。
//
// 注意：关闭后不能再调用 Execute。
func (p *Pool) Close() {
	p.closeOnce.Do(func() {
		close(p.closed)
		close(p.workers)
	})
}

// Size 返回 pool 的大小。
func (p *Pool) Size() int {
	return p.size
}

// Available 返回当前可用的 worker 数量。
func (p *Pool) Available() int {
	return len(p.workers)
}
