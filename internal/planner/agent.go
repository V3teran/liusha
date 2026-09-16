package planner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/rs/zerolog"

	"github.com/V3teran/liusha/internal/controlplane"
	"github.com/V3teran/liusha/internal/framework/core"
	"github.com/V3teran/liusha/internal/executor"
	"github.com/V3teran/liusha/internal/framework/llm"
	"github.com/V3teran/liusha/internal/framework/runtime"
	"github.com/V3teran/liusha/internal/knowledgegraph"
	"github.com/V3teran/liusha/internal/tooladapter"
)

// 编译时检查接口实现
var _ core.Agent = (*Agent)(nil)

// Agent 是事件驱动的 Planner Agent，通过统一的 ReActRuntime 产出 Action
//
// Phase 1 架构：
// - 完全使用 ReActRuntime（移除旧 agentcore）
// - 集成 Checkpoint 系统支持长期运行
// - MessageModifier 链（预留接口）
type Agent struct {
	reactRuntime runtime.ReActRuntime      // ReAct 运行时（唯一引擎）
	taskID       string
	eventBus     *executor.PlannerEventBus // Task 级事件总线（接收触发）
	actionBus    *core.Bus             // Action 级事件总线（发送控制）
	world        *knowledgegraph.Store
	controlPlane *controlplane.Store
	router       *llm.Router
	logger       zerolog.Logger

	// Checkpoint 系统
	checkpointer     core.Checkpointer
	checkpointPolicy runtime.CheckpointPolicy

	stopCh            chan struct{}
	initialPlanDoneCh chan struct{} // 初始规划完成信号
}

// Config 配置 Planner Agent
type Config struct {
	TaskID       string
	EventBus     *executor.PlannerEventBus // Task 级事件总线
	ActionBus    *core.Bus             // Action 级事件总线
	World        *knowledgegraph.Store
	ControlPlane *controlplane.Store
	Router       *llm.Router
	Logger       zerolog.Logger

	// Checkpoint 配置（可选）
	Checkpointer     core.Checkpointer        // nil 表示禁用 checkpoint
	CheckpointPolicy runtime.CheckpointPolicy // nil 使用默认策略
}

// New 创建新的 Planner Agent
func New(cfg Config) *Agent {
	// 创建 ReAct 运行时
	reactRuntime := runtime.NewReActRuntime()

	// 创建工具并用适配器包装
	legacyTools := []tooladapter.LegacyTool{
		NewObserveRoadmapTool(cfg.World, cfg.Logger),
		NewGenerateRoadmapTool(cfg.World, cfg.Logger),
		NewObserveStateTool(cfg.World),
		NewProposeActionsTool(cfg.World, cfg.Logger),
		NewEvaluateProgressTool(cfg.World),
	}

	// 注册到 ReActRuntime
	for _, tool := range legacyTools {
		adapted := tooladapter.NewAdapter(tool)
		if err := reactRuntime.RegisterTool(adapted); err != nil {
			cfg.Logger.Warn().Err(err).Str("tool", tool.Name()).Msg("注册工具失败")
		}
	}

	// 默认 Checkpoint 策略：每 5 次迭代保存
	checkpointPolicy := cfg.CheckpointPolicy
	if checkpointPolicy == nil && cfg.Checkpointer != nil {
		checkpointPolicy = runtime.NewIterationCheckpointPolicy(5)
	}

	return &Agent{
		taskID:            cfg.TaskID,
		reactRuntime:      reactRuntime,
		eventBus:          cfg.EventBus,
		actionBus:         cfg.ActionBus,
		world:             cfg.World,
		controlPlane:      cfg.ControlPlane,
		router:            cfg.Router,
		logger:            cfg.Logger,
		checkpointer:      cfg.Checkpointer,
		checkpointPolicy:  checkpointPolicy,
		stopCh:            make(chan struct{}),
		initialPlanDoneCh: make(chan struct{}),
	}
}

// Start 启动 Planner Agent 的事件循环（阻塞运行）
//
// Planner 职责：纯规划
// - 响应事件（ActionCompleted, FindingVerified 等）
// - 调用 LLM 生成新的 Actions
// - 不负责监察（由 Monitor Agent 负责）
func (a *Agent) Start(ctx context.Context) error {
	a.logger.Info().Str("task_id", a.taskID).Msg("Planner Agent 启动")

	// 订阅事件
	events := a.eventBus.Subscribe(a.taskID)
	defer a.eventBus.Unsubscribe(a.taskID)

	// 初始规划：关键路径，失败则中止任务
	// 与后续 replan 不同：初始 planning 是任务的前置条件，必须成功
	if err := a.performInitialPlanning(ctx); err != nil {
		// 通知失败（Executor 会看到 channel 关闭但没有成功标记）
		close(a.initialPlanDoneCh)
		return fmt.Errorf("初始规划失败，任务中止: %w", err)
	}

	// 通知初始规划成功
	close(a.initialPlanDoneCh)

	for {
		select {
		case <-ctx.Done():
			a.logger.Info().Str("task_id", a.taskID).Msg("Planner Agent 停止（context 取消）")
			return ctx.Err()

		case <-a.stopCh:
			a.logger.Info().Str("task_id", a.taskID).Msg("Planner Agent 停止")
			return nil

		case event := <-events:
			a.logger.Debug().
				Str("task_id", a.taskID).
				Str("event_type", string(event.Type)).
				Msg("收到事件")

			if err := a.replan(ctx, event); err != nil {
				a.logger.Error().Err(err).Str("event_type", string(event.Type)).Msg("重规划失败")
			}
		}
	}
}

// performInitialPlanning 执行初始规划，带重试逻辑
//
// 设计要点：
//   - 初始规划是任务的前置条件，失败则任务无法执行
//   - 重试 3 次，指数退避（2s, 4s, 6s）
//   - 客户端错误（401, 403）不重试，立即失败
//   - 网络错误、限流、服务端错误会重试
func (a *Agent) performInitialPlanning(ctx context.Context) error {
	const maxAttempts = 3
	baseDelay := 2 * time.Second

	event := executor.Event{
		Type:   executor.EventTaskStarted,
		TaskID: a.taskID,
	}

	var lastErr error

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		a.logger.Info().
			Int("attempt", attempt).
			Int("max_attempts", maxAttempts).
			Str("task_id", a.taskID).
			Msg("尝试初始规划")

		err := a.replan(ctx, event)

		if err == nil {
			a.logger.Info().
				Int("attempt", attempt).
				Str("task_id", a.taskID).
				Msg("初始规划成功")
			return nil
		}

		lastErr = err

		a.logger.Warn().
			Err(err).
			Int("attempt", attempt).
			Str("task_id", a.taskID).
			Msg("初始规划尝试失败")

		// 检查是否是明确的客户端错误（不应重试）
		if isClientError(err) {
			a.logger.Error().
				Err(err).
				Str("task_id", a.taskID).
				Msg("初始规划失败（客户端错误，不可重试）")
			return fmt.Errorf("初始规划失败（客户端错误）: %w", err)
		}

		// 还有重试机会
		if attempt < maxAttempts {
			// 指数退避
			delay := time.Duration(attempt) * baseDelay
			a.logger.Info().
				Dur("delay", delay).
				Str("task_id", a.taskID).
				Msg("指数退避后重试初始规划")

			select {
			case <-time.After(delay):
				// 继续下一次尝试
			case <-ctx.Done():
				return fmt.Errorf("重试退避期间 context 取消: %w", ctx.Err())
			}
		}
	}

	return fmt.Errorf("初始规划失败（%d 次尝试后）: %w", maxAttempts, lastErr)
}

// isClientError 判断是否是客户端错误（不应重试的错误）
//
// 包括：
//   - 401 Unauthorized: API Key 无效
//   - 403 Forbidden: 权限不足
//   - 400 Bad Request: 请求格式错误
//   - context.Canceled: 用户主动取消
func isClientError(err error) bool {
	if err == nil {
		return false
	}

	// context 取消不应重试
	if errors.Is(err, context.Canceled) {
		return true
	}

	// 检查错误消息中的 HTTP 状态码
	// 注意：这是启发式判断，理想情况应该用类型断言
	errMsg := err.Error()
	return strings.Contains(errMsg, "401") ||
		strings.Contains(errMsg, "403") ||
		strings.Contains(errMsg, "400")
}

// Stop 停止 Planner Agent
// Stop 实现 core.Agent 接口（优雅关闭）
func (a *Agent) Stop(ctx context.Context) error {
	a.logger.Info().Msg("停止 planner agent")
	close(a.stopCh)
	return nil
}

// WaitInitialPlanDone 等待初始规划完成
func (a *Agent) WaitInitialPlanDone() <-chan struct{} {
	return a.initialPlanDoneCh
}

// replan 执行重新规划（使用 ReActRuntime）
//
// Phase 1 架构决策：
// 1. Checkpoint 恢复：从保存点继续迭代（避免重复计算）
// 2. MessageModifier 错误：fail-fast + 可配置重试（预留）
// 3. Checkpoint 存储：PG + Redis 双层（预留，当前使用内存）
func (a *Agent) replan(ctx context.Context, event executor.Event) error {
	startTime := time.Now()

	// 将 task_id 注入 context（工具需要）
	ctx = context.WithValue(ctx, "task_id", a.taskID)

	// 构建用户提示词（包含当前状态和事件）
	userPrompt, err := a.buildUserPrompt(ctx, event)
	if err != nil {
		return fmt.Errorf("构建用户提示词: %w", err)
	}

	// 获取 LLM 提供者
	provider, err := a.router.For(ctx, llm.ComplexityComplex)
	if err != nil {
		return fmt.Errorf("获取 LLM 提供者: %w", err)
	}

	// 配置 ReAct 运行时
	config := &runtime.ReActConfig{
		Objective:            userPrompt,
		SystemPrompt:         a.buildSystemPrompt(),
		LLMProvider:          provider,
		ModelID:              "deepseek-chat",
		MaxIterations:        100,
		Temperature:          0.7,
		MaxTokens:            8000,
		Tools:                a.reactRuntime.GetTools(),
		MessageModifierChain: runtime.NewPlannerModifierChain(),

		// Checkpoint 集成（从保存点继续）
		Checkpointer:     a.checkpointer,
		CheckpointPolicy: a.checkpointPolicy,
		TaskID:           a.taskID,
		// RestoreFromCheckpoint: 由上层调用方指定（首次为空，恢复时传入）

		OnIteration: func(iteration int, status runtime.IterationStatus) {
			a.logger.Info().
				Str("task_id", a.taskID).
				Int("iteration", iteration).
				Str("status", string(status)).
				Msg("[PLANNER] ReAct 迭代")
		},
	}

	// 运行 ReAct 循环
	result, err := a.reactRuntime.Run(ctx, config)
	if err != nil {
		return fmt.Errorf("ReAct 运行时执行: %w", err)
	}

	// 记录执行结果
	a.logger.Info().
		Str("task_id", a.taskID).
		Int("iterations", result.Iterations).
		Str("status", string(result.Status)).
		Dur("duration", time.Since(startTime)).
		Msg("规划完成")

	// 检查执行状态
	if result.Status == runtime.ReActStatusError {
		return fmt.Errorf("ReAct 执行失败: %w", result.Error)
	}

	return nil
}

// buildSystemPrompt 构建系统提示词
func (a *Agent) buildSystemPrompt() string {
	return `你是一个探索式任务规划 Agent。你的职责是：

1. 生成和维护任务的 Roadmap（路线图）
2. 根据执行结果动态调整 Roadmap
3. 确保探索式任务稳步推进

## 核心概念

### Roadmap（路线图）
- Roadmap 是任务的高层规划，由 10-15 个步骤组成
- 每个步骤是一个可验证的里程碑（中粒度目标）
- 步骤之间可以有依赖关系
- Roadmap 是动态的，会根据执行结果更新

### RoadmapStep（步骤）
- objective: 步骤目标（自然语言描述）
- step: 步骤编号（1.0, 2.0, ...，支持小数如 1.5）
- depends_on: 依赖的步骤编号（高层依赖）
- status: todo（待执行）/active（执行中）/complete（已完成）/skipped（已跳过）

### Action（动作）
- 从 RoadmapStep 派发的实际执行任务
- 一个 Step 可以派发多个 Action（1:N 映射）
- Action 有低层依赖（depends_on，限定在同一 Step 内）

## 可用工具

### observe_roadmap
观察当前任务的 Roadmap 状态

### generate_roadmap
生成或更新 Roadmap（完全替换式更新）

参数：
- steps: 完整的步骤列表（10-15 个步骤）
  - step: 步骤编号（1.0, 2.0, ...）
  - objective: 步骤目标（如"识别 Web 服务类型并测试常见漏洞"）
  - depends_on: 依赖的步骤编号（可选）
  - rationale: 为什么规划这一步（可选，用于调试）

### observe_state
观察当前世界模型状态（目标、Action、观察、发现）

### propose_actions（兼容旧逻辑，优先使用 Roadmap）
生成新的 Action

## 规划原则

1. **中粒度步骤**：每个步骤应该是可验证的里程碑，不要太粗（"信息收集"）也不要太细（"扫描 80 端口"）
   - 好的例子："端口扫描和服务识别"、"测试 SQL 注入"、"测试 XSS"
   - 不好的例子："信息收集"（太粗）、"curl http://target"（太细）

2. **探索式规划**：
   - 初始规划：生成粗略的 Roadmap（10-15 步）
   - 动态调整：根据执行结果，插入、修改或跳过步骤
   - 完全替换：每次更新时重新生成完整 Roadmap（简化 LLM 认知负担）

3. **依赖管理**：
   - 高层依赖：Step 2 依赖 Step 1（通过 RoadmapStep.depends_on）
   - 低层依赖：Action 之间的依赖（通过 Action.depends_on，限定在同一 Step 内）

4. **逐步推进**：
   - 初始规划后，只派发第一个 Step 的 Action
   - Step 完成后，被唤醒，派发下一个 Step
   - 根据发现动态调整后续步骤

5. **状态转换**：
   - Step: todo → active（派发了第一个 Action）
   - Step: active → complete（所有 Action 完成）
   - Step: todo/active → skipped（决定跳过）

## 工作流程

### 初始规划（任务启动时）
1. 使用 observe_state 观察任务目标
2. 使用 generate_roadmap 生成初始 Roadmap（10-15 步）
3. 使用 propose_actions 为第一个 Step 派发 Action

### 动态调整（Step 完成后）
1. 使用 observe_roadmap 查看当前 Roadmap
2. 使用 observe_state 查看执行结果和新发现
3. 根据结果调整 Roadmap：
   - 插入新步骤（如发现新攻击面）
   - 修改后续步骤（如改变目标）
   - 跳过不需要的步骤（如目标已达成）
4. 使用 generate_roadmap 保存更新后的 Roadmap
5. 使用 propose_actions 为下一个可执行的 Step 派发 Action

## 输出格式

使用工具调用输出，不要输出纯文本解释。优先使用 Roadmap 机制。`

}

// buildUserPrompt 构建用户提示词
func (a *Agent) buildUserPrompt(ctx context.Context, event executor.Event) (string, error) {
	prompt := fmt.Sprintf("事件类型：%s\n\n", event.Type)

	switch event.Type {
	case executor.EventTaskStarted:
		prompt += "任务刚刚启动，请生成初始 Action。\n"
	case executor.EventActionCompleted:
		actionID, ok := event.Payload["action_id"].(string)
		if !ok || actionID == "" {
			return "", fmt.Errorf("EventActionCompleted 缺少 action_id")
		}
		prompt += fmt.Sprintf("Action %s 已完成，请根据新状态重新规划。\n", actionID)
	case executor.EventVerificationPassed:
		nodeID, ok := event.Payload["node_id"].(string)
		if !ok || nodeID == "" {
			return "", fmt.Errorf("EventVerificationPassed 缺少 node_id")
		}
		prompt += fmt.Sprintf("节点 %s 验证通过，请根据新发现调整计划。\n", nodeID)
	case executor.EventManualGuidance:
		guidance, ok := event.Payload["guidance"].(string)
		if !ok {
			return "", fmt.Errorf("EventManualGuidance 缺少 guidance")
		}
		prompt += fmt.Sprintf("人工指导：%s\n", guidance)
	case executor.EventHeartbeat:
		prompt += "定期检查：评估当前进展，必要时生成新 Action。\n"
	}

	prompt += "\n请使用 observe_state 工具观察当前状态，然后决定下一步行动。"
	return prompt, nil
}

// ============================================
// 实现 framework/core.Agent 接口
// ============================================

// Name 实现 core.Agent 接口
func (a *Agent) Name() string {
	return "planner"
}

// Run 实现 core.Agent 接口（调用现有的 Start 方法）
func (a *Agent) Run(ctx context.Context) error {
	return a.Start(ctx)
}

// ExportState 实现 core.Recoverable 接口
func (a *Agent) ExportState() (json.RawMessage, error) {
	// 导出 Planner 的内部状态
	state := map[string]interface{}{
		"task_id": a.taskID,
		// 可以添加更多需要持久化的状态
	}
	return json.Marshal(state)
}

// ImportState 实现 core.Recoverable 接口
func (a *Agent) ImportState(data json.RawMessage) error {
	// 导入状态（用于恢复）
	var state map[string]interface{}
	if err := json.Unmarshal(data, &state); err != nil {
		return err
	}
	// 恢复状态逻辑
	return nil
}
