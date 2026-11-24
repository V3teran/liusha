package runtime

import (
	"context"
	"fmt"
	"time"

	"github.com/V3teran/liusha/internal/framework/llm"
)

// MessageModifier 是 LLM 消息修改单元。
// 链产出收敛回权威消息历史（检查点语义）：压缩类修改器按需收敛历史（裁掉
// 超预算前缀并补纪要），校验类修改器作不变量哨兵。每次 LLM 调用前执行，
// 产出即此后追加的底座。
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

// chainSlack 是压缩链总超时比单次蒸馏时限多出的余量。
const chainSlack = 5 * time.Second

// CompactionTimeouts 是一步推理内部压缩链的时间预算：Distill 是单次纪要蒸馏的时限，Chain 是
// 整条链的总超时，比蒸馏略长——链里只有蒸馏会耗时，蒸馏先于它到期，慢的蒸馏走降级路径
// 而不是把整步推理弄死。它跑在「一步推理」内部、上一次进展之后，执行者看门狗的停摆窗口
// 把 Chain 直接计入（见 executor.StallTimeout）。
type CompactionTimeouts struct {
	Distill time.Duration
	Chain   time.Duration
}

// CompactionTimeoutsFor 由单步 LLM 看门狗上限推导压缩链的时间预算：蒸馏的时限取它的一半。
// 不写死成常数：单步上限可以在系统配置里热改，模型慢时调到 900 秒，写死的 150 秒就不够用
// （蒸馏恰恰在模型慢的时候最容易超时），模型快时它又白等。
//
// 按一半取数的依据（缺省单步上限 300 秒时为 150 秒）：蒸馏时限里要容得下两件事。其一，为
// 账户在途名额排队——蒸馏调用至多用掉剩余预算的一半去排队；蒸馏排在优先队首，拿到的是下一个
// 空出的名额，重载时四个名额都被长调用占着，要等上几十秒。其二，请求本身——纪要走中档模型，
// 同形态调用实测中位 19 秒、p90 40 秒、p99 88 秒；150 秒让排队的蒸馏也给请求留下 75 秒。
// 定得偏紧的代价是纪要整段丢失（被裁掉的历史只剩一句占位声明）：库里留存的蒸馏记录里，
// 20 秒时限下超时 43 次、成功 16 次；重载时段 90 秒时限下两次蒸馏两次超时。
func CompactionTimeoutsFor(stepLLM time.Duration) CompactionTimeouts {
	distill := stepLLM / 2
	return CompactionTimeouts{Distill: distill, Chain: distill + chainSlack}
}

// NewCompactionChain 构建默认消息修改链：上下文压缩（token 预算 + 蒸馏纪要）
// → 长度校验哨兵。
//
// 压缩在前：超预算的臃肿前缀先被裁掉，校验只看裁后的窗口——单条超长消息在
// 正常路径下由压缩解决，越线即真不变量（工具输出另有运行时钳制兜底）。
// budgetTokens 是历史总预算（比例 × provider.context_window，引擎侧解析）；
// role 是角色名（蒸馏 prompt 定侧重）；d 可空（降级纯截断）；timeouts 见 CompactionTimeoutsFor。
func NewCompactionChain(budgetTokens int, role string, d Distiller, timeouts CompactionTimeouts) *MessageModifierChain {
	return NewMessageModifierChain(
		[]MessageModifier{
			NewContextCompactor(budgetTokens, role, d, timeouts.Distill),
			NewValidateModifier(100000),
		},
		ErrorStrategyFailFast,
		DefaultRetryConfig(),
		int(timeouts.Chain/time.Second),
	)
}

// ─────────────────────────────────────────────
// 内置修改器实现
// ─────────────────────────────────────────────

// ValidateModifier 验证消息合法性（例如长度、格式等）。
type ValidateModifier struct {
	maxContentLength int
}

// NewValidateModifier 构造校验修饰器（maxContentLength<=0 用默认 100k 字符）。
func NewValidateModifier(maxContentLength int) *ValidateModifier {
	if maxContentLength <= 0 {
		maxContentLength = 100000 // 默认 100k 字符
	}
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
