package planner

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/V3teran/liusha/internal/provider"
	"github.com/V3teran/liusha/internal/worldmodel"
)

// Strategy 决定 frontier 招法的排序，也可裁剪，但不得新增 frontier 之外的招法。
// 分离动机：候选是确定性图逻辑（可单测），先打哪个是策略判断（未来接 LLM 打分）。
type Strategy interface {
	Rank(ctx context.Context, req RankRequest) ([]Move, error)
}

// RankRequest 是 Strategy.Rank 的输入参数，包含 Frontier + 上下文信息。
type RankRequest struct {
	Frontier    []Move               // 当前可执行的 Move 候选
	TopK        []worldmodel.Node    // 高价值节点（按相关性排序）
	MoveRecords []worldmodel.Move    // 历史 Move 执行记录（最近N条）
	State       map[string]interface{} // 其他状态信息（预留）
}

// killChainStrategy 按杀伤链纵深优先：利用已知弱点 > 探测新弱点 > 枚举新面 > 持久化。
// 无 LLM 依赖，行为可预测——LLM 策略就位前的兜底。
type killChainStrategy struct{}

func NewKillChainStrategy() Strategy { return killChainStrategy{} }

var moveWeight = map[MoveKind]int{
	MoveExploit:   50, // 最高优先级：坐实利用，获取立足点
	MoveEscalate:  40, // 扩展攻击面：提权/横移
	MoveProbe:     30, // 探测弱点
	MoveEnumerate: 20, // 枚举新面
	MovePersist:   10, // 后渗透/持久化
}

func (killChainStrategy) Rank(ctx context.Context, req RankRequest) ([]Move, error) {
	out := make([]Move, len(req.Frontier))
	copy(out, req.Frontier)
	for i := range out {
		out[i].Priority = moveWeight[out[i].Kind]
	}
	// 稳定排序保同权重下的自然顺序（节点 Seq 序），结果可复现。
	stableSortByPriorityDesc(out)
	return out, nil
}

func stableSortByPriorityDesc(xs []Move) {
	for i := 1; i < len(xs); i++ {
		for j := i; j > 0 && xs[j].Priority > xs[j-1].Priority; j-- {
			xs[j], xs[j-1] = xs[j-1], xs[j]
		}
	}
}

// ────────────────────────────────────────────────────────────────
//  LLM Strategy（智能规划，替换硬编码权重）
// ────────────────────────────────────────────────────────────────

// LLMStrategy 调用 LLM 对 Frontier 进行智能排序，失败时降级到 KillChainStrategy。
type LLMStrategy struct {
	provider         provider.Provider
	fallbackStrategy Strategy
}

func NewLLMStrategy(p provider.Provider) Strategy {
	return &LLMStrategy{
		provider:         p,
		fallbackStrategy: NewKillChainStrategy(),
	}
}

func (s *LLMStrategy) Rank(ctx context.Context, req RankRequest) ([]Move, error) {
	// 1. 构造 Planner prompt
	prompt := buildPlannerPrompt(req)

	// 2. 调用 LLM（纯文本，要求返回 JSON）
	resp, err := s.provider.Complete(ctx, provider.Request{
		Messages: []provider.Message{{Role: "user", Content: prompt}},
	})
	if err != nil || resp.Content == "" {
		// LLM 失败，降级到 KillChainStrategy
		return s.fallbackStrategy.Rank(ctx, req)
	}

	// 3. 提取 JSON（可能包裹在 markdown code block 中）
	jsonStr := extractJSON(resp.Content)
	if jsonStr == "" {
		// 无法提取 JSON，降级
		return s.fallbackStrategy.Rank(ctx, req)
	}

	// 4. 解析 JSON
	var planned []struct {
		OnNodeID string  `json:"on_node_id"`
		Reason   string  `json:"reason"`
		Priority float64 `json:"priority"`
	}
	if err := json.Unmarshal([]byte(jsonStr), &planned); err != nil {
		// LLM 返回非法 JSON，降级
		return s.fallbackStrategy.Rank(ctx, req)
	}

	// 5. 构造 on_node_id → Move 映射（用于匹配 Frontier）
	nodeToMove := make(map[string]*Move)
	for i := range req.Frontier {
		nodeToMove[req.Frontier[i].OnNodeID] = &req.Frontier[i]
	}

	// 6. 按 LLM 输出的优先级构造结果
	var result []Move
	seen := make(map[string]bool)
	for _, p := range planned {
		if m, ok := nodeToMove[p.OnNodeID]; ok && !seen[p.OnNodeID] {
			m.Priority = int(p.Priority * 10) // 转为整数（保留小数点后1位）
			m.Reason = p.Reason                // 用 LLM 的 reason 覆盖原因
			result = append(result, *m)
			seen[p.OnNodeID] = true
		}
	}

	// 7. 追加 LLM 未提及的 Frontier Move（权重设为0）
	for _, m := range req.Frontier {
		if !seen[m.OnNodeID] {
			m.Priority = 0
			result = append(result, m)
		}
	}

	// 8. 按 priority 降序排序
	sort.SliceStable(result, func(i, j int) bool {
		return result[i].Priority > result[j].Priority
	})

	return result, nil
}

// extractJSON 从 LLM 响应中提取 JSON（处理 markdown code block 包裹）。
func extractJSON(s string) string {
	// 尝试提取 ```json ... ``` 或 ``` ... ``` 包裹的内容
	start := strings.Index(s, "```json")
	if start == -1 {
		start = strings.Index(s, "```")
	}
	if start == -1 {
		// 无 markdown block，直接返回原文
		return strings.TrimSpace(s)
	}

	// 跳过 ```json 或 ```
	start = strings.Index(s[start:], "\n")
	if start == -1 {
		return ""
	}
	start += strings.Index(s, "```")

	// 找到结束的 ```
	end := strings.Index(s[start:], "```")
	if end == -1 {
		return ""
	}

	return strings.TrimSpace(s[start : start+end])
}

// buildPlannerPrompt 构造 LLM Planner 的 prompt。
func buildPlannerPrompt(req RankRequest) string {
	var b strings.Builder

	b.WriteString("你是一个渗透测试规划器。根据当前攻击图状态，决定下一步执行哪些 Move。\n\n")

	// 1. 当前攻击前沿（Frontier）
	b.WriteString("## 当前攻击前沿（Frontier）\n")
	if len(req.Frontier) == 0 {
		b.WriteString("（无可执行 Move）\n")
	} else {
		for i, m := range req.Frontier {
			b.WriteString(fmt.Sprintf("%d. **%s** on `%s`\n", i+1, m.Kind, m.OnNodeID))
			if m.Reason != "" {
				b.WriteString(fmt.Sprintf("   - 原因：%s\n", m.Reason))
			}
			b.WriteString(fmt.Sprintf("   - 目标：domain=%s, refKind=%s, locator=%s\n",
				m.Target.Domain, m.Target.RefKind, m.Target.Locator))
		}
	}
	b.WriteString("\n")

	// 2. 高价值节点（TopK）
	b.WriteString("## 高价值节点（TopK）\n")
	if len(req.TopK) == 0 {
		b.WriteString("（无）\n")
	} else {
		for i, n := range req.TopK {
			b.WriteString(fmt.Sprintf("%d. **%s** (id=%s)\n", i+1, n.Kind, n.ID))
			b.WriteString(fmt.Sprintf("   - domain=%s, refKind=%s, locator=%s\n",
				n.Ref.Domain, n.Ref.RefKind, n.Ref.Locator))
			if n.Confidence != "" {
				b.WriteString(fmt.Sprintf("   - confidence=%s\n", n.Confidence))
			}
		}
	}
	b.WriteString("\n")

	// 3. 历史执行记录（MoveRecords）
	b.WriteString("## 历史执行记录（MoveRecords）\n")
	if len(req.MoveRecords) == 0 {
		b.WriteString("（无）\n")
	} else {
		for i, m := range req.MoveRecords {
			b.WriteString(fmt.Sprintf("%d. **%s** (status=%s, target=%s)\n",
				i+1, m.Kind, m.Status, m.TargetNode))
			if m.Reason != "" {
				b.WriteString(fmt.Sprintf("   - 原因：%s\n", m.Reason))
			}
		}
	}
	b.WriteString("\n")

	// 4. 可用 MoveKind
	b.WriteString("## 可用 MoveKind\n")
	b.WriteString("- enumerate: 信息收集/枚举（web目录、端口、云资产、域对象）\n")
	b.WriteString("- probe: 漏洞探测/弱点发现（扫描/fuzz/初步PoC）\n")
	b.WriteString("- exploit: 漏洞利用/坐实（PoC确认、获取立足点）\n")
	b.WriteString("- escalate: 权限提升/横向移动（本机提权、域提权、跨主机）\n")
	b.WriteString("- persist: 后渗透（数据采集、持久化、影响评估）\n\n")

	// 5. 输出格式
	b.WriteString("## 输出格式\n")
	b.WriteString("返回 JSON 数组，每个元素包含：\n")
	b.WriteString("- on_node_id: 在哪个图节点上执行（从 Frontier 中选取）\n")
	b.WriteString("- reason: 为什么选这个 Move（1-2句话）\n")
	b.WriteString("- priority: 优先级（1-10，10最高）\n\n")
	b.WriteString("按优先级降序排列，最多返回5个 Move。\n")

	return b.String()
}
