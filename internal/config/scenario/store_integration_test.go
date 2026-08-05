//go:build integration

package scenario

import (
	"context"
	"testing"

	cfghunter "github.com/V3teran/liusha/internal/config/hunter"
	"github.com/V3teran/liusha/internal/dbtest"
)

// seedHunter 建一个领域 hunter，返回其 uuid，供 scenario.solo_hunter_id 引用。
func seedHunter(t *testing.T, hs *cfghunter.Store, code string) string {
	t.Helper()
	h, err := hs.Create(context.Background(), cfghunter.NewParams{
		Code: code, Kind: cfghunter.KindDomain, Name: code, Enabled: true,
	})
	if err != nil {
		t.Fatalf("seed hunter %s: %v", code, err)
	}
	return h.ID
}

// TestStore_CreateThenGetByCode 验证：建 swarm 场景后按 code 回读一致，domain 落库。
func TestStore_CreateThenGetByCode(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.NewPgPool(t)
	s := NewStore(pool)

	created, err := s.Create(ctx, NewParams{
		Code:        "web-pentest-killchain",
		Name:        "Web 渗透杀伤链",
		Instruction: "聚焦 Web 应用漏洞利用链",
		Domain:      "web",
		Engine:      EngineSwarm,
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
	if got.Engine != EngineSwarm || got.Domain != "web" || got.SoloHunterID != nil {
		t.Fatalf("字段不匹配: %+v", got)
	}
}

// TestStore_SoloReferencesHunter 验证：solo 场景按 solo_hunter_id 回读一致，domain 空折 web。
func TestStore_SoloReferencesHunter(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.NewPgPool(t)
	s := NewStore(pool)
	hs := cfghunter.NewStore(pool)

	hID := seedHunter(t, hs, "traffic-analysis")
	sc, err := s.Create(ctx, NewParams{
		Code: "passive-recon", Name: "被动侦察", Engine: EngineSolo,
		SoloHunterID: &hID, Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if sc.Domain != "web" {
		t.Fatalf("空 domain 应折成 web，得 %q", sc.Domain)
	}
	if sc.SoloHunterID == nil || *sc.SoloHunterID != hID {
		t.Fatalf("solo_hunter_id 应回读为 %s，得 %+v", hID, sc.SoloHunterID)
	}
}

// TestStore_SoloRejectsMissingHunter 验证：solo 场景未指定 solo_hunter_id 在应用层被拒。
func TestStore_SoloRejectsMissingHunter(t *testing.T) {
	ctx := context.Background()
	s := NewStore(dbtest.NewPgPool(t))

	if _, err := s.Create(ctx, NewParams{Code: "x", Name: "x", Engine: EngineSolo}); err == nil {
		t.Fatal("solo 场景缺 solo_hunter_id 应报错")
	}
}

// TestStore_SwarmRejectsHunter 验证：swarm 场景指定 solo_hunter_id 在应用层被拒。
func TestStore_SwarmRejectsHunter(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.NewPgPool(t)
	s := NewStore(pool)
	hs := cfghunter.NewStore(pool)

	hID := seedHunter(t, hs, "recon")
	if _, err := s.Create(ctx, NewParams{
		Code: "x", Name: "x", Engine: EngineSwarm, SoloHunterID: &hID,
	}); err == nil {
		t.Fatal("swarm 场景带 solo_hunter_id 应报错")
	}
}

// TestStore_CreateRejectsBadEngine 验证：非法 engine 在应用层被拒。
func TestStore_CreateRejectsBadEngine(t *testing.T) {
	ctx := context.Background()
	s := NewStore(dbtest.NewPgPool(t))

	if _, err := s.Create(ctx, NewParams{Code: "x", Name: "x", Engine: "bogus"}); err == nil {
		t.Fatal("非法 engine 应报错")
	}
}

// TestStore_SoloRejectsMissingHunterFK 验证：引用不存在的 hunter uuid 撞 DB FK。
func TestStore_SoloRejectsMissingHunterFK(t *testing.T) {
	ctx := context.Background()
	s := NewStore(dbtest.NewPgPool(t))

	ghost := "00000000-0000-0000-0000-000000000000"
	if _, err := s.Create(ctx, NewParams{
		Code: "x", Name: "x", Engine: EngineSolo, SoloHunterID: &ghost,
	}); err == nil {
		t.Fatal("引用不存在 hunter 应撞 FK 报错")
	}
}
