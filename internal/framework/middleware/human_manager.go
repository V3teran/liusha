package middleware

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"
	"github.com/V3teran/liusha/internal/framework/core"
)

// HumanInteractionManagerImpl 是人机交互管理器的实现。
type HumanInteractionManagerImpl struct {
	store     HumanInputStore
	validator HumanInputValidator
	eventBus  core.EventBus
	logger    zerolog.Logger

	// 等待队列（requestID -> 响应通道）
	mu       sync.RWMutex
	waiters  map[string]chan *HumanInputResponse
	cancelFn map[string]context.CancelFunc

	// 订阅者（taskID -> 请求通道列表）
	subscribers map[string][]chan HumanInputRequest
}

// NewHumanInteractionManager 创建人机交互管理器。
func NewHumanInteractionManager(
	store HumanInputStore,
	validator HumanInputValidator,
	eventBus core.EventBus,
	logger zerolog.Logger,
) *HumanInteractionManagerImpl {
	return &HumanInteractionManagerImpl{
		store:       store,
		validator:   validator,
		eventBus:    eventBus,
		logger:      logger.With().Str("component", "human_interaction").Logger(),
		waiters:     make(map[string]chan *HumanInputResponse),
		cancelFn:    make(map[string]context.CancelFunc),
		subscribers: make(map[string][]chan HumanInputRequest),
	}
}

// RequestInput 请求人工输入（阻塞直到收到输入或超时）。
func (h *HumanInteractionManagerImpl) RequestInput(ctx context.Context, req HumanInputRequest) (*HumanInputResponse, error) {
	// 生成 ID
	if req.ID == "" {
		req.ID = uuid.New().String()
	}
	req.CreatedAt = time.Now()

	h.logger.Info().
		Str("request_id", req.ID).
		Str("task_id", req.TaskID).
		Str("input_type", req.InputType).
		Msg("requesting human input")

	// 保存请求
	if err := h.store.SaveRequest(ctx, req); err != nil {
		return nil, fmt.Errorf("save request: %w", err)
	}

	// 创建响应通道
	respCh := make(chan *HumanInputResponse, 1)
	h.mu.Lock()
	h.waiters[req.ID] = respCh
	h.mu.Unlock()

	// 清理函数
	defer func() {
		h.mu.Lock()
		delete(h.waiters, req.ID)
		delete(h.cancelFn, req.ID)
		h.mu.Unlock()
		close(respCh)
	}()

	// 发布事件
	if h.eventBus != nil {
		event := core.NewEvent(core.EventHumanInputRequired, req.TaskID, "human", map[string]any{
			"request_id": req.ID,
			"node_id":    req.NodeID,
			"input_type": req.InputType,
			"prompt":     req.Prompt,
		})
		h.eventBus.Publish(ctx, event)
	}

	// 通知订阅者
	h.notifySubscribers(req)

	// 设置超时
	timeout := time.Duration(req.TimeoutSec) * time.Second
	if timeout == 0 {
		timeout = 1 * time.Hour // 默认 1 小时
	}

	timeoutCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	h.mu.Lock()
	h.cancelFn[req.ID] = cancel
	h.mu.Unlock()

	// 阻塞等待响应或超时
	select {
	case resp := <-respCh:
		h.logger.Info().
			Str("request_id", req.ID).
			Str("submitter", resp.Submitter).
			Msg("human input received")
		return resp, nil

	case <-timeoutCtx.Done():
		h.logger.Warn().
			Str("request_id", req.ID).
			Dur("timeout", timeout).
			Msg("human input timeout")

		// 使用默认值
		if req.DefaultValue != "" {
			resp := &HumanInputResponse{
				RequestID:   req.ID,
				TaskID:      req.TaskID,
				NodeID:      req.NodeID,
				Value:       req.DefaultValue,
				Approved:    false,
				SubmittedAt: time.Now(),
				Submitter:   "system(timeout)",
			}
			return resp, nil
		}

		return nil, core.ErrHumanInputTimeout{
			RequestID: req.ID,
			Timeout:   req.TimeoutSec,
		}

	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// SubmitInput 提交人工输入。
func (h *HumanInteractionManagerImpl) SubmitInput(ctx context.Context, resp HumanInputResponse) error {
	// 获取请求
	req, err := h.store.GetRequest(ctx, resp.RequestID)
	if err != nil {
		return fmt.Errorf("get request: %w", err)
	}

	// 验证输入
	if h.validator != nil {
		if err := h.validator.Validate(ctx, *req, resp); err != nil {
			return fmt.Errorf("validate input: %w", err)
		}
	}

	// 设置时间戳
	resp.SubmittedAt = time.Now()

	// 保存响应
	if err := h.store.SaveResponse(ctx, resp); err != nil {
		return fmt.Errorf("save response: %w", err)
	}

	h.logger.Info().
		Str("request_id", resp.RequestID).
		Str("submitter", resp.Submitter).
		Msg("human input submitted")

	// 通知等待者
	h.mu.RLock()
	waiter, exists := h.waiters[resp.RequestID]
	h.mu.RUnlock()

	if exists {
		select {
		case waiter <- &resp:
			// 发送成功
		default:
			// 通道已满或已关闭
		}
	}

	// 发布事件
	if h.eventBus != nil {
		event := core.NewEvent(core.EventHumanInputReceived, resp.TaskID, "human", map[string]any{
			"request_id": resp.RequestID,
			"node_id":    resp.NodeID,
			"approved":   resp.Approved,
			"submitter":  resp.Submitter,
		})
		h.eventBus.Publish(ctx, event)
	}

	return nil
}

// ListPending 列出待处理的请求。
func (h *HumanInteractionManagerImpl) ListPending(ctx context.Context, taskID string) ([]HumanInputRequest, error) {
	filter := HumanInputFilter{
		TaskID: taskID,
		Status: "pending",
		Limit:  100,
	}

	requests, err := h.store.ListRequests(ctx, filter)
	if err != nil {
		return nil, fmt.Errorf("list requests: %w", err)
	}

	return requests, nil
}

// Cancel 取消请求。
func (h *HumanInteractionManagerImpl) Cancel(ctx context.Context, requestID string) error {
	h.logger.Info().Str("request_id", requestID).Msg("canceling human input request")

	// 取消等待
	h.mu.Lock()
	if cancel, exists := h.cancelFn[requestID]; exists {
		cancel()
	}
	h.mu.Unlock()

	// 更新状态（需要在 store 中实现 UpdateStatus 方法）
	// 这里简化处理，实际需要更新数据库状态

	return nil
}

// Subscribe 订阅新请求（用于前端 SSE 推送）。
func (h *HumanInteractionManagerImpl) Subscribe(ctx context.Context, taskID string) (<-chan HumanInputRequest, error) {
	ch := make(chan HumanInputRequest, 10)

	h.mu.Lock()
	h.subscribers[taskID] = append(h.subscribers[taskID], ch)
	h.mu.Unlock()

	// 自动取消订阅
	go func() {
		<-ctx.Done()
		h.mu.Lock()
		defer h.mu.Unlock()

		subs := h.subscribers[taskID]
		for i, sub := range subs {
			if sub == ch {
				h.subscribers[taskID] = append(subs[:i], subs[i+1:]...)
				close(ch)
				break
			}
		}
	}()

	return ch, nil
}

// notifySubscribers 通知订阅者有新请求。
func (h *HumanInteractionManagerImpl) notifySubscribers(req HumanInputRequest) {
	h.mu.RLock()
	subs := h.subscribers[req.TaskID]
	h.mu.RUnlock()

	for _, ch := range subs {
		select {
		case ch <- req:
			// 发送成功
		default:
			// 订阅者太慢，跳过
		}
	}
}

// Close 关闭管理器。
func (h *HumanInteractionManagerImpl) Close() error {
	h.mu.Lock()
	defer h.mu.Unlock()

	// 取消所有等待
	for _, cancel := range h.cancelFn {
		cancel()
	}

	// 关闭所有订阅
	for _, subs := range h.subscribers {
		for _, ch := range subs {
			close(ch)
		}
	}

	h.waiters = make(map[string]chan *HumanInputResponse)
	h.cancelFn = make(map[string]context.CancelFunc)
	h.subscribers = make(map[string][]chan HumanInputRequest)

	return nil
}
