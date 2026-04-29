package spawner

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/V3teran/liusha/internal/task"
	"github.com/V3teran/liusha/internal/worker"
)

// fakeTaskStore 是 TaskStore 的内存实现：覆盖 GetByID / Create / 两个 Count 用于
// 单元测试。所有调用都被记录到字段，便于断言。
type fakeTaskStore struct {
	// GetByID 返回的固定 task（按 id 索引）。
	tasks map[string]task.Task

	// Create 入参与产出。
	created   []task.NewParams
	nextID    int
	createErr error

	// CountInflightChildren 返回。
	childrenCount    int
	childrenCountErr error

	// CountInflightInEngagement 返回。
	engagementCount    int
	engagementCountErr error
}

func (f *fakeTaskStore) GetByID(_ context.Context, id string) (task.Task, error) {
	t, ok := f.tasks[id]
	if !ok {
		return task.Task{}, errors.New("not found")
	}
	return t, nil
}

func (f *fakeTaskStore) Create(_ context.Context, p task.NewParams) (string, error) {
	if f.createErr != nil {
		return "", f.createErr
	}
	f.created = append(f.created, p)
	f.nextID++
	id := "child-id-stub"
	if f.nextID > 1 {
		// 多次创建时附加序号，便于断言区分。
		id = id + "-" + string(rune('0'+f.nextID))
	}
	return id, nil
}

func (f *fakeTaskStore) CountInflightChildren(_ context.Context, _ string) (int, error) {
	return f.childrenCount, f.childrenCountErr
}

func (f *fakeTaskStore) CountInflightInEngagement(_ context.Context, _ string) (int, error) {
	return f.engagementCount, f.engagementCountErr
}

// fakeEnqueuer 是 Enqueuer 的内存实现，记录最后一次入参；可注入错误。
type fakeEnqueuer struct {
	calls []enqueueCall
	err   error
}

type enqueueCall struct {
	role    worker.Role
	payload worker.Payload
}

func (f *fakeEnqueuer) Enqueue(_ context.Context, role worker.Role, p worker.Payload) (string, string, error) {
	if f.err != nil {
		return "", "", f.err
	}
	f.calls = append(f.calls, enqueueCall{role: role, payload: p})
	return "asynq-" + p.TaskID, role.Queue(), nil
}

// 编译期接口断言：保证 *task.Store 与 *worker.Client 自动满足 Spawner 依赖的窄接口。
// 任何签名漂移都会在 build/test 阶段立即暴露。
var (
	_ TaskStore = (*task.Store)(nil)
	_ Enqueuer  = (*worker.Client)(nil)
)

// helper: 构造一个 parent 任务（无 parent_task_id）+ 已就位的 fakeTaskStore。
func newParentStore(parentID, eid, role string) *fakeTaskStore {
	return &fakeTaskStore{
		tasks: map[string]task.Task{
			parentID: {
				ID:           parentID,
				EngagementID: eid,
				ParentTaskID: nil,
				Role:         role,
			},
		},
	}
}

// 正常路径：父任务无 parent_task_id → 创建子任务 + Enqueue 成功，且 task ID 一致（幂等）。
func TestSpawner_DepthZero_OK(t *testing.T) {
	const (
		parentID = "p-1"
		eid      = "eng-1"
		role     = "sniffer"
	)
	ts := newParentStore(parentID, eid, role)
	enq := &fakeEnqueuer{}
	s := New(ts, enq, Limits{MaxChildrenPerParent: 10, MaxInflightPerEngagement: 20})

	id, err := s.Spawn(
		context.Background(),
		parentID,
		"vuln/web/bac",
		json.RawMessage(`{"flow_id":1}`),
		json.RawMessage(`{"max_steps":20}`),
	)
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	if id == "" {
		t.Fatal("childTaskID 不能为空")
	}

	// 断言 Create 被调用一次，参数继承父 role/engagement 且 parent_task_id 指向父。
	if len(ts.created) != 1 {
		t.Fatalf("Create 应调用 1 次，got %d", len(ts.created))
	}
	got := ts.created[0]
	if got.EngagementID != eid {
		t.Fatalf("EngagementID = %q, want %q", got.EngagementID, eid)
	}
	if got.Role != role {
		t.Fatalf("Role = %q, want %q (应继承父 role)", got.Role, role)
	}
	if got.ParentTaskID == nil || *got.ParentTaskID != parentID {
		t.Fatalf("ParentTaskID = %v, want %q", got.ParentTaskID, parentID)
	}
	if got.Skill != "vuln/web/bac" {
		t.Fatalf("Skill = %q, want vuln/web/bac", got.Skill)
	}

	// 断言 Enqueue 被调用一次，且 payload.TaskID == 创建的 child id（幂等关键）。
	if len(enq.calls) != 1 {
		t.Fatalf("Enqueue 应调用 1 次，got %d", len(enq.calls))
	}
	call := enq.calls[0]
	if call.role != worker.Role(role) {
		t.Fatalf("Enqueue role = %q, want %q", call.role, role)
	}
	if call.payload.TaskID != id {
		t.Fatalf("Enqueue payload.TaskID = %q, want %q（必须与 Create 返回的 id 一致）",
			call.payload.TaskID, id)
	}
	if call.payload.EngagementID != eid {
		t.Fatalf("Enqueue payload.EngagementID = %q, want %q", call.payload.EngagementID, eid)
	}
	if call.payload.Skill != "vuln/web/bac" {
		t.Fatalf("Enqueue payload.Skill = %q, want vuln/web/bac", call.payload.Skill)
	}
}

// 父任务自身已经是子任务（parent_task_id 非空）→ 拒绝（spawn depth >= 1）。
func TestSpawner_DepthOne_Rejected(t *testing.T) {
	const (
		grand    = "grand-1"
		parentID = "p-1"
		eid      = "eng-1"
	)
	ts := &fakeTaskStore{
		tasks: map[string]task.Task{
			parentID: {
				ID:           parentID,
				EngagementID: eid,
				ParentTaskID: ptr(grand),
				Role:         "sniffer",
			},
		},
	}
	enq := &fakeEnqueuer{}
	s := New(ts, enq, Limits{MaxChildrenPerParent: 10, MaxInflightPerEngagement: 20})

	_, err := s.Spawn(context.Background(), parentID, "vuln/web/bac", nil, nil)
	if err == nil {
		t.Fatal("depth>=1 应拒绝")
	}
	if !strings.Contains(err.Error(), "depth") {
		t.Fatalf("错误信息应提到 depth，got %q", err.Error())
	}
	// 拒绝路径不应触发 Create / Enqueue。
	if len(ts.created) != 0 {
		t.Fatalf("拒绝路径不应调用 Create，got %d 次", len(ts.created))
	}
	if len(enq.calls) != 0 {
		t.Fatalf("拒绝路径不应调用 Enqueue，got %d 次", len(enq.calls))
	}
}

// inflight children 已达 10 → 拒绝。
func TestSpawner_TooManyChildren(t *testing.T) {
	const parentID = "p-1"
	ts := newParentStore(parentID, "eng-1", "sniffer")
	ts.childrenCount = 10
	enq := &fakeEnqueuer{}
	s := New(ts, enq, Limits{MaxChildrenPerParent: 10, MaxInflightPerEngagement: 20})

	_, err := s.Spawn(context.Background(), parentID, "vuln/web/bac", nil, nil)
	if err == nil {
		t.Fatal("children>=10 应拒绝")
	}
	if !strings.Contains(err.Error(), "children") {
		t.Fatalf("错误信息应提到 children，got %q", err.Error())
	}
	if len(ts.created) != 0 {
		t.Fatalf("拒绝路径不应调用 Create")
	}
}

// inflight in engagement 已达 20 → 拒绝。
func TestSpawner_TooManyInEngagement(t *testing.T) {
	const parentID = "p-1"
	ts := newParentStore(parentID, "eng-1", "sniffer")
	ts.engagementCount = 20
	enq := &fakeEnqueuer{}
	s := New(ts, enq, Limits{MaxChildrenPerParent: 10, MaxInflightPerEngagement: 20})

	_, err := s.Spawn(context.Background(), parentID, "vuln/web/bac", nil, nil)
	if err == nil {
		t.Fatal("engagement inflight>=20 应拒绝")
	}
	if !strings.Contains(err.Error(), "engagement") {
		t.Fatalf("错误信息应提到 engagement，got %q", err.Error())
	}
	if len(ts.created) != 0 {
		t.Fatalf("拒绝路径不应调用 Create")
	}
}

// 父任务不存在 → 透传 GetByID 错误。
func TestSpawner_ParentNotFound(t *testing.T) {
	ts := &fakeTaskStore{tasks: map[string]task.Task{}}
	enq := &fakeEnqueuer{}
	s := New(ts, enq, Limits{})

	_, err := s.Spawn(context.Background(), "missing", "x", nil, nil)
	if err == nil {
		t.Fatal("父任务不存在应报错")
	}
}

// Enqueue 失败 → 错误回吐（注意：此时 task 已经写库，但本单测只关心错误透传，
// 不验证补偿逻辑——上层用 outbox/重试解决）。
func TestSpawner_EnqueueFails(t *testing.T) {
	const parentID = "p-1"
	ts := newParentStore(parentID, "eng-1", "sniffer")
	enq := &fakeEnqueuer{err: errors.New("redis down")}
	s := New(ts, enq, Limits{})

	_, err := s.Spawn(context.Background(), parentID, "vuln/web/bac", nil, nil)
	if err == nil {
		t.Fatal("Enqueue 失败应回吐 error")
	}
	if !strings.Contains(err.Error(), "enqueue") {
		t.Fatalf("错误信息应提到 enqueue，got %q", err.Error())
	}
}

// New 的零值默认：MaxChildrenPerParent=0 → 10；MaxInflightPerEngagement=0 → 20。
func TestSpawner_DefaultLimits(t *testing.T) {
	const parentID = "p-1"
	ts := newParentStore(parentID, "eng-1", "sniffer")
	// 9 < 10（默认），仍允许；如果默认值没生效，0 上限会立即拒绝。
	ts.childrenCount = 9
	enq := &fakeEnqueuer{}
	s := New(ts, enq, Limits{})

	if _, err := s.Spawn(context.Background(), parentID, "x", nil, nil); err != nil {
		t.Fatalf("默认上限下 9 个 inflight 子任务不应被拒绝: %v", err)
	}
}

// ptr 把 string 转为 *string，给 ParentTaskID 用。
func ptr(s string) *string { return &s }
