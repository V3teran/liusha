// Package dispatcher — Complexity-aware Executor 工厂。
//
// 按 Action.Complexity 选 Profile，执行 Executor.Run。
package dispatcher

import (
	"context"
	"fmt"

	"github.com/V3teran/liusha/internal/executor"
	"github.com/V3teran/liusha/internal/framework/core"
	"github.com/V3teran/liusha/internal/framework/llm"
	"github.com/V3teran/liusha/internal/framework/runtime"
	"github.com/V3teran/liusha/internal/registry"
	"github.com/V3teran/liusha/internal/knowledgegraph"
	"github.com/rs/zerolog"
)

// Profile 是针对单个 Complexity 的 Executor 配置。
type Profile struct {
	Complexity   knowledgegraph.Complexity
	SystemPrompt string
	Tools        []string // 允许使用的工具名列表
	Budget       executor.Budget
	Settle       executor.SettleConfig
}

// Dispatcher 是 Complexity-aware Executor 工厂。
type Dispatcher struct {
	profiles       map[knowledgegraph.Complexity]Profile
	provider       llm.Provider
	registry       *registry.Registry
	checkpointer   core.Checkpointer
	emitter        executor.SSEEmitter
	logger         zerolog.Logger
	knowledgegraph *knowledgegraph.Store
	eventBus       executor.EventBus
}

// New 构造 Dispatcher。
func New(
	p llm.Provider,
	reg *registry.Registry,
	checkpointer core.Checkpointer,
	emitter executor.SSEEmitter,
	logger zerolog.Logger,
	wm *knowledgegraph.Store,
	eventBus executor.EventBus,
) *Dispatcher {
	return &Dispatcher{
		profiles:       make(map[knowledgegraph.Complexity]Profile),
		provider:       p,
		registry:       reg,
		checkpointer:   checkpointer,
		emitter:        emitter,
		logger:         logger,
		knowledgegraph: wm,
		eventBus:       eventBus,
	}
}

// RegisterProfile 注册 Complexity 对应的 Profile。
func (d *Dispatcher) RegisterProfile(p Profile) {
	d.profiles[p.Complexity] = p
}

// Execute 按 Action.Complexity 选 Profile，执行 Executor，返回 Execution。
func (d *Dispatcher) Execute(ctx context.Context, action executor.Action) (executor.Execution, error) {
	profile, ok := d.profiles[action.Complexity]
	if !ok {
		return executor.Execution{}, fmt.Errorf("dispatcher: no profile for complexity %q", action.Complexity)
	}

	// 创建 worldmodel 适配器
	wmReader := executor.NewWorldModelAdapter(d.knowledgegraph)

	// 创建 Agent
	a := executor.NewAgent(d.provider, d.emitter, d.logger, wmReader, d.checkpointer, runtime.NewIterationCheckpointPolicy(3))

	// 配置 eventBus（启用 Planner → Executor 通信）
	if d.eventBus != nil {
		a = a.WithEventBus(d.eventBus)
	}

	req := d.buildExecutorReq(profile, action, nil)
	result, err := a.Run(ctx, action.ID, req)
	if err != nil {
		return executor.Execution{}, fmt.Errorf("dispatcher: executor run: %w", err)
	}

	return executor.Execution{
		Index:      0,
		Result:     result,
		Hypotheses: nil,
	}, nil
}

func (d *Dispatcher) buildExecutorReq(profile Profile, action executor.Action, hypotheses []string) executor.ExecutorReq {
	system := profile.SystemPrompt
	if len(hypotheses) > 0 {
		system += "\n\n# Working Memory\n"
		for _, h := range hypotheses {
			system += "- " + h + "\n"
		}
	}

	instruction := fmt.Sprintf("Complexity: %s\nTarget: %s\nInstruction: %s",
		action.Complexity, action.Target.Display(), action.Instruction)
	for _, cue := range action.Cues {
		instruction += "\nCue: " + cue
	}

	return executor.ExecutorReq{
		System: system,
		Inbox: []executor.Message{
			{Role: "user", Content: instruction},
		},
		Budget:             profile.Budget,
		Settle:             profile.Settle,
		PendingConstraints: action.Constraints,
	}
}
