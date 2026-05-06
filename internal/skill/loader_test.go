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

// TestLoader_Load_Basic：读一个最小有效 SKILL.md，断言核心字段。
func TestLoader_Load_Basic(t *testing.T) {
	body := `---
name: vuln/web/bac
description: BAC
allowed-tools: [fetch_credentials, replay_multi_identity]
---
正文`
	root := writeSkill(t, "vuln/web/bac", body)
	l := NewLoader(root)
	c, err := l.Load("vuln/web/bac")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.Name != "vuln/web/bac" {
		t.Errorf("name=%q", c.Name)
	}
	if c.Description != "BAC" {
		t.Errorf("desc=%q", c.Description)
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
	if _, err := l.Load("broken"); err == nil {
		t.Fatal("expected yaml parse error, got nil")
	}
}

// TestLoader_Load_MissingFrontmatter：缺少 --- 分隔符。
func TestLoader_Load_MissingFrontmatter(t *testing.T) {
	body := `name: x
没有分隔符的纯正文`
	root := writeSkill(t, "noheader", body)
	l := NewLoader(root)
	_, err := l.Load("noheader")
	if err == nil {
		t.Fatal("expected missing-frontmatter error")
	}
	if !strings.Contains(err.Error(), "frontmatter") {
		t.Errorf("err should mention frontmatter, got: %v", err)
	}
}

// v1.1 末次精简：删除 allowed-tools / done_validator frontmatter 字段，
// 相关 3 个测试已废弃（TestLoader_Load_AllowedToolsField /
// TestLoader_Load_DoneValidator_NotRegistered/Registered）。
// 理由：builder 是唯一真理来源，frontmatter 重复声明已删除。

// 文件不存在
func TestLoader_Load_SkillNotFound(t *testing.T) {
	root := t.TempDir()
	l := NewLoader(root)
	_, err := l.Load("nope")
	if err == nil {
		t.Fatal("expected file not found error")
	}
}
