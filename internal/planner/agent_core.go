package planner

import (
	"slices"
	"sync"
	"time"

	"github.com/rs/zerolog"

	"github.com/V3teran/liusha/internal/bus"
	"github.com/V3teran/liusha/internal/explorationgraph"
)

// Agent 是异步规划 Agent
//
// 职责：
// - 监听 EventVerificationPassed/Refuted 事件
// - 根据探索图生成新的 Action
// - 发布 EventActionProposed 事件
type Agent struct {
	taskID        string
	agentRunID    string // 本轮认知循环的 agent_run.id（LLM 审计归属）
	graph         *explorationgraph.Store
	planner       GraphPlanner
	eventBus      bus.EventBus
	logger        zerolog.Logger
	functionTools []string      // function_tools 白名单
	pollInterval  time.Duration // 轮询间隔（作为兜底）

	// onConverged 把收敛直接通知完成检测器（控制信号，不走可丢的事件总线）。
	onConverged func(reason string)

	// 重规划请求（经 RequestReplan 发出）：replanCh 容量 1，未消费前的多次请求合并为
	// 一次，理由逐条保留（去重）。
	replanCh      chan struct{}
	replanMu      sync.Mutex
	replanReasons []string

	stopCh chan struct{}
}

// AgentConfig 配置
type AgentConfig struct {
	TaskID        string
	AgentRunID    string // 本轮认知循环的 agent_run.id（LLM 审计归属）
	Graph         *explorationgraph.Store
	Planner       GraphPlanner
	EventBus      bus.EventBus
	Logger        zerolog.Logger
	PollInterval  time.Duration       // 默认 10s
	FunctionTools []string            // function_tools 白名单（agent 配置；nil=拒绝，[]=空集，["*"]=全量）
	OnConverged   func(reason string) // 收敛通知（必填：任务结束靠它）
}

// NewAgent 创建 planner Agent。
func NewAgent(cfg AgentConfig) *Agent {
	if cfg.PollInterval == 0 {
		cfg.PollInterval = 10 * time.Second
	}

	return &Agent{
		taskID:        cfg.TaskID,
		agentRunID:    cfg.AgentRunID,
		graph:         cfg.Graph,
		planner:       cfg.Planner,
		eventBus:      cfg.EventBus,
		logger:        cfg.Logger.With().Str("agent", "planner").Logger(),
		functionTools: cfg.FunctionTools,
		pollInterval:  cfg.PollInterval,
		onConverged:   cfg.OnConverged,
		replanCh:      make(chan struct{}, 1),
		stopCh:        make(chan struct{}),
	}
}

// RequestReplan 请求一次强制重规划：不看是否还有待执行动作，立即带着理由再规划一轮。
// 未消费前的多次请求合并为一次，各来源的理由逐条保留（相同理由只留一条）。
func (a *Agent) RequestReplan(reason string) {
	a.replanMu.Lock()
	if !slices.Contains(a.replanReasons, reason) {
		a.replanReasons = append(a.replanReasons, reason)
	}
	a.replanMu.Unlock()
	select {
	case a.replanCh <- struct{}{}:
	default:
	}
}

// takeReplanReasons 取走待处理重规划请求的全部理由。
func (a *Agent) takeReplanReasons() []string {
	a.replanMu.Lock()
	defer a.replanMu.Unlock()
	reasons := a.replanReasons
	a.replanReasons = nil
	return reasons
}
