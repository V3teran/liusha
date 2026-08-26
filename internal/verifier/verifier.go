// Package verifier 实现 L4 认知引擎的晋升门（Verifier）。
//
// 更新（2026-08-26）：适配统一世界模型
//
// 世界模型铁律：图里只存坐实/假定的结果态；Lead/Observation 是在途假设（Redis 黑板），
// 只有过复现才能晋升成图节点。Verifier 就是这道 **不可绕过的状态转换门** 的执法者——
// 它不取代 LLM 判断，而是给"晋升成坐实态"这个动作强制加一道复现关卡：
//
//	Lead(在途假设) → Promote(attempt)
//	    → Replayer 执行复现 → Result{confirmed, evidence}
//	    → RecordVerification(confirmed/refuted)  // 证据链，无论成败都落
//	    → confirmed: CreateNode(confidence=verified) 进图
//	    └ refuted:   不进图（证据仍留 wm_verification 供审计）
//
// domain-agnostic：复现怎么做归各域（web=replay_traffic、binary=gdb、cloud=API 调用），
// Verifier 只认 Replayer 接口，不认域——保证加新域时晋升门零改动。
package verifier

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/V3teran/liusha/internal/worldmodel"
)

// worldWriter 是 Verifier 依赖的世界模型写入子集：收窄依赖 + 便于测试替身。
// *worldmodel.Store 自动满足本接口。
type worldWriter interface {
	RecordVerification(ctx context.Context, v worldmodel.Verification) (string, error)
	CreateNode(ctx context.Context, n worldmodel.Node) (string, error)
}

// Replayer 是 domain-specific 复现执行器。Verifier 把"复现"委托给它，自身不碰域细节。
// primitives 是要回放的 L1 原语序列（形状由域定义）；返回是否坐实 + 证据。
type Replayer interface {
	Replay(ctx context.Context, primitives json.RawMessage) (Result, error)
}

// Result 是一次复现执行的结论。
type Result struct {
	Confirmed  bool            // 复现是否坐实（决定能否进图）
	Evidence   json.RawMessage // 复现证据（req/resp、崩溃现场、API 响应…）
	DurationMs int64           // 复现耗时
}

// Attempt 是一次晋升尝试的输入：要复现什么、坐实后落成哪种节点、落在哪。
type Attempt struct {
	TaskID     string              // = assignment_id，图归属（一次交战一个图）
	NodeID     string              // 溯源到的源节点 ID（可空）
	Kind       worldmodel.NodeKind // 坐实后的节点类型（observation/discovery）
	Primitives json.RawMessage     // 要回放的 L1 原语序列
	Content    json.RawMessage     // 坐实后写入节点的载荷（severity/taxonomy/evidence…）
	Priority   int                 // 优先级（discovery 通常更高）
}

// Verifier 是 Lead→图节点的晋升门。
type Verifier struct {
	world    worldWriter
	replayer Replayer
}

// New 构造 Verifier。replayer 为 nil 时 Promote 会报错（无复现能力即无晋升）。
func New(world worldWriter, replayer Replayer) *Verifier {
	return &Verifier{world: world, replayer: replayer}
}

// Promote 把一条 Lead 过复现门晋升成世界模型节点。
//
// 返回值语义：
//   - (node, nil)  复现坐实，已晋升成 verified 节点；
//   - (nil, nil)   复现证伪，未进图（证据已留 wm_verification 供审计）——非错误；
//   - (nil, err)   门本身出错（复现执行/落库失败）。
//
// 库不 log，错误上抛由 caller 记录（与 worldmodel.Store 一致）。
func (v *Verifier) Promote(ctx context.Context, a Attempt) (*worldmodel.Node, error) {
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
	outcome := worldmodel.OutcomeRefuted
	if res.Confirmed {
		outcome = worldmodel.OutcomeConfirmed
	}
	verID, err := v.world.RecordVerification(ctx, worldmodel.Verification{
		ID:         uuid.New().String(),
		TaskID:     a.TaskID,
		NodeID:     nodeID, // 预先分配，即使证伪也记录（审计需要）
		Primitives: a.Primitives,
		Outcome:    outcome,
		Evidence:   res.Evidence,
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
	verified := worldmodel.ConfVerified
	node := worldmodel.Node{
		ID:         nodeID,
		TaskID:     a.TaskID,
		Kind:       a.Kind,
		Content:    a.Content,
		Confidence: &verified,
		Priority:   a.Priority,
		SourceType: "verifier",
		SourceID:   verID,
		CreatedAt:  time.Now(),
		UpdatedAt:  time.Now(),
	}

	_, err = v.world.CreateNode(ctx, node)
	if err != nil {
		return nil, fmt.Errorf("verifier: 晋升节点失败: %w", err)
	}

	return &node, nil
}
