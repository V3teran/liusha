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

func (a ListChildren) Description() string {
	return "列出本父任务派的所有子任务状态（running/done/failed + 步数 + 失败原因）。" +
		"\n\n【何时调】实在好奇子进度时调一次；**不要 polling**。" +
		"\n【子的成果】子的 finding 自动冒给父，**用 read_findings 看**——本工具只看『是否在跑』。" +
		"\n【done 准备】**别先 list_children 再 done**——直接 done，PreDoneCheck 会拦 + 错误消息含 running 子摘要。" +
		"\n【截断】仅返全部 running + 最近 10 个终态（防累计 spawn 上百轮 token 爆）。"
}

// ParametersJSON 无入参 schema。
func (a ListChildren) ParametersJSON() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{}}`)
}

// recentTerminalLimit 终态子保留条数。父历史可累计 spawn 100+ 次，但 LLM
// 关心的是当前 running + 最近完成；早期 done 子的发现已通过共享黑板 finding 沉淀。
const recentTerminalLimit = 10

// Execute 返回 Registry.Snapshot 截断后的结果（全 running + 最近 N 终态）。
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
	visible := truncateSnapshots(snaps, recentTerminalLimit)
	out, err := json.Marshal(map[string]any{
		"total_count":   len(snaps),
		"running_count": runningCount,
		"shown_count":   len(visible),
		"children":      visible,
	})
	if err != nil {
		return toolfx.Result{}, fmt.Errorf("marshal children: %w", err)
	}
	summary := fmt.Sprintf("list_children total=%d running=%d shown=%d", len(snaps), runningCount, len(visible))
	return toolfx.Result{Output: out, Summary: summary}, nil
}

// truncateSnapshots 保留全 running + 最近 maxTerminal 个终态。
// snaps 按 SpawnedAt 升序（Registry.Snapshot 契约），终态尾部=最近完成。
// 输出顺序：旧终态 → 新终态 → running（保留时间感）。
func truncateSnapshots(snaps []subtask.ChildSnapshot, maxTerminal int) []subtask.ChildSnapshot {
	var running, terminal []subtask.ChildSnapshot
	for _, s := range snaps {
		if s.Status == subtask.StatusRunning {
			running = append(running, s)
		} else {
			terminal = append(terminal, s)
		}
	}
	if len(terminal) > maxTerminal {
		terminal = terminal[len(terminal)-maxTerminal:]
	}
	return append(terminal, running...)
}
