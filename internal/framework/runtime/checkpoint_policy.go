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

func NewIterationCheckpointPolicy(interval int) *IterationCheckpointPolicy {
	return &IterationCheckpointPolicy{Interval: interval}
}

func (p *IterationCheckpointPolicy) ShouldSave(iteration int, trace *IterationTrace) bool {
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

func NewStateCheckpointPolicy(onSuccess, onError, onTool bool) *StateCheckpointPolicy {
	return &StateCheckpointPolicy{
		OnSuccess: onSuccess,
		OnError:   onError,
		OnTool:    onTool,
	}
}

func (p *StateCheckpointPolicy) ShouldSave(iteration int, trace *IterationTrace) bool {
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

func NewAlwaysCheckpointPolicy() *AlwaysCheckpointPolicy {
	return &AlwaysCheckpointPolicy{}
}

func (p *AlwaysCheckpointPolicy) ShouldSave(iteration int, trace *IterationTrace) bool {
	return true
}

// NeverCheckpointPolicy 从不保存（禁用检查点）
type NeverCheckpointPolicy struct{}

func NewNeverCheckpointPolicy() *NeverCheckpointPolicy {
	return &NeverCheckpointPolicy{}
}

func (p *NeverCheckpointPolicy) ShouldSave(iteration int, trace *IterationTrace) bool {
	return false
}
