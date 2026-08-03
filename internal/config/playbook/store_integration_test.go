//go:build integration

package playbook

import (
	"context"
	"testing"

	cfghunter "github.com/V3teran/liusha/internal/config/hunter"
	"github.com/V3teran/liusha/internal/dbtest"
)

// seedHunter 建一个领域猎手，返回其 uuid，供组合测试引用。
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

// TestStore_SetHuntersOrdered 验证：SetHunters 后 ListHunters 按 position 有序返回。
func TestStore_SetHuntersOrdered(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.NewPgPool(t)
	s := NewStore(pool)
	hs := cfghunter.NewStore(pool)

	pb, err := s.Create(ctx, NewParams{Code: "web-pentest", Name: "Web 渗透", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	reconID := seedHunter(t, hs, "recon")
	exploitID := seedHunter(t, hs, "exploitation")

	// 故意乱序传入，靠 position 排序
	if err := s.SetHunters(ctx, pb.ID, []PlaybookHunter{
		{PlaybookID: pb.ID, HunterID: exploitID, Position: 1},
		{PlaybookID: pb.ID, HunterID: reconID, Position: 0},
	}); err != nil {
		t.Fatalf("set hunters: %v", err)
	}

	got, err := s.ListHunters(ctx, pb.ID)
	if err != nil {
		t.Fatalf("list hunters: %v", err)
	}
	if len(got) != 2 || got[0].Code != "recon" || got[1].Code != "exploitation" {
		t.Fatalf("组合应按 position 有序 [recon, exploitation]，得 %+v", got)
	}
}

// TestStore_SetHuntersIdempotent 验证：重复 SetHunters 幂等（先删后插，不累积）。
func TestStore_SetHuntersIdempotent(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.NewPgPool(t)
	s := NewStore(pool)
	hs := cfghunter.NewStore(pool)

	pb, _ := s.Create(ctx, NewParams{Code: "pb", Name: "pb", Enabled: true})
	reconID := seedHunter(t, hs, "recon")

	items := []PlaybookHunter{{PlaybookID: pb.ID, HunterID: reconID, Position: 0}}
	if err := s.SetHunters(ctx, pb.ID, items); err != nil {
		t.Fatal(err)
	}
	if err := s.SetHunters(ctx, pb.ID, items); err != nil {
		t.Fatalf("second set: %v", err)
	}
	got, err := s.ListHunters(ctx, pb.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("重复 SetHunters 应幂等（1 条），得 %d", len(got))
	}
}

// TestStore_DeleteCascadesCombination 验证：删 playbook 级联清 playbook_hunter（ON DELETE CASCADE）。
func TestStore_DeleteCascadesCombination(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.NewPgPool(t)
	s := NewStore(pool)
	hs := cfghunter.NewStore(pool)

	pb, _ := s.Create(ctx, NewParams{Code: "pb", Name: "pb", Enabled: true})
	reconID := seedHunter(t, hs, "recon")
	if err := s.SetHunters(ctx, pb.ID, []PlaybookHunter{{PlaybookID: pb.ID, HunterID: reconID, Position: 0}}); err != nil {
		t.Fatal(err)
	}

	if err := s.Delete(ctx, "pb"); err != nil {
		t.Fatalf("delete playbook: %v", err)
	}
	// 组合应随 CASCADE 清空
	var count int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM playbook_hunter WHERE playbook_id=$1", pb.ID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("删 playbook 后组合应级联清空，仍有 %d 条", count)
	}
	// 但被引用的 hunter 不受影响（ON DELETE RESTRICT 保护它）
	if _, err := hs.GetByCode(ctx, "recon"); err != nil {
		t.Fatalf("hunter 不应随 playbook 删除: %v", err)
	}
}
