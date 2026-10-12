package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/V3teran/liusha/internal/framework/core"
)

func (r *DefaultReActRuntime) saveCheckpoint(
	ctx context.Context,
	config *ReActConfig,
	result *ReActResult,
	iteration int,
) (core.CheckpointID, error) {
	// 序列化当前状态
	state := map[string]interface{}{
		"message_history": result.MessageHistory,
		"trace":           result.Trace,
		"iteration":       iteration,
		"timestamp":       time.Now().UnixMilli(),
	}
	stateJSON, err := json.Marshal(state)
	if err != nil {
		return "", fmt.Errorf("serialize state failed: %w", err)
	}

	// 构造检查点
	checkpoint := core.Checkpoint{
		TaskID:        config.TaskID,
		StateSnapshot: stateJSON,
		Phase:         fmt.Sprintf("iteration_%d", iteration),
		ComponentStates: map[string]json.RawMessage{
			"react_runtime": stateJSON,
		},
		Labels: map[string]string{
			"iteration": fmt.Sprintf("%d", iteration),
			"status":    string(result.Status),
		},
		CreatedAt: time.Now(),
	}

	// 保存检查点
	cpID, err := config.Checkpointer.Save(ctx, checkpoint)
	if err != nil {
		return "", fmt.Errorf("save checkpoint failed: %w", err)
	}

	return cpID, nil
}

// forceFinalSummary 强制要求 LLM 输出最终总结（当达到最大迭代次数时）
