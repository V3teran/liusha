// Package runtime 实现 Agent 运行时：ReAct (Reasoning + Acting) 循环、
// 消息压缩链（MessageModifierChain）与检查点策略。
package runtime

import (
	"context"
	"fmt"

	"github.com/V3teran/liusha/internal/framework/core"
	"github.com/V3teran/liusha/internal/framework/llm"
	"github.com/V3teran/liusha/internal/registry"
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
	RegisterTool(tool registry.Tool) error
}

// ReActConfig 是 ReAct 运行时的配置
type ReActConfig struct {
	// 目标（用户的原始请求）
	Objective string

	// 系统提示（可选，用于设定 Agent 角色）
	SystemPrompt string

	// LLM 提供者
	LLMProvider llm.Provider

	// 最大迭代次数（防止无限循环；<=0 = 不设限，上限由调用方配置唯一决定）
	MaxIterations int

	// 初始消息历史（可选，用于多轮对话）
	InitialHistory []llm.Message

	// 提前终止条件（返回 true 时停止循环）
	EarlyStopCondition func(thought string) bool

	// MaxConsecutiveToolFailures 是连续工具失败止损阈值：连续失败达到该数
	// 即终止本轮 ReAct（环境有障碍时避免 LLM 无限挣扎烧 token），把已收集的
	// 观察交回上层裁决。<=0 用默认值 8。
	MaxConsecutiveToolFailures int

	// 回调函数
	OnThought func(thought string)
	// OnThoughtDelta 是思考文本的流式增量回调（驱动前端打字机）。非空时本轮
	// 调用走 Provider.Stream 并逐段回调；Provider 不支持流式时自动降级 Complete
	// （此时只有整段 OnThought，无增量）。与 OnThought 的分工：Delta 逐段、
	// Thought 每轮一次携完整文本。
	OnThoughtDelta func(chunk string)
	// OnThinkingDelta 是扩展思考（模型内部 CoT，anthropic thinking block）的
	// 流式增量回调——与 OnThoughtDelta 分道：思考先流、正文后流，前端分两个
	// 展示区（思考折叠区 + 正文打字机）。可空 = 调用方不消费思考流。
	OnThinkingDelta func(chunk string)
	OnAction        func(action *Action)
	OnObservation   func(observation string)
	OnIteration     func(iteration int, status IterationStatus)

	// 温度参数（0-1，控制随机性）
	Temperature float64

	// 最大 Token 数
	MaxTokens int

	// 是否启用流式输出
	// 消息预处理链（在 LLM 调用前对消息历史进行转换）
	// 包括：消息压缩、字段注入、验证等
	MessageModifierChain *MessageModifierChain

	// 中间件链（在每次迭代前后执行，用于检测循环、目标偏离等异常）
	MiddlewareChain *MiddlewareChain

	// Checkpoint 支持
	CheckpointPolicy      CheckpointPolicy  // 何时保存检查点（nil 表示不保存）
	Checkpointer          core.Checkpointer // 检查点存储（nil 表示不保存）
	TaskID                string            // 关联任务 ID（用于检查点）
	RestoreFromCheckpoint core.CheckpointID // 从指定检查点恢复（空表示新执行）

	// 结构化输出格式（可选）
	// 当 LLM 自然停止但输出格式不符合预期时，用于强制要求输出特定格式
	// 如果为空，使用通用的文本总结格式
	OutputFormatPrompt string // 输出格式的 prompt 模板
	OutputFormatSchema string // JSON Schema 示例（嵌入到 prompt 中）
}

// ReActResult 是 ReAct 循环的执行结果
type ReActResult struct {
	// 最终答案
	FinalAnswer string

	// FinalThinking 是最终轮的扩展思考原文（模型内部 CoT；供会话时间线落库，
	// 与 FinalAnswer 一道构成该轮的完整产出一一正文是结论、思考是过程）。
	FinalThinking string

	// Usage 是本轮所有 LLM 调用的 token 累计（时间线每步用量 chip 的数据源；
	// 每次 callLLM 后累加，含工具循环里的全部迭代）。
	Usage llm.Usage

	// Model 是本轮绑定的模型标识（provider 侧 ModelID；供审计/展示归属）。
	Model string

	// ElapsedMs 是本轮 Run 的墙钟耗时（含 LLM 与工具执行——「这一步我等了多久」）。
	ElapsedMs int64

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
	// LastCheckpointError 是最近一次检查点保存失败的原因（nil=全部成功）。
	// 检查点为 best-effort：失败不中断 ReAct，但不无声丢弃。
	LastCheckpointError error

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

// IterationStatusComplete 等枚举定义。
const (
	// IterationStatusRunning 等枚举定义。
	IterationStatusRunning  IterationStatus = "running"
	IterationStatusComplete IterationStatus = "complete"
	IterationStatusFailed   IterationStatus = "failed"
)

// Validate 验证配置的合法性
func (c *ReActConfig) Validate() error {
	if c.Objective == "" {
		return fmt.Errorf("目标（Objective）不能为空")
	}

	if c.LLMProvider == nil {
		return fmt.Errorf("LLM 提供者不能为空")
	}

	// MaxIterations<=0 合法：= 不设限（上限完全由调用方配置决定，运行时不二次封顶）。

	// Temperature 合法下界为 0（贪心解码）；上界因协议族而异，
	// 由 Provider 侧约束，运行时只拒绝负值。
	if c.Temperature < 0 {
		return fmt.Errorf("温度参数不能为负数")
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

// AddAssistantMessage 添加助手消息。thinking 是该轮的扩展思考块（anthropic
// 多轮 tool_use 规范回传用；无思考为 nil）。
func (r *ReActResult) AddAssistantMessage(content string, toolCalls []llm.ToolCall, thinking *llm.ThinkingBlock) {
	r.MessageHistory = append(r.MessageHistory, llm.Message{
		Role:      llm.RoleAssistant,
		Content:   content,
		ToolCalls: toolCalls,
		Thinking:  thinking,
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
