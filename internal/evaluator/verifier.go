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
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"github.com/V3teran/liusha/internal/explorationgraph"
	"github.com/V3teran/liusha/internal/finding"
	"github.com/V3teran/liusha/internal/framework/core"
)

// graphStore 是 Evaluator 依赖的探索图读写子集：收窄依赖 + 便于测试替身。
// *explorationgraph.Store 自动满足本接口。
//
// 图的写权限不变式：planner 写 objective/action，executor 写 observation，
// evaluator 只**新建** result（晋升）；对既有假设节点仅做 metadata 审判标记
// （verification_outcome + confidence），不新建节点。复放证据不走图——
// 落 exploration_verification 审计链。ListResults 供晋升前的语义去重对照。
type graphStore interface {
	RecordVerification(ctx context.Context, v explorationgraph.Verification) (string, error)
	CreateNode(ctx context.Context, n explorationgraph.Node) (string, error)
	CreateNodeWithEdges(ctx context.Context, n explorationgraph.Node, edges []explorationgraph.Edge) error
	UpdateNodeMetadata(ctx context.Context, id string, metadata json.RawMessage) error
	ListResults(ctx context.Context, taskID string) ([]explorationgraph.Node, error)
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
	Evaluation json.RawMessage // 基线/利用请求双对照证据 + executor 声明预期的核验明细（参考）
	DurationMs int64           // 利用请求重放耗时
}

// Attempt 是一次晋升尝试的输入：要复现什么、坐实后落成哪种节点、落在哪。
type Attempt struct {
	TaskID     string          // = assignment_id，图归属（一次交战一个图）
	NodeID     string          // 溯源到的源节点 ID（可空）
	Kind       core.NodeKind   // 坐实后的节点类型（result）
	Primitives json.RawMessage // 要回放的 L1 原语序列
	Content    json.RawMessage // 坐实后写入节点的载荷（severity/taxonomy/evidence…）
	Priority   string          // 优先级（critical/high/medium/low）
}

// Verdict 是 LLM 裁决的两态结论字面量。
const (
	VerdictConfirmed = "confirmed"
	VerdictRefuted   = "refuted"
)

// Verdict 是裁决官的终裁输出：结论 + 置信 + 理由 + 结构化指纹（坐实时给出，
// 晋升门据此做语义去重）。
type Verdict struct {
	Verdict     string              // confirmed / refuted
	Confidence  float64             // 0-1，裁决官自评
	Reasoning   string              // 一句话裁决理由（引用亲见差分）
	Fingerprint *FindingFingerprint // 坐实时的发现指纹；缺失则该发现不参与语义去重
}

// FindingFingerprint 是漏洞发现的语义指纹：判定权持有者对"什么洞、在哪个位置"
// 的结构化回答。同类漏洞在同一位置的重复确认共享同一规范形——晋升门据此把
// 第二次及以后的确认折叠为既有发现的佐证，不再新建 result、不再重写 finding。
type FindingFingerprint struct {
	Class  string `json:"class"`  // 漏洞类别，小写 snake_case（reflected_xss / stored_xss / sql_injection…）
	Method string `json:"method"` // 请求方法
	Host   string `json:"host"`   // 目标主机（含端口）
	Path   string `json:"path"`   // URL 路径（不含 query）
	Param  string `json:"param"`  // 注入点（参数名/头名/正文字段）；无明确注入点留空
}

// Key 返回规范形指纹串（比较与落库唯一用它）。归一化方向一律从宽：
// 大小写折叠、路径去尾斜杠——去重的目标是合并同义报告，不是区分书写差异。
func (f *FindingFingerprint) Key() string {
	if f == nil {
		return ""
	}
	norm := func(s string) string { return strings.ToLower(strings.TrimSpace(s)) }
	return strings.Join([]string{
		norm(f.Class),
		norm(f.Method),
		norm(f.Host),
		strings.TrimSuffix(norm(f.Path), "/"),
		norm(f.Param),
	}, "|")
}

// fingerprintMetadataKey 是 result 节点 metadata 中指纹的落库键。
const fingerprintMetadataKey = "finding_fingerprint"

// 验证档位（verification_tier）：结论成色的显式标记，下游（报告/前端/planner）
// 据此分级消费——judge_only 的结论没经过机器复放通道，含金量低于全门。
const (
	TierFullGate  = "full_gate"  // 机器复放采证 + LLM 终裁（web 域）
	TierJudgeOnly = "judge_only" // 无机器重放通道，裁决官自主取证自主终裁（generic 域，含 web assert 不完整的降级配方）
)

// 反驳原因（refute_reason）：区分"证据没拿到"与"拿到了证据但不构成漏洞"——
// planner 据此决定是换配方重试还是方向作废，两者处置不同。
const (
	RefuteNotReproducible         = "not_reproducible"          // 机器护栏：断言未命中/复放失败——配方或上下文问题，可换配方重试
	RefuteNotAVuln                = "not_a_vuln"                // 裁决官语义证伪：复现了但不构成漏洞——方向作废
	RefuteAssertNotDiscriminative = "assert_not_discriminative" // 质量门：断言无鉴别力，配方无法构成证据——须换断言重提
	RefuteInvalidRecipe           = "invalid_recipe"            // 确定性配方缺陷：重放必然同样失败，快速终局出圈
)

// verificationTier 从域信封判定验证档位。evaluator 不 import executor（会成环），
// 域字面量与 executor.ReproEnvelope 的 domain 约定保持一致：web 有机器重放通道；
// generic（含 web assert 不完整、被 evaluator agent 降级改写的配方）无机器通道。
func verificationTier(primitives json.RawMessage) string {
	var env struct {
		Domain string `json:"domain"`
	}
	if err := json.Unmarshal(primitives, &env); err != nil || env.Domain == "generic" {
		return TierJudgeOnly
	}
	return TierFullGate
}

// ErrAssertNotDiscriminative 是复现方的"质量拒绝"哨兵：断言无鉴别力
// （自指、基线同样命中等）——配方被证明无法构成漏洞证据。这不是门故障
// （可重试），而是对这条假设的**证伪裁决**：走审计链留档，不走重试上限。
var ErrAssertNotDiscriminative = errors.New("assert not discriminative")

// PromotionEvaluator 是 Observation→图节点的晋升门。
type PromotionEvaluator struct {
	graph    graphStore
	replayer Replayer
	findings findingWriter   // 验证通过后写入 finding 表
	judge    LLMJudge        // 可选：LLM 语义裁决（生产必配；nil=纯机器断言，供测试/降级）
	logger   *zerolog.Logger // 可选：finding 写入失败等非致命错误经此告警
	// agentRunID 归属的 agent_run.id（finding.agent_task_id NOT NULL）；
	// 洞察链上 executor 侧 run 由装配方注入，空则写库会失败并告警。
	agentRunID *string
}

// New 构造 Evaluator。replayer 为 nil 时 Promote 会报错（无复现能力即无晋升）。
func New(graph graphStore, replayer Replayer, findings findingWriter) *PromotionEvaluator {
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
//   - (node, nil)  复现坐实，已晋升成 verified 节点（node 为新建或沿用的既有发现）；
//   - (nil, nil)   复现证伪，未进图（证据已留 exploration_verification 供审计）——非错误；
//   - (nil, err)   门本身出错（复现执行/落库失败）。
//
// 库不 log，错误上抛由 caller 记录（与 explorationgraph.Store 一致）。
func (v *PromotionEvaluator) Promote(ctx context.Context, a Attempt) (*explorationgraph.Node, error) {
	// 1. 验证输入
	if err := v.validateAttempt(a); err != nil {
		return nil, err
	}

	// 2. 创建重放闭包
	var replays []replayRecord
	replay := v.createReplayFunction(a, &replays)

	// 3. 预跑阶段
	res, err := v.executePrerun(ctx, replay)
	if err != nil {
		// 质量拒绝（断言无鉴别力）是对假设的证伪裁决：留审计链，不走重试
		if errors.Is(err, ErrAssertNotDiscriminative) {
			return v.refuteByQualityGate(ctx, a, err)
		}
		return nil, err
	}

	// 4. 提取假设
	hyp := extractHypothesis(a)
	tier := verificationTier(a.Primitives)

	// 5. 机器护栏检查
	if node, shouldContinue, err := v.checkMachineGuard(ctx, a, hyp, res, replays); !shouldContinue {
		return node, err
	}

	// 6. LLM 裁决
	verdict, err := v.executeLLMJudge(ctx, a, hyp, res, replay)
	if err != nil {
		return nil, err
	}

	// 7. 聚合证据
	final, evaluation := aggregateEvidence(res, tier, verdict.Verdict, verdict.Reasoning, replays)

	// 8. 坐实先查语义去重：同一漏洞（同指纹）的再次确认折叠为既有发现的佐证
	fingerprintKey := verdict.Fingerprint.Key()
	var duplicate *explorationgraph.Node
	if verdict.Verdict == VerdictConfirmed && fingerprintKey != "" {
		duplicate, err = v.findConfirmedDuplicate(ctx, a.TaskID, fingerprintKey)
		if err != nil {
			return nil, err
		}
	}
	if duplicate != nil {
		evaluation = withDuplicateInfo(evaluation, duplicate.ID, fingerprintKey)
	}

	// 9. 处理裁决结果并记录验证到审计链
	outcome := explorationgraph.OutcomeRefuted
	if verdict.Verdict == VerdictConfirmed {
		outcome = explorationgraph.OutcomeConfirmed
	}
	verID, err := v.recordVerification(ctx, a, outcome, evaluation, final.DurationMs)
	if err != nil {
		return nil, err
	}

	// 10. 在假设节点上回写审判标记
	mark := hypothesisMark{
		verID:        verID,
		outcome:      outcome,
		confirmed:    verdict.Verdict == VerdictConfirmed,
		tier:         tier,
		promotedNode: "",
	}
	switch {
	case verdict.Verdict == VerdictRefuted:
		mark.refuteReason = RefuteNotAVuln
	case duplicate != nil:
		mark.promotedNode = duplicate.ID
		mark.duplicateOf = duplicate.ID
	}
	v.markHypothesisNode(ctx, a, mark)

	// 11. 证伪：不进图
	if verdict.Verdict == VerdictRefuted {
		return nil, nil
	}

	// 12. 语义重复：既有发现即归属——不新建节点、不重写 finding，
	// 返回既有节点让上游事件指向它（新证据已在审计链 replays 里）。
	if duplicate != nil {
		if v.logger != nil {
			v.logger.Info().
				Str("node_id", a.NodeID).
				Str("duplicate_of", duplicate.ID).
				Str("fingerprint", fingerprintKey).
				Msg("同指纹发现已存在，本次确认折叠为佐证")
		}
		return duplicate, nil
	}

	// 13. 创建已验证节点（指纹随 metadata 落库，供后续去重对照）
	nodeID := uuid.New().String()
	node, err := v.createVerifiedNode(ctx, a, nodeID, verID, fingerprintKey)
	if err != nil {
		return nil, err
	}

	// 14. 写入 finding 表
	v.writeFindingIfNeeded(ctx, *node, a, final)

	return node, nil
}

// findConfirmedDuplicate 在既有 verified result 中查找同指纹发现。
// 对照键是裁决官产出的语义指纹；未带指纹的历史 result 不参与对照
// （缺失指纹的发现保持原样，不影响其地位）。
func (v *PromotionEvaluator) findConfirmedDuplicate(ctx context.Context, taskID, key string) (*explorationgraph.Node, error) {
	results, err := v.graph.ListResults(ctx, taskID)
	if err != nil {
		return nil, fmt.Errorf("verifier: 语义去重查询既有 result 失败: %w", err)
	}
	for i := range results {
		var m struct {
			Fingerprint string `json:"finding_fingerprint"`
		}
		if json.Unmarshal(results[i].Metadata, &m) != nil || m.Fingerprint == "" {
			continue
		}
		if m.Fingerprint == key {
			return &results[i], nil
		}
	}
	return nil, nil
}

// withDuplicateInfo 把语义去重归属并入审计 JSON（解析失败退回原证据）。
func withDuplicateInfo(evaluation json.RawMessage, existingID, key string) json.RawMessage {
	merged := map[string]interface{}{}
	if json.Unmarshal(evaluation, &merged) != nil {
		return evaluation
	}
	merged["duplicate_of"] = existingID
	merged["finding_fingerprint"] = key
	if b, err := json.Marshal(merged); err == nil {
		return b
	}
	return evaluation
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

// refuteByQualityGate 把"断言无鉴别力"的质量拒绝落成正式证伪：审计链留档
// （带 quality_gate 标记与拒绝理由）、假设节点 confidence 流转为 refuted。
// 语义区别于 invalid_recipe（门故障止损）：这是裁决结论——配方证明了
// 命中不构成漏洞证据。
func (v *PromotionEvaluator) refuteByQualityGate(ctx context.Context, a Attempt, cause error) (*explorationgraph.Node, error) {
	evaluation, _ := json.Marshal(map[string]interface{}{
		"quality_gate":      true,
		"refute_reason":     RefuteAssertNotDiscriminative,
		"verification_tier": verificationTier(a.Primitives),
		"reason":            cause.Error(),
		"phase":             "prerun",
	})

	verID, err := v.graph.RecordVerification(ctx, explorationgraph.Verification{
		ID:         uuid.New().String(),
		TaskID:     a.TaskID,
		NodeID:     a.NodeID,
		Primitives: a.Primitives,
		Outcome:    explorationgraph.OutcomeRefuted,
		Evaluation: evaluation,
		CreatedAt:  time.Now(),
	})
	if err != nil {
		return nil, fmt.Errorf("verifier: 质量拒绝留档失败: %w", err)
	}

	v.markHypothesisNode(ctx, a, hypothesisMark{
		verID:        verID,
		outcome:      explorationgraph.OutcomeRefuted,
		confirmed:    false,
		tier:         verificationTier(a.Primitives),
		refuteReason: RefuteAssertNotDiscriminative,
	})
	if v.logger != nil {
		v.logger.Info().
			Str("node_id", a.NodeID).
			Str("reason", cause.Error()).
			Msg("质量门证伪（断言无鉴别力）")
	}
	return nil, nil
}

// refuteEarly 断言未命中时的机器证伪路径：落 verification 审计（不打扰 judge）、
// 给假设节点打 refuted 标记、发布 refuted 事件语义（node=nil）。
func (v *PromotionEvaluator) refuteEarly(
	ctx context.Context,
	a Attempt, hyp string, res Result, replays []replayRecord,
) (*explorationgraph.Node, error) {
	const verdict = VerdictRefuted
	const reasoning = "机器护栏：复现断言全部未命中（配方声称的特征未出现），无支持坐实的证据，不经裁决官直接证伪"
	tier := verificationTier(a.Primitives)
	evaluation := withReplayLog(res.Evaluation, tier, verdict, reasoning, replays)

	verID, err := v.graph.RecordVerification(ctx, explorationgraph.Verification{
		ID:         uuid.New().String(),
		TaskID:     a.TaskID,
		NodeID:     a.NodeID,
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
			"verification_tier":    tier,
			"refute_reason":        RefuteNotReproducible,
			"verification_at":      time.Now().Format(time.RFC3339),
			"confidence":           explorationgraph.ConfidenceRefuted,
			"refuted_by":           "machine_guard",
		})
		if uErr := v.graph.UpdateNodeMetadata(ctx, a.NodeID, mark); uErr != nil && v.logger != nil {
			v.logger.Warn().Err(uErr).Str("node_id", a.NodeID).Msg("假设节点 refuted 标记写入失败")
		}
	}
	return nil, nil
}

func withReplayLog(evaluation json.RawMessage, tier, verdict, reasoning string, replays []replayRecord) json.RawMessage {
	merged := map[string]interface{}{}
	if len(evaluation) > 0 {
		if err := json.Unmarshal(evaluation, &merged); err != nil {
			return evaluation // 解析失败退回原证据，绝不让审计聚合毁掉本体
		}
	}
	merged["verdict"] = verdict
	if tier != "" {
		merged["verification_tier"] = tier
	}
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
		"node_id":           node.ID,
		"verification_id":   node.SourceID,
		"verification_tier": verificationTier(attempt.Primitives), // 结论成色：full_gate/judge_only
		"evaluation":        res.Evaluation,
		"duration_ms":       res.DurationMs,
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
