package evaluator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/V3teran/liusha/internal/bus"
	"github.com/V3teran/liusha/internal/explorationgraph"
	"github.com/V3teran/liusha/internal/framework/core"
)

func newTestAgent(t *testing.T) *Agent {
	t.Helper()
	return NewAgent(AgentConfig{TaskID: "t1", EventBus: bus.New(context.Background()), Graph: explorationgraph.NewMemoryStore(), Logger: zerolog.New(io.Discard)})
}

// 验证锁的年龄从验证真正开始时算起：排队等并发名额的时间不算——积压时排队可以远比一次验证
// 久，把排队算进去会让还在排队的假设被判成卡死而重复派发。
func TestVerifyLock_QueueTimeDoesNotExpire(t *testing.T) {
	a := newTestAgent(t)

	a.verifying["obs-q"] = time.Time{} // 已派发，还在排队
	assert.True(t, a.isVerifying("obs-q"))
	assert.False(t, a.tryAcquire("obs-q"), "排着队的假设不能再派一个工人")

	a.markRunning("obs-q")
	assert.True(t, a.isVerifying("obs-q"), "刚开跑的验证在途")
	assert.False(t, a.tryAcquire("obs-q"))

	a.verifying["obs-r"] = time.Now().Add(-a.lockTimeout) // 开跑已久、超过了租期：疑似卡死
	assert.False(t, a.isVerifying("obs-r"))
	assert.True(t, a.tryAcquire("obs-r"), "过期的锁强制释放后可以重新获取")
	assert.True(t, a.verifying["obs-r"].IsZero(), "重新获取后先是排队态")
}

// 锁的租期必须长于它守护的那次验证：两者相等时，验证刚好跑满时限就会被判成卡死。
func TestVerifyLock_OutlivesTheVerificationItGuards(t *testing.T) {
	a := newTestAgent(t)
	require.Greater(t, a.lockTimeout, verifyTimeout)
}

// newPendingAgent 建一个评估者和一条待验证假设。
func newPendingAgent(t *testing.T, id string) (*Agent, Claim) {
	t.Helper()
	a := newTestAgent(t)
	pending := explorationgraph.VerificationPending
	obs := explorationgraph.Node{
		ID: id, TaskID: "t1", Kind: core.KindObservation,
		Content:            json.RawMessage(`{"statement":"S","repro":{"kind":"steps","recipe":{"steps":"s"},"assert":{"description":"d"}}}`),
		VerificationStatus: &pending,
	}
	_, err := a.graph.CreateNode(context.Background(), obs)
	require.NoError(t, err)
	return a, Claim{TaskID: "t1", NodeID: id, Repro: obs.Content}
}

func (a *Agent) attempts(observationID string) int {
	a.verifyAttemptsMu.Lock()
	defer a.verifyAttemptsMu.Unlock()
	return a.verifyAttempts[observationID]
}

func (a *Agent) timeoutStreak(observationID string) int {
	a.verifyAttemptsMu.Lock()
	defer a.verifyAttemptsMu.Unlock()
	return a.verifyTimeouts[observationID]
}

var errVerifyTimeout = fmt.Errorf("verification timeout (will retry): %w", context.DeadlineExceeded)

// 连续超时有上限：头几次不计入失败次数，超过之后每一次都计入——一条假设反复跑到超时，不再
// 每十分钟白烧一轮、无限重试。
func TestRecordVerifyFailure_RepeatedTimeoutsCountAfterTheExemptions(t *testing.T) {
	a, claim := newPendingAgent(t, "obs-t")
	ctx := context.Background()

	for i := 0; i < maxVerifyTimeouts; i++ {
		a.recordVerifyFailure(ctx, "obs-t", claim, errVerifyTimeout)
	}
	assert.Zero(t, a.attempts("obs-t"), "头 %d 次超时不计入失败次数", maxVerifyTimeouts)

	for i := 1; i < maxVerifyAttempts; i++ {
		a.recordVerifyFailure(ctx, "obs-t", claim, errVerifyTimeout)
		assert.Equal(t, i, a.attempts("obs-t"), "超过豁免之后每次超时都计入")
	}
	a.recordVerifyFailure(ctx, "obs-t", claim, errVerifyTimeout)

	node, err := a.graph.GetNode(ctx, "obs-t")
	require.NoError(t, err)
	assert.True(t, node.HasVerificationStatus(explorationgraph.VerificationRefuted), "耗尽后出圈")
	assert.Equal(t, RefuteNotReproducible, node.RefuteReason(), "超时是没取到证据，不是否定主张")
}

// 连续超时只数「连续」的：这次验证不是因为超时结束的，计数清零。
func TestRecordVerifyFailure_TimeoutStreakResets(t *testing.T) {
	a, claim := newPendingAgent(t, "obs-t")
	ctx := context.Background()

	a.recordVerifyFailure(ctx, "obs-t", claim, errVerifyTimeout)
	a.recordVerifyFailure(ctx, "obs-t", claim, errVerifyTimeout)
	a.recordVerifyFailure(ctx, "obs-t", claim, errors.New("promote: 复放失败"))
	assert.Equal(t, 1, a.attempts("obs-t"))
	assert.Zero(t, a.timeoutStreak("obs-t"), "非超时的失败清掉连续超时")

	a.recordVerifyFailure(ctx, "obs-t", claim, errVerifyTimeout)
	assert.Equal(t, 1, a.attempts("obs-t"), "又从头豁免")

	a.clearTimeouts("obs-t")
	assert.Zero(t, a.timeoutStreak("obs-t"))
}
