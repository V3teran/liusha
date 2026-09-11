package runtime

import (
	"context"
	"fmt"
)

// CompositionPattern 是组合模式的枚举。
type CompositionPattern string

const (
	PatternSequential  CompositionPattern = "sequential"  // 顺序执行
	PatternParallel    CompositionPattern = "parallel"    // 并行执行
	PatternPipeline    CompositionPattern = "pipeline"    // 管道执行
	PatternConditional CompositionPattern = "conditional" // 条件执行
	PatternLoop        CompositionPattern = "loop"        // 循环执行
)

// Composition 是组合执行器。
type Composition struct {
	pattern CompositionPattern
	tasks   []Task
}

// Task 是可组合的任务接口。
type Task interface {
	Execute(ctx context.Context, input any) (any, error)
	Name() string
}

// NewComposition 创建组合执行器。
func NewComposition(pattern CompositionPattern) *Composition {
	return &Composition{
		pattern: pattern,
		tasks:   make([]Task, 0),
	}
}

// Add 添加任务。
func (c *Composition) Add(task Task) {
	c.tasks = append(c.tasks, task)
}

// Execute 执行组合。
func (c *Composition) Execute(ctx context.Context, input any) (any, error) {
	switch c.pattern {
	case PatternSequential:
		return c.executeSequential(ctx, input)
	case PatternParallel:
		return c.executeParallel(ctx, input)
	case PatternPipeline:
		return c.executePipeline(ctx, input)
	default:
		return nil, fmt.Errorf("unsupported pattern: %s", c.pattern)
	}
}

// executeSequential 顺序执行。
func (c *Composition) executeSequential(ctx context.Context, input any) (any, error) {
	results := make([]any, 0, len(c.tasks))

	for _, task := range c.tasks {
		result, err := task.Execute(ctx, input)
		if err != nil {
			return nil, fmt.Errorf("task %s failed: %w", task.Name(), err)
		}
		results = append(results, result)
	}

	return results, nil
}

// executeParallel 并行执行。
func (c *Composition) executeParallel(ctx context.Context, input any) (any, error) {
	type taskResult struct {
		index  int
		result any
		err    error
	}

	resultCh := make(chan taskResult, len(c.tasks))

	for i, task := range c.tasks {
		go func(idx int, t Task) {
			result, err := t.Execute(ctx, input)
			resultCh <- taskResult{index: idx, result: result, err: err}
		}(i, task)
	}

	results := make([]any, len(c.tasks))
	for i := 0; i < len(c.tasks); i++ {
		tr := <-resultCh
		if tr.err != nil {
			return nil, fmt.Errorf("task %s failed: %w", c.tasks[tr.index].Name(), tr.err)
		}
		results[tr.index] = tr.result
	}

	return results, nil
}

// executePipeline 管道执行（前一个的输出是后一个的输入）。
func (c *Composition) executePipeline(ctx context.Context, input any) (any, error) {
	current := input

	for _, task := range c.tasks {
		result, err := task.Execute(ctx, current)
		if err != nil {
			return nil, fmt.Errorf("task %s failed: %w", task.Name(), err)
		}
		current = result
	}

	return current, nil
}

// ============================================
// 具体任务实现
// ============================================

// FuncTask 是函数任务（适配器）。
type FuncTask struct {
	name string
	fn   func(context.Context, any) (any, error)
}

// NewFuncTask 创建函数任务。
func NewFuncTask(name string, fn func(context.Context, any) (any, error)) *FuncTask {
	return &FuncTask{
		name: name,
		fn:   fn,
	}
}

// Execute 实现 Task 接口。
func (t *FuncTask) Execute(ctx context.Context, input any) (any, error) {
	return t.fn(ctx, input)
}

// Name 实现 Task 接口。
func (t *FuncTask) Name() string {
	return t.name
}

// ConditionalTask 是条件任务。
type ConditionalTask struct {
	condition func(any) bool
	thenTask  Task
	elseTask  Task
}

// NewConditionalTask 创建条件任务。
func NewConditionalTask(condition func(any) bool, thenTask, elseTask Task) *ConditionalTask {
	return &ConditionalTask{
		condition: condition,
		thenTask:  thenTask,
		elseTask:  elseTask,
	}
}

// Execute 实现 Task 接口。
func (t *ConditionalTask) Execute(ctx context.Context, input any) (any, error) {
	if t.condition(input) {
		return t.thenTask.Execute(ctx, input)
	}

	if t.elseTask != nil {
		return t.elseTask.Execute(ctx, input)
	}

	return input, nil
}

// Name 实现 Task 接口。
func (t *ConditionalTask) Name() string {
	return "conditional"
}

// LoopTask 是循环任务。
type LoopTask struct {
	condition func(any) bool
	task      Task
	maxIter   int
}

// NewLoopTask 创建循环任务。
func NewLoopTask(condition func(any) bool, task Task, maxIter int) *LoopTask {
	return &LoopTask{
		condition: condition,
		task:      task,
		maxIter:   maxIter,
	}
}

// Execute 实现 Task 接口。
func (t *LoopTask) Execute(ctx context.Context, input any) (any, error) {
	current := input
	iter := 0

	for t.condition(current) {
		if iter >= t.maxIter {
			return nil, fmt.Errorf("max iterations exceeded: %d", t.maxIter)
		}

		result, err := t.task.Execute(ctx, current)
		if err != nil {
			return nil, fmt.Errorf("iteration %d failed: %w", iter, err)
		}

		current = result
		iter++
	}

	return current, nil
}

// Name 实现 Task 接口。
func (t *LoopTask) Name() string {
	return "loop"
}

// RetryTask 是重试任务。
type RetryTask struct {
	task       Task
	maxRetries int
}

// NewRetryTask 创建重试任务。
func NewRetryTask(task Task, maxRetries int) *RetryTask {
	return &RetryTask{
		task:       task,
		maxRetries: maxRetries,
	}
}

// Execute 实现 Task 接口。
func (t *RetryTask) Execute(ctx context.Context, input any) (any, error) {
	var lastErr error

	for i := 0; i <= t.maxRetries; i++ {
		result, err := t.task.Execute(ctx, input)
		if err == nil {
			return result, nil
		}
		lastErr = err
	}

	return nil, fmt.Errorf("task failed after %d retries: %w", t.maxRetries, lastErr)
}

// Name 实现 Task 接口。
func (t *RetryTask) Name() string {
	return "retry-" + t.task.Name()
}

// ============================================
// 组合构建器
// ============================================

// CompositionBuilder 是组合构建器。
type CompositionBuilder struct {
	composition *Composition
}

// NewSequential 创建顺序组合构建器。
func NewSequential() *CompositionBuilder {
	return &CompositionBuilder{
		composition: NewComposition(PatternSequential),
	}
}

// NewParallel 创建并行组合构建器。
func NewParallel() *CompositionBuilder {
	return &CompositionBuilder{
		composition: NewComposition(PatternParallel),
	}
}

// NewPipeline 创建管道组合构建器。
func NewPipeline() *CompositionBuilder {
	return &CompositionBuilder{
		composition: NewComposition(PatternPipeline),
	}
}

// Then 添加任务（链式调用）。
func (b *CompositionBuilder) Then(task Task) *CompositionBuilder {
	b.composition.Add(task)
	return b
}

// ThenFunc 添加函数任务（链式调用）。
func (b *CompositionBuilder) ThenFunc(name string, fn func(context.Context, any) (any, error)) *CompositionBuilder {
	b.composition.Add(NewFuncTask(name, fn))
	return b
}

// Build 构建组合。
func (b *CompositionBuilder) Build() *Composition {
	return b.composition
}

// Execute 执行（便捷方法）。
func (b *CompositionBuilder) Execute(ctx context.Context, input any) (any, error) {
	return b.composition.Execute(ctx, input)
}
