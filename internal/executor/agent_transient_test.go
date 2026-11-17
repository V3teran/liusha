package executor

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/V3teran/liusha/internal/bus"
	"github.com/V3teran/liusha/internal/explorationgraph"
	"github.com/V3teran/liusha/internal/framework/core"
	"github.com/V3teran/liusha/internal/framework/llm"
)

// fixedErrExecutor 不做实事，直接以给定错误结束——用于驱动 executeAction 的失败分类。
type fixedErrExecutor struct{ err error }

func (e fixedErrExecutor) Execute(context.Context, explorationgraph.Node) (Outcome, error) {
	return OutcomeDone, e.err
}

func newOpenAction(t *testing.T, store *explorationgraph.Store, id string) {
	t.Helper()
	open := explorationgraph.StateOpen
	_, err := store.CreateNode(context.Background(), explorationgraph.Node{
		ID: id, TaskID: "t1", Kind: core.KindAction, State: &open,
		SourceType: explorationgraph.SourcePlanner, Content: []byte(`{"instruction":"探测认证面"}`),
	})
	require.NoError(t, err)
}

func runExecuteAction(t *testing.T, store *explorationgraph.Store, execErr error) explorationgraph.Node {
	t.Helper()
	a := NewAgent(AgentConfig{
		TaskID: "t1", AgentRunID: "run-1", Graph: store,
		Executor: fixedErrExecutor{err: execErr},
		EventBus: bus.New(context.Background()), Logger: zerolog.Nop(),
	})
	_ = a.executeAction(context.Background(), mustNode(t, store, "a1"))
	return mustNode(t, store, "a1")
}

// 执行期遇 provider 瞬时故障（限流 / 过载 / 网络抖动）：动作交还 open 待重试，不记
// failed。记成失败会让规划者把可行方向读作「此路不通」弃掉——瞬时故障不是动作判决。
func TestExecuteAction_TransientLLMFailureReleasesToOpen(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
	}{
		{"流式中途限流", fmt.Errorf("ReAct 执行失败: LLM 调用失败: stream error: %w", &llm.HTTPError{Code: 429})},
		{"服务端过载", fmt.Errorf("ReAct 执行失败: LLM 调用失败: %w", &llm.HTTPError{Code: 503})},
		{"网络抖动", fmt.Errorf("ReAct 执行失败: LLM 调用失败: %w", errors.New("connection reset by peer"))},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := explorationgraph.NewMemoryStore()
			newOpenAction(t, store, "a1")
			n := runExecuteAction(t, store, tc.err)
			assert.Equal(t, explorationgraph.StateOpen, *n.State, "瞬时故障应交还 open 待重试，不记 failed")
			assert.Nil(t, n.StateReason, "交还不写失败原因")
		})
	}
}

// 真·执行失败（环境问题、非瞬时）仍记 failed：规划者据此知道这一步此路不通。
func TestExecuteAction_GenuineFailureMarksFailed(t *testing.T) {
	store := explorationgraph.NewMemoryStore()
	newOpenAction(t, store, "a1")
	n := runExecuteAction(t, store, errors.New("sandbox exec: command not found"))
	assert.Equal(t, explorationgraph.StateFailed, *n.State, "非瞬时失败应记 failed")
	require.NotNil(t, n.StateReason)
	assert.Contains(t, *n.StateReason, "command not found")
}

// LLM 配置错误（401/403/404）不是瞬时故障：不被交还分支吞掉，仍按失败结束并上抛，
// 由认知循环终结任务（重试不会自愈）。
func TestExecuteAction_ConfigErrorNotReleased(t *testing.T) {
	store := explorationgraph.NewMemoryStore()
	newOpenAction(t, store, "a1")
	n := runExecuteAction(t, store, fmt.Errorf("ReAct 执行失败: LLM 调用失败: %w", &llm.HTTPError{Code: 401}))
	assert.Equal(t, explorationgraph.StateFailed, *n.State, "配置错误不得被瞬时分支交还")
}
