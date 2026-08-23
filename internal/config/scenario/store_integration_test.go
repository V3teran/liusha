//go:build integration

package scenario

import (
	"context"
	"testing"

	cfgagent "github.com/V3teran/liusha/internal/config/agent"
	"github.com/V3teran/liusha/internal/dbtest"
)

// seedAgent 建一个领域 agent，返回其 uuid，供 scenario.solo_agent_id 引用。
func seedAgent(t *testing.T, hs *cfgagent.Store, code string) string {
	t.Helper()
	h, err := hs.Create(context.Background(), cfgagent.NewParams{
		Code: code, Kind: cfgagent.KindExecutor, Name: code, Enabled: true,
	})
	if err != nil {
		t.Fatalf("seed executor %s: %v", code, err)
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
	if got.Engine != EngineSwarm || got.SoloExecutorID != nil {
		t.Fatalf("字段不匹配: %+v", got)
	}
}

// TestStore_SoloReferencesAgent 验证：solo 场景按 solo_agent_id 回读一致。
func TestStore_SoloReferencesAgent(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.NewPgPool(t)
	s := NewStore(pool)
	hs := cfgagent.NewStore(pool)

	hID := seedAgent(t, hs, "traffic-analysis")
	sc, err := s.Create(ctx, NewParams{
		Code: "api-pentest", Name: "API 渗透", Engine: EngineSolo,
		SoloExecutorID: &hID, Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if sc.SoloExecutorID == nil || *sc.SoloExecutorID != hID {
		t.Fatalf("solo_agent_id 应回读为 %s，得 %+v", hID, sc.SoloExecutorID)
	}
}

// TestStore_SoloRejectsMissingAgent 验证：solo 场景未指定 solo_agent_id 在应用层被拒。
func TestStore_SoloRejectsMissingAgent(t *testing.T) {
	ctx := context.Background()
	s := NewStore(dbtest.NewPgPool(t))

	if _, err := s.Create(ctx, NewParams{Code: "x", Name: "x", Engine: EngineSolo}); err == nil {
		t.Fatal("solo 场景缺 solo_agent_id 应报错")
	}
}

// TestStore_SwarmRejectsAgent 验证：swarm 场景指定 solo_agent_id 在应用层被拒。
func TestStore_SwarmRejectsAgent(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.NewPgPool(t)
	s := NewStore(pool)
	hs := cfgagent.NewStore(pool)

	hID := seedAgent(t, hs, "recon")
	if _, err := s.Create(ctx, NewParams{
		Code: "x", Name: "x", Engine: EngineSwarm, SoloExecutorID: &hID,
	}); err == nil {
		t.Fatal("swarm 场景带 solo_agent_id 应报错")
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

// TestStore_SoloRejectsMissingAgentFK 验证：引用不存在的 agent uuid 撞 DB FK。
func TestStore_SoloRejectsMissingAgentFK(t *testing.T) {
	ctx := context.Background()
	s := NewStore(dbtest.NewPgPool(t))

	ghost := "00000000-0000-0000-0000-000000000000"
	if _, err := s.Create(ctx, NewParams{
		Code: "x", Name: "x", Engine: EngineSolo, SoloExecutorID: &ghost,
	}); err == nil {
		t.Fatal("引用不存在 executor 应撞 FK 报错")
	}
}
