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
	"github.com/V3teran/liusha/internal/framework/llm"
	"github.com/V3teran/liusha/internal/httpreplay"
)

// TestClaimFromHypothesis_TolerantOfMalformedRepro 锁定双路径契约一致：
// repro 归一化失败（如 generic 域缺 assert）时轮询路径保留原始配方继续派发，
// 而不是整条丢弃——此前该假设永远待验证：每 5s 重解析重告警（实测同一
// 节点连告 39 次），且 hasPendingVerdicts 永真阻塞任务收敛。
func TestClaimFromHypothesis_TolerantOfMalformedRepro(t *testing.T) {
	a := NewAgent(AgentConfig{Logger: zerolog.New(io.Discard)})

	raw := json.RawMessage(`{"domain":"generic","recipe":{"steps":"1. 登录 2. 提交 payload 3. 断言回显"}}`) // 缺 assert
	obs := explorationgraph.Node{
		ID:      "obs-bad-repro",
		TaskID:  "t1",
		Kind:    core.KindObservation,
		Content: appendJSONField(t, `{"statement":"S 存在存储型 XSS"}`, "repro", raw),
	}

	att, err := a.claimFromHypothesis(obs)
	if err != nil {
		t.Fatalf("归一化失败不应整条丢弃（由复现门终局）: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(att.Repro, &got); err != nil {
		t.Fatalf("原始配方应原样保留: %v", err)
	}
	if got["domain"] != "generic" {
		t.Fatalf("配方内容不应被改写: %s", att.Repro)
	}
}

// TestRecordVerifyFailure_SplitsJudgeFromRecipe 锁定耗尽出圈的语义
// 分流：评估者侧失败（ErrJudgeUnavailable——LLM 调用/输出解析反复
// 失败）标 judge_unavailable，配方确定性缺陷才标 invalid_recipe。混标会把
// 机器重放全过、主张为真的方向误判成配方作废（实测真漏洞因此出圈）。
func TestRecordVerifyFailure_SplitsJudgeFromRecipe(t *testing.T) {
	cases := []struct {
		name       string
		cause      error
		wantReason string
	}{
		{
			name:       "评估者不可用 → judge_unavailable",
			cause:      fmt.Errorf("%w: verifier: LLM 判定失败: judge: 解析判定失败: unexpected end of JSON input", ErrJudgeUnavailable),
			wantReason: RefuteJudgeUnavailable,
		},
		{
			name:       "配方确定性缺陷 → invalid_recipe",
			cause:      errors.New("归一化 repro 失败: repro 解析失败: json: cannot unmarshal array"),
			wantReason: RefuteInvalidRecipe,
		},
		{
			// 目标不可达（重放没拿到响应）：计入上限、耗尽按复放失败出圈。曾被当作 LLM
			// provider 瞬时故障无限豁免——目标宕机时假设永远待验证，任务不收敛且持续打目标。
			name: "重放未到达目标（EOF） → not_reproducible",
			cause: fmt.Errorf("promote: verifier: 复现执行失败: web.Replayer: 基线重放失败: %w",
				fmt.Errorf("%w: %w", httpreplay.ErrTransport, io.EOF)),
			wantReason: RefuteNotReproducible,
		},
		{
			name: "重放超时（目标挂起） → not_reproducible",
			cause: fmt.Errorf("web.Replayer: 重发失败: %w",
				fmt.Errorf("%w: Get \"http://t/\": context deadline exceeded (Client.Timeout exceeded while awaiting headers)", httpreplay.ErrTransport)),
			wantReason: RefuteNotReproducible,
		},
		{
			name:       "普通门错误（非配方非判定） → invalid_recipe",
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
			pending := explorationgraph.VerificationPending
			obs := explorationgraph.Node{
				ID: "obs-x", TaskID: "t1", Kind: core.KindObservation,
				Content:            json.RawMessage(`{"statement":"S","repro":{"domain":"generic","recipe":{"steps":"s"},"assert":{"description":"d"}}}`),
				VerificationStatus: &pending,
			}
			if _, err := memGraph.CreateNode(context.Background(), obs); err != nil {
				t.Fatalf("建观察失败: %v", err)
			}

			attempt := Claim{TaskID: "t1", NodeID: "obs-x", Repro: obs.Content}
			for i := 0; i < maxVerifyAttempts; i++ {
				a.recordVerifyFailure(context.Background(), "obs-x", attempt, tc.cause)
			}

			node, err := memGraph.GetNode(context.Background(), "obs-x")
			if err != nil {
				t.Fatalf("读回节点失败: %v", err)
			}
			// 出圈即证伪：验证状态与其它证伪路径同为 refuted（规划器据此看到它），
			// 出圈原因只在 refute_reason。
			if !node.HasVerificationStatus(explorationgraph.VerificationRefuted) || node.RefuteReason() != tc.wantReason {
				t.Fatalf("status=%v refute_reason=%q, 期望 refuted / %q", node.VerificationStatus, node.RefuteReason(), tc.wantReason)
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
func TestErrJudgeUnavailableWrapping(t *testing.T) {
	wrapped := fmt.Errorf("outer: %w", fmt.Errorf("%w: inner detail", ErrJudgeUnavailable))
	if !errors.Is(wrapped, ErrJudgeUnavailable) {
		t.Fatal("包装链必须保留哨兵可辨识性")
	}
}

// LLM 配置错误：不计入单条假设的重试阶梯（否则每条假设都被误标 judge_unavailable
// 出圈），而是上报为 evaluator 致命错误，由 Run 返回终结任务。
func TestRecordVerifyFailure_ConfigErrorIsFatalNotPerHypothesis(t *testing.T) {
	a := NewAgent(AgentConfig{Logger: zerolog.New(io.Discard)})
	cfgErr := fmt.Errorf("%w: verifier: LLM 判定失败: %w", ErrJudgeUnavailable, &llm.HTTPError{Code: 401})
	a.recordVerifyFailure(context.Background(), "obs-1", Claim{}, cfgErr)

	a.verifyAttemptsMu.Lock()
	n := a.verifyAttempts["obs-1"]
	a.verifyAttemptsMu.Unlock()
	if n != 0 {
		t.Fatalf("配置错误不应计入假设失败次数，得 %d", n)
	}
	select {
	case err := <-a.fatal:
		if !errors.Is(err, cfgErr) {
			t.Fatalf("致命通道应携带配置错误，得 %v", err)
		}
	default:
		t.Fatal("配置错误应上报到致命通道")
	}
}

// TestRecordVerifyFailure_CanceledReplayNotCounted：任务停止时在途重放因取消失败，
// 不是目标不可达，不得计入上限把假设标成复放失败出圈。
func TestRecordVerifyFailure_CanceledReplayNotCounted(t *testing.T) {
	memGraph := explorationgraph.NewMemoryStore()
	a := NewAgent(AgentConfig{TaskID: "t1", EventBus: bus.New(context.Background()), Graph: memGraph, Logger: zerolog.New(io.Discard)})
	pending := explorationgraph.VerificationPending
	obs := explorationgraph.Node{ID: "obs-c", TaskID: "t1", Kind: core.KindObservation, Content: json.RawMessage(`{"statement":"S"}`), VerificationStatus: &pending}
	if _, err := memGraph.CreateNode(context.Background(), obs); err != nil {
		t.Fatalf("建观察失败: %v", err)
	}
	cause := fmt.Errorf("web.Replayer: 重发失败: %w", fmt.Errorf("%w: %w", httpreplay.ErrTransport, context.Canceled))
	for i := 0; i < maxVerifyAttempts+1; i++ {
		a.recordVerifyFailure(context.Background(), "obs-c", Claim{TaskID: "t1", NodeID: "obs-c"}, cause)
	}
	node, err := memGraph.GetNode(context.Background(), "obs-c")
	if err != nil {
		t.Fatalf("读回节点失败: %v", err)
	}
	if len(node.Metadata) > 0 && string(node.Metadata) != "null" && string(node.Metadata) != "{}" {
		var m map[string]any
		_ = json.Unmarshal(node.Metadata, &m)
		if m["refute_reason"] != nil {
			t.Fatalf("取消不应计入上限，得 metadata=%s", node.Metadata)
		}
	}
}
