package core

import (
	"time"
)

// LoopType 循环类型
type LoopType string

const (
	// LoopTypeWhile 条件为真时继续循环
	LoopTypeWhile LoopType = "while"

	// LoopTypeUntil 条件为假时继续循环（条件为真时退出）
	LoopTypeUntil LoopType = "until"
)

// LoopConfig 循环配置
type LoopConfig struct {
	// 最大迭代次数（防止无限循环，必填）
	MaxIterations int `json:"max_iterations"`

	// 退出条件函数（返回 true 表示应该检查退出）
	// 对于 LoopTypeWhile: 返回 false 时退出
	// 对于 LoopTypeUntil: 返回 true 时退出
	BreakCondition func(state any) bool `json:"-"`

	// 超时时间（0 表示不限制）
	Timeout time.Duration `json:"timeout"`

	// 循环类型
	LoopType LoopType `json:"loop_type"`

	// 循环节点列表（循环体包含的节点 ID）
	LoopNodes []string `json:"loop_nodes"`
}

// LoopState 循环执行状态
type LoopState struct {
	// 当前迭代次数
	CurrentIteration int `json:"current_iteration"`

	// 循环开始时间
	StartTime time.Time `json:"start_time"`

	// 历史轨迹（已执行的迭代记录）
	History []LoopIteration `json:"history"`

	// 是否应该退出
	ShouldBreak bool `json:"should_break"`

	// 退出原因
	BreakReason string `json:"break_reason"`
}

// LoopIteration 单次迭代记录
type LoopIteration struct {
	// 迭代序号
	Iteration int `json:"iteration"`

	// 执行的节点 ID
	NodeID string `json:"node_id"`

	// 执行时间戳（Unix 毫秒）
	Timestamp int64 `json:"timestamp"`

	// 节点输出状态快照（可选）
	State any `json:"state,omitempty"`
}

// Validate 验证循环配置的完整性
func (c *LoopConfig) Validate() error {
	if c.MaxIterations <= 0 {
		return ErrInvalidConfig{Field: "MaxIterations", Reason: "must be greater than 0"}
	}

	if c.LoopType != LoopTypeWhile && c.LoopType != LoopTypeUntil {
		return ErrInvalidConfig{Field: "LoopType", Reason: "must be 'while' or 'until'"}
	}

	if len(c.LoopNodes) == 0 {
		return ErrInvalidConfig{Field: "LoopNodes", Reason: "must contain at least one node"}
	}

	return nil
}

// HasExitCondition 判断是否有有效的退出条件
func (c *LoopConfig) HasExitCondition() bool {
	return c.BreakCondition != nil || c.Timeout > 0 || c.MaxIterations > 0
}

// ShouldExit 判断是否应该退出循环
func (s *LoopState) ShouldExit(config *LoopConfig, currentState any) (bool, string) {
	// 检查最大迭代次数
	if s.CurrentIteration >= config.MaxIterations {
		return true, "max_iterations_reached"
	}

	// 检查超时
	if config.Timeout > 0 && time.Since(s.StartTime) > config.Timeout {
		return true, "timeout"
	}

	// 检查退出条件
	if config.BreakCondition != nil {
		conditionMet := config.BreakCondition(currentState)

		switch config.LoopType {
		case LoopTypeUntil:
			// until: 条件为真时退出
			if conditionMet {
				return true, "condition_met"
			}
		case LoopTypeWhile:
			// while: 条件为假时退出
			if !conditionMet {
				return true, "condition_not_met"
			}
		}
	}

	return false, ""
}

// NewLoopState 创建新的循环状态
func NewLoopState() *LoopState {
	return &LoopState{
		CurrentIteration: 0,
		StartTime:        time.Now(),
		History:          make([]LoopIteration, 0),
		ShouldBreak:      false,
		BreakReason:      "",
	}
}
