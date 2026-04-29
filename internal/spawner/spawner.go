// Package spawner 把"sniffer 调 spawn_subtask action"翻译为
// task.Store.Create + worker.Client.Enqueue，并强制三条约束（spec §4.3）：
//   - Spawn depth ≤ 1：父任务自身已是子任务时拒绝（避免无限递归）
//   - 单 parent inflight 子任务 ≤ 10
//   - 单 engagement inflight 任务 ≤ 20
//
// 设计要点：
//   - 依赖窄接口（TaskStore / Enqueuer），单测可用 fake，不依赖 docker。
//   - 子任务 role 继承父任务，engagement 继承父任务（spec §4.3）。
//   - Create 与 Enqueue 共用同一 task ID（asynq.TaskID）保证幂等。
package spawner

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/V3teran/liusha/internal/task"
	"github.com/V3teran/liusha/internal/worker"
)

// 默认上限（构造函数零值时回填）。
const (
	defaultMaxChildrenPerParent     = 10
	defaultMaxInflightPerEngagement = 20
)

// TaskStore 是 Spawner 依赖的最小持久化接口。
// *task.Store 自动满足（Create / GetByID / CountInflightChildren / CountInflightInEngagement）。
type TaskStore interface {
	GetByID(ctx context.Context, id string) (task.Task, error)
	Create(ctx context.Context, p task.NewParams) (string, error)
	CountInflightChildren(ctx context.Context, parentID string) (int, error)
	CountInflightInEngagement(ctx context.Context, engagementID string) (int, error)
}

// Enqueuer 是 Spawner 依赖的最小入队接口。
// *worker.Client 自动满足。
type Enqueuer interface {
	Enqueue(ctx context.Context, role worker.Role, p worker.Payload) (string, string, error)
}

// Limits 是 Spawner 的并发上限。零值会被替换为 defaults。
type Limits struct {
	MaxChildrenPerParent     int
	MaxInflightPerEngagement int
}

// Spawner 把 spawn_subtask 行为翻译为 (Create + Enqueue)。
type Spawner struct {
	tasks  TaskStore
	enq    Enqueuer
	limits Limits
}

// New 构造 Spawner；Limits 零值时使用默认 (10/parent, 20/engagement)。
func New(tasks TaskStore, enq Enqueuer, l Limits) *Spawner {
	if l.MaxChildrenPerParent <= 0 {
		l.MaxChildrenPerParent = defaultMaxChildrenPerParent
	}
	if l.MaxInflightPerEngagement <= 0 {
		l.MaxInflightPerEngagement = defaultMaxInflightPerEngagement
	}
	return &Spawner{tasks: tasks, enq: enq, limits: l}
}

// Spawn 创建一条子任务并入队，返回新 task ID。
//
// 流程：
//  1. GetByID(parent) → 校验 ParentTaskID 必须为空（depth ≤ 1）
//  2. CountInflightChildren ≤ 10；CountInflightInEngagement ≤ 20
//  3. tasks.Create（继承父 role / engagement，写 parent_task_id / skill / input / budget）
//  4. enq.Enqueue（payload.TaskID = 新 task id，保证幂等）
//
// 注意：第 4 步失败时第 3 步已落库——本函数透传错误，不做补偿。上层可由
// asynq 的 enqueue retry 或离线 reaper（plan 1 T28）兜底。
func (s *Spawner) Spawn(
	ctx context.Context,
	parentTaskID string,
	skill string,
	input, budget json.RawMessage,
) (string, error) {
	parent, err := s.tasks.GetByID(ctx, parentTaskID)
	if err != nil {
		return "", fmt.Errorf("get parent task %s: %w", parentTaskID, err)
	}
	if parent.ParentTaskID != nil {
		return "", fmt.Errorf("spawn depth >= 1, cannot spawn from subtask %s", parentTaskID)
	}

	if n, err := s.tasks.CountInflightChildren(ctx, parentTaskID); err != nil {
		return "", fmt.Errorf("count inflight children: %w", err)
	} else if n >= s.limits.MaxChildrenPerParent {
		return "", fmt.Errorf("inflight children %d >= limit %d for parent %s",
			n, s.limits.MaxChildrenPerParent, parentTaskID)
	}

	if n, err := s.tasks.CountInflightInEngagement(ctx, parent.EngagementID); err != nil {
		return "", fmt.Errorf("count inflight in engagement: %w", err)
	} else if n >= s.limits.MaxInflightPerEngagement {
		return "", fmt.Errorf("inflight in engagement %d >= limit %d (engagement=%s)",
			n, s.limits.MaxInflightPerEngagement, parent.EngagementID)
	}

	pid := parentTaskID
	childID, err := s.tasks.Create(ctx, task.NewParams{
		EngagementID: parent.EngagementID,
		ParentTaskID: &pid,
		Role:         parent.Role,
		Skill:        skill,
		Input:        input,
		Budget:       budget,
	})
	if err != nil {
		return "", fmt.Errorf("create child task: %w", err)
	}

	role := worker.Role(parent.Role)
	payload := worker.Payload{
		TaskID:       childID,
		EngagementID: parent.EngagementID,
		Role:         role,
		Skill:        skill,
		Input:        input,
	}
	if _, _, err := s.enq.Enqueue(ctx, role, payload); err != nil {
		return "", fmt.Errorf("enqueue child task %s: %w", childID, err)
	}
	return childID, nil
}
