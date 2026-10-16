package evaluator

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/V3teran/liusha/internal/explorationgraph"
)

// validateAttempt 验证 Attempt 的必填字段
func (v *PromotionEvaluator) validateAttempt(a Attempt) error {
	if a.TaskID == "" {
		return fmt.Errorf("verifier: Attempt.TaskID 必填")
	}
	if a.Kind == "" {
		return fmt.Errorf("verifier: Attempt.Kind 必填")
	}
	if a.NodeID == "" {
		return fmt.Errorf("verifier: Attempt.NodeID 必填（审计链与审判标记都要指向被裁决的假设节点）")
	}
	if v.replayer == nil {
		return fmt.Errorf("verifier: 无 Replayer，无法复现晋升")
	}
	if v.judge == nil {
		return fmt.Errorf("verifier: 无 LLM 裁决器，无法晋升（机器不持判定权）")
	}
	return nil
}

// createReplayFunction 创建重放闭包，记录完整证据链
func (v *PromotionEvaluator) createReplayFunction(a Attempt, replays *[]replayRecord) func(context.Context, string) (Result, error) {
	return func(rctx context.Context, phase string) (Result, error) {
		res, err := v.replayer.Replay(rctx, a.Primitives)
		rec := replayRecord{Phase: phase, DurationMs: res.DurationMs}
		if err != nil {
			rec.Error = err.Error()
		} else {
			rec.Evidence = res.Evaluation
		}
		*replays = append(*replays, rec)
		return res, err
	}
}

// executePrerun 执行预跑阶段
func (v *PromotionEvaluator) executePrerun(ctx context.Context, replay func(context.Context, string) (Result, error)) (Result, error) {
	res, err := replay(ctx, "prerun")
	if err != nil {
		return Result{}, fmt.Errorf("verifier: 复现执行失败: %w", err)
	}
	return res, nil
}

// extractHypothesis 从 Attempt 中提取假设陈述
func extractHypothesis(a Attempt) string {
	var hyp string
	var c struct {
		Statement string `json:"statement"`
	}
	if json.Unmarshal(a.Content, &c) == nil {
		hyp = c.Statement
	}
	return hyp
}

// checkMachineGuard 机器护栏：检查断言是否通过
func (v *PromotionEvaluator) checkMachineGuard(
	ctx context.Context,
	a Attempt,
	hyp string,
	res Result,
	replays []replayRecord,
) (*explorationgraph.Node, bool, error) {
	// 机器护栏：executor 声明的断言全部未命中 = 配方声称的特征根本没出现
	// 不存在任何支持坐实的机器证据，直接 refuted、不进 judge
	if !assertPassedInEvidence(res.Evaluation) {
		node, err := v.refuteEarly(ctx, a, hyp, res, replays)
		return node, false, err
	}
	return nil, true, nil
}

// executeLLMJudge 执行 LLM 裁决
func (v *PromotionEvaluator) executeLLMJudge(
	ctx context.Context,
	a Attempt,
	hyp string,
	res Result,
	replay func(context.Context, string) (Result, error),
) (Verdict, error) {
	verdict, err := v.judge.Judge(ctx, hyp, a.Primitives, res.Evaluation,
		func(rctx context.Context) (Result, error) { return replay(rctx, "judge") })
	if err != nil {
		return Verdict{}, fmt.Errorf("verifier: LLM 裁决失败（本次未裁决，不回退机器）: %w", err)
	}
	return verdict, nil
}

// aggregateEvidence 聚合证据：最后一次成功重放的证据为主体（验证档位随审计
// 载荷落库，下游据此区分结论成色）。
func aggregateEvidence(res Result, tier, verdict string, reasoning string, replays []replayRecord) (Result, []byte) {
	final := res
	for _, rec := range replays {
		if rec.Error == "" && len(rec.Evidence) > 0 {
			final = Result{Evaluation: rec.Evidence, DurationMs: rec.DurationMs}
		}
	}
	evaluation := withReplayLog(final.Evaluation, tier, verdict, reasoning, replays)
	return final, evaluation
}

// recordVerification 记录验证结果到审计链。
// node_id 指向被裁决的假设节点——审计链回答"谁被验证了"。
func (v *PromotionEvaluator) recordVerification(
	ctx context.Context,
	a Attempt,
	outcome explorationgraph.VerifyOutcome,
	evaluation []byte,
	durationMs int64,
) (string, error) {
	verID, err := v.graph.RecordVerification(ctx, explorationgraph.Verification{
		ID:         uuid.New().String(),
		TaskID:     a.TaskID,
		NodeID:     a.NodeID,
		Primitives: a.Primitives,
		Outcome:    outcome,
		Evaluation: evaluation,
		DurationMs: durationMs,
		CreatedAt:  time.Now(),
	})
	if err != nil {
		return "", fmt.Errorf("verifier: 记录 verification 失败: %w", err)
	}
	return verID, nil
}

// hypothesisMark 是假设节点审判标记的完整载荷：验证溯源、结论、成色与归属。
type hypothesisMark struct {
	verID        string
	outcome      explorationgraph.VerifyOutcome
	promotedNode string // 本次新建或沿用的 result 节点 ID
	confirmed    bool
	tier         string
	refuteReason string
	duplicateOf  string // 非空 = 语义重复（同指纹确认折叠为既有发现的佐证）
}

// markHypothesisNode 在假设节点上回写审判标记：verification_outcome 记录
// 裁决溯源，confidence 同步流转（unverified → verified/refuted）——
// 轮询方以 confidence=unverified 圈定待裁决集合，两个键必须一致更新。
func (v *PromotionEvaluator) markHypothesisNode(
	ctx context.Context,
	a Attempt,
	m hypothesisMark,
) {
	if a.NodeID == "" {
		return
	}

	confidence := explorationgraph.ConfidenceRefuted
	if m.confirmed {
		confidence = explorationgraph.ConfidenceVerified
	}

	mark := map[string]interface{}{
		"verification_id":      m.verID,
		"verification_outcome": m.outcome,
		"verification_tier":    m.tier,
		"verification_at":      time.Now().Format(time.RFC3339),
		"confidence":           confidence,
	}
	if m.refuteReason != "" {
		mark["refute_reason"] = m.refuteReason
	}
	if m.confirmed && m.promotedNode != "" {
		mark["promoted_node"] = m.promotedNode
	}
	if m.duplicateOf != "" {
		mark["duplicate_of"] = m.duplicateOf
	}

	b, mErr := json.Marshal(mark)
	if mErr != nil {
		return
	}

	if uErr := v.graph.UpdateNodeMetadata(ctx, a.NodeID, b); uErr != nil && v.logger != nil {
		v.logger.Warn().Err(uErr).Str("node_id", a.NodeID).Msg("假设节点审判标记写入失败（不影响裁决）")
	}
}

// createVerifiedNode 创建已验证的 result 节点，并落 observation → result 的
// generates 溯源边——推导链 objective →(generates)→ action →(generates)→
// observation →(generates)→ result 全程可溯，result 不是图孤儿。
// fingerprintKey 非空时随 metadata 落库，作为后续语义去重的对照键。
func (v *PromotionEvaluator) createVerifiedNode(
	ctx context.Context,
	a Attempt,
	nodeID string,
	verID string,
	fingerprintKey string,
) (*explorationgraph.Node, error) {
	verified := explorationgraph.ConfidenceVerified
	node := explorationgraph.Node{
		ID:         nodeID,
		TaskID:     a.TaskID,
		Kind:       a.Kind,
		Content:    a.Content,
		Confidence: &verified,
		Priority:   explorationgraph.Priority(a.Priority),
		SourceType: explorationgraph.SourceEvaluator,
		SourceID:   verID,
		CreatedAt:  time.Now(),
		UpdatedAt:  time.Now(),
	}
	if fingerprintKey != "" {
		if b, err := json.Marshal(map[string]string{fingerprintMetadataKey: fingerprintKey}); err == nil {
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
		if err := v.graph.CreateNodeWithEdges(ctx, node, []explorationgraph.Edge{edge}); err != nil {
			return nil, fmt.Errorf("verifier: 晋升节点失败: %w", err)
		}
		return &node, nil
	}

	if _, err := v.graph.CreateNode(ctx, node); err != nil {
		return nil, fmt.Errorf("verifier: 晋升节点失败: %w", err)
	}

	return &node, nil
}

// writeFindingIfNeeded 写入 finding 表（如果配置了）
func (v *PromotionEvaluator) writeFindingIfNeeded(
	ctx context.Context,
	node explorationgraph.Node,
	a Attempt,
	final Result,
) {
	if v.findings == nil {
		return
	}

	if err := v.writeFinding(ctx, node, a, final); err != nil {
		// finding 写入失败不阻塞晋升（节点已进图），但不能静默吞掉
		if v.logger != nil {
			v.logger.Warn().Err(err).Str("node_id", node.ID).Msg("finding 写入失败（节点已晋升，不影响图状态）")
		}
	}
}
