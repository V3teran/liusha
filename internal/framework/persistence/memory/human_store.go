package memory

import (
	"context"
	"sync"
	"time"

	"github.com/V3teran/liusha/internal/framework/middleware"
)

// HumanInputStore 是内存版人工输入存储。
type HumanInputStore struct {
	mu        sync.RWMutex
	requests  map[string]*middleware.HumanInputRequest
	responses map[string]*middleware.HumanInputResponse
}

// NewHumanInputStore 创建内存版存储。
func NewHumanInputStore() *HumanInputStore {
	return &HumanInputStore{
		requests:  make(map[string]*middleware.HumanInputRequest),
		responses: make(map[string]*middleware.HumanInputResponse),
	}
}

// SaveRequest 保存请求。
func (s *HumanInputStore) SaveRequest(ctx context.Context, req middleware.HumanInputRequest) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.requests[req.ID] = &req
	return nil
}

// SaveResponse 保存响应。
func (s *HumanInputStore) SaveResponse(ctx context.Context, resp middleware.HumanInputResponse) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.responses[resp.RequestID] = &resp

	// 更新请求状态
	if req, exists := s.requests[resp.RequestID]; exists {
		if req.Metadata == nil {
			req.Metadata = make(map[string]any)
		}
		req.Metadata["status"] = "completed"
		req.Metadata["completed_at"] = time.Now()
	}

	return nil
}

// GetRequest 获取请求。
func (s *HumanInputStore) GetRequest(ctx context.Context, requestID string) (*middleware.HumanInputRequest, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	req, exists := s.requests[requestID]
	if !exists {
		return nil, nil
	}

	// 返回副本
	copied := *req
	return &copied, nil
}

// GetResponse 获取响应。
func (s *HumanInputStore) GetResponse(ctx context.Context, requestID string) (*middleware.HumanInputResponse, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	resp, exists := s.responses[requestID]
	if !exists {
		return nil, nil
	}

	// 返回副本
	copied := *resp
	return &copied, nil
}

// ListRequests 列出请求。
func (s *HumanInputStore) ListRequests(ctx context.Context, filter middleware.HumanInputFilter) ([]middleware.HumanInputRequest, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var result []middleware.HumanInputRequest

	for _, req := range s.requests {
		// 应用过滤器
		if filter.TaskID != "" && req.TaskID != filter.TaskID {
			continue
		}

		// 状态过滤
		if filter.Status != "" {
			status := "pending"
			if req.Metadata != nil {
				if s, ok := req.Metadata["status"].(string); ok {
					status = s
				}
			}
			if status != filter.Status {
				continue
			}
		}

		// 时间范围过滤
		if !filter.StartTime.IsZero() && req.CreatedAt.Before(filter.StartTime) {
			continue
		}
		if !filter.EndTime.IsZero() && req.CreatedAt.After(filter.EndTime) {
			continue
		}

		// 返回副本
		copied := *req
		result = append(result, copied)
	}

	// 分页
	if filter.Offset > 0 {
		if filter.Offset >= len(result) {
			return []middleware.HumanInputRequest{}, nil
		}
		result = result[filter.Offset:]
	}
	if filter.Limit > 0 && len(result) > filter.Limit {
		result = result[:filter.Limit]
	}

	return result, nil
}

// Clear 清空所有数据（仅测试用）。
func (s *HumanInputStore) Clear() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.requests = make(map[string]*middleware.HumanInputRequest)
	s.responses = make(map[string]*middleware.HumanInputResponse)
}
