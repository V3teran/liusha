package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/V3teran/liusha/internal/llm"
	"github.com/V3teran/liusha/internal/passivesession"
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

	tid := p.TaskID
	ot, oid := p.OwnerType, p.OwnerID
	otPtr, oidPtr := &ot, &oid

	// tracker LLM Generator（passive 侦察兵 — 单 agent，独立追踪流量线索）
	hunterRaw, err := h.router.For(ctx, "tracker")
	if err != nil {
		return h.failTask(ctx, p.TaskID, err)
	}
	hunterGen := llm.Instrument(hunterRaw, h.calls,
		llm.CallMeta{TaskID: &tid, OwnerType: otPtr, OwnerID: oidPtr, RouteKey: "tracker"},
		h.pricing,
	)

	// inspector 装配——FlowSummary 含流量首行约束评估范围；HostFindingsFetcher 仅作背景参考不参与 terminate。
	flowSummary := fmt.Sprintf("%s %s%s", ep.Method, ep.Host, ep.URL)
	inspector, err := h.buildInspector(ctx, ot, oid, ep.Host, flowSummary, tid, otPtr, oidPtr)
	if err != nil {
		return h.failTask(ctx, p.TaskID, err)
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

	// tracker不开 spawn（M1：skill.go SpawnerFactory 守卫 Mode=="active"），
	// 无 parentRegistries Store / 无striker goroutine，不需要 H3 的 cancel+WaitAll。
	cfg, err := h.hunterBuilder(ctx, skill.BuilderParams{
		OwnerType:       ot,
		OwnerID:         oid,
		TaskID:          tid,
		Mode:            "passive",
		FlowID:          ep.FlowID,
		Host:            ep.Host,
		URL:             ep.URL,
		Method:          ep.Method,
		LLM:             hunterGen,
		Inspector:        inspector,
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

	// owner 中止时让 react.Run 自然停
	// OnAbort 切到新表：worker.Payload.OwnerID 现在为空（B6.3），用 oid 查
	// passive_session.Status。oid 必非空——ingestor 单源走 passive.LookupOrCreate。
	cfg.OnAbort = func(c context.Context) (bool, error) {
		sess, err := h.passiveSessions.GetByID(c, oid)
		if err != nil {
			return false, err
		}
		return sess.Status != passivesession.StatusActive, nil
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
		"inspector_hints": out.InspectorHints,
	})
	if err != nil {
		return h.failTask(ctx, p.TaskID, fmt.Errorf("marshal task result: %w", err))
	}
	return h.tasks.SetDone(ctx, p.TaskID, res)
}
