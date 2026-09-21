// Package verifier 实现认知循环的晋升门（Evaluator）。
//
// 更新（2026-08-26）：适配统一世界模型
//
// 世界模型铁律：图里只存坐实/假定的结果态；Lead/Observation 是在途假设（Redis 黑板），
// 只有过复现才能晋升成图节点。Evaluator 就是这道 **不可绕过的状态转换门** 的执法者——
// 它不取代 LLM 判断，而是给"晋升成坐实态"这个动作强制加一道复现关卡：
//
//	Lead(在途假设) → Promote(attempt)
//	    → Replayer 执行复现 → Result{confirmed, evidence}
//	    → RecordVerification(confirmed/refuted)  // 证据链，无论成败都落
//	    → confirmed: CreateNode(confidence=verified) 进图
//	    └ refuted:   不进图（证据仍留 wm_verification 供审计）
//
// domain-agnostic：复现怎么做归各域（web=replay_traffic、binary=gdb、cloud=API 调用），
// Evaluator 只认 Replayer 接口，不认域——保证加新域时晋升门零改动。
package evaluator

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/V3teran/liusha/internal/framework/core"
	"github.com/V3teran/liusha/internal/knowledgegraph"
)

// worldWriter 是 Evaluator 依赖的世界模型写入子集：收窄依赖 + 便于测试替身。
// *knowledgegraph.Store 自动满足本接口。
type worldWriter interface {
	RecordVerification(ctx context.Context, v knowledgegraph.Verification) (string, error)
	CreateNode(ctx context.Context, n knowledgegraph.Node) (string, error)
}

// findingWriter 是 Evaluator 依赖的 finding 写入子集：验证通过后才能写入 finding 表。
// *finding.Store 自动满足本接口。
type findingWriter interface {
	Save(ctx context.Context, f interface{}) (interface{}, error)
}

// Replayer 是 domain-specific 复现执行器。Evaluator 把"复现"委托给它，自身不碰域细节。
// primitives 是要回放的 L1 原语序列（形状由域定义）；返回是否坐实 + 证据。
type Replayer interface {
	Replay(ctx context.Context, primitives json.RawMessage) (Result, error)
}

// Result 是一次复现执行的结论。
type Result struct {
	Confirmed  bool            // 复现是否坐实（决定能否进图）
	Evaluation json.RawMessage // 复现证据（req/resp、崩溃现场、API 响应…）
	DurationMs int64           // 复现耗时
}

// Attempt 是一次晋升尝试的输入：要复现什么、坐实后落成哪种节点、落在哪。
type Attempt struct {
	TaskID     string              // = assignment_id，图归属（一次交战一个图）
	NodeID     string              // 溯源到的源节点 ID（可空）
	Kind       core.NodeKind // 坐实后的节点类型（observation/discovery）
	Primitives json.RawMessage     // 要回放的 L1 原语序列
	Content    json.RawMessage     // 坐实后写入节点的载荷（severity/taxonomy/evidence…）
	Priority   string              // 优先级（critical/high/medium/low）
}

// Evaluator 是 Lead→图节点的晋升门。
type PromotionEvaluator struct {
	world    worldWriter
	replayer Replayer
	findings findingWriter // 验证通过后写入 finding 表
}

// New 构造 Evaluator。replayer 为 nil 时 Promote 会报错（无复现能力即无晋升）。
func New(world worldWriter, replayer Replayer, findings findingWriter) *PromotionEvaluator {
	return &PromotionEvaluator{
		world:    world,
		replayer: replayer,
		findings: findings,
	}
}

// Promote 把一条 Lead 过复现门晋升成世界模型节点。
//
// 返回值语义：
//   - (node, nil)  复现坐实，已晋升成 verified 节点；
//   - (nil, nil)   复现证伪，未进图（证据已留 wm_verification 供审计）——非错误；
//   - (nil, err)   门本身出错（复现执行/落库失败）。
//
// 库不 log，错误上抛由 caller 记录（与 knowledgegraph.Store 一致）。
func (v *PromotionEvaluator) Promote(ctx context.Context, a Attempt) (*knowledgegraph.Node, error) {
	if a.TaskID == "" {
		return nil, fmt.Errorf("verifier: Attempt.TaskID 必填")
	}
	if a.Kind == "" {
		return nil, fmt.Errorf("verifier: Attempt.Kind 必填")
	}
	if v.replayer == nil {
		return nil, fmt.Errorf("verifier: 无 Replayer，无法复现晋升")
	}

	res, err := v.replayer.Replay(ctx, a.Primitives)
	if err != nil {
		return nil, fmt.Errorf("verifier: 复现执行失败: %w", err)
	}

	// 生成节点 ID（预先分配）
	nodeID := uuid.New().String()

	// 证据链：无论坐实与否都落 wm_verification（refuted 也留档供审计/复盘）。
	outcome := knowledgegraph.OutcomeRefuted
	if res.Confirmed {
		outcome = knowledgegraph.OutcomeConfirmed
	}
	verID, err := v.world.RecordVerification(ctx, knowledgegraph.Verification{
		ID:         uuid.New().String(),
		TaskID:     a.TaskID,
		NodeID:     nodeID, // 预先分配，即使证伪也记录（审计需要）
		Primitives: a.Primitives,
		Outcome:    outcome,
		Evaluation:   res.Evaluation,
		DurationMs: res.DurationMs,
		CreatedAt:  time.Now(),
	})
	if err != nil {
		return nil, fmt.Errorf("verifier: 记录 verification 失败: %w", err)
	}

	// 证伪：不进图。铁律——图只存坐实态。
	if !res.Confirmed {
		return nil, nil
	}

	// 坐实：晋升成 verified 节点
	verified := knowledgegraph.ConfidenceVerified
	node := knowledgegraph.Node{
		ID:         nodeID,
		TaskID:     a.TaskID,
		Kind:       a.Kind,
		Content:    a.Content,
		Confidence: &verified,
		Priority:   knowledgegraph.Priority(a.Priority),
		SourceType: knowledgegraph.SourceEvaluator,
		SourceID:   verID,
		CreatedAt:  time.Now(),
		UpdatedAt:  time.Now(),
	}

	_, err = v.world.CreateNode(ctx, node)
	if err != nil {
		return nil, fmt.Errorf("verifier: 晋升节点失败: %w", err)
	}

	// 验证通过，写入 finding 表（未验证的不进 finding 表）
	if v.findings != nil {
		if err := v.writeFinding(ctx, node, a, res); err != nil {
			// finding 写入失败不阻塞晋升（节点已进图），仅记录警告
			// TODO: 可考虑加 logger 记录
			_ = err
		}
	}

	return &node, nil
}

// writeFinding 将验证通过的节点写入 finding 表

// writeFinding 将验证通过的节点写入 finding 表
func (v *PromotionEvaluator) writeFinding(ctx context.Context, node knowledgegraph.Node, attempt Attempt, res Result) error {
	// 只有 Result 类节点（包含漏洞）才写入 finding 表
	if node.Kind != core.KindResult {
		return nil
	}

	// 解析 node.Content 提取漏洞信息
	var content map[string]interface{}
	if err := json.Unmarshal(node.Content, &content); err != nil {
		return fmt.Errorf("解析节点内容失败: %w", err)
	}

	summary, _ := content["summary"].(string)
	if summary == "" {
		summary = fmt.Sprintf("Verified vulnerability (node %s)", node.ID)
	}

	severity, _ := content["severity"].(string)
	if severity == "" {
		severity = "medium" // 默认中危
	}

	host, _ := content["host"].(string)
	if host == "" {
		host = "unknown" // 降级处理
	}

	// 构造 finding（使用 map 避免循环依赖 finding 包）
	findingData := map[string]interface{}{
		"task_id":  node.TaskID,
		"host":     host,
		"summary":  summary,
		"severity": severity,
		"evaluation": map[string]interface{}{
			"node_id":         node.ID,
			"verification_id": node.SourceID,
			"confirmed":       res.Confirmed,
			"evaluation":      res.Evaluation,
			"duration_ms":     res.DurationMs,
		},
		"target": content["target"],
		"repro":  attempt.Primitives, // 复现配方
	}

	// 写入 finding 表（通过 interface{} 避免循环依赖）
	_, err := v.findings.Save(ctx, findingData)
	return err
}
