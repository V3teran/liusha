package einoagent

import (
	"context"
	"errors"
	"fmt"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
)

// spawn.go：commander 的 spawn_striker 工具（eino 原生 swarm，替代 liusha 的 asynq subtask 机器）。
//
// 架构（见 docs/superpowers/specs/2026-06-07-eino-full-migration.md §P4）：
//   - spawn_striker 是**同步**工具：调用即建一个**全新 striker（全新 model + BuildStrikerTools）**
//     同步跑 RunStriker 返结果。
//   - 并发来自 commander 一个 turn 发**多个** spawn_striker 调用 —— eino ToolsNode 默认并行执行
//     多 tool call（compose/tool_node.go parallelRunToolCall）。
//   - 由此天然满足：①各 striker 独立 model（per-hunter 铁律，spike 实测）②真并发
//     ③done-gating（同步工具未返回，commander 不能收口）。
//   - 这删掉了 react 路径的 async spawn + subtask.Registry + PreDoneCheck + done cooldown 整套机器。

// StrikerModelFactory 产 striker 的独立 ChatModel（*einollm.Factory 自动满足）。
type StrikerModelFactory interface {
	For(ctx context.Context, role string) (model.ToolCallingChatModel, error)
}

// StrikerSpawnConfig 是 spawn_striker 工具的闭包依赖（commander 装配时注入一份，所有 spawn 共享）。
type StrikerSpawnConfig struct {
	Factory     StrikerModelFactory // 产 fresh striker model（role="striker"）
	ToolDeps    TrackerToolDeps     // BuildStrikerTools 依赖（含 commander/striker 共享 sandbox）
	Instruction string              // striker system prompt（hunter.SystemPromptFor("active", false)）

	OwnerType string
	OwnerID   string
	Host      string

	// NewHunterID 为每个 spawn **建一行 striker hunter run** 返其 id（uuid）。
	// finding/llm_invocation/tool_invocation.hunter_id 有 FK→hunter(id)，故 striker 必须先有行。
	// 由 scanner 注入（持 hunter.Store），避免 einoagent→hunter 耦合。
	NewHunterID func(ctx context.Context) (string, error)

	// OnStrikerDone 在 striker 跑完（成功/失败）后标记其 hunter run 终态。可 nil。
	OnStrikerDone func(ctx context.Context, strikerID string, runErr error)

	// BuildUserPrompt 由 scanner 注入（闭包持 hunter.Deps），把 brief + 已有 finding/notes/lesson/
	// 索引段拼成 striker 的首条 user message。nil 时退化为只发 brief。避免 einoagent→hunter 耦合。
	BuildUserPrompt func(ctx context.Context, brief, strikerHunterID string, flowID int64) string

	// RunOpts 给每个 striker 产 per-run 中间件（压缩）+ 选项（计费埋点）。可 nil。
	RunOpts func(strikerHunterID string) ([]adk.AgentMiddleware, []adk.AgentRunOption)
}

// spawnStrikerArgs 是 spawn_striker 入参。
type spawnStrikerArgs struct {
	Brief  string `json:"brief"   jsonschema:"required,description=给 striker 的完整自然语言任务指令：攻面/目标/已知证据/期望产出。striker 据此深挖单点并自行 write_finding。"`
	FlowID int64  `json:"flow_id" jsonschema:"description=可选：种子流量 id，striker 会看到该 flow 的 raw 请求+响应（做 replay/BAC 时用）"`
}

// BuildSpawnStriker 造 commander 的 spawn_striker 工具。
//
// 用法约束（commander 一次发多个 spawn_striker 并行）由 system_prompt_commander.md 给出，本工具只执行。
func BuildSpawnStriker(cfg StrikerSpawnConfig) (tool.BaseTool, error) {
	if cfg.Factory == nil || cfg.NewHunterID == nil {
		return nil, errors.New("spawn_striker: Factory / NewHunterID 必填")
	}
	return utils.InferTool(
		"spawn_striker",
		"派一个 striker 突击手深挖单点（同步执行，返回该 striker 的产出摘要）。"+
			"brief 写清攻面/目标/已知证据/期望产出，striker 会独立挖并自行 write_finding。"+
			"**要并发挖多个攻面/多类漏洞时，在同一轮里发多个 spawn_striker 调用**——它们会并行跑。"+
			"你是指挥官：协调 + 拆活，**不亲自挖洞 / 不亲自 write_finding**（撞证据也走 spawn）。",
		func(ctx context.Context, in spawnStrikerArgs) (map[string]any, error) {
			if in.Brief == "" {
				return nil, errors.New("brief 必填")
			}
			sid, err := cfg.NewHunterID(ctx) // 建 striker hunter 行（FK 完整）
			if err != nil {
				return nil, fmt.Errorf("create striker run: %w", err)
			}

			m, err := cfg.Factory.For(ctx, "striker") // 全新独立实例（铁律）
			if err != nil {
				return nil, fmt.Errorf("spawn striker model: %w", err)
			}

			params := TrackerToolParams{
				OwnerType: cfg.OwnerType,
				OwnerID:   cfg.OwnerID,
				HunterID:  sid,
				Host:      cfg.Host,
				FlowID:    in.FlowID,
			}
			tools, err := BuildStrikerTools(cfg.ToolDeps, params)
			if err != nil {
				return nil, fmt.Errorf("spawn striker tools: %w", err)
			}

			userText := in.Brief
			if cfg.BuildUserPrompt != nil {
				userText = cfg.BuildUserPrompt(ctx, in.Brief, sid, in.FlowID)
			}

			var mws []adk.AgentMiddleware
			var opts []adk.AgentRunOption
			if cfg.RunOpts != nil {
				mws, opts = cfg.RunOpts(sid)
			}

			res, runErr := RunStriker(ctx, m, tools, cfg.Instruction, userText, mws, opts...)
			if cfg.OnStrikerDone != nil {
				cfg.OnStrikerDone(ctx, sid, runErr) // 标记 striker hunter run 终态
			}
			if runErr != nil {
				return nil, fmt.Errorf("striker %s run: %w", sid, runErr)
			}
			return map[string]any{
				"striker_id": sid,
				"tool_calls": res.ToolCalls,
				"summary":    res.FinalText,
			}, nil
		})
}
