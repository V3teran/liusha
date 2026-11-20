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

	// onConverged 把收敛直接通知完成检测器（控制信号，不走可丢的事件总线），带上
	// 收敛所依据的目标：任务落 completed 时以它为前置，期间目标被改写则不结束任务。
	onConverged func(reason string, basis explorationgraph.GoalBasis)

	// conclusion 是上一次规划「本轮不再新增动作」的结论（停止探索或等待），图面不变时
	// 复用（见 conclusion）。只在 Run 的事件循环内读写；进程内状态，新 run 从零开始，
	// 首轮规划重新求解一次。
	conclusion *conclusion

	// 重规划请求（经 RequestReplan 发出）：replanCh 容量 1，未消费前的多次请求合并为
	// 一次，请求逐条保留（按理由去重）。
	replanCh       chan struct{}
	replanMu       sync.Mutex
	replanRequests []replanRequest

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
	PollInterval  time.Duration                                         // 默认 10s
	FunctionTools []string                                              // function_tools 白名单（agent 配置；nil=拒绝，[]=空集，["*"]=全量）
	OnConverged   func(reason string, basis explorationgraph.GoalBasis) // 收敛通知（必填：任务结束靠它）
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

// replanRequest 是一次待处理的重规划请求。seen 是发起方作出判断时所见的图面：请求只在
// 这个前提仍成立时有效（见 liveReplanReasons）；为空的请求无条件有效——目标调整发起的
// 请求，前提就是目标变了。
type replanRequest struct {
	reason string
	seen   *explorationgraph.Surface
}

// RequestReplan 请求一次强制重规划：不看是否还有待执行动作，带着理由再规划一轮。seen 是
// 发起方作出判断时所见的图面，处理时图面已有实质进展的请求作废；nil 表示无条件有效。
// 未消费前的多次请求合并为一次，各来源的请求逐条保留（相同理由只留一条）。
func (a *Agent) RequestReplan(reason string, seen *explorationgraph.Surface) {
	a.replanMu.Lock()
	if !slices.ContainsFunc(a.replanRequests, func(r replanRequest) bool { return r.reason == reason }) {
		a.replanRequests = append(a.replanRequests, replanRequest{reason: reason, seen: seen})
	}
	a.replanMu.Unlock()
	select {
	case a.replanCh <- struct{}{}:
	default:
	}
}

// takeReplanRequests 取走全部待处理的重规划请求。
func (a *Agent) takeReplanRequests() []replanRequest {
	a.replanMu.Lock()
	defer a.replanMu.Unlock()
	requests := a.replanRequests
	a.replanRequests = nil
	return requests
}
