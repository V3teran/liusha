package distill

import (
	"context"
	"fmt"
	"strings"

	"github.com/V3teran/liusha/internal/scenario"
	"github.com/V3teran/liusha/internal/task"
)

// maxLessons 是一个任务至多沉淀的经验条数。多数任务没有值得沉淀的经验，上限只是兜底。
const maxLessons = 3

// 经验的门槛：「吃过亏」由代码按引用核验，「不是常识、能迁移」由提示词约束。
const lessonsSystemPrompt = `你是经验提炼官。从一个已结束任务的过程里提炼以后的任务可以复用的经验（lessons）。

经验是一条能迁移到别的目标上的判断：在某种情境下，按常规做法会走弯路或判断失误，按这条经验可以避免。它只能来自本任务实际付出过代价的地方。

材料逐条编号（M1、M2……），标注【弯路】的是付出过代价的地方：被证伪的主张、失败或被否决的动作、做完却没有任何产出的动作。其余是走通的路：坐实的结果、确认的事实、有产出的动作。

每条候选经验逐项检查，任何一项不满足就不写：
1. 有代价为证：evidence 至少引用一条【弯路】材料，说明常规做法在哪里吃了亏；可以再引用走通的材料，说明正确的做法；
2. 不是常识：一个有经验的同行默认就会这么做的，不写；
3. 能迁移：去掉本任务目标特有的地址、名称、取值、位置之后仍然成立；只对这个目标成立的是对本题解法的复述，不写。

以下内容也不写：
- 关于执行系统本身的工具、记录格式、流程或限制的观察：那是待修的系统问题，不是经验；
- 任务场景里已经写明的规定。

多数任务没有值得沉淀的经验，输出空数组是正常的结果。至多 3 条。

每条经验：
- title：一句话（不超过 40 字）；
- content：正文（不超过 300 字），写清情境、常规做法的问题、应当怎么做。正文写给以后的任务读：不出现「本题」「本任务」这类指代（换了任务就失去所指），本任务的具体细节至多作为一句例子；
- scoped：经验依赖任务场景的规定（如什么算可交付、类别词表）时为 true，只对同一场景的任务可见；不依赖为 false；没有任务场景时一律为 false；
- evidence：引用的材料编号。

只输出 JSON，格式：
` + "```json\n" + `{"lessons": [{"title": "...", "content": "...", "scoped": false, "evidence": ["M3", "M7"]}]}
` + "```"

// Lesson 是一条候选经验。
type Lesson struct {
	Title    string   `json:"title"`
	Content  string   `json:"content"`
	Scoped   bool     `json:"scoped"`   // 依赖任务场景的规定：只对同一场景的任务可见
	Evidence []string `json:"evidence"` // 引用的材料编号
}

// generate 调 LLM 提炼候选经验，并按证据门槛过滤。解析失败按无经验处理——沉淀是尽力而为的
// 收尾，不为格式问题重试。
func (s *Service) generate(ctx context.Context, goal string, sc *task.Scenario, ms []material) ([]Lesson, error) {
	var user strings.Builder
	user.WriteString("【任务目标】\n" + goal + "\n\n【任务场景】\n")
	if sc == nil {
		user.WriteString("（无）")
	} else {
		user.WriteString(scenario.Render(sc.Name, sc.Body))
	}
	user.WriteString("\n\n【材料】\n" + renderMaterials(ms))

	var payload struct {
		Lessons []Lesson `json:"lessons"`
	}
	if ok, err := s.complete(ctx, lessonsSystemPrompt, user.String(), &payload); err != nil || !ok {
		return nil, err
	}
	kept, dropped := admit(payload.Lessons, ms, sc != nil)
	for _, d := range dropped {
		s.logger.Info().Str("lesson", d).Msg("候选经验未过门槛，不沉淀")
	}
	return kept, nil
}

// admit 按门槛筛选候选经验：标题正文不能为空；evidence 引用的编号必须存在，且至少一条是弯路
// ——没有代价为证的不是经验。至多保留 maxLessons 条。dropped 写明每条被拒的原因。
func admit(lessons []Lesson, ms []material, hasScenario bool) (kept []Lesson, dropped []string) {
	byRef := make(map[string]material, len(ms))
	for _, m := range ms {
		if m.ref != "" {
			byRef[m.ref] = m
		}
	}
	for _, l := range lessons {
		l.Title, l.Content = strings.TrimSpace(l.Title), strings.TrimSpace(l.Content)
		if l.Title == "" || l.Content == "" {
			dropped = append(dropped, "标题或正文为空")
			continue
		}
		var refs []string
		detour := false
		for _, r := range l.Evidence {
			m, ok := byRef[strings.Trim(strings.TrimSpace(r), "[]")]
			if !ok {
				continue
			}
			refs = append(refs, m.ref)
			detour = detour || m.detour
		}
		if !detour {
			dropped = append(dropped, fmt.Sprintf("「%s」没有引用弯路材料（引用 %v）", l.Title, l.Evidence))
			continue
		}
		if len(kept) == maxLessons {
			dropped = append(dropped, fmt.Sprintf("「%s」超出每个任务 %d 条的上限", l.Title, maxLessons))
			continue
		}
		l.Evidence = refs
		l.Scoped = l.Scoped && hasScenario
		kept = append(kept, l)
	}
	return kept, dropped
}
