package worker_test

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/V3teran/liusha/internal/worker"
)

// TestNewAgentRunTask_RoundTrip 验证 payload 序列化/反序列化完整保留字段。
func TestNewAgentRunTask_RoundTrip(t *testing.T) {
	engID := uuid.New()
	windowID := uuid.New()
	taskID := uuid.New()
	nodeIDs := []uuid.UUID{uuid.New(), uuid.New()}

	in := worker.AgentRunPayload{
		EngagementID: engID,
		WindowID:     windowID,
		TaskID:       taskID,
		Role:         "web_recon",
		InputNodeIDs: nodeIDs,
	}

	task, err := worker.NewAgentRunTask(in)
	require.NoError(t, err)
	assert.Equal(t, worker.TypeAgentRun, task.Type())

	var out worker.AgentRunPayload
	require.NoError(t, json.Unmarshal(task.Payload(), &out))
	assert.Equal(t, engID, out.EngagementID)
	assert.Equal(t, windowID, out.WindowID)
	assert.Equal(t, taskID, out.TaskID)
	assert.Equal(t, "web_recon", out.Role)
	assert.Equal(t, nodeIDs, out.InputNodeIDs)
}

// TestNewAgentRunTask_EmptyInputNodes 验证 InputNodeIDs 为空不报错。
func TestNewAgentRunTask_EmptyInputNodes(t *testing.T) {
	in := worker.AgentRunPayload{
		EngagementID: uuid.New(),
		TaskID:       uuid.New(),
		Role:         "planner",
	}
	task, err := worker.NewAgentRunTask(in)
	require.NoError(t, err)
	assert.NotNil(t, task)
}
