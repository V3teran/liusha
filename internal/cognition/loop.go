// Package cognition 是认知引擎（L4）的顶层环：把 Planner（战略）、Executor（战术）、
// Verifier（晋升门）咬合成 plan→execute→promote→replan 的感知-行动循环。
//
// 分层：Planner 确定性看图定「下一步打哪」，Executor 用 LLM 在招法范围内定「怎么打」，
// 产出候选晋升，Verifier 复现坐实才沉淀回图。每步后重读图——晋升改变 frontier。
package cognition

import (
	"context"

	"github.com/V3teran/liusha/internal/planner"
	"github.com/V3teran/liusha/internal/verifier"
	"github.com/V3teran/liusha/internal/worldmodel"
)

// Planner 产出排序后的下一步招法（*planner.Planner 满足）。
type Planner interface {
	Plan(ctx context.Context, taskID string) ([]planner.Move, error)
}

// Executor 战术执行一条招法，产出待验证的候选晋升（不写图，提议权与裁决权分离）。
type Executor interface {
	Execute(ctx context.Context, m planner.Move) ([]verifier.Attempt, error)
}

// Promoter 是晋升门（*verifier.Verifier 满足）。
type Promoter interface {
	Promote(ctx context.Context, a verifier.Attempt) (*worldmodel.Node, error)
}

const defaultMaxSteps = 50

// Loop 是认知环。maxSteps 上限保证终止（防 Executor 屡不坐实导致 frontier 不收敛的空转）。
type Loop struct {
	planner  Planner
	executor Executor
	promoter Promoter
	maxSteps int
	eventBus *EventBus // 可选：用于发布验证通过事件
}

// New 构造认知环。maxSteps<=0 时用默认上限。
func New(p Planner, o Executor, v Promoter, maxSteps int) *Loop {
	if maxSteps <= 0 {
		maxSteps = defaultMaxSteps
	}
	return &Loop{planner: p, executor: o, promoter: v, maxSteps: maxSteps}
}

// WithEventBus 设置事件总线（可选，用于事件驱动的 Planner）
func (l *Loop) WithEventBus(bus *EventBus) *Loop {
	l.eventBus = bus
	return l
}
