package explorationgraph

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/V3teran/liusha/internal/framework/core"
)

// PlanRevision 是规划层对探索图的一次修订：结果分析提出新方向、宣告方向达成、
// 让新方向吸收旧方向，并把被分析的结果标记为已分析；常规规划新增动作、安排线索的
// 跟进与关闭。一次修订是同一个规划决策的不同侧面，整体原子落图——不会出现「新目标
// 已建、被它吸收的旧目标仍 open」「旧目标已 superseded、却查不到吸收者」，
// 也不会出现决策只落了一半、或结论已落而结果仍待分析，从而在重试或下一个 run
// 里被重复写入。
//
// 目标修订只作用于派生目标（IsDerivedObjective）。根目标代表任务本身，只随
// 任务终态由控制面收敛；动作挂在根目标下。
//
// 修订以 Basis（规划所依据的目标）为前置：规划期间目标被改写，修订整体作废
// （ErrGoalChanged），不会把按旧目标得出的结论写进新目标的图。
type PlanRevision struct {
	Basis               GoalBasis             // 规划所依据的目标（必填，规划开始前读取）
	Analyzed            []string              // 本次分析消费的 result：同事务落分析水位（metaAnalyzed）
	AddedObjectives     []ObjectiveDraft      // 新提出的派生目标
	CompletedObjectives []ObjectiveCompletion // 宣告达成的派生目标
	AddedActions        []ActionDraft         // 常规规划新增的动作
	Pursuits            []Pursuit             // 线索的跟进：新动作认领，或在办动作挂接
	ClosedLeads         []LeadClosing         // 规划层关闭的线索
}

// ObjectiveDraft 是一个待落图的新派生目标。
type ObjectiveDraft struct {
	Node        Node     // 目标节点：kind=objective、state=open、source=planner、属本任务
	TriggeredBy []string // 催生它的 result（落 triggers 边）
	Supersedes  []string // 被它吸收的既有派生目标（落 supersedes 边，旧目标转 superseded）
}

// ObjectiveCompletion 宣告一个派生目标已达成。
type ObjectiveCompletion struct {
	ObjectiveID string
	Evidence    []string // 作证的 result，至少一条有效才成立
}

// ActionDraft 是一个待落图的动作（归属根目标，落 generates 边）。
type ActionDraft struct {
	Node        Node     // 动作节点：kind=action、state=open、source=planner、属本任务
	TriggeredBy []string // 催生它的 result（落 triggers 边）
	// Serves 是本动作推进的方向（进行中的派生目标 ID，可空），落一条 serves 边
	// （action → 方向）。动作只有一个归属：它认领线索（见 Pursuit）时挂在线索下，Serves
	// 改为写在线索上——规划者据此改写这条线索的方向归属，一条线索只改一次。
	// 引用的不是可修订的派生目标时剔除该引用（不拖垮修订）。
	Serves string
}

// Pursuit 是一条跟进关系：动作的结局结算这条线索。动作为跟进它而排出（同在本次修订里
// 新增）落 pursues 边，执行者据它知道自己接手的是哪条线索；图上在办的动作落 attaches
// 边——线索出现得比动作晚，把在办动作挂到它上只记账，不改这个动作的范围。只能挂到
// 在办（open / running）的动作上：想说明之前已完成的动作已经覆盖了它，走关闭。
type Pursuit struct {
	ActionID string
	LeadID   string
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
	FieldCompleted   RevisionField = "completed_objectives"
	FieldEvidence    RevisionField = "evidence_ids"
	FieldTriggeredBy RevisionField = "triggered_by"
	FieldSupersedes  RevisionField = "supersedes"
	FieldServes      RevisionField = "serves"
	FieldPursues     RevisionField = "pursues"
	FieldLeads       RevisionField = "leads"
)

// RejectedReference 是修订中被剔除的无效引用。修订内容来自 LLM 提案：引用
// 失效（幻觉 ID、已收敛目标、根目标）只剔除该引用并如实报告，不拖垮整次修订。
type RejectedReference struct {
	Field  RevisionField
	ID     string
	Reason string
}

// RevisionReport 是一次修订的落图结果。
type RevisionReport struct {
	AddedObjectives      []string            // 新建的派生目标
	AddedActions         []string            // 新建的动作
	AttachedLeads        []string            // 挂到在办动作上的线索
	ClosedLeads          []string            // 被规划层关闭的线索
	CompletedObjectives  []string            // 转 completed 的派生目标
	SupersededObjectives []string            // 转 superseded 的派生目标
	Rejected             []RejectedReference // 被剔除的无效引用
}

// IsEmpty 判断修订是否没有任何内容。
func (r PlanRevision) IsEmpty() bool {
	return len(r.Analyzed) == 0 && len(r.AddedObjectives) == 0 &&
		len(r.CompletedObjectives) == 0 && len(r.AddedActions) == 0 &&
		len(r.Pursuits) == 0 && len(r.ClosedLeads) == 0
}

// RevisePlan 校验并原子应用一次规划修订。
//
// 校验在事务外基于当前图快照进行，剔除无效引用（见 RejectedReference）；随后
// 全部新节点、边、状态迁移与分析水位在一个事务内落定。规划所依据的目标已被改写
// 时整体回滚并返回 ErrGoalChanged——结论作废，调用方按新目标重新规划；若校验后
// 被引用目标已被并发流转，或被分析的结果已被另一次分析消费，整体回滚并返回包裹
// core.ErrGraphStateConflict 的错误——什么都没落，调用方下一轮按新快照整体重做即可。
// 缺少依据、草稿节点本身不合法（类型/来源/状态/归属/重复 ID）或水位引用了不存在
// 的结果，属于调用方编程错误，直接返回错误。
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
	b.complete(rev.CompletedObjectives)
	b.addObjectives(rev.AddedObjectives)
	// 关闭先于跟进：同一条线索既被关闭又有动作跟进时，跟进落空（见 pursue）。
	b.closeLeads(rev.ClosedLeads)
	if err := b.addActions(rev.AddedActions); err != nil {
		return RevisionReport{Rejected: b.report.Rejected}, err
	}
	for _, pursuit := range rev.Pursuits {
		b.pursue(pursuit)
	}
	b.settleDirections(rev.AddedActions)

	if err := s.graphStore.ApplyChangeSet(ctx, b.changes); err != nil {
		if errors.Is(err, core.ErrVersionMismatch) {
			err = ErrGoalChanged
		}
		return RevisionReport{Rejected: b.report.Rejected}, fmt.Errorf("应用规划修订: %w", err)
	}
	return b.report, nil
}

// leadSnapshot 是修订开始时与线索有关的图快照：待深挖的线索、可被挂接的在办动作，
// 以及已经改写过方向归属的线索。
type leadSnapshot struct {
	leads      map[string]Node // 待深挖的线索（供 pursues 与 leads 校验）
	inFlight   map[string]Node // 在办（open / running）的动作（供挂接校验）
	redirected map[string]bool // 已改写过方向归属的线索
}

// readLeadSnapshot 读取线索快照。本次修订不碰线索时跳过加载——纯结果分析修订与不碰
// 线索的常规规划不为此多查；在办动作与既有归属只在修订确实用到时读。
func (s *Store) readLeadSnapshot(ctx context.Context, taskID string, rev PlanRevision) (leadSnapshot, error) {
	snap := leadSnapshot{}
	if len(rev.ClosedLeads) == 0 && len(rev.Pursuits) == 0 {
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
	newActions := make(map[string]string, len(rev.AddedActions))
	for _, d := range rev.AddedActions {
		newActions[d.Node.ID] = d.Serves
	}
	needInFlight, needRedirected := false, false
	for _, p := range rev.Pursuits {
		serves, isNew := newActions[p.ActionID]
		if !isNew {
			needInFlight = true
		}
		if isNew && serves != "" {
			needRedirected = true
		}
	}
	if needInFlight {
		actions, err := s.ListNodesByKind(ctx, taskID, core.KindAction)
		if err != nil {
			return snap, fmt.Errorf("读取动作快照: %w", err)
		}
		snap.inFlight = make(map[string]Node, len(actions))
		for _, a := range actions {
			if a.State != nil && (*a.State == StateOpen || *a.State == StateRunning) {
				snap.inFlight[a.ID] = a
			}
		}
	}
	if needRedirected {
		edges, err := s.graphStore.ListEdges(ctx, core.GraphEdgeQuery{TaskID: taskID, Relation: string(RelServes)})
		if err != nil {
			return snap, fmt.Errorf("读取 serves 边: %w", err)
		}
		snap.redirected = make(map[string]bool)
		for _, e := range edges {
			if _, isLead := snap.leads[e.From]; isLead {
				snap.redirected[e.From] = true
			}
		}
	}
	return snap, nil
}

// revisionBuilder 基于图快照把一次修订翻译为变更集，并记录落图结果与被剔除的引用。
type revisionBuilder struct {
	store      *Store
	taskID     string
	objectives []Node
	objective  map[string]Node
	results    map[string]Node
	leads      map[string]Node // 修订开始时待深挖的线索
	inFlight   map[string]Node // 修订开始时在办的动作
	redirected map[string]bool // 已改写过方向归属的线索
	now        time.Time

	newActions map[string]string // 本次修订的新动作 → 它声明的方向（可空）
	completed  map[string]bool   // 本次宣告达成的目标
	superseded map[string]bool   // 本次被吸收的目标
	closed     map[string]bool   // 本次关闭的线索
	pursued    map[string]bool   // 本次修订里已被跟进的线索
	following  map[string]bool   // 本次修订里认领了线索的新动作

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
		leads: snap.leads, inFlight: snap.inFlight, redirected: snap.redirected,
		now:        time.Now(),
		newActions: make(map[string]string),
		completed:  map[string]bool{},
		superseded: map[string]bool{},
		closed:     map[string]bool{},
		pursued:    map[string]bool{},
		following:  map[string]bool{},
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

// complete 宣告达成：目标可修订且至少一条有效证据。
func (b *revisionBuilder) complete(completions []ObjectiveCompletion) {
	for _, c := range completions {
		if b.completed[c.ObjectiveID] {
			continue
		}
		if ok, reason := b.revisable(c.ObjectiveID); !ok {
			b.reject(FieldCompleted, c.ObjectiveID, reason)
			continue
		}
		if len(b.validResults(FieldEvidence, c.Evidence)) == 0 {
			b.reject(FieldCompleted, c.ObjectiveID, "缺少有效的结果证据")
			continue
		}
		b.completed[c.ObjectiveID] = true
		b.transition(c.ObjectiveID, StateCompleted)
		b.report.CompletedObjectives = append(b.report.CompletedObjectives, c.ObjectiveID)
	}
}

// addObjectives 新目标 + 溯源边 + 吸收关系。多个新目标可共同吸收同一旧目标
// （拆分）：旧目标只迁移一次，每个吸收者各落一条边。
func (b *revisionBuilder) addObjectives(drafts []ObjectiveDraft) {
	for _, d := range drafts {
		b.changes.Nodes = append(b.changes.Nodes, b.store.nodeToGraphNode(d.Node))
		b.report.AddedObjectives = append(b.report.AddedObjectives, d.Node.ID)
		for _, resultID := range b.validResults(FieldTriggeredBy, d.TriggeredBy) {
			b.edge(resultID, RelTriggers, d.Node.ID)
		}
		absorbed := make(map[string]bool, len(d.Supersedes))
		for _, oldID := range d.Supersedes {
			if !absorbed[oldID] && b.absorb(oldID) {
				absorbed[oldID] = true
				b.edge(d.Node.ID, RelSupersedes, oldID)
			}
		}
	}
}

// absorb 判定旧目标能否被吸收；首次吸收时登记迁移。返回 false 表示引用被剔除。
func (b *revisionBuilder) absorb(oldID string) bool {
	if b.superseded[oldID] {
		return true
	}
	if b.completed[oldID] {
		b.reject(FieldSupersedes, oldID, "同一修订已宣告该目标达成")
		return false
	}
	if ok, reason := b.revisable(oldID); !ok {
		b.reject(FieldSupersedes, oldID, reason)
		return false
	}
	b.superseded[oldID] = true
	b.transition(oldID, StateSuperseded)
	b.report.SupersededObjectives = append(b.report.SupersededObjectives, oldID)
	return true
}

// addActions 新动作：归属根目标 + 溯源边。动作声明的方向先记下，认领了线索的动作由
// pursue 把它写在线索上，其余的由 settleDirections 写在动作上。
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
		b.edge(root.ID, RelGenerates, d.Node.ID)
		for _, resultID := range b.validResults(FieldTriggeredBy, d.TriggeredBy) {
			b.edge(resultID, RelTriggers, d.Node.ID)
		}
		b.newActions[d.Node.ID] = d.Serves
	}
	return nil
}

// settleDirections 给没有认领线索的新动作落方向归属（serves 边，action → 方向）。
func (b *revisionBuilder) settleDirections(drafts []ActionDraft) {
	for _, d := range drafts {
		if d.Serves != "" && !b.following[d.Node.ID] {
			b.serve(d.Node.ID, d.Serves)
		}
	}
}

// serve 为节点落一条 serves 边（node → 方向）。引用的不是可修订的派生目标（幻觉 ID、
// 已收敛的方向、根目标）即剔除并如实报告，不拖垮修订——与 result 引用的剔除同口径。
func (b *revisionBuilder) serve(nodeID, directionID string) {
	if ok, reason := b.revisable(directionID); !ok {
		b.reject(FieldServes, directionID, reason)
		return
	}
	b.edge(nodeID, RelServes, directionID)
}

// pursue 落一条跟进关系。新动作认领线索落 pursues 边；图上在办的动作落 attaches 边
// （只记账）。引用失效（不是本任务待深挖的线索、同一修订里已被关闭或已有动作跟进、
// 挂接的对象不是在办动作）即剔除并报告，不拖垮修订。
func (b *revisionBuilder) pursue(p Pursuit) {
	switch {
	case b.closed[p.LeadID]:
		b.reject(FieldPursues, p.LeadID, "同一修订已关闭这条线索")
		return
	case b.pursued[p.LeadID]:
		b.reject(FieldPursues, p.LeadID, "同一修订里已有动作跟进这条线索")
		return
	}
	if _, ok := b.leads[p.LeadID]; !ok {
		b.reject(FieldPursues, p.LeadID, "不是本任务待深挖的线索")
		return
	}
	serves, isNew := b.newActions[p.ActionID]
	if isNew {
		b.following[p.ActionID] = true
		b.edge(p.ActionID, RelPursues, p.LeadID)
	} else {
		if _, ok := b.inFlight[p.ActionID]; !ok {
			b.reject(FieldPursues, p.ActionID, "只能挂到本任务在办的动作上")
			return
		}
		b.edge(p.ActionID, RelAttaches, p.LeadID)
		b.report.AttachedLeads = append(b.report.AttachedLeads, p.LeadID)
	}
	b.pursued[p.LeadID] = true
	// 认领线索的动作挂在线索下：它声明的方向是这条线索的方向归属。
	if serves != "" {
		b.redirect(p.LeadID, serves)
	}
}

// redirect 把一条线索的方向归属改写到另一个方向（serves 边，线索 → 方向）。一条线索
// 只改一次：此前改写过（图上已有，或同一修订里刚改过）即剔除并报告——归属来回改，
// 按方向分组就失去了意义。
func (b *revisionBuilder) redirect(leadID, directionID string) {
	if b.redirected[leadID] {
		b.reject(FieldServes, leadID, "这条线索的方向归属已改过")
		return
	}
	if ok, reason := b.revisable(directionID); !ok {
		b.reject(FieldServes, directionID, reason)
		return
	}
	b.redirected[leadID] = true
	b.edge(leadID, RelServes, directionID)
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

// revisable 判定一个既有目标能否被本次修订改写，不能时给出原因。
func (b *revisionBuilder) revisable(id string) (bool, string) {
	node, ok := b.objective[id]
	switch {
	case !ok:
		return false, "不是本任务的目标"
	case !node.IsDerivedObjective():
		return false, "根目标只随任务终态收敛"
	case node.State == nil || *node.State != StateOpen:
		return false, fmt.Sprintf("目标已收敛（%s）", stateLabel(node.State))
	}
	return true, ""
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

func (b *revisionBuilder) transition(id string, to State) {
	b.changes.Transitions = append(b.changes.Transitions, core.GraphStateTransition{
		TaskID: b.taskID, NodeID: id, From: string(StateOpen), To: string(to),
	})
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
