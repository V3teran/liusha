package builtin_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/V3teran/liusha/internal/builtin"
	"github.com/V3teran/liusha/internal/evaluator"
	evaluatorcontract "github.com/V3teran/liusha/internal/evaluator/contract"
	executorcontract "github.com/V3teran/liusha/internal/executor/contract"
	plannercontract "github.com/V3teran/liusha/internal/planner/contract"
)

// 核心层（四个 agent、探索图、agent 框架、知识链路）领域中立：领域内容只出现在场景
// （internal/builtin/scenarios，按任务选择、拼在系统提示词末尾）以及产品表层与领域实现里。
// 以下守门防止领域词回流到章程、输出契约与核心包代码里会进入 LLM 的文本。

// domainTerms 是领域（当前部署：渗透测试）专有的词。只收不会出现在中立表述里的词。
var domainTerms = []string{
	"漏洞", "渗透", "攻击", "越权", "提权", "反弹", "靶机", "利用链", "打法",
	"payload", "exploit", "xss", "sqli", "sql 注入", "sql注入", "sql syntax", "union select",
	"ssrf", "cve-", "sqlmap", "nmap", "waf",
}

func findDomainTerm(text string) string {
	lower := strings.ToLower(text)
	for _, term := range domainTerms {
		if strings.Contains(lower, term) {
			return term
		}
	}
	return ""
}

// 章程整份领域中立：领域内容放进场景。
func TestChartersAreDomainNeutral(t *testing.T) {
	for _, a := range builtin.Agents() {
		for i, line := range strings.Split(a.Charter, "\n") {
			if term := findDomainTerm(line); term != "" {
				t.Errorf("%s 章程第 %d 行含领域词 %q（领域内容放进场景）: %s", a.Code, i+1, term, strings.TrimSpace(line))
			}
		}
	}
}

// 输出契约是程序解析依赖的格式，不可自定义，因此必须领域中立。
func TestContractsAreDomainNeutral(t *testing.T) {
	for name, text := range map[string]string{
		"planner":          plannercontract.Text,
		"planner/analysis": plannercontract.AnalysisText,
		"executor":         executorcontract.Text,
		"evaluator":        evaluatorcontract.Text,
	} {
		for i, line := range strings.Split(text, "\n") {
			if term := findDomainTerm(line); term != "" {
				t.Errorf("%s 契约第 %d 行含领域词 %q: %s", name, i+1, term, strings.TrimSpace(line))
			}
		}
	}
}

// coreSources 是核心层里会产出 LLM 可见文本的源码（相对 internal/）。产品表层（闲聊、问答、
// 意图分类、标题生成、漏洞台账）与 Web 专用工具（http_request、browser_drive、凭证、流量）
// 保持渗透测试定位，不在此列。
var coreSources = []string{
	"planner", "executor", "evaluator", "monitor", "cognition", "framework",
	"explorationgraph", "reasoning", "repro", "httprepro", "distill", "skill",
	"websearch", "tools/websearch.go", "tools/corpus.go", "tools/skill.go", "tools/command.go", "scenario",
}

// isLogCall 判断字符串字面量是否位于日志调用里（日志不进 LLM）。
func isLogCall(stack []ast.Node) bool {
	for i := len(stack) - 1; i >= 0; i-- {
		call, ok := stack[i].(*ast.CallExpr)
		if !ok {
			continue
		}
		if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
			switch sel.Sel.Name {
			case "Msg", "Msgf", "Str", "Debug", "Info", "Warn", "Error":
				return true
			}
		}
	}
	return false
}

// 核心包代码里的字符串字面量（提示词、工具描述、回给 LLM 的报错）不含领域词。
func TestCoreSourcesAreDomainNeutral(t *testing.T) {
	for _, rel := range coreSources {
		root := filepath.Join("..", rel)
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return err
			}
			fset := token.NewFileSet()
			file, err := parser.ParseFile(fset, path, nil, 0)
			if err != nil {
				return err
			}
			var stack []ast.Node
			ast.Inspect(file, func(n ast.Node) bool {
				if n == nil {
					stack = stack[:len(stack)-1]
					return true
				}
				stack = append(stack, n)
				lit, ok := n.(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING || isLogCall(stack) {
					return true
				}
				s, err := strconv.Unquote(lit.Value)
				if err != nil {
					return true
				}
				if term := findDomainTerm(s); term != "" {
					t.Errorf("%s:%d 含领域词 %q: %.120s", path, fset.Position(lit.Pos()).Line, term, s)
				}
				return true
			})
			return nil
		})
		if err != nil {
			t.Fatalf("扫描 %s: %v", root, err)
		}
	}
}

// 规划者对已证伪假设的处置写在章程里（代码只陈述反驳原因的含义）：每个反驳原因都要有处置，
// 新增原因时章程与 evaluator 的常量同步。
func TestPlannerCharterCoversRefuteReasons(t *testing.T) {
	var charter string
	for _, a := range builtin.Agents() {
		if a.Code == "planner" {
			charter = a.Charter
		}
	}
	for _, reason := range []string{
		evaluator.RefuteNotReproducible, evaluator.RefuteInvalidRecipe, evaluator.RefuteAssertNotDiscriminative,
		evaluator.RefuteClaimInvalid, evaluator.RefuteJudgeUnavailable,
	} {
		if !strings.Contains(charter, "`"+reason+"`") {
			t.Errorf("规划者章程缺少反驳原因 %s 的处置", reason)
		}
	}
}
