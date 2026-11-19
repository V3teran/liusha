//go:build integration

package dbtest

import (
	"context"
	"testing"

	"github.com/V3teran/liusha/internal/builtinsync"
)

const migration0236 uint = 236

// 迁移 0236：出厂场景 ctf 改名「CTF」。
//   - 已建任务的场景快照改为新名称（任务详情与续跑的提示词随之一致）；
//   - 用户改过内容的内置行一并改名（没改过的由出厂同步跟随新版，迁移不碰，否则会被误判为改过）；
//   - 自建场景与其它场景的任务不动。
func TestMigration0236_RenameCTFScenario(t *testing.T) {
	ctx := context.Background()
	pool := NewPgPoolBefore(t, migration0236)
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	const oldName, oldBody = "CTF 夺旗", "本任务是在授权 CTF 靶场里解题夺旗，交付物是题目设定的 flag。"
	oldHash := builtinsync.Hash(oldName, "d", oldBody)
	assignment := SeedAssignment(t, pool)
	exec(`INSERT INTO task (assignment_id, goal, scenario, scenario_name, scenario_body) VALUES
		($1, 'ctf 任务', 'ctf', $2, $3),
		($1, '渗透任务', 'pentest', '渗透测试', '解题夺旗')`, assignment, oldName, oldBody)

	// 改过内容的内置 ctf 行
	exec(`INSERT INTO scenario (code, name, description, body, is_builtin, seed_hash) VALUES
		('ctf', $1, 'd', '改过的正文：解题夺旗', true, $2)`, oldName, oldHash)
	MigrateTo(t, pool, migration0236)

	var name, body string
	if err := pool.QueryRow(ctx, `SELECT scenario_name, scenario_body FROM task WHERE goal='ctf 任务'`).Scan(&name, &body); err != nil ||
		name != "CTF" || body != "本任务是在授权 CTF 靶场里解题，交付物是题目设定的 flag。" {
		t.Errorf("ctf 任务快照应改为新名称与新措辞: %q %q %v", name, body, err)
	}
	if err := pool.QueryRow(ctx, `SELECT scenario_name, scenario_body FROM task WHERE goal='渗透任务'`).Scan(&name, &body); err != nil ||
		name != "渗透测试" || body != "解题夺旗" {
		t.Errorf("其它场景的任务快照不动: %q %q %v", name, body, err)
	}
	var seedHash *string
	if err := pool.QueryRow(ctx, `SELECT name, body, seed_hash FROM scenario WHERE code='ctf'`).Scan(&name, &body, &seedHash); err != nil ||
		name != "CTF" || body != "改过的正文：解题" || seedHash == nil || *seedHash != oldHash {
		t.Errorf("改过内容的内置 ctf 行应改名、基线不动（仍视为改过）: %q %q %v %v", name, body, seedHash, err)
	}
}

// 没改过的内置 ctf 行迁移不碰：出厂同步按「当前内容哈希 == 基线」认出它没改过并跟随新版。
func TestMigration0236_UnmodifiedBuiltinLeftToSync(t *testing.T) {
	ctx := context.Background()
	pool := NewPgPoolBefore(t, migration0236)
	const oldName, oldBody = "CTF 夺旗", "解题夺旗"
	if _, err := pool.Exec(ctx, `INSERT INTO scenario (code, name, description, body, is_builtin, seed_hash) VALUES
		('ctf', $1, 'd', $2, true, $3)`, oldName, oldBody, builtinsync.Hash(oldName, "d", oldBody)); err != nil {
		t.Fatal(err)
	}
	MigrateTo(t, pool, migration0236)

	var name string
	if err := pool.QueryRow(ctx, `SELECT name FROM scenario WHERE code='ctf'`).Scan(&name); err != nil || name != oldName {
		t.Errorf("没改过的内置行应留给出厂同步: %q %v", name, err)
	}
}
