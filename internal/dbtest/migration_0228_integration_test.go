//go:build integration

package dbtest

import (
	"context"
	"testing"
)

const migration0228 uint = 228

// 迁移 0228：删除 finding.source_traffic_id。这一列声明了「漏洞来自哪条流量」，但从没有
// 任何代码写入它，界面据此提供「这条流量的漏洞」会是一个伪造的关系。
func TestMigration0228_DropFindingSourceTraffic(t *testing.T) {
	ctx := context.Background()
	pool := NewPgPoolBefore(t, migration0228)
	MigrateTo(t, pool, migration0228)
	var exists bool
	if err := pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM information_schema.columns
		WHERE table_name = 'finding' AND column_name = 'source_traffic_id')`).Scan(&exists); err != nil {
		t.Fatal(err)
	}
	if exists {
		t.Error("finding.source_traffic_id 应已删除")
	}
}
