package worker_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/V3teran/liusha/internal/worker"
)

// stubRunner 是 worker.Runner 的测试替身，记录调用与返回错误可控。
type stubRunner struct {
	called      int
	lastPayload worker.AgentRunPayload
	err         error
}

func (s *stubRunner) Run(_ context.Context, p worker.AgentRunPayload) error {
	s.called++
	s.lastPayload = p
	return s.err
}

// TestHandler_Success 验证 handler 解析 payload 并调用 Runner。
func TestHandler_Success(t *testing.T) {
	runner := &stubRunner{}
	h := worker.NewHandler(runner)

	payload := worker.AgentRunPayload{
		EngagementID: uuid.New(),
		WindowID:     uuid.New(),
		TaskID:       uuid.New(),
		Role:         "web_recon",
		InputNodeIDs: []uuid.UUID{uuid.New()},
	}
	raw, err := json.Marshal(payload)
	require.NoError(t, err)

	task := asynq.NewTask(worker.TypeAgentRun, raw)

	err = h.HandleAgentRun(context.Background(), task)
	require.NoError(t, err)
	assert.Equal(t, 1, runner.called)
	assert.Equal(t, payload.TaskID, runner.lastPayload.TaskID)
	assert.Equal(t, payload.Role, runner.lastPayload.Role)
}

// TestHandler_BadPayload 验证坏 payload 返回 asynq.SkipRetry（视为 schema 错误不重试）。
func TestHandler_BadPayload(t *testing.T) {
	runner := &stubRunner{}
	h := worker.NewHandler(runner)

	task := asynq.NewTask(worker.TypeAgentRun, []byte("not-json"))

	err := h.HandleAgentRun(context.Background(), task)
	require.Error(t, err)
	assert.ErrorIs(t, err, asynq.SkipRetry)
	assert.Equal(t, 0, runner.called)
}

// TestHandler_RunnerError 验证 Runner 错误向上传播以触发 Asynq 重试。
func TestHandler_RunnerError(t *testing.T) {
	boom := errors.New("llm 瞬时失败")
	runner := &stubRunner{err: boom}
	h := worker.NewHandler(runner)

	payload := worker.AgentRunPayload{
		EngagementID: uuid.New(),
		WindowID:     uuid.New(),
		TaskID:       uuid.New(),
		Role:         "planner",
	}
	raw, err := json.Marshal(payload)
	require.NoError(t, err)

	task := asynq.NewTask(worker.TypeAgentRun, raw)

	err = h.HandleAgentRun(context.Background(), task)
	require.Error(t, err)
	assert.ErrorIs(t, err, boom)
	assert.Equal(t, 1, runner.called)
}
