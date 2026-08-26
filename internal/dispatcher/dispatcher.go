// Package dispatcher — Complexity-aware Actor 工厂。
//
// 按 Move.Complexity 选 Profile，循环执行 Actor.Run，Critic 给 Steer 后重跑。
package dispatcher

import (
	"context"
	"fmt"

	"github.com/V3teran/liusha/internal/actor"
	"github.com/V3teran/liusha/internal/provider"
	"github.com/V3teran/liusha/internal/registry"
)

// Profile 是针对单个 Complexity 的 Actor 配置。
type Profile struct {
	Complexity    actor.Complexity
	SystemPrompt  string
	Tools         []string // 允许使用的工具名列表
	Budget        actor.Budget
	Settle        actor.SettleConfig
	MaxExecutions int // Critic Steer 最多重跑次数，默认 3
}

// Dispatcher 是 Complexity-aware Actor 工厂。
type Dispatcher struct {
	profiles   map[actor.Complexity]Profile
	provider   provider.Provider
	registry   *registry.Registry
	compactor  actor.Compactor
	critic     *actor.LLMCritic
	checkpoint actor.CheckpointStore
	emitter    actor.SSEEmitter
}

// New 构造 Dispatcher。
func New(
	p provider.Provider,
	reg *registry.Registry,
	compactor actor.Compactor,
	critic *actor.LLMCritic,
	cp actor.CheckpointStore,
	emitter actor.SSEEmitter,
) *Dispatcher {
	return &Dispatcher{
		profiles:   make(map[actor.Complexity]Profile),
		provider:   p,
		registry:   reg,
		compactor:  compactor,
		critic:     critic,
		checkpoint: cp,
		emitter:    emitter,
	}
}

// RegisterProfile 注册 Complexity 对应的 Profile。
func (d *Dispatcher) RegisterProfile(p Profile) {
	d.profiles[p.Complexity] = p
}

// Execute 按 Move.Complexity 选 Profile，执行 Actor 循环，返回所有 Execution。
func (d *Dispatcher) Execute(ctx context.Context, move actor.Move) ([]actor.Execution, error) {
	profile, ok := d.profiles[move.Complexity]
	if !ok {
		return nil, fmt.Errorf("dispatcher: no profile for complexity %q", move.Complexity)
	}

	maxExec := profile.MaxExecutions
	if maxExec <= 0 {
		maxExec = 3
	}

	// 构造工具受限的 sub-registry
	subReg := d.buildSubRegistry(profile.Tools)

	a := actor.New(d.provider, subReg, d.compactor, d.critic, d.checkpoint, d.emitter)

	var executions []actor.Execution
	var hypotheses []string

	for i := 0; i < maxExec; i++ {
		req := d.buildActorReq(profile, move, hypotheses)
		result, err := a.Run(ctx, move.ID, req)
		if err != nil {
			return executions, fmt.Errorf("dispatcher: actor run #%d: %w", i, err)
		}

		exec := actor.Execution{
			Index:      i,
			Result:     result,
			Hypotheses: hypotheses,
		}
		executions = append(executions, exec)

		// Critic 评估
		assessment, cerr := d.critic.Evaluate(ctx, move, result.Steps)
		if cerr != nil || assessment.Verdict == actor.VerdictContinue || assessment.Verdict == actor.VerdictAbandon {
			break
		}
		// VerdictSteer：注入 steering 文本，下次 Execution 带上
		if assessment.Verdict == actor.VerdictSteer {
			executions[len(executions)-1].Steer = assessment.Observation
			hypotheses = append(hypotheses, assessment.Observation)
		}
	}
	return executions, nil
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

func (d *Dispatcher) buildActorReq(profile Profile, move actor.Move, hypotheses []string) actor.ActorReq {
	system := profile.SystemPrompt
	if len(hypotheses) > 0 {
		system += "\n\n# Working Memory\n"
		for _, h := range hypotheses {
			system += "- " + h + "\n"
		}
	}

	instruction := fmt.Sprintf("Complexity: %s\nTarget: %s\nInstruction: %s",
		move.Complexity, move.Target.Display(), move.Instruction)
	for _, cue := range move.Cues {
		instruction += "\nCue: " + cue
	}

	return actor.ActorReq{
		System: system,
		Inbox: []actor.Message{
			{Role: "user", Content: instruction},
		},
		Hypotheses:         hypotheses,
		Budget:             profile.Budget,
		Settle:             profile.Settle,
		PendingConstraints: move.Constraints,
	}
}
