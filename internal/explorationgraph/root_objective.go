package explorationgraph

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/V3teran/liusha/internal/framework/core"
)

// 根目标代表任务本身：每个任务恰好一个（DB 部分唯一索引兜底），只由
// EnsureRootObjective 创建，内容只有目标描述（description）。目标所指的对象范围
// （如 Web 域的主机）属于任务与领域，不进根目标。

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

// EnsureRootObjective 返回任务的根目标，不存在时以 goal 创建。并发创建由唯一索引兜底：
// 后到者写入失败后重读到先到者创建的那个。
func (s *Store) EnsureRootObjective(ctx context.Context, taskID, goal string) (Node, error) {
	if root, ok, err := s.RootObjectiveOf(ctx, taskID); err != nil || ok {
		return root, err
	}
	open := StateOpen
	node := Node{
		ID:         uuid.New().String(),
		TaskID:     taskID,
		Kind:       core.KindObjective,
		Content:    MustJSON(map[string]string{"description": goal}),
		State:      &open,
		Priority:   PriorityHigh,
		SourceType: SourceUser,
		CreatedAt:  time.Now(),
	}
	if _, err := s.CreateNode(ctx, node); err != nil {
		if root, ok, rErr := s.RootObjectiveOf(ctx, taskID); rErr == nil && ok {
			return root, nil
		}
		return Node{}, fmt.Errorf("创建根目标: %w", err)
	}
	return node, nil
}

// SetGoal 改写任务的目标（根目标的描述）；根目标还不存在时以新目标创建。
func (s *Store) SetGoal(ctx context.Context, taskID, goal string) error {
	root, err := s.EnsureRootObjective(ctx, taskID, goal)
	if err != nil {
		return err
	}
	if root.Goal() == goal {
		return nil
	}
	return s.UpdateNodeContent(ctx, root.ID, MustJSON(map[string]string{"description": goal}))
}
