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
	// eino 迁移：默认走 eino commander+spawn 路径；LIUSHA_USE_REACT=1 才走下方旧 react 路径（退路）。
	if !h.useReact {
		return h.handleActiveEino(ctx, p, entrypoint)
	}

	var ep struct {
		Brief string `json:"brief"`
	}
	if err := json.Unmarshal(entrypoint, &ep); err != nil {
		return h.failTask(ctx, p.HunterID, err)
	}
	if ep.Brief == "" {
		return h.failTask(ctx, p.HunterID, fmt.Errorf("active entrypoint 缺 brief"))
	}

	tid := p.HunterID
	ot, oid := p.OwnerType, p.OwnerID
	otPtr, oidPtr := &ot, &oid
	// 优先从 brief 抽真实 URL host（如 target.com:8080），让 lesson/finding/note
	// 按真站点身份切分跨 task 复用；抽不到回退 owner_id 兜底（lesson 跨 task 失效）。
	virtualHost := extractHostFromBrief(ep.Brief, oid)
	// 抽到真实 host（非 owner_id 兜底）时回填 active_scan.target_host，便于查询/报表；
	// best-effort，失败不阻塞扫描（host 运行时路由用 virtualHost，不依赖该列）。
	if virtualHost != oid {
		if err := h.activeScans.SetTargetHost(ctx, oid, virtualHost); err != nil {
			h.logger.Warn().Err(err).Str("owner_id", oid).Str("host", virtualHost).
				Msg("回填 active_scan.target_host 失败（不阻塞扫描）")
		}
	}

	// commander LLM（commander指挥官）——vision_provider 支持 browser-use 截图。
	// deepseek 走 openai_compat 不支持 multimodal，触发 ErrVisionUnsupported。
	hunterRaw, err := h.router.For(ctx, "commander")
	if err != nil {
		return h.failTask(ctx, p.HunterID, err)
	}
	hunterGen := llm.Instrument(hunterRaw, h.calls,
		llm.CallMeta{HunterID: &tid, OwnerType: otPtr, OwnerID: oidPtr, RouteKey: "commander"},
		h.pricing,
	)

	// inspector 装配——virtualHost 已由 extractHostFromBrief 解析（brief 真 host 或 owner_id 兜底）。
	inspector, err := h.buildInspector(ctx, ot, oid, virtualHost, "ACTIVE owner="+oid, tid, otPtr, oidPtr)
	if err != nil {
		return h.failTask(ctx, p.HunterID, err)
	}

	// 为本次 agent run 启动 sandbox 容器（容器内 CLI 工具流量直连目标不入字典；chromium 经
	// browser-svc CDP capture → ingest 入字典，source=internal、active_scan owner。
	// hunterID 仅用作容器名隔离；commander + 所有 spawn 出的 striker 共享同一容器，文件级
	// 按 task_id 切 cwd/OUTPUT_DIR 隔离，chrome cookie/storage 自动跨 tab 共享）
	sandboxClient, err := h.launcher.Spawn(ctx, p.HunterID)
	if err != nil {
		return h.failTask(ctx, p.HunterID, fmt.Errorf("launcher.Spawn(%s): %w", p.HunterID, err))
	}
	defer func() {
		destroyCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := h.launcher.Destroy(destroyCtx, p.HunterID); err != nil {
			h.logger.Warn().Err(err).Str("hunter_id", p.HunterID).
				Msg("launcher.Destroy 失败（max lifetime / 下次启动 CleanupOrphans 兜底）")
		}
	}()

	// H3：commander react.Run 退出（含 max_steps 绕过 PreDoneCheck）后striker goroutine 可能仍在跑——
	// 若 Destroy 容器，striker /exec 报错→ silent failure。包一层 cancelable commanderCtx，
	// defer 里先 cancel striker 子树 + WaitAll，再让 Destroy defer 跑（LIFO）。
	commanderCtx, cancelParent := context.WithCancel(ctx)
	defer func() {
		cancelParent()
		if reg, ok := h.parentRegistries.LoadAndDelete(p.HunterID); ok {
			waitCtx, waitCancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer waitCancel()
			if !reg.(*subtask.Registry).WaitAll(waitCtx) {
				h.logger.Warn().Str("hunter_id", p.HunterID).
					Msg("striker goroutine 30s 未全退（容器即将销毁可能孤儿）")
			}
		}
	}()

	cfg, err := h.hunterBuilder(commanderCtx, skill.BuilderParams{
		OwnerType:   ot,
		OwnerID:     oid,
		HunterID:    tid,
		CommanderID: p.CommanderID, // active asynq 入口commander总是空；非空表示由 subtask 包内 ActiveSpawner 在 commander goroutine 内派的 striker
		Host:        virtualHost,
		LLM:         hunterGen,
		Inspector:   inspector,
		Mode:        "active",
		Brief:       ep.Brief,
		Sandbox:     sandboxClient,
	})
	if err != nil {
		return h.failTask(ctx, p.HunterID, err)
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

	// finalizeScan 翻转 active_scan 终态——commander run 自然退出后无人收尾会让 scan 永久卡
	// 'active'（model.go 包注释"active 跑完即终态"在此落地）。用 fresh ctx：ctx-cancel 分支
	// 里原 ctx 已死，复用会让终态 DB 写直接失败。TerminateBy=="aborted" 不在此收尾——那是
	// OnAbort 因 scan 已被用户 API 置非 active 才触发，scan 已是终态，重写会覆盖用户 abort 原因。
	finalizeScan := func(complete bool, reason string) {
		fctx, fcancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer fcancel()
		var ferr error
		if complete {
			ferr = h.activeScans.Complete(fctx, oid)
		} else {
			ferr = h.activeScans.Abort(fctx, oid, reason)
		}
		if ferr != nil {
			h.logger.Warn().Err(ferr).Str("scan_id", oid).Bool("complete", complete).
				Msg("active_scan 终态写失败（scan 可能卡 active，待人工排查）")
		}
	}

	out, err := react.Run(commanderCtx, cfg)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			finalizeScan(false, "ctx "+err.Error())
			return h.abortTask(ctx, p.HunterID, "ctx "+err.Error())
		}
		finalizeScan(false, err.Error())
		return h.failTask(ctx, p.HunterID, err)
	}
	if out.TerminateBy == "aborted" {
		return h.abortTask(ctx, p.HunterID, out.TerminateBy)
	}

	res, err := json.Marshal(map[string]any{
		"terminate_by":    out.TerminateBy,
		"total_steps":     out.TotalSteps,
		"total_in":        out.TotalUsage.InTokens,
		"total_out":       out.TotalUsage.OutTokens,
		"total_cached":    out.TotalUsage.CachedTokens,
		"inspector_hints": out.InspectorHints,
	})
	if err != nil {
		finalizeScan(false, "marshal task result")
		return h.failTask(ctx, p.HunterID, fmt.Errorf("marshal task result: %w", err))
	}
	finalizeScan(true, "")
	return h.tasks.SetDone(ctx, p.HunterID, res)
}
