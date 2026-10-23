package planner

import (
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
	planner       Planner
	eventBus      bus.Bus
	logger        zerolog.Logger
	functionTools []string      // function_tools 白名单
	pollInterval  time.Duration // 轮询间隔（作为兜底）

	// analyzedResultIDs 是 AnalyzeResults 的水位：已分析过的 Result 节点不再
	// 重复送 LLM（否则任务后半程每轮轮询都全量重分析，烧 token 且灌重复结论）。
	analyzedResultIDs map[string]bool

	// 收敛去重：上轮规划结论为「无新方向」后记录图终态指纹；后续唤醒（验证
	// 事件成串到达/定时器）若指纹无变化则跳过 LLM 重新审议——实测任务尾段
	// planner 对同一结论重复审议 5 次（~18% LLM 浪费）。任何终态变迁（新
	// result/裁决落定/新动作写入）都会改变指纹、恢复审议。
	convergedPending bool
	convergedAt      graphFingerprint

	stopCh chan struct{}
}

// AgentConfig 配置
type AgentConfig struct {
	TaskID        string
	AgentRunID    string // 本轮认知循环的 agent_run.id（LLM 审计归属）
	Graph         *explorationgraph.Store
	Planner       Planner
	EventBus      bus.Bus
	Logger        zerolog.Logger
	PollInterval  time.Duration // 默认 10s
	FunctionTools []string      // function_tools 白名单（agent 配置；nil=拒绝，[]=空集，["*"]=全量）
}

// NewAgent 创建 planner Agent。
func NewAgent(cfg AgentConfig) *Agent {
	if cfg.PollInterval == 0 {
		cfg.PollInterval = 10 * time.Second
	}

	return &Agent{
		taskID:            cfg.TaskID,
		agentRunID:        cfg.AgentRunID,
		graph:             cfg.Graph,
		planner:           cfg.Planner,
		eventBus:          cfg.EventBus,
		logger:            cfg.Logger.With().Str("agent", "planner").Logger(),
		functionTools:     cfg.FunctionTools,
		pollInterval:      cfg.PollInterval,
		analyzedResultIDs: make(map[string]bool),
		stopCh:            make(chan struct{}),
	}
}

// Run 启动规划主循环（ctx 取消即停止）。
// 启动 Agent 主循环，监听事件并生成新的 Action
