package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/V3teran/liusha/internal/task"
	"github.com/V3teran/liusha/internal/worldmodel"
)

// fakeWorldModel 是 WorldModelAPI 的内存实现，记录收到的 taskID 以验证 task→assignment 解析。
type fakeWorldModel struct {
	nodes      []worldmodel.Node
	edges      []worldmodel.Edge
	vers       []worldmodel.Verification
	err        error
	gotTaskID  string
}

func (f *fakeWorldModel) ListNodes(_ context.Context, taskID string) ([]worldmodel.Node, error) {
	f.gotTaskID = taskID
	return f.nodes, f.err
}
func (f *fakeWorldModel) ListEdges(_ context.Context, taskID string) ([]worldmodel.Edge, error) {
	return f.edges, f.err
}
func (f *fakeWorldModel) ListVerifications(_ context.Context, taskID string) ([]worldmodel.Verification, error) {
	return f.vers, f.err
}

// fakeTaskScan 是 TaskScanResolver 的内存实现。
type fakeTaskScan struct {
	task task.Task
	err  error
}

func (f *fakeTaskScan) GetByID(_ context.Context, _ string) (task.Task, error) {
	return f.task, f.err
}

func TestAttackGraphHandler(t *testing.T) {
	verifiedBy := "v1"

	t.Run("200 返回图并按 task 解析出 task_id", func(t *testing.T) {
		wm := &fakeWorldModel{
			nodes: []worldmodel.Node{{
				ID: "n1", Seq: 1, Kind: worldmodel.KindFinding,
				Ref:        worldmodel.TargetRef{Domain: "web", RefKind: "endpoint", Locator: "/login"},
				Attrs:      json.RawMessage(`{"summary":"SQLi"}`),
				Confidence: worldmodel.ConfConfirmed,
				VerifiedBy: &verifiedBy,
			}},
			edges: []worldmodel.Edge{{ID: "e1", Rel: worldmodel.RelOn, Src: "n1", Dst: "n2"}},
			vers:  []worldmodel.Verification{{ID: "v1", LeadID: "l1", Outcome: worldmodel.OutcomeConfirmed}},
		}
		resolver := &fakeTaskScan{task: task.Task{ID: "t1", AssignmentID: "a1"}}
		srv := newTestServer(t, Deps{WorldModel: wm, TaskScan: resolver})
		defer srv.Close()

		req, _ := http.NewRequest("GET", srv.URL+"/attack_graph/t1", nil)
		req.Header.Set("X-API-Key", "k")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != 200 {
			t.Fatalf("状态码=%d，期望 200", resp.StatusCode)
		}
		var body attackGraphResponse
		if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body.TaskID != "a1" || body.TaskID != "t1" {
			t.Errorf("task_id/task_id 不符：%+v", body)
		}
		if wm.gotTaskID != "a1" {
			t.Errorf("应按 task→assignment 解析出 task_id=a1，实得 %q", wm.gotTaskID)
		}
		if len(body.Nodes) != 1 || body.Nodes[0].Kind != worldmodel.KindFinding {
			t.Errorf("节点不符：%+v", body.Nodes)
		}
		// Edge Src/Dst → source/target 单点转换。
		if len(body.Edges) != 1 || body.Edges[0].Source != "n1" || body.Edges[0].Target != "n2" {
			t.Errorf("边 source/target 映射不符：%+v", body.Edges)
		}
		if len(body.Verifications) != 1 || body.Verifications[0].Outcome != worldmodel.OutcomeConfirmed {
			t.Errorf("取证链不符：%+v", body.Verifications)
		}
	})

	t.Run("空图归一成空数组（不回传 null）", func(t *testing.T) {
		// Go 序列化 nil 切片成 JSON null，前端对 nodes/edges 直接迭代会崩。验证响应为 []。
		wm := &fakeWorldModel{}
		resolver := &fakeTaskScan{task: task.Task{ID: "t1", AssignmentID: "a1"}}
		srv := newTestServer(t, Deps{WorldModel: wm, TaskScan: resolver})
		defer srv.Close()

		req, _ := http.NewRequest("GET", srv.URL+"/attack_graph/t1", nil)
		req.Header.Set("X-API-Key", "k")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()

		var raw map[string]any
		if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
			t.Fatal(err)
		}
		for _, k := range []string{"nodes", "edges", "verifications"} {
			arr, ok := raw[k].([]any)
			if !ok || len(arr) != 0 {
				t.Errorf("%s 应为空数组而非 null：%+v", k, raw[k])
			}
		}
	})

	t.Run("task 不存在返回 404", func(t *testing.T) {
		resolver := &fakeTaskScan{err: pgx.ErrNoRows}
		srv := newTestServer(t, Deps{WorldModel: &fakeWorldModel{}, TaskScan: resolver})
		defer srv.Close()

		req, _ := http.NewRequest("GET", srv.URL+"/attack_graph/missing", nil)
		req.Header.Set("X-API-Key", "k")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != 404 {
			t.Errorf("状态码=%d，期望 404", resp.StatusCode)
		}
	})

	t.Run("错误 API key 401", func(t *testing.T) {
		srv := newTestServer(t, Deps{WorldModel: &fakeWorldModel{}, TaskScan: &fakeTaskScan{}})
		defer srv.Close()

		req, _ := http.NewRequest("GET", srv.URL+"/attack_graph/t1", nil)
		req.Header.Set("X-API-Key", "wrong")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != 401 {
			t.Errorf("状态码=%d，期望 401", resp.StatusCode)
		}
	})
}
