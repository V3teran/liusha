package evaluator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"testing"

	"github.com/rs/zerolog"

	"github.com/V3teran/liusha/internal/bus"
	"github.com/V3teran/liusha/internal/explorationgraph"
	"github.com/V3teran/liusha/internal/framework/core"
)

// TestParseObservationToAttempt_TolerantOfMalformedRepro 锁定双路径契约一致：
// repro 归一化失败（如 generic 域缺 assert）时轮询路径保留原始配方继续派发，
// 而不是整条丢弃——此前该假设永远 unverified：每 5s 重解析重告警（实测同一
// 节点连告 39 次），且 hasPendingVerdicts 永真阻塞任务收敛。
func TestParseObservationToAttempt_TolerantOfMalformedRepro(t *testing.T) {
	a := NewAgent(AgentConfig{Logger: zerolog.New(io.Discard)})

	raw := json.RawMessage(`{"domain":"generic","recipe":{"steps":"1. 登录 2. 提交 payload 3. 断言回显"}}`) // 缺 assert
	obs := explorationgraph.Node{
		ID:      "obs-bad-repro",
		TaskID:  "t1",
		Kind:    core.KindObservation,
		Content: appendJSONField(t, `{"statement":"S 存在存储型 XSS"}`, "repro", raw),
	}

	att, err := a.parseObservationToAttempt(obs)
	if err != nil {
		t.Fatalf("归一化失败不应整条丢弃（由裁决门终局）: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(att.Primitives, &got); err != nil {
		t.Fatalf("原始配方应原样保留: %v", err)
	}
	if got["domain"] != "generic" {
		t.Fatalf("配方内容不应被改写: %s", att.Primitives)
	}
}

// TestRecordVerifyFailure_SplitsAdjudicationFromRecipe 锁定耗尽出圈的语义
// 分流：裁决官侧失败（ErrAdjudicationUnavailable——LLM 调用/输出解析反复
// 失败）标 adjudication_failed，配方确定性缺陷才标 invalid_recipe。混标会把
// 机器重放全过、主张为真的方向误判成配方作废（实测真漏洞因此出圈）。
func TestRecordVerifyFailure_SplitsAdjudicationFromRecipe(t *testing.T) {
	cases := []struct {
		name       string
		cause      error
		wantReason string
	}{
		{
			name:       "裁决官不可用 → adjudication_failed",
			cause:      fmt.Errorf("%w: verifier: LLM 裁决失败: judge: 解析裁决失败: unexpected end of JSON input", ErrAdjudicationUnavailable),
			wantReason: RefuteAdjudicationFailed,
		},
		{
			name:       "配方确定性缺陷 → invalid_recipe",
			cause:      errors.New("归一化 repro 失败: repro 解析失败: json: cannot unmarshal array"),
			wantReason: RefuteInvalidRecipe,
		},
		{
			name:       "普通门错误（非配方非裁决） → invalid_recipe 维持旧语义",
			cause:      errors.New("promote: 复现执行失败: 网络分区"),
			wantReason: RefuteInvalidRecipe,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			memGraph := explorationgraph.NewMemoryStore()
			b := bus.New(context.Background())
			a := NewAgent(AgentConfig{
				TaskID: "t1", EventBus: b, Graph: memGraph,
				Logger: zerolog.New(io.Discard),
			})
			unverified := explorationgraph.ConfidenceUnverified
			obs := explorationgraph.Node{
				ID: "obs-x", TaskID: "t1", Kind: core.KindObservation,
				Content:    json.RawMessage(`{"statement":"S","repro":{"domain":"generic","recipe":{"steps":"s"},"assert":{"description":"d"}}}`),
				Confidence: &unverified,
			}
			if _, err := memGraph.CreateNode(context.Background(), obs); err != nil {
				t.Fatalf("建观察失败: %v", err)
			}

			attempt := Attempt{TaskID: "t1", NodeID: "obs-x", Primitives: obs.Content}
			for i := 0; i < maxVerifyAttempts; i++ {
				a.recordVerifyFailure(context.Background(), "obs-x", attempt, tc.cause)
			}

			node, err := memGraph.GetNode(context.Background(), "obs-x")
			if err != nil {
				t.Fatalf("读回节点失败: %v", err)
			}
			var m struct {
				RefuteReason string `json:"refute_reason"`
				Outcome      string `json:"verification_outcome"`
			}
			if err := json.Unmarshal(node.Metadata, &m); err != nil {
				t.Fatalf("解析 metadata: %v", err)
			}
			if m.RefuteReason != tc.wantReason || m.Outcome != tc.wantReason {
				t.Fatalf("refute_reason=%q outcome=%q, 期望 %q", m.RefuteReason, m.Outcome, tc.wantReason)
			}
		})
	}
}

// appendJSONField 把字段拼进 JSON 对象载荷（测试构造用，不做完整校验）。
func appendJSONField(t *testing.T, obj string, key string, val json.RawMessage) json.RawMessage {
	t.Helper()
	var m map[string]json.RawMessage
	if err := json.Unmarshal([]byte(obj), &m); err != nil {
		t.Fatalf("构造载荷失败: %v", err)
	}
	m[key] = val
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("构造载荷失败: %v", err)
	}
	return b
}

// 编译期锁定：哨兵错误必须可被 errors.Is 从 executeLLMJudge 的包装链穿透。
func TestErrAdjudicationUnavailableWrapping(t *testing.T) {
	wrapped := fmt.Errorf("outer: %w", fmt.Errorf("%w: inner detail", ErrAdjudicationUnavailable))
	if !errors.Is(wrapped, ErrAdjudicationUnavailable) {
		t.Fatal("包装链必须保留哨兵可辨识性")
	}
}
