package subtask

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/V3teran/liusha/internal/agentrun"
	"github.com/V3teran/liusha/internal/finding"
	"github.com/V3teran/liusha/internal/flow"
	"github.com/V3teran/liusha/internal/lesson"
	"github.com/V3teran/liusha/internal/llm"
	"github.com/V3teran/liusha/internal/llminvocation"
	"github.com/V3teran/liusha/internal/notes"
	"github.com/V3teran/liusha/internal/react"
	"github.com/V3teran/liusha/internal/sandbox"
	"github.com/V3teran/liusha/internal/skill"
	"github.com/V3teran/liusha/internal/worker"
)

// ErrMaxChildren 是 max_children 闸触发的固定错误。
// spawn_child 工具识别后给 LLM 友好提示（"已达 N 个 child 上限，等部分完成再 spawn"）。
var ErrMaxChildren = errors.New("max_children reached")

// ActiveSpawnerConfig 是 ActiveSpawner 的全部依赖（避免构造函数参数膨胀）。
//
// 字段都是 cmd/scanner 装配 hunter Deps 时已经持有的——SpawnerFactory 闭包从
// handler 字段捕获即可。
type ActiveSpawnerConfig struct {
	// 任务身份
	ParentTaskID string
	OwnerType    string // 与父任务对齐；'passive_session' / 'active_scan'
	OwnerID      string
	Host         string

	// PG 存储
	AgentRuns *agentrun.Store
	Findings  *finding.Store
	Lessons   *lesson.Store
	Calls     *llminvocation.Store
	// Flows 可空（active 父无 flow）；FlowID>0 时调 GetByID 拉 raw HTTP 填子 BuilderParams。
	Flows *flow.Store

	// 短期记忆
	Notes notes.Store

	// LLM
	Router  *llm.Router
	Pricing llm.PricingProvider

	// 装配子 hunter
	HunterBuilder skill.Builder

	// 共享父容器（子任务文件按 task_id 隔离 — PR2 已就绪）
	SandboxClient sandbox.Client

	// 父进程内的子任务句柄注册表
	Registry *Registry

	// 闸值
	MaxChildren int

	// Reviewer 装配参数（与父 active 路径对齐）
	ReviewerArgsTruncate  int
	ReviewerObsTruncate   int
	ReviewerFindingsLimit int
	ReviewerLessonsLimit  int
}

// ActiveSpawner 实现 Spawner 接口——为 active 父任务派子 active 任务。
//
// parentCtx 是父 react.Run 的 ctx；子 ctx 由 WithCancel(parentCtx) 派生，
// 父 abort / engagement abort / parent ctx timeout 都会自动级联到子。
type ActiveSpawner struct {
	cfg       ActiveSpawnerConfig
	parentCtx context.Context
}

// NewActiveSpawner 构造 ActiveSpawner。parentCtx 必须是父 react.Run 的活 ctx。
func NewActiveSpawner(parentCtx context.Context, cfg ActiveSpawnerConfig) *ActiveSpawner {
	return &ActiveSpawner{cfg: cfg, parentCtx: parentCtx}
}

// Spawn 创建一行 child agent_run + 启 goroutine 跑子 react.Run，立即返回 childTaskID（异步）。
// opts.FlowID>0 时子能在 user prompt 看到完整 raw HTTP（passive 父常用）。
func (s *ActiveSpawner) Spawn(ctx context.Context, brief string, opts SpawnOptions) (string, error) {
	// max_children 是"同时并发上限"：只数 running 子，已 done/failed 的不占额
	// → LLM 视角下 list_children 看见"全 done"时 quota 真的释放了，可以继续 spawn。
	// 错误消息内嵌当前 running / max，spawn_child 工具直接透传给 LLM 看。
	if running := s.cfg.Registry.RunningCount(); running >= s.cfg.MaxChildren {
		return "", fmt.Errorf("%w: 已有 %d running 子（max=%d），调 list_children 等部分完成再 spawn",
			ErrMaxChildren, running, s.cfg.MaxChildren)
	}

	// PG agent_run 行（pending）— input 记录 brief + flow_id（可空）
	entrypoint := map[string]any{"brief": brief}
	if opts.FlowID > 0 {
		entrypoint["flow_id"] = opts.FlowID
	}
	payloadInput, err := json.Marshal(map[string]any{
		"mode":       "active",
		"entrypoint": entrypoint,
	})
	if err != nil {
		return "", fmt.Errorf("marshal child payload: %w", err)
	}
	childTID, err := s.cfg.AgentRuns.Create(ctx, agentrun.NewParams{
		OwnerType: s.cfg.OwnerType, // 与父任务对齐
		OwnerID:   s.cfg.OwnerID,
		Role:      string(worker.RoleHunter),
		Input:     payloadInput,
		ParentID:  s.cfg.ParentTaskID,
	})
	if err != nil {
		return "", fmt.Errorf("agentrun.Create(child): %w", err)
	}

	// pending → running 必须在 Registry.Register 之前——否则 SetRunning 失败时
	// handle 已 Register 但 goroutine 没启 → 永 running 句柄污染 RunningCount/PreDoneCheck
	// → 父 done 永卡。
	if err := s.cfg.AgentRuns.SetRunning(ctx, childTID); err != nil {
		return "", fmt.Errorf("agentrun.SetRunning(child): %w", err)
	}

	handle := s.cfg.Registry.Register(childTID, brief)

	// 父 ctx 派生子 ctx——父 abort / engagement abort / parent timeout 自动级联
	childCtx, cancel := context.WithCancel(s.parentCtx)
	// trackGoroutine / untrackGoroutine 让 Registry.WaitAll 能等所有子 goroutine 退出
	// → handleActive 在父 react.Run 返回（含 max_steps）后能确保子全退再 Destroy 容器，
	//   避免孤儿 goroutine 在已销毁容器上调 /exec。
	s.cfg.Registry.trackGoroutine()
	go func() {
		defer s.cfg.Registry.untrackGoroutine()
		s.runChild(childCtx, cancel, childTID, brief, opts.FlowID, handle)
	}()

	return childTID, nil
}

// runChild 在独立 goroutine 内装配 + 跑子 react.Run。
// 任何路径（成功 / 失败 / panic / abort）都更新 handle 状态 + PG agent_run 行。
// flowID>0 时拉 flow 填 BuilderParams，让子 user prompt 渲染 raw HTTP 段 + brief 段。
func (s *ActiveSpawner) runChild(ctx context.Context, cancel context.CancelFunc, childTID, brief string, flowID int64, handle *Handle) {
	defer cancel()
	defer func() {
		if r := recover(); r != nil {
			err := fmt.Errorf("child panic: %v", r)
			handle.MarkFailed(err)
			// 用 background ctx——可能 ctx 已 cancel，SetError 仍要落库
			_ = s.cfg.AgentRuns.SetError(context.Background(), childTID, err.Error())
		}
	}()

	// owner 透传给 llm_invocation；CallMeta.OwnerType/OwnerID 是 *string 类型
	ot, oid := s.cfg.OwnerType, s.cfg.OwnerID
	var otPtr, oidPtr *string
	if ot != "" {
		otPtr = &ot
	}
	if oid != "" {
		oidPtr = &oid
	}

	// hunter LLM
	hunterRaw, err := s.cfg.Router.For(ctx, "hunter_vision")
	if err != nil {
		s.markFailed(childTID, handle, fmt.Errorf("router hunter_vision: %w", err))
		return
	}
	hunterGen := llm.Instrument(hunterRaw, s.cfg.Calls,
		llm.CallMeta{TaskID: &childTID, OwnerType: otPtr, OwnerID: oidPtr, RouteKey: "hunter_vision"},
		s.cfg.Pricing,
	)

	// reviewer
	reviewLLMRaw, err := s.cfg.Router.For(ctx, "reviewer")
	if err != nil {
		s.markFailed(childTID, handle, fmt.Errorf("router reviewer: %w", err))
		return
	}
	reviewLLMGen := llm.Instrument(reviewLLMRaw, s.cfg.Calls,
		llm.CallMeta{TaskID: &childTID, OwnerType: otPtr, OwnerID: oidPtr, RouteKey: "reviewer"},
		s.cfg.Pricing,
	)
	// reviewer notes key 用 owner_id（与 finding/lesson 切分一致）
	reviewer := react.NewLLMReviewer(reviewLLMGen, s.cfg.Notes, oid, s.cfg.Host)
	reviewer.ArgsTruncate = s.cfg.ReviewerArgsTruncate
	reviewer.ObsTruncate = s.cfg.ReviewerObsTruncate
	reviewer.FlowSummary = "ACTIVE child owner=" + oid + " parent=" + s.cfg.ParentTaskID
	reviewer.HostFindingsFetcher = func(ctx context.Context) ([]string, error) {
		fs, err := s.cfg.Findings.ListByOwnerAndHost(ctx, ot, oid, s.cfg.Host, s.cfg.ReviewerFindingsLimit)
		if err != nil {
			return nil, err
		}
		out := make([]string, 0, len(fs))
		for _, f := range fs {
			out = append(out, fmt.Sprintf("[%s] %s", f.Severity, f.Summary))
		}
		return out, nil
	}
	reviewer.LessonFetcher = func(ctx context.Context) ([]string, error) {
		ls, err := s.cfg.Lessons.ListByHost(ctx, s.cfg.Host, s.cfg.ReviewerLessonsLimit)
		if err != nil {
			return nil, err
		}
		out := make([]string, 0, len(ls))
		for _, l := range ls {
			out = append(out, fmt.Sprintf("[p%d] %s", l.Priority, l.Content))
		}
		return out, nil
	}

	// 装配 BuilderParams——ParentTaskID 非空让 hunter builder 不注册 spawn/list（max_depth=1）
	bp := skill.BuilderParams{
		OwnerType: ot, // 与父任务对齐
		OwnerID:   oid,
		TaskID:    childTID,
		ParentTaskID: s.cfg.ParentTaskID,
		Host:         s.cfg.Host,
		LLM:          hunterGen,
		Reviewer:     reviewer,
		Mode:         "active",
		Brief:        brief,
		Sandbox:      s.cfg.SandboxClient,
	}

	// 父传 flow_id 时拉 flow 填到 BuilderParams——子 buildUserPrompt 字段触发渲染 raw HTTP 段。
	// Flows 为 nil（active 父场景未注入）或 GetByID 失败时降级到纯 brief 模式（仅 warn）。
	if flowID > 0 && s.cfg.Flows != nil {
		fl, ferr := s.cfg.Flows.GetByID(ctx, flowID)
		if ferr != nil {
			handle.MarkFailed(fmt.Errorf("拉父流量 flow_id=%d 失败: %w", flowID, ferr))
			_ = s.cfg.AgentRuns.SetError(context.Background(), childTID, ferr.Error())
			return
		}
		bp.FlowID = fl.ID
		bp.URL = fl.URL
		bp.Method = fl.Method
		bp.RequestHeaders = fl.RequestHeaders
		bp.RequestBody = fl.RequestBody
		bp.ResponseStatus = fl.StatusCode
		bp.ResponseHeaders = fl.ResponseHeaders
		bp.ResponseBody = fl.ResponseBody
	}

	cfg, err := s.cfg.HunterBuilder(ctx, bp)
	if err != nil {
		s.markFailed(childTID, handle, fmt.Errorf("hunter builder: %w", err))
		return
	}

	// 子继承父 ctx → engagement abort 时父 ctx cancel 自动传到这里
	out, runErr := react.Run(ctx, cfg)
	if runErr != nil {
		// ctx cancel / DeadlineExceeded 视为 abort——PG 写 SetAborted（非 SetError），
		// handle 仍 MarkFailed 给父 LLM 看到 failureReason=context canceled
		if errors.Is(runErr, context.Canceled) || errors.Is(runErr, context.DeadlineExceeded) {
			handle.MarkFailed(runErr)
			_ = s.cfg.AgentRuns.SetAborted(context.Background(), childTID)
			return
		}
		s.markFailed(childTID, handle, runErr)
		return
	}

	handle.MarkDone(Outcome{
		TerminateBy: out.TerminateBy,
		TotalSteps:  out.TotalSteps,
	})

	res, _ := json.Marshal(map[string]any{
		"terminate_by":   out.TerminateBy,
		"total_steps":    out.TotalSteps,
		"total_in":       out.TotalUsage.InTokens,
		"total_out":      out.TotalUsage.OutTokens,
		"total_cached":   out.TotalUsage.CachedTokens,
		"reviewer_hints": out.ReviewerHints,
		"parent_task_id": s.cfg.ParentTaskID,
	})
	_ = s.cfg.AgentRuns.SetDone(context.Background(), childTID, res)
}

// markFailed 同时更新 handle + PG（中间过程错的统一收尾）。
func (s *ActiveSpawner) markFailed(childTID string, handle *Handle, err error) {
	handle.MarkFailed(err)
	_ = s.cfg.AgentRuns.SetError(context.Background(), childTID, err.Error())
}
