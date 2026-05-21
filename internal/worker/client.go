package worker

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/hibiken/asynq"
)

// Client 是 asynq 生产者的薄封装，按 Role 路由到对应队列，并以 Payload.TaskID 实现幂等。
type Client struct {
	c *asynq.Client
}

// NewClient 创建一个 Client。调用方负责 Close。
func NewClient(opt asynq.RedisClientOpt) *Client {
	return &Client{c: asynq.NewClient(opt)}
}

// Close 释放底层连接。
func (c *Client) Close() error { return c.c.Close() }

// Enqueue 投递一个任务到 role 对应的队列。
//
// 幂等：用 p.TaskID 作为 asynq 的 task ID，重复 Enqueue 同一 TaskID 会返回 asynq.ErrTaskIDConflict。
// opts 透传给 asynq.NewTask（如 asynq.MaxRetry(0) 用于 active commander禁止重试——
// active 4h × asynq 默认 25 retry = 4 天死循环，且 retry 接管必弄 PG 僵尸态）。
// 返回 (asynq 分配的 task ID, queue 名, error)。
func (c *Client) Enqueue(ctx context.Context, role Role, p Payload, opts ...asynq.Option) (string, string, error) {
	if p.TaskID == "" {
		return "", "", fmt.Errorf("worker: payload.TaskID 不能为空")
	}
	// Role 字段以 role 参数为准；强制覆盖，确保消费端路由与队列名一致。
	p.Role = role

	body, err := json.Marshal(p)
	if err != nil {
		return "", "", fmt.Errorf("worker: 序列化 payload 失败: %w", err)
	}

	taskOpts := append([]asynq.Option{
		asynq.Queue(role.Queue()),
		asynq.TaskID(p.TaskID),
	}, opts...)
	task := asynq.NewTask(TaskTypeRun, body, taskOpts...)

	info, err := c.c.EnqueueContext(ctx, task)
	if err != nil {
		return "", "", fmt.Errorf("worker: 入队 role=%s task=%s 失败: %w", role, p.TaskID, err)
	}
	return info.ID, info.Queue, nil
}
