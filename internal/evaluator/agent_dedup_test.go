package evaluator

import (
	"encoding/json"
	"fmt"
	"io"
	"testing"

	"github.com/rs/zerolog"

	"github.com/V3teran/liusha/internal/explorationgraph"
	"github.com/V3teran/liusha/internal/framework/core"
)

// TestAttemptHashKeysOnStatementAndRecipe 锁定裁决键语义：裁决对象是"假设+配方"，
// 同配方不同陈述是两次独立裁决，不得沿用结论（历史 bug：纯配方哈希让"XSS"和
// "SQLi"共享同一配方时互相继承裁决）。
func TestAttemptHashKeysOnStatementAndRecipe(t *testing.T) {
	recipe := json.RawMessage(`{"domain":"web","recipe":{"request":{"method":"GET","url":"http://t/search?q=1"}},"assert":{"body_contains":["leaked"]}}`)

	xss := Attempt{Content: json.RawMessage(`{"summary":"/search 存在反射型 XSS"}`), Primitives: recipe}
	sqli := Attempt{Content: json.RawMessage(`{"summary":"/search 存在 SQL 注入"}`), Primitives: recipe}
	sameAsXSS := Attempt{Content: json.RawMessage(`{"summary":"/search 存在反射型 XSS","severity":"high"}`), Primitives: recipe}

	if attemptHash(xss) == attemptHash(sqli) {
		t.Fatal("同配方不同陈述不得同键：会跨假设沿用裁决")
	}
	if attemptHash(xss) != attemptHash(sameAsXSS) {
		t.Fatal("同陈述同配方（载荷多无关字段）应同键")
	}
}

// TestParseObservationToAttemptContentShape 锁定轮询路径与事件路径的载荷形状
// 一致：summary/severity/confidence 三字段齐全，缺省 confidence=low。
func TestParseObservationToAttemptContentShape(t *testing.T) {
	a := NewAgent(AgentConfig{Logger: zerolog.New(io.Discard)})

	obs := explorationgraph.Node{
		ID:      "obs-1",
		TaskID:  "task-1",
		Kind:    core.KindObservation,
		Content: json.RawMessage(`{"statement":"S 存在漏洞","severity":"high","confidence":"high","repro":{"domain":"generic","recipe":{"steps":["curl x"]},"assert":{"description":"d"}}}`),
	}
	att, err := a.parseObservationToAttempt(obs)
	if err != nil {
		t.Fatalf("parseObservationToAttempt: %v", err)
	}
	var c map[string]string
	if err := json.Unmarshal(att.Content, &c); err != nil {
		t.Fatalf("unmarshal content: %v", err)
	}
	if c["summary"] != "S 存在漏洞" || c["severity"] != "high" || c["confidence"] != "high" {
		t.Fatalf("载荷字段缺失/错值: %+v", c)
	}

	// 缺省 confidence → low（与 executor 侧 observationToAttempt 同口径）
	obs.Content = json.RawMessage(`{"statement":"S2","repro":{"domain":"generic","recipe":{"steps":["curl x"]},"assert":{"description":"d"}}}`)
	att, err = a.parseObservationToAttempt(obs)
	if err != nil {
		t.Fatalf("parseObservationToAttempt: %v", err)
	}
	c = map[string]string{}
	if err := json.Unmarshal(att.Content, &c); err != nil {
		t.Fatalf("unmarshal content: %v", err)
	}
	if c["confidence"] != "low" {
		t.Fatalf("缺省 confidence 应为 low: %+v", c)
	}
}

// TestIsDeterministicRecipeError 锁定 fail-fast 语义：确定性配方缺陷（形状/
// 缺字段）重试无意义，应直接终局出圈；网络类错误不在其列。
func TestIsDeterministicRecipeError(t *testing.T) {
	deterministic := []string{
		"verifier: 复现执行失败: Replayer: 解析域信封失败: json: cannot unmarshal array into Go value of type executor.ReproEnvelope",
		"web.Replayer: 解析 assert 失败: json: cannot unmarshal array into Go struct field .header_contains",
		"observation 缺少 repro 字段",
		"Replayer: 未知域 \"xss\"（已支持: web, generic）",
		"web.Replayer: request 非法——url 必须是完整 URL（含 http:// 或 https://），当前 \"x\"",
	}
	for _, s := range deterministic {
		if !isDeterministicRecipeError(fmt.Errorf("%s", s)) {
			t.Errorf("应判为确定性缺陷: %s", s)
		}
	}
	transient := []string{
		`web.Replayer: 基线重放失败: replay 请求失败: Get "http://t": dial tcp: lookup t: no such host`,
		"verification timeout (will retry): context deadline exceeded",
		"http 429: Too Many Requests",
	}
	for _, s := range transient {
		if isDeterministicRecipeError(fmt.Errorf("%s", s)) {
			t.Errorf("不应判为确定性缺陷: %s", s)
		}
	}
}
