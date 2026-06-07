package main

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/hibiken/asynq"
	"github.com/rs/zerolog"

	"github.com/V3teran/liusha/internal/activescan"
	hunterbuilder "github.com/V3teran/liusha/internal/builder/hunter"
	"github.com/V3teran/liusha/internal/config"
	"github.com/V3teran/liusha/internal/einollm"
	"github.com/V3teran/liusha/internal/finding"
	"github.com/V3teran/liusha/internal/flow"
	"github.com/V3teran/liusha/internal/hunter"
	"github.com/V3teran/liusha/internal/lesson"
	"github.com/V3teran/liusha/internal/llm"
	"github.com/V3teran/liusha/internal/llminvocation"
	"github.com/V3teran/liusha/internal/notes"
	"github.com/V3teran/liusha/internal/passivesession"
	"github.com/V3teran/liusha/internal/react"
	"github.com/V3teran/liusha/internal/sandbox"
	"github.com/V3teran/liusha/internal/skill"
	"github.com/V3teran/liusha/internal/worker"
)

// handler 持有所有跨任务共享依赖。
type handler struct {
	tasks           *hunter.Store
	passiveSessions *passivesession.Store
	activeScans     *activescan.Store
	notes           *notes.RedisStore
	findings        *finding.Store
	lessons         *lesson.Store
	flows           *flow.Store
	calls           *llminvocation.Store
	cfg             config.Config
	scannerCfg      config.ScannerConfig
	pricing         llm.PricingProvider
	router          *llm.Router
	hunterBuilder   skill.Builder
	launcher        sandbox.Launcher
	logger          zerolog.Logger

	// eino 迁移（P3c）：passive 路径切到 eino ChatModelAgent。
	//   - einoFactory：按 role 产独立 eino ChatModel（per-hunter 铁律）
	//   - hunterDeps：复用旧 builder 的 store/loader 依赖（装 TrackerToolDeps + BuildUserPrompt）
	//   - einoPassive：LIUSHA_EINO_PASSIVE=1 时 handlePassive 走 eino 路径（默认 false 走 react）
	// gap（暂缺，待后续增量补 eino middleware）：LLM 计费 instrument / inspector / history 压缩。
	einoFactory *einollm.Factory
	hunterDeps  hunterbuilder.Deps
	einoPassive bool

	// parentRegistries 索引 commander hunterID → striker Registry（subtask swarm）。
	// spawnerFactory 闭包 Store；handleActive 在 react.Run 返回后 LoadAndDelete
	// + cancel commander ctx + WaitAll，确保striker goroutine 全退再 Destroy sandbox，防孤儿。
	parentRegistries *sync.Map
}

// buildInspector 装配 LLMInspector：复用 handler_passive / handler_active 两处胶水。
// inspector LLM 走 light_provider（router "inspector" 路由），按 (owner, host) 做 notes/findings/lessons 范围隔离。
// flowSummary 由 caller 提供，约束 inspector 评估范围（passive 含流量首行 / active 含 owner 标识）。
func (h handler) buildInspector(ctx context.Context, ownerType, ownerID, host, flowSummary, hunterID string, otPtr, oidPtr *string) (react.Inspector, error) {
	reviewLLMRaw, err := h.router.For(ctx, "inspector")
	if err != nil {
		return nil, err
	}
	reviewLLMGen := llm.Instrument(reviewLLMRaw, h.calls,
		llm.CallMeta{HunterID: &hunterID, OwnerType: otPtr, OwnerID: oidPtr, RouteKey: "inspector"},
		h.pricing,
	)
	inspector := react.NewLLMInspector(reviewLLMGen, h.notes, ownerID, host)
	inspector.Logger = h.logger
	inspector.ArgsTruncate = h.cfg.React.InspectorArgsTruncate
	inspector.ObsTruncate = h.cfg.React.InspectorObsTruncate
	inspector.FlowSummary = flowSummary
	// HostFindingsFetcher：inspector 看到 (owner, host) 已有 finding 列表（背景参考，不参与 terminate 判定）。
	inspector.HostFindingsFetcher = func(ctx context.Context) ([]string, error) {
		fs, err := h.findings.ListByOwnerAndHost(ctx, ownerType, ownerID, host, h.cfg.React.InspectorFindingsLimit)
		if err != nil {
			return nil, err
		}
		out := make([]string, 0, len(fs))
		for _, f := range fs {
			out = append(out, fmt.Sprintf("[%s] %s", f.Severity, f.Summary))
		}
		return out, nil
	}
	// LessonFetcher：跨 owner 累积的 host 历史经验，供 redirect hint 参考。
	inspector.LessonFetcher = func(ctx context.Context) ([]string, error) {
		lessons, err := h.lessons.ListByHost(ctx, host, h.cfg.React.InspectorLessonsLimit)
		if err != nil {
			return nil, err
		}
		out := make([]string, 0, len(lessons))
		for _, l := range lessons {
			out = append(out, fmt.Sprintf("[p%d] %s", l.Priority, l.Content))
		}
		return out, nil
	}
	return inspector, nil
}

// failTask 把错误标记到 task 表。
func (h handler) failTask(ctx context.Context, hunterID string, err error) error {
	if setErr := h.tasks.SetError(ctx, hunterID, err.Error()); setErr != nil {
		h.logger.Warn().Err(setErr).Str("hunter_id", hunterID).
			Msg("SetError 失败（task 留在 running，原始错误已透传给 caller）")
	}
	return err
}

// abortTask 把 task 推进到 aborted 终态（inspector 终止 / owner 中止 / ctx 取消）。
// 与 failTask 区别：aborted 是"主动收手"非错误，不应触发告警。
func (h handler) abortTask(ctx context.Context, hunterID, reason string) error {
	if setErr := h.tasks.SetAborted(ctx, hunterID); setErr != nil {
		h.logger.Warn().Err(setErr).Str("hunter_id", hunterID).Str("reason", reason).
			Msg("SetAborted 失败（task 留在 running）")
	}
	h.logger.Info().Str("hunter_id", hunterID).Str("reason", reason).Msg("task aborted")
	return nil
}

// handle 是单个 hunter task 的处理入口。
//
// timeout 按 mode 分档：passive 用 AgentRunTimeoutSeconds（默认 1h），
// active 用 ActiveAgentRunTimeoutSeconds（默认 4h，对齐 sandbox max lifetime）。
func (h handler) handle(ctx context.Context, p worker.Payload) (retErr error) {
	taskStart := time.Now()
	h.logger.Info().
		Str("hunter_id", p.HunterID).
		Str("owner_type", p.OwnerType).
		Str("owner_id", p.OwnerID).
		Str("role", string(p.Role)).
		Msg("asynq task ▶ enter")
	defer func() {
		ev := h.logger.Info()
		if retErr != nil {
			ev = h.logger.Warn().Err(retErr)
		}
		ev.Str("hunter_id", p.HunterID).
			Str("owner_id", p.OwnerID).
			Dur("duration", time.Since(taskStart)).
			Msg("asynq task ◀ exit")
	}()

	// 入口检查：asynq 重试场景（PG status 已非 pending）→ SkipRetry。
	// 防 commander被重试时新 Registry 空 → PreDoneCheck 永放行 → 旧 PG striker 僵尸 + 矛盾态。
	// GetByID 错误（PG 短时不可用等）不阻塞——让 SetRunning 走正常错误路径。
	if run, getErr := h.tasks.GetByID(ctx, p.HunterID); getErr == nil && run.Status != hunter.StatusPending {
		h.logger.Warn().
			Str("hunter_id", p.HunterID).
			Str("status", string(run.Status)).
			Msg("asynq task 已被处理过，跳过重试（防 PG 僵尸 + 矛盾态）")
		return asynq.SkipRetry
	}

	if err := h.tasks.SetRunning(ctx, p.HunterID); err != nil {
		return err
	}

	var input struct {
		Mode       string          `json:"mode"`
		Entrypoint json.RawMessage `json:"entrypoint"`
	}
	if err := json.Unmarshal(p.Input, &input); err != nil {
		return h.failTask(ctx, p.HunterID, err)
	}

	// 按 mode 选 timeout（解析 input 后才知道 mode；未知 mode 用 passive 兜底，
	// switch default 会立即报错，无超时浪费）。
	timeout := h.scannerCfg.AgentRunTimeoutSeconds
	if input.Mode == "active" {
		timeout = h.scannerCfg.ActiveAgentRunTimeoutSeconds
	}
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
		defer cancel()
	}

	switch input.Mode {
	case "passive":
		return h.handlePassive(ctx, p, input.Entrypoint)
	case "active":
		return h.handleActive(ctx, p, input.Entrypoint)
	default:
		err := fmt.Errorf("unknown mode: %s", input.Mode)
		return h.failTask(ctx, p.HunterID, err)
	}
}
