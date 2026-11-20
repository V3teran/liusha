// Package distill 实现任务收尾的经验沉淀：把一个自然收敛的任务中「经过验证的结论」提炼成
// 跨任务经验，并与知识库已有条目整合后写入。
//
// 两步完成：
//  1. 提炼：依据已验证材料（已坐实的结果、已确认的事实、已证伪的假设）与任务场景生成候选
//     经验，并判定每条是否依赖场景的规定——依赖的只对同一场景的任务可见，否则为通用。
//  2. 整合：为每条候选检索知识库里最接近的已有条目，判定是新增、印证已有，还是修订已有。
//     人工内容只能被印证。整合在知识库写锁下进行，并发的两次整合不会写出重复知识。
//
// 被终止的任务不沉淀（过程性结论可能误导后续任务）；只取验证过的节点作材料，不做二次筛选。
package distill

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/rs/zerolog"

	"github.com/V3teran/liusha/internal/corpus"
	"github.com/V3teran/liusha/internal/explorationgraph"
	"github.com/V3teran/liusha/internal/framework/core"
	fwllm "github.com/V3teran/liusha/internal/framework/llm"
	"github.com/V3teran/liusha/internal/scenario"
	"github.com/V3teran/liusha/internal/task"
)

// TaskReader 读取任务行（取场景快照）。
type TaskReader interface {
	GetByID(ctx context.Context, id string) (task.Task, error)
}

// Service 是任务经验沉淀服务（runner 装配一次，任务自然收敛后调用）。
type Service struct {
	router     *fwllm.Router
	complexity fwllm.Complexity // 提炼与整合是分析综合，medium 足够
	graph      *explorationgraph.Store
	tasks      TaskReader
	corpus     *corpus.Service
	logger     zerolog.Logger
}

// New 构造经验沉淀服务。
func New(router *fwllm.Router, graph *explorationgraph.Store, tasks TaskReader, kb *corpus.Service, logger zerolog.Logger) *Service {
	return &Service{
		router:     router,
		complexity: fwllm.ComplexityMedium,
		graph:      graph,
		tasks:      tasks,
		corpus:     kb,
		logger:     logger.With().Str("component", "distill").Logger(),
	}
}

// 候选上限：材料不足时宁少勿滥，上限只是兜底。
const maxLessons = 8

// neighborLimit 是整合时每条候选带上的已有条目数。
const neighborLimit = 3

const lessonsSystemPrompt = `你是经验蒸馏官。把一个已结束任务中「经过验证的结论」蒸馏成以后的任务可复用的经验（lessons）。

要求：
- 只依据给出的材料，不编造；
- 经验写到方法层：什么情境、怎么做、有什么坑；去掉本任务特有的细节（具体地址、随机路径、一次性标识、时间）；
- 任务场景里已经写明的规定不必再沉淀；
- 判断每条经验是否依赖任务场景的规定（如什么算可交付、类别词表）：依赖的 scoped 为 true，只对同一场景的任务可见；不依赖的 scoped 为 false，对所有任务可见；没有任务场景时一律为 false；
- title 一句话（不超过 40 字），content 正文（不超过 300 字）；
- 至多 8 条；材料不足时宁少勿滥，可以为 0 条。

只输出 JSON，格式：
` + "```json\n" + `{"lessons": [{"title": "...", "content": "...", "scoped": false}]}
` + "```"

const consolidateSystemPrompt = `你是知识库整理员。一个任务沉淀出若干条候选经验（C1、C2……），每条附上知识库里与它最接近的已有条目（E1、E2……）。逐条判定候选与已有条目的关系：
- add：已有条目讲的都不是同一件事，候选作为新条目写入；
- confirm：某条已有条目已经讲了同一件事（措辞可以不同），候选只是再次印证它，不写入新条目；
- revise：某条已有条目讲的是同一件事，但候选让它更准确或更完整（补充适用条件、纠正错误）；给出改写后的完整标题与正文，保留已有条目里仍然成立的内容。只能改写来源为「任务沉淀」的条目，「人工录入」的条目只能 confirm 或另行 add。被多个任务印证过的条目，改写要更谨慎。

target 写已有条目的编号（add 时留空）；title 与 content 只在 revise 时填写，标题不超过 40 字、正文不超过 300 字。

只输出 JSON，格式：
` + "```json\n" + `{"decisions": [{"candidate": "C1", "action": "confirm", "target": "E2", "title": "", "content": ""}]}
` + "```"

// Lesson 是一条候选经验。
type Lesson struct {
	Title   string `json:"title"`
	Content string `json:"content"`
	Scoped  bool   `json:"scoped"` // 依赖任务场景的规定：只对同一场景的任务可见
}

// Outcome 是一次沉淀的结果。
type Outcome struct {
	Added     int
	Confirmed int
	Revised   int
}

// Total 是写入或印证的知识条数。
func (o Outcome) Total() int { return o.Added + o.Confirmed + o.Revised }

// DistillTask 沉淀一个已自然收敛的任务。任务目标取图上的根目标（运行中调整过也以最终为准），
// 场景取任务行的快照。没有已验证材料时不调 LLM。
// LLM 调用挂审计归属（role=distill + taskID）。
func (s *Service) DistillTask(ctx context.Context, taskID string) (Outcome, error) {
	ctx = fwllm.WithCallMeta(ctx, fwllm.CallMeta{TaskID: taskID, Role: "distill"})
	materials := s.collect(ctx, taskID)
	if len(materials) == 0 {
		s.logger.Info().Str("task_id", taskID).Msg("无已验证材料，跳过经验沉淀")
		return Outcome{}, nil
	}
	tk, err := s.tasks.GetByID(ctx, taskID)
	if err != nil {
		return Outcome{}, fmt.Errorf("读取任务: %w", err)
	}
	root, ok, err := s.graph.RootObjectiveOf(ctx, taskID)
	if err != nil {
		return Outcome{}, fmt.Errorf("读取任务目标: %w", err)
	}
	if !ok {
		return Outcome{}, fmt.Errorf("任务 %s 没有根目标", taskID)
	}

	lessons, err := s.generate(ctx, root.Goal(), tk.Scenario, materials)
	if err != nil {
		return Outcome{}, fmt.Errorf("提炼经验: %w", err)
	}
	if len(lessons) == 0 {
		return Outcome{}, nil
	}

	var out Outcome
	err = s.corpus.Exclusive(ctx, func(ctx context.Context) error {
		var err error
		out, err = s.consolidate(ctx, tk, lessons)
		return err
	})
	if err != nil {
		return out, err
	}
	s.logger.Info().Str("task_id", taskID).Int("added", out.Added).Int("confirmed", out.Confirmed).
		Int("revised", out.Revised).Msg("任务经验沉淀完成")
	return out, nil
}

// distillMaterialsMaxRunes 是材料总量钳制：持续探索的图只增不减，无界材料会撑爆模型窗口
// （本调用没有上下文压缩，入口限量是唯一防线）。
const distillMaterialsMaxRunes = 24000

// collect 汇集已验证材料：已坐实的结果、已确认的事实、已证伪的假设（含反驳原因）。
// 查询失败只告警并跳过对应桶——沉淀尽力而为，不因单个桶读失败放弃整体。
func (s *Service) collect(ctx context.Context, taskID string) []string {
	var results, facts, refuted []string

	if nodes, err := s.graph.ListNodesByKind(ctx, taskID, core.KindResult); err != nil {
		s.logger.Warn().Err(err).Msg("读取 result 节点失败（该桶缺席）")
	} else {
		for _, r := range nodes {
			var c struct {
				Statement string `json:"statement"`
			}
			if json.Unmarshal(r.Content, &c) == nil && strings.TrimSpace(c.Statement) != "" {
				results = append(results, "已确认发现："+c.Statement)
			}
		}
	}

	observations, err := s.graph.ListNodesByKind(ctx, taskID, core.KindObservation)
	if err != nil {
		s.logger.Warn().Err(err).Msg("读取 observation 节点失败（fact/refuted 两桶缺席）")
		return assembleMaterials(results, facts, refuted)
	}
	for _, obs := range observations {
		var c struct {
			Statement string `json:"statement"`
		}
		if json.Unmarshal(obs.Content, &c) != nil || strings.TrimSpace(c.Statement) == "" {
			continue
		}
		switch {
		case obs.IsFact():
			facts = append(facts, "已确认事实："+c.Statement)
		case obs.HasVerificationStatus(explorationgraph.VerificationRefuted):
			refuted = append(refuted, "已证伪假设："+c.Statement+"（原因："+obs.RefuteReason()+"）")
		}
	}
	return assembleMaterials(results, facts, refuted)
}

// assembleMaterials 分桶优先组装材料并按总量钳制：已确认发现优先，事实次之，已证伪最后；
// 桶内自最新回填（最新转写反映任务终态），预算耗尽后低优桶整体让位，未纳入条数以标记行
// 显式留痕。各桶入参为创建序（旧→新）。
func assembleMaterials(buckets ...[]string) []string {
	total, dropped := 0, 0
	full := false
	materials := make([]string, 0, 8)
	for _, bucket := range buckets {
		if full {
			dropped += len(bucket)
			continue
		}
		kept := make([]string, 0, len(bucket))
		for i := len(bucket) - 1; i >= 0; i-- {
			// 至少保留一条（整体首条无条件纳入）——空材料的钳制没有意义。
			if total+len(bucket[i]) > distillMaterialsMaxRunes && len(materials)+len(kept) > 0 {
				full = true
				dropped += i + 1
				break
			}
			total += len(bucket[i])
			kept = append(kept, bucket[i])
		}
		for i, j := 0, len(kept)-1; i < j; i, j = i+1, j-1 {
			kept[i], kept[j] = kept[j], kept[i] // 新→旧收集，返回时间正序
		}
		materials = append(materials, kept...)
	}
	if dropped > 0 {
		materials = append(materials, fmt.Sprintf("…（另有 %d 条更早的材料因体量上限未纳入）", dropped))
	}
	return materials
}

// generate 调 LLM 提炼候选经验。解析失败按无经验处理——沉淀是尽力而为的收尾，不为格式问题重试。
func (s *Service) generate(ctx context.Context, goal string, sc *task.Scenario, materials []string) ([]Lesson, error) {
	var user strings.Builder
	user.WriteString("【任务目标】\n" + goal + "\n\n【任务场景】\n")
	if sc == nil {
		user.WriteString("（无）")
	} else {
		user.WriteString(scenario.Render(sc.Name, sc.Body))
	}
	user.WriteString("\n\n【已验证材料】\n" + strings.Join(materials, "\n"))

	var payload struct {
		Lessons []Lesson `json:"lessons"`
	}
	if ok, err := s.complete(ctx, lessonsSystemPrompt, user.String(), &payload); err != nil || !ok {
		return nil, err
	}
	out := make([]Lesson, 0, len(payload.Lessons))
	for _, l := range payload.Lessons {
		l.Title, l.Content = strings.TrimSpace(l.Title), strings.TrimSpace(l.Content)
		if l.Title == "" || l.Content == "" {
			continue
		}
		if sc == nil {
			l.Scoped = false
		}
		out = append(out, l)
	}
	if len(out) > maxLessons {
		out = out[:maxLessons]
	}
	return out, nil
}

// complete 发一次 LLM 调用并把回复解析进 v。解析失败记日志并返回 ok=false。
func (s *Service) complete(ctx context.Context, system, user string, v any) (bool, error) {
	provider, err := s.router.For(ctx, s.complexity)
	if err != nil {
		return false, fmt.Errorf("provider: %w", err)
	}
	resp, err := provider.Complete(ctx, fwllm.Request{
		Messages: []fwllm.Message{
			{Role: fwllm.RoleSystem, Content: system},
			{Role: fwllm.RoleUser, Content: user},
		},
	})
	if err != nil {
		return false, fmt.Errorf("complete: %w", err)
	}
	if err := fwllm.ParseJSON(resp.Content, v); err != nil {
		s.logger.Warn().Err(err).Str("raw", resp.Content).Msg("经验沉淀的 LLM 输出解析失败（按无结果处理）")
		return false, nil
	}
	return true, nil
}

// candidate 是带着近似已有条目的一条候选。
type candidate struct {
	lesson    Lesson
	neighbors []corpus.Entry
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
	for i, c := range cands {
		d, ok := decisions[i]
		if !ok {
			d = decision{action: actionAdd}
		}
		if err := s.apply(ctx, tk, scope, c, d, &out); err != nil {
			return out, err
		}
	}
	return out, nil
}

const (
	actionAdd     = "add"
	actionConfirm = "confirm"
	actionRevise  = "revise"
)

// decision 是一条候选的整合结论（target 已从编号解析为条目）。
type decision struct {
	action  string
	target  corpus.Entry
	title   string
	content string
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

// rawDecision 是 LLM 输出的一条整合判定。
type rawDecision struct {
	Candidate string `json:"candidate"`
	Action    string `json:"action"`
	Target    string `json:"target"`
	Title     string `json:"title"`
	Content   string `json:"content"`
}

// resolveDecisions 把判定解析为「候选下标 → 结论」。指向不存在的候选或已有条目、动作不合法的
// 判定不采纳（该候选按新增处理），以 rejected 返回供记日志。同一候选多条判定取第一条。
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
		switch d.action {
		case actionAdd:
		case actionConfirm, actionRevise:
			target, found := refs[strings.TrimSpace(r.Target)]
			if !found {
				rejected = append(rejected, fmt.Sprintf("%s 指向不存在的已有条目 %q", r.Candidate, r.Target))
				continue
			}
			d.target = target
		default:
			rejected = append(rejected, fmt.Sprintf("%s 的动作 %q 不合法", r.Candidate, r.Action))
			continue
		}
		out[idx-1] = d
	}
	return out, rejected
}

func describe(e corpus.Entry) string {
	src := "任务沉淀"
	if e.Source == corpus.SourceExpert {
		src = "人工录入"
	}
	return fmt.Sprintf("来源：%s，%d 个任务印证", src, e.Confirmations)
}

// apply 落定一条候选的整合结论。修订遇到人工内容或其间被改过的条目时改为新增（不把这次
// 任务的结论算作对它的印证——两者的内容并不相同）。
func (s *Service) apply(ctx context.Context, tk task.Task, scope corpus.Scope, c candidate, d decision, out *Outcome) error {
	switch d.action {
	case actionConfirm:
		if err := s.corpus.Confirm(ctx, d.target.ID, tk.ID); err != nil {
			return err
		}
		out.Confirmed++
		return nil
	case actionRevise:
		err := s.corpus.Revise(ctx, d.target, d.title, d.content, tk.ID)
		if err == nil {
			out.Revised++
			return nil
		}
		if !errors.Is(err, corpus.ErrAuthored) && !errors.Is(err, corpus.ErrStale) && !errors.Is(err, corpus.ErrInvalid) {
			return err
		}
		s.logger.Info().Err(err).Str("entry_id", d.target.ID).Msg("修订不成立，候选改为新增")
	}
	return s.add(ctx, tk, scope, c.lesson, out)
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
