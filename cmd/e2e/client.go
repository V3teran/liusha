package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// KnowledgeGraphClient 是知识图谱 API 的 HTTP 客户端
type KnowledgeGraphClient struct {
	baseURL string
	apiKey  string
	client  *http.Client
}

// NewKnowledgeGraphClient 创建客户端
func NewKnowledgeGraphClient(baseURL, apiKey string) *KnowledgeGraphClient {
	return &KnowledgeGraphClient{
		baseURL: baseURL,
		apiKey:  apiKey,
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// GetTaskStats 获取任务的节点类型统计
func (c *KnowledgeGraphClient) GetTaskStats(ctx context.Context, taskID string) (GraphStats, error) {
	url := fmt.Sprintf("%s/api/v1/tasks/%s/stats", c.baseURL, taskID)

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return GraphStats{}, fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("X-API-Key", c.apiKey)

	resp, err := c.client.Do(req)
	if err != nil {
		return GraphStats{}, fmt.Errorf("do request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return GraphStats{}, fmt.Errorf("unexpected status %d: %s", resp.StatusCode, string(body))
	}

	var stats GraphStats
	if err := json.NewDecoder(resp.Body).Decode(&stats); err != nil {
		return GraphStats{}, fmt.Errorf("decode response: %w", err)
	}

	return stats, nil
}
