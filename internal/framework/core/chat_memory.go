package core

import (
	"context"
	"fmt"
	"sync"
)

// ChatMemory 对话记忆接口
type ChatMemory interface {
	// AddMessage 添加消息到记忆中
	AddMessage(ctx context.Context, msg Message) error

	// GetMessages 获取所有消息
	GetMessages(ctx context.Context) ([]Message, error)

	// GetRecentMessages 获取最近的消息（受 token 预算限制）
	GetRecentMessages(ctx context.Context, tokenBudget int) ([]Message, error)

	// Clear 清空记忆
	Clear(ctx context.Context) error

	// Size 返回当前记忆中的消息数量
	Size() int
}

// BufferMemory 缓冲区记忆
// 保持最近 N 条消息在内存中
type BufferMemory struct {
	mu          sync.RWMutex
	messages    []Message
	maxMessages int
}

// NewBufferMemory 创建缓冲区记忆
func NewBufferMemory(maxMessages int) *BufferMemory {
	if maxMessages <= 0 {
		maxMessages = 10 // 默认保留 10 条消息
	}

	return &BufferMemory{
		messages:    make([]Message, 0, maxMessages),
		maxMessages: maxMessages,
	}
}

// AddMessage 添加消息
func (m *BufferMemory) AddMessage(ctx context.Context, msg Message) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.messages = append(m.messages, msg)

	// 如果超过最大数量，移除最早的消息
	if len(m.messages) > m.maxMessages {
		// 保留 system 消息
		systemMsgs := make([]Message, 0)
		otherMsgs := make([]Message, 0)

		for _, msg := range m.messages {
			if msg.Role == RoleSystem {
				systemMsgs = append(systemMsgs, msg)
			} else {
				otherMsgs = append(otherMsgs, msg)
			}
		}

		// 移除最早的非 system 消息
		excess := len(m.messages) - m.maxMessages
		if excess > 0 && len(otherMsgs) > excess {
			otherMsgs = otherMsgs[excess:]
		}

		m.messages = append(systemMsgs, otherMsgs...)
	}

	return nil
}

// GetMessages 获取所有消息
func (m *BufferMemory) GetMessages(ctx context.Context) ([]Message, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make([]Message, len(m.messages))
	copy(result, m.messages)
	return result, nil
}

// GetRecentMessages 获取最近的消息（受 token 预算限制）
func (m *BufferMemory) GetRecentMessages(ctx context.Context, tokenBudget int) ([]Message, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if tokenBudget <= 0 {
		return m.messages, nil
	}

	// 分离 system 消息和其他消息
	systemMsgs := make([]Message, 0)
	otherMsgs := make([]Message, 0)

	for _, msg := range m.messages {
		if msg.Role == RoleSystem {
			systemMsgs = append(systemMsgs, msg)
		} else {
			otherMsgs = append(otherMsgs, msg)
		}
	}

	// system 消息始终包含
	totalTokens := 0
	for _, msg := range systemMsgs {
		totalTokens += msg.TokenCount()
	}

	// 从最新的消息开始，向前累加直到达到预算
	result := make([]Message, 0)
	for i := len(otherMsgs) - 1; i >= 0; i-- {
		msgTokens := otherMsgs[i].TokenCount()
		if totalTokens+msgTokens > tokenBudget {
			break
		}
		totalTokens += msgTokens
		result = append([]Message{otherMsgs[i]}, result...)
	}

	// 合并 system 消息和选中的消息
	return append(systemMsgs, result...), nil
}

// Clear 清空记忆
func (m *BufferMemory) Clear(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.messages = make([]Message, 0, m.maxMessages)
	return nil
}

// Size 返回消息数量
func (m *BufferMemory) Size() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.messages)
}

// WindowMemory 滑动窗口记忆
// 保持固定 token 预算的最近消息
type WindowMemory struct {
	mu          sync.RWMutex
	messages    []Message
	tokenBudget int
	currentSize int
}

// NewWindowMemory 创建滑动窗口记忆
func NewWindowMemory(tokenBudget int) *WindowMemory {
	if tokenBudget <= 0 {
		tokenBudget = 4000 // 默认 4000 tokens
	}

	return &WindowMemory{
		messages:    make([]Message, 0),
		tokenBudget: tokenBudget,
		currentSize: 0,
	}
}

// AddMessage 添加消息
func (m *WindowMemory) AddMessage(ctx context.Context, msg Message) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	msgTokens := msg.TokenCount()
	m.messages = append(m.messages, msg)
	m.currentSize += msgTokens

	// 如果超过预算，移除最早的消息（保留 system 消息）
	for m.currentSize > m.tokenBudget && len(m.messages) > 1 {
		// 找到第一个非 system 消息并移除
		removed := false
		for i := 0; i < len(m.messages); i++ {
			if m.messages[i].Role != RoleSystem {
				m.currentSize -= m.messages[i].TokenCount()
				m.messages = append(m.messages[:i], m.messages[i+1:]...)
				removed = true
				break
			}
		}

		if !removed {
			// 如果只剩 system 消息且还超预算，也要移除
			if len(m.messages) > 0 {
				m.currentSize -= m.messages[0].TokenCount()
				m.messages = m.messages[1:]
			}
		}
	}

	return nil
}

// GetMessages 获取所有消息
func (m *WindowMemory) GetMessages(ctx context.Context) ([]Message, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make([]Message, len(m.messages))
	copy(result, m.messages)
	return result, nil
}

// GetRecentMessages 获取最近的消息（受 token 预算限制）
func (m *WindowMemory) GetRecentMessages(ctx context.Context, tokenBudget int) ([]Message, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if tokenBudget <= 0 || tokenBudget >= m.currentSize {
		result := make([]Message, len(m.messages))
		copy(result, m.messages)
		return result, nil
	}

	// 从最新消息向前累加
	totalTokens := 0
	result := make([]Message, 0)

	for i := len(m.messages) - 1; i >= 0; i-- {
		msgTokens := m.messages[i].TokenCount()
		if totalTokens+msgTokens > tokenBudget {
			break
		}
		totalTokens += msgTokens
		result = append([]Message{m.messages[i]}, result...)
	}

	return result, nil
}

// Clear 清空记忆
func (m *WindowMemory) Clear(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.messages = make([]Message, 0)
	m.currentSize = 0
	return nil
}

// Size 返回消息数量
func (m *WindowMemory) Size() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.messages)
}

// SummaryMemory 摘要记忆
// 当消息超过阈值时，自动生成摘要并压缩历史
type SummaryMemory struct {
	mu              sync.RWMutex
	messages        []Message
	summaries       []string
	summarizer      Summarizer
	maxMessages     int
	summaryThreshold int
}

// Summarizer 摘要生成器接口
type Summarizer interface {
	// Summarize 生成消息列表的摘要
	Summarize(ctx context.Context, messages []Message) (string, error)
}

// NewSummaryMemory 创建摘要记忆
func NewSummaryMemory(summarizer Summarizer, maxMessages int, summaryThreshold int) *SummaryMemory {
	if maxMessages <= 0 {
		maxMessages = 10
	}
	if summaryThreshold <= 0 {
		summaryThreshold = 5
	}

	return &SummaryMemory{
		messages:         make([]Message, 0),
		summaries:        make([]string, 0),
		summarizer:       summarizer,
		maxMessages:      maxMessages,
		summaryThreshold: summaryThreshold,
	}
}

// AddMessage 添加消息
func (m *SummaryMemory) AddMessage(ctx context.Context, msg Message) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.messages = append(m.messages, msg)

	// 如果消息数超过阈值，触发摘要
	if len(m.messages) > m.summaryThreshold {
		// 分离 system 消息
		systemMsgs := make([]Message, 0)
		otherMsgs := make([]Message, 0)

		for _, msg := range m.messages {
			if msg.Role == RoleSystem {
				systemMsgs = append(systemMsgs, msg)
			} else {
				otherMsgs = append(otherMsgs, msg)
			}
		}

		// 对旧消息生成摘要
		if len(otherMsgs) > m.summaryThreshold {
			toSummarize := otherMsgs[:len(otherMsgs)-m.summaryThreshold]
			summary, err := m.summarizer.Summarize(ctx, toSummarize)
			if err != nil {
				return fmt.Errorf("生成摘要失败: %w", err)
			}

			m.summaries = append(m.summaries, summary)
			m.messages = append(systemMsgs, otherMsgs[len(otherMsgs)-m.summaryThreshold:]...)
		}
	}

	return nil
}

// GetMessages 获取所有消息（包含摘要）
func (m *SummaryMemory) GetMessages(ctx context.Context) ([]Message, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make([]Message, 0, len(m.summaries)+len(m.messages))

	// 添加摘要（作为 system 消息）
	for i, summary := range m.summaries {
		result = append(result, *NewTextMessage(
			fmt.Sprintf("summary-%d", i),
			RoleSystem,
			"历史对话摘要："+summary,
		))
	}

	// 添加当前消息
	result = append(result, m.messages...)

	return result, nil
}

// GetRecentMessages 获取最近的消息（受 token 预算限制）
func (m *SummaryMemory) GetRecentMessages(ctx context.Context, tokenBudget int) ([]Message, error) {
	messages, err := m.GetMessages(ctx)
	if err != nil {
		return nil, err
	}

	if tokenBudget <= 0 {
		return messages, nil
	}

	// 从最新消息向前累加
	totalTokens := 0
	result := make([]Message, 0)

	for i := len(messages) - 1; i >= 0; i-- {
		msgTokens := messages[i].TokenCount()
		if totalTokens+msgTokens > tokenBudget {
			break
		}
		totalTokens += msgTokens
		result = append([]Message{messages[i]}, result...)
	}

	return result, nil
}

// Clear 清空记忆
func (m *SummaryMemory) Clear(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.messages = make([]Message, 0)
	m.summaries = make([]string, 0)
	return nil
}

// Size 返回消息数量（不包括摘要）
func (m *SummaryMemory) Size() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.messages)
}
