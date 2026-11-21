package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/hibiken/asynq"
)

// 经验沉淀（任务自然收敛后把验证过的结论整合进知识库）是持久化的队列任务：它要调用两次 LLM、
// 耗时分钟级，在进程里起协程跑会随重启丢失；入队后，停机中断或调用失败都会留在队列里重试。
const (
	// QueueDistill 是经验沉淀的队列。与 agent 队列分开，消费端以较低权重处理，积压时不挤占任务执行。
	QueueDistill = "distill"
	// TaskTypeDistill 是经验沉淀的任务类型。
	TaskTypeDistill = "corpus.distill"
	// distillMaxRetry 是沉淀失败（含停机中断）后的重试次数。整合对同一任务幂等：印证只记一次，
	// 近似条目判为印证，重试不会写出重复知识。
	distillMaxRetry = 3
)

type distillPayload struct {
	TaskID string `json:"task_id"`
}

func distillTaskID(taskID string) string { return "distill:" + taskID }

// EnqueueDistill 投递一个任务的经验沉淀，timeout 是单次处理的时限。按任务幂等：该任务的沉淀
// 还在队列里（排队、重试中或处理中）时不重复入队——处理时读的是任务的最新图面。
func (c *Client) EnqueueDistill(ctx context.Context, taskID string, timeout time.Duration) error {
	if taskID == "" {
		return errors.New("worker: 沉淀任务的 taskID 不能为空")
	}
	body, err := json.Marshal(distillPayload{TaskID: taskID})
	if err != nil {
		return fmt.Errorf("worker: 序列化沉淀载荷失败: %w", err)
	}
	_, err = c.c.EnqueueContext(ctx, asynq.NewTask(TaskTypeDistill, body,
		asynq.Queue(QueueDistill),
		asynq.TaskID(distillTaskID(taskID)),
		asynq.MaxRetry(distillMaxRetry),
		asynq.Timeout(timeout),
	))
	if err != nil && !errors.Is(err, asynq.ErrTaskIDConflict) {
		return fmt.Errorf("worker: 投递任务 %s 的经验沉淀失败: %w", taskID, err)
	}
	return nil
}

// DistillHandler 处理一个任务的经验沉淀；返回错误即按重试策略重试。
type DistillHandler func(ctx context.Context, taskID string) error
