package actions

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// fakeSpawner 是 SpawnEngine 接口的内存实现，记录 Spawn 入参，可注入错误。
type fakeSpawner struct {
	calls    []spawnCall
	returnID string
	err      error
}

type spawnCall struct {
	parentTaskID string
	skill        string
	input        json.RawMessage
	budget       json.RawMessage
}

func (f *fakeSpawner) Spawn(
	_ context.Context,
	parentTaskID string,
	skill string,
	input, budget json.RawMessage,
) (string, error) {
	f.calls = append(f.calls, spawnCall{
		parentTaskID: parentTaskID,
		skill:        skill,
		input:        append(json.RawMessage(nil), input...),
		budget:       append(json.RawMessage(nil), budget...),
	})
	if f.err != nil {
		return "", f.err
	}
	if f.returnID == "" {
		return "child-id-stub", nil
	}
	return f.returnID, nil
}

// TestSpawnSubtask_Execute：参数解析 + Spawn 调用 + 输出回填 child_task_id。
func TestSpawnSubtask_Execute(t *testing.T) {
	eng := &fakeSpawner{returnID: "child-42"}
	a := &SpawnSubtask{
		Engine:       eng,
		ParentTaskID: "parent-1",
		EngagementID: "eng-1",
	}

	args := json.RawMessage(`{
	  "skill": "vuln/web/bac",
	  "input": {"flow_id": 1, "host": "vulnapp"},
	  "budget": {"max_steps": 20}
	}`)
	res, err := a.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("Execute err=%v", err)
	}

	// 验证 Spawn 被调一次，参数透传正确。
	if len(eng.calls) != 1 {
		t.Fatalf("Spawn 应调用 1 次，got %d", len(eng.calls))
	}
	call := eng.calls[0]
	if call.parentTaskID != "parent-1" {
		t.Fatalf("parentTaskID = %q, want parent-1", call.parentTaskID)
	}
	if call.skill != "vuln/web/bac" {
		t.Fatalf("skill = %q, want vuln/web/bac", call.skill)
	}

	// input/budget 是 json.RawMessage：解析后字段对得上即可。
	var input map[string]any
	if err := json.Unmarshal(call.input, &input); err != nil {
		t.Fatalf("input 解析: %v", err)
	}
	if input["host"] != "vulnapp" {
		t.Fatalf("input.host = %v, want vulnapp", input["host"])
	}
	var budget map[string]any
	if err := json.Unmarshal(call.budget, &budget); err != nil {
		t.Fatalf("budget 解析: %v", err)
	}
	if budget["max_steps"] != float64(20) {
		t.Fatalf("budget.max_steps = %v, want 20", budget["max_steps"])
	}

	// 验证 Result.Output 含 child_task_id。
	var out struct {
		ChildTaskID string `json:"child_task_id"`
	}
	if err := json.Unmarshal(res.Output, &out); err != nil {
		t.Fatalf("Output 解析: %v", err)
	}
	if out.ChildTaskID != "child-42" {
		t.Fatalf("child_task_id = %q, want child-42", out.ChildTaskID)
	}
}

// budget 缺省允许（参数 schema 中 budget 是 optional）。
func TestSpawnSubtask_BudgetOptional(t *testing.T) {
	eng := &fakeSpawner{}
	a := &SpawnSubtask{Engine: eng, ParentTaskID: "p", EngagementID: "e"}

	_, err := a.Execute(context.Background(), json.RawMessage(`{"skill":"x","input":{}}`))
	if err != nil {
		t.Fatalf("budget 缺省不应报错: %v", err)
	}
	if len(eng.calls) != 1 {
		t.Fatalf("Spawn 应调用 1 次")
	}
}

// 缺 skill → 拒绝（无意义的 spawn）。
func TestSpawnSubtask_RejectsEmptySkill(t *testing.T) {
	eng := &fakeSpawner{}
	a := &SpawnSubtask{Engine: eng, ParentTaskID: "p", EngagementID: "e"}

	_, err := a.Execute(context.Background(), json.RawMessage(`{"skill":"","input":{}}`))
	if err == nil {
		t.Fatal("空 skill 应报错")
	}
	if len(eng.calls) != 0 {
		t.Fatalf("校验失败时不应调用 Spawner.Spawn")
	}
}

// 非法 JSON args → 解析错误。
func TestSpawnSubtask_RejectsMalformedArgs(t *testing.T) {
	eng := &fakeSpawner{}
	a := &SpawnSubtask{Engine: eng, ParentTaskID: "p", EngagementID: "e"}

	_, err := a.Execute(context.Background(), json.RawMessage(`{not-json`))
	if err == nil {
		t.Fatal("非法 JSON 应报错")
	}
}

// Spawner.Spawn 返错 → 透传。
func TestSpawnSubtask_PropagatesSpawnerErr(t *testing.T) {
	eng := &fakeSpawner{err: errors.New("depth >= 1")}
	a := &SpawnSubtask{Engine: eng, ParentTaskID: "p", EngagementID: "e"}

	_, err := a.Execute(context.Background(), json.RawMessage(`{"skill":"x","input":{}}`))
	if err == nil {
		t.Fatal("Spawner 错误应透传")
	}
	if !strings.Contains(err.Error(), "depth") {
		t.Fatalf("应携带原始错误信息，got %q", err.Error())
	}
}

// Name / ParametersJSON 静态契约。
func TestSpawnSubtask_StaticContracts(t *testing.T) {
	a := &SpawnSubtask{}
	if a.Name() != "spawn_subtask" {
		t.Fatalf("Name = %q, want spawn_subtask", a.Name())
	}
	// ParametersJSON 必须是合法 JSON 且声明了 skill / input 必填。
	var schema struct {
		Type       string         `json:"type"`
		Required   []string       `json:"required"`
		Properties map[string]any `json:"properties"`
	}
	if err := json.Unmarshal(a.ParametersJSON(), &schema); err != nil {
		t.Fatalf("ParametersJSON 不是合法 JSON: %v", err)
	}
	if schema.Type != "object" {
		t.Fatalf("schema.type = %q, want object", schema.Type)
	}
	hasSkill, hasInput := false, false
	for _, r := range schema.Required {
		if r == "skill" {
			hasSkill = true
		}
		if r == "input" {
			hasInput = true
		}
	}
	if !hasSkill || !hasInput {
		t.Fatalf("required 应包含 skill 和 input，got %v", schema.Required)
	}
}
