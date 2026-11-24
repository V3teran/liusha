//go:build integration

package runner

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/V3teran/liusha/internal/agentrun"
	"github.com/V3teran/liusha/internal/dbtest"
	"github.com/V3teran/liusha/internal/sandbox"
	"github.com/V3teran/liusha/internal/task"
	"github.com/V3teran/liusha/internal/worker"
)

// attached 报告一轮 run 是否已登记在本进程（它的执行在进行中）。
func (h *Heartbeats) attached(runID string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.runs[runID] != nil
}

// rejectingSandbox 以 4xx 拒绝请求（配置问题，不会自愈）：run 开跑后立刻按出错收尾。它在被问询
// 安装状态的那一刻——run 正在执行——记下这轮 run 是否已登记在存活心跳上。
type rejectingSandbox struct {
	heartbeats *Heartbeats
	runID      string
	attached   bool
}

func (s *rejectingSandbox) Inventory(context.Context) (sandbox.Inventory, error) {
	s.attached = s.heartbeats.attached(s.runID)
	return sandbox.Inventory{}, &sandbox.StatusError{Code: 403}
}

func (s *rejectingSandbox) Exec(context.Context, sandbox.ExecRequest) (sandbox.ExecResult, error) {
	return sandbox.ExecResult{}, errors.New("not used")
}

func (s *rejectingSandbox) Release(context.Context, string) (sandbox.ReleaseResult, error) {
	return sandbox.ReleaseResult{}, errors.New("not used")
}

// 一轮 run 从开跑到收尾都登记在存活心跳上，收尾之后撤销：恢复监控只凭心跳判定，执行期间
// 没有登记就等于被判停摆。
func TestHandler_RunIsAttachedToHeartbeatsWhileExecuting(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.NewPgPool(t)
	tasks := task.NewStore(pool)
	runs := agentrun.NewStore(pool)
	tk, err := tasks.Create(ctx, task.NewParams{AssignmentID: dbtest.SeedAssignment(t, pool), Goal: "扫描 http://a.test"})
	if err != nil {
		t.Fatal(err)
	}
	runID, err := runs.Create(ctx, agentrun.NewParams{TaskID: tk.ID})
	if err != nil {
		t.Fatal(err)
	}
	hb := NewHeartbeats()
	sb := &rejectingSandbox{heartbeats: hb, runID: runID}
	h := &Handler{AgentRuns: runs, Tasks: tasks, Heartbeats: hb, Sandbox: sb, Logger: zerolog.Nop()}

	if hb.attached(runID) {
		t.Fatal("开跑之前不应登记")
	}
	// 沙盒拒绝请求：run 出错、任务以错误结束（与心跳无关，这里只借这条最短的收尾路径）。
	if err := h.Handle(ctx, worker.Payload{AgentRunID: runID, TaskID: tk.ID, TaskRole: worker.RoleAgent}); err == nil || !strings.Contains(err.Error(), "沙盒拒绝请求") {
		t.Fatalf("沙盒拒绝请求应原样交还，实际 %v", err)
	}
	if !sb.attached {
		t.Fatal("执行期间这轮 run 应登记在存活心跳上")
	}
	if hb.attached(runID) {
		t.Fatal("收尾之后应撤销登记")
	}
	// 开跑时心跳从当下起算（排队可能很久）；收尾后 run 不再是 running。
	var beat time.Time
	if err := pool.QueryRow(ctx, `SELECT heartbeat_at FROM agent_run WHERE id=$1`, runID).Scan(&beat); err != nil {
		t.Fatal(err)
	}
	if time.Since(beat) > time.Minute {
		t.Fatal("开跑应把心跳重置为当下")
	}
}
