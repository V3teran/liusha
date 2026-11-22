package corpus

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// Section 是从 Markdown 切出的一条待导入知识。
type Section struct {
	Title   string
	Content string
}

// SplitMarkdown 把 Markdown 切成知识条目：每个标题起一节，标题文字作条目标题；首个标题之前
// 的内容以首行为标题。正文超过 MaxContentRunes 的节按段落（空行）再切，段落本身过长时按行、
// 再按字数切；切出的各段标题标注序号（「标题（2/3）」）。只有标题没有正文的节跳过。
func SplitMarkdown(md string) []Section {
	var out []Section
	title, body := "", strings.Builder{}
	flush := func() {
		content := strings.TrimSpace(body.String())
		body.Reset()
		if content == "" {
			return
		}
		t := title
		if t == "" {
			t = firstLine(content)
		}
		out = append(out, splitLong(t, content)...)
	}
	inFence := false // 代码围栏内的「# 注释」不是标题
	for _, line := range strings.Split(strings.ReplaceAll(md, "\r\n", "\n"), "\n") {
		if t := strings.TrimSpace(line); strings.HasPrefix(t, "```") || strings.HasPrefix(t, "~~~") {
			inFence = !inFence
		} else if h, ok := heading(line); ok && !inFence {
			flush()
			title = h
			continue
		}
		body.WriteString(line)
		body.WriteByte('\n')
	}
	flush()
	return out
}

// heading 识别 ATX 标题行（「# 标题」到「###### 标题」），返回标题文字。
func heading(line string) (string, bool) {
	t := strings.TrimSpace(line)
	level := len(t) - len(strings.TrimLeft(t, "#"))
	if level == 0 || level > 6 || (len(t) > level && t[level] != ' ') {
		return "", false
	}
	h := strings.TrimSpace(strings.TrimRight(strings.TrimSpace(t[level:]), "#"))
	return clipRunes(h, MaxTitleRunes), h != ""
}

func firstLine(content string) string {
	line, _, _ := strings.Cut(content, "\n")
	return clipRunes(strings.TrimSpace(line), MaxTitleRunes)
}

func clipRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n])
}

// splitLong 把超长正文切成不超过 MaxContentRunes 的若干段。
func splitLong(title, content string) []Section {
	if utf8.RuneCountInString(content) <= MaxContentRunes {
		return []Section{{Title: title, Content: content}}
	}
	var parts []string
	var cur strings.Builder
	push := func() {
		if s := strings.TrimSpace(cur.String()); s != "" {
			parts = append(parts, s)
		}
		cur.Reset()
	}
	for _, unit := range units(content) {
		if cur.Len() > 0 && utf8.RuneCountInString(cur.String())+utf8.RuneCountInString(unit)+2 > MaxContentRunes {
			push()
		}
		if cur.Len() > 0 {
			cur.WriteString("\n\n")
		}
		cur.WriteString(unit)
	}
	push()
	out := make([]Section, len(parts))
	for i, p := range parts {
		suffix := fmt.Sprintf("（%d/%d）", i+1, len(parts))
		out[i] = Section{Title: clipRunes(title, MaxTitleRunes-utf8.RuneCountInString(suffix)) + suffix, Content: p}
	}
	return out
}

// units 把正文拆成不超过上限的切分单元：段落；过长的段落拆成行；过长的行按字数硬切。
func units(content string) []string {
	var out []string
	for _, para := range strings.Split(content, "\n\n") {
		para = strings.TrimSpace(para)
		switch {
		case para == "":
		case utf8.RuneCountInString(para) <= MaxContentRunes:
			out = append(out, para)
		default:
			for _, line := range strings.Split(para, "\n") {
				r := []rune(strings.TrimSpace(line))
				for len(r) > MaxContentRunes {
					out = append(out, string(r[:MaxContentRunes]))
					r = r[MaxContentRunes:]
				}
				if len(r) > 0 {
					out = append(out, string(r))
				}
			}
		}
	}
	return out
}
