// Package distill 实现任务终态的经验蒸馏：把一个自然收敛任务中「经过复现门
// 验证的结论」（已确认发现 / 已确认事实 / 已证伪假设）经 LLM 蒸馏成跨任务
// 教训，写回 corpus（source=agent）。
//
// 这是长期记忆的写入闭环：此前 corpus 只有 expert 人工导入一条写路径，agent
// 收尾自动沉淀（SourceAgent 常量的注释承诺）一直缺位。材料质量靠构造保证——
// 只取复现门裁决过的节点，不做二次筛选；被中止任务不进本管道（过程性结论
// 可能误导后续任务）。
//
// 去重语义由 corpus.Add 的 content_hash 承接：重复教训命中即 hit_count++，
// 跨任务重复踩坑自然强化该条经验的优先级。
package distill

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/rs/zerolog"

	"github.com/V3teran/liusha/internal/corpus"
	"github.com/V3teran/liusha/internal/explorationgraph"
	"github.com/V3teran/liusha/internal/framework/core"
	fwllm "github.com/V3teran/liusha/internal/framework/llm"
)

// Service 是任务经验蒸馏服务（runner 装配一次，任务终态调用）。
type Service struct {
	router     *fwllm.Router
	complexity fwllm.Complexity // 教训生成的 LLM 档位（蒸馏是分析综合，medium 足够）
	graph      *explorationgraph.Store
	corpus     *corpus.Store
	embedder   corpus.Embedder // 可空：embedding 不可用时教训降级纯 sparse 检索
	logger     zerolog.Logger
}

// New 构造蒸馏服务。
func New(router *fwllm.Router, graph *explorationgraph.Store, store *corpus.Store, embedder corpus.Embedder, logger zerolog.Logger) *Service {
	return &Service{
		router:     router,
		complexity: fwllm.ComplexityMedium,
		graph:      graph,
		corpus:     store,
		embedder:   embedder,
		logger:     logger.With().Str("component", "distill").Logger(),
	}
}

const distillSystemPrompt = `你是渗透测试任务的经验蒸馏官。把一个已完成任务中「经过复现门验证的结论」蒸馏成可跨任务复用的教训（lessons）。

要求：
- 只依据给出的材料，禁止编造；
- 教训要抽象到方法层（什么情境 + 什么做法/什么坑），去掉本任务特有的临时细节（随机路径、一次性 ID、具体时间戳）；
- 每条 lesson：title 一句话（不超过 40 字），content 方法层正文（不超过 300 字），tags 1~3 个小写英文标签（如 sqli、auth-bypass、recon、waf-bypass、defense-evasion）；
- 最多 8 条；材料不足时宁少勿滥，可为 0 条。

只输出 JSON，格式：
` + "```json\n" + `{"lessons": [{"title": "...", "content": "...", "tags": ["..."]}]}
` + "```"

// Lesson 是一条跨任务教训（corpus 行的前身）。
type Lesson struct {
	Title   string   `json:"title"`
	Content string   `json:"content"`
	Tags    []string `json:"tags"`
}

type lessonsPayload struct {
	Lessons []Lesson `json:"lessons"`
}

// DistillTask 蒸馏一个已完成任务并写回 corpus，返回写入的教训数。
// 无已验证材料时返回 0（不调 LLM）。写入逐条 best-effort：单条失败不阻断其余。
// LLM 调用挂审计归属（role=distill + taskID）——后台蒸馏与在线调用同一审计面。
func (s *Service) DistillTask(ctx context.Context, taskID, brief string) (int, error) {
	ctx = fwllm.WithCallMeta(ctx, fwllm.CallMeta{TaskID: taskID, Role: "distill"})
	materials := s.collect(ctx, taskID)
	if len(materials) == 0 {
		s.logger.Info().Str("task_id", taskID).Msg("无已验证材料，跳过经验蒸馏")
		return 0, nil
	}

	lessons, err := s.generate(ctx, brief, materials)
	if err != nil {
		return 0, fmt.Errorf("generate lessons: %w", err)
	}
	if len(lessons) == 0 {
		return 0, nil
	}

	// 批量产出 passage 向量（embedding 可用时），逐条写 corpus。
	vectors, vecErr := s.embedPassages(ctx, lessons)
	if vecErr != nil {
		s.logger.Warn().Err(vecErr).Msg("教训 embedding 失败，降级纯 sparse 写入")
		vectors = nil
	}

	written := 0
	for i, l := range lessons {
		var emb []float32
		if vectors != nil && i < len(vectors) {
			emb = vectors[i]
		}
		if _, err := s.corpus.Add(ctx, corpus.Entry{
			Title:        l.Title,
			Content:      l.Content,
			Tags:         l.Tags,
			Source:       corpus.SourceAgent,
			SourceTaskID: taskID,
			Embedding:    emb,
		}); err != nil {
			s.logger.Warn().Err(err).Int("index", i).Str("title", l.Title).Msg("教训写入 corpus 失败")
			continue
		}
		written++
	}
	s.logger.Info().Str("task_id", taskID).Int("written", written).Int("total", len(lessons)).
		Msg("任务经验蒸馏完成")
	return written, nil
}

// distillMaterialsMaxRunes 是蒸馏材料面的总量钳制：持续探索模式的图只增
// 不减，材料随任务时长线性增长——无界材料会把蒸馏调用撑爆模型窗口（本调用
// 无修改链，入口限量是唯一防线）。
const distillMaterialsMaxRunes = 24000

// collect 汇集已验证材料：已确认发现（result）、已确认事实（fact 观察）、
// 已证伪假设（含反驳原因）。查询失败只告警并跳过对应桶——蒸馏尽力而为，
// 不因单个桶的读失败放弃整体。产出经 assembleMaterials 分桶优先限量。
func (s *Service) collect(ctx context.Context, taskID string) []string {
	var results, facts, refuted []string

	if nodes, err := s.graph.ListNodesByKind(ctx, taskID, core.KindResult); err != nil {
		s.logger.Warn().Err(err).Msg("读取 result 节点失败（该桶缺席）")
	} else {
		for _, r := range nodes {
			var c struct {
				Summary string `json:"summary"`
			}
			if json.Unmarshal(r.Content, &c) == nil && strings.TrimSpace(c.Summary) != "" {
				results = append(results, "已确认发现："+c.Summary)
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
		if obs.Confidence != nil && *obs.Confidence == explorationgraph.ConfidenceFactual {
			facts = append(facts, "已确认事实："+c.Statement)
			continue
		}
		if obs.Metadata == nil {
			continue
		}
		var m struct {
			VerificationOutcome string `json:"verification_outcome"`
			RefuteReason        string `json:"refute_reason"`
		}
		if json.Unmarshal(obs.Metadata, &m) == nil && m.VerificationOutcome == string(explorationgraph.OutcomeRefuted) {
			refuted = append(refuted, "已证伪假设："+c.Statement+"（原因："+m.RefuteReason+"）")
		}
	}
	return assembleMaterials(results, facts, refuted)
}

// assembleMaterials 分桶优先组装材料并按总量钳制：results（已确认发现，
// 数量最少价值最高）全保优先，facts 次之，refuted 最后；桶内自最新回填
// （最新转写反映任务终态），预算耗尽后低优桶整体让位，未纳入条数以标记行
// 显式留痕（不静默）。各桶入参为创建序（旧→新）。
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
			kept[i], kept[j] = kept[j], kept[i] // 新→旧收集，反回时间正序
		}
		materials = append(materials, kept...)
	}
	if dropped > 0 {
		materials = append(materials, fmt.Sprintf("…（另有 %d 条更早的材料因体量上限未纳入蒸馏）", dropped))
	}
	return materials
}

// generate 调 LLM 产出教训。解析失败（含修复层后）按无教训处理——蒸馏是
// best-effort 交付尾工，不值得为格式问题重试。
func (s *Service) generate(ctx context.Context, brief string, materials []string) ([]Lesson, error) {
	provider, err := s.router.For(ctx, s.complexity)
	if err != nil {
		return nil, fmt.Errorf("provider: %w", err)
	}
	user := "【任务简报】\n" + brief + "\n\n【已验证材料】\n" + strings.Join(materials, "\n")
	resp, err := provider.Complete(ctx, fwllm.Request{
		Messages: []fwllm.Message{
			{Role: fwllm.RoleSystem, Content: distillSystemPrompt},
			{Role: fwllm.RoleUser, Content: user},
		},
		MaxTokens: 2000,
	})
	if err != nil {
		return nil, fmt.Errorf("complete: %w", err)
	}
	var payload lessonsPayload
	if err := fwllm.ParseJSON(resp.Content, &payload); err != nil {
		s.logger.Warn().Err(err).Str("raw", resp.Content).Msg("教训 JSON 解析失败（按无教训处理）")
		return nil, nil
	}
	// 材料层面二次过滤：空 title/content 的残缺条目丢弃
	out := make([]Lesson, 0, len(payload.Lessons))
	for _, l := range payload.Lessons {
		if strings.TrimSpace(l.Title) == "" || strings.TrimSpace(l.Content) == "" {
			continue
		}
		if l.Tags == nil {
			l.Tags = []string{}
		}
		out = append(out, l)
	}
	if len(out) > 8 {
		out = out[:8]
	}
	return out, nil
}

// embedPassages 批量产出教训向量（embedder 为 nil 时返回 nil, nil）。
func (s *Service) embedPassages(ctx context.Context, lessons []Lesson) ([][]float32, error) {
	if s.embedder == nil || len(lessons) == 0 {
		return nil, nil
	}
	texts := make([]string, len(lessons))
	for i, l := range lessons {
		texts[i] = l.Title + "\n" + l.Content
	}
	return s.embedder.EmbedPassage(ctx, texts)
}
