//go:build integration

package dbtest

import (
	"context"
	"encoding/json"
	"slices"
	"testing"

	"github.com/V3teran/liusha/internal/builtin"
	"github.com/V3teran/liusha/internal/config"
	"github.com/V3teran/liusha/internal/configstore"
	"github.com/V3teran/liusha/internal/settingstore"
)

const migration0201 = "0201_builtin_defaults_layering.up.sql"

// 迁移 0201：老库按旧结构（agent / skill / system_setting 存整份配置）写入「当年出厂值」与
// 「用户改过的值」的混合，迁移后只有改过的字段进入用户层，其余跟随新版出厂值。
func TestMigration0201_SeparatesUserChangesFromDefaults(t *testing.T) {
	ctx := context.Background()
	pool := NewPgPoolBefore(t, migration0201)

	planner, _ := builtin.AgentByCode("planner")
	executor, _ := builtin.AgentByCode("executor")
	evaluator, _ := builtin.AgentByCode("evaluator")
	monitor, _ := builtin.AgentByCode("monitor")
	withoutBrowser := slices.DeleteFunc(slices.Clone(executor.FunctionTools), func(s string) bool { return s == "drive_browser" })

	insertAgent := func(code, desc, prompt string, fn, cli, skills []string, maxIt int, cx string) {
		t.Helper()
		j := func(v []string) string { b, _ := json.Marshal(v); return string(b) }
		if _, err := pool.Exec(ctx, `INSERT INTO agent (code, name, description, system_prompt, function_tools, cli_tools, skills, max_iterations, complexity)
			VALUES ($1, $1, $2, $3, $4::jsonb, $5::jsonb, $6::jsonb, $7, $8)`,
			code, desc, prompt, j(fn), j(cli), j(skills), maxIt, cx); err != nil {
			t.Fatalf("写旧 agent %s: %v", code, err)
		}
	}
	// planner：全部为出厂值（章程首尾多出空白也应识别为出厂值）
	insertAgent("planner", planner.Description, "\n"+planner.Charter+"\n\n", planner.FunctionTools, planner.CliTools, planner.Skills, planner.MaxIterations, planner.Complexity)
	// executor：改了调度摘要，取消了 drive_browser
	insertAgent("executor", "我的执行者", executor.Charter, withoutBrowser, executor.CliTools, executor.Skills, executor.MaxIterations, executor.Complexity)
	// evaluator：档位改为 simple
	insertAgent("evaluator", evaluator.Description, evaluator.Charter, evaluator.FunctionTools, evaluator.CliTools, evaluator.Skills, evaluator.MaxIterations, "simple")
	// monitor：提示词被用户改过
	insertAgent("monitor", monitor.Description, "我自己写的监察提示词", monitor.FunctionTools, monitor.CliTools, monitor.Skills, monitor.MaxIterations, monitor.Complexity)

	bac, _ := builtin.SkillByCode("bac")
	browser, _ := builtin.SkillByCode("browser-use")
	insertSkill := func(code, category, name, desc, body string, builtinRow, enabled bool) {
		t.Helper()
		if _, err := pool.Exec(ctx, `INSERT INTO skill (code, category, name, description, body, is_builtin, enabled)
			VALUES ($1, $2, $3, $4, $5, $6, $7)`, code, category, name, desc, body, builtinRow, enabled); err != nil {
			t.Fatalf("写旧 skill %s: %v", code, err)
		}
	}
	insertSkill("bac", "vuln", bac.Name, bac.Description, "我改过的 BAC 正文", true, false)
	insertSkill("browser-use", "tool", browser.Name, browser.Description, browser.Body, true, true)
	insertSkill("mine", "vuln", "我的技能", "d", "正文", false, true)
	insertSkill("retired-edited", "tool", "已下线但改过", "d", "用户改过的正文", true, true)

	insertSetting := func(group, value string) {
		t.Helper()
		if _, err := pool.Exec(ctx, `INSERT INTO system_setting (group_key, value) VALUES ($1, $2::jsonb)`, group, value); err != nil {
			t.Fatalf("写旧设置 %s: %v", group, err)
		}
	}
	insertSetting("runtime", `{"step_tool_timeout_seconds":1800,"step_llm_timeout_seconds":240,"run_tail_bytes":8192,"tool_output_tail_bytes":32768}`)
	insertSetting("compaction", `{"planner_context_ratio":0.6,"executor_context_ratio":0.5,"evaluator_context_ratio":0.5,"monitor_context_ratio":0.5,"planner_window_messages":40}`)
	insertSetting("proxy_filter", `{"allow_hosts":[],"exclude_methods":["OPTIONS","HEAD","CONNECT"],"exclude_hosts":["a.com"],
		"exclude_upgrade_protocols":["websocket"],"exclude_suffixes":[".css",".js",".png",".jpg",".gif",".ico",".woff",".woff2"],
		"exclude_content_types":[],"exclude_status_codes":[400,401,403,404,405,408,409,410,413,414,415,422,429,500,501,502,503,504],
		"max_request_body_size":2097152,"max_response_body_size":8388608}`)

	ApplyMigrationsFrom(t, pool, migration0201)

	// ── 用户层只含改过的字段 ──────────────────────────────────────────
	fields := func(table, keyCol, key string) map[string]json.RawMessage {
		t.Helper()
		var raw []byte
		err := pool.QueryRow(ctx, "SELECT fields FROM "+table+" WHERE "+keyCol+"=$1", key).Scan(&raw)
		if err != nil {
			return nil
		}
		var m map[string]json.RawMessage
		_ = json.Unmarshal(raw, &m)
		return m
	}
	if f := fields("agent_override", "agent_code", "planner"); f != nil {
		t.Errorf("planner 全为出厂值，不应有自定义: %v", f)
	}
	if f := fields("agent_override", "agent_code", "executor"); len(f) != 2 || !jsonEq(f["description"], `{"value":"我的执行者"}`) ||
		!jsonEq(f["function_tools"], `{"removed":["drive_browser"]}`) {
		t.Errorf("executor 自定义应为调度摘要与取消 drive_browser: %v", f)
	}
	if f := fields("agent_override", "agent_code", "evaluator"); len(f) != 1 || !jsonEq(f["complexity"], `{"value":"simple"}`) {
		t.Errorf("evaluator 自定义应只有档位: %v", f)
	}
	if f := fields("agent_override", "agent_code", "monitor"); f != nil {
		t.Errorf("提示词改动走归档而非自定义: %v", f)
	}

	var archived string
	if err := pool.QueryRow(ctx, "SELECT prompt FROM agent_prompt_archive WHERE agent_code='monitor'").Scan(&archived); err != nil || archived != "我自己写的监察提示词" {
		t.Errorf("monitor 改过的提示词应原文归档: %q %v", archived, err)
	}
	var archives int
	_ = pool.QueryRow(ctx, "SELECT count(*) FROM agent_prompt_archive").Scan(&archives)
	if archives != 1 {
		t.Errorf("只有改过提示词的 monitor 应归档，实际 %d 条", archives)
	}

	if f := fields("skill_override", "skill_code", "bac"); len(f) != 2 || !jsonEq(f["body"], `{"value":"我改过的 BAC 正文"}`) ||
		!jsonEq(f["enabled"], `{"value":false}`) {
		t.Errorf("bac 自定义应为正文与停用: %v", f)
	}
	if f := fields("skill_override", "skill_code", "browser-use"); f != nil {
		t.Errorf("browser-use 未改，不应有自定义: %v", f)
	}
	var customs []string
	rows, _ := pool.Query(ctx, "SELECT code FROM custom_skill ORDER BY code")
	for rows.Next() {
		var c string
		_ = rows.Scan(&c)
		customs = append(customs, c)
	}
	rows.Close()
	if !slices.Equal(customs, []string{"mine", "retired-edited"}) {
		t.Errorf("自建技能 + 已下线但改过的内置技能应进 custom_skill: %v", customs)
	}

	if f := fields("setting_override", "group_key", "runtime"); len(f) != 1 || !jsonEq(f["step_llm_timeout_seconds"], `{"value":240}`) {
		t.Errorf("runtime 自定义应只有 LLM 超时: %v", f)
	}
	if f := fields("setting_override", "group_key", "compaction"); f != nil {
		t.Errorf("compaction 全为出厂值（旧字段丢弃），不应有自定义: %v", f)
	}
	if f := fields("setting_override", "group_key", "proxy_filter"); len(f) != 1 || !jsonEq(f["exclude_hosts"], `{"added":["a.com"]}`) {
		t.Errorf("proxy_filter 自定义应只有新增排除主机: %v", f)
	}

	for _, old := range []string{"agent", "skill", "system_setting"} {
		var exists bool
		_ = pool.QueryRow(ctx, "SELECT to_regclass('public.'||$1) IS NOT NULL", old).Scan(&exists)
		if exists {
			t.Errorf("旧表 %s 应已删除", old)
		}
	}

	// ── 经新的读写入口验证生效值 ──────────────────────────────────────
	cfg := configstore.New(pool, smokeCache(t), configstore.ToolCatalogFrom(nil))
	ex, err := cfg.Agent(ctx, "executor")
	if err != nil {
		t.Fatal(err)
	}
	if ex.Description != "我的执行者" || slices.Contains(ex.FunctionTools, "drive_browser") || ex.Charter != executor.Charter {
		t.Errorf("executor 生效值: desc=%q tools=%v 章程跟随出厂=%v", ex.Description, ex.FunctionTools, ex.Charter == executor.Charter)
	}
	mon, _ := cfg.Agent(ctx, "monitor")
	if mon.Charter != monitor.Charter {
		t.Error("monitor 生效章程应为新版出厂章程（旧提示词已归档）")
	}
	detail, _ := cfg.AgentDetail(ctx, "monitor")
	if detail.PromptArchive == nil {
		t.Error("monitor 详情应带提示词归档")
	}
	sk, _ := cfg.Skill(ctx, "bac")
	if sk.Body != "我改过的 BAC 正文" || sk.Enabled || sk.Name != bac.Name {
		t.Errorf("bac 生效值: %+v", sk)
	}

	defaults := config.AppConfig{}
	defaults.ApplyDefaults()
	set := settingstore.New(pool, smokeCache(t), settingstore.DefaultsFrom(defaults))
	rt, _ := set.Runtime(ctx)
	if rt.StepLLMTimeoutSeconds != 240 || rt.RunTailBytes != 8192 {
		t.Errorf("runtime 生效值: %+v", rt)
	}
	pf, _ := set.ProxyFilter(ctx)
	if !slices.Contains(pf.ExcludeHosts, "a.com") {
		t.Errorf("proxy_filter 生效值应含新增排除主机: %+v", pf.ExcludeHosts)
	}
}

// jsonEq 按语义比较 JSON（jsonb 输出的空白、键序与源字面量不同）。
func jsonEq(raw json.RawMessage, want string) bool {
	var a, b any
	if json.Unmarshal(raw, &a) != nil || json.Unmarshal([]byte(want), &b) != nil {
		return false
	}
	ja, _ := json.Marshal(a)
	jb, _ := json.Marshal(b)
	return string(ja) == string(jb)
}
