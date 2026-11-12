package server

import (
	"errors"
	"net/http"
	"sync"
	"syscall"

	"github.com/V3teran/liusha/internal/sandbox"
	"github.com/V3teran/liusha/internal/sandbox/browser"
)

// taskProcesses 记录各任务的命令进程组。命令正常结束后若组内仍有进程（后台监听器、
// nohup 的扫描等）则继续保留记录，任务释放时整组结束。
//
// 进程组号在组内还有进程时不会被系统复用（POSIX 保证），组空即注销，因此结束的只会是
// 本任务的进程。
type taskProcesses struct {
	mu     sync.Mutex
	byTask map[string]map[int]struct{}
}

func newTaskProcesses() *taskProcesses {
	return &taskProcesses{byTask: map[string]map[int]struct{}{}}
}

func (p *taskProcesses) add(taskID string, pgid int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	groups := p.byTask[taskID]
	if groups == nil {
		groups = map[int]struct{}{}
		p.byTask[taskID] = groups
	}
	groups[pgid] = struct{}{}
}

// forgetIfGone 在进程组已无进程时注销它。
func (p *taskProcesses) forgetIfGone(taskID string, pgid int) {
	if groupAlive(pgid) {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.byTask[taskID], pgid)
	if len(p.byTask[taskID]) == 0 {
		delete(p.byTask, taskID)
	}
}

// killTask 结束任务的全部进程组，返回实际结束的组数。
func (p *taskProcesses) killTask(taskID string) int {
	p.mu.Lock()
	groups := p.byTask[taskID]
	delete(p.byTask, taskID)
	p.mu.Unlock()

	killed := 0
	for pgid := range groups {
		if err := syscall.Kill(-pgid, syscall.SIGKILL); err == nil {
			killed++
		}
	}
	return killed
}

// groupAlive 报告进程组内是否还有进程（信号 0 只做存在性检查）。
func groupAlive(pgid int) bool {
	err := syscall.Kill(-pgid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

// handleRelease 处理 POST /tasks/{id}/release：关闭任务的浏览器会话，结束其全部进程组与
// 脱离进程组的任务进程。只动该任务，并发任务不受影响；任务没有遗留资源时是空操作。
func (s *Server) handleRelease(w http.ResponseWriter, r *http.Request) {
	taskID := r.PathValue("id")
	if !isPathSafe(taskID) {
		writeError(w, http.StatusBadRequest, "task id must be [A-Za-z0-9._-]{1,64}")
		return
	}
	// 先优雅关浏览器（服务关闭 chromium 后退出），再兜底结束进程组。
	browsers, err := browser.ReleaseTask(r.Context(), taskID)
	if err != nil {
		releaseLog.Warn().Err(err).Str("task_id", taskID).Msg("部分浏览器会话关闭失败")
	}
	res := sandbox.ReleaseResult{
		Browsers:      browsers,
		ProcessGroups: s.procs.killTask(taskID),
		Escaped:       killEscaped(taskID),
	}
	releaseLog.Info().Str("task_id", taskID).Int("browsers", res.Browsers).
		Int("process_groups", res.ProcessGroups).Int("escaped", res.Escaped).Msg("任务资源已释放")
	writeJSON(w, http.StatusOK, res)
}
