//go:build integration

package audit

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/V3teran/liusha/internal/dbtest"
)

// TestListPostgres 锁定审计列表的 SQL 语义：筛选组合、操作者按字面包含（LIKE 元字符转义）、
// 时间窗口、倒序与分页，total 恒为筛选全集行数。
func TestListPostgres(t *testing.T) {
	if testing.Short() {
		t.Skip("跳过集成测试")
	}
	ctx := context.Background()
	pool := dbtest.NewPgPool(t)
	defer pool.Close()
	store := NewStore(pool)
	if _, err := pool.Exec(ctx, "DELETE FROM audit_log"); err != nil {
		t.Fatalf("清理: %v", err)
	}

	mk := func(actor, action, target string, at time.Time) {
		meta, _ := json.Marshal(map[string]string{"k": "v"})
		if _, err := store.Append(ctx, Event{Actor: actor, Action: action, TargetKind: "task", TargetID: target, Metadata: meta}); err != nil {
			t.Fatalf("写入: %v", err)
		}
		// created_at 由 DB now() 生成，逐行回写以构造可断言的时间序
		if _, err := pool.Exec(ctx, "UPDATE audit_log SET created_at=$1 WHERE target_id=$2", at, target); err != nil {
			t.Fatalf("回写时间: %v", err)
		}
	}
	base := time.Now().Add(-time.Hour).Truncate(time.Second)
	mk("user:admin", ActionTaskCreate, "t1", base)
	mk("user:admin", ActionTaskAbort, "t2", base.Add(time.Minute))
	mk("runner", ActionCredentialDelete, "t3", base.Add(2*time.Minute))
	mk("user:100%_x", ActionTaskCreate, "t4", base.Add(3*time.Minute)) // LIKE 元字符按字面

	// 无筛选：倒序全量
	res, err := store.List(ctx, Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Total != 4 || len(res.Items) != 4 {
		t.Fatalf("total=%d items=%d", res.Total, len(res.Items))
	}
	if res.Items[0].TargetID != "t4" || res.Items[3].TargetID != "t1" {
		t.Fatalf("应按 created_at 倒序: %s .. %s", res.Items[0].TargetID, res.Items[3].TargetID)
	}

	// actor 按字面包含：% _ 不当通配符
	res, err = store.List(ctx, Filter{Actor: "100%_x"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Total != 1 || res.Items[0].Actor != "user:100%_x" {
		t.Fatalf("元字符应按字面匹配: total=%d %+v", res.Total, res.Items)
	}

	// action + 时间窗组合
	res, err = store.List(ctx, Filter{Action: ActionTaskCreate, Start: base.Add(30 * time.Second), End: base.Add(4 * time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	if res.Total != 1 || res.Items[0].TargetID != "t4" {
		t.Fatalf("组合筛选错误: total=%d %+v", res.Total, res.Items)
	}

	// 分页：total 不随页变化
	res, err = store.List(ctx, Filter{Limit: 2, Offset: 2})
	if err != nil {
		t.Fatal(err)
	}
	if res.Total != 4 || len(res.Items) != 2 || res.Items[0].TargetID != "t2" {
		t.Fatalf("分页错误: total=%d n=%d first=%s", res.Total, len(res.Items), res.Items[0].TargetID)
	}
}
