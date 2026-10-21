package runtime

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/V3teran/liusha/internal/registry"
)

// DefaultReActRuntime 是 ReActRuntime 的默认实现。
// 一次 Run 是一次自包含执行：消息历史经 ReActConfig.InitialHistory 传入、
// 经 ReActResult.MessageHistory 传出，运行时自身不跨 Run 保存对话状态——
// 复用同一运行时的多次 Run 互不串扰，跨执行的延续由检查点机制承担。
type DefaultReActRuntime struct {
	mu sync.RWMutex

	// 工具注册表
	tools map[string]registry.Tool

	// consecutiveToolFailures 是本轮 Run 内连续工具失败计数（跨迭代累计，
	// 成功即清零）——连续失败止损的依据。Run 开始时归零。
	consecutiveToolFailures int
}

// NewReActRuntime 创建 ReAct 运行时
func NewReActRuntime() *DefaultReActRuntime {
	return &DefaultReActRuntime{
		tools: make(map[string]registry.Tool),
	}
}

// RegisterTool 注册工具
func (r *DefaultReActRuntime) RegisterTool(tool registry.Tool) error {
	if tool == nil {
		return fmt.Errorf("工具不能为 nil")
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.tools[tool.Name()]; exists {
		return fmt.Errorf("工具 %s 已存在", tool.Name())
	}

	r.tools[tool.Name()] = tool
	return nil
}

// Run 运行 ReAct 循环
func (r *DefaultReActRuntime) Run(ctx context.Context, config *ReActConfig) (*ReActResult, error) {
	// 1. 验证配置
	if err := config.Validate(); err != nil {
		return nil, fmt.Errorf("配置验证失败: %w", err)
	}

	// 2. 初始化结果
	result := initializeResult()
	start := time.Now()
	result.Model = config.LLMProvider.ModelID()
	// 墙钟耗时覆盖所有退出路径（含错误/取消/迭代上限），defer 挂在 result 初始化后。
	defer func() { result.ElapsedMs = time.Since(start).Milliseconds() }()

	// 3. 恢复或初始化执行状态
	startIteration, err := r.restoreFromCheckpoint(ctx, config, result)
	if err != nil {
		return nil, err
	}
	r.consecutiveToolFailures = 0
	if startIteration == 1 {
		r.initializeNewExecution(config, result)
	}

	// 4. ReAct 循环：从 startIteration 开始迭代
	unbounded := config.MaxIterations <= 0
	for iteration := startIteration; unbounded || iteration <= config.MaxIterations; iteration++ {
		// 4.1 检查上下文取消
		if err := ctx.Err(); err != nil {
			result.Status = ReActStatusCancelled
			result.Error = err
			return result, err
		}

		// 4.2 执行单次迭代
		trace, shouldStop, err := r.runIteration(ctx, config, result, iteration)
		result.Trace = append(result.Trace, trace)
		result.Iterations = iteration

		// 回调：迭代完成
		if config.OnIteration != nil {
			config.OnIteration(iteration, trace.Status)
		}

		// 4.3 处理检查点
		r.handleCheckpoint(ctx, config, result, iteration, trace)

		// 4.4 处理错误
		if err != nil {
			result.Status = ReActStatusError
			result.Error = err
			return result, err
		}

		// 4.5 检查是否应该停止
		if shouldStop {
			// 正常停止时强制输出结构化总结
			r.handleNormalCompletion(ctx, config, result)
			break
		}

		// 4.6 检查是否达到最大迭代次数
		if !unbounded && iteration >= config.MaxIterations {
			result.Status = ReActStatusMaxIterations
			r.handleMaxIterationsReached(ctx, config, result)
			break
		}
	}

	// 5. 完成结果处理
	result.FinalAnswer = result.ExtractFinalAnswer()

	return result, nil
}

// runIteration 运行单次迭代
// 返回：(执行轨迹, 是否应该停止, 错误)
