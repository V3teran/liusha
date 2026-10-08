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

// Agent 是持续运行的验证 Agent
//
// 职责：
// - 主动轮询探索图中的 observation 节点
// - 筛选未验证的 observation（没有对应 result 节点的）
// - 经 PromotionEvaluator 复现门验证后晋升为 result 节点
// - 发布 EventVerificationPassed/Refuted 事件
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

	// adjudicated 是配方哈希 → 已裁决结论的进程内去重表：LLM 会反复重提同一
	// （或实质相同的）假设，每条都进复现门 = judge ReAct 成本翻倍 + confirmed
	// 时重复写 finding。同一配方只裁一次；进程重启即清零（跨 run 去重靠图上的
	// verification_outcome 标记让规划侧不再重提）。
	adjudicatedMu sync.Mutex
	adjudicated   map[string]string

	// processedObs 记录已处理的 observation ID（防止重复处理）
	processedObsMu sync.Mutex
	processedObs   map[string]bool
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

		adjudicated:  map[string]string{},
		processedObs: map[string]bool{},
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
		Msg("Evaluator Agent 启动（轮询模式）")

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

		case <-ticker.C:
			if err := a.pollAndVerify(ctx, sem); err != nil {
				a.logger.Error().Err(err).Msg("轮询验证失败")
			}
		}
	}
}

// pollAndVerify 执行一轮轮询：查询未验证的 observation 节点并启动验证
func (a *Agent) pollAndVerify(ctx context.Context, sem chan struct{}) error {
	// 1. 查询所有 observation 节点
	observations, err := a.graph.ListNodesByKind(ctx, a.taskID, "observation")
	if err != nil {
		return fmt.Errorf("查询 observation 节点失败: %w", err)
	}

	if len(observations) == 0 {
		a.logger.Debug().Msg("暂无 observation 节点")
		return nil
	}

	a.logger.Debug().Int("count", len(observations)).Msg("发现 observation 节点")

	// 2. 查询所有边，构建已处理的 observation 集合
	edges, err := a.graph.ListEdgesForAPI(ctx, a.taskID)
	if err != nil {
		a.logger.Error().Err(err).Msg("查询边失败")
		return fmt.Errorf("查询边失败: %w", err)
	}

	// 构建 observation -> result 的映射（通过 generates 边）
	obsHasResult := make(map[string]bool)
	for _, edge := range edges {
		if edge.Rel == "generates" {
			// 检查目标节点是否为 result
			// 注意：这里假设 generates 边从 observation 指向 result
			obsHasResult[edge.SrcID] = true
		}
	}

	// 3. 筛选未处理的节点
	newObservations := 0
	for _, obs := range observations {
		// 去重检查：是否已处理过此 observation
		a.processedObsMu.Lock()
		alreadyProcessed := a.processedObs[obs.ID]
		a.processedObsMu.Unlock()

		if alreadyProcessed {
			continue
		}

		// 检查是否已有对应的 result 节点（通过边关系）
		if obsHasResult[obs.ID] {
			// 已有 result，标记为已处理
			a.processedObsMu.Lock()
			a.processedObs[obs.ID] = true
			a.processedObsMu.Unlock()
			continue
		}

		// 解析 observation 中的 Attempt 数据
		attempt, err := a.parseObservationToAttempt(obs)
		if err != nil {
			a.logger.Debug().Err(err).Str("obs_id", obs.ID).Msg("解析 observation 失败，跳过")
			a.processedObsMu.Lock()
			a.processedObs[obs.ID] = true
			a.processedObsMu.Unlock()
			continue
		}

		newObservations++

		// 异步验证
		a.inFlight.Add(1)
		go func(obsID string, attempt Attempt) {
			defer a.inFlight.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			a.logger.Info().
				Str("observation_id", obsID).
				Msg("开始验证 observation")

			if err := a.verifyObservation(ctx, obsID, attempt); err != nil {
				a.logger.Error().
					Err(err).
					Str("observation_id", obsID).
					Msg("验证失败")
			}

			// 标记为已处理
			a.processedObsMu.Lock()
			a.processedObs[obsID] = true
			a.processedObsMu.Unlock()
		}(obs.ID, attempt)
	}

	if newObservations > 0 {
		a.logger.Info().Int("count", newObservations).Msg("启动验证任务")
	}

	return nil
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
		return Attempt{}, fmt.Errorf("observation 缺少 repro 字段（可能是执行状态记录，跳过）")
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

// verifyObservation 验证单个 observation（原 verifyAttempt 的逻辑）
func (a *Agent) verifyObservation(ctx context.Context, observationID string, attempt Attempt) error {
	startTime := time.Now()

	// 去重：同一配方（字节级相同）已裁决过则跳过——不再烧 judge、不再重复写 finding。
	hash := primitivesHash(attempt.Primitives)
	if prev, seen := a.adjudicatedLookup(hash); seen {
		a.logger.Info().
			Str("observation_id", observationID).
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
