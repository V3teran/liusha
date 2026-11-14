package explorationgraph

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// 根目标每个任务只有一个，由 EnsureRootObjective 唯一创建：重复调用返回同一个节点。
func TestEnsureRootObjective_Idempotent(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()

	first, err := store.EnsureRootObjective(ctx, "t1", "对 http://target:8000 做 XSS 测试")
	require.NoError(t, err)
	second, err := store.EnsureRootObjective(ctx, "t1", "另一个简报")
	require.NoError(t, err)

	assert.Equal(t, first.ID, second.ID, "已有根目标时不再创建")
	assert.Equal(t, "对 http://target:8000 做 XSS 测试", second.Goal(), "已有根目标的目标不被改写")
	assert.Equal(t, SourceUser, first.SourceType)
	require.NotNil(t, first.State)
	assert.Equal(t, StateOpen, *first.State)
}

// 调整目标改写根目标的描述；根目标还不存在时（run 尚未开始）以新目标创建。
func TestSetGoal(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()

	require.NoError(t, store.SetGoal(ctx, "t1", "新目标"))
	root, err := store.EnsureRootObjective(ctx, "t1", "不会用到")
	require.NoError(t, err)
	assert.Equal(t, "新目标", root.Goal())

	require.NoError(t, store.SetGoal(ctx, "t1", "再调整"))
	again, err := store.EnsureRootObjective(ctx, "t1", "不会用到")
	require.NoError(t, err)
	assert.Equal(t, root.ID, again.ID)
	assert.Equal(t, "再调整", again.Goal())
}
