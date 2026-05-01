package skill

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 工具：在 t.TempDir 下落 SKILL.md / cognitive_map.md，返回 root 路径。
func writeSkill(t *testing.T, name, body string) string {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(body), 0o644); err != nil {
		t.Fatalf("write SKILL.md: %v", err)
	}
	return root
}

// 写一个含 N 个 ^##\s+\d+\. 标题的 cognitive_map 到 root/relPath
func writeCognitiveMap(t *testing.T, root, relPath string, slots int) {
	t.Helper()
	full := filepath.Join(root, relPath)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	var b strings.Builder
	b.WriteString("# Cognitive Map\n\n")
	for i := 1; i <= slots; i++ {
		b.WriteString("## ")
		b.WriteString(itoa(i))
		b.WriteString(". 槽位 ")
		b.WriteString(itoa(i))
		b.WriteString("\n\n占位说明\n\n")
	}
	if err := os.WriteFile(full, []byte(b.String()), 0o644); err != nil {
		t.Fatalf("write cognitive_map: %v", err)
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

const validBody = `---
name: vuln/web/bac
description: BAC（未授权 / 垂直越权 / 水平越权）
applies_to:
  - role: sniffer
budget:
  max_steps: 10
  max_tokens: 15000
required_actions:
  - fetch_credentials
  - replay_multi_identity
  - heuristic_check
  - compute_similarity
  - write_finding
  - write_graph
  - done
---
# 你是 BAC 检测器
正文：步骤指引、判定逻辑。`

// 默认所有 done_validator key 都已注册（用于不关心 done_validator 校验的测试）
func anyDoneValidator(string) bool { return true }

// TestLoader_Load_Basic：读一个最小有效 SKILL.md，断言核心字段。
func TestLoader_Load_Basic(t *testing.T) {
	body := `---
name: vuln/web/bac
description: BAC
applies_to:
  - role: sniffer
budget:
  max_steps: 10
  max_tokens: 15000
required_actions: [fetch_credentials, replay_multi_identity]
---
正文`
	root := writeSkill(t, "vuln/web/bac", body)
	l := NewLoader(root)
	c, err := l.Load("vuln/web/bac", anyDoneValidator, nil)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.Name != "vuln/web/bac" {
		t.Errorf("name=%q", c.Name)
	}
	if c.Description != "BAC" {
		t.Errorf("desc=%q", c.Description)
	}
	if len(c.AppliesTo) != 1 || c.AppliesTo[0].Role != "sniffer" {
		t.Errorf("applies_to=%+v", c.AppliesTo)
	}
	if c.Budget.MaxSteps != 10 || c.Budget.MaxTokens != 15000 {
		t.Errorf("budget=%+v", c.Budget)
	}
	if len(c.RequiredActions) != 2 {
		t.Errorf("required_actions=%v", c.RequiredActions)
	}
	if !strings.Contains(c.Body, "正文") {
		t.Errorf("body=%q", c.Body)
	}
}

// TestLoader_Load_FrontmatterParseError：YAML 格式错误。
func TestLoader_Load_FrontmatterParseError(t *testing.T) {
	body := `---
name: [unclosed
---
正文`
	root := writeSkill(t, "broken", body)
	l := NewLoader(root)
	if _, err := l.Load("broken", anyDoneValidator, nil); err == nil {
		t.Fatal("expected yaml parse error, got nil")
	}
}

// TestLoader_Load_MissingFrontmatter：缺少 --- 分隔符。
func TestLoader_Load_MissingFrontmatter(t *testing.T) {
	body := `name: x
没有分隔符的纯正文`
	root := writeSkill(t, "noheader", body)
	l := NewLoader(root)
	_, err := l.Load("noheader", anyDoneValidator, nil)
	if err == nil {
		t.Fatal("expected missing-frontmatter error")
	}
	if !strings.Contains(err.Error(), "frontmatter") {
		t.Errorf("err should mention frontmatter, got: %v", err)
	}
}

// TestLoader_Load_RequiredActionsField：required_actions 数组解析正确。
func TestLoader_Load_RequiredActionsField(t *testing.T) {
	root := writeSkill(t, "vuln/web/bac", validBody)
	l := NewLoader(root)
	c, err := l.Load("vuln/web/bac", anyDoneValidator, nil)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := []string{
		"fetch_credentials", "replay_multi_identity", "heuristic_check",
		"compute_similarity", "write_finding", "write_graph", "done",
	}
	if len(c.RequiredActions) != len(want) {
		t.Fatalf("required_actions len=%d, want=%d", len(c.RequiredActions), len(want))
	}
	for i, a := range want {
		if c.RequiredActions[i] != a {
			t.Errorf("required_actions[%d]=%q, want=%q", i, c.RequiredActions[i], a)
		}
	}
}

// 黑客松扩展：cognitive_map 含 6 槽位 → 校验通过。
//
// 注意：loader 现在按 cwd 相对路径解析 cognitive_map（仓库实际布局是
// docs/skills/bac/cognitive_map.md 相对仓库根），因此测试 t.Chdir 到 tempdir
// 让 cognitive_map: docs/skills/bac/cognitive_map.md 能落到 tempdir 内。
func TestLoader_Load_CognitiveMapPathExists(t *testing.T) {
	body := `---
name: vuln/web/bac
description: BAC
applies_to:
  - role: sniffer
budget:
  max_steps: 10
  max_tokens: 15000
required_actions: [done]
cognitive_map: docs/skills/bac/cognitive_map.md
---
正文`
	root := writeSkill(t, "vuln/web/bac", body)
	writeCognitiveMap(t, root, "docs/skills/bac/cognitive_map.md", 6)
	t.Chdir(root)

	l := NewLoader(root)
	c, err := l.Load("vuln/web/bac", anyDoneValidator, nil)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.CognitiveMap != "docs/skills/bac/cognitive_map.md" {
		t.Errorf("cognitive_map=%q", c.CognitiveMap)
	}
}

// 黑客松扩展：cognitive_map 只有 5 槽位 → 报错。
func TestLoader_Load_CognitiveMap_Missing6Slots(t *testing.T) {
	body := `---
name: x
description: x
applies_to:
  - role: sniffer
budget: {max_steps: 1, max_tokens: 1}
required_actions: [done]
cognitive_map: cm.md
---
正文`
	root := writeSkill(t, "x", body)
	writeCognitiveMap(t, root, "cm.md", 5)

	l := NewLoader(root)
	_, err := l.Load("x", anyDoneValidator, nil)
	if err == nil {
		t.Fatal("expected error: cognitive_map missing slots")
	}
	if !strings.Contains(err.Error(), "cognitive_map") {
		t.Errorf("err should mention cognitive_map, got: %v", err)
	}
}

// 黑客松扩展：cognitive_map 文件不存在 → 报错。
func TestLoader_Load_CognitiveMap_FileNotExist(t *testing.T) {
	body := `---
name: x
description: x
applies_to:
  - role: sniffer
budget: {max_steps: 1, max_tokens: 1}
required_actions: [done]
cognitive_map: not/exist.md
---
正文`
	root := writeSkill(t, "x", body)
	l := NewLoader(root)
	_, err := l.Load("x", anyDoneValidator, nil)
	if err == nil {
		t.Fatal("expected error: cognitive_map file not exist")
	}
	if !strings.Contains(err.Error(), "cognitive_map") {
		t.Errorf("err should mention cognitive_map, got: %v", err)
	}
}

// 黑客松扩展：done_validator 未注册 → 报错。
func TestLoader_Load_DoneValidator_NotRegistered(t *testing.T) {
	body := `---
name: x
description: x
applies_to:
  - role: sniffer
budget: {max_steps: 1, max_tokens: 1}
required_actions: [done]
done_validator: bac_v1
---
正文`
	root := writeSkill(t, "x", body)
	l := NewLoader(root)
	noneRegistered := func(string) bool { return false }
	_, err := l.Load("x", noneRegistered, nil)
	if err == nil {
		t.Fatal("expected error: done_validator not registered")
	}
	if !strings.Contains(err.Error(), "done_validator") {
		t.Errorf("err should mention done_validator, got: %v", err)
	}
}

// 黑客松扩展：done_validator 已注册 → 通过。
func TestLoader_Load_DoneValidator_Registered(t *testing.T) {
	body := `---
name: x
description: x
applies_to:
  - role: sniffer
budget: {max_steps: 1, max_tokens: 1}
required_actions: [done]
done_validator: bac_v1
---
正文`
	root := writeSkill(t, "x", body)
	l := NewLoader(root)
	registered := func(key string) bool { return key == "bac_v1" }
	c, err := l.Load("x", registered, nil)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.DoneValidator != "bac_v1" {
		t.Errorf("done_validator=%q", c.DoneValidator)
	}
}

// 文件不存在
func TestLoader_Load_SkillNotFound(t *testing.T) {
	root := t.TempDir()
	l := NewLoader(root)
	_, err := l.Load("nope", anyDoneValidator, nil)
	if err == nil {
		t.Fatal("expected file not found error")
	}
}
