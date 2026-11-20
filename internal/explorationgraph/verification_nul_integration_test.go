//go:build integration

package explorationgraph

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// 复现证据捕获的是工具与响应原文，二进制内容混入 NUL 是常态：验证审计（证据与
// 配方都是 jsonb 写入）不得因此整条被数据库拒绝——实测发生过 SQLSTATE 22P05，
// 一次坐实判定整条丢失。
func TestRecordVerification_NULEvidenceAccepted(t *testing.T) {
	if testing.Short() {
		t.Skip("跳过集成测试")
	}
	ctx := context.Background()
	pool := setupTestDB(t)
	defer pool.Close()
	store := NewStore(pool)

	const taskID = "7a11a11a-1111-4111-8111-111111111111"
	const nodeID = "7a11a11a-2222-4222-8222-222222222222"
	_, err := store.CreateNode(ctx, Node{
		ID: nodeID, TaskID: taskID, Kind: "observation", SourceType: SourceExecutor,
		Content: []byte(`{"category":"hypothesis","statement":"s"}`),
	})
	require.NoError(t, err)
	// 证据里的 NUL 用 \u0000 转义表达（编码器对字符串内 NUL 的产出形态）
	evidence := []byte("{\"body\":\"bin\\u0000ary\",\"nested\":{\"k\":\"x\\u0000y\"}}")
	if _, err := store.RecordVerification(ctx, Verification{
		ID: "7a11a11a-3333-4333-8333-333333333333", TaskID: taskID, NodeID: nodeID,
		Repro: evidence, Outcome: VerificationConfirmed, Evidence: evidence,
	}); err != nil {
		t.Fatalf("含 NUL 的证据应被清洗后接受: %v", err)
	}
	rows, err := store.ListVerificationsByTask(ctx, taskID, 10)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.NotContains(t, string(rows[0].Evidence), "\\u0000", "回读证据不应含 NUL 转义")
	require.Contains(t, string(rows[0].Evidence), "binary", "其余内容应保留")
}
