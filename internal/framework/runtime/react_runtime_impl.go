package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/V3teran/liusha/internal/framework/core"
	"github.com/V3teran/liusha/internal/framework/llm"
	"github.com/V3teran/liusha/internal/registry"
)

// DefaultReActRuntime 是 ReActRuntime 的默认实现
type DefaultReActRuntime struct {
	mu sync.RWMutex

	// 工具注册表
	tools map[string]registry.Tool

	// 消息历史（跨多次 Run 保留）
	messageHistory []llm.Message
}

// NewReActRuntime 创建 ReAct 运行时
func NewReActRuntime() *DefaultReActRuntime {
	return &DefaultReActRuntime{
		tools:          make(map[string]registry.Tool),
		messageHistory: make([]llm.Message, 0),
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

// GetMessageHistory 获取消息历史
func (r *DefaultReActRuntime) GetMessageHistory() []llm.Message {
	r.mu.RLock()
	defer r.mu.RUnlock()

	history := make([]llm.Message, len(r.messageHistory))
	copy(history, r.messageHistory)
	return history
}

// ClearHistory 清空消息历史
func (r *DefaultReActRuntime) ClearHistory() {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.messageHistory = make([]llm.Message, 0)
}

// Run 运行 ReAct 循环
func (r *DefaultReActRuntime) Run(ctx context.Context, config *ReActConfig) (*ReActResult, error) {
	// 1. 验证配置
	if err := config.Validate(); err != nil {
		return nil, fmt.Errorf("配置验证失败: %w", err)
	}

	// 2. 初始化结果
	result := initializeResult()

	// 3. 恢复或初始化执行状态
	startIteration, err := r.restoreFromCheckpoint(ctx, config, result)
	if err != nil {
		return nil, err
	}
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
		trace, shouldStop, err := r.executeIteration(ctx, config, result, iteration)

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
	r.finalizeResult(result)

	return result, nil
}

// runIteration 运行单次迭代
// 返回：(执行轨迹, 是否应该停止, 错误)
func (r *DefaultReActRuntime) runIteration(
	ctx context.Context,
	config *ReActConfig,
	result *ReActResult,
	iteration int,
) (*IterationTrace, bool, error) {
	trace := &IterationTrace{
		Iteration:    iteration,
		StartTime:    time.Now().UnixMilli(),
		Status:       IterationStatusRunning,
		Actions:      make([]*Action, 0),
		Observations: make([]string, 0),
	}

	// 1. 调用 LLM（Thought + Action）
	response, err := r.callLLM(ctx, config, result.MessageHistory)
	if err != nil {
		trace.Status = IterationStatusFailed
		trace.EndTime = time.Now().UnixMilli()
		return trace, false, fmt.Errorf("LLM 调用失败: %w", err)
	}

	// 提取思考内容
	trace.Thought = response.Content

	// 回调：思考
	if config.OnThought != nil && response.Content != "" {
		config.OnThought(response.Content)
	}

	// 检查提前终止条件
	if config.EarlyStopCondition != nil && config.EarlyStopCondition(response.Content) {
		trace.Status = IterationStatusComplete
		trace.EndTime = time.Now().UnixMilli()

		// 添加助手消息
		result.AddAssistantMessage(response.Content, nil)
		return trace, true, nil
	}

	// 2. 检查是否有工具调用
	if len(response.ToolCalls) == 0 {
		// 没有工具调用 = 得出最终答案
		trace.Status = IterationStatusComplete
		trace.EndTime = time.Now().UnixMilli()

		// 添加助手消息
		result.AddAssistantMessage(response.Content, nil)
		return trace, true, nil
	}

	// 添加助手消息（包含工具调用）
	result.AddAssistantMessage(response.Content, response.ToolCalls)

	// 3. 执行工具调用（Observation）
	for _, toolCall := range response.ToolCalls {
		action := &Action{
			ToolCall:  toolCall,
			Thought:   response.Content,
			Timestamp: time.Now().UnixMilli(),
		}
		trace.Actions = append(trace.Actions, action)

		// 回调：动作
		if config.OnAction != nil {
			config.OnAction(action)
		}

		// 执行工具
		observation, err := r.executeTool(ctx, toolCall)
		if err != nil {
			observation = fmt.Sprintf("工具执行失败: %s", err.Error())
		}

		trace.Observations = append(trace.Observations, observation)

		// 回调：观察
		if config.OnObservation != nil {
			config.OnObservation(observation)
		}

		// 添加工具结果消息
		result.AddToolMessage(toolCall.ID, observation)
	}

	trace.Status = IterationStatusComplete
	trace.EndTime = time.Now().UnixMilli()

	return trace, false, nil
}

// callLLM 调用 LLM
func (r *DefaultReActRuntime) callLLM(
	ctx context.Context,
	config *ReActConfig,
	messageHistory []llm.Message,
) (*llmResponse, error) {
	// 应用消息预处理链（如果配置了）
	processedMessages := messageHistory
	if config.MessageModifierChain != nil {
		var err error
		processedMessages, err = config.MessageModifierChain.Apply(ctx, messageHistory)
		if err != nil {
			return nil, fmt.Errorf("message modifier chain failed: %w", err)
		}
	}

	// 消息已经是 llm.Message 格式，直接使用
	llmMessages := processedMessages

	// 构建 LLM 请求（温度按调用透传——Validate 已保证 0-1，>0 才有意义下发）
	request := llm.Request{
		Messages:  llmMessages,
		MaxTokens: config.MaxTokens,
	}
	if config.Temperature > 0 {
		t := config.Temperature
		request.Temperature = &t
	}

	// 添加工具定义（函数调用）
	if len(r.tools) > 0 {
		request.Tools = r.convertToolsToLLMFormat()
	}

	// 调用 LLM：流式开关生效——StreamEnabled 走 Stream 聚合（TTFT 可测、首 token
	// 即回调），否则非流式 Complete。两条路径产出同构的 llmResponse。
	var response llm.Response
	var err error
	if config.StreamEnabled {
		response, err = r.callLLMStreaming(ctx, config, request)
	} else {
		response, err = config.LLMProvider.Complete(ctx, request)
	}
	if err != nil {
		return nil, err
	}

	// 解析响应
	return r.parseLLMResponse(response)
}

// callLLMStreaming 流式调用并聚合为完整响应：text 增量拼接（首个增量即触发
// OnThought 前置回调的素材就绪）、StreamToolCall 收集、StreamDone 补 usage。
func (r *DefaultReActRuntime) callLLMStreaming(
	ctx context.Context,
	config *ReActConfig,
	request llm.Request,
) (llm.Response, error) {
	ch, err := config.LLMProvider.Stream(ctx, request)
	if err != nil {
		// provider 不支持流式 → 显式降级非流式（调用方语义仍是"拿到一次生成"）
		if strings.Contains(err.Error(), "不支持流式") {
			return config.LLMProvider.Complete(ctx, request)
		}
		return llm.Response{}, err
	}

	var resp llm.Response
	var sb strings.Builder
	for ev := range ch {
		switch ev.Kind {
		case llm.StreamText:
			sb.WriteString(ev.Content)
		case llm.StreamToolCall:
			if ev.Tool != nil {
				resp.ToolCalls = append(resp.ToolCalls, *ev.Tool)
			}
		case llm.StreamDone:
			if ev.Usage != nil {
				resp.Usage = *ev.Usage
			}
		case llm.StreamError:
			if ev.Err != nil {
				return llm.Response{}, fmt.Errorf("stream error: %w", ev.Err)
			}
			return llm.Response{}, fmt.Errorf("stream closed with error event")
		}
		if ctx.Err() != nil {
			return llm.Response{}, ctx.Err()
		}
	}
	resp.Content = sb.String()
	resp.FinishReason = "stop"
	return resp, nil
}

// llmResponse 是 LLM 响应的内部表示
type llmResponse struct {
	Content   string
	ToolCalls []llm.ToolCall
}

// convertToolsToLLMFormat 转换工具为 LLM 格式
func (r *DefaultReActRuntime) convertToolsToLLMFormat() []llm.ToolSchema {
	r.mu.RLock()
	defer r.mu.RUnlock()

	tools := make([]llm.ToolSchema, 0, len(r.tools))
	for _, tool := range r.tools {
		tools = append(tools, llm.ToolSchema{
			Name:        tool.Name(),
			Description: tool.Desc(),
			Parameters:  tool.Schema(),
		})
	}

	return tools
}

// parseLLMResponse 解析 LLM 响应
func (r *DefaultReActRuntime) parseLLMResponse(response llm.Response) (*llmResponse, error) {
	result := &llmResponse{
		Content:   response.Content,
		ToolCalls: response.ToolCalls,
	}

	return result, nil
}

// executeTool 执行工具
func (r *DefaultReActRuntime) executeTool(ctx context.Context, toolCall llm.ToolCall) (string, error) {
	r.mu.RLock()
	tool, exists := r.tools[toolCall.Name]
	r.mu.RUnlock()

	if !exists {
		return "", fmt.Errorf("工具 %s 未注册", toolCall.Name)
	}

	// 获取工具超时时间
	timeout := tool.Timeout()
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	// 异步执行工具 + 超时保护
	resultCh := make(chan registry.ToolResult, 1)
	errCh := make(chan error, 1)

	go func() {
		result, err := tool.Execute(ctx, toolCall.Arguments)
		if err != nil {
			errCh <- err
			return
		}
		resultCh <- result
	}()

	// 等待结果或超时
	var result registry.ToolResult
	var err error

	select {
	case result = <-resultCh:
		// 成功获取结果
	case err = <-errCh:
		return "", err
	case <-ctx.Done():
		return "", fmt.Errorf("工具 %s 执行超时（%s）", toolCall.Name, timeout)
	}

	// 如果工具返回错误
	if result.Error != "" {
		return result.Error, nil
	}

	// 返回工具输出（Output 字段已经是字符串）
	return result.Output, nil
}

// saveCheckpoint 保存当前执行状态到检查点
func (r *DefaultReActRuntime) saveCheckpoint(
	ctx context.Context,
	config *ReActConfig,
	result *ReActResult,
	iteration int,
) (core.CheckpointID, error) {
	// 序列化当前状态
	state := map[string]interface{}{
		"message_history": result.MessageHistory,
		"trace":           result.Trace,
		"iteration":       iteration,
		"timestamp":       time.Now().UnixMilli(),
	}
	stateJSON, err := json.Marshal(state)
	if err != nil {
		return "", fmt.Errorf("serialize state failed: %w", err)
	}

	// 构造检查点
	checkpoint := core.Checkpoint{
		TaskID:        config.TaskID,
		StateSnapshot: stateJSON,
		Phase:         fmt.Sprintf("iteration_%d", iteration),
		ComponentStates: map[string]json.RawMessage{
			"react_runtime": stateJSON,
		},
		Labels: map[string]string{
			"iteration": fmt.Sprintf("%d", iteration),
			"status":    string(result.Status),
		},
		CreatedAt: time.Now(),
	}

	// 保存检查点
	cpID, err := config.Checkpointer.Save(ctx, checkpoint)
	if err != nil {
		return "", fmt.Errorf("save checkpoint failed: %w", err)
	}

	return cpID, nil
}

// forceFinalSummary 强制要求 LLM 输出最终总结（当达到最大迭代次数时）
func (r *DefaultReActRuntime) forceFinalSummary(
	ctx context.Context,
	config *ReActConfig,
	result *ReActResult,
) error {
	// 构造强制总结的 prompt
	summaryPrompt := `你已经达到最大迭代次数限制。请基于当前已收集的所有观察和执行结果，输出一个最终总结。

要求：
1. 使用 Final Answer: 开头标记最终答案
2. 总结你已经完成的工作和发现的关键信息
3. 如果任务未完全完成，说明原因和已完成的部分
4. 不要再调用任何工具

请立即输出最终总结。`

	// 添加总结 prompt 到消息历史
	result.AddUserMessage(summaryPrompt)

	// 调用 LLM 获取最终总结（不允许工具调用）
	request := llm.Request{
		Messages:  result.MessageHistory,
		MaxTokens: config.MaxTokens,
	}
	if config.Temperature > 0 {
		t := config.Temperature
		request.Temperature = &t
	}
	// 注意：不添加 tools，禁止工具调用

	resp, err := config.LLMProvider.Complete(ctx, request)
	if err != nil {
		return fmt.Errorf("force final summary LLM call failed: %w", err)
	}

	// 将总结添加到消息历史
	result.AddAssistantMessage(resp.Content, nil)

	return nil
}
