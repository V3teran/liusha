package explorationgraph

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/V3teran/liusha/internal/framework/core"
)

// 线索：执行者探通了一条走得通、但还没走到结论的路径，在相关事实观察上标 opens_deeper
// （见 Node.OpensDeeper）。线索是探索里真正做分配决策的那一级——它具体、有明确的延续，
// 结局也清楚；方向（派生目标）只作分组，不当这一级用。
//
// 线索之后怎么走，由图上的事实决定，都不写在标志本身上：
//   - 跟进：为它排出的动作经 pursues 边认领它（见 PlanRevision），由规划层写入，只追加；
//   - 关闭：规划层的决定批注在它身上（见 metaLeadClosed），或由跟进的结局推导（见
//     deriveLeads）。
//
// 线索只有开与关两态，处境全部由图结构实时推导，不在观察上另存状态（见 Leads）：关掉的
// 靠重新出现复活——路真的还通，别的动作会在同一个对象上再报出新线索。

// LeadClosureKind 是线索因何关闭。
type LeadClosureKind string

// 关闭类别。前五种由规划者判定（Declared），只能经规划修订写入；后四种由跟进的结局推导。
const (
	ClosureNotWorth       LeadClosureKind = "not_worth"       // 并不是一条走得通的路，或价值够不上一个动作
	ClosureOutOfScope     LeadClosureKind = "out_of_scope"    // 超出任务目标
	ClosurePremiseRefuted LeadClosureKind = "premise_refuted" // 前提已被证伪
	ClosureExhausted      LeadClosureKind = "exhausted"       // 跟进过且已走到头，或已被之前的动作覆盖
	ClosureBlocked        LeadClosureKind = "blocked"         // 受阻：想跟进但过不去，理由写明受阻于什么
	ClosureConcluded      LeadClosureKind = "concluded"       // 有结论：跟进它的动作报出了主张
	ClosureDry            LeadClosureKind = "dry"             // 干涸：跟进完，图上没有多出任何东西
	ClosureVetoed         LeadClosureKind = "vetoed"          // 被否决关闭
)

// Declared 判断这一类是否由规划者判定——规划修订只接受这些类别。
func (k LeadClosureKind) Declared() bool {
	switch k {
	case ClosureNotWorth, ClosureOutOfScope, ClosurePremiseRefuted, ClosureExhausted, ClosureBlocked:
		return true
	}
	return false
}

// vetoLimit 是同一条线索容许被否决（只否决这一步）的次数：否决与重开来回打转的止损，
// 累计到这一次数即关闭，不再重开。
const vetoLimit = 2

// Lead 是一条线索与它由图结构推导出的处境。
type Lead struct {
	Node // 线索本身：一条标了 opens_deeper 的事实观察

	ProducerID string // 产出它的动作（generates 边；查不到为空）

	// InFlight：有在办（待执行或执行中）的动作在跟进它——结局还不知道，它会推进这条线索。
	InFlight bool

	Truncated        int    // 跟进它的动作因迭代预算耗尽而没做完的次数
	TruncationStreak int    // 连续没做完的次数：自最近一次已了结的跟进向前数，中途被别的方式打断即归零
	Faults           int    // 跟进它的动作执行出错（故障、停摆）的次数
	Progressed       int    // 跟进过、推进了但没到结论的次数
	Vetoes           int    // 跟进它的动作被否决（只否决这一步）的次数
	LastVetoReason   string // 最近一次否决的理由

	// Closure 是关闭的原因，nil 表示线索仍开着。
	Closure *LeadClosure
}

// Closed 判断线索是否已关闭。
func (l Lead) Closed() bool { return l.Closure != nil }

// Pending 判断线索是否待深挖：开着，且当前没有在办的动作在跟进它。
func (l Lead) Pending() bool { return l.Closure == nil && !l.InFlight }

// close 记下关闭的原因；已经关闭的不改写——原因以先到的那次为准。
func (l *Lead) close(kind LeadClosureKind, reason string) {
	if l.Closure == nil {
		l.Closure = &LeadClosure{Kind: kind, Reason: reason}
	}
}

// vetoed 按一次否决评判的范围结算：连线索一起否决的直接关闭；只否决这一步的记一次，
// 累计到 vetoLimit 即关闭。
func (l *Lead) vetoed(action Node) {
	reason := ""
	if action.StateReason != nil {
		reason = *action.StateReason
	}
	if action.RejectionScope() == RejectLead {
		l.close(ClosureVetoed, reason)
		return
	}
	l.Vetoes++
	l.LastVetoReason = reason
	if l.Vetoes >= vetoLimit {
		l.close(ClosureVetoed, fmt.Sprintf("跟进它的动作累计被否决 %d 次；最近一次：%s", l.Vetoes, reason))
	}
}

// Leads 返回任务里的全部线索与各自由图结构推导出的处境，按创建先后排列。
//
// 一次跟进的结局按它的停止原因归类，不按动作有没有跑完一刀切：
//   - completed：看它在图上留下了什么（见 pursuitOutcome）——报出了主张即有结论，关闭；
//     没有主张但留下了新东西即推进了，线索仍待深挖（这条路还能往下走，再追还是关由
//     规划层定）；什么都没留下即干涸，关闭——线索的含义就是这条路还能往下走，跟进完
//     却没有新东西是对它的直接反证，一次就够；
//   - exhausted（迭代预算耗尽）：没做完。不计好坏，线索重新开着，连续没做完的次数
//     累加——连续多次说明这条线索对一个动作来说太大，该拆分而不是放弃；
//   - failed（执行出错、停摆）：故障，与线索的好坏无关，线索重新开着；
//   - rejected（否决）：评判这一步的，线索带着否决理由重新开着，累计 vetoLimit 次关闭；
//     连线索一起评判的（见 RejectLead）直接关闭；
//   - aborted（收回）：不含对错判断，什么都不计，线索重新开着。
func (s *Store) Leads(ctx context.Context, taskID string) ([]Lead, error) {
	observations, err := s.ListNodesByKind(ctx, taskID, core.KindObservation)
	if err != nil {
		return nil, fmt.Errorf("读取观察: %w", err)
	}
	if !slices.ContainsFunc(observations, func(n Node) bool { return n.OpensDeeper() }) {
		return nil, nil
	}
	actions, err := s.ListNodesByKind(ctx, taskID, core.KindAction)
	if err != nil {
		return nil, fmt.Errorf("读取动作: %w", err)
	}
	edges, err := s.ListEdgesByTask(ctx, taskID)
	if err != nil {
		return nil, fmt.Errorf("读取边: %w", err)
	}
	return deriveLeads(observations, actions, edges), nil
}

// OpenLeads 返回本任务待深挖的线索（开着、且没有在办的动作在跟进），按创建先后排列。
// 这是「待跟进」的唯一判定口径：规划摘要单列、监察者观测、修订校验都用它。
func (s *Store) OpenLeads(ctx context.Context, taskID string) ([]Lead, error) {
	leads, err := s.Leads(ctx, taskID)
	if err != nil {
		return nil, err
	}
	open := make([]Lead, 0, len(leads))
	for _, lead := range leads {
		if lead.Pending() {
			open = append(open, lead)
		}
	}
	return open, nil
}

// ClosedLeads 返回本任务已关闭的线索（带着关闭原因），按创建先后排列。
func (s *Store) ClosedLeads(ctx context.Context, taskID string) ([]Lead, error) {
	leads, err := s.Leads(ctx, taskID)
	if err != nil {
		return nil, err
	}
	closed := make([]Lead, 0, len(leads))
	for _, lead := range leads {
		if lead.Closed() {
			closed = append(closed, lead)
		}
	}
	return closed, nil
}

// PursuedLeads 返回某个动作经 pursues 边认领的线索——指令为它们而写，执行者据它知道
// 自己接手的是哪条线索，沿它推进到结论。
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

// deriveLeads 由任务里的观察、动作与边推导每条线索的处境（归类见 Leads）。
func deriveLeads(observations, actions []Node, edges []Edge) []Lead {
	leads := make([]Lead, 0)
	flagged := make(map[string]bool)
	obsByID := make(map[string]*Node, len(observations))
	for i := range observations {
		obsByID[observations[i].ID] = &observations[i]
		if observations[i].OpensDeeper() {
			flagged[observations[i].ID] = true
			leads = append(leads, Lead{Node: observations[i]})
		}
	}
	if len(leads) == 0 {
		return nil
	}
	// 创建先后是推导的契约之一，不依赖存储的返回顺序；同一时刻创建的按 ID 定序，结果确定。
	slices.SortStableFunc(leads, func(a, b Lead) int {
		if byTime := a.CreatedAt.Compare(b.CreatedAt); byTime != 0 {
			return byTime
		}
		return strings.Compare(a.ID, b.ID)
	})
	actionByID := make(map[string]*Node, len(actions))
	for i := range actions {
		actionByID[actions[i].ID] = &actions[i]
	}

	var (
		producer = map[string]string{}   // 观察ID → 产出它的动作
		produced = map[string][]string{} // 动作ID → 它产出的观察
		pursuits = map[string][]Node{}   // 线索ID → 跟进它的动作（pursues）
	)
	for _, e := range edges {
		switch e.Rel {
		case RelGenerates:
			if _, ok := actionByID[e.SrcID]; ok {
				produced[e.SrcID] = append(produced[e.SrcID], e.DstID)
				if _, isObservation := obsByID[e.DstID]; isObservation {
					producer[e.DstID] = e.SrcID
				}
			}
		case RelPursues:
			if a, ok := actionByID[e.SrcID]; ok && flagged[e.DstID] {
				pursuits[e.DstID] = append(pursuits[e.DstID], *a)
			}
		}
	}

	// 同一陈述 → 报出过它的动作：判断一次跟进有没有留下新东西时，别的动作已经报过同一
	// 陈述的观察不算新。
	reporters := map[string]map[string]bool{}
	for i := range observations {
		if statement := observations[i].Statement(); statement != "" {
			if reporters[statement] == nil {
				reporters[statement] = map[string]bool{}
			}
			reporters[statement][producer[observations[i].ID]] = true
		}
	}

	for i := range leads {
		lead := &leads[i]
		lead.ProducerID = producer[lead.ID]
		if closure, ok := lead.LeadClosure(); ok {
			lead.Closure = &closure
			continue
		}
		// 按结局发生的先后结算：存储返回边的顺序不一（Postgres 新的在前），而关闭的原因以
		// 先到的那次为准。
		ended := slices.Clone(pursuits[lead.ID])
		slices.SortStableFunc(ended, func(a, b Node) int { return a.UpdatedAt.Compare(b.UpdatedAt) })
		for _, action := range ended {
			if action.State == nil {
				continue
			}
			switch *action.State {
			case StateOpen, StateRunning:
				lead.InFlight = true
			case StateCompleted:
				concluded, progressed := pursuitOutcome(produced[action.ID], obsByID, producer, reporters)
				switch {
				case concluded:
					lead.close(ClosureConcluded, "")
				case progressed:
					lead.Progressed++
				default:
					lead.close(ClosureDry, "")
				}
			case StateExhausted:
				lead.Truncated++
			case StateFailed:
				lead.Faults++
			case StateRejected:
				lead.vetoed(action)
			}
		}
		lead.TruncationStreak = truncationStreak(pursuits[lead.ID])
	}
	return leads
}

// pursuitOutcome 判定一次已完成的跟进把线索推到了哪一步：报出了主张即有结论；没有主张
// 但留下了新东西（新的线索、别处没报过的事实）即推进了；什么都没留下即干涸。杂记
// （note）不算新东西——它是兜底类别，动作零观察完结时系统补的执行总结也落在这里。
func pursuitOutcome(observationIDs []string, obsByID map[string]*Node, producer map[string]string, reporters map[string]map[string]bool) (concluded, progressed bool) {
	for _, id := range observationIDs {
		obs, ok := obsByID[id]
		if !ok {
			continue
		}
		switch {
		case obs.IsHypothesis():
			return true, true
		case obs.OpensDeeper():
			progressed = true
		case !obs.IsFact() || !informativeCategory(obs.Category()):
		case len(reporters[obs.Statement()]) <= 1:
			progressed = true
		}
	}
	return false, progressed
}

// informativeCategory 判断观察类别是否陈述了世界的状态：对象清单与三类机制事实是，
// 杂记与无法归类的不是。
func informativeCategory(category ObservationCategory) bool {
	return category != CategoryNote && category != ""
}

// endedStates 是有了结局的跟进动作所处的状态：被收回（aborted）不含对错判断，不在其列。
var endedStates = []State{StateCompleted, StateExhausted, StateFailed, StateRejected}

// truncationStreak 自最近一次已了结的跟进向前数连续没做完（迭代预算耗尽）的次数。
func truncationStreak(pursuits []Node) int {
	ended := make([]Node, 0, len(pursuits))
	for _, action := range pursuits {
		if action.State != nil && slices.Contains(endedStates, *action.State) {
			ended = append(ended, action)
		}
	}
	slices.SortStableFunc(ended, func(a, b Node) int { return b.UpdatedAt.Compare(a.UpdatedAt) })
	streak := 0
	for _, action := range ended {
		if *action.State != StateExhausted {
			break
		}
		streak++
	}
	return streak
}
