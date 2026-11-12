package server

import (
	"net/http"

	"github.com/V3teran/liusha/internal/sandbox"
	"github.com/V3teran/liusha/internal/sandbox/browser"
)

// handleRelease 处理 POST /tasks/{id}/release：关闭任务的浏览器会话，结束任务的全部进程。
// 只动该任务，并发任务不受影响；任务没有遗留资源时是空操作。
func (s *Server) handleRelease(w http.ResponseWriter, r *http.Request) {
	taskID := r.PathValue("id")
	if !isPathSafe(taskID) {
		writeError(w, http.StatusBadRequest, "task id must be [A-Za-z0-9._-]{1,64}")
		return
	}
	// 先优雅关浏览器（服务关闭 chromium 后退出），再兜底结束任务的其余进程。
	browsers, err := browser.ReleaseTask(r.Context(), s.cfg.BrowserDir, taskID)
	if err != nil {
		releaseLog.Warn().Err(err).Str("task_id", taskID).Msg("部分浏览器会话关闭失败")
	}
	res := sandbox.ReleaseResult{Browsers: browsers, Processes: s.procs.killTask(taskID)}
	s.browsers.forgetTask(s, taskID)
	releaseLog.Info().Str("task_id", taskID).Int("browsers", res.Browsers).
		Int("processes", res.Processes).Msg("任务资源已释放")
	writeJSON(w, http.StatusOK, res)
}
