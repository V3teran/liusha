// Package evaluator 实现本包职责；细节见文件级注释。

// verifier.go 实现认知循环的复现晋升门（Promotion Evaluator）。
//
// 探索图写权限不变式（4 种节点、谁写什么）：
//   - planner 写 objective / action
//   - executor 写 observation（假设 / 事实）
//   - evaluator 只写 result——本文件就是 observation(假设) → result(坐实) 的唯一状态转换门
//
//	Observation(在途假设，图节点，verification_status=pending)
//	    → Promote(attempt)
//	    → 按信封 kind 选复现通道采证（recipe/assert 对本包不透明，形状归各配方形状；
//	      没有通道的形状回显配方，由评估者自主取证）
//	    → LLMJudge 终判（机器采证，LLM 持判定权）
//	    → RecordVerification(confirmed/refuted)  // 审计链：replays[] 含预跑+评估者每次复放
//	    → confirmed: CreateNode(result) 进图 + 写 finding 表
//	    └ refuted:   不进图（证据留 exploration_verification 供审计）
//
// 配方怎么执行归各配方形状的复现通道（装配时经 WithReplayer 注册），门只认 Replayer
// 接口与哨兵错误（ErrInvalidRecipe / ErrTargetUnreachable）——加新形状晋升门零改动。

package evaluator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"github.com/V3teran/liusha/internal/explorationgraph"
	"github.com/V3teran/liusha/internal/framework/core"
	"github.com/V3teran/liusha/internal/repro"
)

// graphStore 是 Evaluator 依赖的探索图读写子集：收窄依赖 + 便于测试替身。
// *explorationgraph.Store 自动满足本接口。
//
// 图的写权限不变式：planner 写 objective/action，executor 写 observation，
// evaluator 只**新建** result（晋升）；对既有假设节点仅做 metadata 验证标记
// （verification_status + refute_reason），不新建节点。复放证据不走图——
// 落 exploration_verification 审计链。ListResults 供晋升前的语义去重对照。
type graphStore interface {
	RecordVerification(ctx context.Context, v explorationgraph.Verification) (string, error)
	CreateNode(ctx context.Context, n explorationgraph.Node) (string, error)
	CreateNodeWithEdges(ctx context.Context, n explorationgraph.Node, edges []explorationgraph.Edge) error
	UpdateNodeMetadata(ctx context.Context, id string, metadata json.RawMessage) error
	ListResults(ctx context.Context, taskID string) ([]explorationgraph.Node, error)
}

// ConfirmedClaim 是复现门坐实一条主张后交给交付管道的完整输入：门只透传通用字段
// （陈述/证据/配方/成色），交付侧怎样解释它们（评级、台账定位）归交付管道。
type ConfirmedClaim struct {
	TaskID         string
	ResultNodeID   string          // 晋升产生的 result 节点
	VerificationID string          // 审计链回指
	Statement      string          // 主张陈述
	Class          string          // 主张类别（评估者按判定契约给出，类别词表由场景定；空 = 未给出）
	Priority       string          // 优先级（交付侧按自己的评级体系转译并兜底）
	Impact         string          // 影响与波及面（中性因果陈述；台账同名直传）
	Suggestion     string          // 处理建议（译作 remediation）
	HostHint       string          // 载荷自带的位置提示（可空，交付侧可重抽）
	Target         json.RawMessage // 载荷自带的目标引用（可空）
	Evidence       json.RawMessage // 复现证据（对照/验证请求双对照 + 断言明细）
	Repro          json.RawMessage // 复现配方
	Tier           string          // 验证档位 full_gate/judge_only
	DurationMs     int64
}

// ClaimSink 是坐实主张的交付管道（装配时注入，交付物由部署决定）。nil 容忍（不交付，供测试）。
type ClaimSink interface {
	Record(ctx context.Context, claim ConfirmedClaim) error
}

// Replayer 是一种配方形状的机器复现通道。门把「复现」委托给它，自身不碰形状细节。
// repro 是要回放的复现配方（信封，recipe/assert 形状由配方形状定义）；返回采集到的证据。配方缺陷与
// 目标不可达分别包装成 ErrInvalidRecipe / ErrTargetUnreachable。
type Replayer interface {
	Replay(ctx context.Context, repro json.RawMessage) (Result, error)
}

// Result 是一次复现采集的客观证据（机器不判定坐实——语义判定权全在 LLMJudge）。
type Result struct {
	Evidence   json.RawMessage // 基线/利用请求双对照证据 + executor 声明预期的核验明细（参考）
	DurationMs int64           // 利用请求重放耗时
}

// Claim 是一条待验证的主张：被验证的假设节点、它的复现配方与坐实后结果节点的载荷。
// 只由评估者从图上的假设节点构造。
type Claim struct {
	TaskID   string          // 所属任务
	NodeID   string          // 被验证的假设节点
	Repro    json.RawMessage // 复现配方（信封）
	Content  json.RawMessage // 坐实后写入结果节点的载荷（statement/impact/suggestion，与假设同名）
	Priority string          // 优先级（取假设节点的）
}

// Verdict 是 LLM 判定的两态结论字面量。
const (
	VerdictConfirmed = "confirmed"
	VerdictRefuted   = "refuted"
)

// Verdict 是评估者的终判输出：结论 + 置信 + 理由 + 主张指纹（坐实时按判定契约给出，
// 形状见 ClaimFingerprint）。
type Verdict struct {
	Verdict      string          // confirmed / refuted
	Confidence   float64         // 0-1，评估者自评
	Reasoning    string          // 一句话评估理由（引用亲见差分）
	RefuteReason string          // refuted 时评估者给出的反驳原因原文；合法性由 judgeRefuteReason 判定
	Fingerprint  json.RawMessage // 主张指纹原文；空或形状损坏 = 不参与语义去重
}

// claimFingerprintMetadataKey 是 result 节点 metadata 中指纹规范键的落库键。
const claimFingerprintMetadataKey = "claim_fingerprint"

// claimClassMetadataKey 是 result 节点 metadata 中主张类别的落库键：交付按它给出类别，
// 不从规范键里拆（规范键是去重的对照形式，不是数据的出处）。
const claimClassMetadataKey = "claim_class"

// 验证档位（verification_tier）：结论成色的显式标记，下游（报告/前端/planner）
// 据此分级消费——judge_only 的结论没经过机器复放通道，含金量低于全门。
const (
	TierFullGate  = "full_gate"  // 机器复放采证 + LLM 终判（有复现通道的配方形状，如 http）
	TierJudgeOnly = "judge_only" // 无机器复现通道，评估者自主取证自主终判（如 steps）
)

// 反驳原因（refute_reason）：区分"证据没拿到"与"拿到了证据但主张不成立"——
// planner 据此决定是换配方重试还是方向作废，两者处置不同。
//
// 前三种既可由机器（护栏、质量门）判出，也可由评估者在判定里给出；后两种只属于复现通道
// 与评估环节，评估者不能给出。
const (
	RefuteNotReproducible         = "not_reproducible"          // 证据没拿到：断言未命中、复放失败或评估者取证不足——主张未被否定，可换配方重试
	RefuteClaimInvalid            = "claim_invalid"             // 主张不成立：证据到手但不支持主张——方向作废
	RefuteAssertNotDiscriminative = "assert_not_discriminative" // 断言无鉴别力：命中的特征在正常情况下也出现，配方构不成证据——须换断言重提
	RefuteInvalidRecipe           = "invalid_recipe"            // 确定性配方缺陷：重放必然同样失败，快速终局出圈
	RefuteJudgeUnavailable        = "judge_unavailable"         // 评估者不可用耗尽（LLM 调用/输出解析反复失败）——配方未必有错，方向可重试
)

// judgeRefuteReason 把评估者给出的反驳原因归一为落标值：只接受判定层面的三种原因；缺失或
// 越权时按 claim_invalid 落标并记日志（判定契约要求必填，缺失属于输出不合规）。
func (g *PromotionGate) judgeRefuteReason(nodeID, given string) string {
	switch given {
	case RefuteNotReproducible, RefuteAssertNotDiscriminative, RefuteClaimInvalid:
		return given
	}
	if g.logger != nil {
		g.logger.Warn().Str("node_id", nodeID).Str("refute_reason", given).
			Msg("评估者未给出合法的反驳原因，按 claim_invalid 落标")
	}
	return RefuteClaimInvalid
}

// ErrJudgeUnavailable 标记评估者侧的基础设施失败（LLM 调用失败/终稿
// 解析失败）——与配方损坏性质不同：机器重放可能全部通过、主张真实成立，
// 只是判定这一环没跑通。耗尽重试后以 RefuteJudgeUnavailable 出圈而非
// invalid_recipe，planner 的 refuted 视野据此把方向视为可重试而非配方作废
// （实测：断言全过 + 判定散文写明 confirmed 0.99 的真实主张被误标
// invalid_recipe/refuted 出圈）。
var ErrJudgeUnavailable = errors.New("评估者不可用")

// verificationTier 判定验证档位：配方形状有机器复现通道即 full_gate，否则 judge_only。
func (g *PromotionGate) verificationTier(envelope json.RawMessage) string {
	var env repro.Envelope
	if json.Unmarshal(envelope, &env) == nil {
		if _, ok := g.replayers[env.Kind]; ok {
			return TierFullGate
		}
	}
	return TierJudgeOnly
}

// ErrAssertNotDiscriminative 是复现方的"质量拒绝"哨兵：断言无鉴别力
// （自指、基线同样命中等）——配方被证明无法构成主张的证据。这不是门故障
// （可重试），而是对这条假设的**证伪判定**：走审计链留档，不走重试上限。
var ErrAssertNotDiscriminative = errors.New("assert not discriminative")

// ErrInvalidRecipe 标记确定性配方缺陷（信封或配方形状不对、缺必需字段）：重放必然
// 同样失败，复现门快速终局出圈（refute_reason=invalid_recipe），不走重试阶梯。复现
// 通道把自己的形状错误包装成它。
var ErrInvalidRecipe = errors.New("复现配方无效")

// ErrTargetUnreachable 标记目标侧不可达（重放没拿到响应）：与评估者瞬时故障不同，
// 它计入重试上限，耗尽按 not_reproducible 出圈——否则目标宕机时验证无限重试、任务
// 永不收敛。复现通道把自己的传输失败包装成它。
var ErrTargetUnreachable = errors.New("复现目标不可达")

// PromotionGate 是 Observation→图节点的晋升门。
type PromotionGate struct {
	graph     graphStore
	replayers map[string]Replayer // 各配方形状的机器复现通道；没有通道的形状由评估者自主取证
	sink      ClaimSink           // 坐实主张的交付管道（装配时注入；nil 不交付）
	judge     LLMJudge            // LLM 语义判定（生产必配）
	logger    *zerolog.Logger     // 非致命错误（交付失败等）经此告警
}

// New 构造复现晋升门。配方形状的机器复现通道经 WithReplayer 注册；没有通道的形状（如
// steps）由评估者按配方自主取证。
func New(graph graphStore, sink ClaimSink) *PromotionGate {
	return &PromotionGate{
		graph:     graph,
		replayers: map[string]Replayer{},
		sink:      sink,
	}
}

// WithReplayer 注册一种配方形状的机器复现通道（链式）。
func (g *PromotionGate) WithReplayer(kind string, r Replayer) *PromotionGate {
	g.replayers[kind] = r
	return g
}

// replay 按信封的 kind 分发到机器复现通道；没有通道的形状回显配方，由评估者按步骤自主
// 执行取证（验证档位 judge_only）。
func (g *PromotionGate) replay(ctx context.Context, envelope json.RawMessage) (Result, error) {
	var env repro.Envelope
	if err := json.Unmarshal(envelope, &env); err != nil {
		return Result{}, fmt.Errorf("%w: 解析配方信封失败: %w", ErrInvalidRecipe, err)
	}
	if r, ok := g.replayers[env.Kind]; ok {
		return r.Replay(ctx, envelope)
	}
	if len(env.Recipe) == 0 {
		return Result{}, fmt.Errorf("%w: recipe 为空——评估者无从执行", ErrInvalidRecipe)
	}
	assert := env.Assert
	if len(assert) == 0 {
		assert = json.RawMessage("{}")
	}
	out, err := json.Marshal(map[string]any{
		"kind":   env.Kind,
		"recipe": env.Recipe,
		"assert": assert,
		"note":   "该形状的配方没有机器复现通道，以上是配方原文",
	})
	if err != nil {
		return Result{}, fmt.Errorf("序列化配方回显失败: %w", err)
	}
	return Result{Evidence: out}, nil
}

// WithJudge 注入 LLM 判定器（链式）。判定语义：机器重放只采集证据，LLM 终判
// 坐实/证伪——机器不持判定权，LLM 失败不回退机器断言（否则断言命中即坐实
// 会放过"命中即常态输出"的假阳性）；本次未出结论，错误带
// ErrJudgeUnavailable 交由调用方重试/止损分流。
func (g *PromotionGate) WithJudge(j LLMJudge) *PromotionGate {
	g.judge = j
	return g
}

// WithLogger 注入可选日志器：finding 写入失败等非致命错误经此告警。
func (g *PromotionGate) WithLogger(l zerolog.Logger) *PromotionGate {
	g.logger = &l
	return g
}

// replayRecord 是验证链上一次机器重放的审计记录（预跑 + 评估者自主复放都落）。
type replayRecord struct {
	Phase      string          `json:"phase"` // prerun（预跑）/ judge（评估者自主复放）
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
func (g *PromotionGate) Promote(ctx context.Context, a Claim) (*explorationgraph.Node, error) {
	// 1. 验证输入
	if err := g.validateClaim(a); err != nil {
		return nil, err
	}

	// 2. 创建重放闭包
	var replays []replayRecord
	replay := g.createReplayFunction(a, &replays)

	// 3. 预跑阶段
	res, err := g.executePrerun(ctx, replay)
	if err != nil {
		// 质量拒绝（断言无鉴别力）是对假设的证伪判定：留审计链，不走重试
		if errors.Is(err, ErrAssertNotDiscriminative) {
			return g.refuteByQualityGate(ctx, a, err)
		}
		return nil, err
	}

	// 4. 提取假设
	hyp := claimStatement(a)
	tier := g.verificationTier(a.Repro)

	// 5. 机器护栏检查
	if node, shouldContinue, err := g.checkMachineGuard(ctx, a, hyp, res, replays); !shouldContinue {
		return node, err
	}

	// 6. LLM 判定
	verdict, err := g.executeLLMJudge(ctx, a, hyp, res, replay)
	if err != nil {
		return nil, err
	}

	// 7. 聚合证据
	final, evaluation := aggregateEvidence(res, tier, verdict.Verdict, verdict.Reasoning, replays)

	// 8. 坐实先查语义去重：同一主张（同指纹规范键）的再次确认折叠为既有
	// 结果的佐证。指纹缺失或形状损坏则不折叠。
	fingerprintKey, class := "", ""
	if verdict.Verdict == VerdictConfirmed {
		if key, ok := canonicalFingerprint(verdict.Fingerprint); ok {
			fingerprintKey = key
		}
		class = claimClass(verdict.Fingerprint)
	}
	var duplicate *explorationgraph.Node
	if fingerprintKey != "" {
		duplicate, err = g.findConfirmedDuplicate(ctx, a.TaskID, fingerprintKey)
		if err != nil {
			return nil, err
		}
	}
	if duplicate != nil {
		evaluation = withDuplicateInfo(evaluation, duplicate.ID, fingerprintKey)
	}

	// 9. 处理判定结果并记录验证到审计链
	outcome := explorationgraph.VerificationRefuted
	if verdict.Verdict == VerdictConfirmed {
		outcome = explorationgraph.VerificationConfirmed
	}
	verID, err := g.recordVerification(ctx, a, outcome, evaluation, final.DurationMs)
	if err != nil {
		return nil, err
	}

	// 10. 落图先于假设标记：标记会把假设移出待验证集合，必须等结果节点落地。
	// 交付尽力而为（见 deliverClaim）：验证结论以审计链与图为准，交付是幂等投影，
	// 缺口由 runner 的周期补交付收账——交付故障拖不垮验证，也不再把假设按
	// 不可复现错驳（实测：交付反复失败曾令已坐实主张整批出圈）。
	mark := hypothesisMark{status: outcome}
	var promoted *explorationgraph.Node
	switch {
	case verdict.Verdict == VerdictRefuted:
		// 11. 证伪：不进图；反驳原因取评估者的分类（证据没拿到 / 断言无鉴别力 / 主张不成立）
		mark.refuteReason = g.judgeRefuteReason(a.NodeID, verdict.RefuteReason)
		mark.refuteDetail = verdict.Reasoning
	case duplicate != nil:
		// 12. 语义重复：既有发现即归属——不新建节点，返回既有节点让上游事件指向它
		// （新证据已在审计链 replays 里）。重放交付：补齐既有节点此前可能失败的交付，
		// finding 按结果节点幂等，不会重复或改写已有记录。
		mark.duplicateOf = duplicate.ID
		if g.logger != nil {
			g.logger.Info().
				Str("node_id", a.NodeID).
				Str("duplicate_of", duplicate.ID).
				Str("fingerprint", fingerprintKey).
				Msg("同指纹发现已存在，本次确认折叠为佐证")
		}
		if err := g.deliverClaim(ctx, a, *duplicate, verID, final, tier); err != nil {
			return nil, err
		}
		promoted = duplicate
	default:
		// 13. 创建已验证节点（指纹规范键与主张类别随 metadata 落库：前者供后续去重对照，后者随交付给出）
		node, err := g.createVerifiedNode(ctx, a, uuid.New().String(), fingerprintKey, class)
		if err != nil {
			return nil, err
		}
		// 14. 交付：坐实主张交给装配时注入的交付管道
		if err := g.deliverClaim(ctx, a, *node, verID, final, tier); err != nil {
			return nil, err
		}
		promoted = node
	}

	// 15. 在假设节点上回写验证标记
	g.markHypothesisNode(ctx, a, mark)
	return promoted, nil
}

// deliverClaim 把坐实主张交给交付管道，尽力而为：失败只告警、不影响验证结论与
// 假设标记。交付按结果节点幂等，台账缺口由 runner 的周期补交付按同一载荷收账；
// 把交付失败当验证失败会重跑复现、按不可复现错驳已坐实的主张。
func (g *PromotionGate) deliverClaim(
	ctx context.Context,
	a Claim,
	node explorationgraph.Node,
	verID string,
	res Result,
	tier string,
) error {
	if g.sink == nil || node.Kind != core.KindResult {
		return nil
	}
	claim, err := confirmedClaim(node, verID, res.Evidence, a.Repro, tier, res.DurationMs)
	if err != nil {
		// 载荷组装是确定性变换，失败即坐实材料损坏：如实告警，留补交付按结果节点重试。
		if g.logger != nil {
			g.logger.Error().Err(err).Str("node_id", node.ID).Msg("坐实主张交付载荷组装失败（补交付将重试）")
		}
		return nil
	}
	if err := g.sink.Record(ctx, claim); err != nil {
		if g.logger != nil {
			g.logger.Error().Err(err).Str("node_id", node.ID).Msg("坐实主张交付失败（周期补交付将收账）")
		}
	}
	return nil
}

// confirmedClaim 由结果节点与坐实它的那次验证组装交付载荷（正常交付与补交付共用一份）。
func confirmedClaim(node explorationgraph.Node, verID string, evidence, repro json.RawMessage, tier string, durationMs int64) (ConfirmedClaim, error) {
	var meta struct {
		Class string `json:"claim_class"`
	}
	if len(node.Metadata) > 0 {
		_ = json.Unmarshal(node.Metadata, &meta) // metadata 损坏只缺类别，不拦交付
	}
	var content struct {
		Statement  string          `json:"statement"`
		Impact     string          `json:"impact"`
		Suggestion string          `json:"suggestion"`
		Host       string          `json:"host"`
		Target     json.RawMessage `json:"target"`
	}
	if err := json.Unmarshal(node.Content, &content); err != nil {
		return ConfirmedClaim{}, fmt.Errorf("交付载荷解析失败 (node %s): %w", node.ID, err)
	}
	return ConfirmedClaim{
		TaskID:         node.TaskID,
		ResultNodeID:   node.ID,
		VerificationID: verID,
		Statement:      content.Statement,
		Class:          meta.Class,
		Priority:       string(node.Priority),
		Impact:         content.Impact,
		Suggestion:     content.Suggestion,
		HostHint:       content.Host,
		Target:         content.Target,
		Evidence:       evidence,
		Repro:          repro,
		Tier:           tier,
		DurationMs:     durationMs,
	}, nil
}

// ConfirmedResultReader 是补交付读取坐实结果所需的窄接口（*explorationgraph.Store 满足）。
type ConfirmedResultReader interface {
	GetNode(ctx context.Context, id string) (*explorationgraph.Node, error)
	ConfirmingVerification(ctx context.Context, resultID string) (explorationgraph.Verification, error)
}

// Redeliver 补交付一个坐实了却没进交付物的结果（交付一直失败，或曾被交付侧错误合并）：载荷由
// 结果节点与坐实它的那次验证重建，与正常交付同一份；交付侧按结果节点幂等。
func Redeliver(ctx context.Context, graph ConfirmedResultReader, sink ClaimSink, resultID string) error {
	node, err := graph.GetNode(ctx, resultID)
	if err != nil {
		return fmt.Errorf("读取结果节点 %s: %w", resultID, err)
	}
	if node == nil || node.Kind != core.KindResult {
		return fmt.Errorf("%s 不是结果节点", resultID)
	}
	v, err := graph.ConfirmingVerification(ctx, resultID)
	if err != nil {
		return err
	}
	var meta struct {
		Tier string `json:"verification_tier"`
	}
	_ = json.Unmarshal(v.Evidence, &meta) // 档位缺失按空交付（交付侧兜底 unknown）
	claim, err := confirmedClaim(*node, v.ID, v.Evidence, v.Repro, meta.Tier, v.DurationMs)
	if err != nil {
		return err
	}
	if err := sink.Record(ctx, claim); err != nil {
		return fmt.Errorf("补交付失败 (node %s): %w", resultID, err)
	}
	return nil
}

// findConfirmedDuplicate 在既有 verified result 中查找同指纹主张。
// 对照键是归一后的指纹规范键；未带指纹的 result 不参与对照
// （缺失指纹的发现保持原样，不影响其地位）。
func (g *PromotionGate) findConfirmedDuplicate(ctx context.Context, taskID, key string) (*explorationgraph.Node, error) {
	results, err := g.graph.ListResults(ctx, taskID)
	if err != nil {
		return nil, fmt.Errorf("verifier: 语义去重查询既有 result 失败: %w", err)
	}
	for i := range results {
		var m struct {
			Fingerprint string `json:"claim_fingerprint"`
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
	merged["claim_fingerprint"] = key
	if b, err := json.Marshal(merged); err == nil {
		return b
	}
	return evaluation
}

// withReplayLog 把最终证据、评估理由与完整重放链（预跑 + 评估者复放，含失败）聚合成一份审计 JSON。
// 保留原证据顶层字段（url/method 等）不套壳——下游（finding host 抽取等）依赖其形状。
// assertPassedInEvidence 从机器证据中读 executor 断言的命中情况。
// 证据无 assert_passed 字段（如无复现通道时的配方回显）返回 true——不阻断 judge 受理。
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
// （带 quality_gate 标记与拒绝理由）、假设节点验证状态流转为 refuted。
// 语义区别于 invalid_recipe（门故障止损）：这是验证结论——配方证明了
// 命中不构成主张的证据。
func (g *PromotionGate) refuteByQualityGate(ctx context.Context, a Claim, cause error) (*explorationgraph.Node, error) {
	evaluation, _ := json.Marshal(map[string]interface{}{
		"quality_gate":      true,
		"refute_reason":     RefuteAssertNotDiscriminative,
		"verification_tier": g.verificationTier(a.Repro),
		"reason":            cause.Error(),
		"phase":             "prerun",
	})

	_, err := g.graph.RecordVerification(ctx, explorationgraph.Verification{
		ID:        uuid.New().String(),
		TaskID:    a.TaskID,
		NodeID:    a.NodeID,
		Repro:     a.Repro,
		Outcome:   explorationgraph.VerificationRefuted,
		Evidence:  evaluation,
		CreatedAt: time.Now(),
	})
	if err != nil {
		return nil, fmt.Errorf("verifier: 质量拒绝留档失败: %w", err)
	}

	g.markHypothesisNode(ctx, a, hypothesisMark{
		status:       explorationgraph.VerificationRefuted,
		refuteReason: RefuteAssertNotDiscriminative,
	})
	if g.logger != nil {
		g.logger.Info().
			Str("node_id", a.NodeID).
			Str("reason", cause.Error()).
			Msg("质量门证伪（断言无鉴别力）")
	}
	return nil, nil
}

// refuteEarly 断言未命中时的机器证伪路径：落 verification 审计（不打扰 judge）、
// 给假设节点打 refuted 标记、发布 refuted 事件语义（node=nil）。
func (g *PromotionGate) refuteEarly(
	ctx context.Context,
	a Claim, hyp string, res Result, replays []replayRecord,
) (*explorationgraph.Node, error) {
	const verdict = VerdictRefuted
	const reasoning = "机器护栏：复现断言全部未命中（配方声称的特征未出现），无支持坐实的证据，不经评估者直接证伪"
	tier := g.verificationTier(a.Repro)
	evaluation := withReplayLog(res.Evidence, tier, verdict, reasoning, replays)

	_, err := g.graph.RecordVerification(ctx, explorationgraph.Verification{
		ID:         uuid.New().String(),
		TaskID:     a.TaskID,
		NodeID:     a.NodeID,
		Repro:      a.Repro,
		Outcome:    explorationgraph.VerificationRefuted,
		Evidence:   evaluation,
		DurationMs: res.DurationMs,
		CreatedAt:  time.Now(),
	})
	if err != nil {
		return nil, fmt.Errorf("verifier: 记录 verification 失败: %w", err)
	}
	_ = hyp // refute 路径不复用假设对象

	g.markHypothesisNode(ctx, a, hypothesisMark{
		status:       explorationgraph.VerificationRefuted,
		refuteReason: RefuteNotReproducible,
	})
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
