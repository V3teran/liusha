package core

import "fmt"

// 定义框架的错误类型。

// ErrAgentTypeNotFound 表示 Agent 类型未注册。
type ErrAgentTypeNotFound struct {
	Type string
}

func (e ErrAgentTypeNotFound) Error() string {
	return fmt.Sprintf("agent type not found: %s", e.Type)
}

// ErrVersionConflict 表示乐观锁版本冲突。
type ErrVersionConflict struct {
	TaskID          string
	ExpectedVersion int64
	ActualVersion   int64
}

func (e ErrVersionConflict) Error() string {
	return fmt.Sprintf("version conflict for task %s: expected %d, actual %d", e.TaskID, e.ExpectedVersion, e.ActualVersion)
}

// ErrNodeNotFound 表示节点不存在。
type ErrNodeNotFound struct {
	NodeID string
}

func (e ErrNodeNotFound) Error() string {
	return fmt.Sprintf("node not found: %s", e.NodeID)
}

// ErrCheckpointNotFound 表示检查点不存在。
type ErrCheckpointNotFound struct {
	CheckpointID CheckpointID
}

func (e ErrCheckpointNotFound) Error() string {
	return fmt.Sprintf("checkpoint not found: %s", e.CheckpointID)
}

// ErrTaskNotFound 表示任务不存在。
type ErrTaskNotFound struct {
	TaskID string
}

func (e ErrTaskNotFound) Error() string {
	return fmt.Sprintf("task not found: %s", e.TaskID)
}

// ErrInvalidState 表示状态无效（不允许的状态转换）。
type ErrInvalidState struct {
	From string
	To   string
}

func (e ErrInvalidState) Error() string {
	return fmt.Sprintf("invalid state transition: %s -> %s", e.From, e.To)
}

// ErrTimeout 表示操作超时。
type ErrTimeout struct {
	Operation string
	Duration  int64 // 毫秒
}

func (e ErrTimeout) Error() string {
	return fmt.Sprintf("operation timeout: %s (%dms)", e.Operation, e.Duration)
}

// ErrDependencyNotSatisfied 表示依赖未满足。
type ErrDependencyNotSatisfied struct {
	NodeID       string
	Dependencies []string
}

func (e ErrDependencyNotSatisfied) Error() string {
	return fmt.Sprintf("dependencies not satisfied for node %s: %v", e.NodeID, e.Dependencies)
}

// ErrCyclicDependency 表示存在循环依赖。
type ErrCyclicDependency struct {
	Cycle []string
}

func (e ErrCyclicDependency) Error() string {
	return fmt.Sprintf("cyclic dependency detected: %v", e.Cycle)
}

// ErrHumanInputTimeout 表示人工输入超时。
type ErrHumanInputTimeout struct {
	RequestID string
	Timeout   int // 秒
}

func (e ErrHumanInputTimeout) Error() string {
	return fmt.Sprintf("human input timeout for request %s after %ds", e.RequestID, e.Timeout)
}

// ErrHumanInputRejected 表示人工输入被拒绝。
type ErrHumanInputRejected struct {
	RequestID string
	Reason    string
}

func (e ErrHumanInputRejected) Error() string {
	return fmt.Sprintf("human input rejected for request %s: %s", e.RequestID, e.Reason)
}
