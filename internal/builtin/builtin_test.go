package builtin_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/V3teran/liusha/internal/builtin"
	"github.com/V3teran/liusha/internal/constants"
	"github.com/V3teran/liusha/internal/registry"
	"github.com/V3teran/liusha/internal/tools/manifest"
)

// 出厂内容随编译固定：任何引用错误都应在构建期由本测试拦下，而不是等运行期发现。

func TestAgentsAreTheFourRoles(t *testing.T) {
	var got []string
	for _, a := range builtin.Agents() {
		got = append(got, a.Code)
	}
	want := []string{constants.AgentCodeEvaluator, constants.AgentCodeExecutor, constants.AgentCodeMonitor, constants.AgentCodePlanner}
	if !slices.Equal(got, want) {
		t.Fatalf("出厂 agent = %v，应为四个角色 %v", got, want)
	}
}

func TestAgentDefaultsAreValid(t *testing.T) {
	m, err := manifest.Load("../../deployments/tool-images/sandbox/tools.yaml")
	if err != nil {
		t.Fatalf("加载 CLI 工具清单: %v", err)
	}
	cliTools := map[string]bool{}
	for _, tool := range m.Tools {
		cliTools[tool.Name] = true
	}
	fnTools := map[string]bool{}
	for _, meta := range registry.FunctionToolCatalog {
		fnTools[meta.Name] = true
	}
	skills := map[string]bool{}
	for _, s := range builtin.Skills() {
		skills[s.Code] = true
	}

	for _, a := range builtin.Agents() {
		if a.Name == "" || a.Description == "" || strings.TrimSpace(a.Charter) == "" {
			t.Errorf("%s: 名称、调度摘要、章程不可为空", a.Code)
		}
		if !slices.Contains([]string{"simple", "medium", "complex"}, a.Complexity) {
			t.Errorf("%s: 非法档位 %q", a.Code, a.Complexity)
		}
		if a.MaxIterations <= 0 {
			t.Errorf("%s: max_iterations 必须 > 0", a.Code)
		}
		checkRefs(t, a.Code, "function_tools", a.FunctionTools, fnTools)
		checkRefs(t, a.Code, "cli_tools", a.CliTools, cliTools)
		checkRefs(t, a.Code, "skills", a.Skills, skills)
	}
}

func checkRefs(t *testing.T, agent, field string, refs []string, known map[string]bool) {
	t.Helper()
	for _, r := range refs {
		if r != "*" && !known[r] {
			t.Errorf("%s.%s 引用了不存在的 %q", agent, field, r)
		}
	}
}

// 输出契约随解析代码放在各 agent 包内，章程里出现契约会让同一约定有两份，
// 自定义章程后两份不一致——章程必须不含契约。
func TestChartersExcludeContracts(t *testing.T) {
	for _, a := range builtin.Agents() {
		for _, line := range strings.Split(a.Charter, "\n") {
			if strings.HasPrefix(line, "#") && (strings.Contains(line, "输出契约") || strings.Contains(line, "输出格式")) {
				t.Errorf("%s: 章程不应含契约章节 %q（契约属于解析它的代码包）", a.Code, line)
			}
		}
	}
}

func TestSkillsAreValid(t *testing.T) {
	seen := map[string]bool{}
	for _, s := range builtin.Skills() {
		if seen[s.Code] {
			t.Errorf("技能 code %q 重复", s.Code)
		}
		seen[s.Code] = true
		if !slices.Contains([]string{"vuln", "tool"}, s.Category) {
			t.Errorf("%s: 非法分类 %q", s.Code, s.Category)
		}
		if s.Name == "" || s.Description == "" || strings.TrimSpace(s.Body) == "" {
			t.Errorf("%s: 名称、描述、正文不可为空", s.Code)
		}
	}
	if len(seen) == 0 {
		t.Fatal("内置技能为空：embed 路径失效")
	}
}

func TestAccessorsReturnCopies(t *testing.T) {
	a, ok := builtin.AgentByCode(constants.AgentCodeExecutor)
	if !ok {
		t.Fatal("executor 缺失")
	}
	a.FunctionTools[0] = "tampered"
	again, _ := builtin.AgentByCode(constants.AgentCodeExecutor)
	if again.FunctionTools[0] == "tampered" {
		t.Fatal("调用方修改返回值不得影响出厂内容")
	}
}

func TestSplitFrontmatter(t *testing.T) {
	front, body, err := builtin.SplitFrontmatter([]byte("---\ncode: x\n---\n\n# 正文\n"))
	if err != nil || string(front) != "\ncode: x" || string(body) != "# 正文\n" {
		t.Fatalf("front=%q body=%q err=%v", front, body, err)
	}
	if _, _, err := builtin.SplitFrontmatter([]byte("# 无 frontmatter")); err == nil {
		t.Fatal("缺 frontmatter 应报错")
	}
	if _, _, err := builtin.SplitFrontmatter([]byte("---\ncode: x\n")); err == nil {
		t.Fatal("frontmatter 未闭合应报错")
	}
}
