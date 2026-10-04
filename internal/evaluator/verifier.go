// Package evaluator 实现本包职责；细节见文件级注释。

// verifier.go 实现认知循环的复现晋升门（Promotion Evaluator）。
//
// 探索图写权限不变式（4 种节点、谁写什么）：
//   - planner 写 objective / action
//   - executor 写 observation（假设 / 执行记录 / executor 侧证据）
//   - evaluator 只写 result——本文件就是 observation(假设) → result(坐实) 的唯一状态转换门
//
//	Observation(在途假设，图节点，confidence=unverified)
//	    → Promote(attempt)
//	    → 域 Replayer 复放采证（primitives 对本包是不透明 blob，形状归各域）
//	    → LLMJudge 终裁（机器采证，LLM 持判定权）
//	    → RecordVerification(confirmed/refuted)  // 审计链：replays[] 含预跑+裁决官每次复放
//	    → confirmed: CreateNode(confidence=verified) 进图 + 写 finding 表
//	    └ refuted:   不进图（证据留 exploration_verification 供审计）
//
// domain-agnostic：配方怎么执行归各域（web=HTTP 重放、generic=裁决官自主执行、
// 未来 binary/cloud/lateral=各自 Replayer），Evaluator 只认 Replayer 接口——加新域
// 晋升门零改动。

package evaluator

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"github.com/V3teran/liusha/internal/explorationgraph"
	"github.com/V3teran/liusha/internal/finding"
	"github.com/V3teran/liusha/internal/framework/core"
)

// graphWriter 是 Evaluator 依赖的探索图写入子集：收窄依赖 + 便于测试替身。
// *explorationgraph.Store 自动满足本接口。
//
// 图的写权限不变式：planner 写 objective/action，executor 写 observation，
// evaluator 只**新建** result（晋升）；对既有假设节点仅做 metadata 审判标记
// （verification_outcome），不新建节点。复放证据不走图——落 exploration_verification 审计链。
type graphWriter interface {
	RecordVerification(ctx context.Context, v explorationgraph.Verification) (string, error)
	CreateNode(ctx context.Context, n explorationgraph.Node) (string, error)
	UpdateNodeMetadata(ctx context.Context, id string, metadata json.RawMessage) error
}

// findingWriter 是 Evaluator 依赖的 finding 写入子集：验证通过后才能写入 finding 表。
// *finding.Store 自动满足本接口。
type findingWriter interface {
	Save(ctx context.Context, f finding.VulnFinding) (finding.VulnFinding, error)
}

// Replayer 是 domain-specific 复现执行器。Evaluator 把"复现"委托给它，自身不碰域细节。
// primitives 是要回放的 L1 原语序列（形状由域定义）；返回是否坐实 + 证据。
type Replayer interface {
	Replay(ctx context.Context, primitives json.RawMessage) (Result, error)
}

// Result 是一次复现采集的客观证据（机器不判定坐实——语义裁决权全在 LLMJudge）。
type Result struct {
	Evaluation json.RawMessage // 基线/攻击双对照证据 + executor 声明预期的核验明细（参考）
	DurationMs int64           // 攻击重放耗时
}

// Attempt 是一次晋升尝试的输入：要复现什么、坐实后落成哪种节点、落在哪。
type Attempt struct {
	TaskID     string          // = assignment_id，图归属（一次交战一个图）
	NodeID     string          // 溯源到的源节点 ID（可空）
	Kind       core.NodeKind   // 坐实后的节点类型（observation/discovery）
	Primitives json.RawMessage // 要回放的 L1 原语序列
	Content    json.RawMessage // 坐实后写入节点的载荷（severity/taxonomy/evidence…）
	Priority   string          // 优先级（critical/high/medium/low）
}

// Verdict 是 LLM 裁决的三态结论。
const (
	VerdictConfirmed = "confirmed"
	VerdictRefuted   = "refuted"
)

// PromotionEvaluator 是 Observation→图节点的晋升门。
type PromotionEvaluator struct {
	graph    graphWriter
	replayer Replayer
	findings findingWriter   // 验证通过后写入 finding 表
	judge    LLMJudge        // 可选：LLM 语义裁决（生产必配；nil=纯机器断言，供测试/降级）
	logger   *zerolog.Logger // 可选：finding 写入失败等非致命错误经此告警
	// agentRunID 归属的 agent_run.id（finding.agent_task_id NOT NULL）；
	// 洞察链上 executor 侧 run 由装配方注入，空则写库会失败并告警。
	agentRunID *string
}

// New 构造 Evaluator。replayer 为 nil 时 Promote 会报错（无复现能力即无晋升）。
func New(graph graphWriter, replayer Replayer, findings findingWriter) *PromotionEvaluator {
	return &PromotionEvaluator{
		graph:    graph,
		replayer: replayer,
		findings: findings,
	}
}

// WithAgentRunID 注入归属 agent_run.id（finding 表 NOT NULL 外键）。链式。
func (v *PromotionEvaluator) WithAgentRunID(runID string) *PromotionEvaluator {
	if runID != "" {
		v.agentRunID = &runID
	}
	return v
}

// WithJudge 注入 LLM 裁决器（链式）。裁决语义：机器重放采集证据，LLM 终裁坐实/证伪；
// LLM 调用失败时回退机器断言判定（可用性优先——LLM 抖动不卡死晋升链）。
func (v *PromotionEvaluator) WithJudge(j LLMJudge) *PromotionEvaluator {
	v.judge = j
	return v
}

// WithLogger 注入可选日志器：finding 写入失败等非致命错误经此告警。
func (v *PromotionEvaluator) WithLogger(l zerolog.Logger) *PromotionEvaluator {
	v.logger = &l
	return v
}

// replayRecord 是裁决链上一次机器重放的审计记录（预跑 + 裁决官自主复放都落）。
type replayRecord struct {
	Phase      string          `json:"phase"` // prerun（预跑）/ judge（裁决官自主复放）
	Evidence   json.RawMessage `json:"evidence"`
	DurationMs int64           `json:"duration_ms"`
	Error      string          `json:"error,omitempty"` // 重放失败也留档（审计需要）
}

// Promote 把一条 Observation 过复现门晋升成探索图节点。
//
// 返回值语义：
//   - (node, nil)  复现坐实，已晋升成 verified 节点；
//   - (nil, nil)   复现证伪，未进图（证据已留 exploration_verification 供审计）——非错误；
//   - (nil, err)   门本身出错（复现执行/落库失败）。
//
// 库不 log，错误上抛由 caller 记录（与 explorationgraph.Store 一致）。
func (v *PromotionEvaluator) Promote(ctx context.Context, a Attempt) (*explorationgraph.Node, error) {
	if a.TaskID == "" {
		return nil, fmt.Errorf("verifier: Attempt.TaskID 必填")
	}
	if a.Kind == "" {
		return nil, fmt.Errorf("verifier: Attempt.Kind 必填")
	}
	if v.replayer == nil {
		return nil, fmt.Errorf("verifier: 无 Replayer，无法复现晋升")
	}

	// 重放闭包：预跑与裁决官每次 replay_for_verification 都经此——完整证据链在此记录，
	// 裁决官复放的中间观察不再只活在 judge 的 ReAct 上下文里。
	var replays []replayRecord
	replay := func(rctx context.Context, phase string) (Result, error) {
		res, err := v.replayer.Replay(rctx, a.Primitives)
		rec := replayRecord{Phase: phase, DurationMs: res.DurationMs}
		if err != nil {
			rec.Error = err.Error()
		} else {
			rec.Evidence = res.Evaluation
		}
		replays = append(replays, rec)
		return res, err
	}

	res, err := replay(ctx, "prerun")
	if err != nil {
		return nil, fmt.Errorf("verifier: 复现执行失败: %w", err)
	}

	// 裁决：LLM 是唯一判定权持有者（机器只采证不判定）。judge 未装配/调用失败 = 本次
	// 未裁决，返回错误——绝不回退机器断言（谓词判不了漏洞语义，回退即橡皮图章）。
	if v.judge == nil {
		return nil, fmt.Errorf("verifier: 无 LLM 裁决器，无法晋升（机器不持判定权）")
	}
	var hyp string
	var c struct {
		Statement string `json:"statement"`
	}
	if json.Unmarshal(a.Content, &c) == nil {
		hyp = c.Statement
	}

	// 机器护栏：executor 声明的断言全部未命中 = 配方声称的特征根本没出现，不存在任何
	// 支持坐实的机器证据。此时直接 refuted、不进 judge（e2e 实测：judge 在
	// assert_passed=false 且 404 的证据下仍输出 confirmed——LLM 会违背裁决语义，
	// 机器兜底）。这是机器证伪而非机器坐实：LLM 只拥有 confirmed 的判定权，
	// refuted 是保守缺省，不违反判定权分层。
	if !assertPassedInEvidence(res.Evaluation) {
		return v.refuteEarly(ctx, a, hyp, res, replays)
	}

	verdict, reasoning, jErr := v.judge.Judge(ctx, hyp, a.Primitives, res.Evaluation,
		func(rctx context.Context) (Result, error) { return replay(rctx, "judge") })
	if jErr != nil {
		return nil, fmt.Errorf("verifier: LLM 裁决失败（本次未裁决，不回退机器）: %w", jErr)
	}
	confirmed := verdict == VerdictConfirmed // 唯一坐实来源

	// 证据聚合：最后一次成功重放的证据为主体，附裁决理由与完整重放链（含失败记录）。
	// generic 域无机器重放通道，裁决官的执行轨迹与结论就以 reasoning + replays[] 为审计面。
	final := res
	for _, rec := range replays {
		if rec.Error == "" && len(rec.Evidence) > 0 {
			final = Result{Evaluation: rec.Evidence, DurationMs: rec.DurationMs}
		}
	}
	evaluation := withReplayLog(final.Evaluation, verdict, reasoning, replays)

	// 证据链：无论坐实与否都落 exploration_verification（refuted 也留档供审计/复盘）。
	// 这是复放证据的唯一归宿——evaluator 不往图写观察节点（图的写权限不变式）。
	// nodeID 预先分配：坐实时它成为晋升 result 节点的 ID（verification.node_id 回指），
	// 证伪时它是未落图的占位（审计仍可定位本次裁决对象）。
	nodeID := uuid.New().String()
	outcome := explorationgraph.OutcomeRefuted
	if confirmed {
		outcome = explorationgraph.OutcomeConfirmed
	}
	verID, err := v.graph.RecordVerification(ctx, explorationgraph.Verification{
		ID:         uuid.New().String(),
		TaskID:     a.TaskID,
		NodeID:     nodeID, // 预分配，即使证伪也记录（审计需要）
		Primitives: a.Primitives,
		Outcome:    outcome,
		Evaluation: evaluation,
		DurationMs: final.DurationMs,
		CreatedAt:  time.Now(),
	})
	if err != nil {
		return nil, fmt.Errorf("verifier: 记录 verification 失败: %w", err)
	}

	// 审判标记：在假设节点上回写 verification_outcome——图可回答"这个假设试过没有、
	// 结果如何"（防死假设复活循环：planner/executor 读图可见已裁决）。仅 metadata
	// 浅合并，不新建节点（写权限不变式保持）。失败不阻塞裁决（标记是增强非门）。
	if a.NodeID != "" {
		mark := map[string]interface{}{
			"verification_id":      verID,
			"verification_outcome": outcome,
			"verification_at":      time.Now().Format(time.RFC3339),
		}
		if confirmed {
			mark["promoted_node"] = nodeID
		}
		if b, mErr := json.Marshal(mark); mErr == nil {
			if uErr := v.graph.UpdateNodeMetadata(ctx, a.NodeID, b); uErr != nil && v.logger != nil {
				v.logger.Warn().Err(uErr).Str("node_id", a.NodeID).Msg("假设节点审判标记写入失败（不影响裁决）")
			}
		}
	}

	// 证伪：不进图。铁律——图只存坐实态。
	if !confirmed {
		return nil, nil
	}

	// 坐实：晋升成 verified 节点（evaluator 对图的唯一写权限）
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

	_, err = v.graph.CreateNode(ctx, node)
	if err != nil {
		return nil, fmt.Errorf("verifier: 晋升节点失败: %w", err)
	}

	// 验证通过，写入 finding 表（未验证的不进 finding 表）
	if v.findings != nil {
		if err := v.writeFinding(ctx, node, a, final); err != nil {
			// finding 写入失败不阻塞晋升（节点已进图），但不能静默吞掉
			if v.logger != nil {
				v.logger.Warn().Err(err).Str("node_id", node.ID).Msg("finding 写入失败（节点已晋升，不影响图状态）")
			}
		}
	}

	return &node, nil
}

// withReplayLog 把最终证据、裁决理由与完整重放链（预跑 + 裁决官复放，含失败）聚合成一份审计 JSON。
// 保留原证据顶层字段（url/method 等）不套壳——下游（finding host 抽取等）依赖其形状。
// assertPassedInEvidence 从机器证据中读 executor 断言的命中情况。
// 证据无 assert_passed 字段（如 generic 域回显）返回 true——不阻断 judge 受理。
func assertPassedInEvidence(evidence json.RawMessage) bool {
	if len(evidence) == 0 {
		return true
	}
	var e struct {
		AssertPassed *bool `json:"assert_passed"`
	}
	if json.Unmarshal(evidence, &e) != nil || e.AssertPassed == nil {
		return true
	}
	return *e.AssertPassed
}

// refuteEarly 断言未命中时的机器证伪路径：落 verification 审计（不打扰 judge）、
// 给假设节点打 refuted 标记、发布 refuted 事件语义（node=nil）。
func (v *PromotionEvaluator) refuteEarly(
	ctx context.Context,
	a Attempt, hyp string, res Result, replays []replayRecord,
) (*explorationgraph.Node, error) {
	const verdict = VerdictRefuted
	const reasoning = "机器护栏：复现断言全部未命中（配方声称的特征未出现），无支持坐实的证据，不经裁决官直接证伪"
	evaluation := withReplayLog(res.Evaluation, verdict, reasoning, replays)

	verID, err := v.graph.RecordVerification(ctx, explorationgraph.Verification{
		ID:         uuid.New().String(),
		TaskID:     a.TaskID,
		NodeID:     uuid.New().String(),
		Primitives: a.Primitives,
		Outcome:    explorationgraph.OutcomeRefuted,
		Evaluation: evaluation,
		DurationMs: res.DurationMs,
		CreatedAt:  time.Now(),
	})
	if err != nil {
		return nil, fmt.Errorf("verifier: 记录 verification 失败: %w", err)
	}
	_ = verID
	_ = hyp

	if a.NodeID != "" {
		mark, _ := json.Marshal(map[string]interface{}{
			"verification_id":      verID,
			"verification_outcome": explorationgraph.OutcomeRefuted,
			"verification_at":      time.Now().Format(time.RFC3339),
			"refuted_by":           "machine_guard",
		})
		if uErr := v.graph.UpdateNodeMetadata(ctx, a.NodeID, mark); uErr != nil && v.logger != nil {
			v.logger.Warn().Err(uErr).Str("node_id", a.NodeID).Msg("假设节点 refuted 标记写入失败")
		}
	}
	return nil, nil
}

func withReplayLog(evaluation json.RawMessage, verdict, reasoning string, replays []replayRecord) json.RawMessage {
	merged := map[string]interface{}{}
	if len(evaluation) > 0 {
		if err := json.Unmarshal(evaluation, &merged); err != nil {
			return evaluation // 解析失败退回原证据，绝不让审计聚合毁掉本体
		}
	}
	merged["verdict"] = verdict
	if reasoning != "" {
		merged["reasoning"] = reasoning
	}
	merged["replay_count"] = len(replays)
	merged["replays"] = replays
	out, err := json.Marshal(merged)
	if err != nil {
		return evaluation
	}
	return out
}

// writeFinding 将验证通过的节点写入 finding 表（仅 Result 类节点）。
func (v *PromotionEvaluator) writeFinding(ctx context.Context, node explorationgraph.Node, attempt Attempt, res Result) error {
	// 只有 Result 类节点（包含漏洞）才写入 finding 表
	if node.Kind != core.KindResult {
		return nil
	}

	// 解析 node.Content 提取漏洞信息
	var content struct {
		Summary  string          `json:"summary"`
		Severity string          `json:"severity"`
		Host     string          `json:"host"`
		Target   json.RawMessage `json:"target"`
	}
	if err := json.Unmarshal(node.Content, &content); err != nil {
		return fmt.Errorf("解析节点内容失败: %w", err)
	}

	if content.Summary == "" {
		content.Summary = fmt.Sprintf("Verified vulnerability (node %s)", node.ID)
	}
	if content.Severity == "" {
		content.Severity = "medium" // 默认中危
	}
	hostFromEvidence := func(ev json.RawMessage) string {
		var e struct {
			URL string `json:"url"`
		}
		if json.Unmarshal(ev, &e) == nil && e.URL != "" {
			if u, pErr := url.Parse(e.URL); pErr == nil && u.Host != "" {
				return u.Host
			}
		}
		return "unknown"
	}

	if content.Host == "" || content.Host == "unknown" {
		content.Host = hostFromEvidence(res.Evaluation) // 从复现证据的 URL 抽真实 host
	}

	evaluation, err := json.Marshal(map[string]interface{}{
		"node_id":         node.ID,
		"verification_id": node.SourceID,
		"evaluation":      res.Evaluation,
		"duration_ms":     res.DurationMs,
	})
	if err != nil {
		return fmt.Errorf("marshal evaluation: %w", err)
	}

	f := finding.VulnFinding{
		TaskID:     node.TaskID,
		AgentRunID: v.agentRunID,
		Host:       content.Host,
		Summary:    content.Summary,
		Severity:   content.Severity,
		Evaluation: evaluation,
		Target:     content.Target,
		Repro:      attempt.Primitives, // 复现配方
	}

	_, err = v.findings.Save(ctx, f)
	return err
}
