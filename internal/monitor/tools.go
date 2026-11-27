package monitor

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/V3teran/liusha/internal/constants"
	"github.com/V3teran/liusha/internal/explorationgraph"
	"github.com/V3teran/liusha/internal/framework/core"
	"github.com/V3teran/liusha/internal/monitor/metrics"
	"github.com/V3teran/liusha/internal/registry"
)

// ============================================
// GetGlobalStateTool - 获取任务全局状态
// ============================================

// GetGlobalStateTool 把探索图全局态势（根目标、动作、结果、假设、在途动作、速率）喂给 LLM。
type GetGlobalStateTool struct {
	registry.BaseTool
	graph            *explorationgraph.Store
	taskID           string
	metricsCollector *metrics.Collector
}

// NewGetGlobalStateTool 构造全局态势工具。
func NewGetGlobalStateTool(graph *explorationgraph.Store, taskID string, metricsCollector *metrics.Collector) *GetGlobalStateTool {
	t := &GetGlobalStateTool{
		graph:            graph,
		taskID:           taskID,
		metricsCollector: metricsCollector,
	}
	t.SetTimeout(constants.ToolTimeoutLong)
	return t
}

// Name 实现工具接口。
func (t *GetGlobalStateTool) Name() string {
	return "task_survey"
}

// ShortDesc 实现工具接口。
func (t *GetGlobalStateTool) ShortDesc() string {
	return "获取任务全局状态"
}

// Desc 实现工具接口。
func (t *GetGlobalStateTool) Desc() string {
	return "获取任务的全局态势：根目标、全部动作、已坐实的结果、全部假设（含验证状态与反驳原因）、在途动作与速率指标"
}

// Schema 实现工具接口。
func (t *GetGlobalStateTool) Schema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {},
		"required": []
	}`)
}

// Execute 实现工具接口：读探索图汇总全局态势。
func (t *GetGlobalStateTool) Execute(ctx context.Context, _ json.RawMessage) (registry.ToolResult, error) {
	// 读取所有 Actions
	allActions, err := t.graph.ListAllActions(ctx, t.taskID)
	if err != nil {
		return registry.ToolResult{Error: fmt.Sprintf("list actions: %v", err)}, nil
	}

	// 读取所有坐实发现（kind=result）
	findings, err := t.graph.ListResults(ctx, t.taskID)
	if err != nil {
		return registry.ToolResult{Error: fmt.Sprintf("list results: %v", err)}, nil
	}

	observations, err := t.graph.ListNodesByKind(ctx, t.taskID, core.KindObservation)
	if err != nil {
		return registry.ToolResult{Error: fmt.Sprintf("list observations: %v", err)}, nil
	}

	// 待深挖的线索由图结构推导（见 explorationgraph 的 lead.go）。线索堆积着没人追、
	// 追了总做不完，是可直接观测的功能障碍。
	openLeads, err := t.graph.OpenLeads(ctx, t.taskID)
	if err != nil {
		return registry.ToolResult{Error: fmt.Sprintf("list open leads: %v", err)}, nil
	}

	// 根目标（任务本身）
	root, ok, err := t.graph.RootObjectiveOf(ctx, t.taskID)
	if err != nil {
		return registry.ToolResult{Error: fmt.Sprintf("get objective: %v", err)}, nil
	}
	if !ok {
		return registry.ToolResult{Error: "任务还没有根目标"}, nil
	}

	// 本轮所见的图面：决策据它发出，重规划请求带上它（见 seenSurface）
	surface, err := t.graph.Surface(ctx, t.taskID, explorationgraph.GoalBasis{ObjectiveID: root.ID, Version: root.Version})
	if err != nil {
		return registry.ToolResult{Error: fmt.Sprintf("read surface: %v", err)}, nil
	}

	// 构建状态快照（在途动作的指令供判断重复与偏离；卡死由执行者看门狗处理）
	state := GlobalState{
		Objective:      ObjectiveView{ID: root.ID, Goal: root.Goal()},
		Actions:        allActions,
		Results:        findings,
		Hypotheses:     hypothesisViews(observations),
		OpenLeads:      openLeadViews(openLeads, time.Now()),
		RunningActions: t.runningActionViews(allActions, time.Now()),
		Metrics:        t.metricsCollector.Snapshot(),
	}

	// 序列化为 JSON
	stateJSON, err := json.Marshal(state)
	if err != nil {
		return registry.ToolResult{Error: fmt.Sprintf("marshal state: %v", err)}, nil
	}
	recordSeen(ctx, surface)

	return registry.ToolResult{Output: string(stateJSON)}, nil
}

// hypothesisViews 从观察中抽取假设的监察视图，按提交先后排列。
func hypothesisViews(observations []explorationgraph.Node) []HypothesisView {
	views := []HypothesisView{}
	for i := range observations {
		obs := &observations[i]
		if !obs.IsHypothesis() {
			continue
		}
		var c struct {
			Statement string `json:"statement"`
		}
		if err := json.Unmarshal(obs.Content, &c); err != nil {
			continue // content 由 MustJSON 写入，解析失败=节点损坏，不进视图
		}
		views = append(views, HypothesisView{
			ID:                 obs.ID,
			Statement:          c.Statement,
			VerificationStatus: *obs.VerificationStatus,
			RefuteReason:       obs.RefuteReason(),
			CreatedAt:          obs.CreatedAt,
		})
	}
	sort.SliceStable(views, func(i, j int) bool { return views[i].CreatedAt.Before(views[j].CreatedAt) })
	return views
}

// openLeadViews 把待深挖的线索（由 Store.OpenLeads 推导）映射成监察视图，保持入参的
// 创建先后序：显式算好挂了多久，不让决策者做时间戳算术。入参已是筛选后的线索，这里
// 不再判定处境。
func openLeadViews(leads []explorationgraph.Lead, now time.Time) []OpenLead {
	views := []OpenLead{}
	for i := range leads {
		statement := leads[i].Statement()
		if statement == "" {
			continue
		}
		mins := now.Sub(leads[i].CreatedAt).Minutes()
		if mins < 0 {
			mins = 0
		}
		views = append(views, OpenLead{
			ID: leads[i].ID, Statement: statement, AgeMinutes: math.Round(mins*10) / 10,
			Truncated: leads[i].Truncated, TruncationStreak: leads[i].TruncationStreak,
			Faults: leads[i].Faults, Progressed: leads[i].Progressed,
			Vetoes: leads[i].Vetoes, LastVetoReason: leads[i].LastVetoReason,
		})
	}
	return views
}

// runningActionViews 从动作列表抽取 running 态的监察视图。
// 时长基准取 UpdatedAt（最近一次状态迁移时刻：open→running 时刷新）。
func (t *GetGlobalStateTool) runningActionViews(actions []explorationgraph.Node, now time.Time) []RunningActionView {
	var views []RunningActionView
	for _, a := range actions {
		if a.State == nil || *a.State != explorationgraph.StateRunning {
			continue
		}
		var c struct {
			Instruction string `json:"instruction"`
		}
		if err := json.Unmarshal(a.Content, &c); err != nil {
			continue // content 由 MustJSON 写入，解析失败=节点损坏，不进运行中视图
		}
		mins := now.Sub(a.UpdatedAt).Minutes()
		if mins < 0 {
			mins = 0
		}
		view := RunningActionView{
			ID:             a.ID,
			Instruction:    c.Instruction,
			RunningMinutes: math.Round(mins*10) / 10,
		}
		views = append(views, view)
	}
	return views
}

// ============================================
// PublishDecisionTool - 发布监察决策
// ============================================

// Replanner 接收监察者的重规划请求（规划者实现）。seen 是监察者作出判断时所见的图面，
// 规划者处理时图面已有实质进展的请求作废。
type Replanner interface {
	RequestReplan(reason string, seen *explorationgraph.Surface)
}

// 监察决策类型：工具 schema 是决策名的唯一出处，章程只用文字描述何时干预。
const (
	// DecisionRejectAction 否决一个待执行或执行中的动作（落图 → rejected）。
	DecisionRejectAction = "reject_action"
	// DecisionRequestReplan 请求规划者带着理由重新规划（进程内直达，不落图）。
	DecisionRequestReplan = "request_replan"
)

// 决策针对的功能障碍（工具 schema 的 problem 取值，与章程的干预场景一一对应）。决策必须
// 指明其中一类：没有功能障碍的判断（「运转正常、无需干预」）表达不成一次决策。
const (
	ProblemDuplicate          = "duplicate"           // 与已完成或已失败的动作做同一件事
	ProblemDirectionRefuted   = "direction_refuted"   // 所在方向的主张已被判定不成立，或已被新结果覆盖
	ProblemOffGoal            = "off_goal"            // 对象不在任务目标范围内
	ProblemStalled            = "stalled"             // 动作不少却长时间没有新结果
	ProblemGoalSatisfied      = "goal_satisfied"      // 目标要求的成果已坐实，探索仍在继续
	ProblemDiminishingReturns = "diminishing_returns" // 动作大量累积而结果寥寥
	ProblemLeadsNeglected     = "leads_neglected"     // 线索挂着没人跟进，动作都去了新的对象面
)

// problemLabels 是问题类别交给规划者时的说法（写在否决原因与重规划理由的开头）。
var problemLabels = map[string]string{
	ProblemDuplicate:          "重复",
	ProblemDirectionRefuted:   "方向已作废",
	ProblemOffGoal:            "偏离目标",
	ProblemStalled:            "探索停滞",
	ProblemGoalSatisfied:      "目标已满足",
	ProblemDiminishingReturns: "收益递减",
	ProblemLeadsNeglected:     "线索被搁置",
}

// decisionProblems 是每种决策可针对的问题类别。
var decisionProblems = map[string][]string{
	DecisionRejectAction:  {ProblemDuplicate, ProblemDirectionRefuted, ProblemOffGoal},
	DecisionRequestReplan: {ProblemStalled, ProblemGoalSatisfied, ProblemDiminishingReturns, ProblemOffGoal, ProblemLeadsNeglected},
}

// PublishDecisionTool 是 monitor 的决策出口：否决落探索图状态，重规划请求直接交给
// 规划者（控制信号不走可丢的事件总线，也不是图上的事实）。
type PublishDecisionTool struct {
	registry.BaseTool
	graph     *explorationgraph.Store
	replanner Replanner
	taskID    string
}

// NewPublishDecisionTool 构造决策发布工具。
func NewPublishDecisionTool(graph *explorationgraph.Store, replanner Replanner, taskID string) *PublishDecisionTool {
	t := &PublishDecisionTool{
		graph:     graph,
		replanner: replanner,
		taskID:    taskID,
	}
	t.SetTimeout(constants.ToolTimeoutMedium)
	return t
}

// Name 实现工具接口。
func (t *PublishDecisionTool) Name() string {
	return "decision_publish"
}

// ShortDesc 实现工具接口。
func (t *PublishDecisionTool) ShortDesc() string {
	return "发布监察决策"
}

// Desc 实现工具接口。
func (t *PublishDecisionTool) Desc() string {
	return "发布监察决策：否决一个动作（reject_action），或请求规划者重新规划（request_replan）"
}

// Schema 实现工具接口。
func (t *PublishDecisionTool) Schema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"type": {
				"type": "string",
				"description": "reject_action：否决一个待执行或执行中的动作（执行中的立即中断，规划者不再提出等效动作；用户指派的动作不可否决）。request_replan：请求规划者按 reason 重新规划。没有功能障碍时不发决策。",
				"enum": ["reject_action", "request_replan"]
			},
			"problem": {
				"type": "string",
				"description": "决策针对的功能障碍。reject_action 可取 duplicate（与已完成或已失败的动作做同一件事；只否决这一步，它跟进的线索带着否决理由重新待深挖）、direction_refuted（所在方向的主张已被判定不成立，或已被新结果覆盖）、off_goal（对象不在任务目标范围内）——后两类评判的是这条线本身，被否决的动作所跟进的线索随之关闭；request_replan 可取 stalled（动作不少却长时间没有新结果）、goal_satisfied（目标要求的成果已坐实，探索仍在继续）、diminishing_returns（动作大量累积而结果寥寥）、off_goal（用户指派的动作偏离目标，只能在重规划里指出）、leads_neglected（线索挂着没人跟进，动作都去了新的对象面）",
				"enum": ["duplicate", "direction_refuted", "off_goal", "stalled", "goal_satisfied", "diminishing_returns", "leads_neglected"]
			},
			"evidence_ids": {
				"type": "array",
				"items": {"type": "string"},
				"description": "支撑判断的节点 ID（动作、假设、结果），至少一个，取自本轮 task_survey 的态势"
			},
			"action_id": {
				"type": "string",
				"description": "被否决动作的 ID（reject_action 必填）"
			},
			"reason": {
				"type": "string",
				"description": "决策理由：否决时写明为何这一步不该做（规划者据此避开等效方向），重规划时写明要修正什么"
			}
		},
		"required": ["type", "problem", "evidence_ids", "reason"]
	}`)
}

// Execute 实现工具接口：校验决策有据可依后，否决落探索图状态，重规划请求直达规划者。
func (t *PublishDecisionTool) Execute(ctx context.Context, args json.RawMessage) (registry.ToolResult, error) {
	var decision Decision
	if err := json.Unmarshal(args, &decision); err != nil {
		return registry.ToolResult{Error: fmt.Sprintf("invalid arguments: %v", err)}, nil
	}
	seen := lastSeen(ctx)
	if msg := t.validate(ctx, decision, seen); msg != "" {
		return registry.ToolResult{Error: msg}, nil
	}

	switch decision.Type {
	case DecisionRejectAction:
		if msg := t.rejectAction(ctx, decision); msg != "" {
			return registry.ToolResult{Error: msg}, nil
		}
	case DecisionRequestReplan:
		t.replanner.RequestReplan(decision.statement(), seen)
	}

	result := map[string]interface{}{
		"type":    decision.Type,
		"reason":  decision.Reason,
		"applied": true,
	}
	if decision.ActionID != "" {
		result["action_id"] = decision.ActionID
	}
	resultJSON, _ := json.Marshal(result)
	return registry.ToolResult{Output: string(resultJSON)}, nil
}

// rejectAction 落一次否决，返回给模型的拒绝说明（空 = 已生效）。用户指派的动作是人的
// 明确意图，监察者无权否决——只有用户自己能否决它。
func (t *PublishDecisionTool) rejectAction(ctx context.Context, decision Decision) string {
	node, err := t.graph.GetNode(ctx, decision.ActionID)
	if err != nil || node.TaskID != t.taskID || node.Kind != core.KindAction {
		return fmt.Sprintf("reject_action 拒绝：%s 不是本任务的动作", decision.ActionID)
	}
	if node.SourceType == explorationgraph.SourceUser {
		return fmt.Sprintf("reject_action 拒绝：动作 %s 是用户指派的，只有用户能否决", decision.ActionID)
	}
	ok, err := t.graph.RejectAction(ctx, t.taskID, decision.ActionID, explorationgraph.SourceMonitor,
		rejectionScope(decision.Problem), decision.statement())
	if err != nil {
		return fmt.Sprintf("reject_action 失败: %v", err)
	}
	if !ok {
		return fmt.Sprintf("reject_action 拒绝：动作 %s 已结束，只有待执行或执行中的动作可否决", decision.ActionID)
	}
	return ""
}

// rejectionScope 是一类问题所对应的否决范围：偏离目标与方向作废评判的是这条线本身，
// 被否决的动作所跟进的线索随之关闭；重复只评判这一步，线索带着否决理由重新待深挖。
func rejectionScope(problem string) explorationgraph.RejectScope {
	switch problem {
	case ProblemOffGoal, ProblemDirectionRefuted:
		return explorationgraph.RejectLead
	default:
		return explorationgraph.RejectStep
	}
}

// validate 校验决策有据可依，返回给模型的拒绝说明（空 = 可以生效）：本轮看过全局态势、
// 指明了这种决策可针对的问题类别、引用了本任务里真实存在的证据节点。
func (t *PublishDecisionTool) validate(ctx context.Context, d Decision, seen *explorationgraph.Surface) string {
	problems, ok := decisionProblems[d.Type]
	if !ok {
		return "type must be reject_action or request_replan"
	}
	if d.Type == DecisionRejectAction && d.ActionID == "" {
		return "action_id is required for reject_action"
	}
	if seen == nil {
		return "发布决策前先调用 task_survey 查看本轮态势：决策要基于亲见的状态"
	}
	if !slices.Contains(problems, d.Problem) {
		return fmt.Sprintf("problem 必须是 %s 可针对的功能障碍之一：%s；没有功能障碍时不发决策", d.Type, strings.Join(problems, " / "))
	}
	if len(d.EvidenceIDs) == 0 {
		return "evidence_ids 至少引用一个支撑判断的节点 ID"
	}
	for _, id := range d.EvidenceIDs {
		node, err := t.graph.GetNode(ctx, id)
		if err != nil || node.TaskID != t.taskID {
			return fmt.Sprintf("evidence_ids 中的 %s 不是本任务的节点", id)
		}
	}
	return ""
}

// statement 是决策交给规划者的完整说法：问题类别、理由与证据。
func (d Decision) statement() string {
	return fmt.Sprintf("%s：%s（证据：%s）", problemLabels[d.Problem], d.Reason, strings.Join(d.EvidenceIDs, "、"))
}
