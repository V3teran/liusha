//go:build integration

package dispatch

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/V3teran/liusha/internal/conversation"
	"github.com/V3teran/liusha/internal/convstream"
	"github.com/V3teran/liusha/internal/dbtest"
	"github.com/V3teran/liusha/internal/explorationgraph"
	"github.com/V3teran/liusha/internal/framework/core"
	"github.com/V3teran/liusha/internal/task"
)

type steeringFixture struct {
	svc    *Service
	graph  *explorationgraph.Store
	taskID string
	convID string
}

// newSteeringFixture 建一个进行中的任务（带会话）与贴近生产装配的 Service。
func newSteeringFixture(t *testing.T) steeringFixture {
	t.Helper()
	pool := dbtest.NewPgPool(t)
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	tasks := task.NewStore(pool)
	conversations := conversation.NewStore(pool)
	ctx := context.Background()

	tk, err := tasks.Create(ctx, task.NewParams{AssignmentID: dbtest.SeedAssignment(t, pool), Goal: "测试 http://a.test 的登录页"})
	require.NoError(t, err)
	conv, err := conversations.CreateConversation(ctx, "t", tk.ID)
	require.NoError(t, err)
	return steeringFixture{
		svc:    &Service{DB: pool, Tasks: tasks, Conversations: conversations, Publisher: convstream.NewPublisher(rdb)},
		graph:  explorationgraph.NewStore(pool),
		taskID: tk.ID,
		convID: conv.ID,
	}
}

func (f steeringFixture) action(t *testing.T, state explorationgraph.State, source explorationgraph.SourceType) string {
	t.Helper()
	id, err := f.graph.CreateNode(context.Background(), explorationgraph.Node{
		ID: uuid.NewString(), TaskID: f.taskID, Kind: core.KindAction, State: &state, SourceType: source,
		Priority: explorationgraph.PriorityMedium, Content: []byte(`{"instruction":"x"}`), CreatedAt: time.Now(),
	})
	require.NoError(t, err)
	return id
}

func (f steeringFixture) state(t *testing.T, id string) explorationgraph.State {
	t.Helper()
	n, err := f.graph.GetNode(context.Background(), id)
	require.NoError(t, err)
	return *n.State
}

// receipts 返回会话里的引导回执（op → 消息）。
func (f steeringFixture) receipts(t *testing.T) []conversation.Message {
	t.Helper()
	msgs, err := f.svc.Conversations.ListMessages(context.Background(), f.convID, 0, 100)
	require.NoError(t, err)
	var out []conversation.Message
	for _, m := range msgs {
		var md struct {
			Kind string `json:"kind"`
		}
		if json.Unmarshal(m.Metadata, &md) == nil && md.Kind == "steering" {
			out = append(out, m)
		}
	}
	return out
}

func metaOf(t *testing.T, m conversation.Message) map[string]any {
	t.Helper()
	var md map[string]any
	require.NoError(t, json.Unmarshal(m.Metadata, &md))
	return md
}

// 调整目标：任务行与根目标一同改写，未开始的规划动作撤回，用户指派与执行中的不动；
// 回执与变更同事务落库。内容不变什么都不做；已结束的任务拒绝。
func TestAdjustGoal(t *testing.T) {
	f := newSteeringFixture(t)
	ctx := context.Background()
	planned := f.action(t, explorationgraph.StateOpen, explorationgraph.SourcePlanner)
	assigned := f.action(t, explorationgraph.StateOpen, explorationgraph.SourceUser)
	running := f.action(t, explorationgraph.StateRunning, explorationgraph.SourcePlanner)

	require.NoError(t, f.svc.AdjustGoal(ctx, f.taskID, "改测 http://b.test 的注册页"))

	tk, err := f.svc.Tasks.GetByID(ctx, f.taskID)
	require.NoError(t, err)
	assert.Equal(t, "改测 http://b.test 的注册页", tk.Goal)
	assert.Equal(t, "b.test", tk.TargetHost)
	root, _, err := f.graph.RootObjectiveOf(ctx, f.taskID)
	require.NoError(t, err)
	assert.Equal(t, tk.Goal, root.Goal())
	assert.Equal(t, explorationgraph.StateAborted, f.state(t, planned))
	assert.Equal(t, explorationgraph.StateOpen, f.state(t, assigned))
	assert.Equal(t, explorationgraph.StateRunning, f.state(t, running))

	receipts := f.receipts(t)
	require.Len(t, receipts, 1)
	assert.Equal(t, tk.Goal, receipts[0].Content)
	md := metaOf(t, receipts[0])
	assert.Equal(t, conversation.SteeringGoalAdjusted, md["op"])
	assert.EqualValues(t, 1, md["withdrawn"])

	require.NoError(t, f.svc.AdjustGoal(ctx, f.taskID, tk.Goal))
	assert.Len(t, f.receipts(t), 1, "内容不变不写回执")

	require.NoError(t, f.svc.Tasks.Abort(ctx, f.taskID, "停止"))
	assert.ErrorIs(t, f.svc.AdjustGoal(ctx, f.taskID, "迟到的调整"), task.ErrNotActive)
	assert.Len(t, f.receipts(t), 1)
}

// 指派动作：根目标下新建 open 的用户动作，暂停中也可以（保持暂停）；回执带动作 ID。
func TestAssignAction(t *testing.T) {
	f := newSteeringFixture(t)
	ctx := context.Background()
	_, err := f.svc.Tasks.MarkPaused(ctx, f.taskID)
	require.NoError(t, err)

	id, err := f.svc.AssignAction(ctx, f.taskID, "复测登录接口的 SQL 注入", explorationgraph.PriorityHigh)
	require.NoError(t, err)

	node, err := f.graph.GetNode(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, explorationgraph.StateOpen, *node.State)
	assert.Equal(t, explorationgraph.SourceUser, node.SourceType)
	assert.Equal(t, explorationgraph.PriorityHigh, node.Priority)
	tk, err := f.svc.Tasks.GetByID(ctx, f.taskID)
	require.NoError(t, err)
	assert.True(t, tk.Paused, "暂停状态保持")
	receipts := f.receipts(t)
	require.Len(t, receipts, 1)
	assert.Equal(t, "复测登录接口的 SQL 注入", receipts[0].Content)
	assert.Equal(t, id, metaOf(t, receipts[0])["action_id"])

	require.NoError(t, f.svc.Tasks.Abort(ctx, f.taskID, "停止"))
	_, err = f.svc.AssignAction(ctx, f.taskID, "x", explorationgraph.PriorityHigh)
	assert.ErrorIs(t, err, task.ErrNotActive)
}

// 否决动作：待执行或执行中的动作落 rejected（否决方 user）；已结束 409、不存在 404。
func TestRejectAction(t *testing.T) {
	f := newSteeringFixture(t)
	ctx := context.Background()
	running := f.action(t, explorationgraph.StateRunning, explorationgraph.SourcePlanner)
	done := f.action(t, explorationgraph.StateCompleted, explorationgraph.SourcePlanner)

	require.NoError(t, f.svc.RejectAction(ctx, f.taskID, running, "超出授权范围"))
	node, err := f.graph.GetNode(ctx, running)
	require.NoError(t, err)
	assert.Equal(t, explorationgraph.StateRejected, *node.State)
	assert.Equal(t, explorationgraph.SourceUser, node.RejectedBy())
	receipts := f.receipts(t)
	require.Len(t, receipts, 1)
	assert.Equal(t, "超出授权范围", receipts[0].Content)

	assert.ErrorIs(t, f.svc.RejectAction(ctx, f.taskID, done, "x"), explorationgraph.ErrActionSettled)
	assert.ErrorIs(t, f.svc.RejectAction(ctx, f.taskID, uuid.NewString(), "x"), explorationgraph.ErrActionNotFound)
	assert.Len(t, f.receipts(t), 1, "被拒的操作不写回执")

	pending := f.action(t, explorationgraph.StateOpen, explorationgraph.SourcePlanner)
	require.NoError(t, f.svc.Tasks.Abort(ctx, f.taskID, "停止"))
	assert.ErrorIs(t, f.svc.RejectAction(ctx, f.taskID, pending, "x"), task.ErrNotActive)
}
