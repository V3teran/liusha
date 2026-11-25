//go:build integration

package dbtest

import (
	"context"
	"encoding/json"
	"testing"
)

const migration0257 uint = 257

// 迁移 0257：内置函数工具名统一成「对象_动词」。工具名不只出现在出厂名单里，还是存量数据与
// 用户配置里的字符串，迁移把它们一并改写：
//   - agent_override：用户在智能体页改过的名单（added / removed 按工具名记）与整值。不改的话，
//     用户当初去掉的工具会回来（旧名对不上新出厂名单，减不掉）、加上的会是个不存在的名字；
//   - tool_invocation.tool_name / message.metadata.tool_name：工具调用记录与会话时间线，
//     不改的话历史任务页面上显示的是已经不存在的工具名。
//
// 恰好同形但不是工具名的文本不改：沙盒里 browser-use 命令报错原文里的事件名
// drive_browser.browser…（Go 代码靠匹配它识别浏览器退化）。
func TestMigration0257_ToolNames(t *testing.T) {
	ctx := context.Background()
	pool := NewPgPoolBefore(t, migration0257)

	// agent_override 只属于四个内置智能体（表上有 CHECK），名单字段的自定义一律是 added / removed。
	seed := []string{
		// 执行者：去掉了 drive_browser、加了 search_corpus（旧名）。不改写的话，去掉的工具会回来
		// （旧名对不上新出厂名单，减不掉），加上的会是个不存在的名字
		`INSERT INTO agent_override (agent_code, fields) VALUES ('executor', '{"function_tools":{"removed":["drive_browser"],"added":["search_corpus"]}}')`,
		// 评估者：去掉的名字里有内置工具、也有 CLI 工具名（不是函数工具，不该被动）与通配
		`INSERT INTO agent_override (agent_code, fields) VALUES ('evaluator', '{"cli_tools":{"removed":["nmap","run_command"]},"skills":{"added":["bac"]}}')`,
		// 与工具名无关的自定义：原样不动
		`INSERT INTO agent_override (agent_code, fields) VALUES ('planner', '{"complexity":{"value":"simple"},"charter":{"value":"必要时用 run_command 跑命令。Event handler drive_browser.browser.watchdog_base timed out"}}')`,
	}
	for _, q := range seed {
		if _, err := pool.Exec(ctx, q); err != nil {
			t.Fatalf("写旧数据 %s: %v", q, err)
		}
	}
	// 工具调用记录与会话时间线（挂在任务下，造一个任务承载）
	assignment := SeedAssignment(t, pool)
	var taskID string
	if err := pool.QueryRow(ctx, `INSERT INTO task (assignment_id, goal) VALUES ($1, 'test') RETURNING id`, assignment).Scan(&taskID); err != nil {
		t.Fatal(err)
	}
	// 工具调用记录归属一次运行（agent_run_id 是必填的外键）
	var runID string
	if err := pool.QueryRow(ctx, `INSERT INTO agent_run (task_id, status) VALUES ($1, 'completed') RETURNING id`, taskID).Scan(&runID); err != nil {
		t.Fatal(err)
	}
	// 一条旧名、一条已是新名、一条不是内置函数工具
	if _, err := pool.Exec(ctx, `INSERT INTO tool_invocation (agent_run_id, task_id, tool_name)
		VALUES ($1, $2, 'drive_browser'), ($1, $2, 'browser_drive'), ($1, $2, 'nmap')`, runID, taskID); err != nil {
		t.Fatalf("写工具调用记录: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO conversation (id, task_id) VALUES (gen_random_uuid(), $1)`, taskID); err != nil {
		t.Fatal(err)
	}
	var convID string
	if err := pool.QueryRow(ctx, `SELECT id FROM conversation WHERE task_id = $1`, taskID).Scan(&convID); err != nil {
		t.Fatal(err)
	}
	insertMsg := func(metadata string) {
		t.Helper()
		if _, err := pool.Exec(ctx, `INSERT INTO message (conversation_id, role, kind, content, metadata)
			VALUES ($1, 'assistant', 'event', '', $2::jsonb)`, convID, metadata); err != nil {
			t.Fatalf("写时间线消息 %s: %v", metadata, err)
		}
	}
	insertMsg(`{"kind":"tool_call","agent_name":"executor","tool_name":"run_command","call_id":"c1","args":"curl -s http://x/"}`)
	insertMsg(`{"kind":"tool_result","agent_name":"executor","tool_name":"run_command","call_id":"c1","result":"ok"}`)
	insertMsg(`{"kind":"reasoning","agent_name":"planner","text":"用 run_command 与 drive_browser","thinking":"Event handler drive_browser.browser.watchdog_base timed out"}`)

	MigrateTo(t, pool, migration0257)

	// jsonb 不保证键序与空白：按 JSON 语义比较，不比字符串
	fields := func(agent string) json.RawMessage {
		t.Helper()
		var raw []byte
		if err := pool.QueryRow(ctx, `SELECT fields FROM agent_override WHERE agent_code = $1`, agent).Scan(&raw); err != nil {
			t.Fatal(err)
		}
		return raw
	}
	overrides := map[string]json.RawMessage{}
	for _, agent := range []string{"executor", "evaluator", "planner"} {
		overrides[agent] = fields(agent)
	}

	// added / removed：成员按工具名精确改写
	if got := overrides["executor"]; !jsonEq(got, `{"function_tools":{"added":["corpus_search"],"removed":["browser_drive"]}}`) {
		t.Errorf("执行者的名单自定义应改成新工具名，得 %s", got)
	}
	// 只改函数工具名：CLI 工具名单里恰好叫 run_command 的不是内置函数工具，原样保留；别的名单不动
	if got := overrides["evaluator"]; !jsonEq(got, `{"cli_tools":{"removed":["nmap","run_command"]},"skills":{"added":["bac"]}}`) {
		t.Errorf("CLI 工具与技能的名单不应被改动，得 %s", got)
	}
	// 与名单无关的字段不动：哪怕正文里出现了旧工具名，那是用户写的散文，不是工具名的引用；
	// 外部系统的报错原文更要原样保留
	if got := overrides["planner"]; !jsonEq(got, `{"complexity":{"value":"simple"},"charter":{"value":"必要时用 run_command 跑命令。Event handler drive_browser.browser.watchdog_base timed out"}}`) {
		t.Errorf("章程与档位的自定义不应被改动，得 %s", got)
	}

	names := func(q string, args ...any) []string {
		t.Helper()
		rows, err := pool.Query(ctx, q, args...)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var out []string
		for rows.Next() {
			var s string
			if err := rows.Scan(&s); err != nil {
				t.Fatal(err)
			}
			out = append(out, s)
		}
		return out
	}
	got := names(`SELECT tool_name FROM tool_invocation WHERE task_id = $1 ORDER BY tool_name`, taskID)
	if len(got) != 3 || got[0] != "browser_drive" || got[1] != "browser_drive" || got[2] != "nmap" {
		t.Errorf("工具调用记录的 tool_name 应改写为：browser_drive ×2、nmap，得 %v", got)
	}
	got = names(`SELECT metadata->>'tool_name' FROM message WHERE conversation_id = $1 AND metadata ? 'tool_name' ORDER BY seq`, convID)
	if len(got) != 2 || got[0] != "command_run" || got[1] != "command_run" {
		t.Errorf("时间线事件的 tool_name 应改写为 command_run ×2，得 %v", got)
	}
	var text, thinking string
	if err := pool.QueryRow(ctx, `SELECT metadata->>'text', metadata->>'thinking' FROM message WHERE metadata->>'kind' = 'reasoning'`).Scan(&text, &thinking); err != nil {
		t.Fatal(err)
	}
	// reasoning 的正文与思考是模型写的散文，不是工具名的引用：不改
	if text != "用 run_command 与 drive_browser" || thinking != "Event handler drive_browser.browser.watchdog_base timed out" {
		t.Errorf("时间线的散文与外部报错原文不应被改动，得 text=%q thinking=%q", text, thinking)
	}
}

// 没改过函数工具名单的部署：迁移不碰它们的任何一行（没有 function_tools 自定义、或该字段为空）。
// 这条迁移改写的是用户配置，对没改过的用户必须是零影响。
func TestMigration0257_LeavesUntouchedListsAlone(t *testing.T) {
	ctx := context.Background()
	pool := NewPgPoolBefore(t, migration0257)
	for _, q := range []string{
		`INSERT INTO agent_override (agent_code, fields) VALUES ('executor', '{"complexity":{"value":"simple"}}')`,
		`INSERT INTO agent_override (agent_code, fields) VALUES ('monitor', '{"function_tools":{}}')`,
	} {
		if _, err := pool.Exec(ctx, q); err != nil {
			t.Fatalf("写旧数据 %s: %v", q, err)
		}
	}

	MigrateTo(t, pool, migration0257)

	for agent, want := range map[string]string{
		"executor": `{"complexity":{"value":"simple"}}`,
		"monitor":  `{"function_tools":{}}`,
	} {
		var raw []byte
		if err := pool.QueryRow(ctx, `SELECT fields FROM agent_override WHERE agent_code = $1`, agent).Scan(&raw); err != nil {
			t.Fatal(err)
		}
		if !jsonEq(raw, want) {
			t.Errorf("%s 没有函数工具名单的自定义，迁移后应原样不动，得 %s", agent, raw)
		}
	}
	// 迁移自己建的临时函数不留在库里
	var leftover int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM pg_proc WHERE proname = 'rename_tool'`).Scan(&leftover); err != nil {
		t.Fatal(err)
	}
	if leftover != 0 {
		t.Errorf("迁移用的辅助函数不应留在库里，得 %d 个", leftover)
	}
}

// 回退：三处的工具名改回旧名（前提是程序也退回改名的版本）。
func TestMigration0257_Down(t *testing.T) {
	ctx := context.Background()
	pool := NewPgPoolBefore(t, migration0257)
	MigrateTo(t, pool, migration0257)
	for _, q := range []string{
		`INSERT INTO agent_override (agent_code, fields) VALUES ('executor', '{"function_tools":{"added":["corpus_search"],"removed":["browser_drive"]}}')`,
	} {
		if _, err := pool.Exec(ctx, q); err != nil {
			t.Fatal(err)
		}
	}

	MigrateTo(t, pool, migration0257-1)

	var raw []byte
	if err := pool.QueryRow(ctx, `SELECT fields FROM agent_override WHERE agent_code = 'executor'`).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if !jsonEq(raw, `{"function_tools":{"added":["search_corpus"],"removed":["drive_browser"]}}`) {
		t.Errorf("回退后名单自定义应改回旧工具名，得 %s", raw)
	}
}
