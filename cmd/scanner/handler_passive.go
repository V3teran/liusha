package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/V3teran/liusha/internal/engagement"
	"github.com/V3teran/liusha/internal/llm"
	"github.com/V3teran/liusha/internal/react"
	"github.com/V3teran/liusha/internal/skill"
	"github.com/V3teran/liusha/internal/worker"
)

// handlePassive 处理 mode=passive 的 hunter task：拉 flow 完整 raw（请求 + 响应）
// → 装配 hunter react.Config → 跑 react.Run（1 流量 → 1 hunter agent）。
func (h handler) handlePassive(ctx context.Context, p worker.Payload, entrypoint json.RawMessage) error {
	var ep struct {
		FlowID int64  `json:"flow_id"`
		Host   string `json:"host"`
		URL    string `json:"url"`
		Method string `json:"method"`
	}
	if err := json.Unmarshal(entrypoint, &ep); err != nil {
		return h.failTask(ctx, p.TaskID, err)
	}

	tid, eid := p.TaskID, p.EngagementID
	// 双轨期：p.OwnerType/OwnerID 可能空（旧 enqueue 路径 / passive LookupOrCreate 失败）。
	// CallMeta.OwnerType/OwnerID 仅在非空时填指针，否则保 nil → llm_invocation 列写 NULL。
	ot, oid := p.OwnerType, p.OwnerID
	var otPtr, oidPtr *string
	if ot != "" {
		otPtr = &ot
	}
	if oid != "" {
		oidPtr = &oid
	}

	// hunter LLM Generator
	hunterRaw, err := h.router.For(ctx, "hunter")
	if err != nil {
		return h.failTask(ctx, p.TaskID, err)
	}
	hunterGen := llm.Instrument(hunterRaw, h.calls,
		llm.CallMeta{TaskID: &tid, EngagementID: &eid, OwnerType: otPtr, OwnerID: oidPtr, RouteKey: "hunter"},
		h.pricing,
	)

	// reviewer
	reviewLLMRaw, err := h.router.For(ctx, "reviewer")
	if err != nil {
		return h.failTask(ctx, p.TaskID, err)
	}
	reviewLLMGen := llm.Instrument(reviewLLMRaw, h.calls,
		llm.CallMeta{TaskID: &tid, EngagementID: &eid, OwnerType: otPtr, OwnerID: oidPtr, RouteKey: "reviewer"},
		h.pricing,
	)
	// hostForFetchers 提前定义：reviewer 需要 host 做 notes 范围隔离。
	hostForFetchers := ep.Host
	reviewer := react.NewLLMReviewer(reviewLLMGen, h.notes, eid, hostForFetchers)
	reviewer.ArgsTruncate = h.cfg.React.ReviewerArgsTruncate
	reviewer.ObsTruncate = h.cfg.React.ReviewerObsTruncate
	// FlowSummary 约束 reviewer 只评本流量任务，避免跨流量推方向
	reviewer.FlowSummary = fmt.Sprintf("%s %s%s", ep.Method, ep.Host, ep.URL)
	// HostFindingsFetcher 让 reviewer 看到 engagement + host 范围内已有 finding 列表（背景参考）。
	// 列表仅作背景知识：reviewer 知道本 host 漏洞面，但**不**把"已有 N 条"误当成本流量任务进度——
	// 否则同 host 别的流量先挖到 finding 时，本流量（如 bac/profile 真无漏洞）会被误推
	// terminate / 编造 hint。terminate 判定完全交给 reviewer 基于 window 行为推理。
	reviewer.HostFindingsFetcher = func(ctx context.Context) ([]string, error) {
		fs, err := h.findings.ListByEngagementAndHost(ctx, eid, hostForFetchers, h.cfg.React.ReviewerFindingsLimit)
		if err != nil {
			return nil, err
		}
		out := make([]string, 0, len(fs))
		for _, f := range fs {
			out = append(out, fmt.Sprintf("[%s] %s", f.Severity, f.Summary))
		}
		return out, nil
	}
	// LessonFetcher 让 reviewer 看到该 host 历史 lesson（跨 engagement 长期经验），
	// 用于方向修正 hint。lesson 是经验，不参与"是否 terminate"决策。
	reviewer.LessonFetcher = func(ctx context.Context) ([]string, error) {
		lessons, err := h.lessons.ListByHost(ctx, hostForFetchers, h.cfg.React.ReviewerLessonsLimit)
		if err != nil {
			return nil, err
		}
		out := make([]string, 0, len(lessons))
		for _, l := range lessons {
			out = append(out, fmt.Sprintf("[p%d] %s", l.Priority, l.Content))
		}
		return out, nil
	}

	// 拉 flow 完整 raw（请求 + 响应）填 BuilderParams
	fl, err := h.flows.GetByID(ctx, ep.FlowID)
	if err != nil {
		return h.failTask(ctx, p.TaskID, fmt.Errorf("flows.GetByID(%d): %w", ep.FlowID, err))
	}

	// 为本次 agent run 启动 sandbox 容器：spawn → 等 healthz → 返回 Client。
	// defer Destroy 保证 react.Run 结束后容器被回收（正常 / 异常 / panic 路径都覆盖）；
	// 失败时由 sandbox-server max lifetime 4h + 下次 scanner 启动 CleanupOrphans 兜底。
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

	// passive 父不开 spawn（M1：skill.go SpawnerFactory 守卫 Mode=="active"），
	// 无 parentRegistries Store / 无子 goroutine，不需要 H3 的 cancel+WaitAll。
	cfg, err := h.hunterBuilder(ctx, skill.BuilderParams{
		EngagementID:    eid,
		TaskID:          tid,
		Mode:            "passive",
		FlowID:          ep.FlowID,
		Host:            ep.Host,
		URL:             ep.URL,
		Method:          ep.Method,
		LLM:             hunterGen,
		Reviewer:        reviewer,
		RequestHeaders:  fl.RequestHeaders,
		RequestBody:     fl.RequestBody,
		ResponseStatus:  fl.StatusCode,
		ResponseHeaders: fl.ResponseHeaders,
		ResponseBody:    fl.ResponseBody,
		Sandbox:         sandboxClient,
	})
	if err != nil {
		return h.failTask(ctx, p.TaskID, err)
	}

	// engagement 中止时让 react.Run 自然停
	cfg.OnAbort = func(c context.Context) (bool, error) {
		eng, err := h.engagements.GetByID(c, eid)
		if err != nil {
			return false, err
		}
		return eng.Status != engagement.StatusActive, nil
	}

	out, err := react.Run(ctx, cfg)
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
