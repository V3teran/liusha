package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/cloudwego/eino/adk"

	"github.com/V3teran/liusha/internal/activescan"
	hunterbuilder "github.com/V3teran/liusha/internal/builder/hunter"
	"github.com/V3teran/liusha/internal/einoagent"
	"github.com/V3teran/liusha/internal/hunter"
	"github.com/V3teran/liusha/internal/skill"
	"github.com/V3teran/liusha/internal/worker"
)

// handleActiveEino 是 handleActive 的 eino 版（LIUSHA_EINO_PASSIVE=1 时也接管 active）：
// commander=单 ChatModelAgent + 同步 spawn_striker 工具。并发派活 = 一轮多 spawn 调用（ToolsNode 并行），
// 各 striker 独立 model（铁律）。这删掉了 react 路径的 subtask.Registry/PreDoneCheck/cancel+WaitAll。
//
// commander + 所有 striker 共享同一 sandbox 容器；striker 是进程内同步工具调用（非 asynq task），
// 但仍建 hunter 行（finding/llm_invocation/tool_invocation.hunter_id FK 完整 + UI 可见）。
func (h handler) handleActiveEino(ctx context.Context, p worker.Payload, entrypoint json.RawMessage) error {
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
	virtualHost := extractHostFromBrief(ep.Brief, oid)
	if virtualHost != oid {
		if err := h.activeScans.SetTargetHost(ctx, oid, virtualHost); err != nil {
			h.logger.Warn().Err(err).Str("owner_id", oid).Str("host", virtualHost).
				Msg("回填 active_scan.target_host 失败（不阻塞扫描）")
		}
	}

	model, err := h.einoFactory.For(ctx, "commander") // commander 独立 model
	if err != nil {
		return h.failTask(ctx, p.HunterID, err)
	}

	// commander + striker 共享 sandbox 容器；defer Destroy 覆盖正常/异常/panic。
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

	toolDeps := h.einoToolDeps(sandboxClient)

	// striker 生命周期闭包（持 hunter.Store / hunter.Deps，避免 einoagent→scanner 耦合）。
	newStrikerID := func(c context.Context) (string, error) {
		input, _ := json.Marshal(map[string]any{"mode": "active", "role": "striker", "commander": tid})
		sid, err := h.tasks.Create(c, hunter.NewParams{
			OwnerType: ot, OwnerID: oid, Role: "striker", CommanderID: tid, Input: input,
		})
		if err != nil {
			return "", fmt.Errorf("hunter.Create(striker): %w", err)
		}
		if err := h.tasks.SetRunning(c, sid); err != nil {
			return "", fmt.Errorf("hunter.SetRunning(striker): %w", err)
		}
		return sid, nil
	}
	onStrikerDone := func(c context.Context, sid string, runErr error) {
		// fresh ctx：commander run ctx 可能因 cancel 已死。
		fctx, fcancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer fcancel()
		if runErr != nil {
			_ = h.tasks.SetError(fctx, sid, runErr.Error())
			return
		}
		_ = h.tasks.SetDone(fctx, sid, json.RawMessage(`{"engine":"eino","role":"striker"}`))
	}
	buildStrikerPrompt := func(c context.Context, brief, sid string, flowID int64) string {
		return hunterbuilder.BuildUserPrompt(c, h.hunterDeps, skill.BuilderParams{
			OwnerType: ot, OwnerID: oid, HunterID: sid, CommanderID: tid,
			Host: virtualHost, Mode: "active", Brief: brief, FlowID: flowID, Sandbox: sandboxClient,
		})
	}
	strikerRunOpts := func(sid string) ([]adk.AgentMiddleware, []adk.AgentRunOption) {
		return h.einoRunOpts(ctx, sid, ot, oid, "striker")
	}

	spawnTool, err := einoagent.BuildSpawnStriker(einoagent.StrikerSpawnConfig{
		Factory:         h.einoFactory,
		ToolDeps:        toolDeps,
		Instruction:     hunterbuilder.SystemPromptFor("active", false), // striker prompt
		OwnerType:       ot,
		OwnerID:         oid,
		Host:            virtualHost,
		NewHunterID:     newStrikerID,
		OnStrikerDone:   onStrikerDone,
		BuildUserPrompt: buildStrikerPrompt,
		RunOpts:         strikerRunOpts,
	})
	if err != nil {
		return h.failTask(ctx, p.HunterID, err)
	}

	commanderTools, err := einoagent.BuildCommanderTools(toolDeps,
		einoagent.TrackerToolParams{OwnerType: ot, OwnerID: oid, HunterID: tid, Host: virtualHost},
		spawnTool,
	)
	if err != nil {
		return h.failTask(ctx, p.HunterID, err)
	}

	commanderInstruction := hunterbuilder.SystemPromptFor("active", true) // commander prompt
	commanderPrompt := hunterbuilder.BuildUserPrompt(ctx, h.hunterDeps, skill.BuilderParams{
		OwnerType: ot, OwnerID: oid, HunterID: tid,
		Host: virtualHost, Mode: "active", Brief: ep.Brief, Sandbox: sandboxClient,
	})

	// active_scan 终态收尾（commander 退出后无人收尾会卡 'active'）。
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

	// owner 中止 watcher：轮询 active_scan.Status，非 active 即 cancel commander。
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	go h.watchAbortActive(runCtx, cancel, oid)

	mws, opts := h.einoRunOpts(ctx, tid, ot, oid, "commander")
	res, err := einoagent.RunCommander(runCtx, model, commanderTools, commanderInstruction, commanderPrompt, mws, opts...)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			finalizeScan(false, "ctx "+err.Error())
			return h.abortTask(ctx, p.HunterID, "ctx "+err.Error())
		}
		finalizeScan(false, err.Error())
		return h.failTask(ctx, p.HunterID, err)
	}

	out, err := json.Marshal(map[string]any{
		"engine":     "eino",
		"role":       "commander",
		"tool_calls": res.ToolCalls,
		"final_text": res.FinalText,
	})
	if err != nil {
		finalizeScan(false, "marshal task result")
		return h.failTask(ctx, p.HunterID, fmt.Errorf("marshal task result: %w", err))
	}
	finalizeScan(true, "")
	return h.tasks.SetDone(ctx, p.HunterID, out)
}

// watchAbortActive 后台轮询 active_scan 中止状态；非 active 即 cancel，让 RunCommander 停。
func (h handler) watchAbortActive(ctx context.Context, cancel context.CancelFunc, ownerID string) {
	ticker := time.NewTicker(abortPollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			sc, err := h.activeScans.GetByID(ctx, ownerID)
			if err != nil {
				continue
			}
			if sc.Status != activescan.StatusActive {
				h.logger.Info().Str("scan_id", ownerID).Msg("owner 中止，cancel eino commander")
				cancel()
				return
			}
		}
	}
}
