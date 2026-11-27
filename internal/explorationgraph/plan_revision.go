package explorationgraph

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/V3teran/liusha/internal/framework/core"
)

// PlanRevision 是规划层对探索图的一次修订：结果分析提出新方向，并把被分析的结果标记为
// 已分析；常规规划新增动作（可以认领先前待深挖的线索）、关闭线索。一次修订是同一个
// 规划决策的不同侧面，整体原子落图——不会出现决策只落了一半、或结论已落而结果仍待
// 分析，从而在重试或下一个 run 里被重复写入。
//
// 方向只作分组：任务进行期间方向始终进行中，由结果分析追加，规划层不收敛它；任务结束
// 时随任务落定（见 SyncWithTask）。根目标代表任务本身，同样只随任务终态由控制面收敛；
// 动作挂在它所属的目标下（见 ActionDraft.ObjectiveID）。
//
// 修订以 Basis（规划所依据的目标）为前置：规划期间目标被改写，修订整体作废
// （ErrGoalChanged），不会把按旧目标得出的结论写进新目标的图。
type PlanRevision struct {
	Basis           GoalBasis        // 规划所依据的目标（必填，规划开始前读取）
	Analyzed        []string         // 本次分析消费的 result：同事务落分析水位（metaAnalyzed）
	AddedObjectives []ObjectiveDraft // 新提出的方向
	AddedActions    []ActionDraft    // 常规规划新增的动作
	ClosedLeads     []LeadClosing    // 规划层关闭的线索
}

// ObjectiveDraft 是一个待落图的新方向。
type ObjectiveDraft struct {
	Node        Node     // 目标节点：kind=objective、state=open、source=planner、属本任务
	TriggeredBy []string // 催生它的 result（落 triggers 边）
}

// ActionDraft 是一个待落图的动作：挂在它所属的目标下（落 generates 边）。认领的线索随
// 动作落 pursues 边——线索由这个动作的结局结算，执行者据它知道自己接手的是哪条线索。
type ActionDraft struct {
	Node        Node     // 动作节点：kind=action、state=open、source=planner、属本任务
	ObjectiveID string   // 所属的方向（进行中的派生目标，可以是本次修订新提出的）；空或引用失效 = 根目标
	Pursues     []string // 认领的线索：必须是修订开始时待深挖的线索
	TriggeredBy []string // 催生它的 result（落 triggers 边）
}

// LeadClosing 是规划层关闭一条线索的决定：类别须是规划者可判定的（LeadClosureKind.Declared）。
type LeadClosing struct {
	LeadID string
	Kind   LeadClosureKind
	Reason string
}

// RevisionField 标识修订中的引用位置（与规划输出契约的字段同名）。
type RevisionField string

// 修订中的引用位置。
const (
	FieldTriggeredBy RevisionField = "triggered_by"
	FieldDirection   RevisionField = "direction"
	FieldPursues     RevisionField = "pursues"
	FieldLeads       RevisionField = "leads"
)

// RejectedReference 是修订中被剔除的无效引用。修订内容来自 LLM 提案：引用
// 失效（幻觉 ID）只剔除该引用并如实报告，不拖垮整次修订。
type RejectedReference struct {
	Field  RevisionField
	ID     string
	Reason string
}

// RevisionReport 是一次修订的落图结果。
type RevisionReport struct {
	AddedObjectives []string            // 新建的方向
	AddedActions    []string            // 新建的动作
	ClosedLeads     []string            // 被规划层关闭的线索
	Rejected        []RejectedReference // 被剔除的无效引用
}

// IsEmpty 判断修订是否没有任何内容。
func (r PlanRevision) IsEmpty() bool {
	return len(r.Analyzed) == 0 && len(r.AddedObjectives) == 0 &&
		len(r.AddedActions) == 0 && len(r.ClosedLeads) == 0
}

// RevisePlan 校验并原子应用一次规划修订。
//
// 校验在事务外基于当前图快照进行，剔除无效引用（见 RejectedReference）；随后
// 全部新节点、边与分析水位在一个事务内落定。规划所依据的目标已被改写时整体回滚
// 并返回 ErrGoalChanged——结论作废，调用方按新目标重新规划；若校验后被分析的结果
// 已被另一次分析消费，整体回滚并返回包裹 core.ErrGraphStateConflict 的错误——什么
// 都没落，调用方下一轮按新快照整体重做即可。缺少依据、草稿节点本身不合法（类型 /
// 来源 / 状态 / 归属 / 重复 ID）或水位引用了不存在的结果，属于调用方编程错误，直接
// 返回错误。
func (s *Store) RevisePlan(ctx context.Context, taskID string, rev PlanRevision) (RevisionReport, error) {
	if rev.IsEmpty() {
		return RevisionReport{}, nil
	}
	if rev.Basis.ObjectiveID == "" {
		return RevisionReport{}, fmt.Errorf("规划修订缺少依据（任务 %s）", taskID)
	}
	if err := validateDrafts(taskID, rev); err != nil {
		return RevisionReport{}, err
	}

	objectives, err := s.ListNodesByKind(ctx, taskID, core.KindObjective)
	if err != nil {
		return RevisionReport{}, fmt.Errorf("读取目标快照: %w", err)
	}
	results, err := s.nodesByID(ctx, taskID, core.KindResult)
	if err != nil {
		return RevisionReport{}, fmt.Errorf("读取结果快照: %w", err)
	}
	snapshot, err := s.readLeadSnapshot(ctx, taskID, rev)
	if err != nil {
		return RevisionReport{}, err
	}

	root, ok := RootObjective(objectives)
	if !ok || root.ID != rev.Basis.ObjectiveID {
		return RevisionReport{}, fmt.Errorf("修订依据 %s 不是任务 %s 的根目标", rev.Basis.ObjectiveID, taskID)
	}

	b := newRevisionBuilder(s, taskID, objectives, results, snapshot)
	b.changes.Preconditions = []core.GraphNodeVersion{{TaskID: taskID, NodeID: root.ID, Version: rev.Basis.Version}}
	if err := b.markAnalyzed(rev.Analyzed); err != nil {
		return RevisionReport{}, err
	}
	b.addObjectives(rev.AddedObjectives)
	// 关闭先于认领：同一条线索既被关闭又有动作认领时，认领落空（见 claim）。
	b.closeLeads(rev.ClosedLeads)
	if err := b.addActions(rev.AddedActions); err != nil {
		return RevisionReport{Rejected: b.report.Rejected}, err
	}

	if err := s.graphStore.ApplyChangeSet(ctx, b.changes); err != nil {
		if errors.Is(err, core.ErrVersionMismatch) {
			err = ErrGoalChanged
		}
		return RevisionReport{Rejected: b.report.Rejected}, fmt.Errorf("应用规划修订: %w", err)
	}
	return b.report, nil
}

// leadSnapshot 是修订开始时与线索有关的图快照：待深挖的线索（供 pursues 与 leads 校验）。
type leadSnapshot struct {
	leads map[string]Node
}

// readLeadSnapshot 读取线索快照。本次修订不碰线索时跳过加载——纯结果分析修订与不碰
// 线索的常规规划不为此多查。
func (s *Store) readLeadSnapshot(ctx context.Context, taskID string, rev PlanRevision) (leadSnapshot, error) {
	snap := leadSnapshot{}
	if len(rev.ClosedLeads) == 0 && !claimsLeads(rev.AddedActions) {
		return snap, nil
	}
	open, err := s.OpenLeads(ctx, taskID)
	if err != nil {
		return snap, err
	}
	snap.leads = make(map[string]Node, len(open))
	for _, lead := range open {
		snap.leads[lead.ID] = lead.Node
	}
	return snap, nil
}

// claimsLeads 判断本批新动作里有没有认领线索的。
func claimsLeads(drafts []ActionDraft) bool {
	return slices.ContainsFunc(drafts, func(d ActionDraft) bool { return len(d.Pursues) > 0 })
}

// revisionBuilder 基于图快照把一次修订翻译为变更集，并记录落图结果与被剔除的引用。
type revisionBuilder struct {
	store      *Store
	taskID     string
	objectives []Node
	objective  map[string]Node
	results    map[string]Node
	leads      map[string]Node // 修订开始时待深挖的线索
	now        time.Time

	closed  map[string]bool // 本次关闭的线索
	pursued map[string]bool // 本次修订里已被认领的线索

	changes core.GraphChangeSet
	report  RevisionReport
}

func newRevisionBuilder(s *Store, taskID string, objectives []Node, results map[string]Node, snap leadSnapshot) *revisionBuilder {
	byID := make(map[string]Node, len(objectives))
	for _, n := range objectives {
		byID[n.ID] = n
	}
	return &revisionBuilder{
		store: s, taskID: taskID, objectives: objectives, objective: byID, results: results,
		leads:   snap.leads,
		now:     time.Now(),
		closed:  map[string]bool{},
		pursued: map[string]bool{},
	}
}

// markAnalyzed 落分析水位：被消费的 result 来自调用方自己读到的快照，不存在即
// 编程错误。水位是一次性批注——两次分析并发消费同一结果（如僵尸回收重投导致
// 新旧 run 短暂重叠）时只有一次修订能提交，另一次整体回滚，结论不会落两遍。
func (b *revisionBuilder) markAnalyzed(ids []string) error {
	seen := make(map[string]bool, len(ids))
	for _, id := range ids {
		if seen[id] {
			continue
		}
		seen[id] = true
		if _, ok := b.results[id]; !ok {
			return fmt.Errorf("分析水位引用了不属于任务 %s 的结果 %s", b.taskID, id)
		}
		b.changes.Annotations = append(b.changes.Annotations, core.GraphNodeAnnotation{
			TaskID: b.taskID, NodeID: id, Metadata: map[string]interface{}{metaAnalyzed: true}, Once: true,
		})
	}
	return nil
}

// addObjectives 新方向 + 溯源边。新方向随即登记进索引：修订是原子的，同一次修订里的动作
// 可以声明挂在它下面。
func (b *revisionBuilder) addObjectives(drafts []ObjectiveDraft) {
	for _, d := range drafts {
		b.changes.Nodes = append(b.changes.Nodes, b.store.nodeToGraphNode(d.Node))
		b.objective[d.Node.ID] = d.Node
		b.report.AddedObjectives = append(b.report.AddedObjectives, d.Node.ID)
		for _, resultID := range b.validResults(FieldTriggeredBy, d.TriggeredBy) {
			b.edge(resultID, RelTriggers, d.Node.ID)
		}
	}
}

// addActions 新动作：挂在所属的目标下 + 认领的线索 + 溯源边。
func (b *revisionBuilder) addActions(drafts []ActionDraft) error {
	if len(drafts) == 0 {
		return nil
	}
	root, ok := RootObjective(b.objectives)
	if !ok {
		return fmt.Errorf("任务 %s 没有根目标，动作无处归属", b.taskID)
	}
	for _, d := range drafts {
		b.changes.Nodes = append(b.changes.Nodes, b.store.nodeToGraphNode(d.Node))
		b.report.AddedActions = append(b.report.AddedActions, d.Node.ID)
		b.edge(b.owner(d.ObjectiveID, root.ID), RelGenerates, d.Node.ID)
		for _, leadID := range d.Pursues {
			b.claim(d.Node.ID, leadID)
		}
		for _, resultID := range b.validResults(FieldTriggeredBy, d.TriggeredBy) {
			b.edge(resultID, RelTriggers, d.Node.ID)
		}
	}
	return nil
}

// owner 判定动作挂在哪个目标下：声明的方向仍进行中时取它，否则取根目标。引用失效
// （不是本任务的方向）即剔除并报告，动作照常排出——只是来路上记不到它所属的方向，
// 不因此作废这一步。
func (b *revisionBuilder) owner(directionID, rootID string) string {
	node, ok := b.objective[directionID]
	switch {
	case directionID == "":
	case !ok:
		b.reject(FieldDirection, directionID, "不是本任务的目标")
	case !node.IsDerivedObjective():
		b.reject(FieldDirection, directionID, "不是派生目标")
	case node.State == nil || *node.State != StateOpen:
		b.reject(FieldDirection, directionID, fmt.Sprintf("方向已结束（%s）", stateLabel(node.State)))
	default:
		return directionID
	}
	return rootID
}

// claim 落下动作对一条线索的认领（pursues 边）。引用失效（不是修订开始时待深挖的线索、
// 同一修订里已被关闭或已有动作认领）即剔除并报告，不拖垮修订。
func (b *revisionBuilder) claim(actionID, leadID string) {
	switch {
	case b.closed[leadID]:
		b.reject(FieldPursues, leadID, "同一修订已关闭这条线索")
		return
	case b.pursued[leadID]:
		b.reject(FieldPursues, leadID, "同一修订里已有动作认领这条线索")
		return
	}
	if _, ok := b.leads[leadID]; !ok {
		b.reject(FieldPursues, leadID, "不是本任务待深挖的线索")
		return
	}
	b.edge(actionID, RelPursues, leadID)
	b.pursued[leadID] = true
}

// closeLeads 关闭线索：类别与理由批注在线索上，它从此退出待跟进清单。引用的不是修订
// 开始时待深挖的线索、类别不是规划者可判定的、或没给理由的，剔除该条并如实报告，
// 不拖垮修订。批注是一次性的：并发的两次修订关闭同一条线索时只有一次能提交。
func (b *revisionBuilder) closeLeads(closings []LeadClosing) {
	for _, c := range closings {
		if b.closed[c.LeadID] {
			continue
		}
		if _, ok := b.leads[c.LeadID]; !ok {
			b.reject(FieldLeads, c.LeadID, "不是本任务待深挖的线索")
			continue
		}
		if !c.Kind.Declared() {
			b.reject(FieldLeads, c.LeadID, "不是规划者可判定的关闭类别")
			continue
		}
		if strings.TrimSpace(c.Reason) == "" {
			b.reject(FieldLeads, c.LeadID, "缺少关闭的理由")
			continue
		}
		b.closed[c.LeadID] = true
		b.changes.Annotations = append(b.changes.Annotations, core.GraphNodeAnnotation{
			TaskID: b.taskID, NodeID: c.LeadID, Once: true,
			Metadata: map[string]interface{}{metaLeadClosed: map[string]interface{}{
				"kind": string(c.Kind), "reason": strings.TrimSpace(c.Reason),
			}},
		})
		b.report.ClosedLeads = append(b.report.ClosedLeads, c.LeadID)
	}
}

// validResults 过滤出本任务真实存在的 result（去重），其余记为被剔除的引用。
func (b *revisionBuilder) validResults(field RevisionField, ids []string) []string {
	kept := make([]string, 0, len(ids))
	seen := make(map[string]bool, len(ids))
	for _, id := range ids {
		if seen[id] {
			continue
		}
		seen[id] = true
		if _, ok := b.results[id]; !ok {
			b.reject(field, id, "不是本任务的结果")
			continue
		}
		kept = append(kept, id)
	}
	return kept
}

func (b *revisionBuilder) reject(field RevisionField, id, reason string) {
	b.report.Rejected = append(b.report.Rejected, RejectedReference{Field: field, ID: id, Reason: reason})
}

func (b *revisionBuilder) edge(src string, rel Relation, dst string) {
	b.changes.Edges = append(b.changes.Edges, b.store.edgeToGraphEdge(&Edge{
		TaskID: b.taskID, SrcID: src, Rel: rel, DstID: dst, CreatedAt: b.now,
	}))
}

// validateDrafts 校验草稿节点的结构合法性（调用方构造，不合法即编程错误）。
func validateDrafts(taskID string, rev PlanRevision) error {
	ids := make(map[string]bool, len(rev.AddedObjectives)+len(rev.AddedActions))
	check := func(n Node, kind core.NodeKind) error {
		switch {
		case n.ID == "":
			return fmt.Errorf("新 %s 缺少 ID", kind)
		case ids[n.ID]:
			return fmt.Errorf("新节点 ID 重复: %s", n.ID)
		case n.TaskID != taskID:
			return fmt.Errorf("新 %s %s 不属于任务 %s", kind, n.ID, taskID)
		case n.Kind != kind || n.SourceType != SourcePlanner:
			return fmt.Errorf("新节点 %s 必须是 planner 创建的 %s", n.ID, kind)
		case n.State == nil || *n.State != StateOpen:
			return fmt.Errorf("新 %s %s 初始状态必须为 open", kind, n.ID)
		}
		ids[n.ID] = true
		return nil
	}
	for _, d := range rev.AddedObjectives {
		if err := check(d.Node, core.KindObjective); err != nil {
			return err
		}
	}
	for _, d := range rev.AddedActions {
		if err := check(d.Node, core.KindAction); err != nil {
			return err
		}
	}
	return nil
}

// nodesByID 读取任务内某类节点并按 ID 建索引。
func (s *Store) nodesByID(ctx context.Context, taskID string, kind core.NodeKind) (map[string]Node, error) {
	nodes, err := s.ListNodesByKind(ctx, taskID, kind)
	if err != nil {
		return nil, err
	}
	index := make(map[string]Node, len(nodes))
	for _, n := range nodes {
		index[n.ID] = n
	}
	return index, nil
}

func stateLabel(state *State) string {
	if state == nil {
		return "无状态"
	}
	return string(*state)
}
