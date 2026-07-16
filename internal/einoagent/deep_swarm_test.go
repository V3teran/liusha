package einoagent_test

import (
	"context"
	"sort"
	"testing"

	"github.com/V3teran/liusha/internal/einoagent"
	"github.com/V3teran/liusha/internal/traffic"
)

// ---- 工具注册表 ----

func toolDefsFromCtx(t *testing.T, role einoagent.RoleDef) []string {
	t.Helper()
	f := allFake{}
	tools, err := einoagent.BuildRoleTools(role, einoagent.ToolBuildCtx{
		Deps:   einoagent.TrafficAnalysisToolDeps{Findings: f, Corpus: f, Credentials: f, AgentFlows: traffic.NewAgentStore(nil)},
		Params: einoagent.TrafficAnalysisToolParams{TaskID: "task-1", Mode: "active", HunterID: "h", Host: "host"},
	})
	if err != nil {
		t.Fatalf("BuildRoleTools: %v", err)
	}
	var names []string
	for _, bt := range tools {
		info, _ := bt.Info(context.Background())
		names = append(names, info.Name)
	}
	sort.Strings(names)
	return names
}

func TestBuildRoleTools_ByName(t *testing.T) {
	role := einoagent.RoleDef{
		ID:    "exploitation",
		Tools: []string{"read_findings", "write_finding", "replay_traffic", "list_traffic", "view_traffic", "done"},
	}
	names := toolDefsFromCtx(t, role)
	want := []string{"done", "list_traffic", "read_findings", "replay_traffic", "view_traffic", "write_finding"}
	if len(names) != len(want) {
		t.Fatalf("工具数错: 得 %v want %v", names, want)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Errorf("工具 [%d]: 得 %q want %q", i, names[i], want[i])
		}
	}
}

func TestBuildRoleTools_UnknownTool(t *testing.T) {
	role := einoagent.RoleDef{ID: "x", Tools: []string{"read_findings", "no_such_tool"}}
	f := allFake{}
	_, err := einoagent.BuildRoleTools(role, einoagent.ToolBuildCtx{
		Deps:   einoagent.TrafficAnalysisToolDeps{Findings: f, Corpus: f, Credentials: f},
		Params: einoagent.TrafficAnalysisToolParams{TaskID: "task-1", Host: "h"},
	})
	if err == nil {
		t.Fatal("未知工具应报错")
	}
}

func TestBuildRoleTools_RunCommandNeedsSandbox(t *testing.T) {
	role := einoagent.RoleDef{ID: "x", Tools: []string{"run_command"}}
	f := allFake{}
	_, err := einoagent.BuildRoleTools(role, einoagent.ToolBuildCtx{
		Deps:   einoagent.TrafficAnalysisToolDeps{Findings: f, Corpus: f, Credentials: f}, // Sandbox nil
		Params: einoagent.TrafficAnalysisToolParams{TaskID: "task-1", Host: "h"},
	})
	if err == nil {
		t.Fatal("run_command 无 Sandbox 应报错")
	}
}

func TestKnownToolNames_CoversCore(t *testing.T) {
	names := map[string]bool{}
	for _, n := range einoagent.KnownToolNames() {
		names[n] = true
	}
	for _, must := range []string{"read_findings", "write_finding", "run_command", "done", "list_traffic"} {
		if !names[must] {
			t.Errorf("注册表缺核心工具 %s", must)
		}
	}
}

// ---- deep 装配 ----

func TestBuildDeepSwarm_AssemblesOrchestratorAndSubAgents(t *testing.T) {
	f := allFake{}
	orchestratorRole := einoagent.RoleDef{
		ID: "orchestrator", Kind: einoagent.RoleOrchestrator,
		Description: "拆活派 exploitation", SystemPrompt: "你是编排者",
		Tools: []string{"read_findings", "list_traffic"}, MaxIterations: 300,
	}
	exploitationRole := einoagent.RoleDef{
		ID: "exploitation", Kind: einoagent.RoleSubAgent,
		Description: "深挖单点", SystemPrompt: "你是exploitation",
		Tools: []string{"read_findings", "write_finding", "done"}, MaxIterations: 120,
	}
	agent, err := einoagent.BuildDeepSwarm(context.Background(), einoagent.DeepSwarmConfig{
		Model:        &fakeModel{},
		Orchestrator: orchestratorRole,
		SubAgents:    []einoagent.RoleDef{exploitationRole},
		ToolDeps:     einoagent.TrafficAnalysisToolDeps{Findings: f, Corpus: f, Credentials: f, AgentFlows: traffic.NewAgentStore(nil)},
		Params:       einoagent.TrafficAnalysisToolParams{TaskID: "task-1", Mode: "active", HunterID: "cmd", Host: "host"},
	})
	if err != nil {
		t.Fatalf("BuildDeepSwarm: %v", err)
	}
	if agent == nil {
		t.Fatal("BuildDeepSwarm 返回 nil agent")
	}
	if agent.Name(context.Background()) != "orchestrator" {
		t.Errorf("orchestrator name 错: %q", agent.Name(context.Background()))
	}
}

func TestBuildDeepSwarm_NilModel(t *testing.T) {
	_, err := einoagent.BuildDeepSwarm(context.Background(), einoagent.DeepSwarmConfig{})
	if err == nil {
		t.Fatal("nil model 应报错")
	}
}
