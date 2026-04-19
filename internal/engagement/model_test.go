package engagement_test

import (
	"testing"

	"github.com/V3teran/liusha/internal/engagement"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNew_SetsDefaults: 合法输入应返回状态为 active、version=1、非零 UUID、非零 started_at。
func TestNew_SetsDefaults(t *testing.T) {
	e, err := engagement.New(engagement.CreateInput{
		TenantID:  uuid.MustParse("11111111-1111-1111-1111-111111111111"),
		Mode:      engagement.ModeProxy,
		Scope:     map[string]any{"hosts": []string{"example.com"}},
		Objective: "Find RCE in upload flow",
	})

	require.NoError(t, err)
	assert.Equal(t, engagement.StatusActive, e.Status)
	assert.Equal(t, int64(1), e.Version)
	assert.NotEqual(t, uuid.Nil, e.ID)
	assert.False(t, e.StartedAt.IsZero())
}

// TestNew_RejectsUnknownMode: mode 只接受 proxy/browser/ctf，其它应返回包含 "mode" 的错误。
func TestNew_RejectsUnknownMode(t *testing.T) {
	_, err := engagement.New(engagement.CreateInput{
		TenantID: uuid.MustParse("11111111-1111-1111-1111-111111111111"),
		Mode:     engagement.Mode("wifi"),
		Scope:    map[string]any{"hosts": []string{"example.com"}},
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "mode")
}

// TestNew_RejectsEmptyScope: scope 必须非空；空 map 应返回包含 "scope" 的错误。
func TestNew_RejectsEmptyScope(t *testing.T) {
	_, err := engagement.New(engagement.CreateInput{
		TenantID: uuid.MustParse("11111111-1111-1111-1111-111111111111"),
		Mode:     engagement.ModeProxy,
		Scope:    map[string]any{},
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "scope")
}
