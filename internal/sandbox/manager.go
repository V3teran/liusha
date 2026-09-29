package sandbox

import (
	"context"
	"time"
)

// Manager 是 Sandbox 生命周期管理的统一接口。
// 不同的实现策略（单例、简单池、Per-User 池、全局调度器）都实现此接口，
// 使得切换策略时无需修改调用方代码。
type Manager interface {
	// Acquire 获取一个可用的 Sandbox。
	// req 包含任务 ID、用户 ID 等上下文信息。
	Acquire(ctx context.Context, req AcquireRequest) (*Sandbox, error)

	// Release 释放 Sandbox。
	// 不同实现的行为：
	//   - SingletonManager: 空操作（不销毁容器）
	//   - PoolManager: 放回池中复用
	//   - GlobalScheduler: 根据调度策略决定是否回收
	Release(ctx context.Context, sb *Sandbox) error

	// Healthz 健康检查。
	// 返回 nil 表示管理器及其管理的 Sandbox 都健康。
	Healthz(ctx context.Context) error

	// Metrics 返回当前监控指标。
	Metrics() ManagerMetrics

	// Shutdown 优雅关闭，清理所有资源。
	Shutdown(ctx context.Context) error
}

// AcquireRequest 是获取 Sandbox 的请求参数。
type AcquireRequest struct {
	TaskID   string        // 任务 ID（必填）
	UserID   string        // 用户 ID（可选，Per-User 池时使用）
	Priority int           // 优先级（可选，全局调度器时使用，0=normal）
	Timeout  time.Duration // 获取超时（可选，0=使用默认值）
	WorkDir  string        // 工作目录（可选，为空则自动生成 /work/{taskID}）
}

// Sandbox 是对容器客户端的封装，增加任务隔离能力。
type Sandbox struct {
	Client    Client // 底层容器客户端
	ID        string // 容器 ID
	TaskID    string // 当前关联的任务 ID
	WorkDir   string // 任务工作目录
	CreatedAt time.Time
}

// ManagerMetrics 是 Sandbox 管理器的监控指标。
type ManagerMetrics struct {
	// 基础指标（所有实现都支持）
	TotalSandboxes   int     // 总容器数
	HealthyCount     int     // 健康容器数
	AcquireLatencyMs float64 // 平均获取延迟（毫秒）
	AcquireTotal     int64   // 累计获取次数
	AcquireErrors    int64   // 累计获取失败次数

	// 高级指标（池化实现支持）
	WarmPoolSize     int     // 预热池大小
	BusyPoolSize     int     // 使用中容器数
	IdlePoolSize     int     // 空闲容器数
	QueueLength      int     // 等待队列长度
	UtilizationRatio float64 // 利用率（busy / total）

	// 生命周期指标
	CreatedTotal   int64 // 累计创建容器数
	DestroyedTotal int64 // 累计销毁容器数
	ResetTotal     int64 // 累计重置次数
}

// PrepareWorkDir 准备任务工作目录。
// 注意：实际的工作目录由 sandbox-server 管理（/liusha/<task_id>/<agent_id>/workspace/）。
// 这里不需要创建任何目录，sandbox-server 会自动创建。
func (sb *Sandbox) PrepareWorkDir(_ context.Context) error {
	// 设置工作目录标识（用于日志）
	if sb.WorkDir == "" {
		sb.WorkDir = "/liusha/" + sb.TaskID
	}

	// 不需要实际创建目录，sandbox-server 会在 exec 时自动创建
	// /liusha/<task_id>/<agent_id>/workspace/ 和 output/

	return nil
}

// CleanupWorkDir 清理任务工作目录。
// 注意：清理整个 Task 级别的目录（包括所有 Agent 的子目录）。
func (sb *Sandbox) CleanupWorkDir(ctx context.Context) error {
	if sb.WorkDir == "" {
		return nil
	}

	// 删除整个 Task 目录（/liusha/<task_id>/）
	_, err := sb.Client.Exec(ctx, ExecRequest{
		TaskID:         sb.TaskID,
		AgentID:        "cleanup",
		Command:        "rm -rf " + sb.WorkDir,
		TimeoutSeconds: 30,
	})
	return err
}

// SoftReset 软重置 Sandbox（清理任务目录，不销毁容器）。
func (sb *Sandbox) SoftReset(ctx context.Context) error {
	// 1. 清理任务目录（整个 /liusha/<task_id>/）
	if err := sb.CleanupWorkDir(ctx); err != nil {
		return err
	}

	// 2. Kill 残留进程（增强版：先 TERM，等待 2 秒，再 KILL）
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
		TaskID:         "reset",
		AgentID:        "cleanup",
		Command:        cleanupScript,
		TimeoutSeconds: 10,
	})

	// 3. 清理临时文件（保留重要的系统目录）
	// 只清理用户可能创建的临时文件，不影响系统文件
	_, _ = sb.Client.Exec(ctx, ExecRequest{
		TaskID:         "reset",
		AgentID:        "cleanup",
		Command:        "find /tmp -mindepth 1 -maxdepth 1 ! -name '.X*' ! -name '.ICE-unix' -exec rm -rf {} + 2>/dev/null || true",
		TimeoutSeconds: 10,
	})

	// 4. 清理共享内存（防止内存泄漏）
	_, _ = sb.Client.Exec(ctx, ExecRequest{
		TaskID:         "reset",
		AgentID:        "cleanup",
		Command:        "rm -rf /dev/shm/* 2>/dev/null || true",
		TimeoutSeconds: 5,
	})

	// 5. 清理可能的僵尸进程
	_, _ = sb.Client.Exec(ctx, ExecRequest{
		TaskID:         "reset",
		AgentID:        "cleanup",
		Command:        "ps aux | awk '$8==\"Z\" {print $2}' | xargs -r kill -9 2>/dev/null || true",
		TimeoutSeconds: 5,
	})

	// 6. 重置状态
	sb.TaskID = ""
	sb.WorkDir = ""

	return nil
}
