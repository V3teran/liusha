package explorationgraph

import (
	"context"
	"fmt"

	"github.com/V3teran/liusha/internal/framework/core"
)

// 线索：执行者探通了一条走得通、但还没推进到结论的路径，在相关事实观察上标 opens_deeper
// （见 Node.OpensDeeper）。线索之后怎么走，由图上两样东西决定，都不写在标志本身上：
//   - 跟进：规划层排出的动作经 pursues 边指向线索（见 PlanRevision）；
//   - 不追：规划层判定不再跟进，在线索上批注理由（见 metaLeadDeclined）。
//
// 由此得出线索的几种处境，全部由图结构实时推导，不在观察上另存状态：
//   - 待深挖：没有被判不追，也没有算数的跟进动作（见 OpenLeads）；
//   - 跟进中：有 open / running 的动作在跟进它；
//   - 已办结：跟进它的动作已 completed / failed——做完了，或试过且出错。失败本身是
//     观察，由常规规划据它决定是否换法，线索不因此反复回到待深挖；
//   - 没跟进完：跟进它的动作被撤回（aborted）、否决（rejected）或迭代预算耗尽
//     （exhausted）——这些都没有把线索推进到结论，线索回到待深挖，由规划层重新安排。
//
// 已不追的线索见 DeclinedLeads。

// countsAsPursuit 判断跟进线索的动作在这个状态下算不算数：在办或已有结局算，半途
// 而废不算。
func countsAsPursuit(state *State) bool {
	if state == nil {
		return false
	}
	switch *state {
	case StateOpen, StateRunning, StateCompleted, StateFailed:
		return true
	default: // StateAborted / StateRejected / StateExhausted
		return false
	}
}

// OpenLeads 返回本任务待深挖的线索，按创建先后排列。这是「待深挖」的唯一判定口径：
// 规划摘要单列、监察者观测、修订校验都用它。
func (s *Store) OpenLeads(ctx context.Context, taskID string) ([]Node, error) {
	leads, err := s.leads(ctx, taskID)
	if err != nil {
		return nil, err
	}
	held, err := s.leadsHeldByPursuit(ctx, taskID)
	if err != nil {
		return nil, err
	}
	open := make([]Node, 0, len(leads))
	for _, lead := range leads {
		if lead.LeadDeclineReason() == "" && !held[lead.ID] {
			open = append(open, lead)
		}
	}
	return open, nil
}

// DeclinedLeads 返回本任务被判定不追的线索，按创建先后排列（理由见 Node.LeadDeclineReason）。
func (s *Store) DeclinedLeads(ctx context.Context, taskID string) ([]Node, error) {
	leads, err := s.leads(ctx, taskID)
	if err != nil {
		return nil, err
	}
	declined := make([]Node, 0)
	for _, lead := range leads {
		if lead.LeadDeclineReason() != "" {
			declined = append(declined, lead)
		}
	}
	return declined, nil
}

// PursuedLeads 返回某个动作经 pursues 边跟进的线索。执行者据它知道自己认领了什么：
// 认领线索的动作沿这条线索推进到结论。
func (s *Store) PursuedLeads(ctx context.Context, taskID, actionID string) ([]Node, error) {
	edges, err := s.graphStore.ListEdges(ctx, core.GraphEdgeQuery{
		TaskID: taskID, From: actionID, Relation: string(RelPursues),
	})
	if err != nil {
		return nil, fmt.Errorf("读取 pursues 边: %w", err)
	}
	leads := make([]Node, 0, len(edges))
	for _, e := range edges {
		node, err := s.GetNode(ctx, e.To)
		if err != nil {
			return nil, fmt.Errorf("读取线索 %s: %w", e.To, err)
		}
		leads = append(leads, *node)
	}
	return leads, nil
}

// leads 列出任务里被标为线索的事实观察，按创建先后排列。
func (s *Store) leads(ctx context.Context, taskID string) ([]Node, error) {
	observations, err := s.ListNodesByKind(ctx, taskID, core.KindObservation)
	if err != nil {
		return nil, fmt.Errorf("读取观察: %w", err)
	}
	leads := make([]Node, 0)
	for _, obs := range observations {
		if obs.OpensDeeper() {
			leads = append(leads, obs)
		}
	}
	return leads, nil
}

// leadsHeldByPursuit 收集被算数的跟进动作持有的线索 ID 集合。
func (s *Store) leadsHeldByPursuit(ctx context.Context, taskID string) (map[string]bool, error) {
	actions, err := s.ListNodesByKind(ctx, taskID, core.KindAction)
	if err != nil {
		return nil, fmt.Errorf("读取动作: %w", err)
	}
	state := make(map[string]*State, len(actions))
	for i := range actions {
		state[actions[i].ID] = actions[i].State
	}
	edges, err := s.graphStore.ListEdges(ctx, core.GraphEdgeQuery{TaskID: taskID, Relation: string(RelPursues)})
	if err != nil {
		return nil, fmt.Errorf("读取 pursues 边: %w", err)
	}
	held := make(map[string]bool, len(edges))
	for _, e := range edges {
		if countsAsPursuit(state[e.From]) {
			held[e.To] = true
		}
	}
	return held, nil
}
