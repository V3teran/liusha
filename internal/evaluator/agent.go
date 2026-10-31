// Package evaluator 提供验证层（Agent），负责验证 Observation 并决定是否晋升为 Result 节点
package evaluator

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog"

	"github.com/V3teran/liusha/internal/bus"
	"github.com/V3teran/liusha/internal/explorationgraph"
	"github.com/V3teran/liusha/internal/framework/llm"
	"github.com/V3teran/liusha/internal/repro"
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
// - 经 PromotionGate 复现门验证后晋升为 result 节点
// - 发布 EventVerificationPassed/Refuted 事件
//
// 去重分两层：
//   - 持久层：假设节点的 confidence（unverified → verified/refuted）是裁决
//     状态的唯一事实源，进程重启后轮询按它圈定剩余工作，不重验已裁决项；
//   - 进程层：inFlight 集合防止事件与轮询并发触发同一假设；adjudicated
//     表按裁决键（假设+配方联合哈希）去重——LLM 重提同一假设的同一配方时
//     沿用既有裁决，不再烧 judge。
type Agent struct {
	taskID        string
	agentRunID    string         // 本轮认知循环的 agent_run.id（LLM 审计归属）
	gate          *PromotionGate // 使用具体类型
	eventBus      bus.EventBus
	graph         *explorationgraph.Store // 探索图存储
	logger        zerolog.Logger
	maxConcurrent int
	pollInterval  time.Duration // 轮询间隔

	// inFlight 收口在途验证 goroutine：Run 退出前等它们落定（ctx 已取消时
	// 会快速返回），避免停机后仍向 bus/图写事件的竞态。
	inFlight sync.WaitGroup

	// verifying 是在途假设集合（观察节点 ID → 锁定时间），事件与轮询并发触发时先到先得。
	// 记录锁定时间，支持超时检测
	verifyingMu sync.Mutex
	verifying   map[string]time.Time

	// lockTimeout 是验证锁的超时时间（默认10分钟）
	// 超过此时间的锁将被强制释放，防止goroutine卡死导致永久锁定
	lockTimeout time.Duration

	// verifyAttempts 记录各假设的验证失败次数（达上限后标记裁决失败出圈）。
	verifyAttemptsMu sync.Mutex
	verifyAttempts   map[string]int

	// adjudicated 是裁决键（假设+配方联合哈希）→ 既有裁决的进程内去重表。
	// 持久去重靠假设节点的 confidence 流转；本表只省"同裁决键重复进 judge"的钱。
	adjudicatedMu sync.Mutex
	adjudicated   map[string]priorVerdict
}

// priorVerdict 是同裁决键命中时沿用的既有结论：裁决对象是"假设+配方"，
// 沿用时反驳原因与验证档位一并流转，保持图上标记形状一致。
type priorVerdict struct {
	outcome      string
	refuteReason string
	tier         string
}

// AgentConfig 配置
type AgentConfig struct {
	TaskID        string
	AgentRunID    string // 本轮认知循环的 agent_run.id（LLM 审计归属）
	Gate          *PromotionGate
	EventBus      bus.EventBus
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
		gate:          cfg.Gate,
		eventBus:      cfg.EventBus,
		graph:         cfg.Graph,
		logger:        cfg.Logger.With().Str("agent", "evaluator").Logger(),
		maxConcurrent: cfg.MaxConcurrent,
		pollInterval:  cfg.PollInterval,

		verifying:      map[string]time.Time{}, // 改为记录时间
		verifyAttempts: map[string]int{},
		adjudicated:    map[string]priorVerdict{},
		lockTimeout:    10 * time.Minute, // 验证锁超时时间
	}
}

// Run 启动轮询循环（ctx 取消即停止）。
func (a *Agent) Run(ctx context.Context) error {
	if a.gate == nil {
		return fmt.Errorf("evaluator: PromotionGate is required")
	}
	if a.graph == nil {
		return fmt.Errorf("evaluator: Graph is required")
	}

	// LLM 审计维度：裁决调用归 task/本轮 run、角色 evaluator。
	ctx = llm.WithCallMeta(ctx, llm.CallMeta{TaskID: a.taskID, AgentRunID: a.agentRunID, Role: "evaluator"})

	a.logger.Info().
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
			a.logger.Info().Msg("Agent 停止（context done），等待在途验证落定")
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
// 添加锁超时检测，如果锁已超时则强制释放并重新获取
func (a *Agent) tryAcquire(observationID string) bool {
	a.verifyingMu.Lock()
	defer a.verifyingMu.Unlock()

	if lockTime, exists := a.verifying[observationID]; exists {
		// 检查锁是否超时
		if time.Since(lockTime) < a.lockTimeout {
			return false // 锁还在有效期内，拒绝重复获取
		}

		// 锁已超时，强制释放
		a.logger.Warn().
			Str("observation_id", observationID).
			Dur("locked_for", time.Since(lockTime)).
			Dur("timeout", a.lockTimeout).
			Msg("验证锁超时，强制释放并重新获取")
	}

	a.verifying[observationID] = time.Now()
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

	// 无配方的 Attempt 不是可裁决假设——执行记录（降级模式推测、上下文结论）
	// 不参与验证生命周期（图级 confidence 只属于带 repro 的假设）。放行会让
	// 它们在裁决门里必然失败：烧掉裁决预算、以 invalid_recipe 污染 refuted
	// 分类（planner 会把执行记录误读成"可重试的失败假设"）、审计链还会因
	// 空 primitives 违反 NOT NULL 丢记录。执行记录保持 confidence 为空，
	// 留在图上作审计面。
	if len(attempt.Primitives) == 0 {
		a.logger.Info().
			Str("observation_id", attempt.NodeID).
			Msg("Attempt 无复现配方（执行记录非假设），不进入裁决生命周期")
		return
	}

	// 门侧归一化：与轮询路径（parseObservationToAttempt）对称——裁决入口
	// 永远不信任生产侧的形状。矫正失败保留原样，复放会以确定性错误终局，
	// 归一化只救该救的，不掩盖真损坏。
	if normalized, _, nErr := repro.NormalizeReproEnvelope(attempt.Primitives); nErr == nil {
		attempt.Primitives = normalized
	}

	observationID := attempt.NodeID

	a.logger.Info().
		Str("observation_id", observationID).
		Str("action_id", event.ActionID).
		Msg("收到 EventAttemptGenerated，立即验证")

	a.dispatchVerification(ctx, observationID, attempt, sem)
}

// pollAndVerify 圈定待裁决假设（confidence=unverified）并逐个过复现门。
//
// evaluator 的管辖范围只有假设：事实（factual）是 executor 写入即终态的
// 观察，出处即信任，无流转、无晋升、不消耗裁决预算——planner 直接从图
// 读取。result 节点因此只承载经裁决坐实的主张，不承载事实。
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
		// 带 confidence=unverified 的假设必有 repro（写入侧不变式）；content
		// 解析失败才是真损坏（executor 写入侧 content 必为合法 JSON），持续
		// 告警。repro 形状问题已在 parse 内宽容化（保留原始配方继续派发）。
		attempt, err := a.parseObservationToAttempt(obs)
		if err != nil {
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

		// 添加panic recovery，确保defer a.release()一定执行
		defer func() {
			if r := recover(); r != nil {
				a.logger.Error().
					Interface("panic", r).
					Str("observation_id", observationID).
					Msg("验证goroutine panic，已捕获并释放锁")
			}
		}()

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
//
// 两类错误不计入重试阶梯：
//   - 瞬时故障（网络/限流/超时）——重试有意义；
//   - 确定性配方缺陷（JSON 形状/缺字段）——重放同样配方必然同样失败，
//     立即终局出圈，不烧重试阶梯（通用原则：确定性错误 fail-fast，只有
//     瞬时错误才值得退避重试）。
func (a *Agent) recordVerifyFailure(ctx context.Context, observationID string, attempt Attempt, cause error) {
	if isContextTimeoutError(cause) {
		a.logger.Info().
			Str("observation_id", observationID).
			Msg("验证因超时失败（不计入失败上限，将重试）")
		return
	}

	if isTransientProviderError(cause) {
		a.logger.Info().
			Str("observation_id", observationID).
			Msg("验证因 provider 瞬时故障失败（不计入失败上限）")
		return
	}

	a.verifyAttemptsMu.Lock()
	if isDeterministicRecipeError(cause) {
		// 确定性缺陷：重试无意义，直接视为耗尽（-1 为随后的 ++ 预留，
		// 审计计数与真实尝试次数一致）
		a.verifyAttempts[observationID] = maxVerifyAttempts - 1
	}
	a.verifyAttempts[observationID]++
	attempts := a.verifyAttempts[observationID]
	a.verifyAttemptsMu.Unlock()

	if attempts < maxVerifyAttempts {
		return
	}

	// 耗尽出圈的 refute_reason 分流：配方确定性缺陷 → invalid_recipe（planner
	// 视为换配方才有意义的方向）；裁决官侧基础设施失败（LLM 调用/输出解析
	// 反复失败）→ adjudication_failed——机器重放可能全过、主张可能为真，
	// 只是裁决环节没跑通，planner 可原方向重试。混标 invalid_recipe 会把
	// 可重试方向误判成配方作废（实测真漏洞因此出圈）。
	refuteReason := RefuteInvalidRecipe
	adjudicationFailed := errors.Is(cause, ErrAdjudicationUnavailable)
	if adjudicationFailed {
		refuteReason = RefuteAdjudicationFailed
	}

	a.logger.Warn().
		Str("observation_id", observationID).
		Str("refute_reason", refuteReason).
		Int("attempts", attempts).
		Msg("验证反复失败达上限，标记裁决失败出圈")

	mark, mErr := json.Marshal(map[string]interface{}{
		"verification_outcome": refuteReason,
		"refute_reason":        refuteReason,
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

	// 审计链留档：invalid_recipe / adjudication_failed 都是裁决结论（前者是
	// 配方无法被本门执行，后者是裁决环节不可用），证伪/失败都落
	// exploration_verification，保证全链路可回放。
	audit := map[string]interface{}{
		"refute_reason":   refuteReason,
		"reason":          cause.Error(),
		"verify_attempts": attempts,
	}
	if refuteReason == RefuteInvalidRecipe {
		audit["invalid_recipe"] = true
	}
	if _, vErr := a.graph.RecordVerification(ctx, explorationgraph.Verification{
		ID:         uuid.New().String(),
		TaskID:     a.taskID,
		NodeID:     observationID,
		Primitives: coalescePrimitives(attempt.Primitives),
		Outcome:    explorationgraph.OutcomeRefuted,
		Evaluation: mustJSON(audit),
		CreatedAt:  time.Now(),
	}); vErr != nil {
		a.logger.Warn().Err(vErr).Str("observation_id", observationID).Msg("invalid_recipe 审计留档失败")
	}

	a.eventBus.PublishVerificationRefuted(a.taskID, observationID)
}

// coalescePrimitives 把空配方归一为空对象：primitives 落库列 NOT NULL，
// 审计记录绝不能因载荷形状静默丢失（曾致 7 条 invalid_recipe 裁决零审计可查）。
func coalescePrimitives(p json.RawMessage) json.RawMessage {
	if len(p) == 0 {
		return json.RawMessage(`{}`)
	}
	return p
}

var mustJSON = explorationgraph.MustJSON

// parseObservationToAttempt 从 observation 节点解析出 Attempt
func (a *Agent) parseObservationToAttempt(obs explorationgraph.Node) (Attempt, error) {
	var content struct {
		Statement   string          `json:"statement"`
		Severity    string          `json:"severity"`
		Remediation string          `json:"remediation"`         // 修复建议——坐实后随 finding 落台账
		Confidence  string          `json:"executor_confidence"` // executor 自评置信度的载荷键（图级 confidence 另有其位）
		Repro       json.RawMessage `json:"repro"`
	}

	if err := json.Unmarshal(obs.Content, &content); err != nil {
		return Attempt{}, fmt.Errorf("解析 observation content 失败: %w", err)
	}

	if len(content.Repro) == 0 {
		return Attempt{}, fmt.Errorf("observation 缺少 repro 字段")
	}

	// 归一化 repro：处理数组格式、缺失 domain 等历史兼容问题。
	// 归一化失败不整条丢弃——与事件路径（handleEvent）同口径：保留原始配方
	// 继续派发，由复现门/裁决官终局（确定性配方缺陷 fail-fast 出圈；generic
	// 域 judge_only 无机器断言也可裁决）。此前整条丢弃会让该假设永远
	// unverified：轮询每 5s 重解析重告警（实测同一节点连告 39 次），且
	// hasPendingVerdicts 永真——planner 永不收敛，任务只能被外部终止。
	normalized, hint, err := repro.NormalizeReproEnvelope(content.Repro)
	if err != nil {
		a.logger.Warn().Err(err).Str("observation_id", obs.ID).
			Msg("repro 归一化失败，保留原始配方继续派发（由裁决门终局）")
		normalized = content.Repro
	}
	if hint != "" {
		a.logger.Debug().
			Str("observation_id", obs.ID).
			Str("hint", hint).
			Msg("repro 归一化提示")
	}

	// web domain的assert降级处理
	// 如果web domain的assert只有description，缺少机器可验证字段（body_contains/status_code等），
	// 则自动降级为generic domain，避免验证失败
	normalized = a.downgradeWebAssertIfNeeded(obs.ID, normalized)

	// severity 缺省交领域交付层补（finding 映射处有默认）——通用裁决层
	// 不制造领域评级的默认值；confidence 是 executor 自评载荷，缺省 low。
	severity := content.Severity
	confidence := content.Confidence
	if confidence == "" {
		confidence = "low"
	}

	// 构造 Content（用于晋升后的节点）
	attContent, err := json.Marshal(map[string]string{
		"summary":     content.Statement,
		"severity":    severity,
		"confidence":  confidence,
		"remediation": content.Remediation,
	})
	if err != nil {
		return Attempt{}, fmt.Errorf("构造 content 失败: %w", err)
	}

	return Attempt{
		TaskID:     a.taskID,
		NodeID:     obs.ID,
		Kind:       "result",   // 晋升为 result 节点
		Primitives: normalized, // 使用归一化后的repro
		Content:    attContent,
		Priority:   "medium",
	}, nil
}

// downgradeWebAssertIfNeeded 检查web domain的assert是否完整，如果不完整则降级为generic。
//
// web 域 assert 只有 description（人类可读）而缺机器可验证字段时，复放门
// 无从判定——降级为 generic 域由裁决官语义取证兜底，而不是必然证伪。
func (a *Agent) downgradeWebAssertIfNeeded(obsID string, repro json.RawMessage) json.RawMessage {
	var env struct {
		Domain string          `json:"domain"`
		Recipe json.RawMessage `json:"recipe"`
		Assert json.RawMessage `json:"assert"`
	}

	if err := json.Unmarshal(repro, &env); err != nil {
		return repro // 解析失败，保持原样
	}

	// 只处理web domain
	if env.Domain != "web" {
		return repro
	}

	// 检查assert是否有机器可验证的字段
	var assert struct {
		StatusCode      *int     `json:"status_code"`
		BodyContains    []string `json:"body_contains"`
		BodyNotContains []string `json:"body_not_contains"`
		HeaderContains  []string `json:"header_contains"`
		MinDurationMs   *int     `json:"min_duration_ms"`
		Description     string   `json:"description"` // 只有description不够
	}

	if err := json.Unmarshal(env.Assert, &assert); err != nil {
		return repro // 解析失败，保持原样
	}

	// 判断是否只有description，缺少机器可验证字段
	hasVerifiableField := assert.StatusCode != nil ||
		len(assert.BodyContains) > 0 ||
		len(assert.BodyNotContains) > 0 ||
		len(assert.HeaderContains) > 0 ||
		assert.MinDurationMs != nil

	if hasVerifiableField {
		return repro // assert完整，保持原样
	}

	// assert不完整，降级为generic
	a.logger.Warn().
		Str("observation_id", obsID).
		Msg("web domain observation的assert只有description，缺少机器可验证字段，自动降级为generic domain")

	// 构造generic domain的repro
	// generic domain不需要assert，由judge LLM直接裁决
	genericEnv := map[string]interface{}{
		"domain": "generic",
		"recipe": map[string]interface{}{
			"original_domain": "web",
			"reason":          "assert字段不完整，缺少body_contains/status_code等机器可验证字段",
			"description":     assert.Description,
		},
		"assert": map[string]interface{}{
			"description": assert.Description,
		},
	}

	downgraded, err := json.Marshal(genericEnv)
	if err != nil {
		a.logger.Error().Err(err).Msg("降级为generic失败，保持原样")
		return repro
	}

	return downgraded
}

// verifyObservation 验证单个 observation。
// 裁决落定（坐实/证伪）后假设节点的 confidence 随之流转，轮询不再圈定；
// 门本身出错则保持 unverified，下轮轮询自然重试。
func (a *Agent) verifyObservation(ctx context.Context, observationID string, attempt Attempt) error {
	startTime := time.Now()

	// 去重：同一裁决键（假设+配方）已裁决过则沿用结论——不再烧 judge、不再重复写 finding。
	hash := attemptHash(attempt)
	if prev, seen := a.adjudicatedLookup(hash); seen {
		a.logger.Info().
			Str("observation_id", observationID).
			Str("previous_outcome", prev.outcome).
			Msg("同裁决键已裁决，沿用既有结论")
		return a.adoptPriorVerdict(ctx, observationID, prev)
	}

	// 设置验证超时时间为10分钟（从默认5分钟增加）
	// LLM验证可能需要较长时间（复现+裁决），避免过早超时
	verifyCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()

	// 调用 PromotionGate 验证
	node, err := a.gate.Promote(verifyCtx, attempt)
	if err != nil {
		// 区分超时错误和其他错误
		if isContextTimeoutError(err) {
			a.logger.Warn().
				Err(err).
				Str("observation_id", observationID).
				Int64("duration_ms", time.Since(startTime).Milliseconds()).
				Msg("验证超时（LLM响应慢或复现耗时长），将重试")
			// 超时不计入失败次数，直接返回允许重试
			return fmt.Errorf("verification timeout (will retry): %w", err)
		}
		return fmt.Errorf("promote: %w", err)
	}

	// 裁决完成（坐实或证伪）才入去重表；门出错允许重试。
	a.adjudicatedStore(hash, a.priorVerdictFrom(ctx, observationID, node))

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

// adoptPriorVerdict 把既有裁决结论落到当前假设节点上（裁决键命中的同假设
// 同配方假设）：confidence 与 verification_outcome 同步流转，并注明系沿用。
func (a *Agent) adoptPriorVerdict(ctx context.Context, observationID string, prev priorVerdict) error {
	confidence := explorationgraph.ConfidenceRefuted
	if prev.outcome == string(explorationgraph.OutcomeConfirmed) {
		confidence = explorationgraph.ConfidenceVerified
	}

	mark := map[string]interface{}{
		"verification_outcome": prev.outcome,
		"confidence":           confidence,
		"verification_at":      time.Now().Format(time.RFC3339),
		"verdict_source":       "same-verdict-key-dedup",
	}
	if prev.refuteReason != "" {
		mark["refute_reason"] = prev.refuteReason
	}
	if prev.tier != "" {
		mark["verification_tier"] = prev.tier
	}
	m, err := json.Marshal(mark)
	if err != nil {
		return err
	}

	if err := a.graph.UpdateNodeMetadata(ctx, observationID, m); err != nil {
		return fmt.Errorf("沿用既有裁决写标记失败: %w", err)
	}

	if confidence == explorationgraph.ConfidenceVerified {
		a.eventBus.PublishVerificationPassed(a.taskID, observationID)
	} else {
		a.eventBus.PublishVerificationRefuted(a.taskID, observationID)
	}
	return nil
}

// priorVerdictFrom 从假设节点刚落定的审判标记导出去重条目（outcome +
// refute_reason + verification_tier）：同裁决键沿用时标记形状保持一致。
// 读不到标记时退化为仅 outcome——去重不因读取失败失效。
func (a *Agent) priorVerdictFrom(ctx context.Context, observationID string, node *explorationgraph.Node) priorVerdict {
	pv := priorVerdict{outcome: string(promoteOutcome(node))}
	if a.graph == nil { // 测试替身可无图运行——去重退化为仅 outcome
		return pv
	}
	obs, err := a.graph.GetNode(ctx, observationID)
	if err != nil || obs.Metadata == nil {
		return pv
	}
	var m struct {
		RefuteReason     string `json:"refute_reason"`
		VerificationTier string `json:"verification_tier"`
	}
	if json.Unmarshal(obs.Metadata, &m) == nil {
		pv.refuteReason = m.RefuteReason
		pv.tier = m.VerificationTier
	}
	return pv
}

func (a *Agent) adjudicatedLookup(key string) (priorVerdict, bool) {
	a.adjudicatedMu.Lock()
	defer a.adjudicatedMu.Unlock()
	out, ok := a.adjudicated[key]
	return out, ok
}

func (a *Agent) adjudicatedStore(key string, v priorVerdict) {
	a.adjudicatedMu.Lock()
	defer a.adjudicatedMu.Unlock()
	a.adjudicated[key] = v
}

// attemptHash 对裁决对象（假设陈述 + 配方）联合哈希：裁决针对的是"假设是否
// 坐实"，裸配方相同而陈述不同（同一请求既被当 XSS 也被当 SQLi 提交）是两次
// 独立裁决，不得沿用结论。陈述取 Attempt.Content.summary（事件/轮询两条
// 路径都写它）。
func attemptHash(a Attempt) string {
	h := sha256.New()
	h.Write([]byte(attemptStatement(a)))
	h.Write([]byte{0}) // 定界符：防"陈述A+配方B"与"陈述AB+配方"拼接碰撞
	h.Write(bytes.TrimSpace(a.Primitives))
	return hex.EncodeToString(h.Sum(nil))
}

// attemptStatement 从 Attempt.Content 提取假设陈述（缺失返回空串——
// 此时退化为纯配方哈希，与旧行为等价）。
func attemptStatement(a Attempt) string {
	var c struct {
		Summary string `json:"summary"`
	}
	if json.Unmarshal(a.Content, &c) == nil {
		return c.Summary
	}
	return ""
}

// promoteOutcome 从晋升结果导出裁决结论（nil = refuted）。
func promoteOutcome(node *explorationgraph.Node) explorationgraph.VerifyOutcome {
	if node == nil {
		return explorationgraph.OutcomeRefuted
	}
	return explorationgraph.OutcomeConfirmed
}

// isTransientProviderError 判断是否 provider 侧瞬时故障（网络抖动/DNS/限流/
// 服务端过载/超时）：重试有意义，不该计入配方损坏的止损上限。
// 委托框架层 llm.IsTransient——全系统唯一的瞬时错误分类事实源。
func isTransientProviderError(err error) bool {
	return llm.IsTransient(err)
}

// isContextTimeoutError 判断错误是否为context超时错误
// 用于区分超时错误和其他错误，超时错误不计入失败次数
func isContextTimeoutError(err error) bool {
	if err == nil {
		return false
	}
	// 检查是否是context.DeadlineExceeded
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	// 检查错误消息中是否包含超时关键字
	msg := strings.ToLower(err.Error())
	for _, token := range []string{"deadline exceeded", "context deadline", "timeout", "超时"} {
		if strings.Contains(msg, token) {
			return true
		}
	}
	return false
}

// isDeterministicRecipeError 判断错误是否为确定性配方缺陷（JSON 形状漂移 /
// 缺字段 / 结构非法）：同一配方重放必然同样失败，重试阶梯无意义。
// 词表覆盖 replayer/归一化器的全部确定性错误文案。
func isDeterministicRecipeError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	for _, token := range []string{
		"unmarshal",         // json 形状不匹配（cannot unmarshal array/scalar...）
		"解析失败",              // 归一化器/信封/assert 解析拒绝
		"解析 assert 失败",      // replayer assert 解析
		"解析 recipe 失败",      // replayer recipe 解析
		"解析域信封失败",           // replayer 信封解析
		"assert 为空",         // 无坐实谓词
		"recipe 为空",         // 无复现步骤
		"缺少 recipe.request", // web 域缺请求
		"缺少 domain",         // 缺域信封
		"缺少 repro",          // observation 无配方
		"已废弃",               // 旧引用格式
		"未知复现域",             // 未知 domain
		"未知域",               // replayer 未知域
		"url 必须是完整 url",     // request.url 形状
	} {
		if strings.Contains(msg, token) {
			return true
		}
	}
	return false
}
