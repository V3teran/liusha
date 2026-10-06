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
