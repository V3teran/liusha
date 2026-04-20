package worker_test

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/V3teran/liusha/internal/worker"
)

// newTestClient 启动 miniredis 并返回 worker.Client + Asynq Inspector + 清理函数。
func newTestClient(t *testing.T) (*worker.Client, *asynq.Inspector, func()) {
	t.Helper()

	mr := miniredis.RunT(t)
	opt := asynq.RedisClientOpt{Addr: mr.Addr()}

	client := worker.NewClient(opt)
	inspector := asynq.NewInspector(opt)

	cleanup := func() {
		_ = client.Close()
		_ = inspector.Close()
		mr.Close()
	}
	return client, inspector, cleanup
}

// TestClient_Enqueue_Idempotent 验证相同 TaskID 在 Unique TTL 内只会入队一次。
func TestClient_Enqueue_Idempotent(t *testing.T) {
	client, inspector, cleanup := newTestClient(t)
	defer cleanup()

	taskID := uuid.New()
	payload := worker.AgentRunPayload{
		EngagementID: uuid.New(),
		WindowID:     uuid.New(),
		TaskID:       taskID,
		Role:         "web_recon",
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	info1, err := client.EnqueueAgentRun(ctx, payload)
	require.NoError(t, err)
	assert.NotNil(t, info1)
	assert.Equal(t, taskID.String(), info1.ID)

	// 重复入队同一 TaskID，应返回 asynq.ErrDuplicateTask 或 ErrTaskIDConflict。
	_, err = client.EnqueueAgentRun(ctx, payload)
	require.Error(t, err)
	assert.True(t, worker.IsDuplicate(err), "期望重复入队错误, 实际=%v", err)

	// 队列中应当只存在 1 条 agent:run 任务。
	queues, err := inspector.Queues()
	require.NoError(t, err)
	require.NotEmpty(t, queues)

	pending, err := inspector.ListPendingTasks(queues[0])
	require.NoError(t, err)
	assert.Len(t, pending, 1)
}

// TestClient_Enqueue_DifferentTaskIDs 验证不同 TaskID 可以各自入队。
func TestClient_Enqueue_DifferentTaskIDs(t *testing.T) {
	client, inspector, cleanup := newTestClient(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	for i := 0; i < 3; i++ {
		payload := worker.AgentRunPayload{
			EngagementID: uuid.New(),
			WindowID:     uuid.New(),
			TaskID:       uuid.New(),
			Role:         "web_recon",
		}
		_, err := client.EnqueueAgentRun(ctx, payload)
		require.NoError(t, err, "第 %d 次入队失败", i)
	}

	queues, err := inspector.Queues()
	require.NoError(t, err)
	pending, err := inspector.ListPendingTasks(queues[0])
	require.NoError(t, err)
	assert.Len(t, pending, 3)
}
