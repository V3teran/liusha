package executor

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/rs/zerolog"

	"github.com/V3teran/liusha/internal/explorationgraph"
	"github.com/V3teran/liusha/internal/framework/core"
	"github.com/V3teran/liusha/internal/framework/runtime"
)

// 观察归属在写入时精确建立：Engine.createObservationNode 建节点即落 generates 边
// （action → observation），不存在收割期的二次归属查询——并行执行 action 时各自
// 的观察天然只归各自的 action，不会串。repro 信封整体存进节点（复现门按 kind
// 分发）。
func TestObservationOwnership_ByGeneratesEdgeAtWriteTime(t *testing.T) {
	graph := explorationgraph.NewMemoryStore()
	ctx := context.Background()
	const taskID = "t-harvest"

	engine := NewEngine(EngineConfig{Graph: graph, Logger: zerolog.Nop()})

	mkAction := func(id string) {
		st := explorationgraph.StateOpen
		_, _ = graph.CreateNode(ctx, explorationgraph.Node{
			ID: id, TaskID: taskID, Kind: core.KindAction, State: &st,
			Content: json.RawMessage(`{"instruction":"x"}`),
		})
	}
	mkAction("act-A")
	mkAction("act-B")

	envelope := json.RawMessage(`{"kind":"steps","recipe":{"steps":"do"},"assert":{"description":"see x"}}`)

	// 各 action 走 Engine 写入路径：建观察节点 + 落归属边
	nodeA, err := engine.createObservationNode(ctx, taskID, "act-A",
		Observation{Statement: "A 的假设", Category: explorationgraph.CategoryHypothesis, Repro: envelope})
	if err != nil {
		t.Fatalf("创建 act-A 观察节点失败: %v", err)
	}

	nodeB, err := engine.createObservationNode(ctx, taskID, "act-B",
		Observation{Statement: "B 的假设", Category: explorationgraph.CategoryHypothesis, Repro: envelope})
	if err != nil {
		t.Fatalf("创建 act-B 观察节点失败: %v", err)
	}

	// 归属精确性：act-A 的 generates 边只指向自己的观察，act-B 同理
	edges, err := graph.ListEdgesByTask(ctx, taskID)
	if err != nil {
		t.Fatalf("查询边失败: %v", err)
	}
	owns := map[string][]string{} // action → 其 generates 指向的观察
	for _, e := range edges {
		if e.Rel == explorationgraph.RelGenerates {
			owns[e.SrcID] = append(owns[e.SrcID], e.DstID)
		}
	}
	if len(owns["act-A"]) != 1 || owns["act-A"][0] != nodeA.ID {
		t.Fatalf("act-A 应只归属自己的观察 %s, got %v", nodeA.ID, owns["act-A"])
	}
	if len(owns["act-B"]) != 1 || owns["act-B"][0] != nodeB.ID {
		t.Fatalf("act-B 应只归属自己的观察 %s, got %v", nodeB.ID, owns["act-B"])
	}

	// 信封整体存进节点，kind 可供复现门分发
	var c struct {
		Repro struct {
			Kind string `json:"kind"`
		} `json:"repro"`
	}
	if json.Unmarshal(nodeA.Content, &c) != nil || c.Repro.Kind != "steps" {
		t.Fatalf("节点应携带信封（kind=steps）, got %s", nodeA.Content)
	}
}

// 配方在落图时归一化一次，节点存的就是矫正后的信封——评估者只从节点读配方，
// 不再有第二条携带原始破碎形状（数组信封/标量断言）的消费路径。
func TestProcessOutput_ReproNormalizedOnNode(t *testing.T) {
	graph := explorationgraph.NewMemoryStore()
	out := &bytes.Buffer{}
	e := NewEngine(EngineConfig{
		Graph:  graph,
		Logger: zerolog.New(out),
	})

	action := explorationgraph.Node{
		ID:      "act-norm",
		TaskID:  "t-norm",
		Kind:    core.KindAction,
		Content: json.RawMessage(`{"instruction":"i"}`),
	}
	// 内存图要求边两侧节点先存在（生产里 action 由 planner 落图）
	if _, err := graph.CreateNode(context.Background(), action); err != nil {
		t.Fatalf("建 action 节点失败: %v", err)
	}

	// 数组形状 repro + 标量断言——两种曾漏杀真假设的原始形状
	rawRepro := json.RawMessage(`["登录获取会话","GET http://t.local/x?name=<script>alert(1)</script> 观察回显"]`)
	result := &runtime.ReActResult{
		FinalAnswer: "```json\n{\"status\":\"completed\",\"summary\":\"s\",\"observations\":[{" +
			"\"statement\":\"xss_r name 参数存在反射型 XSS\"," +
			"\"reasoning\":\"r\",\"severity\":\"high\"," +
			"\"repro\":" + string(rawRepro) + "}]}\n```",
	}

	if err := e.processOutput(context.Background(), action, result); err != nil {
		t.Fatalf("processOutput: %v", err)
	}
	hyps, err := graph.ListHypotheses(context.Background(), "t-norm", explorationgraph.VerificationPending, 0)
	if err != nil || len(hyps) != 1 {
		t.Fatalf("应落图 1 条待验证假设: %d %v\nlogs:\n%s", len(hyps), err, out.String())
	}

	// 节点上存的是归一化后的配方信封对象，而非原始数组
	var content struct {
		Repro struct {
			Kind string `json:"kind"`
		} `json:"repro"`
	}
	if err := json.Unmarshal(hyps[0].Content, &content); err != nil {
		t.Fatalf("节点 repro 应为归一化信封对象: %v (%s)", err, hyps[0].Content)
	}
	if content.Repro.Kind != "steps" && content.Repro.Kind != "http" {
		t.Fatalf("归一化后应有合法 kind: %q", content.Repro.Kind)
	}
}

// 生命周期分流不变式：hypothesis 写入即待验证（pending，评估者从图上取），
// 事实写入即终态（无验证状态）——result 只承载经判定坐实的主张，事实永远不伪装成 result。
func TestHarvestObservation_CategoryLifecycleSplit(t *testing.T) {
	graph := explorationgraph.NewMemoryStore()
	e := NewEngine(EngineConfig{Graph: graph, Logger: zerolog.Nop()})
	action := explorationgraph.Node{
		ID: "act-split", TaskID: "t-split", Kind: core.KindAction,
		Content: json.RawMessage(`{"instruction":"i"}`),
	}
	if _, err := graph.CreateNode(context.Background(), action); err != nil {
		t.Fatalf("建 action 失败: %v", err)
	}

	// 事实：落图为终态
	fact := e.harvestObservation(context.Background(), action, Observation{
		Category:  explorationgraph.CategoryNote,
		Statement: "会话已建立且过滤机制由 X 控制",
	})
	if fact == nil || !fact.IsFact() {
		t.Fatalf("事实应落图且不带验证状态: %+v", fact)
	}

	// hypothesis：写入即待验证
	hyp := e.harvestObservation(context.Background(), action, Observation{
		Category:  explorationgraph.CategoryHypothesis,
		Statement: "X 参数存在可复现的注入现象",
		Repro:     json.RawMessage(`{"kind":"steps","recipe":{"steps":"1. 发请求 2. 观察回显"},"assert":{"description":"看到回显"}}`),
	})
	if hyp == nil || !hyp.HasVerificationStatus(explorationgraph.VerificationPending) {
		t.Fatalf("hypothesis 写入时应为待验证: %+v", hyp)
	}

	// 无配方的 hypothesis：落图前降级为事实（无配方的主张无从验证）
	spec := e.harvestObservation(context.Background(), action, Observation{
		Category:  explorationgraph.CategoryHypothesis,
		Statement: "未实测的推测",
	})
	if spec == nil || !spec.IsFact() {
		t.Fatalf("无配方 hypothesis 应降级为事实落图: %+v", spec)
	}
}

// 事实类观察不带配方，不该走配方归一化：此前对空配方也归一化，每条事实观察都报一次
// 「repro 归一化失败」（本地 e2e 一轮 37 条，全是事实观察），淹没真正缺判据的假设。
func TestHarvestObservation_FactWithoutReproIsNotNormalized(t *testing.T) {
	var logs bytes.Buffer
	graph := explorationgraph.NewMemoryStore()
	e := NewEngine(EngineConfig{Graph: graph, Logger: zerolog.New(&logs)})
	action := explorationgraph.Node{
		ID: "act-fact", TaskID: "t-fact", Kind: core.KindAction,
		Content: json.RawMessage(`{"instruction":"i"}`),
	}
	if _, err := graph.CreateNode(context.Background(), action); err != nil {
		t.Fatalf("建 action 失败: %v", err)
	}

	fact := Observation{Category: explorationgraph.CategoryBehavior, Statement: "登录接口对错误密码返回 200 与提示文案"}
	if e.harvestObservation(context.Background(), action, fact) == nil {
		t.Fatal("事实观察应落图")
	}
	if bytes.Contains(logs.Bytes(), []byte("repro 归一化失败")) {
		t.Fatalf("无配方的事实观察不应报归一化失败:\n%s", logs.String())
	}
}

// 优先级只在节点层存一份：观察给出的优先级归一后写进节点（图与界面据此展示），
// 内容里不再另存。
func TestHarvestObservation_PriorityLivesOnNode(t *testing.T) {
	graph := explorationgraph.NewMemoryStore()
	e := NewEngine(EngineConfig{Graph: graph, Logger: zerolog.Nop()})
	action := explorationgraph.Node{
		ID: "act-pri", TaskID: "t-pri", Kind: core.KindAction,
		Content: json.RawMessage(`{"instruction":"i"}`),
	}
	if _, err := graph.CreateNode(context.Background(), action); err != nil {
		t.Fatalf("建 action 失败: %v", err)
	}

	node := e.harvestObservation(context.Background(), action, Observation{
		Category:  explorationgraph.CategoryHypothesis,
		Statement: "管理接口未鉴权",
		Priority:  " Critical ",
		Repro:     json.RawMessage(`{"kind":"steps","recipe":{"steps":"1. 访问"},"assert":{"description":"返回管理数据"}}`),
	})
	if node == nil {
		t.Fatal("假设应落图")
	}
	if node.Priority != explorationgraph.PriorityCritical {
		t.Fatalf("节点优先级应为 critical，得 %q", node.Priority)
	}
	var content map[string]any
	if err := json.Unmarshal(node.Content, &content); err != nil {
		t.Fatal(err)
	}
	if _, ok := content["priority"]; ok {
		t.Fatalf("内容里不应再存优先级: %v", content)
	}
}

// 假设专属字段（priority / impact / suggestion / repro）只属于 hypothesis：事实是写入即
// 终态、不排程、无需验证的节点。契约只靠这里结构化约束，不依赖模型自觉——模型若给事实误填
// 这些字段一律丢弃。此前对所有类别无差别塞字段，priority 还因空值兜底成 medium 必然泄漏
// （每条事实都挂 priority=medium），impact/suggestion/repro 则随模型误填而混入。
func TestHarvestObservation_FactDropsHypothesisOnlyFields(t *testing.T) {
	graph := explorationgraph.NewMemoryStore()
	e := NewEngine(EngineConfig{Graph: graph, Logger: zerolog.Nop()})
	action := explorationgraph.Node{
		ID: "act-factpri", TaskID: "t-factpri", Kind: core.KindAction,
		Content: json.RawMessage(`{"instruction":"i"}`),
	}
	if _, err := graph.CreateNode(context.Background(), action); err != nil {
		t.Fatalf("建 action 失败: %v", err)
	}

	for _, tc := range []struct {
		name string
		obs  Observation
	}{
		{"事实未填专属字段", Observation{Category: explorationgraph.CategoryEntity, Statement: "目标为 nginx 1.18"}},
		{"事实误填专属字段一律丢弃", Observation{
			Category:   explorationgraph.CategoryBehavior,
			Statement:  "参数原样回显",
			Priority:   "high",
			Impact:     "影响面",
			Suggestion: "处理建议",
			Repro:      json.RawMessage(`{"kind":"steps","recipe":{"steps":"1. 发请求"},"assert":{"description":"看到回显"}}`),
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			node := e.harvestObservation(context.Background(), action, tc.obs)
			if node == nil || !node.IsFact() {
				t.Fatalf("事实应落图为终态: %+v", node)
			}
			if node.Priority != "" {
				t.Fatalf("事实不应带优先级，得 %q", node.Priority)
			}
			var content map[string]any
			if err := json.Unmarshal(node.Content, &content); err != nil {
				t.Fatal(err)
			}
			for _, k := range []string{"priority", "impact", "suggestion", "repro"} {
				if _, ok := content[k]; ok {
					t.Fatalf("事实内容不应带假设专属字段 %q: %v", k, content)
				}
			}
		})
	}
}

// 观察只承载陈述与配方：不再内嵌整个任务的流量轨迹（无任何读者，每条观察一份
// 全量拷贝；流量本就在流量表里，按任务可查）。
func TestHarvestObservation_NoExecutionTrace(t *testing.T) {
	graph := explorationgraph.NewMemoryStore()
	e := NewEngine(EngineConfig{Graph: graph, Logger: zerolog.Nop()})
	action := explorationgraph.Node{ID: "act-nt", TaskID: "t-nt", Kind: core.KindAction,
		Content: json.RawMessage(`{"instruction":"i"}`)}
	if _, err := graph.CreateNode(context.Background(), action); err != nil {
		t.Fatal(err)
	}
	if e.harvestObservation(context.Background(), action, Observation{
		Category: explorationgraph.CategoryEntity, Statement: "登录页存在",
	}) == nil {
		t.Fatal("观察应落图")
	}
	nodes, _ := graph.ListNodesByKind(context.Background(), "t-nt", core.KindObservation)
	var content map[string]any
	if err := json.Unmarshal(nodes[0].Content, &content); err != nil {
		t.Fatal(err)
	}
	if _, ok := content["execution_trace"]; ok {
		t.Fatalf("观察不应内嵌执行轨迹: %v", content)
	}
}

// 动作的执行输入只陈述事实：指令、来源（仅用户指派时）、复杂度、任务目标原文（逐字，
// 取自图上的根目标，调整目标后下一个动作即生效）。
func TestActionObjective(t *testing.T) {
	moderate := explorationgraph.ComplexityModerate
	user := explorationgraph.Node{SourceType: explorationgraph.SourceUser, Complexity: &moderate}
	planned := explorationgraph.Node{SourceType: explorationgraph.SourcePlanner}
	goal := "检查 http://t.local/login.php，账号 admin/password"

	got := actionObjective(user, &ActionContent{Instruction: "复测登录"}, goal)
	for _, want := range []string{"## 动作指令\n复测登录", "来源：用户指派", "复杂度：moderate", "## 任务目标（用户原文）\n" + goal} {
		if !strings.Contains(got, want) {
			t.Errorf("执行输入缺少 %q:\n%s", want, got)
		}
	}
	got = actionObjective(planned, &ActionContent{Instruction: "列出目录"}, "")
	if got != "## 动作指令\n列出目录\n" {
		t.Fatalf("规划者的动作不带来源，无复杂度与目标时只有指令，得 %q", got)
	}
}

// 系统提示词只随 agent 配置变化：章程 + 契约 + 工具，同一配置下所有动作共用一份。
func TestBuildSystemPrompt_ConfigOnly(t *testing.T) {
	e := NewEngine(EngineConfig{Charter: "你是执行者。", Logger: zerolog.Nop()})
	prompt := e.buildSystemPrompt(nil)
	if !strings.HasPrefix(prompt, "你是执行者。") || !strings.Contains(prompt, "输出契约") || !strings.Contains(prompt, "无可用工具") {
		t.Fatalf("系统提示词应为章程 + 契约 + 工具清单:\n%s", prompt)
	}
}

// 场景块拼在系统提示词末尾（工具清单之后）；无场景时不加。
func TestBuildSystemPrompt_AppendsScenario(t *testing.T) {
	plain := NewEngine(EngineConfig{Charter: "你是执行者。", Logger: zerolog.Nop()}).buildSystemPrompt(nil)
	block := "<scenario name=\"CTF\">\n规约\n</scenario>"
	got := NewEngine(EngineConfig{Charter: "你是执行者。", Scenario: block, Logger: zerolog.Nop()}).buildSystemPrompt(nil)
	if got != plain+"\n\n"+block {
		t.Fatalf("场景块应拼在末尾:\n%s", got)
	}
}
