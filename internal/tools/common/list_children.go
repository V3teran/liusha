package common

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/V3teran/liusha/internal/subtask"
	"github.com/V3teran/liusha/internal/toolruntime"
)

// ListChildren 列出本父任务派的所有子任务状态 — 与 SpawnChild 配对，只 active 父注册。
type ListChildren struct {
	Registry *subtask.Registry
}

// Name 返回工具名 "list_children"。
func (a ListChildren) Name() string { return "list_children" }

// Description 提供给 LLM 的简介。
func (a ListChildren) Description() string {
	return "列出本父任务派的所有子任务状态（running/done/failed + 步数 + 失败原因）。" +
		"\n\n【建议】spawn 后每 20-30 步调一次看进展；不要每步都调（浪费 step 预算）。" +
		"\n【子的成果】子的 finding 自动通过共享黑板冒给父，**用 read_findings 看**——本工具只看『是否在跑』。" +
		"\n【done 准备】调 done 前先调本工具确认无 running 子；有 running 则等几轮再 done。"
}

// ParametersJSON 无入参 schema。
func (a ListChildren) ParametersJSON() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{}}`)
}

// Execute 返回 Registry.Snapshot 序列化结果。
func (a ListChildren) Execute(_ context.Context, _ json.RawMessage) (toolfx.Result, error) {
	if a.Registry == nil {
		return toolfx.Result{}, errors.New("list_children: Registry 未注入")
	}
	snaps := a.Registry.Snapshot()
	runningCount := 0
	for _, s := range snaps {
		if s.Status == subtask.StatusRunning {
			runningCount++
		}
	}
	out, err := json.Marshal(map[string]any{
		"count":         len(snaps),
		"running_count": runningCount,
		"children":      snaps,
	})
	if err != nil {
		return toolfx.Result{}, fmt.Errorf("marshal children: %w", err)
	}
	summary := fmt.Sprintf("list_children total=%d running=%d", len(snaps), runningCount)
	return toolfx.Result{Output: out, Summary: summary}, nil
}
