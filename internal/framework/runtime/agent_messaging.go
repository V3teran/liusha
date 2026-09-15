package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
)

// ─────────────────────────────────────────────
//  Agent 消息传递接口
// ─────────────────────────────────────────────

// MessageType 消息类型
type MessageType string

const (
	// MessageTypeRequest 请求消息（期待响应）
	MessageTypeRequest MessageType = "request"
	// MessageTypeResponse 响应消息
	MessageTypeResponse MessageType = "response"
	// MessageTypeEvent 事件消息（单向通知）
	MessageTypeEvent MessageType = "event"
	// MessageTypeBroadcast 广播消息
	MessageTypeBroadcast MessageType = "broadcast"
)

// AgentMessage Agent 间传递的消息
type AgentMessage struct {
	// 消息唯一标识
	ID string `json:"id"`

	// 消息类型
	Type MessageType `json:"type"`

	// 发送者 Agent ID
	From string `json:"from"`

	// 接收者 Agent ID（广播时为空）
	To string `json:"to"`

	// 消息主题/事件类型（用于订阅过滤）
	Subject string `json:"subject,omitempty"`

	// 消息内容（业务自定义）
	Payload json.RawMessage `json:"payload"`

	// 回复目标消息 ID（响应消息时使用）
	ReplyTo string `json:"reply_to,omitempty"`

	// 创建时间
	CreatedAt time.Time `json:"created_at"`

	// 元数据
	Metadata map[string]string `json:"metadata,omitempty"`
}

// NewAgentMessage 创建 Agent 消息
func NewAgentMessage(from, to string, msgType MessageType, payload any) (*AgentMessage, error) {
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("序列化消息内容失败: %w", err)
	}

	return &AgentMessage{
		ID:        uuid.New().String(),
		Type:      msgType,
		From:      from,
		To:        to,
		Payload:   payloadBytes,
		CreatedAt: time.Now(),
		Metadata:  make(map[string]string),
	}, nil
}

// AgentMessaging Agent 消息传递接口（每个 Agent 持有一个实例）
type AgentMessaging interface {
	// AgentID 返回当前 Agent ID
	AgentID() string

	// Send 发送消息到指定 Agent（异步，不等待响应）
	Send(ctx context.Context, to string, msgType MessageType, payload any) error

	// SendMessage 发送已构造的消息
	SendMessage(ctx context.Context, msg *AgentMessage) error

	// Receive 接收消息（阻塞直到有消息或超时/取消）
	Receive(ctx context.Context) (*AgentMessage, error)

	// Request 发送请求并等待响应（同步请求-响应模式）
	Request(ctx context.Context, to string, payload any, timeout time.Duration) (*AgentMessage, error)

	// Reply 回复消息
	Reply(ctx context.Context, originalMsg *AgentMessage, payload any) error

	// Broadcast 广播消息给所有 Agent（除自己）
	Broadcast(ctx context.Context, subject string, payload any) error

	// Subscribe 订阅特定主题的事件（接收到匹配 Subject 的消息时调用 handler）
	Subscribe(subject string, handler MessageHandler) error

	// Unsubscribe 取消订阅
	Unsubscribe(subject string) error

	// Close 关闭消息通道（不再接收消息）
	Close() error
}

// MessageHandler 消息处理函数
type MessageHandler func(ctx context.Context, msg *AgentMessage) error

// MessageBroker Agent 消息代理（中心化的消息路由器）
type MessageBroker interface {
	// Register 注册 Agent（返回该 Agent 的消息通道）
	Register(agentID string, bufferSize int) (AgentMessaging, error)

	// Unregister 注销 Agent
	Unregister(agentID string) error

	// ListAgents 列出所有在线 Agent
	ListAgents() []string

	// GetAgent 获取指定 Agent 的消息接口（用于直接发送）
	GetAgent(agentID string) (AgentMessaging, bool)

	// Shutdown 关闭代理（关闭所有 Agent 通道）
	Shutdown(ctx context.Context) error
}

// ─────────────────────────────────────────────
//  默认实现
// ─────────────────────────────────────────────

// DefaultMessageBroker 基于 Channel 的消息代理实现
type DefaultMessageBroker struct {
	mu     sync.RWMutex
	agents map[string]*agentMailbox // agentID -> mailbox
	closed bool
}

// agentMailbox Agent 的邮箱（包含 inbox 和订阅信息）
type agentMailbox struct {
	agentID        string
	inbox          chan *AgentMessage        // 普通消息队列（请求、事件、广播）
	responseInbox  chan *AgentMessage        // 响应消息队列（单独处理）
	subscriptions  map[string]MessageHandler // subject -> handler
	subMu          sync.RWMutex              // 订阅锁
	broker         *DefaultMessageBroker     // 回指代理
	pendingReplies sync.Map                  // 待响应的请求：requestID -> chan *AgentMessage
	ctx            context.Context           // 邮箱上下文
	cancel         context.CancelFunc        // 取消函数
}

// NewMessageBroker 创建消息代理
func NewMessageBroker() MessageBroker {
	return &DefaultMessageBroker{
		agents: make(map[string]*agentMailbox),
	}
}

// Register 注册 Agent
func (b *DefaultMessageBroker) Register(agentID string, bufferSize int) (AgentMessaging, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.closed {
		return nil, fmt.Errorf("消息代理已关闭")
	}

	if _, exists := b.agents[agentID]; exists {
		return nil, fmt.Errorf("Agent %s 已注册", agentID)
	}

	if bufferSize <= 0 {
		bufferSize = 100 // 默认缓冲大小
	}

	ctx, cancel := context.WithCancel(context.Background())

	mailbox := &agentMailbox{
		agentID:       agentID,
		inbox:         make(chan *AgentMessage, bufferSize),
		responseInbox: make(chan *AgentMessage, bufferSize),
		subscriptions: make(map[string]MessageHandler),
		broker:        b,
		ctx:           ctx,
		cancel:        cancel,
	}

	// 启动后台响应处理器
	go mailbox.handleResponses()

	b.agents[agentID] = mailbox
	return mailbox, nil
}

// Unregister 注销 Agent
func (b *DefaultMessageBroker) Unregister(agentID string) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	mailbox, exists := b.agents[agentID]
	if !exists {
		return fmt.Errorf("Agent %s 未注册", agentID)
	}

	mailbox.cancel() // 停止后台处理器
	close(mailbox.inbox)
	close(mailbox.responseInbox)
	delete(b.agents, agentID)
	return nil
}

// ListAgents 列出所有在线 Agent
func (b *DefaultMessageBroker) ListAgents() []string {
	b.mu.RLock()
	defer b.mu.RUnlock()

	agents := make([]string, 0, len(b.agents))
	for agentID := range b.agents {
		agents = append(agents, agentID)
	}
	return agents
}

// GetAgent 获取指定 Agent
func (b *DefaultMessageBroker) GetAgent(agentID string) (AgentMessaging, bool) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	mailbox, exists := b.agents[agentID]
	return mailbox, exists
}

// Shutdown 关闭代理
func (b *DefaultMessageBroker) Shutdown(ctx context.Context) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.closed {
		return nil
	}

	b.closed = true
	for agentID, mailbox := range b.agents {
		close(mailbox.inbox)
		delete(b.agents, agentID)
	}

	return nil
}

// ─────────────────────────────────────────────
//  agentMailbox 实现 AgentMessaging 接口
// ─────────────────────────────────────────────

// handleResponses 后台处理响应消息（自动将响应路由到等待的请求）
func (m *agentMailbox) handleResponses() {
	for {
		select {
		case msg, ok := <-m.responseInbox:
			if !ok {
				return
			}

			// 响应消息：通知等待的请求
			if msg.ReplyTo != "" {
				if ch, ok := m.pendingReplies.LoadAndDelete(msg.ReplyTo); ok {
					replyCh := ch.(chan *AgentMessage)
					select {
					case replyCh <- msg:
					case <-m.ctx.Done():
						return
					}
				}
			}

		case <-m.ctx.Done():
			return
		}
	}
}

// AgentID 返回当前 Agent ID
func (m *agentMailbox) AgentID() string {
	return m.agentID
}

// Send 发送消息到指定 Agent
func (m *agentMailbox) Send(ctx context.Context, to string, msgType MessageType, payload any) error {
	msg, err := NewAgentMessage(m.agentID, to, msgType, payload)
	if err != nil {
		return err
	}
	return m.SendMessage(ctx, msg)
}

// SendMessage 发送已构造的消息
func (m *agentMailbox) SendMessage(ctx context.Context, msg *AgentMessage) error {
	if msg.From == "" {
		msg.From = m.agentID
	}

	// 查找目标 Agent
	m.broker.mu.RLock()
	targetMailbox, exists := m.broker.agents[msg.To]
	m.broker.mu.RUnlock()

	if !exists {
		return fmt.Errorf("目标 Agent %s 不存在或已离线", msg.To)
	}

	// 根据消息类型选择目标队列
	var targetChan chan *AgentMessage
	if msg.Type == MessageTypeResponse {
		targetChan = targetMailbox.responseInbox
	} else {
		targetChan = targetMailbox.inbox
	}

	// 非阻塞发送（如果队列满了会立即返回错误）
	select {
	case targetChan <- msg:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	default:
		return fmt.Errorf("目标 Agent %s 消息队列已满", msg.To)
	}
}

// Receive 接收消息
func (m *agentMailbox) Receive(ctx context.Context) (*AgentMessage, error) {
	select {
	case msg, ok := <-m.inbox:
		if !ok {
			return nil, fmt.Errorf("消息通道已关闭")
		}

		// 如果是事件消息且有订阅，触发处理器
		if msg.Type == MessageTypeEvent && msg.Subject != "" {
			m.subMu.RLock()
			handler, exists := m.subscriptions[msg.Subject]
			m.subMu.RUnlock()

			if exists {
				// 异步处理（不阻塞接收循环）
				go func() {
					_ = handler(ctx, msg)
				}()
			}
		}

		return msg, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// Request 发送请求并等待响应
func (m *agentMailbox) Request(ctx context.Context, to string, payload any, timeout time.Duration) (*AgentMessage, error) {
	// 创建请求消息
	msg, err := NewAgentMessage(m.agentID, to, MessageTypeRequest, payload)
	if err != nil {
		return nil, err
	}

	// 创建响应通道
	replyCh := make(chan *AgentMessage, 1)
	m.pendingReplies.Store(msg.ID, replyCh)
	defer m.pendingReplies.Delete(msg.ID)

	// 发送请求
	if err := m.SendMessage(ctx, msg); err != nil {
		return nil, err
	}

	// 等待响应
	timeoutCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	select {
	case reply := <-replyCh:
		return reply, nil
	case <-timeoutCtx.Done():
		return nil, fmt.Errorf("等待响应超时")
	}
}

// Reply 回复消息
func (m *agentMailbox) Reply(ctx context.Context, originalMsg *AgentMessage, payload any) error {
	msg, err := NewAgentMessage(m.agentID, originalMsg.From, MessageTypeResponse, payload)
	if err != nil {
		return err
	}

	msg.ReplyTo = originalMsg.ID
	return m.SendMessage(ctx, msg)
}

// Broadcast 广播消息
func (m *agentMailbox) Broadcast(ctx context.Context, subject string, payload any) error {
	msg, err := NewAgentMessage(m.agentID, "", MessageTypeBroadcast, payload)
	if err != nil {
		return err
	}

	msg.Subject = subject

	// 发送给所有其他 Agent
	m.broker.mu.RLock()
	agents := make([]*agentMailbox, 0, len(m.broker.agents))
	for agentID, mailbox := range m.broker.agents {
		if agentID != m.agentID { // 排除自己
			agents = append(agents, mailbox)
		}
	}
	m.broker.mu.RUnlock()

	// 并发发送（非阻塞）
	for _, targetMailbox := range agents {
		select {
		case targetMailbox.inbox <- msg:
		case <-ctx.Done():
			return ctx.Err()
		default:
			// 队列满了，跳过该 Agent
		}
	}

	return nil
}

// Subscribe 订阅事件
func (m *agentMailbox) Subscribe(subject string, handler MessageHandler) error {
	m.subMu.Lock()
	defer m.subMu.Unlock()

	if _, exists := m.subscriptions[subject]; exists {
		return fmt.Errorf("已订阅主题 %s", subject)
	}

	m.subscriptions[subject] = handler
	return nil
}

// Unsubscribe 取消订阅
func (m *agentMailbox) Unsubscribe(subject string) error {
	m.subMu.Lock()
	defer m.subMu.Unlock()

	if _, exists := m.subscriptions[subject]; !exists {
		return fmt.Errorf("未订阅主题 %s", subject)
	}

	delete(m.subscriptions, subject)
	return nil
}

// Close 关闭消息通道
func (m *agentMailbox) Close() error {
	return m.broker.Unregister(m.agentID)
}
