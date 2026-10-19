package llm

import (
	"context"
	"errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

// slowProvider 的 Complete 睡固定时长（或直到 ctx 取消）。
type slowProvider struct{ delay time.Duration }

func (s *slowProvider) Complete(ctx context.Context, _ Request) (Response, error) {
	select {
	case <-time.After(s.delay):
		return Response{Content: "done"}, nil
	case <-ctx.Done():
		return Response{}, ctx.Err()
	}
}

func (s *slowProvider) CountTokens(_ context.Context, _ Request) (int, error) { return 0, nil }
func (s *slowProvider) ModelID() string                                       { return "slow" }
func (s *slowProvider) ProviderID() string                                    { return "slow" }

// TestWithCallTimeout_Expires 验证超时预算生效：慢 provider 在预算内未完成即失败。
func TestWithCallTimeout_Expires(t *testing.T) {
	p := WithCallTimeout(&slowProvider{delay: 5 * time.Second}, 50*time.Millisecond)

	_, err := p.Complete(context.Background(), Request{})
	require.Error(t, err)
	assert.True(t, errors.Is(err, context.DeadlineExceeded), "应为截止期超时，got %v", err)
}

// TestWithCallTimeout_WithinBudget 验证预算内完成不受影响。
func TestWithCallTimeout_WithinBudget(t *testing.T) {
	p := WithCallTimeout(&slowProvider{delay: 10 * time.Millisecond}, 5*time.Second)

	resp, err := p.Complete(context.Background(), Request{})
	require.NoError(t, err)
	assert.Equal(t, "done", resp.Content)
}

// TestWithCallTimeout_ZeroNoop 验证非正超时不装饰（透传原 provider）。
func TestWithCallTimeout_ZeroNoop(t *testing.T) {
	inner := &slowProvider{delay: time.Millisecond}
	assert.Same(t, inner, WithCallTimeout(inner, 0))
}
