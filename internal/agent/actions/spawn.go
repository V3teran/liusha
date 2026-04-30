package actions

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/V3teran/liusha/internal/tool"
)

// SpawnEngine 是 SpawnSubtask 依赖的最小接口：把"创建子任务+入队"封装为一次调用。
//
// *spawner.Spawner 自动满足。用 interface 而非具体类型是为了：
//  1. 单测可注入 fake，不必拉起 task.Store / asynq；
//  2. 避免本包对 internal/spawner 的硬依赖（防止循环 import）。
type SpawnEngine interface {
	Spawn(ctx context.Context, parentTaskID, skill string, input, budget json.RawMessage) (string, error)
}

// SpawnSubtask — sniffer ReAct 派发子任务的 action。
//
// 由 ActionRegistry 装配阶段为每个 task 实例化一次：ParentTaskID / EngagementID
// 在装配时注入（即"当前正在跑的 sniffer task"），LLM 通过 args.skill / input / budget
// 控制要派发什么。
type SpawnSubtask struct {
	Engine       SpawnEngine
	ParentTaskID string // 当前 sniffer task 的 id（注入）
	EngagementID string // 用于日志标识；实际 engagement 由 Engine 从父任务读
}

// Name 返回动作名 "spawn_subtask"，与 plan 中 LLM tool 名一致。
func (a *SpawnSubtask) Name() string { return "spawn_subtask" }

// Description 给 LLM 的简介。
func (a *SpawnSubtask) Description() string {
	return "派发一个子任务执行指定 skill。受 spawn depth ≤ 1 与 inflight 上限约束。"
}

// ParametersJSON：skill / input 必填，budget 可选。
func (a *SpawnSubtask) ParametersJSON() json.RawMessage {
	return json.RawMessage(`{
  "type":"object",
  "properties": {
    "skill":  {"type":"string","description":"子任务要加载的 skill，如 vuln/web/bac"},
    "input":  {"type":"object","description":"子任务输入（jsonb），由目标 skill 自行解释"},
    "budget": {"type":"object","description":"子任务预算（max_steps / max_tokens 等），可选"}
  },
  "required": ["skill","input"]
}`)
}

// Execute 解析 args → 调 Engine.Spawn → 返回 {child_task_id}。
//
// 失败语义：
//   - args 非法 JSON / skill 缺失 → tool.Result{} + error（不调用 Engine）
//   - Engine.Spawn 返错（depth、inflight 限额、DB / Redis 故障）→ 错误透传
func (a *SpawnSubtask) Execute(ctx context.Context, args json.RawMessage) (tool.Result, error) {
	var in struct {
		Skill  string          `json:"skill"`
		Input  json.RawMessage `json:"input"`
		Budget json.RawMessage `json:"budget"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return tool.Result{}, fmt.Errorf("解析 spawn_subtask 参数失败: %w", err)
	}
	if in.Skill == "" {
		return tool.Result{}, fmt.Errorf("skill 必填")
	}
	// input 缺省给空对象，避免下游 unmarshal nil。
	if len(in.Input) == 0 {
		in.Input = json.RawMessage(`{}`)
	}

	childID, err := a.Engine.Spawn(ctx, a.ParentTaskID, in.Skill, in.Input, in.Budget)
	if err != nil {
		return tool.Result{}, fmt.Errorf("spawn child task: %w", err)
	}

	out, _ := json.Marshal(map[string]string{"child_task_id": childID})
	return tool.Result{
		Output:  out,
		Summary: fmt.Sprintf("spawn skill=%s child=%s", in.Skill, childID),
	}, nil
}
