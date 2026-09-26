package runtime

import (
	"context"
	"fmt"

	"github.com/V3teran/liusha/internal/framework/core"
	"github.com/V3teran/liusha/internal/framework/llm"
)

// ReActRuntime 是 ReAct (Reasoning + Acting) 循环的运行时。
//
// ReAct 模式：
// 1. Thought（思考）：LLM 分析当前状态，决定下一步行动
// 2. Action（行动）：执行工具调用
// 3. Observation（观察）：获取工具执行结果
// 4. 循环直到得出最终答案
//
// 核心能力：
// - 自动管理消息历史（追加用户消息、助手消息、工具结果）
// - 工具注册和调用（支持函数调用）
// - 循环控制（最大迭代次数、提前终止）
// - 消息压缩（超过上下文窗口时自动压缩历史）
type ReActRuntime interface {
	// Run 运行 ReAct 循环
	Run(ctx context.Context, config *ReActConfig) (*ReActResult, error)

	// RegisterTool 注册工具
	RegisterTool(tool core.Tool) error

	// UnregisterTool 注销工具
	UnregisterTool(name string) error

	// GetTools 获取所有已注册工具
	GetTools() []core.Tool

	// GetMessageHistory 获取消息历史
	GetMessageHistory() []llm.Message

	// ClearHistory 清空消息历史
	ClearHistory()
}

// ReActConfig 是 ReAct 运行时的配置
type ReActConfig struct {
	// 目标（用户的原始请求）
	Objective string

	// 系统提示（可选，用于设定 Agent 角色）
	SystemPrompt string

	// LLM 提供者
	LLMProvider llm.Provider

	// 模型 ID

	// 最大迭代次数（防止无限循环）
	MaxIterations int

	// 初始消息历史（可选，用于多轮对话）
	InitialHistory []llm.Message

	// 工具列表（自动转换为函数调用）
	Tools []core.Tool

	// 提前终止条件（返回 true 时停止循环）
	EarlyStopCondition func(thought string) bool

	// 回调函数
	OnThought     func(thought string)
	OnAction      func(action *Action)
	OnObservation func(observation string)
	OnIteration   func(iteration int, status IterationStatus)

	// 温度参数（0-1，控制随机性）
	Temperature float64

	// 最大 Token 数
	MaxTokens int

	// 是否启用流式输出
	StreamEnabled bool

	// 消息预处理链（在 LLM 调用前对消息历史进行转换）
	// 包括：消息压缩、字段注入、验证等
	MessageModifierChain *MessageModifierChain

	// Checkpoint 支持
	CheckpointPolicy      CheckpointPolicy  // 何时保存检查点（nil 表示不保存）
	Checkpointer          core.Checkpointer // 检查点存储（nil 表示不保存）
	TaskID                string            // 关联任务 ID（用于检查点）
	RestoreFromCheckpoint core.CheckpointID // 从指定检查点恢复（空表示新执行）
}

// ReActResult 是 ReAct 循环的执行结果
type ReActResult struct {
	// 最终答案
	FinalAnswer string

	// 消息历史
	MessageHistory []llm.Message

	// 迭代次数
	Iterations int

	// 执行状态
	Status ReActStatus

	// 错误信息
	Error error

	// 执行轨迹（每次迭代的详情）
	Trace []*IterationTrace

	// Checkpoint 追踪
	CheckpointID  core.CheckpointID   // 最后保存的检查点 ID
	CheckpointIDs []core.CheckpointID // 所有已保存的检查点 ID

	// 恢复信息
	RestoredFromCheckpoint core.CheckpointID // 从哪个检查点恢复（空表示新执行）
	RestoredIteration      int               // 恢复时的起始迭代（0 表示新执行）
}

// ReActStatus 执行状态
type ReActStatus string

const (
	// ReActStatusSuccess 成功得出答案
	ReActStatusSuccess ReActStatus = "success"

	// ReActStatusMaxIterations 达到最大迭代次数
	ReActStatusMaxIterations ReActStatus = "max_iterations"

	// ReActStatusError 执行出错
	ReActStatusError ReActStatus = "error"

	// ReActStatusCancelled 被取消
	ReActStatusCancelled ReActStatus = "cancelled"
)

// Action 是单次工具调用的行动
type Action struct {
	// 工具调用信息
	ToolCall llm.ToolCall

	// 思考过程（LLM 的推理）
	Thought string

	// 时间戳
	Timestamp int64
}

// IterationTrace 是单次迭代的执行轨迹
type IterationTrace struct {
	// 迭代序号
	Iteration int

	// 思考内容
	Thought string

	// 执行的动作
	Actions []*Action

	// 观察结果
	Observations []string

	// 本次迭代状态
	Status IterationStatus

	// 开始/结束时间
	StartTime int64
	EndTime   int64
}

// IterationStatus 迭代状态
type IterationStatus string

const (
	IterationStatusRunning  IterationStatus = "running"
	IterationStatusComplete IterationStatus = "complete"
	IterationStatusFailed   IterationStatus = "failed"
)

// DefaultReActConfig 返回默认配置
func DefaultReActConfig() *ReActConfig {
	return &ReActConfig{
		MaxIterations: 10,
		Temperature:   0.7,
		MaxTokens:     4096,
		StreamEnabled: false,
	}
}

// Validate 验证配置的合法性
func (c *ReActConfig) Validate() error {
	if c.Objective == "" {
		return fmt.Errorf("目标（Objective）不能为空")
	}

	if c.LLMProvider == nil {
		return fmt.Errorf("LLM 提供者不能为空")
	}

	if c.MaxIterations <= 0 {
		return fmt.Errorf("最大迭代次数必须大于 0")
	}

	if c.Temperature < 0 || c.Temperature > 1 {
		return fmt.Errorf("温度参数必须在 0-1 之间")
	}

	return nil
}

// AddSystemMessage 添加系统消息
func (r *ReActResult) AddSystemMessage(content string) {
	r.MessageHistory = append(r.MessageHistory, llm.Message{
		Role:    llm.RoleSystem,
		Content: content,
	})
}

// AddUserMessage 添加用户消息
func (r *ReActResult) AddUserMessage(content string) {
	r.MessageHistory = append(r.MessageHistory, llm.Message{
		Role:    llm.RoleUser,
		Content: content,
	})
}

// AddAssistantMessage 添加助手消息
func (r *ReActResult) AddAssistantMessage(content string, toolCalls []llm.ToolCall) {
	r.MessageHistory = append(r.MessageHistory, llm.Message{
		Role:      llm.RoleAssistant,
		Content:   content,
		ToolCalls: toolCalls,
	})
}

// AddToolMessage 添加工具消息
func (r *ReActResult) AddToolMessage(toolCallID, content string) {
	r.MessageHistory = append(r.MessageHistory, llm.Message{
		Role:       llm.RoleTool,
		Content:    content,
		ToolCallID: toolCallID,
	})
}

// GetLastAssistantMessage 获取最后一条助手消息
func (r *ReActResult) GetLastAssistantMessage() *llm.Message {
	for i := len(r.MessageHistory) - 1; i >= 0; i-- {
		if r.MessageHistory[i].Role == llm.RoleAssistant {
			return &r.MessageHistory[i]
		}
	}
	return nil
}

// HasToolCalls 检查最后一条助手消息是否包含工具调用
func (r *ReActResult) HasToolCalls() bool {
	lastMsg := r.GetLastAssistantMessage()
	return lastMsg != nil && len(lastMsg.ToolCalls) > 0
}

// ExtractFinalAnswer 从消息历史中提取最终答案
// 最终答案是最后一条不包含工具调用的助手消息
func (r *ReActResult) ExtractFinalAnswer() string {
	for i := len(r.MessageHistory) - 1; i >= 0; i-- {
		msg := r.MessageHistory[i]
		if msg.Role == llm.RoleAssistant && len(msg.ToolCalls) == 0 && msg.Content != "" {
			return msg.Content
		}
	}
	return ""
}
