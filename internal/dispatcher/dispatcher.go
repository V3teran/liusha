// Package dispatcher — Complexity-aware Executor 工厂。
//
// 按 Action.Complexity 选 Profile，执行 Executor.Run。
package dispatcher

import (
	"context"
	"fmt"

	"github.com/V3teran/liusha/internal/executor"
	"github.com/V3teran/liusha/internal/provider"
	"github.com/V3teran/liusha/internal/registry"
	"github.com/V3teran/liusha/internal/worldmodel"
	"github.com/rs/zerolog"
)

// Profile 是针对单个 Complexity 的 Executor 配置。
type Profile struct {
	Complexity   worldmodel.Complexity
	SystemPrompt string
	Tools        []string // 允许使用的工具名列表
	Budget       executor.Budget
	Settle       executor.SettleConfig
}

// Dispatcher 是 Complexity-aware Executor 工厂。
type Dispatcher struct {
	profiles   map[worldmodel.Complexity]Profile
	provider   provider.Provider
	registry   *registry.Registry
	compactor  executor.Compactor
	checkpoint executor.CheckpointStore
	emitter    executor.SSEEmitter
	logger     zerolog.Logger
	worldmodel *worldmodel.Store
	eventBus   executor.EventBus
}

// New 构造 Dispatcher。
func New(
	p provider.Provider,
	reg *registry.Registry,
	compactor executor.Compactor,
	cp executor.CheckpointStore,
	emitter executor.SSEEmitter,
	logger zerolog.Logger,
	wm *worldmodel.Store,
	eventBus executor.EventBus,
) *Dispatcher {
	return &Dispatcher{
		profiles:   make(map[worldmodel.Complexity]Profile),
		provider:   p,
		registry:   reg,
		compactor:  compactor,
		checkpoint: cp,
		emitter:    emitter,
		logger:     logger,
		worldmodel: wm,
		eventBus:   eventBus,
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

	// 构造工具受限的 sub-registry
	subReg := d.buildSubRegistry(profile.Tools)

	// 创建 worldmodel 适配器
	wmReader := executor.NewWorldModelAdapter(d.worldmodel)

	a := executor.New(d.provider, subReg, d.compactor, d.checkpoint, d.emitter, d.logger, wmReader)

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

func (d *Dispatcher) buildSubRegistry(allowedTools []string) *registry.Registry {
	if len(allowedTools) == 0 {
		return d.registry
	}
	sub := registry.New()
	for _, name := range allowedTools {
		if t, ok := d.registry.Get(name); ok {
			sub.Register(t)
		}
	}
	return sub
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
		Hypotheses:         hypotheses,
		Budget:             profile.Budget,
		Settle:             profile.Settle,
		PendingConstraints: action.Constraints,
	}
}
