package skill

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/V3teran/liusha/internal/skillstore"
)

// fakeSource 是 SkillSource 的可编程桩：预置 skill 集，可注入错误。
type fakeSource struct {
	skills []skillstore.Skill
	err    error
}

func (f *fakeSource) EnabledSkills(context.Context) ([]skillstore.Skill, error) {
	if f.err != nil {
		return nil, f.err
	}
	// 模拟真实契约：WHERE enabled=true 已在 SQL 过滤，接口返回的就是启用集。
	var out []skillstore.Skill
	for _, sk := range f.skills {
		if sk.Enabled {
			out = append(out, sk)
		}
	}
	return out, nil
}

func (f *fakeSource) SkillByCode(_ context.Context, code string) (skillstore.Skill, error) {
	if f.err != nil {
		return skillstore.Skill{}, f.err
	}
	for _, sk := range f.skills {
		if sk.Code == code {
			return sk, nil
		}
	}
	return skillstore.Skill{}, errors.New("no rows")
}

func testSource() *fakeSource {
	return &fakeSource{skills: []skillstore.Skill{
		{ID: "1", Code: "dom-xss", Category: "vuln", Name: "DOM XSS 指南", Description: "d1", Body: "xss正文", Enabled: true},
		{ID: "2", Code: "bac", Category: "vuln", Name: "BAC 指南", Description: "d2", Body: "bac正文", Enabled: true},
		{ID: "3", Code: "dead", Category: "tooling", Name: "下线手册", Description: "d3", Body: "dead正文", Enabled: false},
	}}
}

// TestStoreReader_Metas：只返回启用项，Key=code，frontmatter 字段齐全。
func TestStoreReader_Metas(t *testing.T) {
	r := NewStoreReader(testSource())
	metas, err := r.Metas(context.Background())
	if err != nil {
		t.Fatalf("Metas: %v", err)
	}
	if len(metas) != 2 {
		t.Fatalf("禁用项不应进索引: %d", len(metas))
	}
	byKey := map[string]*Card{}
	for _, c := range metas {
		byKey[c.Key] = c
	}
	c := byKey["dom-xss"]
	if c == nil || c.Name != "DOM XSS 指南" || c.Description != "d1" || c.Category != "vuln" {
		t.Fatalf("frontmatter 转换缺失: %+v", c)
	}
	if c.Body != "" {
		t.Errorf("Metas 不应携带正文（省 token）: %q", c.Body)
	}
}

// TestStoreReader_Load：启用项返回全文；禁用项拒载且报错可读；不存在报错。
func TestStoreReader_Load(t *testing.T) {
	ctx := context.Background()
	r := NewStoreReader(testSource())

	c, err := r.Load(ctx, "bac")
	if err != nil || c.Body != "bac正文" || c.Key != "bac" {
		t.Fatalf("Load bac: card=%+v err=%v", c, err)
	}

	if _, err := r.Load(ctx, "dead"); err == nil || !strings.Contains(err.Error(), "禁用") {
		t.Errorf("禁用项应拒载并说明: %v", err)
	}
	if _, err := r.Load(ctx, "nope"); err == nil {
		t.Error("不存在的 skill 应报错")
	}
}

// TestStoreReader_SourceError：后端错误透传（调用方降级，不静默当空集）。
func TestStoreReader_SourceError(t *testing.T) {
	src := testSource()
	src.err = errors.New("redis down")
	r := NewStoreReader(src)
	if _, err := r.Metas(context.Background()); err == nil {
		t.Error("后端错误应透传")
	}
}
