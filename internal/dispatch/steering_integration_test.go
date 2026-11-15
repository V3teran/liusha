//go:build integration

package dispatch

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/V3teran/liusha/internal/agentrun"
	"github.com/V3teran/liusha/internal/conversation"
	"github.com/V3teran/liusha/internal/convstream"
	"github.com/V3teran/liusha/internal/dbtest"
	"github.com/V3teran/liusha/internal/explorationgraph"
	"github.com/V3teran/liusha/internal/framework/core"
	"github.com/V3teran/liusha/internal/task"
	"github.com/V3teran/liusha/internal/worker"
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

	_, err = f.svc.Tasks.Abort(ctx, f.taskID, "停止")
	require.NoError(t, err)
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

	_, err = f.svc.Tasks.Abort(ctx, f.taskID, "停止")
	require.NoError(t, err)
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
	_, err = f.svc.Tasks.Abort(ctx, f.taskID, "停止")
	require.NoError(t, err)
	assert.ErrorIs(t, f.svc.RejectAction(ctx, f.taskID, pending, "x"), task.ErrNotActive)
}

// withRunner 给 fixture 装上入队依赖（重开任务要起新 run）。
func (f steeringFixture) withRunner(t *testing.T) steeringFixture {
	t.Helper()
	mr := miniredis.RunT(t)
	enq := worker.NewClient(asynq.RedisClientOpt{Addr: mr.Addr()})
	t.Cleanup(func() { _ = enq.Close() })
	f.svc.Enq = enq
	f.svc.AgentRuns = agentrun.NewStore(f.svc.DB)
	f.svc.MaxRunTimeout = time.Minute
	return f
}

func (f steeringFixture) liveRuns(t *testing.T) int {
	t.Helper()
	var n int
	require.NoError(t, f.svc.DB.QueryRow(context.Background(),
		`SELECT count(*) FROM agent_run WHERE task_id = $1 AND status IN ('pending', 'running')`, f.taskID).Scan(&n))
	return n
}

// 对话里要执行的消息追加到任务目标：进行中（含暂停中）原地追加、保持暂停、不另起 run；
// 已结束的任务在同一事务里重开并追加，再起新一轮 run（续接的指令不再丢失）。
func TestAppendRequirement(t *testing.T) {
	ctx := context.Background()

	t.Run("进行中原地追加", func(t *testing.T) {
		f := newSteeringFixture(t).withRunner(t)
		planned := f.action(t, explorationgraph.StateOpen, explorationgraph.SourcePlanner)
		_, err := f.svc.Tasks.MarkPaused(ctx, f.taskID)
		require.NoError(t, err)

		require.NoError(t, f.svc.appendRequirement(ctx, f.taskID, "再测一下注册页", nil, 0))

		tk, err := f.svc.Tasks.GetByID(ctx, f.taskID)
		require.NoError(t, err)
		assert.Equal(t, "测试 http://a.test 的登录页\n\n补充要求：再测一下注册页", tk.Goal)
		assert.True(t, tk.Paused, "暂停状态保持")
		assert.Equal(t, explorationgraph.StateAborted, f.state(t, planned), "按旧目标排出的规划撤回")
		assert.Zero(t, f.liveRuns(t), "进行中的任务不另起 run")
		receipts := f.receipts(t)
		require.Len(t, receipts, 1)
		assert.Equal(t, "补充要求：再测一下注册页", receipts[0].Content, "回执是实际写入的内容")
		assert.Equal(t, conversation.SteeringGoalAppended, metaOf(t, receipts[0])["op"])
	})

	t.Run("已结束则重开并起新 run", func(t *testing.T) {
		f := newSteeringFixture(t).withRunner(t)
		require.NoError(t, f.svc.Tasks.Complete(ctx, f.taskID))

		require.NoError(t, f.svc.appendRequirement(ctx, f.taskID, "继续测越权", nil, 0))

		tk, err := f.svc.Tasks.GetByID(ctx, f.taskID)
		require.NoError(t, err)
		assert.Equal(t, task.StatusActive, tk.Status)
		assert.Contains(t, tk.Goal, "补充要求：继续测越权")
		root, _, err := f.graph.RootObjectiveOf(ctx, f.taskID)
		require.NoError(t, err)
		assert.Equal(t, explorationgraph.StateOpen, *root.State)
		assert.Equal(t, tk.Goal, root.Goal())
		assert.Equal(t, 1, f.liveRuns(t), "重开后起新一轮 run")
	})

	t.Run("超长被拒且什么都不改", func(t *testing.T) {
		f := newSteeringFixture(t).withRunner(t)
		require.NoError(t, f.svc.Tasks.Complete(ctx, f.taskID))
		err := f.svc.appendRequirement(ctx, f.taskID, strings.Repeat("测", task.MaxGoalRunes), nil, 0)
		require.ErrorIs(t, err, errGoalTooLong)
		tk, err := f.svc.Tasks.GetByID(ctx, f.taskID)
		require.NoError(t, err)
		assert.Equal(t, task.StatusCompleted, tk.Status)
		assert.Zero(t, f.liveRuns(t))
	})
}

// 重复停止（连点、或与恢复监控判死竞争）幂等，时间线只落一个「已停止」节点。
func TestTerminateTask_RecordsStopOnce(t *testing.T) {
	f := newSteeringFixture(t)
	ctx := context.Background()
	require.NoError(t, f.svc.TerminateTask(ctx, f.taskID))
	require.NoError(t, f.svc.TerminateTask(ctx, f.taskID))

	msgs, err := f.svc.Conversations.ListMessages(ctx, f.convID, 0, 100)
	require.NoError(t, err)
	stops := 0
	for _, m := range msgs {
		var md struct {
			Kind   string `json:"kind"`
			Status string `json:"status"`
		}
		if json.Unmarshal(m.Metadata, &md) == nil && md.Kind == "task_status" && md.Status == conversation.TaskStatusAborted {
			stops++
		}
	}
	assert.Equal(t, 1, stops)
}
