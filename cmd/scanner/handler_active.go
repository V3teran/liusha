package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/V3teran/liusha/internal/activescan"
	"github.com/V3teran/liusha/internal/llm"
	"github.com/V3teran/liusha/internal/react"
	"github.com/V3teran/liusha/internal/skill"
	"github.com/V3teran/liusha/internal/subtask"
	"github.com/V3teran/liusha/internal/worker"
)

// handleActive 处理 mode=active 的 hunter task：把 brief 自然语言整段喂 hunter，
// 由 LLM 自行从 brief 识别目标 URL / 凭据 / 测试范围。
//
// 与 handlePassive 差异：
//   - 跳过 flows.GetByID（active 无 flow）
//   - BuilderParams 走 active 字段（Brief 而非 Request*/Response*）
//   - Host 用 brief 抽出的 URL host 当切分键（抽不到回退 owner_id）
//   - 启用 subtask swarm：parentRegistries Store + react.Run 返回后 cancel+WaitAll
func (h handler) handleActive(ctx context.Context, p worker.Payload, entrypoint json.RawMessage) error {
	var ep struct {
		Brief string `json:"brief"`
	}
	if err := json.Unmarshal(entrypoint, &ep); err != nil {
		return h.failTask(ctx, p.TaskID, err)
	}
	if ep.Brief == "" {
		return h.failTask(ctx, p.TaskID, fmt.Errorf("active entrypoint 缺 brief"))
	}

	tid := p.TaskID
	ot, oid := p.OwnerType, p.OwnerID
	otPtr, oidPtr := &ot, &oid
	// 优先从 brief 抽真实 URL host（如 target.com:8080），让 lesson/finding/note
	// 按真站点身份切分跨 task 复用；抽不到回退 owner_id 兜底（lesson 跨 task 失效）。
	virtualHost := extractHostFromBrief(ep.Brief, oid)

	// hunter LLM——active 路由 vision_provider（默认 anthropic），支持 browser-use 截图。
	// deepseek 走 openai_compat 不支持 multimodal，触发 ErrVisionUnsupported。
	hunterRaw, err := h.router.For(ctx, "hunter_vision")
	if err != nil {
		return h.failTask(ctx, p.TaskID, err)
	}
	hunterGen := llm.Instrument(hunterRaw, h.calls,
		llm.CallMeta{TaskID: &tid, OwnerType: otPtr, OwnerID: oidPtr, RouteKey: "hunter_vision"},
		h.pricing,
	)

	// reviewer
	reviewLLMRaw, err := h.router.For(ctx, "reviewer")
	if err != nil {
		return h.failTask(ctx, p.TaskID, err)
	}
	reviewLLMGen := llm.Instrument(reviewLLMRaw, h.calls,
		llm.CallMeta{TaskID: &tid, OwnerType: otPtr, OwnerID: oidPtr, RouteKey: "reviewer"},
		h.pricing,
	)
	// notes key 用 owner_id（与 BuilderParams.OwnerID 一致；0040 FK DROP 后 finding 无 FK 约束）
	reviewer := react.NewLLMReviewer(reviewLLMGen, h.notes, oid, virtualHost)
	reviewer.ArgsTruncate = h.cfg.React.ReviewerArgsTruncate
	reviewer.ObsTruncate = h.cfg.React.ReviewerObsTruncate
	reviewer.FlowSummary = "ACTIVE owner=" + oid
	reviewer.HostFindingsFetcher = func(ctx context.Context) ([]string, error) {
		fs, err := h.findings.ListByOwnerAndHost(ctx, ot, oid, virtualHost, h.cfg.React.ReviewerFindingsLimit)
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
		lessons, err := h.lessons.ListByHost(ctx, virtualHost, h.cfg.React.ReviewerLessonsLimit)
		if err != nil {
			return nil, err
		}
		out := make([]string, 0, len(lessons))
		for _, l := range lessons {
			out = append(out, fmt.Sprintf("[p%d] %s", l.Priority, l.Content))
		}
		return out, nil
	}

	// 为本次 agent run 启动 sandbox 容器
	sandboxClient, err := h.launcher.Spawn(ctx, p.TaskID)
	if err != nil {
		return h.failTask(ctx, p.TaskID, fmt.Errorf("launcher.Spawn(%s): %w", p.TaskID, err))
	}
	defer func() {
		destroyCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := h.launcher.Destroy(destroyCtx, p.TaskID); err != nil {
			h.logger.Warn().Err(err).Str("agent_run_id", p.TaskID).
				Msg("launcher.Destroy 失败（max lifetime / 下次启动 CleanupOrphans 兜底）")
		}
	}()

	// H3：父 react.Run 退出（含 max_steps 绕过 PreDoneCheck）后子 goroutine 可能仍在跑——
	// 若 Destroy 容器，子 /exec 报错→ silent failure。包一层 cancelable parentCtx，
	// defer 里先 cancel 子树 + WaitAll，再让 Destroy defer 跑（LIFO）。
	parentCtx, cancelParent := context.WithCancel(ctx)
	defer func() {
		cancelParent()
		if reg, ok := h.parentRegistries.LoadAndDelete(p.TaskID); ok {
			waitCtx, waitCancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer waitCancel()
			if !reg.(*subtask.Registry).WaitAll(waitCtx) {
				h.logger.Warn().Str("agent_run_id", p.TaskID).
					Msg("子 goroutine 30s 未全退（容器即将销毁可能孤儿）")
			}
		}
	}()

	cfg, err := h.hunterBuilder(parentCtx, skill.BuilderParams{
		OwnerType: ot,
		OwnerID:   oid,
		TaskID:    tid,
		ParentTaskID: p.ParentTaskID, // active asynq 入口父任务总是空；非空表示由 subtask 包内 ActiveSpawner 在父 goroutine 内派的子
		Host:         virtualHost,
		LLM:          hunterGen,
		Reviewer:     reviewer,
		Mode:         "active",
		Brief:        ep.Brief,
		Sandbox:      sandboxClient,
	})
	if err != nil {
		return h.failTask(ctx, p.TaskID, err)
	}

	// OnAbort 切到新表：worker.Payload.OwnerID 现在为空（B6.2），用 oid 查
	// active_scan.Status。oid 必非空——cmd/api 单源走 activescan.Create。
	cfg.OnAbort = func(c context.Context) (bool, error) {
		sc, err := h.activeScans.GetByID(c, oid)
		if err != nil {
			return false, err
		}
		return sc.Status != activescan.StatusActive, nil
	}

	out, err := react.Run(parentCtx, cfg)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return h.abortTask(ctx, p.TaskID, "ctx "+err.Error())
		}
		return h.failTask(ctx, p.TaskID, err)
	}
	if out.TerminateBy == "aborted" {
		return h.abortTask(ctx, p.TaskID, out.TerminateBy)
	}

	res, err := json.Marshal(map[string]any{
		"terminate_by":   out.TerminateBy,
		"total_steps":    out.TotalSteps,
		"total_in":       out.TotalUsage.InTokens,
		"total_out":      out.TotalUsage.OutTokens,
		"total_cached":   out.TotalUsage.CachedTokens,
		"reviewer_hints": out.ReviewerHints,
	})
	if err != nil {
		return h.failTask(ctx, p.TaskID, fmt.Errorf("marshal task result: %w", err))
	}
	return h.tasks.SetDone(ctx, p.TaskID, res)
}
