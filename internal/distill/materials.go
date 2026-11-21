package distill

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/V3teran/liusha/internal/explorationgraph"
	"github.com/V3teran/liusha/internal/framework/core"
	"github.com/V3teran/liusha/internal/textx"
)

// 经验只能来自付出过代价的地方，材料因此分两类：弯路（证伪的主张、失败或被否决的动作、做完
// 却没有任何产出的动作）与正路（坐实的结果、确认的事实、有产出的动作）。被中止的动作不算：
// 它只是任务收口或目标调整时被收回，不含对错判断。

// material 是一条编号的提炼材料。
type material struct {
	ref    string // M1、M2……（按最终顺序编号）
	detour bool
	text   string
}

// 材料体量钳制：持续探索的图只增不减，无界材料会撑爆模型窗口（本调用没有上下文压缩，入口
// 限量是唯一防线）。单条截断，整体按桶优先级让位。
const (
	materialsMaxRunes = 24000
	materialMaxRunes  = 360
)

type statement struct {
	Statement   string `json:"statement"`
	Instruction string `json:"instruction"`
}

func readContent(n explorationgraph.Node) statement {
	var c statement
	if err := json.Unmarshal(n.Content, &c); err != nil {
		return statement{}
	}
	c.Statement, c.Instruction = strings.TrimSpace(c.Statement), strings.TrimSpace(c.Instruction)
	return c
}

// collect 从探索图汇集提炼材料。读失败只告警并让对应的桶缺席——沉淀尽力而为。
func (s *Service) collect(ctx context.Context, taskID string) []material {
	var detours, results, facts, yielded []string

	if nodes, err := s.graph.ListNodesByKind(ctx, taskID, core.KindObservation); err != nil {
		s.logger.Warn().Err(err).Msg("读取观察节点失败（事实与证伪的主张缺席）")
	} else {
		for _, n := range nodes {
			c := readContent(n)
			switch {
			case c.Statement == "":
			case n.IsFact():
				facts = append(facts, "确认的事实："+c.Statement)
			case n.HasVerificationStatus(explorationgraph.VerificationRefuted):
				detours = append(detours, refutedText(c.Statement, n.RefuteReason(), n.RefuteDetail()))
			}
		}
	}

	if nodes, err := s.graph.ListNodesByKind(ctx, taskID, core.KindResult); err != nil {
		s.logger.Warn().Err(err).Msg("读取结果节点失败（坐实的结果缺席）")
	} else {
		for _, n := range nodes {
			if c := readContent(n); c.Statement != "" {
				results = append(results, "坐实的结果："+c.Statement)
			}
		}
	}

	actionDetours, actionYields := s.actionMaterials(ctx, taskID)
	detours = append(detours, actionDetours...)
	yielded = append(yielded, actionYields...)
	return assembleMaterials(detours, results, facts, yielded)
}

func refutedText(st, reason, detail string) string {
	t := "证伪的主张：" + st + "（原因：" + reason
	if detail != "" {
		t += "；说明：" + detail
	}
	return t + "）"
}

// actionMaterials 按动作的结局分类：失败、被否决、做完却没有产出任何观察的是弯路；有产出的
// 是正路（附产出数）。被中止与尚未结束的动作不入材料。
func (s *Service) actionMaterials(ctx context.Context, taskID string) (detours, yielded []string) {
	actions, err := s.graph.ListNodesByKind(ctx, taskID, core.KindAction)
	if err != nil {
		s.logger.Warn().Err(err).Msg("读取动作节点失败（动作的结局缺席）")
		return nil, nil
	}
	edges, err := s.graph.ListEdgesByTask(ctx, taskID)
	if err != nil {
		s.logger.Warn().Err(err).Msg("读取边失败（动作的产出缺席）")
		return nil, nil
	}
	obs, err := s.graph.ListNodesByKind(ctx, taskID, core.KindObservation)
	if err != nil {
		s.logger.Warn().Err(err).Msg("读取观察节点失败（动作的产出缺席）")
		return nil, nil
	}
	isObs := make(map[string]bool, len(obs))
	for _, o := range obs {
		isObs[o.ID] = true
	}
	produced := map[string]int{}
	for _, e := range edges {
		if e.Rel == explorationgraph.RelGenerates && isObs[e.DstID] {
			produced[e.SrcID]++
		}
	}
	for _, a := range actions {
		inst := readContent(a).Instruction
		if inst == "" || a.State == nil {
			continue
		}
		reason := ""
		if a.StateReason != nil && strings.TrimSpace(*a.StateReason) != "" {
			reason = "（原因：" + strings.TrimSpace(*a.StateReason) + "）"
		}
		switch *a.State {
		case explorationgraph.StateFailed:
			detours = append(detours, "失败的动作："+inst+reason)
		case explorationgraph.StateRejected:
			detours = append(detours, "被否决的动作："+inst+reason)
		case explorationgraph.StateCompleted:
			if n := produced[a.ID]; n == 0 {
				detours = append(detours, "没有任何产出的动作："+inst)
			} else {
				yielded = append(yielded, fmt.Sprintf("有产出的动作（%d 条观察）：%s", n, inst))
			}
		}
	}
	return detours, yielded
}

// assembleMaterials 按桶优先级组装并编号：弯路、结果、事实、有产出的动作。单条截断；预算
// 耗尽后低优先级的桶让位，桶内保留最新的（最新转写反映任务终态），未纳入的条数显式说明。
// 各桶入参为创建序（旧→新）；第一个桶是弯路。
func assembleMaterials(detours []string, rest ...[]string) []material {
	buckets := append([][]string{detours}, rest...)
	total, dropped := 0, 0
	full := false
	var out []material
	for bi, bucket := range buckets {
		if full {
			dropped += len(bucket)
			continue
		}
		kept := make([]string, 0, len(bucket))
		for i := len(bucket) - 1; i >= 0; i-- {
			text := textx.TruncateRunes(bucket[i], materialMaxRunes, "…（截断）")
			n := len([]rune(text))
			if total+n > materialsMaxRunes && len(out)+len(kept) > 0 {
				full = true
				dropped += i + 1
				break
			}
			total += n
			kept = append(kept, text)
		}
		for i := len(kept) - 1; i >= 0; i-- { // 新→旧收集，按时间正序编入
			out = append(out, material{detour: bi == 0, text: kept[i]})
		}
	}
	for i := range out {
		out[i].ref = fmt.Sprintf("M%d", i+1)
	}
	if dropped > 0 {
		out = append(out, material{text: fmt.Sprintf("（另有 %d 条较早的材料因体量上限未列出）", dropped)})
	}
	return out
}

// hasDetour 报告材料里是否有弯路：没有吃过亏就没有可沉淀的经验，不必调用 LLM。
func hasDetour(ms []material) bool {
	for _, m := range ms {
		if m.detour {
			return true
		}
	}
	return false
}

// renderMaterials 写出材料：弯路带【弯路】标注，供经验引用。
func renderMaterials(ms []material) string {
	var b strings.Builder
	for _, m := range ms {
		switch {
		case m.ref == "":
			b.WriteString(m.text)
		case m.detour:
			fmt.Fprintf(&b, "[%s]【弯路】%s", m.ref, m.text)
		default:
			fmt.Fprintf(&b, "[%s] %s", m.ref, m.text)
		}
		b.WriteByte('\n')
	}
	return b.String()
}
