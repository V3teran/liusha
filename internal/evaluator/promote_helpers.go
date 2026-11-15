package evaluator

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/V3teran/liusha/internal/explorationgraph"
	"github.com/V3teran/liusha/internal/framework/core"
)

// validateClaim 验证 Claim 的必填字段
func (g *PromotionGate) validateClaim(a Claim) error {
	if a.TaskID == "" {
		return fmt.Errorf("verifier: Claim.TaskID 必填")
	}
	if a.NodeID == "" {
		return fmt.Errorf("verifier: Claim.NodeID 必填（审计链与验证标记都要指向被验证的假设节点）")
	}
	if g.judge == nil {
		return fmt.Errorf("verifier: 无 LLM 判定器，无法晋升（机器不持判定权）")
	}
	return nil
}

// createReplayFunction 创建重放闭包，记录完整证据链
func (g *PromotionGate) createReplayFunction(a Claim, replays *[]replayRecord) func(context.Context, string) (Result, error) {
	return func(rctx context.Context, phase string) (Result, error) {
		res, err := g.replay(rctx, a.Repro)
		rec := replayRecord{Phase: phase, DurationMs: res.DurationMs}
		if err != nil {
			rec.Error = err.Error()
		} else {
			rec.Evidence = res.Evidence
		}
		*replays = append(*replays, rec)
		return res, err
	}
}

// executePrerun 执行预跑阶段
func (g *PromotionGate) executePrerun(ctx context.Context, replay func(context.Context, string) (Result, error)) (Result, error) {
	res, err := replay(ctx, "prerun")
	if err != nil {
		return Result{}, fmt.Errorf("verifier: 复现执行失败: %w", err)
	}
	return res, nil
}

// checkMachineGuard 机器护栏：检查断言是否通过
func (g *PromotionGate) checkMachineGuard(
	ctx context.Context,
	a Claim,
	hyp string,
	res Result,
	replays []replayRecord,
) (*explorationgraph.Node, bool, error) {
	// 机器护栏：executor 声明的断言全部未命中 = 配方声称的特征根本没出现
	// 不存在任何支持坐实的机器证据，直接 refuted、不进 judge
	if !assertPassedInEvidence(res.Evidence) {
		node, err := g.refuteEarly(ctx, a, hyp, res, replays)
		return node, false, err
	}
	return nil, true, nil
}

// executeLLMJudge 执行 LLM 判定
func (g *PromotionGate) executeLLMJudge(
	ctx context.Context,
	a Claim,
	hyp string,
	res Result,
	replay func(context.Context, string) (Result, error),
) (Verdict, error) {
	verdict, err := g.judge.Judge(ctx, hyp, a.Repro, res.Evidence,
		func(rctx context.Context) (Result, error) { return replay(rctx, "judge") })
	if err != nil {
		return Verdict{}, fmt.Errorf("%w: verifier: LLM 判定失败（本次未出结论，不回退机器）: %w",
			ErrJudgeUnavailable, err)
	}
	return verdict, nil
}

// aggregateEvidence 聚合证据：最后一次成功重放的证据为主体（验证档位随审计
// 载荷落库，下游据此区分结论成色）。
func aggregateEvidence(res Result, tier, verdict string, reasoning string, replays []replayRecord) (Result, []byte) {
	final := res
	for _, rec := range replays {
		if rec.Error == "" && len(rec.Evidence) > 0 {
			final = Result{Evidence: rec.Evidence, DurationMs: rec.DurationMs}
		}
	}
	evaluation := withReplayLog(final.Evidence, tier, verdict, reasoning, replays)
	return final, evaluation
}

// recordVerification 记录验证结果到审计链。
// node_id 指向被验证的假设节点——审计链回答"谁被验证了"。
func (g *PromotionGate) recordVerification(
	ctx context.Context,
	a Claim,
	outcome explorationgraph.VerificationStatus,
	evaluation []byte,
	durationMs int64,
) (string, error) {
	verID, err := g.graph.RecordVerification(ctx, explorationgraph.Verification{
		ID:         uuid.New().String(),
		TaskID:     a.TaskID,
		NodeID:     a.NodeID,
		Repro:      a.Repro,
		Outcome:    outcome,
		Evidence:   evaluation,
		DurationMs: durationMs,
		CreatedAt:  time.Now(),
	})
	if err != nil {
		return "", fmt.Errorf("verifier: 记录 verification 失败: %w", err)
	}
	return verID, nil
}

// hypothesisMark 是假设节点验证标记的载荷：结论与归属。
// 溯源细节（验证记录 ID/档位/时刻）以 exploration_verification 行为权威事实，
// 节点 metadata 只留结论键——mark 里不重复落指针与时间戳。
type hypothesisMark struct {
	status       explorationgraph.VerificationStatus // confirmed / refuted
	refuteReason string
	duplicateOf  string // 非空 = 语义重复（同指纹确认折叠为既有发现的佐证）
}

// markHypothesisNode 在假设节点上回写验证结论：验证状态流转（pending → confirmed /
// refuted，轮询方以 pending 圈定待验证集合），证伪时附证伪原因。
func (g *PromotionGate) markHypothesisNode(
	ctx context.Context,
	a Claim,
	m hypothesisMark,
) {
	if a.NodeID == "" {
		return
	}

	mark := map[string]interface{}{
		"verification_status": m.status,
	}
	if m.refuteReason != "" {
		mark["refute_reason"] = m.refuteReason
	}
	if m.duplicateOf != "" {
		mark["duplicate_of"] = m.duplicateOf
	}

	b, mErr := json.Marshal(mark)
	if mErr != nil {
		return
	}

	if uErr := g.graph.UpdateNodeMetadata(ctx, a.NodeID, b); uErr != nil && g.logger != nil {
		g.logger.Warn().Err(uErr).Str("node_id", a.NodeID).Msg("假设节点验证标记写入失败（不影响判定）")
	}
}

// createVerifiedNode 创建已验证的 result 节点，并落 observation → result 的
// generates 溯源边——推导链 objective →(generates)→ action →(generates)→
// observation →(generates)→ result 全程可溯，result 不是图孤儿。
// fingerprintKey 非空时随 metadata 落库，作为后续语义去重的对照键。
func (g *PromotionGate) createVerifiedNode(
	ctx context.Context,
	a Claim,
	nodeID string,
	fingerprintKey string,
) (*explorationgraph.Node, error) {
	node := explorationgraph.Node{
		ID:         nodeID,
		TaskID:     a.TaskID,
		Kind:       core.KindResult,
		Content:    a.Content,
		Priority:   explorationgraph.Priority(a.Priority),
		SourceType: explorationgraph.SourceEvaluator,
		CreatedAt:  time.Now(),
		UpdatedAt:  time.Now(),
	}
	if fingerprintKey != "" {
		if b, err := json.Marshal(map[string]string{claimFingerprintMetadataKey: fingerprintKey}); err == nil {
			node.Metadata = b
		}
	}

	if a.NodeID != "" {
		edge := explorationgraph.Edge{
			TaskID:    a.TaskID,
			SrcID:     a.NodeID,
			Rel:       explorationgraph.RelGenerates,
			DstID:     nodeID,
			CreatedAt: time.Now(),
		}
		if err := g.graph.CreateNodeWithEdges(ctx, node, []explorationgraph.Edge{edge}); err != nil {
			return nil, fmt.Errorf("verifier: 晋升节点失败: %w", err)
		}
		return &node, nil
	}

	if _, err := g.graph.CreateNode(ctx, node); err != nil {
		return nil, fmt.Errorf("verifier: 晋升节点失败: %w", err)
	}

	return &node, nil
}
