package einoagent

import (
	"context"
	"fmt"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/adk/prebuilt/deep"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"

	"github.com/V3teran/liusha/internal/lead"
)

// deep_swarm.go：用 eino deep prebuilt 装配 orchestrator + 杀伤链 sub-agents（orchestrator 经 deep 自带 task 工具派活）。
//
// 关键事实（源码复核 2026-06-11，见 reference_eino_vs_adk / project_eino_migration 记忆）：
//   - 共享 ChatModel 并发安全（per-hunter 独立 model「铁律」已作废）→ 所有 agent 共用一个 model
//   - 框架层不强制串行：deep task 工具是 InvokableTool（一 task=一 sub-agent），AgentTool 每次调用
//     新建私有 bridge store（非固定 checkpoint 锁），多 task 由 ToolsNode 并行执行。实际是否并发取决于
//     orchestrator 一轮发几个 task；杀伤链本就阶段串行，可接受（深度并发未经 spike 实测，按需再验）
//   - sub-agent **内部**多工具并行（eino ToolsNode 原生）
//   - 文件隔离：run_command 每命令独立临时目录（deep 无法 per-sub-agent 注入 key，einotools 侧处理）

// DeepSwarmConfig 是装配 deep orchestrator+sub-agents 所需依赖（scanner composition root 注入）。
type DeepSwarmConfig struct {
	Model        model.ToolCallingChatModel     // orchestrator + 所有 sub-agent 共享（并发安全）
	Orchestrator RoleDef                        // 主代理角色（kind=orchestrator）
	SubAgents    []RoleDef                      // 杀伤链阶段子代理角色
	ToolDeps     TrafficAnalysisToolDeps        // 工具装配依赖（store/loader/sandbox）
	Params       TrafficAnalysisToolParams      // owner/host/hunter 注入值
	Middlewares  []adk.AgentMiddleware          // 截图回灌/遥测/事件（einoRunOpts 产，struct 版）
	Handlers     []adk.ChatModelAgentMiddleware // ① summarization 上下文压缩（einoRunOpts 产，接口版 Handlers）
	MaxIteration int                            // orchestrator 迭代上限；0=用 Orchestrator.MaxIterations 或默认
}

const defaultDeepMaxIter = 300

// subAgentTargetSection 生成追加到子代理 system prompt 末尾的「固定目标 Host」段。
// host 空（如无法从 brief 抽到目标）则返回空串，不污染提示。
func subAgentTargetSection(host string) string {
	if host == "" {
		return ""
	}
	return fmt.Sprintf("\n\n## 目标 Host（本次扫描的固定目标）\n\n`%s`\n\n"+
		"所有侦察 / 利用都针对这个地址，按 `http(s)://<host>` 构造 URL。"+
		"**不要**去访问 localhost / 127.0.0.1 / 容器内网来「找」目标——目标就是上面这个 host，沙箱可直连。\n", host)
}

// leadSection 读该 host 的情报黑板并渲染成追加到子代理 system prompt 末尾的固定段（§7.5）。
// store/host 任一为空，或读取失败/无情报 → 返回空串，不污染提示。
//
// 这是"子代理看不到 orchestrator user message"的解——共享的是黑板，不是对话：不动 eino
// WithFullChatHistoryAsInput（避免 token 爆炸），而是像 subAgentTargetSection 一样结构化注入。
func leadSection(ctx context.Context, store LeadStore, host string) string {
	if store == nil || host == "" {
		return ""
	}
	grouped, err := store.ReadRecent(ctx, host)
	if err != nil {
		return ""
	}
	if section := lead.FormatSection(grouped); section != "" {
		return "\n\n" + section
	}
	return ""
}

// BuildDeepSwarm 用 deep 装配 orchestrator。
//
// orchestrator 工具集 = Orchestrator 角色声明的工具（通常只读 + 不含 write_finding，铁律靠角色 md 不声明）。
// sub-agent = 每个 SubAgents 角色一个 ChatModelAgent（共享 model + 各自工具集 + 各自 prompt）。
// orchestrator 通过 deep 的 task(subagent_type, brief) 派活给 sub-agent（串行，杀伤链）。
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
			maxIter = defaultExploitationMaxIters
		}
		sa, err := adk.NewChatModelAgent(ctx, &adk.ChatModelAgentConfig{
			Name:        role.ID,
			Description: role.Description,
			// 子代理系统提示 = 角色 md + 固定目标 Host 段 + 情报黑板段。
			// 子代理是 deep task 派的瞬时代理，只看到 orchestrator 写的 task 文案 + 自己的 system prompt，
			// 看不到 orchestrator 的 user message（带 ## 目标 Host / 情报黑板）。若 orchestrator 派活时漏写
			// 目标地址，子代理就会瞎猜 localhost/127.0.0.1（实测 recon 误扫容器内网根因）。此处结构化注入
			// 目标 host + 该 host 已有情报，不依赖 orchestrator LLM 每次都记得复述/转述——
			// 与 buildUserPrompt 给顶层 agent 注入 host/lead 同源思路。
			Instruction: role.SystemPrompt + subAgentTargetSection(cfg.Params.Host) +
				leadSection(ctx, cfg.ToolDeps.Lead, cfg.Params.Host),
			Model:         cfg.Model,
			ToolsConfig:   adk.ToolsConfig{ToolsNodeConfig: compose.ToolsNodeConfig{Tools: tools}},
			MaxIterations: maxIter,
			Middlewares:   cfg.Middlewares,
			Handlers:      cfg.Handlers, // ① summarization 压缩挂每个子代理（子代理 run 各自独立计 token）
			// 瞬时 provider 错（mimo 偶发超时/4xx/429/EOF）重试，避免一次抖动杀掉整个 sub-agent run。
			// 对齐 passive（traffic_analysis）已有的 ModelRetryConfig——active 此前缺，是扫描偶发 abort 根因。
			ModelRetryConfig: &adk.ModelRetryConfig{MaxRetries: defaultModelRetries},
		})
		if err != nil {
			return nil, fmt.Errorf("sub-agent %q: %w", role.ID, err)
		}
		subAgents = append(subAgents, sa)
	}

	// orchestrator 工具集（Orchestrator 角色 md 声明；派活由 deep 自带 task 工具负责，不在此）
	cmdTools, err := BuildRoleTools(cfg.Orchestrator, ToolBuildCtx{Deps: cfg.ToolDeps, Params: cfg.Params})
	if err != nil {
		return nil, fmt.Errorf("orchestrator 工具: %w", err)
	}

	maxIter := cfg.MaxIteration
	if maxIter <= 0 {
		maxIter = cfg.Orchestrator.MaxIterations
	}
	if maxIter <= 0 {
		maxIter = defaultDeepMaxIter
	}

	orchestrator, err := deep.New(ctx, &deep.Config{
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
		Handlers:               cfg.Handlers, // ① summarization 压缩挂 orchestrator（deep 自动下传 task 工具内子代理）
		// orchestrator 主 ChatModel 同样重试瞬时 provider 错——否则 orchestrator 一次 mimo 超时即中止整个扫描。
		ModelRetryConfig: &adk.ModelRetryConfig{MaxRetries: defaultModelRetries},
	})
	if err != nil {
		return nil, fmt.Errorf("deep.New(orchestrator): %w", err)
	}
	return orchestrator, nil
}

// RunDeepSwarm 跑一次 deep orchestrator（active 站点扫描），消费事件流收集 ToolCalls + 最终文字。
// userText = 站点任务 brief。opts 透传 Runner.Run（计费埋点 handler）。
func RunDeepSwarm(ctx context.Context, orchestrator adk.Agent, userText string, opts ...adk.AgentRunOption) (TrafficAnalysisResult, error) {
	// 不开 EnableStreaming：deep prebuilt 的 orchestrator/子代理 ChatModelAgent 总带 builtin
	// Handlers（writeTodos planning + task 派活 middleware），其 AfterModelRewriteState 需完整
	// model 输出做 state rewrite，故 eino 的 stateModelWrapper.Stream 必 ConcatMessageStream 把
	// 流式拍平成整条（vendor wrappers.go:679）——逐 token 在 deep 内部被吃掉，开 streaming 无逐字
	// 收益反多一次 stream+concat 开销。active 推理整条到达（功能完整），逐字是 deep 架构约束下的取舍。
	// 对比 passive（traffic_analysis 纯 ChatModelAgent 无 Handlers，EnableStreaming 真逐字 delta）。
	runner := adk.NewRunner(ctx, adk.RunnerConfig{Agent: orchestrator})
	iter := runner.Run(ctx, []adk.Message{schema.UserMessage(userText)}, opts...)
	return drainAgentEvents(iter, "deep-orchestrator")
}
