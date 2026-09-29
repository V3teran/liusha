package runtime

// CheckpointPolicy 定义何时保存检查点
type CheckpointPolicy interface {
	// ShouldSave 判断当前是否应保存检查点
	ShouldSave(iteration int, trace *IterationTrace) bool
}

// IterationCheckpointPolicy 每 N 次迭代保存一次
type IterationCheckpointPolicy struct {
	Interval int // 每 N 次迭代保存
}

// NewIterationCheckpointPolicy 按迭代间隔保存。
func NewIterationCheckpointPolicy(interval int) *IterationCheckpointPolicy {
	return &IterationCheckpointPolicy{Interval: interval}
}

// ShouldSave 实现策略接口：每 N 次迭代保存一次。
func (p *IterationCheckpointPolicy) ShouldSave(iteration int, _ *IterationTrace) bool {
	if p.Interval <= 0 {
		return false
	}
	return iteration > 0 && iteration%p.Interval == 0
}

// StateCheckpointPolicy 特定状态时保存
type StateCheckpointPolicy struct {
	OnSuccess bool // 迭代成功时保存
	OnError   bool // 出错时保存
	OnTool    bool // 工具调用后保存
}

// NewStateCheckpointPolicy 在指定节点保存。
func NewStateCheckpointPolicy(onSuccess, onError, onTool bool) *StateCheckpointPolicy {
	return &StateCheckpointPolicy{
		OnSuccess: onSuccess,
		OnError:   onError,
		OnTool:    onTool,
	}
}

// ShouldSave 实现策略接口：命中节点名才保存。
func (p *StateCheckpointPolicy) ShouldSave(_ int, trace *IterationTrace) bool {
	if p.OnSuccess && trace.Status == IterationStatusComplete {
		return true
	}
	if p.OnError && trace.Status == IterationStatusFailed {
		return true
	}
	if p.OnTool && len(trace.Actions) > 0 {
		return true
	}
	return false
}

// AlwaysCheckpointPolicy 每次迭代都保存（调试用）
type AlwaysCheckpointPolicy struct{}

// NewAlwaysCheckpointPolicy 每步都保存。
func NewAlwaysCheckpointPolicy() *AlwaysCheckpointPolicy {
	return &AlwaysCheckpointPolicy{}
}

// ShouldSave 实现策略接口：恒真。
func (p *AlwaysCheckpointPolicy) ShouldSave(_ int, _ *IterationTrace) bool {
	return true
}

// NeverCheckpointPolicy 从不保存（禁用检查点）
type NeverCheckpointPolicy struct{}

// NewNeverCheckpointPolicy 从不保存（显式禁用）。
func NewNeverCheckpointPolicy() *NeverCheckpointPolicy {
	return &NeverCheckpointPolicy{}
}

// ShouldSave 实现策略接口：恒假。
func (p *NeverCheckpointPolicy) ShouldSave(_ int, _ *IterationTrace) bool {
	return false
}
