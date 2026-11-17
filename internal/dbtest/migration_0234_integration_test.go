//go:build integration

package dbtest

import (
	"context"
	"testing"
)

const migration0234 uint = 234

// 迁移 0234：配方信封 domain → kind（web → http、generic → steps），证据 exploit_* →
// 与 baseline_* 对称的 status_code / duration_ms，结果节点上旧形状的指纹键作废。
// 证据嵌在多层，全部改写；JSON 字符串内容里出现的同名文字不动。
func TestMigration0234_ReproKind(t *testing.T) {
	ctx := context.Background()
	pool := NewPgPoolBefore(t, migration0234)
	assignment := SeedAssignment(t, pool)
	var taskID string
	if err := pool.QueryRow(ctx, `INSERT INTO task (assignment_id, goal) VALUES ($1, 'x') RETURNING id`, assignment).Scan(&taskID); err != nil {
		t.Fatal(err)
	}
	node := func(kind, content, metadata string) string {
		var id string
		if err := pool.QueryRow(ctx, `INSERT INTO exploration_node (id, task_id, kind, content, metadata)
			VALUES (gen_random_uuid(), $1, $2, $3::jsonb, $4::jsonb) RETURNING id::text`,
			taskID, kind, content, metadata).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	obsHTTP := node("observation", `{"statement":"写着 \"domain\": \"web\" 的陈述","repro":{"domain":"web","recipe":{},"assert":{}}}`, `{}`)
	obsSteps := node("observation", `{"repro":{"domain":"generic","recipe":{"steps":"s"},"assert":{}}}`, `{}`)
	result := node("result", `{"statement":"s"}`, `{"claim_fingerprint":"c|get|h|/p|q","task_id":"t"}`)

	evidence := `{"exploit_status_code":200,"exploit_duration_ms":5,"baseline_status_code":404,` +
		`"session_refresh":[{"status_code":302}],"replays":[{"evidence":{"exploit_status_code":200,"domain":"generic"}}]}`
	if _, err := pool.Exec(ctx, `INSERT INTO exploration_verification (id, task_id, node_id, repro, outcome, evidence)
		VALUES ('v1', $1, $2::uuid, '{"domain":"web","recipe":{},"assert":{}}', 'confirmed', $3::jsonb)`,
		taskID, obsHTTP, evidence); err != nil {
		t.Fatal(err)
	}
	var findingID string
	if err := pool.QueryRow(ctx, `INSERT INTO finding (task_id, host, severity, summary, repro, evaluation)
		VALUES ($1, 'h', 'high', 's', '{"domain":"generic","recipe":{},"assert":{}}'::jsonb,
		        jsonb_build_object('evidence', $2::jsonb)) RETURNING id::text`, taskID, evidence).Scan(&findingID); err != nil {
		t.Fatal(err)
	}

	MigrateTo(t, pool, migration0234)

	text := func(q string, args ...any) string {
		t.Helper()
		var s string
		if err := pool.QueryRow(ctx, q, args...).Scan(&s); err != nil {
			t.Fatal(err)
		}
		return s
	}
	wantEvidence := `{"replays": [{"evidence": {"kind": "steps", "status_code": 200}}], "duration_ms": 5, "status_code": 200, ` +
		`"session_refresh": [{"status_code": 302}], "baseline_status_code": 404}`
	checks := []struct{ name, got, want string }{
		{"http 观察", text(`SELECT content->>'repro' FROM exploration_node WHERE id = $1`, obsHTTP), `{"kind": "http", "assert": {}, "recipe": {}}`},
		{"陈述原文不动", text(`SELECT content->>'statement' FROM exploration_node WHERE id = $1`, obsHTTP), `写着 "domain": "web" 的陈述`},
		{"steps 观察", text(`SELECT content->'repro'->>'kind' FROM exploration_node WHERE id = $1`, obsSteps), `steps`},
		{"指纹作废", text(`SELECT metadata::text FROM exploration_node WHERE id = $1`, result), `{"task_id": "t"}`},
		{"审计配方", text(`SELECT repro->>'kind' FROM exploration_verification WHERE id = 'v1'`), `http`},
		{"审计证据", text(`SELECT evidence::text FROM exploration_verification WHERE id = 'v1'`), wantEvidence},
		{"台账配方", text(`SELECT repro->>'kind' FROM finding WHERE id = $1::uuid`, findingID), `steps`},
		{"台账证据", text(`SELECT evaluation->'evidence'::text FROM finding WHERE id = $1::uuid`, findingID), wantEvidence},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s = %s，期望 %s", c.name, c.got, c.want)
		}
	}
}
