package explorationgraph

import (
	"context"
	"fmt"
	"time"

	"github.com/V3teran/liusha/internal/framework/core"
)

// PlanRevision 是规划层基于一次结果分析对探索图的修订：提出新方向、宣告方向
// 达成、让新方向吸收旧方向、在任务方向上追加延续动作，并把被分析的结果标记
// 为已分析。它们是同一个规划决策的不同侧面，整体原子落图——不会出现「新目标
// 已建、被它吸收的旧目标仍 open」「旧目标已 superseded、却查不到吸收者」，
// 也不会出现决策只落了一半、或结论已落而结果仍待分析，从而在重试或下一个 run
// 里被重复写入。
//
// 目标修订只作用于派生目标（IsDerivedObjective）。根目标代表任务本身，只随
// 任务终态由控制面收敛；延续动作挂在根目标下。
type PlanRevision struct {
	Analyzed            []string              // 本次分析消费的 result：同事务落分析水位（metaAnalyzed）
	AddedObjectives     []ObjectiveDraft      // 新提出的派生目标
	CompletedObjectives []ObjectiveCompletion // 宣告达成的派生目标
	AddedActions        []ActionDraft         // 任务方向上的延续动作
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

// ActionDraft 是一个待落图的延续动作（归属根目标，落 generates 边）。
type ActionDraft struct {
	Node        Node     // 动作节点：kind=action、state=open、source=planner、属本任务
	TriggeredBy []string // 催生它的 result（落 triggers 边）
}

// RevisionField 标识修订中的引用位置（与分析输出契约的字段同名）。
type RevisionField string

// 修订中的引用位置。
const (
	FieldCompleted   RevisionField = "completed_objectives"
	FieldEvidence    RevisionField = "evidence_ids"
	FieldTriggeredBy RevisionField = "triggered_by"
	FieldSupersedes  RevisionField = "supersedes"
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
	AddedActions         []string            // 新建的延续动作
	CompletedObjectives  []string            // 转 completed 的派生目标
	SupersededObjectives []string            // 转 superseded 的派生目标
	Rejected             []RejectedReference // 被剔除的无效引用
}

// IsEmpty 判断修订是否没有任何内容。
func (r PlanRevision) IsEmpty() bool {
	return len(r.Analyzed) == 0 && len(r.AddedObjectives) == 0 &&
		len(r.CompletedObjectives) == 0 && len(r.AddedActions) == 0
}

// RevisePlan 校验并原子应用一次规划修订。
//
// 校验在事务外基于当前图快照进行，剔除无效引用（见 RejectedReference）；随后
// 全部新节点、边、状态迁移与分析水位在一个事务内落定。若校验后被引用目标已被
// 并发流转（如任务终态收敛），或被分析的结果已被另一次分析消费，事务整体回滚
// 并返回包裹 core.ErrGraphStateConflict 的错误——什么都没落，调用方下一轮按新
// 快照整体重做即可。
// 草稿节点本身不合法（类型/来源/状态/归属/重复 ID）或水位引用了不存在的结果，
// 属于调用方编程错误，直接返回错误。
func (s *Store) RevisePlan(ctx context.Context, taskID string, rev PlanRevision) (RevisionReport, error) {
	if rev.IsEmpty() {
		return RevisionReport{}, nil
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

	b := newRevisionBuilder(s, taskID, objectives, results)
	if err := b.markAnalyzed(rev.Analyzed); err != nil {
		return RevisionReport{}, err
	}
	b.complete(rev.CompletedObjectives)
	b.addObjectives(rev.AddedObjectives)
	if err := b.addActions(rev.AddedActions); err != nil {
		return RevisionReport{Rejected: b.report.Rejected}, err
	}

	if err := s.graphStore.ApplyChangeSet(ctx, b.changes); err != nil {
		return RevisionReport{Rejected: b.report.Rejected}, fmt.Errorf("应用规划修订: %w", err)
	}
	return b.report, nil
}

// revisionBuilder 基于图快照把一次修订翻译为变更集，并记录落图结果与被剔除的引用。
type revisionBuilder struct {
	store      *Store
	taskID     string
	objectives []Node
	objective  map[string]Node
	results    map[string]Node
	now        time.Time

	completed  map[string]bool // 本次宣告达成的目标
	superseded map[string]bool // 本次被吸收的目标

	changes core.GraphChangeSet
	report  RevisionReport
}

func newRevisionBuilder(s *Store, taskID string, objectives []Node, results map[string]Node) *revisionBuilder {
	byID := make(map[string]Node, len(objectives))
	for _, n := range objectives {
		byID[n.ID] = n
	}
	return &revisionBuilder{
		store: s, taskID: taskID, objectives: objectives, objective: byID, results: results,
		now: time.Now(), completed: map[string]bool{}, superseded: map[string]bool{},
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

// addActions 延续动作：归属根目标 + 溯源边。
func (b *revisionBuilder) addActions(drafts []ActionDraft) error {
	if len(drafts) == 0 {
		return nil
	}
	root, ok := RootObjective(b.objectives)
	if !ok {
		return fmt.Errorf("任务 %s 没有根目标，延续动作无处归属", b.taskID)
	}
	for _, d := range drafts {
		b.changes.Nodes = append(b.changes.Nodes, b.store.nodeToGraphNode(d.Node))
		b.report.AddedActions = append(b.report.AddedActions, d.Node.ID)
		b.edge(root.ID, RelGenerates, d.Node.ID)
		for _, resultID := range b.validResults(FieldTriggeredBy, d.TriggeredBy) {
			b.edge(resultID, RelTriggers, d.Node.ID)
		}
	}
	return nil
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
