//go:build integration

package dbtest

import (
	"context"
	"encoding/json"
	"maps"
	"testing"
)

const migration0208 uint = 208

// 迁移 0208：观察节点的图级状态由 metadata.confidence（unverified/verified/refuted/factual）
// 改为只有假设才有的 metadata.verification_status（pending/confirmed/refuted）；双写的
// verification_outcome 删除，出圈原因只留 refute_reason；issue 类观察并入 note；
// result 不再带任何置信度；评估者不可用的出圈原因 adjudication_failed 改名 judge_unavailable
// （节点与审计链两处）。存量数据形状取自实际库。
func TestMigration0208_VerificationStatus(t *testing.T) {
	ctx := context.Background()
	pool := NewPgPoolBefore(t, migration0208)
	assignment := SeedAssignment(t, pool)
	var taskID string
	if err := pool.QueryRow(ctx, `INSERT INTO task (assignment_id, brief) VALUES ($1, 'test') RETURNING id`,
		assignment).Scan(&taskID); err != nil {
		t.Fatal(err)
	}

	type row struct {
		kind, content, metadata string
		wantContent             map[string]any
		wantMetadata            map[string]any
	}
	base := func(extra map[string]any) map[string]any {
		m := map[string]any{"task_id": taskID, "source_type": "executor", "source_id": "x", "priority": "medium"}
		maps.Copy(m, extra)
		return m
	}
	rows := []row{
		{"observation", `{"category":"entity","statement":"s"}`, `{"confidence":"factual"}`,
			map[string]any{"category": "entity", "statement": "s"}, base(nil)},
		{"observation", `{"category":"issue","statement":"缺少 CSP"}`, `{"confidence":"factual"}`,
			map[string]any{"category": "note", "statement": "缺少 CSP"}, base(nil)},
		{"observation", `{"category":"hypothesis","statement":"h1"}`, `{"confidence":"unverified"}`,
			map[string]any{"category": "hypothesis", "statement": "h1"}, base(map[string]any{"verification_status": "pending"})},
		{"observation", `{"category":"hypothesis","statement":"h2"}`, `{"confidence":"verified","verification_outcome":"confirmed","duplicate_of":"r"}`,
			map[string]any{"category": "hypothesis", "statement": "h2"}, base(map[string]any{"verification_status": "confirmed", "duplicate_of": "r"})},
		{"observation", `{"category":"hypothesis","statement":"h3"}`, `{"confidence":"refuted","verification_outcome":"refuted","refute_reason":"claim_invalid"}`,
			map[string]any{"category": "hypothesis", "statement": "h3"}, base(map[string]any{"verification_status": "refuted", "refute_reason": "claim_invalid"})},
		{"observation", `{"category":"hypothesis","statement":"h4"}`, `{"confidence":"refuted","verification_outcome":"refuted","refute_reason":"not_reproducible","refuted_by":"machine_guard"}`,
			map[string]any{"category": "hypothesis", "statement": "h4"}, base(map[string]any{"verification_status": "refuted", "refute_reason": "not_reproducible"})},
		// 验证重试耗尽出圈的旧形状：verification_outcome 被写成了出圈原因
		{"observation", `{"category":"hypothesis","statement":"h5"}`, `{"confidence":"refuted","verification_outcome":"invalid_recipe","refute_reason":"invalid_recipe","failure_reason":"f","verify_attempts":3}`,
			map[string]any{"category": "hypothesis", "statement": "h5"}, base(map[string]any{"verification_status": "refuted", "refute_reason": "invalid_recipe", "failure_reason": "f", "verify_attempts": float64(3)})},
		// 只有 verification_outcome 记着原因、缺 refute_reason 的旧形状：原因迁到 refute_reason
		{"observation", `{"category":"hypothesis","statement":"h6"}`, `{"confidence":"refuted","verification_outcome":"adjudication_failed"}`,
			map[string]any{"category": "hypothesis", "statement": "h6"}, base(map[string]any{"verification_status": "refuted", "refute_reason": "judge_unavailable"})},
		{"observation", `{"category":"hypothesis","statement":"h7"}`, `{"confidence":"refuted","verification_outcome":"adjudication_failed","refute_reason":"adjudication_failed","verify_attempts":3}`,
			map[string]any{"category": "hypothesis", "statement": "h7"}, base(map[string]any{"verification_status": "refuted", "refute_reason": "judge_unavailable", "verify_attempts": float64(3)})},
		{"result", `{"summary":"r","priority":"high","confidence":"low"}`, `{"confidence":"verified","claim_fingerprint":"k"}`,
			map[string]any{"summary": "r", "priority": "high"}, base(map[string]any{"claim_fingerprint": "k"})},
	}
	ids := make([]string, len(rows))
	for i, r := range rows {
		var meta map[string]any
		if err := json.Unmarshal([]byte(r.metadata), &meta); err != nil {
			t.Fatal(err)
		}
		full, _ := json.Marshal(base(meta))
		if err := pool.QueryRow(ctx,
			`INSERT INTO exploration_node (id, task_id, kind, content, metadata) VALUES (gen_random_uuid(), $1, $2, $3, $4) RETURNING id`,
			taskID, r.kind, r.content, full).Scan(&ids[i]); err != nil {
			t.Fatal(err)
		}
	}

	// 审计链：出圈留档的 evidence 也记着 refute_reason。
	if _, err := pool.Exec(ctx,
		`INSERT INTO exploration_verification (id, task_id, node_id, outcome, evidence) VALUES
		   ('v1', $1, $2, 'refuted', '{"refute_reason":"adjudication_failed","verify_attempts":3}'),
		   ('v2', $1, $2, 'refuted', '{"refute_reason":"invalid_recipe","invalid_recipe":true}')`,
		taskID, ids[0]); err != nil {
		t.Fatal(err)
	}

	MigrateTo(t, pool, migration0208)

	for id, want := range map[string]map[string]any{
		"v1": {"refute_reason": "judge_unavailable", "verify_attempts": float64(3)},
		"v2": {"refute_reason": "invalid_recipe", "invalid_recipe": true},
	} {
		var evidence map[string]any
		if err := pool.QueryRow(ctx, `SELECT evidence FROM exploration_verification WHERE id = $1`, id).Scan(&evidence); err != nil {
			t.Fatal(err)
		}
		if !jsonEqual(evidence, want) {
			t.Errorf("审计 %s evidence = %v，期望 %v", id, evidence, want)
		}
	}

	for i, r := range rows {
		var content, metadata map[string]any
		if err := pool.QueryRow(ctx, `SELECT content, metadata FROM exploration_node WHERE id = $1`, ids[i]).
			Scan(&content, &metadata); err != nil {
			t.Fatal(err)
		}
		if !maps.Equal(content, r.wantContent) {
			t.Errorf("行 %d content = %v，期望 %v", i, content, r.wantContent)
		}
		if !jsonEqual(metadata, r.wantMetadata) {
			t.Errorf("行 %d metadata = %v，期望 %v", i, metadata, r.wantMetadata)
		}
	}

	// 约束：验证状态只能出现在观察上，取值限 pending/confirmed/refuted。
	for _, bad := range []struct{ kind, metadata string }{
		{"observation", `{"verification_status":"verified"}`},
		{"result", `{"verification_status":"confirmed"}`},
	} {
		meta := base(nil)
		var extra map[string]any
		_ = json.Unmarshal([]byte(bad.metadata), &extra)
		maps.Copy(meta, extra)
		full, _ := json.Marshal(meta)
		if _, err := pool.Exec(ctx,
			`INSERT INTO exploration_node (id, task_id, kind, content, metadata) VALUES (gen_random_uuid(), $1, $2, '{}', $3)`,
			taskID, bad.kind, full); err == nil {
			t.Errorf("%s %s 应被约束拒绝", bad.kind, bad.metadata)
		}
	}
}

func jsonEqual(a, b map[string]any) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
}
