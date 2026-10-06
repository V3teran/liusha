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
	// 验证配置
	if err := config.Validate(); err != nil {
		return nil, fmt.Errorf("配置验证失败: %w", err)
	}

	// 初始化结果
	result := &ReActResult{
		MessageHistory: make([]llm.Message, 0),
		Trace:          make([]*IterationTrace, 0),
		Status:         ReActStatusSuccess,
		CheckpointIDs:  make([]core.CheckpointID, 0),
	}

	// 起始迭代编号
	startIteration := 1

	// 恢复：如果指定了检查点，从中恢复
	if config.RestoreFromCheckpoint != "" && config.Checkpointer != nil {
		checkpoint, err := config.Checkpointer.Load(ctx, config.RestoreFromCheckpoint)
		if err != nil {
			return nil, fmt.Errorf("load checkpoint failed: %w", err)
		}
		if checkpoint == nil { // 自定义 Checkpointer 未按契约返回 ErrCheckpointNotFound 时的防御
			return nil, fmt.Errorf("load checkpoint failed: checkpoint %s not found", config.RestoreFromCheckpoint)
		}

		// 反序列化状态
		var state struct {
			MessageHistory []llm.Message     `json:"message_history"`
			Trace          []*IterationTrace `json:"trace"`
			Iteration      int               `json:"iteration"`
		}
		if err := json.Unmarshal(checkpoint.StateSnapshot, &state); err != nil {
			return nil, fmt.Errorf("deserialize checkpoint state failed: %w", err)
		}

		// 恢复消息历史和轨迹
		result.MessageHistory = state.MessageHistory
		result.Trace = state.Trace
		result.RestoredFromCheckpoint = config.RestoreFromCheckpoint
		result.RestoredIteration = state.Iteration

		// 从下一次迭代继续
		startIteration = state.Iteration + 1
	} else {
		// 新执行：加载初始历史
		if len(config.InitialHistory) > 0 {
			result.MessageHistory = append(result.MessageHistory, config.InitialHistory...)
		} else {
			r.mu.RLock()
			result.MessageHistory = append(result.MessageHistory, r.messageHistory...)
			r.mu.RUnlock()
		}

		// 添加系统提示
		if config.SystemPrompt != "" {
			result.AddSystemMessage(config.SystemPrompt)
		}

		// 添加用户目标
		result.AddUserMessage(config.Objective)
	}

	// ReAct 循环（从 startIteration 开始）
	for iteration := startIteration; iteration <= config.MaxIterations; iteration++ {
		if err := ctx.Err(); err != nil {
			result.Status = ReActStatusCancelled
			result.Error = err
			return result, err
		}

		// 执行单次迭代
		trace, shouldStop, err := r.runIteration(ctx, config, result, iteration)
		result.Trace = append(result.Trace, trace)
		result.Iterations = iteration

		// 回调：迭代完成
		if config.OnIteration != nil {
			config.OnIteration(iteration, trace.Status)
		}

		// Checkpoint 保存时机
		if config.Checkpointer != nil && config.CheckpointPolicy != nil {
			if config.CheckpointPolicy.ShouldSave(iteration, trace) {
				cpID, err := r.saveCheckpoint(ctx, config, result, iteration)
				if err != nil {
					// 保存失败不中断执行；错误暴露在结果上供调用方审计
					result.LastCheckpointError = err
				} else {
					result.CheckpointID = cpID
					result.CheckpointIDs = append(result.CheckpointIDs, cpID)
				}
			}
		}

		if err != nil {
			result.Status = ReActStatusError
			result.Error = err
			return result, err
		}

		if shouldStop {
			break
		}

		if iteration >= config.MaxIterations {
			result.Status = ReActStatusMaxIterations
			break
		}
	}

	// 提取最终答案
	result.FinalAnswer = result.ExtractFinalAnswer()

	// 保存消息历史到运行时
	r.mu.Lock()
	r.messageHistory = result.MessageHistory
	r.mu.Unlock()

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
