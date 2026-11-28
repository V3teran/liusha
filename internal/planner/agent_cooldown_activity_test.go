package planner

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/V3teran/liusha/internal/framework/runtime"
)

// recordingActivity 记下登记到所属 run 存活心跳上的在途操作。
type recordingActivity struct {
	mu    sync.Mutex
	held  int
	until time.Time
}

func (r *recordingActivity) Busy(_ string, until time.Time) func() {
	r.mu.Lock()
	r.held++
	r.until = until
	r.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			r.mu.Lock()
			r.held--
			r.mu.Unlock()
		})
	}
}

func (r *recordingActivity) heldCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.held
}

func (r *recordingActivity) deadline() time.Time {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.until
}

// 规划失败后的冷却是有期限的等待，登记为所属 run 的在途操作：恢复监控只认存活心跳，冷却期里
// 既没有新的模型调用也没有在途的工具调用，不登记就与停摆无从区分——冷却封顶两分钟，与回收
// 阈值（缺省 120 秒）相当，一次限流后的退避就足以让活着的 run 被判停摆、白白重投。
func TestPlanFailure_CooldownIsRegisteredAsBoundedWait(t *testing.T) {
	act := &recordingActivity{}
	a := &Agent{logger: zerolog.Nop(), agentRunID: "run-1", activity: act}
	before := time.Now()

	require.NoError(t, a.planFailure(errors.New("图库连接中断"), "定期规划"))
	assert.Equal(t, 1, act.heldCount(), "冷却期应登记为在途操作")
	require.WithinDuration(t, before.Add(5*time.Second), act.deadline(), time.Second, "登记的期限就是冷却的终点")

	// 再失败一次：新的登记覆盖旧的，手里始终只有一份
	require.NoError(t, a.planFailure(errors.New("图库连接中断"), "定期规划"))
	assert.Equal(t, 1, act.heldCount())

	// 致命失败不进冷却（Run 带着错误退出）：登记撤销
	require.Error(t, a.planFailure(fmt.Errorf("plan: %w", runtime.ErrMessageTooLong), "定期规划"))
	assert.Equal(t, 0, act.heldCount())

	// 被取消的一轮不是失败：不登记
	require.NoError(t, a.planFailure(fmt.Errorf("plan: %w", context.Canceled), "定期规划"))
	assert.Equal(t, 0, act.heldCount())

	// 成功一次冷却清零：登记撤销
	require.NoError(t, a.planFailure(errors.New("图库连接中断"), "定期规划"))
	assert.Equal(t, 1, act.heldCount())
	require.NoError(t, a.planFailure(nil, "定期规划"))
	assert.Equal(t, 0, act.heldCount())
}

// Run 退出时撤销冷却的登记：run 都结束了，不该再为它续约。
func TestRun_ReleasesCooldownRegistrationOnExit(t *testing.T) {
	act := &recordingActivity{}
	a := runnableAgent(t, &countingPlanner{err: errors.New("图库连接中断")})
	a.planBackoffBase = time.Millisecond
	a.agentRunID, a.activity = "run-1", act
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	require.Error(t, a.Run(ctx))
	assert.Equal(t, 0, act.heldCount())
}
