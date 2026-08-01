package attackgraph

import (
	"encoding/json"
	"strconv"

	"github.com/V3teran/liusha/internal/conversation"
)

// rootTaskID 是合成的根任务节点 id（主线/orchestrator 的容器）。
// 事件流里没有显式「开始」事件，故合成一个稳定 id 的根，orchestrator 自身的
// 探测/判断与所有子任务都挂在它下面。固定字面值不与 message UUID 冲突。
const rootTaskID = "task-root"

// knownOrchestratorAgent 是主代理的约定角色 id（镜像 einoagent 的 orchestrator role id）。
// 刻意不 import einoagent（避免把 eino 重依赖拖进纯投影包）——这是跨包共享的命名约定。
const knownOrchestratorAgent = "orchestrator"

// 事件 Kind 字面量（对齐 internal/einoagent.ScanEventKind）。
const (
	evReasoning  = "reasoning"
	evToolCall   = "tool_call"
	evToolResult = "tool_result"
	evSpawn      = "spawn"
	evInsight    = "insight" // mark_insight 工具：agent 自标关键判断/信号（§2 self-mark）
)

// 工具名。
const (
	toolWriteFinding = "write_finding"
)

// 语义节点短标签的最长字符数（原文不入节点，只留可读摘要，见设计 §6）。
const (
	hypothesisTitleMax = 120
	signalTitleMax     = 120
)

// insightHypothesis / insightSignal 是 mark_insight 的 type 取值（对齐 einotools.Insight*）。
const (
	insightHypothesis = "hypothesis"
	insightSignal     = "signal"
)

// insightArgs 解析 mark_insight 事件的 Args（{type,text,dead_end}）。真相源：einotools/insight.go。
type insightArgs struct {
	Type    string `json:"type"`
	Text    string `json:"text"`
	DeadEnd bool   `json:"dead_end"`
}

// traceEvent 是 message.Metadata 里 einoagent.ScanEvent 的最小可解析镜像。
// 真相源：internal/einoagent/scan_event.go（无 json tag，按字段名大小写不敏感匹配）。
type traceEvent struct {
	Kind       string
	ToolName   string
	CallID     string
	Args       string
	Text       string
	AgentName  string
	Err        string
	Result     string
	DurationMs int
	InTokens   int
	OutTokens  int
}

// skeleton 从会话事件流（按 seq 序入参）派生「确定性骨架」：
// 任务层级（task）+ 探测（probe）+ agent 自标的判断/信号（hypothesis/signal）。
//
// 这一趟零 LLM、永远能出图。未被 agent 自标的 reasoning 不在此成节点——
// 它们由第二趟 enrich（LLM 提炼）按需补成 hypothesis/signal。
//
// 返回：节点、backbone 边、findingParent（finding id → 证实它的节点 id，供成果链挂接）。
func skeleton(messages []conversation.Message) *builder {
	b := newBuilder(resolvePrimaryAgent(messages))

	for _, m := range messages {
		if m.Kind != conversation.KindEvent || len(m.Metadata) == 0 {
			continue
		}
		var ev traceEvent
		if err := json.Unmarshal(m.Metadata, &ev); err != nil {
			continue // 脏 metadata 跳过，不阻断整图
		}
		b.consume(m.ID, ev)
	}
	// 收尾：所有未闭合探测（扫描进行中 / 末尾无边界事件）此刻结算。
	for _, c := range b.cursors {
		b.closeProbe(c)
	}
	return b
}

// builder 承载骨架投影的可变状态（skeleton 每次调用新建，无包级状态）。
//
// 每个 agent 的调查链状态收敛在一个 agentCursor 里（而非若干平行 map）：探测挂在游标的
// attach 点（任务或最近判断）下，agent 自标的判断/信号（mark_insight，§2）实时插进链里，
// 边类型由 backboneEdgeType 从父子 kind 派生，自动织出「任务→判断→探测→信号→判断…」回环。
type builder struct {
	primary       string
	nodes         []Node
	idx           map[string]int          // node id → nodes 下标
	cursors       map[string]*agentCursor // agent → 它当前那条调查链的游标
	probeByCall   map[string]*probeAcc    // CallID → 归属探测（tool_result 回填状态用；跨 agent 唯一键）
	findingParent map[string]string       // finding id → 证实它的探测节点 id
	reasonByTask  map[string][]string     // task 节点 id → 该任务线下的 reasoning 文字（供 ① LLM 兜底提炼）
	taskHasHypo   map[string]bool         // task id → 该任务线是否已有「判断」（②优先：有则 ① 不再兜底）
}

// agentCursor 是单个 agent 一条调查链的游标：当前任务、探测挂载点、最近探测、待接信号，
// 以及正在累积的探测。spawn 子代理时整体重置为一个新游标（一次赋值，不再手删多个 map）。
type agentCursor struct {
	task    string    // 当前所属 task 节点 id
	attach  string    // 新探测的挂载点（task 根，或最近一个 hypothesis；空=回落 task）
	last    string    // 最近一个探测节点 id（signal 经 reveals 挂它下）
	pending string    // 待「引出下一判断」的信号 id（下一个 hypothesis 经 informs 挂它下）
	probe   *probeAcc // 正在累积的探测（nil=当前无未闭合探测）
}

// probeAcc 是一个探测节点的累积态：一段连续工具调用（同 agent、被 reasoning/insight/spawn 分隔）塌缩成一个 probe。
type probeAcc struct {
	nodeIdx    int      // 对应 builder.nodes 下标
	tools      []string // 有序去重的工具名（供标题）
	steps      int      // 工具调用次数
	openCalls  int      // 已发出但结果未回的调用数（收尾判 running）
	anyErr     bool     // 任一调用报错 → 探测判 failed（死路信号）
	durationMs int      // 各调用耗时累加
}

func newBuilder(primary string) *builder {
	return &builder{
		primary:       primary,
		idx:           map[string]int{},
		cursors:       map[string]*agentCursor{},
		probeByCall:   map[string]*probeAcc{},
		findingParent: map[string]string{},
		reasonByTask:  map[string][]string{},
		taskHasHypo:   map[string]bool{},
	}
}

// ensureRoot 惰性合成根任务（主线/orchestrator 容器），首个事件到达时才建。
// 无任何事件（纯 passive 无会话）时根任务不出现——图只剩成果链，不伪造调查主线。
func (b *builder) ensureRoot() {
	if _, ok := b.idx[rootTaskID]; ok {
		return
	}
	b.add(Node{ID: rootTaskID, Kind: KindTask, Title: "调查主线", Status: StatusRunning, Provenance: ProvDerived})
	if b.primary != "" {
		b.cursors[b.primary] = &agentCursor{task: rootTaskID}
	}
}

// add 入列节点并建立 id → 下标索引。
func (b *builder) add(n Node) {
	b.idx[n.ID] = len(b.nodes)
	b.nodes = append(b.nodes, n)
}

// cursor 返回 agent 的调查链游标，惰性建。未经 spawn 登记的 agent（子代理事件早于其 spawn
// 等异常）回落到主线根任务下，保证事件永远有处可挂、不丢。
func (b *builder) cursor(agent string) *agentCursor {
	c, ok := b.cursors[agent]
	if !ok {
		c = &agentCursor{task: rootTaskID}
		b.cursors[agent] = c
	}
	return c
}

// consume 把一条事件折叠进骨架。reasoning/insight 在此仅作探测边界（本身不成节点，
// 由第二趟 enrich 按需提炼成 hypothesis/signal），故骨架零 LLM、永远能出图。
func (b *builder) consume(msgID string, ev traceEvent) {
	b.ensureRoot() // 首个事件到达才合成根任务（无事件不伪造主线）
	agent := ev.AgentName
	if agent == "" {
		agent = b.primary
	}
	c := b.cursor(agent)
	switch ev.Kind {
	case evReasoning:
		b.closeProbe(c) // reasoning 仅作探测边界（未自标的推理不成节点）
		if ev.Text != "" {
			b.reasonByTask[c.task] = append(b.reasonByTask[c.task], ev.Text) // 攒着供 ① LLM 兜底提炼
		}
	case evInsight:
		b.closeProbe(c)
		b.handleInsight(msgID, agent, c, ev)
	case evSpawn:
		b.closeProbe(c)
		b.handleSpawn(msgID, c, ev)
	case evToolCall:
		b.handleToolCall(msgID, agent, c, ev)
	case evToolResult:
		b.handleToolResult(c, ev)
	}
}

// handleInsight 把 agent 自标（mark_insight §2）实时插进调查链，是执行图最可靠的语义节点：
//
//   - hypothesis：成为后续探测的新挂载点（attach）。若前面有待引出的信号，经 informs 挂它下
//     （信号→判断的调查回环）；否则经 pursues 直接挂当前任务下。
//   - signal：经 reveals 挂最近探测下（无探测则挂当前挂载点）；并记为 pendingSignal，
//     等下一个 hypothesis 来接（若无则收尾时经 reveals 留在探测下即可）。
func (b *builder) handleInsight(msgID, agent string, c *agentCursor, ev traceEvent) {
	var in insightArgs
	if err := json.Unmarshal([]byte(ev.Args), &in); err != nil || in.Text == "" {
		return // 脏 args 跳过，不阻断整图
	}
	switch in.Type {
	case insightHypothesis:
		parent := c.attach
		if c.pending != "" {
			parent = c.pending // 信号引出判断（informs 回环）
			c.pending = ""
		}
		if parent == "" {
			parent = c.task
		}
		b.add(Node{ID: msgID, Kind: KindHypothesis, ParentID: parent, Agent: agent, Title: firstLine(in.Text, hypothesisTitleMax), Status: StatusOpen, Provenance: ProvAgent, Ref: msgID})
		c.attach = msgID             // 后续探测挂到这个判断下
		b.taskHasHypo[c.task] = true // ②已覆盖此任务线，① 不再兜底
	case insightSignal:
		parent := c.last
		if parent == "" {
			parent = c.attachOrTask()
		}
		status := ""
		if in.DeadEnd {
			status = StatusRefuted // 死路信号：此判断被否定的证据
		}
		b.add(Node{ID: msgID, Kind: KindSignal, ParentID: parent, Agent: agent, Title: firstLine(in.Text, signalTitleMax), Status: status, Provenance: ProvAgent, Ref: msgID})
		c.pending = msgID
	}
}

// attachOrTask 返回游标当前挂载点（最近判断），无则回落当前任务根。
func (c *agentCursor) attachOrTask() string {
	if c.attach != "" {
		return c.attach
	}
	return c.task
}

// handleSpawn 建子任务节点，挂到派发它的 agent 当前任务下，并把子代理游标整体重置为新链。
func (b *builder) handleSpawn(msgID string, c *agentCursor, ev traceEvent) {
	st := subagentType(ev.Args)
	b.add(Node{ID: msgID, Kind: KindTask, ParentID: c.task, Agent: st, Title: spawnTitle(ev.Args), Status: StatusRunning, Provenance: ProvDerived, Ref: msgID})
	if st != "" {
		b.cursors[st] = &agentCursor{task: msgID} // 一次赋值重置整条链（不再手删多个 map）
	}
}

// handleToolCall 把调用并入 agent 当前探测；无则新建 probe 节点（挂当前挂载点：最近判断或任务根）。
func (b *builder) handleToolCall(msgID, agent string, c *agentCursor, ev traceEvent) {
	if c.probe == nil {
		b.add(Node{ID: msgID, Kind: KindProbe, ParentID: c.attachOrTask(), Agent: agent, Status: StatusRunning, Provenance: ProvDerived, Ref: msgID})
		c.probe = &probeAcc{nodeIdx: len(b.nodes) - 1}
		c.last = msgID // signal 经 reveals 挂它下
	}
	c.probe.steps++
	c.probe.openCalls++
	c.probe.tools = appendDistinct(c.probe.tools, ev.ToolName)
	if ev.CallID != "" {
		b.probeByCall[ev.CallID] = c.probe
	}
}

// handleToolResult 按 CallID 回填探测状态；write_finding 结果登记 findingParent（成果链挂接）。
func (b *builder) handleToolResult(c *agentCursor, ev traceEvent) {
	acc := b.probeByCall[ev.CallID]
	if acc == nil {
		acc = c.probe // CallID 缺失/交错时兜底挂当前探测
	}
	if acc != nil {
		acc.openCalls--
		acc.durationMs += ev.DurationMs
		if ev.Err != "" {
			acc.anyErr = true
		}
	}
	if ev.CallID != "" {
		delete(b.probeByCall, ev.CallID)
	}
	if ev.ToolName == toolWriteFinding && acc != nil {
		if fid := findingIDFromResult(ev.Result); fid != "" {
			b.findingParent[fid] = b.nodes[acc.nodeIdx].ID
		}
	}
}

// closeProbe 结算游标当前探测：定标题、耗时、终态（failed>running>done），并从游标摘除。
func (b *builder) closeProbe(c *agentCursor) {
	acc := c.probe
	if acc == nil {
		return
	}
	c.probe = nil
	n := &b.nodes[acc.nodeIdx]
	n.Title = probeTitle(acc.tools, acc.steps)
	n.DurationMs = acc.durationMs
	switch {
	case acc.anyErr:
		n.Status = StatusFailed
	case acc.openCalls > 0:
		n.Status = StatusRunning // 结果未回（仍在跑 / 被中断）
	default:
		n.Status = StatusDone
	}
}

// edges 从每个节点的 ParentID 派生 backbone 边，边类型由 (父kind, 子kind) 决定，读起来即故事线。
// 纯查询、无副作用：探测已在 skeleton 收尾结算，此处不再触碰游标。
func (b *builder) edges() []Edge {
	var edges []Edge
	for _, n := range b.nodes {
		if n.ParentID == "" {
			continue
		}
		pIdx, ok := b.idx[n.ParentID]
		if !ok {
			continue // 脏父指针跳过，不产孤儿边
		}
		edges = append(edges, Edge{From: n.ParentID, To: n.ID, Type: backboneEdgeType(b.nodes[pIdx].Kind, n.Kind)})
	}
	return edges
}

// backboneEdgeType 由父/子节点种类推出 backbone 边的语义类型（见设计 §5 故事线）。
// 骨架里 probe 直接挂 task（tests）；enrich 插入 hypothesis 后 probe 改挂 hypothesis，边类型不变。
func backboneEdgeType(parentKind, childKind string) string {
	switch childKind {
	case KindTask:
		return EdgeSpawns
	case KindProbe:
		return EdgeTests
	case KindSignal:
		return EdgeReveals
	case KindHypothesis:
		if parentKind == KindSignal {
			return EdgeInforms // 信号引出下一判断（调查回环）
		}
		return EdgePursues // 任务追一个判断
	case KindFinding:
		return EdgeConfirms
	}
	return EdgeTests
}

// resolvePrimaryAgent 选主线 agent：优先约定的 orchestrator，否则取首个出现的 AgentName。
func resolvePrimaryAgent(messages []conversation.Message) string {
	first := ""
	for _, m := range messages {
		if m.Kind != conversation.KindEvent || len(m.Metadata) == 0 {
			continue
		}
		var ev traceEvent
		if err := json.Unmarshal(m.Metadata, &ev); err != nil {
			continue
		}
		if ev.AgentName == knownOrchestratorAgent {
			return knownOrchestratorAgent
		}
		if first == "" && ev.AgentName != "" {
			first = ev.AgentName
		}
	}
	return first
}

// probeTitle 给探测取短标签：单工具单步用工具名，否则「首个工具 等 N 步」。
func probeTitle(tools []string, steps int) string {
	if len(tools) == 0 {
		return "探测"
	}
	if len(tools) == 1 && steps == 1 {
		return tools[0]
	}
	return tools[0] + " 等 " + strconv.Itoa(steps) + " 步"
}

// appendDistinct 追加非空且未出现过的字符串（保序去重）。
func appendDistinct(xs []string, x string) []string {
	if x == "" {
		return xs
	}
	for _, e := range xs {
		if e == x {
			return xs
		}
	}
	return append(xs, x)
}

// findingIDFromResult 从 write_finding 的 tool_result（{"id":"..."}）取 finding id；失败返空。
func findingIDFromResult(result string) string {
	var r struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(result), &r); err != nil {
		return ""
	}
	return r.ID
}

// subagentType 从 task 工具入参 {subagent_type} 取子代理类型；解析失败/缺字段返回空。
func subagentType(args string) string {
	var in struct {
		SubagentType string `json:"subagent_type"`
	}
	if err := json.Unmarshal([]byte(args), &in); err != nil {
		return ""
	}
	return in.SubagentType
}

// spawnTitle 拼派发短标签；缺类型兜底「派发子代理」（完整入参在 message，点开可见）。
func spawnTitle(args string) string {
	if st := subagentType(args); st != "" {
		return "派发 " + st
	}
	return "派发子代理"
}
