package tools

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/V3teran/liusha/internal/corpus"
	"github.com/V3teran/liusha/internal/credential"
	"github.com/V3teran/liusha/internal/explorationgraph"
	"github.com/V3teran/liusha/internal/finding"
	"github.com/V3teran/liusha/internal/insight"
	"github.com/V3teran/liusha/internal/registry"
	"github.com/V3teran/liusha/internal/sandbox"
	"github.com/V3teran/liusha/internal/skill"
	"github.com/V3teran/liusha/internal/task"
	"github.com/V3teran/liusha/internal/traffic"
)

// noOpCredProvider / noOpSandbox：仅用于构造期注入（BuildTools 只存依赖不调用，
// Execute 才用）——让全部分支工具都进入注册集，供 catalog 一致性比对。
type noOpCredProvider struct{}

func (noOpCredProvider) BatchSave(_ context.Context, _ map[string][]credential.Identity, _ int) error {
	return nil
}
func (noOpCredProvider) GetIdentitiesByHost(_ context.Context, _ string) ([]credential.Identity, error) {
	return nil, nil
}
func (noOpCredProvider) List(_ context.Context, _ string) (map[string][]credential.Identity, error) {
	return nil, nil
}
func (noOpCredProvider) Delete(_ context.Context, _ string) error { return nil }

type noOpSandbox struct{}

func (noOpSandbox) Exec(_ context.Context, _ sandbox.ExecRequest) (sandbox.ExecResult, error) {
	return sandbox.ExecResult{}, nil
}
func (noOpSandbox) Close() error { return nil }

// TestFunctionToolCatalogSync 防漂移：registry.FunctionToolCatalog（前端工具目录
// 事实源，经 config/tool.Reconcile 入库）与 BuildTools 实际构造集严格双向一致。
// evaluator 包本地注册的 replay_for_verification 以白名单追加比对。
func TestFunctionToolCatalogSync(t *testing.T) {
	// 一个最小 skill 目录，让 read_skill 进入注册集（空视图不注册）。
	root := t.TempDir()
	skillDir := filepath.Join(root, "probe")
	if err := os.MkdirAll(skillDir, 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"),
		[]byte("---\nname: probe\ndescription: p\n---\n正文"), 0o600); err != nil {
		t.Fatalf("write SKILL.md: %v", err)
	}
	skillSet := skill.NewSet(map[string]string{"tooling": root})
	if _, err := skillSet.Index(); err != nil {
		t.Fatalf("skill index: %v", err)
	}

	deps := Deps{
		Tasks:      &task.Store{},
		Findings:   &finding.Store{},
		Corpus:     &corpus.Store{},
		Insights:   &insight.Store{},
		ProxyStore: &traffic.ProxyStore{},
		AgentStore: &traffic.AgentStore{},
		Creds:      noOpCredProvider{},
		Sandbox:    noOpSandbox{},
		Graph:      &explorationgraph.Store{},
		Skills:     skillSet,
	}

	registered := make(map[string]bool)
	for _, tl := range BuildTools(deps, nil) {
		registered[tl.Name()] = true
	}
	registered["replay_for_verification"] = true // evaluator judge 本地注册
	// planner/monitor 包内本地注册的图工具（不经 tools.BuildTools）
	registered["observe_state"] = true
	registered["evaluate_progress"] = true
	registered["get_global_state"] = true
	registered["publish_decision"] = true

	inCatalog := make(map[string]bool)
	for _, meta := range registry.FunctionToolCatalog {
		if inCatalog[meta.Name] {
			t.Errorf("catalog 重复项: %s", meta.Name)
		}
		inCatalog[meta.Name] = true
		if !registered[meta.Name] {
			t.Errorf("catalog 声明了未注册的工具（漂移）: %s", meta.Name)
		}
	}
	for name := range registered {
		if !inCatalog[name] {
			t.Errorf("已注册工具不在 catalog（漏登记）: %s", name)
		}
	}
}
