package einoagent

import (
	"context"
	"fmt"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/adk/prebuilt/deep"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
)

// deep_swarm.go：用 eino deep prebuilt 装配 commander（orchestrator）+ 杀伤链 sub-agents。
//
// 替代 spawn.go 的自定义 spawn_striker——改用 eino 原生 deep（用户决策：commander+striker=deepagent）。
// 关键事实（实测/源码，见 reference_eino_vs_adk / project_eino_migration 记忆）：
//   - 共享 ChatModel 并发安全（per-hunter 独立 model「铁律」已作废）→ 所有 agent 共用一个 model
//   - deep 的 sub-agent 间**串行**（AgentTool 固定 checkpoint）→ 符合杀伤链流程本串行
//   - sub-agent **内部**多工具并行（eino ToolsNode 原生）
//   - 文件隔离：run_command 每命令独立临时目录（deep 无法 per-sub-agent 注入 key，einotools 侧处理）

// DeepSwarmConfig 是装配 deep commander+sub-agents 所需依赖（scanner composition root 注入）。
type DeepSwarmConfig struct {
	Model        model.ToolCallingChatModel // commander + 所有 sub-agent 共享（并发安全）
	Orchestrator RoleDef                    // 主代理角色（kind=orchestrator）
	SubAgents    []RoleDef                  // 杀伤链阶段子代理角色
	ToolDeps     TrackerToolDeps            // 工具装配依赖（store/loader/sandbox）
	Params       TrackerToolParams          // owner/host/hunter 注入值
	Middlewares  []adk.AgentMiddleware      // 压缩/截图回灌/遥测（einoRunOpts 产）
	MaxIteration int                        // commander 迭代上限；0=用 Orchestrator.MaxIterations 或默认
}

const defaultDeepMaxIter = 300

// BuildDeepSwarm 用 deep 装配 commander。
//
// commander 工具集 = Orchestrator 角色声明的工具（通常只读 + 不含 write_finding，铁律靠角色 md 不声明）。
// sub-agent = 每个 SubAgents 角色一个 ChatModelAgent（共享 model + 各自工具集 + 各自 prompt）。
// commander 通过 deep 的 task(subagent_type, brief) 派活给 sub-agent（串行，杀伤链）。
func BuildDeepSwarm(ctx context.Context, cfg DeepSwarmConfig) (adk.Agent, error) {
	if cfg.Model == nil {
		return nil, fmt.Errorf("BuildDeepSwarm: Model 必填")
	}
	// 每个 sub-agent 角色 → ChatModelAgent
	subAgents := make([]adk.Agent, 0, len(cfg.SubAgents))
	for _, role := range cfg.SubAgents {
		tools, err := BuildRoleTools(role, ToolBuildCtx{Deps: cfg.ToolDeps, Params: cfg.Params})
		if err != nil {
			return nil, fmt.Errorf("sub-agent %q 工具: %w", role.ID, err)
		}
		maxIter := role.MaxIterations
		if maxIter <= 0 {
			maxIter = defaultStrikerMaxIters
		}
		sa, err := adk.NewChatModelAgent(ctx, &adk.ChatModelAgentConfig{
			Name:          role.ID,
			Description:   role.Description,
			Instruction:   role.SystemPrompt,
			Model:         cfg.Model,
			ToolsConfig:   adk.ToolsConfig{ToolsNodeConfig: compose.ToolsNodeConfig{Tools: tools}},
			MaxIterations: maxIter,
			Middlewares:   cfg.Middlewares,
		})
		if err != nil {
			return nil, fmt.Errorf("sub-agent %q: %w", role.ID, err)
		}
		subAgents = append(subAgents, sa)
	}

	// commander 工具集（Orchestrator 角色声明；不含 spawn_striker——deep 自带 task 工具派活）
	cmdTools, err := BuildRoleTools(cfg.Orchestrator, ToolBuildCtx{Deps: cfg.ToolDeps, Params: cfg.Params})
	if err != nil {
		return nil, fmt.Errorf("commander 工具: %w", err)
	}

	maxIter := cfg.MaxIteration
	if maxIter <= 0 {
		maxIter = cfg.Orchestrator.MaxIterations
	}
	if maxIter <= 0 {
		maxIter = defaultDeepMaxIter
	}

	commander, err := deep.New(ctx, &deep.Config{
		Name:                   cfg.Orchestrator.ID,
		Description:            cfg.Orchestrator.Description,
		ChatModel:              cfg.Model,
		Instruction:            cfg.Orchestrator.SystemPrompt,
		SubAgents:              subAgents,
		ToolsConfig:            adk.ToolsConfig{ToolsNodeConfig: compose.ToolsNodeConfig{Tools: cmdTools}},
		WithoutGeneralSubAgent: true, // 只用我们的杀伤链角色，不要 deep 默认 general-purpose
		WithoutWriteTodos:      true, // liusha 不用 todo；过程靠 finding/note 黑板
		MaxIteration:           maxIter,
		Middlewares:            cfg.Middlewares,
	})
	if err != nil {
		return nil, fmt.Errorf("deep.New(commander): %w", err)
	}
	return commander, nil
}

// RunDeepSwarm 跑一次 deep commander（active 站点扫描），消费事件流收集 ToolCalls + 最终文字。
// userText = 站点任务 brief。opts 透传 Runner.Run（计费埋点 handler）。
func RunDeepSwarm(ctx context.Context, commander adk.Agent, userText string, opts ...adk.AgentRunOption) (TrackerResult, error) {
	runner := adk.NewRunner(ctx, adk.RunnerConfig{Agent: commander})
	iter := runner.Run(ctx, []adk.Message{schema.UserMessage(userText)}, opts...)
	return drainAgentEvents(iter, "deep-commander")
}
