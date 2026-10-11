// Package evaluator 提供验证层（Agent），负责验证 Observation 并决定是否晋升为 Result 节点
package evaluator

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog"

	"github.com/V3teran/liusha/internal/bus"
	"github.com/V3teran/liusha/internal/explorationgraph"
	"github.com/V3teran/liusha/internal/framework/llm"
	"github.com/google/uuid"
)

// pollBatchSize 是单轮轮询最多圈定的待裁决假设数。裁决是重操作
// （机器复放 + judge ReAct），轮询按批推进即可。
const pollBatchSize = 32

// maxVerifyAttempts 是同一假设的验证失败上限：超过后标记为裁决失败并出圈
// （复现配方反复执行失败意味着配方损坏，重试无意义——烧 LLM 不解决问题）。
const maxVerifyAttempts = 3

// Agent 是持续运行的验证 Agent
//
// 职责：
// - 事件驱动 + 轮询兜底地圈定未裁决的假设（observation，confidence=unverified）
// - 经 PromotionEvaluator 复现门验证后晋升为 result 节点
// - 发布 EventVerificationPassed/Refuted 事件
//
// 去重分两层：
//   - 持久层：假设节点的 confidence（unverified → verified/refuted）是裁决
//     状态的唯一事实源，进程重启后轮询按它圈定剩余工作，不重验已裁决项；
//   - 进程层：inFlight 集合防止事件与轮询并发触发同一假设；adjudicated
//     表按配方哈希去重——LLM 重提同一（字节级相同）配方时沿用既有裁决，
//     不再烧 judge。
type Agent struct {
	taskID        string
	agentRunID    string              // 本轮认知循环的 agent_run.id（LLM 审计归属）
	evaluator     *PromotionEvaluator // 使用具体类型
	eventBus      bus.Bus
	graph         *explorationgraph.Store // 探索图存储
	logger        zerolog.Logger
	maxConcurrent int
	pollInterval  time.Duration // 轮询间隔

	// inFlight 收口在途验证 goroutine：Run 退出前等它们落定（ctx 已取消时
	// 会快速返回），避免停机后仍向 bus/图写事件的竞态。
	inFlight sync.WaitGroup

	// verifying 是在途假设集合（观察节点 ID），事件与轮询并发触发时先到先得。
	verifyingMu sync.Mutex
	verifying   map[string]bool

	// verifyAttempts 记录各假设的验证失败次数（达上限后标记裁决失败出圈）。
	verifyAttemptsMu sync.Mutex
	verifyAttempts   map[string]int

	// adjudicated 是配方哈希 → 已裁决结论的进程内去重表。
	// 持久去重靠假设节点的 confidence 流转；本表只省"同配方重复进 judge"的钱。
	adjudicatedMu sync.Mutex
	adjudicated   map[string]string
}

// AgentConfig 配置
type AgentConfig struct {
	TaskID        string
	AgentRunID    string // 本轮认知循环的 agent_run.id（LLM 审计归属）
	Evaluator     *PromotionEvaluator
	EventBus      bus.Bus
	Graph         *explorationgraph.Store // 探索图存储
	Logger        zerolog.Logger
	MaxConcurrent int
	PollInterval  time.Duration // 轮询间隔，默认 5 秒
}

// NewAgent NewEvaluatorAgent 创建 Agent。
func NewAgent(cfg AgentConfig) *Agent {
	if cfg.MaxConcurrent <= 0 {
		cfg.MaxConcurrent = 1
	}
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = 5 * time.Second // 默认 5 秒轮询一次
	}

	return &Agent{
		taskID:        cfg.TaskID,
		agentRunID:    cfg.AgentRunID,
		evaluator:     cfg.Evaluator,
		eventBus:      cfg.EventBus,
		graph:         cfg.Graph,
		logger:        cfg.Logger.With().Str("agent", "evaluator").Logger(),
		maxConcurrent: cfg.MaxConcurrent,
		pollInterval:  cfg.PollInterval,

		verifying:      map[string]bool{},
		verifyAttempts: map[string]int{},
		adjudicated:    map[string]string{},
	}
}

// Run 启动轮询循环（ctx 取消即停止）。
func (a *Agent) Run(ctx context.Context) error {
	if a.evaluator == nil {
		return fmt.Errorf("evaluator: PromotionEvaluator is required")
	}
	if a.graph == nil {
		return fmt.Errorf("evaluator: Graph is required")
	}

	// LLM 审计维度：裁决调用归 task/本轮 run、角色 evaluator。
	ctx = llm.WithCallMeta(ctx, llm.CallMeta{TaskID: a.taskID, AgentRunID: a.agentRunID, Role: "evaluator"})

	a.logger.Info().
		Str("task_id", a.taskID).
		Dur("poll_interval", a.pollInterval).
		Msg("Evaluator Agent 启动（混合模式：事件驱动 + 轮询兜底）")

	// 订阅事件总线
	sub := a.eventBus.SubscribeTask(a.taskID)
	defer sub.Cancel()

	// 并发控制（信号量）
	sem := make(chan struct{}, a.maxConcurrent)

	ticker := time.NewTicker(a.pollInterval)
	defer ticker.Stop()

	// 首次立即执行一轮
	if err := a.pollAndVerify(ctx, sem); err != nil {
		a.logger.Error().Err(err).Msg("首次轮询失败")
	}

	for {
		select {
		case <-ctx.Done():
			a.logger.Info().Str("task_id", a.taskID).Msg("Agent 停止（context done），等待在途验证落定")
			a.inFlight.Wait()
			return ctx.Err()

		case event := <-sub.Events():
			// 事件驱动：立即处理新 observation
			a.handleEvent(ctx, event, sem)

		case <-ticker.C:
			// 轮询兜底：检查遗漏的 observation
			if err := a.pollAndVerify(ctx, sem); err != nil {
				a.logger.Error().Err(err).Msg("轮询验证失败")
			}
		}
	}
}

// tryAcquire 尝试把假设标记为"验证中"。返回 false 表示已在途，跳过。
func (a *Agent) tryAcquire(observationID string) bool {
	a.verifyingMu.Lock()
	defer a.verifyingMu.Unlock()
	if a.verifying[observationID] {
		return false
	}
	a.verifying[observationID] = true
	return true
}

// release 解除"验证中"标记（裁决落定或出错重试前调用）。
func (a *Agent) release(observationID string) {
	a.verifyingMu.Lock()
	defer a.verifyingMu.Unlock()
	delete(a.verifying, observationID)
}

// handleEvent 处理事件总线推送的事件（事件驱动）
func (a *Agent) handleEvent(ctx context.Context, event bus.Event, sem chan struct{}) {
	// 只处理 EventAttemptGenerated
	if event.Type != bus.EventAttemptGenerated {
		return
	}

	// 从 Payload 提取 attempt
	attemptData, ok := event.Payload["attempt"]
	if !ok {
		a.logger.Warn().
			Str("event_type", string(event.Type)).
			Msg("EventAttemptGenerated 缺少 attempt")
		return
	}

	// 类型断言为 Attempt（executor 发布的是 evaluator.Attempt）
	attempt, ok := attemptData.(Attempt)
	if !ok {
		a.logger.Warn().
			Str("event_type", string(event.Type)).
			Str("attempt_type", fmt.Sprintf("%T", attemptData)).
			Msg("attempt 类型不匹配")
		return
	}

	observationID := attempt.NodeID

	a.logger.Info().
		Str("observation_id", observationID).
		Str("action_id", event.ActionID).
		Msg("收到 EventAttemptGenerated，立即验证")

	a.dispatchVerification(ctx, observationID, attempt, sem)
}

// pollAndVerify 圈定待裁决假设（confidence=unverified）并逐个验证。
func (a *Agent) pollAndVerify(ctx context.Context, sem chan struct{}) error {
	observations, err := a.graph.ListObservationsByConfidence(
		ctx, a.taskID, explorationgraph.ConfidenceUnverified, pollBatchSize)
	if err != nil {
		return fmt.Errorf("查询待裁决 observation 失败: %w", err)
	}

	if len(observations) == 0 {
		return nil
	}

	a.logger.Debug().Int("count", len(observations)).Msg("发现待裁决 observation")

	dispatched := 0
	for _, obs := range observations {
		// 解析 observation 中的 Attempt 数据
		attempt, err := a.parseObservationToAttempt(obs)
		if err != nil {
			// 带 confidence=unverified 的假设必有 repro（写入侧不变式）；
			// 解析失败意味着数据损坏，持续告警直到修复。
			a.logger.Warn().Err(err).Str("obs_id", obs.ID).Msg("解析 observation 失败（疑似数据损坏）")
			continue
		}

		a.dispatchVerification(ctx, obs.ID, attempt, sem)
		dispatched++
	}

	if dispatched > 0 {
		a.logger.Info().Int("count", dispatched).Msg("启动验证任务")
	}

	return nil
}

// dispatchVerification 异步验证单个假设（信号量控制并发，先到先得防重入）。
func (a *Agent) dispatchVerification(ctx context.Context, observationID string, attempt Attempt, sem chan struct{}) {
	// 先尝试获取 verifying 锁
	if !a.tryAcquire(observationID) {
		a.logger.Debug().
			Str("observation_id", observationID).
			Msg("observation 验证中，跳过并发触发")
		return
	}

	// 启动 goroutine，在其中获取信号量
	a.inFlight.Add(1)
	go func() {
		defer a.inFlight.Done()
		defer a.release(observationID) // 确保释放 verifying 锁

		// 获取信号量（可能阻塞，但不影响其他 observation 的调度）
		select {
		case sem <- struct{}{}:
			defer func() { <-sem }()
		case <-ctx.Done():
			a.logger.Debug().
				Str("observation_id", observationID).
				Msg("等待信号量期间 ctx 取消，跳过验证")
			return
		}

		a.logger.Info().
			Str("observation_id", observationID).
			Msg("开始验证 observation")

		if err := a.verifyObservation(ctx, observationID, attempt); err != nil {
			a.logger.Error().
				Err(err).
				Str("observation_id", observationID).
				Msg("验证失败（保持 unverified，下轮轮询重试）")
			a.recordVerifyFailure(ctx, observationID, attempt, err)
		}
	}()
}

// recordVerifyFailure 记一次验证失败；达上限后把假设标记为裁决失败并出圈
// （confidence=refuted + verification_outcome=invalid_recipe），轮询不再圈定，
// 审计链留档失败原因。
func (a *Agent) recordVerifyFailure(ctx context.Context, observationID string, attempt Attempt, cause error) {
	if isTransientProviderError(cause) {
		a.logger.Info().
			Str("observation_id", observationID).
			Msg("验证因 provider 瞬时故障失败（不计入失败上限）")
		return
	}

	a.verifyAttemptsMu.Lock()
	a.verifyAttempts[observationID]++
	attempts := a.verifyAttempts[observationID]
	a.verifyAttemptsMu.Unlock()

	if attempts < maxVerifyAttempts {
		return
	}

	a.logger.Warn().
		Str("observation_id", observationID).
		Int("attempts", attempts).
		Msg("验证反复失败达上限，标记裁决失败出圈")

	mark, mErr := json.Marshal(map[string]interface{}{
		"verification_outcome": "invalid_recipe",
		"confidence":           explorationgraph.ConfidenceRefuted,
		"failure_reason":       cause.Error(),
		"verify_attempts":      attempts,
	})
	if mErr != nil {
		return
	}
	if uErr := a.graph.UpdateNodeMetadata(ctx, observationID, mark); uErr != nil {
		a.logger.Error().Err(uErr).Str("observation_id", observationID).Msg("裁决失败标记写入失败")
	}

	// 审计链留档：invalid_recipe 也是裁决结论（该假设无法被本门裁决），
	// 证伪/失败都落 exploration_verification，保证全链路可回放。
	if _, vErr := a.graph.RecordVerification(ctx, explorationgraph.Verification{
		ID:         uuid.New().String(),
		TaskID:     a.taskID,
		NodeID:     observationID,
		Primitives: attempt.Primitives,
		Outcome:    explorationgraph.OutcomeRefuted,
		Evaluation: mustJSON(map[string]interface{}{
			"invalid_recipe":  true,
			"reason":          cause.Error(),
			"verify_attempts": attempts,
		}),
		CreatedAt: time.Now(),
	}); vErr != nil {
		a.logger.Warn().Err(vErr).Str("observation_id", observationID).Msg("invalid_recipe 审计留档失败")
	}

	a.eventBus.PublishVerificationRefuted(a.taskID, observationID)
}

// mustJSON 序列化（失败回空对象——审计字段不阻塞主流程）。
func mustJSON(v interface{}) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return b
}

// parseObservationToAttempt 从 observation 节点解析出 Attempt
func (a *Agent) parseObservationToAttempt(obs explorationgraph.Node) (Attempt, error) {
	var content struct {
		Statement string          `json:"statement"`
		Severity  string          `json:"severity"`
		Repro     json.RawMessage `json:"repro"`
	}

	if err := json.Unmarshal(obs.Content, &content); err != nil {
		return Attempt{}, fmt.Errorf("解析 observation content 失败: %w", err)
	}

	if len(content.Repro) == 0 {
		return Attempt{}, fmt.Errorf("observation 缺少 repro 字段")
	}

	// 设置默认 severity
	severity := content.Severity
	if severity == "" {
		severity = "medium"
	}

	// 构造 Content（用于晋升后的节点）
	attContent, err := json.Marshal(map[string]string{
		"summary":  content.Statement,
		"severity": severity,
	})
	if err != nil {
		return Attempt{}, fmt.Errorf("构造 content 失败: %w", err)
	}

	return Attempt{
		TaskID:     a.taskID,
		NodeID:     obs.ID,
		Kind:       "result", // 晋升为 result 节点
		Primitives: content.Repro,
		Content:    attContent,
		Priority:   "medium",
	}, nil
}

// verifyObservation 验证单个 observation。
// 裁决落定（坐实/证伪）后假设节点的 confidence 随之流转，轮询不再圈定；
// 门本身出错则保持 unverified，下轮轮询自然重试。
func (a *Agent) verifyObservation(ctx context.Context, observationID string, attempt Attempt) error {
	startTime := time.Now()

	// 去重：同一配方（字节级相同）已裁决过则沿用结论——不再烧 judge、不再重复写 finding。
	hash := primitivesHash(attempt.Primitives)
	if prev, seen := a.adjudicatedLookup(hash); seen {
		a.logger.Info().
			Str("observation_id", observationID).
			Str("previous_outcome", prev).
			Msg("同配方已裁决，沿用既有结论")
		return a.adoptPriorVerdict(ctx, observationID, prev)
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
			Str("observation_id", observationID).
			Int64("duration_ms", time.Since(startTime).Milliseconds()).
			Msg("验证证伪（未晋升）")

		a.eventBus.PublishVerificationRefuted(a.taskID, observationID)
		return nil
	}

	// 验证通过，已晋升
	a.logger.Info().
		Str("observation_id", observationID).
		Str("node_id", node.ID).
		Str("node_kind", string(node.Kind)).
		Int64("duration_ms", time.Since(startTime).Milliseconds()).
		Msg("验证通过，已晋升")

	// 发布验证通过事件
	a.eventBus.PublishVerificationPassed(a.taskID, node.ID)

	return nil
}

// adoptPriorVerdict 把既有裁决结论落到当前假设节点上（配方哈希命中的
// 同配方假设）：confidence 与 verification_outcome 同步流转，并注明系沿用。
func (a *Agent) adoptPriorVerdict(ctx context.Context, observationID, outcome string) error {
	confidence := explorationgraph.ConfidenceRefuted
	if outcome == string(explorationgraph.OutcomeConfirmed) {
		confidence = explorationgraph.ConfidenceVerified
	}

	mark, err := json.Marshal(map[string]interface{}{
		"verification_outcome": outcome,
		"confidence":           confidence,
		"verification_at":      time.Now().Format(time.RFC3339),
		"verdict_source":       "same-recipe-dedup",
	})
	if err != nil {
		return err
	}

	if err := a.graph.UpdateNodeMetadata(ctx, observationID, mark); err != nil {
		return fmt.Errorf("沿用既有裁决写标记失败: %w", err)
	}

	if confidence == explorationgraph.ConfidenceVerified {
		a.eventBus.PublishVerificationPassed(a.taskID, observationID)
	} else {
		a.eventBus.PublishVerificationRefuted(a.taskID, observationID)
	}
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
// 由假设节点的 confidence 流转兜底防复活）。
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

// isTransientProviderError 判断是否 provider 侧瞬时故障（限流/服务端过载）：
// 重试有意义，不该计入配方损坏的止损上限。
func isTransientProviderError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	for _, token := range []string{"429", "Too Many Requests", "rate limit", "速率限制", "529"} {
		if strings.Contains(msg, token) {
			return true
		}
	}
	return false
}
