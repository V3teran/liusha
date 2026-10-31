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

func (s *slowProvider) Stream(_ context.Context, _ Request) (<-chan StreamEvent, error) {
	return nil, ErrStreamingUnsupported
}
func (s *slowProvider) ModelID() string    { return "slow" }
func (s *slowProvider) ProviderID() string { return "slow" }

// TestTimeoutProvider_Expires 验证超时预算生效：慢 provider 在预算内未完成即失败。
// dynamic 传 nil 时回退固定预算——与生产装配（动态读配置失败回退默认）同形态。
func TestTimeoutProvider_Expires(t *testing.T) {
	p := WithDynamicCallTimeout(&slowProvider{delay: 5 * time.Second}, 50*time.Millisecond, nil)

	_, err := p.Complete(context.Background(), Request{})
	require.Error(t, err)
	assert.True(t, errors.Is(err, context.DeadlineExceeded), "应为截止期超时，got %v", err)
}

// TestTimeoutProvider_WithinBudget 验证预算内完成不受影响。
func TestTimeoutProvider_WithinBudget(t *testing.T) {
	p := WithDynamicCallTimeout(&slowProvider{delay: 10 * time.Millisecond}, 5*time.Second, nil)

	resp, err := p.Complete(context.Background(), Request{})
	require.NoError(t, err)
	assert.Equal(t, "done", resp.Content)
}

// TestTimeoutProvider_DynamicWins 验证每次调用现读的超时覆盖默认预算。
func TestTimeoutProvider_DynamicWins(t *testing.T) {
	p := WithDynamicCallTimeout(&slowProvider{delay: 5 * time.Second}, time.Hour,
		func(context.Context) time.Duration { return 30 * time.Millisecond })

	_, err := p.Complete(context.Background(), Request{})
	require.Error(t, err)
	assert.True(t, errors.Is(err, context.DeadlineExceeded), "应为动态预算的截止期超时，got %v", err)
}
