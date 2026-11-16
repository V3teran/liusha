package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/hibiken/asynq"
	"github.com/redis/go-redis/v9"
)

// Client 是 asynq 生产者的薄封装，按 TaskRole 路由到对应队列，并以 Payload.AgentRunID 实现幂等。
type Client struct {
	c *asynq.Client
}

// NewClient 创建一个 Client。调用方负责 Close。
func NewClient(opt asynq.RedisClientOpt) *Client {
	return &Client{c: asynq.NewClient(opt)}
}

// RedisOpt 把 go-redis 连接选项换算为任务队列的连接选项：队列与其它 Redis 用途连同一个实例、
// 同一个库，用户名、密码、TLS、超时与连接池一致。
func RedisOpt(o *redis.Options) asynq.RedisClientOpt {
	return asynq.RedisClientOpt{
		Network:      o.Network,
		Addr:         o.Addr,
		Username:     o.Username,
		Password:     o.Password,
		DB:           o.DB,
		DialTimeout:  o.DialTimeout,
		ReadTimeout:  o.ReadTimeout,
		WriteTimeout: o.WriteTimeout,
		PoolSize:     o.PoolSize,
		TLSConfig:    o.TLSConfig,
	}
}

// Close 释放底层连接。
func (c *Client) Close() error { return c.c.Close() }

// runQueueTimeout 是 run 在队列层的时限。asynq 未设时限时缺省 30 分钟就取消处理函数，且
// 无法表达「不限」（Timeout(0) 等同未设置）；run 的墙钟上限由 runner 处理函数按
// runner.agent_run_timeout_seconds 执行，队列层用远超任何 run 的值，不参与截断。
const runQueueTimeout = 365 * 24 * time.Hour

// Enqueue 投递一个 run 到 role 对应的队列。所有生产者（首次下发、继续、续接、恢复监控重投、
// 代理捕获）同一份入队语义，在这里定死，不由调用方各自传：
//   - 幂等：p.AgentRunID 作为 asynq 的 task ID，重复入队同一 run 返回 asynq.ErrTaskIDConflict。
//   - 不重试（MaxRetry(0)）：重试接管的新进程没有原进程的认知循环状态，只会产出脏状态；
//     中断的任务由恢复监控按探索图续跑。
//   - 队列层不设短时限（runQueueTimeout）。
//
// 返回 (asynq 分配的 task ID, queue 名, error)。
func (c *Client) Enqueue(ctx context.Context, role TaskRole, p Payload) (string, string, error) {
	if p.AgentRunID == "" {
		return "", "", fmt.Errorf("worker: payload.AgentRunID 不能为空")
	}
	// TaskRole 字段以 role 参数为准；强制覆盖，确保消费端路由与队列名一致。
	p.TaskRole = role

	body, err := json.Marshal(p)
	if err != nil {
		return "", "", fmt.Errorf("worker: 序列化 payload 失败: %w", err)
	}

	task := asynq.NewTask(TaskTypeRun, body,
		asynq.Queue(role.Queue()),
		asynq.TaskID(p.AgentRunID),
		asynq.MaxRetry(0),
		asynq.Timeout(runQueueTimeout),
	)

	info, err := c.c.EnqueueContext(ctx, task)
	if err != nil {
		return "", "", fmt.Errorf("worker: 入队 role=%s run=%s 失败: %w", role, p.AgentRunID, err)
	}
	return info.ID, info.Queue, nil
}
