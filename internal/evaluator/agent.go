// Package evaluator 提供验证层（Agent），负责验证 Observation 并决定是否晋升为 Evaluation/Result 节点
package evaluator

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/rs/zerolog"

	"github.com/V3teran/liusha/internal/bus"
	"github.com/V3teran/liusha/internal/explorationgraph"
	"github.com/V3teran/liusha/internal/framework/llm"
)

// Agent 是异步验证 Agent
//
// 职责：
// - 监听 EventAttemptGenerated 事件（executor 产出的漏洞候选 Attempt）
// - 经 PromotionEvaluator 复现门验证后晋升（对应铁律：图里只存坐实态）
// - 发布 EventVerificationPassed/Refuted 事件
type Agent struct {
	taskID        string
	evaluator     *PromotionEvaluator // 使用具体类型
	eventBus      bus.Bus
	logger        zerolog.Logger
	maxConcurrent int

	// inFlight 收口在途验证 goroutine：Run 退出前等它们落定（ctx 已取消时
	// 会快速返回），避免停机后仍向 bus/图写事件的竞态。
	inFlight sync.WaitGroup

	// adjudicated 是配方哈希 → 已裁决结论的进程内去重表：LLM 会反复重提同一
	// （或实质相同的）假设，每条都进复现门 = judge ReAct 成本翻倍 + confirmed
	// 时重复写 finding。同一配方只裁一次；进程重启即清零（跨 run 去重靠图上的
	// verification_outcome 标记让规划侧不再重提）。
	adjudicatedMu sync.Mutex
	adjudicated   map[string]string
}

// AgentConfig 配置
type AgentConfig struct {
	TaskID        string
	Evaluator     *PromotionEvaluator
	EventBus      bus.Bus
	Logger        zerolog.Logger
	MaxConcurrent int
}

// NewAgent NewEvaluatorAgent 创建 Agent。
func NewAgent(cfg AgentConfig) *Agent {
	if cfg.MaxConcurrent <= 0 {
		cfg.MaxConcurrent = 1
	}

	return &Agent{
		taskID:        cfg.TaskID,
		evaluator:     cfg.Evaluator,
		eventBus:      cfg.EventBus,
		logger:        cfg.Logger.With().Str("agent", "evaluator").Logger(),
		maxConcurrent: cfg.MaxConcurrent,

		adjudicated: map[string]string{},
	}
}

// Run 启动事件循环（ctx 取消即停止）。
func (a *Agent) Run(ctx context.Context) error {
	if a.evaluator == nil {
		return fmt.Errorf("evaluator: PromotionEvaluator is required")
	}

	// LLM 审计维度：裁决调用归 task、角色 evaluator。
	ctx = llm.WithCallMeta(ctx, llm.CallMeta{TaskID: a.taskID, Role: "evaluator"})

	a.logger.Info().Str("task_id", a.taskID).Msg("Agent 启动")

	// 订阅事件
	sub := a.eventBus.SubscribeTask(a.taskID)
	defer sub.Cancel()

	// 并发控制（信号量）
	sem := make(chan struct{}, a.maxConcurrent)

	for {
		select {
		case <-ctx.Done():
			a.logger.Info().Str("task_id", a.taskID).Msg("Agent 停止（context done），等待在途验证落定")
			a.inFlight.Wait()
			return ctx.Err()

		case event := <-sub.Events():
			if event.Type != bus.EventAttemptGenerated {
				continue
			}
			actionID, ok := event.Payload["action_id"].(string)
			if !ok {
				a.logger.Warn().Interface("payload", event.Payload).Msg("AttemptGenerated 缺少 action_id")
				continue
			}

			attemptPayload, ok := event.Payload["attempt"]
			if !ok {
				a.logger.Warn().Msg("AttemptGenerated 缺少 attempt")
				continue
			}

			// 类型断言为 Attempt
			attempt, ok := attemptPayload.(Attempt)
			if !ok {
				a.logger.Warn().Msg("attempt 类型错误")
				continue
			}

			// 异步验证：信号量在 goroutine 内获取——事件循环不在途等待，
			// 满载时排队而非停摆（ctx.Done 可即时响应）。
			a.inFlight.Add(1)
			go func(actionID string, attempt Attempt) {
				defer a.inFlight.Done()
				sem <- struct{}{}
				defer func() { <-sem }()

				a.logger.Info().
					Str("action_id", actionID).
					Msg("开始验证 Attempt")

				if err := a.verifyAttempt(ctx, actionID, attempt); err != nil {
					a.logger.Error().
						Err(err).
						Str("action_id", actionID).
						Msg("验证失败")
				}
			}(actionID, attempt)
		}
	}
}

// verifyAttempt 验证单个 Attempt
func (a *Agent) verifyAttempt(ctx context.Context, actionID string, attempt Attempt) error {
	startTime := time.Now()

	// 去重：同一配方（字节级相同）已裁决过则跳过——不再烧 judge、不再重复写 finding。
	hash := primitivesHash(attempt.Primitives)
	if prev, seen := a.adjudicatedLookup(hash); seen {
		a.logger.Info().
			Str("action_id", actionID).
			Str("node_id", attempt.NodeID).
			Str("previous_outcome", prev).
			Msg("跳过已裁决配方（去重）")
		return nil
	}

	// 调用 PromotionEvaluator 验证
	node, err := a.evaluator.Promote(ctx, attempt)
	if err != nil {
		return fmt.Errorf("promote: %w", err)
	}

	// 裁决完成（坐实或证伪）才入去重表；门出错允许重试。
	a.adjudicatedStore(hash, string(promoteOutcome(node)))

	// node == nil 表示验证证伪
	if node == nil {
		a.logger.Info().
			Str("action_id", actionID).
			Int64("duration_ms", time.Since(startTime).Milliseconds()).
			Msg("验证证伪（未晋升）")

		a.eventBus.PublishVerificationRefuted(a.taskID, actionID)
		return nil
	}

	// 验证通过，已晋升
	a.logger.Info().
		Str("action_id", actionID).
		Str("node_id", node.ID).
		Str("node_kind", string(node.Kind)).
		Int64("duration_ms", time.Since(startTime).Milliseconds()).
		Msg("验证通过，已晋升")

	// 发布验证通过事件
	a.eventBus.PublishVerificationPassed(a.taskID, node.ID)

	return nil
}

func (a *Agent) adjudicatedLookup(hash string) (string, bool) {
	a.adjudicatedMu.Lock()
	defer a.adjudicatedMu.Unlock()
	out, ok := a.adjudicated[hash]
	return out, ok
}

func (a *Agent) adjudicatedStore(hash, outcome string) {
	a.adjudicatedMu.Lock()
	defer a.adjudicatedMu.Unlock()
	a.adjudicated[hash] = outcome
}

// primitivesHash 对配方做字节级哈希（同一配方重提 = 同哈希；格式微差视为不同，
// 由图上的 verification_outcome 标记兜底防复活）。
func primitivesHash(p json.RawMessage) string {
	sum := sha256.Sum256(bytes.TrimSpace(p))
	return hex.EncodeToString(sum[:])
}

// promoteOutcome 从晋升结果导出裁决结论（nil = refuted）。
func promoteOutcome(node *explorationgraph.Node) explorationgraph.VerifyOutcome {
	if node == nil {
		return explorationgraph.OutcomeRefuted
	}
	return explorationgraph.OutcomeConfirmed
}
