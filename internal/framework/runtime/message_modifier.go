package runtime

import (
	"context"
	"fmt"
	"time"

	"github.com/V3teran/liusha/internal/framework/llm"
)

// MessageModifier 是 LLM 消息预处理单元。
// 在 LLM 调用前对消息历史进行转换：字段注入、历史压缩、敏感信息脱敏等。
type MessageModifier interface {
	// Modify 转换消息列表，返回修改后的消息。
	// 如果返回 error，行为由 MessageModifierChain 的 ErrorStrategy 决定。
	Modify(ctx context.Context, messages []llm.Message) ([]llm.Message, error)
}

// MessageModifierChain 管理多个修改器的执行链。
type MessageModifierChain struct {
	modifiers     []MessageModifier
	errorStrategy ErrorStrategy
	retryConfig   RetryConfig
	timeoutSec    int
}

// ErrorStrategy 定义修改器链的错误处理策略。
type ErrorStrategy string

const (
	// ErrorStrategyFailFast ：第一个修改器失败立即返回错误。
	ErrorStrategyFailFast ErrorStrategy = "fail_fast"

	// ErrorStrategySkip ：失败的修改器被跳过，继续下一个。
	ErrorStrategySkip ErrorStrategy = "skip"
)

// RetryConfig 定义重试策略。
type RetryConfig struct {
	// 最大重试次数（0 = 不重试）
	MaxAttempts int

	// 初始退避时间（用于指数退避）
	InitialBackoff time.Duration

	// 最大退避时间
	MaxBackoff time.Duration

	// 重试条件函数（true = 应该重试，false = 不重试）
	ShouldRetry func(error) bool
}

// DefaultRetryConfig 返回默认重试策略：
// - 最多重试 2 次
// - 初始退避 100ms，最大退避 2s
// - 仅重试网络/超时错误
func DefaultRetryConfig() RetryConfig {
	return RetryConfig{
		MaxAttempts:    2,
		InitialBackoff: 100 * time.Millisecond,
		MaxBackoff:     2 * time.Second,
		ShouldRetry: func(err error) bool {
			return err != nil
		},
	}
}

// NewMessageModifierChain 创建修改器链。
func NewMessageModifierChain(
	modifiers []MessageModifier,
	strategy ErrorStrategy,
	retryConfig RetryConfig,
	timeoutSec int,
) *MessageModifierChain {
	if strategy != ErrorStrategyFailFast && strategy != ErrorStrategySkip {
		strategy = ErrorStrategyFailFast // 默认 fail-fast
	}
	if timeoutSec <= 0 {
		timeoutSec = 30 // 默认 30 秒超时
	}
	return &MessageModifierChain{
		modifiers:     modifiers,
		errorStrategy: strategy,
		retryConfig:   retryConfig,
		timeoutSec:    timeoutSec,
	}
}

// Apply 执行修改器链，返回最终的消息列表。
func (c *MessageModifierChain) Apply(ctx context.Context, messages []llm.Message) ([]llm.Message, error) {
	// 设置整体超时
	ctx, cancel := context.WithTimeout(ctx, time.Duration(c.timeoutSec)*time.Second)
	defer cancel()

	current := make([]llm.Message, len(messages))
	copy(current, messages)

	for _, modifier := range c.modifiers {
		var result []llm.Message
		var lastErr error

		// 重试循环
		for attempt := 0; attempt <= c.retryConfig.MaxAttempts; attempt++ {
			select {
			case <-ctx.Done():
				return nil, fmt.Errorf("message modifier chain timeout: %w", ctx.Err())
			default:
			}

			result, lastErr = modifier.Modify(ctx, current)
			if lastErr == nil {
				current = result
				break
			}

			// 判断是否应该重试
			if attempt < c.retryConfig.MaxAttempts && c.retryConfig.ShouldRetry(lastErr) {
				// 计算退避时间（指数退避）
				backoff := c.retryConfig.InitialBackoff * time.Duration(1<<uint(attempt))
				if backoff > c.retryConfig.MaxBackoff {
					backoff = c.retryConfig.MaxBackoff
				}

				select {
				case <-time.After(backoff):
					// 继续下一次重试
				case <-ctx.Done():
					return nil, fmt.Errorf("message modifier chain timeout: %w", ctx.Err())
				}
				continue
			}

			// 不应重试或已达最大重试次数
			break
		}

		// 处理修改器失败
		if lastErr != nil {
			switch c.errorStrategy {
			case ErrorStrategyFailFast:
				return nil, fmt.Errorf("message modifier failed: %w", lastErr)
			case ErrorStrategySkip:
				// 保持 current 不变，继续下一个修改器
				continue
			}
		}
	}

	return current, nil
}

// ─────────────────────────────────────────────
// 预设修改器链
// ─────────────────────────────────────────────

// NewDefaultModifierChain 创建默认的消息修改器链
// 适用于大多数 Agent：验证 → 滚动窗口压缩
func NewDefaultModifierChain(windowSize int) *MessageModifierChain {
	if windowSize <= 0 {
		windowSize = 20 // 默认保留 20 条消息
	}

	return NewMessageModifierChain(
		[]MessageModifier{
			NewValidateModifier(100000),
			NewRollingWindowModifier(windowSize),
		},
		ErrorStrategyFailFast,
		DefaultRetryConfig(),
		30,
	)
}

// ─────────────────────────────────────────────
// 内置修改器实现
// ─────────────────────────────────────────────

// RollingWindowModifier 滚动窗口压缩：保留系统消息 + 最近 N 条非系统消息
type RollingWindowModifier struct {
	windowSize int
}

// NewRollingWindowModifier 见实现。
func NewRollingWindowModifier(windowSize int) *RollingWindowModifier {
	if windowSize <= 0 {
		windowSize = 20
	}
	return &RollingWindowModifier{windowSize: windowSize}
}

// Modify 实现 RollingWindowModifier 的接口方法。
func (m *RollingWindowModifier) Modify(_ context.Context, messages []llm.Message) ([]llm.Message, error) {
	if len(messages) <= m.windowSize+1 {
		return messages, nil
	}

	// 分离系统消息和其他消息
	var systemMessages []llm.Message
	var otherMessages []llm.Message

	for _, msg := range messages {
		if msg.Role == llm.RoleSystem {
			systemMessages = append(systemMessages, msg)
		} else {
			otherMessages = append(otherMessages, msg)
		}
	}

	// 保留最近的窗口
	if len(otherMessages) > m.windowSize {
		otherMessages = otherMessages[len(otherMessages)-m.windowSize:]
	}

	// 系统消息 + 窗口内消息（显式新切片，避免 append 复用 systemMessages 底层数组）
	result := make([]llm.Message, 0, len(systemMessages)+len(otherMessages))
	result = append(result, systemMessages...)
	result = append(result, otherMessages...)
	return result, nil
}

// ValidateModifier 验证消息合法性（例如长度、格式等）。
type ValidateModifier struct {
	// NewValidateModifier 见实现。
	maxContentLength int
}

// NewValidateModifier 构造校验修饰器。
func NewValidateModifier(maxContentLength int) *ValidateModifier {
	if maxContentLength <= 0 {
		maxContentLength = 100000 // 默认 100k 字符
	}
	// Modify 实现 ValidateModifier 的接口方法。
	return &ValidateModifier{maxContentLength: maxContentLength}
}

// Modify 实现修饰器接口。
func (m *ValidateModifier) Modify(_ context.Context, messages []llm.Message) ([]llm.Message, error) {
	for i, msg := range messages {
		if len(msg.Content) > m.maxContentLength {
			return nil, fmt.Errorf("message %d exceeds max length: %d > %d", i, len(msg.Content), m.maxContentLength)
		}
		if msg.Role == "" {
			return nil, fmt.Errorf("message %d has empty role", i)
		}
	}
	return messages, nil
}
