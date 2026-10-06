package skill

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeCategorizedSkill 在 base 下落 <cat>/<name>/SKILL.md（frontmatter 自定义）。
func writeCategorizedSkill(t *testing.T, base, cat, name, frontmatter, body string) {
	t.Helper()
	dir := filepath.Join(base, cat, name)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	content := "---\n" + frontmatter + "\n---\n" + body
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(content), 0o600); err != nil {
		t.Fatalf("write SKILL.md: %v", err)
	}
}

func newTestSet(t *testing.T) *Set {
	t.Helper()
	base := t.TempDir()
	writeCategorizedSkill(t, base, "tooling", "browser-use",
		"name: Browser Use 手册\ndescription: 浏览器自动化手册", "浏览器正文")
	writeCategorizedSkill(t, base, "vuln", "dom-xss",
		"name: DOM XSS 指南\ndescription: DOM XSS 挖掘指南", "xss正文")
	writeCategorizedSkill(t, base, "vuln", "bac",
		"name: BAC 指南\ndescription: 越权检测指南", "bac正文")
	return NewSet(map[string]string{"tooling": filepath.Join(base, "tooling"), "vuln": filepath.Join(base, "vuln")})
}

// TestSet_Index_BareNames：两目录聚合后按裸名寻址，Key 回填为目录名。
func TestSet_Index_BareNames(t *testing.T) {
	ctx := context.Background()
	s := newTestSet(t)
	names, err := s.Index()
	if err != nil {
		t.Fatalf("Index: %v", err)
	}
	if len(names) != 3 {
		t.Fatalf("names=%v", names)
	}

	c, err := s.Load(ctx, "browser-use") // tooling 类
	if err != nil || c.Body != "浏览器正文" {
		t.Fatalf("Load browser-use: card=%+v err=%v", c, err)
	}
	if c.Key != "browser-use" {
		t.Errorf("Key=%q（应为目录名）", c.Key)
	}
	c, err = s.Load(ctx, "dom-xss") // vuln 类
	if err != nil || c.Body != "xss正文" {
		t.Fatalf("Load dom-xss: card=%+v err=%v", c, err)
	}
	if _, err := s.Load(ctx, "nope"); err == nil {
		t.Error("Load 不存在的名字应报错")
	}
}

// TestSet_Index_MissingCategorySkipped：某类目录缺失→整类跳过，其余类别可用。
func TestSet_Index_MissingCategorySkipped(t *testing.T) {
	base := t.TempDir()
	writeCategorizedSkill(t, base, "vuln", "bac", "name: B\ndescription: b", "正文")
	s := NewSet(map[string]string{
		"tooling": filepath.Join(base, "tooling"), // 不存在
		"vuln":    filepath.Join(base, "vuln"),
	})
	names, err := s.Index()
	if err != nil {
		t.Fatalf("Index: %v", err)
	}
	if len(names) != 1 || names[0] != "bac" {
		t.Fatalf("names=%v（缺目录的类别应跳过）", names)
	}
}

// TestSet_Index_DuplicateBareName：跨目录同名 → 启动期报错（声明歧义 fail-fast）。
func TestSet_Index_DuplicateBareName(t *testing.T) {
	base := t.TempDir()
	writeCategorizedSkill(t, base, "tooling", "dup", "name: A\ndescription: a", "x")
	writeCategorizedSkill(t, base, "vuln", "dup", "name: B\ndescription: b", "y")
	s := NewSet(map[string]string{"tooling": filepath.Join(base, "tooling"), "vuln": filepath.Join(base, "vuln")})
	if _, err := s.Index(); err == nil || !strings.Contains(err.Error(), "冲突") {
		t.Fatalf("期望裸名冲突错误，got %v", err)
	}
}

// TestView_Whitelist：Tier 1 只见声明项（声明序），Tier 2 白名单外拒绝。
func TestView_Whitelist(t *testing.T) {
	ctx := context.Background()
	s := newTestSet(t)
	if _, err := s.Index(); err != nil {
		t.Fatalf("Index: %v", err)
	}

	v := s.Allow([]string{"dom-xss", "bac", "ghost"}) // ghost=声明了但不存在（配置漂移）
	metas, err := v.Metas(ctx)
	if err != nil {
		t.Fatalf("Metas: %v", err)
	}
	// 源序（Set.Metas 按 Key 排序保证渲染稳定）：bac < dom-xss。
	if len(metas) != 2 || metas[0].Key != "bac" || metas[1].Key != "dom-xss" {
		t.Fatalf("Metas 应只含存在项且按 Key 稳定排序: %v %v", metas[0].Key, metas[1].Key)
	}
	if _, err := v.Load(ctx, "browser-use"); err == nil {
		t.Error("白名单外的 Load 应拒绝")
	}
	if _, err := v.Load(ctx, "ghost"); err == nil {
		t.Error("声明了但目录不存在的 Load 应报错")
	}
	if _, err := v.Load(ctx, "dom-xss"); err != nil {
		t.Errorf("白名单内 Load 应放行: %v", err)
	}

	// nil = 不过滤（全量视图）；空 slice = 空集。
	if m, err := s.Allow(nil).Metas(ctx); err != nil || len(m) != 3 {
		t.Errorf("nil 白名单应全量: %d err=%v", len(m), err)
	}
	if m, err := s.Allow([]string{}).Metas(ctx); err != nil || len(m) != 0 {
		t.Errorf("空白名单应为空集: %d err=%v", len(m), err)
	}
}

// TestRenderIndex：寻址名（Key）必须进渲染，显示名括注、空集返回空串。
func TestRenderIndex(t *testing.T) {
	if got := RenderIndex(nil); got != "" {
		t.Errorf("空集应返回空串，got %q", got)
	}
	s := newTestSet(t)
	if _, err := s.Index(); err != nil {
		t.Fatalf("Index: %v", err)
	}
	metas, err := s.Allow([]string{"dom-xss", "browser-use"}).Metas(context.Background())
	if err != nil {
		t.Fatalf("Metas: %v", err)
	}
	got := RenderIndex(metas)
	if !strings.Contains(got, "dom-xss") || !strings.Contains(got, "browser-use") {
		t.Errorf("渲染缺少寻址名: %q", got)
	}
	if !strings.Contains(got, "read_skill") {
		t.Errorf("渲染应提示 read_skill 通道: %q", got)
	}
	if !strings.Contains(got, "DOM XSS 指南") {
		t.Errorf("渲染应含显示名: %q", got)
	}
}
