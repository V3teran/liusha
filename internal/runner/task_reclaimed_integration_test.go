//go:build integration

package runner

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/V3teran/liusha/internal/agentrun"
	"github.com/V3teran/liusha/internal/cognition"
	"github.com/V3teran/liusha/internal/credential"
	"github.com/V3teran/liusha/internal/dbtest"
	"github.com/V3teran/liusha/internal/sandbox"
	"github.com/V3teran/liusha/internal/task"
	"github.com/V3teran/liusha/internal/worker"
)

// reclaimRecorder 记下会话身份的回收调用。
type reclaimRecorder struct {
	mu      sync.Mutex
	deleted []string
}

func (r *reclaimRecorder) SaveIdentities(context.Context, string, []credential.Identity) error {
	return nil
}
func (r *reclaimRecorder) GetIdentitiesByHost(context.Context, string) ([]credential.Identity, error) {
	return nil, nil
}
func (r *reclaimRecorder) ListHosts(context.Context) ([]credential.HostSummary, error) {
	return nil, nil
}

func (r *reclaimRecorder) DeleteIdentity(_ context.Context, _, name string) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.deleted = append(r.deleted, name)
	return true, nil
}

func (r *reclaimRecorder) deletedCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.deleted)
}

// recordingSandbox 记下释放调用；安装状态查询正常返回。
type recordingSandbox struct {
	mu       sync.Mutex
	releases int
}

func (s *recordingSandbox) Inventory(context.Context) (sandbox.Inventory, error) {
	return sandbox.Inventory{}, nil
}

func (s *recordingSandbox) Exec(context.Context, sandbox.ExecRequest) (sandbox.ExecResult, error) {
	return sandbox.ExecResult{}, errors.New("not used")
}

func (s *recordingSandbox) Release(context.Context, string) (sandbox.ReleaseResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.releases++
	return sandbox.ReleaseResult{}, nil
}

func (s *recordingSandbox) releaseCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.releases
}

// 被回收的 run 退出时什么都不写：run 行是恢复监控落定的，任务归后继 run。沙盒任务资源跟随后继
// run——有后继时不释放（沙盒里的进程与浏览器会话归它，旧 run 迟到的释放会打断它正在跑的工具），
// 没有后继（任务暂停或被终止，只回收了 run 行）时由本 run 释放。
//
// 曾有一轮 run 被判停摆并重投之后继续在进程里跑了四十分钟：既不退出，收尾时又把后继 run 的
// 沙盒进程释放掉。
func TestReclaimedRun_LeavesTaskToSuccessor(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.NewPgPool(t)
	tasks := task.NewStore(pool)
	runs := agentrun.NewStore(pool)
	sb := &recordingSandbox{}
	creds := &reclaimRecorder{}
	h := &Handler{AgentRuns: runs, Tasks: tasks, Sandbox: sb, Creds: creds, Logger: zerolog.Nop()}
	h.runLoop = func(context.Context, zerolog.Logger, task.Task, string, string, string, sandbox.Inventory) (cognition.Report, error) {
		return cognition.Report{StopReason: cognition.StopReasonReclaimed}, nil
	}
	startedRun := func(t *testing.T) (task.Task, string) {
		t.Helper()
		tk, err := tasks.Create(ctx, task.NewParams{AssignmentID: dbtest.SeedAssignment(t, pool), Goal: "扫描 http://a.test"})
		require.NoError(t, err)
		runID, err := runs.Create(ctx, agentrun.NewParams{TaskID: tk.ID})
		require.NoError(t, err)
		require.NoError(t, runs.SetRunning(ctx, runID))
		return tk, runID
	}

	// 恢复监控回收旧 run（标 error）、重投后继 run
	tk, old := startedRun(t)
	require.NoError(t, runs.SetError(ctx, old))
	_, err := runs.Create(ctx, agentrun.NewParams{TaskID: tk.ID})
	require.NoError(t, err)

	require.NoError(t, h.executeTask(ctx, worker.Payload{AgentRunID: old, TaskID: tk.ID}, tk, zerolog.Nop()))

	oldRow, err := runs.GetByID(ctx, old)
	require.NoError(t, err)
	assert.Equal(t, agentrun.StatusError, oldRow.Status, "run 行是恢复监控落定的，收尾不再写")
	row, err := tasks.GetByID(ctx, tk.ID)
	require.NoError(t, err)
	assert.Equal(t, task.StatusActive, row.Status, "任务归后继 run，收尾不碰任务行")
	assert.Equal(t, 0, sb.releaseCount(), "有后继 run 时沙盒任务资源归它，旧 run 不释放")
	assert.Equal(t, 0, creds.deletedCount(), "任务还在进行，会话身份留给后继 run")

	// 暂停中的任务只回收了 run 行、没有后继：遗留的进程由本 run 释放
	tk, lone := startedRun(t)
	require.NoError(t, runs.SetAborted(ctx, lone))
	require.NoError(t, h.executeTask(ctx, worker.Payload{AgentRunID: lone, TaskID: tk.ID}, tk, zerolog.Nop()))
	assert.Equal(t, 1, sb.releaseCount(), "没有后继 run 时由本 run 释放沙盒任务资源")
	assert.Equal(t, 0, creds.deletedCount(), "暂停中的任务续跑时复用登录态，不回收")

	// 中断达上限：恢复监控落定了 run 行、也终止了任务——沙盒由本 run 释放，会话身份照常回收
	tk, last := startedRun(t)
	_, err = tasks.Abort(ctx, tk.ID, "runner 连续中断 4 次")
	require.NoError(t, err)
	require.NoError(t, runs.SetAborted(ctx, last))
	require.NoError(t, h.executeTask(ctx, worker.Payload{AgentRunID: last, TaskID: tk.ID}, tk, zerolog.Nop()))
	assert.Equal(t, 2, sb.releaseCount())
	assert.Equal(t, 1, creds.deletedCount(), "任务确已结束，会话身份回收")
}
