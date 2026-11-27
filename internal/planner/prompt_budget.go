package planner

import (
	"fmt"
	"regexp"
	"strings"
)

// 规划摘要的字节预算。
//
// 摘要是规划者每轮的第一条输入，由代码按图面拼装，只随图增长。框架侧对单条消息有硬上限
// （runtime.MaxMessageBytes，按字节计），而上下文压缩管的是历史：它只裁最旧的整条消息，
// 从不裁一条消息内部的内容，只有一条消息时原样放行——摘要自己超长时没有任何东西兜底。
// 实测一个跑了 44 个动作的任务，摘要涨到约 10 万字节后被校验哨兵拒绝，此后每一轮规划都在
// 发出去之前失败，任务又空转了两个多小时。
//
// 所以摘要在这里设预算：装不下时按各段实际占用等比收紧，每一段都留最新的条目、把更早的
// 折叠成一行计数（细节经 graph_read 按需读）。这一轮要处理的事——待深挖的线索、在办与待
// 验证的工作——不参与折叠。预算比硬上限留出余量：摘要只是第一条输入，同一轮里系统提示、
// 追问与工具结果各自成条。
const planningPromptBudget = 60 * 1024

// minKeepItems 是一个条目组被收紧时至少留下的条数：不到这个数的组不动，收紧落在条目多的
// 组上（折叠一行换不来多少空间，却会让一段内容整个消失）。
const minKeepItems = 5

// promptSection 是规划摘要里的一段。
type promptSection struct {
	// name 是折叠行里对条目的称呼（「已完成动作」「事实」…）；空表示这一段不折叠。
	name string
	// newestFirst：段内条目新的在前（事实按类别分桶、桶内近者在前），折叠从末尾旧的去；
	// 否则条目按时间先后排列，折叠从开头旧的去。
	newestFirst bool
	text        string
}

// 条目行以「- 」开头。各段自己折叠时写的计数行不是条目，原样保留。
var (
	itemLinePattern = regexp.MustCompile(`^- `)
	foldNotePattern = regexp.MustCompile(`^- （(?:更早的|另有) \d+ 条`)
	headingPattern  = regexp.MustCompile(`^##+ `)
)

// sectionLines 是一段可折叠的内容解析后的样子：一串按原顺序排列的块，原样保留的行
// （标题、说明、空行）与条目组相间。
type sectionLines struct {
	blocks          []sectionBlock
	trailingNewline bool
}

// sectionBlock 要么是原样保留的一行，要么是一个条目组。
type sectionBlock struct {
	line  string
	group *itemGroup
}

// itemGroup 是一段里连续的一组条目行，连同它前面最近的标题对它们的称呼。
type itemGroup struct {
	name        string
	newestFirst bool
	lines       []string // 条目行（不含换行），按段内的先后排列
	dropped     int      // 已折叠的更早条数
}

// kept 是此刻留着的条目。
func (g *itemGroup) kept() []string {
	if g.newestFirst {
		return g.lines[:len(g.lines)-g.dropped]
	}
	return g.lines[g.dropped:]
}

// oldestKept 是留着的里面最旧的那条的下标（在 lines 里）。
func (g *itemGroup) oldestKept() int {
	if g.newestFirst {
		return len(g.lines) - g.dropped - 1
	}
	return g.dropped
}

// minKeep 是这个组至少留下的条数。
func (g *itemGroup) minKeep() int {
	if len(g.lines) < minKeepItems {
		return len(g.lines)
	}
	return minKeepItems
}

// droppable 是还能折叠的条数：留下的比最低保留数多的那些。
func (g *itemGroup) droppable() int {
	return len(g.kept()) - g.minKeep()
}

// shrinkableBytes 是还能让出的字节（每条含换行）：从最旧的留着的那条起、朝新的方向数。
func (g *itemGroup) shrinkableBytes() int {
	total := 0
	for i := 0; i < g.droppable(); i++ {
		idx := g.oldestKept() + i
		if g.newestFirst {
			idx = g.oldestKept() - i
		}
		total += len(g.lines[idx]) + 1
	}
	return total
}

// dropNext 折叠留着的里面最旧的那条。
func (g *itemGroup) dropNext() {
	g.dropped++
}

func parseSection(s promptSection) *sectionLines {
	sl := &sectionLines{trailingNewline: strings.HasSuffix(s.text, "\n")}
	name := s.name
	for _, line := range strings.Split(strings.TrimSuffix(s.text, "\n"), "\n") {
		switch {
		case headingPattern.MatchString(line):
			name = groupName(line, s.name)
			sl.blocks = append(sl.blocks, sectionBlock{line: line})
		case itemLinePattern.MatchString(line) && !foldNotePattern.MatchString(line):
			if n := len(sl.blocks); n > 0 && sl.blocks[n-1].group != nil {
				sl.blocks[n-1].group.lines = append(sl.blocks[n-1].group.lines, line)
			} else {
				sl.blocks = append(sl.blocks, sectionBlock{group: &itemGroup{name: name, newestFirst: s.newestFirst, lines: []string{line}}})
			}
		default:
			sl.blocks = append(sl.blocks, sectionBlock{line: line})
		}
	}
	return sl
}

// groupName 是标题之下条目的称呼：「### 有什么（对象与属性）」下的叫「「有什么」事实」，
// 其余沿用段自己的名字。
func groupName(heading, sectionName string) string {
	if !strings.HasPrefix(heading, "### ") {
		return sectionName
	}
	title := strings.TrimPrefix(heading, "### ")
	if i := strings.Index(title, "（"); i > 0 {
		title = title[:i]
	}
	return "「" + title + "」事实"
}

func (sl *sectionLines) render() string {
	var sb strings.Builder
	for _, b := range sl.blocks {
		if b.group == nil {
			sb.WriteString(b.line)
			sb.WriteByte('\n')
			continue
		}
		if b.group.dropped > 0 {
			fmt.Fprintf(&sb, "- （更早的 %d 条%s已折叠，需要时用 graph_read 深挖）\n", b.group.dropped, b.group.name)
		}
		for _, line := range b.group.kept() {
			sb.WriteString(line)
			sb.WriteByte('\n')
		}
	}
	if !sl.trailingNewline {
		return strings.TrimSuffix(sb.String(), "\n")
	}
	return sb.String()
}

// fitPrompt 把各段拼成摘要；超出 budget 字节时折叠更早的条目，直到装得下。
func fitPrompt(sections []promptSection, budget int) string {
	joined := concat(sections)
	if len(joined) <= budget {
		return joined
	}

	parsed := make([]*sectionLines, len(sections))
	for i, s := range sections {
		if s.name != "" {
			parsed[i] = parseSection(s)
		}
	}

	// 等比收紧：每一轮把当前超出量按「可让出的字节」摊给各个条目组，从最旧的条目起整条去掉；
	// 整条去掉会略超过份额，折叠行本身也要占几个字节，所以每轮过后重新计量。
	out := render(sections, parsed)
	for over := len(out) - budget; over > 0; over = len(out) - budget {
		groups := trimmableGroups(parsed)
		if len(groups) == 0 {
			break
		}
		shrinkable := 0
		for _, g := range groups {
			shrinkable += g.shrinkableBytes()
		}
		for _, g := range groups {
			share := over * g.shrinkableBytes() / shrinkable
			for dropped := 0; g.droppable() > 0 && dropped < share; {
				dropped += len(g.lines[g.oldestKept()]) + 1
				g.dropNext()
			}
		}
		// 份额摊到零头时可能一组都没动（或折叠行把让出的字节又占了回去）：从还能让出最多
		// 字节的组里再去掉一条，保证每一轮都有进展
		if len(render(sections, parsed))-budget >= over {
			largest := groups[0]
			for _, g := range groups[1:] {
				if g.shrinkableBytes() > largest.shrinkableBytes() {
					largest = g
				}
			}
			if largest.droppable() == 0 {
				break
			}
			largest.dropNext()
		}
		out = render(sections, parsed)
	}
	if len(out) <= budget {
		return out
	}
	// 不该走到这里：不参与折叠的内容自己就超预算时截断，好过让规划者在发出去之前就失败。
	return truncateToBudget(out, budget)
}

// concat 是未经折叠的原文。
func concat(sections []promptSection) string {
	total := 0
	for _, s := range sections {
		total += len(s.text)
	}
	var sb strings.Builder
	sb.Grow(total)
	for _, s := range sections {
		sb.WriteString(s.text)
	}
	return sb.String()
}

// render 按解析结果重新拼出摘要：可折叠的段用各自的块，其余原样。
func render(sections []promptSection, parsed []*sectionLines) string {
	var sb strings.Builder
	for i, s := range sections {
		if parsed[i] == nil {
			sb.WriteString(s.text)
			continue
		}
		sb.WriteString(parsed[i].render())
	}
	return sb.String()
}

func trimmableGroups(parsed []*sectionLines) []*itemGroup {
	var groups []*itemGroup
	for _, sl := range parsed {
		if sl == nil {
			continue
		}
		for _, b := range sl.blocks {
			if b.group != nil && b.group.droppable() > 0 {
				groups = append(groups, b.group)
			}
		}
	}
	return groups
}

// truncateToBudget 在最后一个完整的行上截断，并写明截断。
func truncateToBudget(out string, budget int) string {
	const notice = "- （摘要超出预算，已截断）\n"
	if budget <= len(notice) {
		return notice[:max(budget, 0)]
	}
	limit := budget - len(notice)
	if cut := strings.LastIndexByte(out[:limit], '\n'); cut >= 0 {
		return out[:cut+1] + notice
	}
	return out[:limit] + "\n" + notice
}
