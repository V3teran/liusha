//go:build integration

package dbtest

import (
	"context"
	"testing"
)

const migration0207 uint = 207

// 迁移 0207：浏览器流量的身份戳由「<任务ID>-<身份名>」改为身份名，与 agent 在工具里使用、
// 按身份过滤流量时给出的名字一致。只改写带本任务前缀的浏览器流量；其余原样保留。
func TestMigration0207_BrowserTrafficIdentityBecomesName(t *testing.T) {
	ctx := context.Background()
	pool := NewPgPoolBefore(t, migration0207)
	assignment := SeedAssignment(t, pool)
	var taskID, otherTask string
	for _, id := range []*string{&taskID, &otherTask} {
		if err := pool.QueryRow(ctx, `INSERT INTO task (assignment_id, brief) VALUES ($1, 'test') RETURNING id`,
			assignment).Scan(id); err != nil {
			t.Fatal(err)
		}
	}
	rows := []struct {
		task, identity, tool string
		want                 string
	}{
		{taskID, taskID + "-default", "browser", "default"},
		{taskID, taskID + "-admin-2", "browser", "admin-2"},
		{taskID, otherTask + "-admin", "browser", otherTask + "-admin"}, // 不是本任务前缀：不动
		{taskID, "default", "browser", "default"},                       // 已是身份名
		{taskID, taskID + "-x", "http_request", taskID + "-x"},          // 非浏览器流量
		{taskID, "", "browser", ""},
	}
	ids := make([]int64, len(rows))
	for i, r := range rows {
		if err := pool.QueryRow(ctx,
			`INSERT INTO agent_traffic (task_id, identity, tool, host, method) VALUES ($1, $2, $3, 'h', 'GET') RETURNING id`,
			r.task, r.identity, r.tool).Scan(&ids[i]); err != nil {
			t.Fatal(err)
		}
	}
	var nullID int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO agent_traffic (task_id, tool, host, method) VALUES ($1, 'browser', 'h', 'GET') RETURNING id`,
		taskID).Scan(&nullID); err != nil {
		t.Fatal(err)
	}

	MigrateUp(t, pool)

	for i, r := range rows {
		var got string
		if err := pool.QueryRow(ctx, `SELECT identity FROM agent_traffic WHERE id = $1`, ids[i]).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != r.want {
			t.Errorf("%s/%s: identity = %q，期望 %q", r.tool, r.identity, got, r.want)
		}
	}
	var null *string
	if err := pool.QueryRow(ctx, `SELECT identity FROM agent_traffic WHERE id = $1`, nullID).Scan(&null); err != nil || null != nil {
		t.Errorf("空身份应保持 NULL: %v %v", null, err)
	}
}
