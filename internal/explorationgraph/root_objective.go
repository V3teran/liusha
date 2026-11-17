package explorationgraph

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/V3teran/liusha/internal/framework/core"
)

// 根目标代表任务本身：每个任务恰好一个（DB 部分唯一索引兜底），内容只有目标描述
// （description），与任务行的 goal 是同一个目标——两者只经 task.Store 在同一事务内写入
// （任务创建时 CreateRootObjective，改写目标时 WriteRootGoal）。目标所指的对象范围
// （如目标主机）属于任务行，不进根目标。

// Goal 返回目标节点的目标描述（非目标节点或内容缺失时为空）。
func (n *Node) Goal() string {
	if n.Kind != core.KindObjective || len(n.Content) == 0 {
		return ""
	}
	var c struct {
		Description string `json:"description"`
	}
	if json.Unmarshal(n.Content, &c) != nil {
		return ""
	}
	return c.Description
}

// GoalBasis 是一次规划决策所依据的任务目标：根目标及读取它时的版本。根目标的版本
// 随目标改写与任务状态迁移递增，动作挂到它下面不改变它——版本未变即规划时看到的
// 目标仍然成立。规划结论（新动作、结果分析的修订、收敛）都以它为前置落定。
type GoalBasis struct {
	ObjectiveID string
	Version     int64
}

// ErrGoalChanged 表示规划所依据的目标在规划期间已被改写（或任务状态已迁移）：
// 结论按旧目标得出，整批作废，按新目标重新规划。
var ErrGoalChanged = errors.New("规划依据的任务目标已变化")

// GoalBasis 读取任务当前的目标依据（没有根目标即无从规划，返回错误）。
func (s *Store) GoalBasis(ctx context.Context, taskID string) (GoalBasis, error) {
	root, ok, err := s.RootObjectiveOf(ctx, taskID)
	if err != nil {
		return GoalBasis{}, err
	}
	if !ok {
		return GoalBasis{}, fmt.Errorf("任务 %s 没有根目标", taskID)
	}
	return GoalBasis{ObjectiveID: root.ID, Version: root.Version}, nil
}

// RootObjectiveOf 读取任务的根目标（不存在返回 false）。
func (s *Store) RootObjectiveOf(ctx context.Context, taskID string) (Node, bool, error) {
	objectives, err := s.ListNodesByKind(ctx, taskID, core.KindObjective)
	if err != nil {
		return Node{}, false, fmt.Errorf("读取目标: %w", err)
	}
	root, ok := RootObjective(objectives)
	return root, ok, nil
}

// rootMetadata 是根目标的身份元数据：来源 user（代表任务本身）、优先级 high。
var rootMetadata = MustJSON(map[string]string{"source_type": string(SourceUser), "priority": string(PriorityHigh)})

// CreateRootObjective 在调用方事务内为新任务建根目标（task.Store.Create 的一部分）。
func CreateRootObjective(ctx context.Context, db Execer, taskID, goal string) error {
	_, err := db.Exec(ctx, `
		INSERT INTO exploration_node (id, task_id, kind, state, content, metadata)
		VALUES (gen_random_uuid(), $1::uuid, 'objective', 'open', jsonb_build_object('description', $2::text), $3::jsonb)`,
		taskID, goal, []byte(rootMetadata))
	if err != nil {
		return fmt.Errorf("创建根目标: %w", err)
	}
	return nil
}

// WriteRootGoal 在调用方事务内改写根目标的描述（task.Store.SetGoal 的一部分）。改写
// 递增根目标版本，按旧目标做出的规划结论随之失效（见 GoalBasis）。
func WriteRootGoal(ctx context.Context, db Execer, taskID, goal string) error {
	tag, err := db.Exec(ctx, `
		UPDATE exploration_node SET content = jsonb_build_object('description', $2::text), updated_at = now()
		 WHERE task_id = $1::uuid AND kind = 'objective' AND coalesce(metadata->>'source_type', '') <> 'planner'`,
		taskID, goal)
	if err != nil {
		return fmt.Errorf("改写根目标: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return fmt.Errorf("任务 %s 应恰有一个根目标，改写了 %d 个", taskID, tag.RowsAffected())
	}
	return nil
}
