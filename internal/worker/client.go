package worker

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/hibiken/asynq"
)

// Client 是 worker 模块的对外入队客户端，封装 asynq.Client 并强制幂等。
type Client struct {
	inner *asynq.Client
}

// NewClient 基于给定 Redis 连接选项构造 Client。
func NewClient(opt asynq.RedisConnOpt) *Client {
	return &Client{inner: asynq.NewClient(opt)}
}

// Close 释放底层 asynq.Client 的 Redis 连接。
func (c *Client) Close() error {
	return c.inner.Close()
}

// defaultUniqueTTL 是 agent:run 任务的 Unique key 存活时间，覆盖最长合理执行窗口。
const defaultUniqueTTL = 24 * time.Hour

// EnqueueAgentRun 将一个 AgentRunPayload 入队到默认队列。
//
// 使用 payload.TaskID 同时作为 asynq.TaskID 和 asynq.Unique 的 key，保证：
//   - 相同 TaskID 在 uniqueTTL 内只会成功入队一次
//   - 重复入队返回 ErrDuplicateTask / ErrTaskIDConflict，可通过 IsDuplicate 判定
func (c *Client) EnqueueAgentRun(ctx context.Context, p AgentRunPayload) (*asynq.TaskInfo, error) {
	task, err := NewAgentRunTask(p)
	if err != nil {
		return nil, err
	}

	info, err := c.inner.EnqueueContext(
		ctx,
		task,
		asynq.TaskID(p.TaskID.String()),
		asynq.Unique(defaultUniqueTTL),
		asynq.MaxRetry(3),
		asynq.Timeout(30*time.Minute),
	)
	if err != nil {
		return nil, fmt.Errorf("入队 agent:run 失败: %w", err)
	}
	return info, nil
}

// IsDuplicate 判定 error 是否来自 Asynq 的幂等检测（重复入队同一 TaskID）。
func IsDuplicate(err error) bool {
	return errors.Is(err, asynq.ErrDuplicateTask) || errors.Is(err, asynq.ErrTaskIDConflict)
}
