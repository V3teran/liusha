package httpapi

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/V3teran/liusha/internal/agentrun"
)

// LiveRunReader 读任务当前存活的一轮 run（*agentrun.Store 满足）。
type LiveRunReader interface {
	LiveByTask(ctx context.Context, taskID string) (*agentrun.Run, error)
}

// getTaskRunHandler 处理 GET /tasks/:id/run：任务当前存活的一轮 run 与它的存活心跳。
// 心跳是恢复监控判定这轮 run 是否还在被推进的依据（runner/heartbeat.go），也是外界判断
// 「任务是不是卡住了」应当看的信号——按已完成的调用记录判断，一个有超时的长操作在途时会被
// 误当成停摆。没有存活的 run 时 run 为 null。
func getTaskRunHandler(tasks TaskReader, runs LiveRunReader) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !requireTask(c, tasks) {
			return
		}
		run, err := runs.LiveByTask(c.Request.Context(), c.Param("id"))
		if err != nil {
			respondErr(c, http.StatusInternalServerError, err)
			return
		}
		if run == nil {
			c.JSON(http.StatusOK, gin.H{"run": nil})
			return
		}
		c.JSON(http.StatusOK, gin.H{"run": gin.H{
			"id": run.ID, "status": run.Status, "created_at": run.CreatedAt, "heartbeat_at": run.HeartbeatAt,
		}})
	}
}
