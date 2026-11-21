// Package distill 实现任务收尾的经验沉淀：从一个自然收敛的任务过程里提炼跨任务经验，并与
// 知识库已有条目整合后写入。
//
// 经验的门槛：经验只能来自付出过代价的地方（证伪的主张、失败或被否决的动作、做完却没有任何
// 产出的动作），而且不是常识、去掉本目标特有的细节后仍然成立。没有吃过亏的任务不调用 LLM；
// 每条经验必须按编号引用至少一条弯路材料，引用不成立的由代码丢弃；每个任务至多 3 条。
//
// 两步完成：
//  1. 提炼：依据编号的材料与任务场景生成候选经验，并判定每条是否依赖场景的规定——依赖的只
//     对同一场景的任务可见，否则为通用。
//  2. 整合：为每条候选检索知识库里最接近的已有条目，判定是新增、印证、修订，还是把彼此重复的
//     已有条目合成一条。人工内容只能被印证。整合在知识库写锁下进行，并发的两次整合不会写出
//     重复知识。
//
// 被终止的任务不沉淀（过程性结论可能误导后续任务）。
package distill

import (
	"context"
	"fmt"

	"github.com/rs/zerolog"

	"github.com/V3teran/liusha/internal/corpus"
	"github.com/V3teran/liusha/internal/explorationgraph"
	fwllm "github.com/V3teran/liusha/internal/framework/llm"
	"github.com/V3teran/liusha/internal/task"
)

// TaskReader 读取任务行（取场景快照）。
type TaskReader interface {
	GetByID(ctx context.Context, id string) (task.Task, error)
}

// Service 是任务经验沉淀服务（runner 装配一次，任务自然收敛后由队列调用）。
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

// Outcome 是一次沉淀的结果。
type Outcome struct {
	Added     int
	Confirmed int
	Revised   int // 修订与合并后的条目数
	Merged    int // 被合并删除的条目数
}

// Total 是写入、印证或改写的知识条数。
func (o Outcome) Total() int { return o.Added + o.Confirmed + o.Revised }

// DistillTask 沉淀一个已自然收敛的任务。任务目标取图上的根目标（运行中调整过也以最终为准），
// 场景取任务行的快照。没有弯路材料时不调 LLM。LLM 调用挂审计归属（role=distill + taskID）。
func (s *Service) DistillTask(ctx context.Context, taskID string) (Outcome, error) {
	ctx = fwllm.WithCallMeta(ctx, fwllm.CallMeta{TaskID: taskID, Role: "distill"})
	materials := s.collect(ctx, taskID)
	if !hasDetour(materials) {
		s.logger.Info().Str("task_id", taskID).Msg("任务过程没有付出代价的地方，无可沉淀的经验")
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
		s.logger.Info().Str("task_id", taskID).Msg("没有过门槛的经验")
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
		Int("revised", out.Revised).Int("merged", out.Merged).Msg("任务经验沉淀完成")
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
