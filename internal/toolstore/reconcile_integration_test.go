//go:build integration

package toolstore

import (
	"context"
	"testing"

	"github.com/V3teran/liusha/internal/dbtest"
	"github.com/V3teran/liusha/internal/tools/manifest"
)

// 分类显示名随目录同步入库：CLI 取 tools.yaml 的分类名，函数工具取 registry 的分类名，
// 工具页 / 智能体页据此展示，不在前端另持一份名单。
func TestReconcile_SyncsCategoryNames(t *testing.T) {
	ctx := context.Background()
	s := New(dbtest.NewPgPool(t))
	m := &manifest.ToolManifest{
		Categories: []manifest.Category{{Code: "oob", Name: "带外回调检测", Order: 1}},
		Tools:      []manifest.Tool{{Name: "interactsh-client", Category: "oob", Description: "d"}},
	}
	if _, _, err := Reconcile(ctx, s, m); err != nil {
		t.Fatal(err)
	}
	cases := map[string]string{"interactsh-client": "带外回调检测", "read_credentials": "凭证共享"}
	for name, want := range cases {
		got, err := s.Get(ctx, name)
		if err != nil {
			t.Fatal(err)
		}
		if got.CategoryName != want {
			t.Errorf("%s 分类名应为 %q，得 %q", name, want, got.CategoryName)
		}
	}
}
