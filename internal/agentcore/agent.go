// Package agentcore 提供统一的 LLM Agent 基础框架
//
// 核心职责：
// 1. 封装 LLM 工具调用循环（消除重复代码）
// 2. 统一配置管理（maxRounds/maxTokens/压缩等）
// 3. 提供标准化的 Agent 接口
//
// 设计原则：
// - 框架负责"怎么调用 LLM"，业务负责"调用什么工具"
// - 每个 Agent 注入自己的工具集和系统提示词
// - 支持上下文压缩、checkpoint 等可选能力
package agentcore

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/V3teran/liusha/internal/provider"
	"github.com/V3teran/liusha/internal/registry"
	"github.com/rs/zerolog"
)

// Agent 是统一的 LLM Agent 基础框架
type Agent struct {
	name     string
	provider provider.Provider
	registry *registry.Registry
	logger   zerolog.Logger

	// 配置
	maxRounds      int       // 工具调用最大轮数（默认 100）
	maxTokens      int       // Token 预算（默认 8000）
	systemPrompt   string    // 系统提示词
	compactor      Compactor // 上下文压缩器（可选）
	compactTrigger float64   // 压缩触发阈值（0.7 = 70%）
}

// Compactor 是上下文压缩接口
type Compactor interface {
	Compact(ctx context.Context, p provider.Provider, messages []provider.Message) ([]provider.Message, error)
}

// Config 统一配置
type Config struct {
	Name           string              // Agent 名称（用于日志）
	Provider       provider.Provider   // LLM Provider
	Registry       *registry.Registry  // 工具注册表
	Logger         zerolog.Logger      // 日志器
	MaxRounds      int                 // 最大轮数（默认 100）
	MaxTokens      int                 // Token 预算（默认 8000）
	SystemPrompt   string              // 系统提示词
	Compactor      Compactor           // 压缩器（可选）
	CompactTrigger float64             // 压缩触发阈值（默认 0.7）
}

// New 创建 Agent
func New(cfg Config) *Agent {
	// 设置默认值
	if cfg.MaxRounds == 0 {
		cfg.MaxRounds = 100
	}
	if cfg.MaxTokens == 0 {
		cfg.MaxTokens = 8000
	}
	if cfg.CompactTrigger == 0 {
		cfg.CompactTrigger = 0.7
	}

	return &Agent{
		name:           cfg.Name,
		provider:       cfg.Provider,
		registry:       cfg.Registry,
		logger:         cfg.Logger.With().Str("agent", cfg.Name).Logger(),
		maxRounds:      cfg.MaxRounds,
		maxTokens:      cfg.MaxTokens,
		systemPrompt:   cfg.SystemPrompt,
		compactor:      cfg.Compactor,
		compactTrigger: cfg.CompactTrigger,
	}
}

// Registry 返回工具注册表
func (a *Agent) Registry() *registry.Registry {
	return a.registry
}

// RunToolLoop 执行工具调用循环（核心复用逻辑）
//
// 职责：
// 1. 管理 LLM 对话历史
// 2. 调用 LLM 并解析响应
// 3. 执行工具调用
// 4. 处理上下文压缩
// 5. 检测完成条件
//
// 参数：
// - userPrompt: 用户提示词（任务描述）
//
// 返回：
// - Response: 包含最终内容、轮数、完整消息历史
// - error: 执行错误
func (a *Agent) RunToolLoop(
	ctx context.Context,
	userPrompt string,
) (*Response, error) {
	startTime := time.Now()

	// 初始化消息历史
	messages := []provider.Message{
		{Role: provider.RoleUser, Content: a.systemPrompt},
		{Role: provider.RoleUser, Content: userPrompt},
	}

	a.logger.Info().
		Int("max_rounds", a.maxRounds).
		Int("max_tokens", a.maxTokens).
		Msg("starting tool loop")

	var totalTokens int

	for round := 0; round < a.maxRounds; round++ {
		// 1. 检查上下文是否需要压缩
		if a.compactor != nil && a.provider != nil {
			tokenCount, err := a.provider.CountTokens(ctx, provider.Request{Messages: messages})
			if err == nil && a.maxTokens > 0 {
				ratio := float64(tokenCount) / float64(a.maxTokens)
				if ratio > a.compactTrigger {
					a.logger.Info().
						Float64("ratio", ratio).
						Int("tokens", tokenCount).
						Msg("triggering compaction")

					compacted, err := a.compactor.Compact(ctx, a.provider, messages)
					if err == nil {
						messages = compacted
						a.logger.Info().Msg("compaction succeeded")
					} else {
						a.logger.Warn().Err(err).Msg("compaction failed, continuing")
					}
				}
			}
		}

		// 2. 调用 LLM
		a.logger.Debug().
			Int("round", round+1).
			Int("message_count", len(messages)).
			Msg("calling LLM")

		resp, err := a.provider.Complete(ctx, provider.Request{
			Messages:  messages,
			Tools:     a.registry.Schemas(),
			MaxTokens: a.maxTokens,
		})
		if err != nil {
			// 检查是否是上下文取消
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return &Response{
					Content:  "",
					Rounds:   round + 1,
					Messages: messages,
					Halt:     HaltCanceled,
				}, err
			}
			return nil, fmt.Errorf("LLM complete (round %d): %w", round+1, err)
		}

		totalTokens += resp.Usage.InTokens + resp.Usage.OutTokens

		a.logger.Debug().
			Int("round", round+1).
			Int("tool_calls", len(resp.ToolCalls)).
			Int("tokens_used", resp.Usage.InTokens+resp.Usage.OutTokens).
			Msg("LLM returned")

		// 3. 将 LLM 响应加入历史
		assistantMsg := provider.Message{
			Role:      provider.RoleAssistant,
			Content:   resp.Content,
			ToolCalls: resp.ToolCalls,
		}
		messages = append(messages, assistantMsg)

		// 4. 无工具调用 → 完成
		if len(resp.ToolCalls) == 0 {
			a.logger.Info().
				Int("rounds", round+1).
				Int("total_tokens", totalTokens).
				Dur("duration", time.Since(startTime)).
				Msg("tool loop completed (no tool calls)")

			return &Response{
				Content:     resp.Content,
				Rounds:      round + 1,
				Messages:    messages,
				TotalTokens: totalTokens,
				Halt:        HaltDone,
			}, nil
		}

		// 5. 执行工具调用
		a.logger.Debug().
			Int("round", round+1).
			Int("tool_count", len(resp.ToolCalls)).
			Msg("executing tools")

		results := a.registry.ExecuteParallel(ctx, resp.ToolCalls)

		// 6. 将工具结果加入历史
		for i, tc := range resp.ToolCalls {
			r := results[i]
			content := r.Output
			if r.Error != "" {
				content = "ERROR: " + r.Error
			}

			messages = append(messages, provider.Message{
				Role:       provider.RoleTool,
				ToolCallID: tc.ID,
				Content:    content,
			})
		}

		// 7. 检查 Token 预算
		if a.maxTokens > 0 && totalTokens >= a.maxTokens {
			a.logger.Warn().
				Int("total_tokens", totalTokens).
				Int("max_tokens", a.maxTokens).
				Msg("reached token budget")

			return &Response{
				Content:     resp.Content,
				Rounds:      round + 1,
				Messages:    messages,
				TotalTokens: totalTokens,
				Halt:        HaltBudget,
			}, nil
		}
	}

	// 达到最大轮数
	a.logger.Warn().
		Int("max_rounds", a.maxRounds).
		Int("total_tokens", totalTokens).
		Msg("reached max rounds")

	return &Response{
		Content:     "",
		Rounds:      a.maxRounds,
		Messages:    messages,
		TotalTokens: totalTokens,
		Halt:        HaltMaxRounds,
	}, fmt.Errorf("reached max rounds (%d)", a.maxRounds)
}

// Response 是 Agent 的响应
type Response struct {
	Content     string             // 最终内容
	Rounds      int                // 执行轮数
	Messages    []provider.Message // 完整消息历史
	TotalTokens int                // 总 Token 消耗
	Halt        HaltReason         // 停止原因
}

// HaltReason 是停止原因
type HaltReason string

const (
	HaltDone      HaltReason = "done"       // 正常完成
	HaltMaxRounds HaltReason = "max_rounds" // 达到最大轮数
	HaltBudget    HaltReason = "budget"     // 达到 Token 预算
	HaltCanceled  HaltReason = "canceled"   // 上下文取消
)
