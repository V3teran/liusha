package distill

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/V3teran/liusha/internal/corpus"
	"github.com/V3teran/liusha/internal/task"
)

// neighborLimit 是整合时每条候选带上的已有条目数。
const neighborLimit = 3

const consolidateSystemPrompt = `你是知识库整理员。一个任务沉淀出若干条候选经验（C1、C2……），每条附上知识库里与它最接近的已有条目（E1、E2……）。逐条判定候选与已有条目的关系：
- add：已有条目讲的都不是同一件事，候选作为新条目写入；
- confirm：某条已有条目已经讲了同一件事（措辞可以不同），候选只是再次印证它，不写入新条目；
- revise：某条已有条目讲的是同一件事，但候选让它更准确或更完整（补充适用条件、纠正错误）；给出改写后的完整标题与正文，保留已有条目里仍然成立的内容；
- merge：候选与两条及以上已有条目讲的是同一件事（已有条目彼此重复）；把它们合成一条，给出合并后的完整标题与正文，第一条保留，其余并入后删除。

只能改写或合并来源为「任务沉淀」的条目，「人工录入」的条目只能 confirm 或另行 add；合并的条目适用范围必须相同。被多个任务印证过的条目，改写要更谨慎。

targets 写已有条目的编号：confirm 与 revise 一个，merge 两个及以上，add 为空数组。title 与 content 只在 revise、merge 时填写，标题不超过 40 字、正文不超过 300 字。

只输出 JSON，格式：
` + "```json\n" + `{"decisions": [{"candidate": "C1", "action": "confirm", "targets": ["E2"], "title": "", "content": ""}]}
` + "```"

const (
	actionAdd     = "add"
	actionConfirm = "confirm"
	actionRevise  = "revise"
	actionMerge   = "merge"
)

// candidate 是带着近似已有条目的一条候选。
type candidate struct {
	lesson    Lesson
	neighbors []corpus.Entry
}

// decision 是一条候选的整合结论（targets 已从编号解析为条目）。
type decision struct {
	action  string
	targets []corpus.Entry
	title   string
	content string
}

// consolidate 把候选经验与已有条目整合后写入（调用方持有知识库写锁）。
func (s *Service) consolidate(ctx context.Context, tk task.Task, lessons []Lesson) (Outcome, error) {
	scope := corpus.Scope{}
	if tk.Scenario != nil {
		code := tk.Scenario.Code
		scope.Scenario = &code
	}
	cands := make([]candidate, len(lessons))
	anyNeighbor := false
	for i, l := range lessons {
		res, err := s.corpus.Search(ctx, corpus.Query{Text: corpus.Text(l.Title, l.Content), Scope: scope, Limit: neighborLimit})
		if err != nil {
			return Outcome{}, fmt.Errorf("检索近似条目: %w", err)
		}
		cands[i] = candidate{lesson: l}
		for _, h := range res.Hits {
			cands[i].neighbors = append(cands[i].neighbors, h.Entry)
		}
		anyNeighbor = anyNeighbor || len(cands[i].neighbors) > 0
	}

	decisions := map[int]decision{}
	if anyNeighbor {
		var err error
		if decisions, err = s.decide(ctx, cands); err != nil {
			return Outcome{}, err
		}
	}

	var out Outcome
	// rewritten 记下已被前面的候选改写或合并的条目 → 它现在所在的条目（被并入的指向保留的那条）：
	// 版本已变，后面的候选不能再按旧版本改写；印证改记到它现在所在的条目。
	rewritten := map[string]string{}
	for i, c := range cands {
		d, ok := decisions[i]
		if !ok {
			d = decision{action: actionAdd}
		}
		if err := s.apply(ctx, tk, scope, c, d, rewritten, &out); err != nil {
			return out, err
		}
	}
	return out, nil
}

// decide 调 LLM 判定每条候选与已有条目的关系。不合法或缺失的判定按新增处理（记日志）。
func (s *Service) decide(ctx context.Context, cands []candidate) (map[int]decision, error) {
	prompt, refs := consolidationPrompt(cands)
	var payload struct {
		Decisions []rawDecision `json:"decisions"`
	}
	ok, err := s.complete(ctx, consolidateSystemPrompt, prompt, &payload)
	if err != nil || !ok {
		return map[int]decision{}, err
	}
	out, rejected := resolveDecisions(payload.Decisions, len(cands), refs)
	for _, r := range rejected {
		s.logger.Warn().Str("decision", r).Msg("整合判定不合法，该候选按新增处理")
	}
	return out, nil
}

// consolidationPrompt 写出整合判定的输入：候选编号为 C1、C2……，已有条目编号为 E1、E2……
// （同一条目被多个候选引用时共用一个编号）。返回编号到条目的映射。
func consolidationPrompt(cands []candidate) (string, map[string]corpus.Entry) {
	refs := map[string]corpus.Entry{}
	refOf := map[string]string{}
	var b strings.Builder
	for i, c := range cands {
		fmt.Fprintf(&b, "### C%d\n标题：%s\n正文：%s\n", i+1, c.lesson.Title, c.lesson.Content)
		if len(c.neighbors) == 0 {
			b.WriteString("最接近的已有条目：无\n\n")
			continue
		}
		b.WriteString("最接近的已有条目：\n")
		for _, e := range c.neighbors {
			ref, seen := refOf[e.ID]
			if !seen {
				ref = "E" + strconv.Itoa(len(refs)+1)
				refOf[e.ID], refs[ref] = ref, e
			}
			fmt.Fprintf(&b, "- %s（%s）标题：%s\n  正文：%s\n", ref, describe(e), e.Title, e.Content)
		}
		b.WriteString("\n")
	}
	return b.String(), refs
}

func describe(e corpus.Entry) string {
	src := "任务沉淀"
	if e.Source == corpus.SourceExpert {
		src = "人工录入"
	}
	scope := "通用"
	if e.Scenario != nil {
		scope = "场景 " + *e.Scenario
	}
	return fmt.Sprintf("来源：%s，适用范围：%s，%d 个任务印证", src, scope, e.Confirmations)
}

// rawDecision 是 LLM 输出的一条整合判定。
type rawDecision struct {
	Candidate string   `json:"candidate"`
	Action    string   `json:"action"`
	Targets   []string `json:"targets"`
	Title     string   `json:"title"`
	Content   string   `json:"content"`
}

// resolveDecisions 把判定解析为「候选下标 → 结论」。指向不存在的候选或已有条目、动作不合法、
// 目标数不符的判定不采纳（该候选按新增处理），以 rejected 返回供记日志。同一候选多条判定取第一条。
func resolveDecisions(raw []rawDecision, n int, refs map[string]corpus.Entry) (map[int]decision, []string) {
	out := map[int]decision{}
	var rejected []string
	for _, r := range raw {
		idx, err := strconv.Atoi(strings.TrimPrefix(strings.TrimSpace(r.Candidate), "C"))
		if err != nil || idx < 1 || idx > n {
			rejected = append(rejected, fmt.Sprintf("候选 %q 不存在", r.Candidate))
			continue
		}
		if _, dup := out[idx-1]; dup {
			continue
		}
		d := decision{action: strings.TrimSpace(r.Action), title: r.Title, content: r.Content}
		var want func(int) bool
		switch d.action {
		case actionAdd:
			want = func(k int) bool { return k == 0 }
		case actionConfirm, actionRevise:
			want = func(k int) bool { return k == 1 }
		case actionMerge:
			want = func(k int) bool { return k >= 2 }
		default:
			rejected = append(rejected, fmt.Sprintf("%s 的动作 %q 不合法", r.Candidate, r.Action))
			continue
		}
		targets, bad := resolveTargets(r.Targets, refs)
		if bad != "" || !want(len(targets)) {
			rejected = append(rejected, fmt.Sprintf("%s 的 %s 目标 %v 不合法%s", r.Candidate, d.action, r.Targets, bad))
			continue
		}
		d.targets = targets
		out[idx-1] = d
	}
	return out, rejected
}

// resolveTargets 把编号解析为条目（去重）；有编号不存在时 bad 写明。
func resolveTargets(raw []string, refs map[string]corpus.Entry) (targets []corpus.Entry, bad string) {
	seen := map[string]bool{}
	for _, r := range raw {
		e, ok := refs[strings.TrimSpace(r)]
		if !ok {
			return nil, fmt.Sprintf("（%q 不存在）", r)
		}
		if !seen[e.ID] {
			seen[e.ID] = true
			targets = append(targets, e)
		}
	}
	return targets, ""
}

// apply 落定一条候选的整合结论。改写（修订、合并）不成立——目标是人工内容、其间被改过、
// 已被本次前面的候选改写、或改写内容不合法——时改为新增：两者的内容并不相同，不能算作印证。
func (s *Service) apply(ctx context.Context, tk task.Task, scope corpus.Scope, c candidate, d decision, rewritten map[string]string, out *Outcome) error {
	if d.action == actionConfirm {
		id := d.targets[0].ID
		if now, ok := rewritten[id]; ok {
			id = now
		}
		if err := s.corpus.Confirm(ctx, id, tk.ID); err != nil {
			return err
		}
		out.Confirmed++
		return nil
	}
	if d.action == actionRevise || d.action == actionMerge {
		err := s.rewrite(ctx, tk, d, rewritten)
		if err == nil {
			for _, t := range d.targets {
				rewritten[t.ID] = d.targets[0].ID
			}
			if d.action == actionMerge {
				out.Merged += len(d.targets) - 1
			}
			out.Revised++
			return nil
		}
		if !errors.Is(err, corpus.ErrAuthored) && !errors.Is(err, corpus.ErrStale) &&
			!errors.Is(err, corpus.ErrInvalid) && !errors.Is(err, corpus.ErrDuplicate) {
			return err
		}
		s.logger.Info().Err(err).Str("action", d.action).Msg("改写不成立，候选改为新增")
	}
	return s.add(ctx, tk, scope, c.lesson, out)
}

func (s *Service) rewrite(ctx context.Context, tk task.Task, d decision, rewritten map[string]string) error {
	for _, t := range d.targets {
		if _, ok := rewritten[t.ID]; ok {
			return corpus.ErrStale
		}
	}
	if d.action == actionRevise {
		return s.corpus.Revise(ctx, d.targets[0], d.title, d.content, tk.ID)
	}
	return s.corpus.Merge(ctx, d.targets[0], d.targets[1:], d.title, d.content, tk.ID)
}

// add 写入一条新知识并记下本任务的印证；正文与已有条目一字不差时只记印证。
func (s *Service) add(ctx context.Context, tk task.Task, scope corpus.Scope, l Lesson, out *Outcome) error {
	draft := corpus.Draft{Title: l.Title, Content: l.Content, Source: corpus.SourceAgent}
	if l.Scoped {
		draft.Scenario = scope.Scenario
	}
	e, err := s.corpus.Create(ctx, draft)
	var dup *corpus.DuplicateError
	switch {
	case errors.As(err, &dup):
		if err := s.corpus.Confirm(ctx, dup.ExistingID, tk.ID); err != nil {
			return err
		}
		out.Confirmed++
		return nil
	case errors.Is(err, corpus.ErrInvalid):
		s.logger.Warn().Err(err).Str("title", l.Title).Msg("候选经验不合法，跳过")
		return nil
	case err != nil:
		return err
	}
	if err := s.corpus.Confirm(ctx, e.ID, tk.ID); err != nil {
		return err
	}
	out.Added++
	return nil
}
