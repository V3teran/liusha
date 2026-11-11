package sandbox

import (
	"context"
	"time"
)

// Manager 是沙箱实例的生命周期管理接口（实现：WarmPoolManager——单个暖池容器由并发任务
// 共享，工作区按 批次 / 任务 两级隔离）。
type Manager interface {
	// Acquire 为任务借出沙箱实例。
	Acquire(ctx context.Context, req AcquireRequest) (*Instance, error)

	// Release 归还 Acquire 借出的实例。容器内可能仍有其他任务在跑，归还不做任何
	// 容器级清理——清理在容器空闲后的下一次借出前进行。
	Release(ctx context.Context, sb *Instance) error

	// Healthz 健康检查：nil 表示管理器及其沙箱实例都健康。
	Healthz(ctx context.Context) error

	// Shutdown 优雅关闭，清理所有资源。
	Shutdown(ctx context.Context) error
}

// AcquireRequest 是借出沙箱实例的请求参数。
type AcquireRequest struct {
	AssignmentID string // 批次 ID（工作区一级分组，必填）
	TaskID       string // 任务 ID（必填）
}

// Instance 是一个活的沙箱实例：对容器客户端的封装，附带借用方的批次 / 任务归属。
type Instance struct {
	Client       Client // 底层容器客户端
	ID           string // 容器 ID
	AssignmentID string // 当前关联的批次 ID
	TaskID       string // 当前关联的任务 ID
	CreatedAt    time.Time
}

// SoftReset 软重置容器（不销毁容器、不动任务工作区）：杀残留进程、清临时文件与共享内存。
// 会波及容器内全部进程，只能在容器无任何持有者时调用。
func (sb *Instance) SoftReset(ctx context.Context) {
	// 1. Kill 残留进程（增强版：先 TERM，等待 2 秒，再 KILL）
	// 杀掉所有非 init/sandbox-server 的进程
	cleanupScript := `
		# 获取 sandbox-server 的 PID（保护它不被杀）
		SERVER_PID=$(pgrep -f sandbox-server | head -1)

		# 杀掉其他所有进程（排除 PID 1, sandbox-server, 以及当前 bash）
		for pid in $(ps aux | awk 'NR>1 {print $2}'); do
			if [ "$pid" != "1" ] && [ "$pid" != "$SERVER_PID" ] && [ "$pid" != "$$" ]; then
				kill -TERM "$pid" 2>/dev/null || true
			fi
		done

		# 等待进程优雅退出
		sleep 2

		# 强制杀掉仍然存活的进程
		for pid in $(ps aux | awk 'NR>1 {print $2}'); do
			if [ "$pid" != "1" ] && [ "$pid" != "$SERVER_PID" ] && [ "$pid" != "$$" ]; then
				kill -KILL "$pid" 2>/dev/null || true
			fi
		done
	`
	_, _ = sb.Client.Exec(ctx, ExecRequest{
		AssignmentID:   "_system",
		TaskID:         "reset",
		AgentID:        "cleanup",
		Command:        cleanupScript,
		TimeoutSeconds: 10,
	})

	// 2. 清理临时文件（保留重要的系统目录）
	// 只清理用户可能创建的临时文件，不影响系统文件
	_, _ = sb.Client.Exec(ctx, ExecRequest{
		AssignmentID:   "_system",
		TaskID:         "reset",
		AgentID:        "cleanup",
		Command:        "find /tmp -mindepth 1 -maxdepth 1 ! -name '.X*' ! -name '.ICE-unix' -exec rm -rf {} + 2>/dev/null || true",
		TimeoutSeconds: 10,
	})

	// 3. 清理共享内存（防止内存泄漏）
	_, _ = sb.Client.Exec(ctx, ExecRequest{
		AssignmentID:   "_system",
		TaskID:         "reset",
		AgentID:        "cleanup",
		Command:        "rm -rf /dev/shm/* 2>/dev/null || true",
		TimeoutSeconds: 5,
	})

	// 4. 清理可能的僵尸进程
	_, _ = sb.Client.Exec(ctx, ExecRequest{
		TaskID:         "reset",
		AgentID:        "cleanup",
		Command:        "ps aux | awk '$8==\"Z\" {print $2}' | xargs -r kill -9 2>/dev/null || true",
		TimeoutSeconds: 5,
	})
}
