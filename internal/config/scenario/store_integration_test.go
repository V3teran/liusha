//go:build integration

package scenario

import (
	"context"
	"testing"

	cfgplaybook "github.com/V3teran/liusha/internal/config/playbook"
	"github.com/V3teran/liusha/internal/dbtest"
)

// seedPlaybook 建一个 playbook，返回其 uuid，供 scenario FK 引用。
func seedPlaybook(t *testing.T, ps *cfgplaybook.Store, code string) string {
	t.Helper()
	pb, err := ps.Create(context.Background(), cfgplaybook.NewParams{Code: code, Name: code, Enabled: true})
	if err != nil {
		t.Fatalf("seed playbook %s: %v", code, err)
	}
	return pb.ID
}

// TestStore_CreateThenGetByCode 验证：建场景后按 code 回读一致，domain 落库。
func TestStore_CreateThenGetByCode(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.NewPgPool(t)
	s := NewStore(pool)
	ps := cfgplaybook.NewStore(pool)

	pbID := seedPlaybook(t, ps, "web-pentest")
	created, err := s.Create(ctx, NewParams{
		Code:        "web-pentest-killchain",
		Name:        "Web 渗透杀伤链",
		Instruction: "聚焦 Web 应用漏洞利用链",
		Domain:      "web",
		Engine:      EngineSwarm,
		PlaybookID:  pbID,
		Enabled:     true,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if created.ID == "" {
		t.Fatal("create 应回填 uuid")
	}

	got, err := s.GetByCode(ctx, "web-pentest-killchain")
	if err != nil {
		t.Fatalf("get by code: %v", err)
	}
	if got.Engine != EngineSwarm || got.Domain != "web" || got.PlaybookID != pbID {
		t.Fatalf("字段不匹配: %+v", got)
	}
}

// TestStore_DomainDefaultsToWeb 验证：Domain 留空时应用层折成 web。
func TestStore_DomainDefaultsToWeb(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.NewPgPool(t)
	s := NewStore(pool)
	ps := cfgplaybook.NewStore(pool)

	pbID := seedPlaybook(t, ps, "pb")
	sc, err := s.Create(ctx, NewParams{
		Code: "solo-scan", Name: "单跑", Engine: EngineSolo, PlaybookID: pbID, Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if sc.Domain != "web" {
		t.Fatalf("空 domain 应折成 web，得 %q", sc.Domain)
	}
}

// TestStore_CreateRejectsBadEngine 验证：非法 engine 在应用层被拒。
func TestStore_CreateRejectsBadEngine(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.NewPgPool(t)
	s := NewStore(pool)
	ps := cfgplaybook.NewStore(pool)

	pbID := seedPlaybook(t, ps, "pb")
	if _, err := s.Create(ctx, NewParams{Code: "x", Name: "x", Engine: "bogus", PlaybookID: pbID}); err == nil {
		t.Fatal("非法 engine 应报错")
	}
}

// TestStore_CreateRejectsMissingPlaybook 验证：引用不存在的 playbook_id 撞 FK。
func TestStore_CreateRejectsMissingPlaybook(t *testing.T) {
	ctx := context.Background()
	s := NewStore(dbtest.NewPgPool(t))

	if _, err := s.Create(ctx, NewParams{
		Code: "x", Name: "x", Engine: EngineSolo,
		PlaybookID: "00000000-0000-0000-0000-000000000000",
	}); err == nil {
		t.Fatal("引用不存在 playbook 应撞 FK 报错")
	}
}
