package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/V3teran/liusha/internal/attackgraph"
)

type fakeAttackGraph struct {
	graph            attackgraph.Graph
	err              error
	milestones       []attackgraph.Milestone
	milestonesErr    error
	gotConv, gotTask string
}

func (f *fakeAttackGraph) Project(_ context.Context, convID, taskID string) (attackgraph.Graph, error) {
	f.gotConv, f.gotTask = convID, taskID
	if f.err != nil {
		return attackgraph.Graph{}, f.err
	}
	return f.graph, nil
}

func (f *fakeAttackGraph) ProjectMilestones(_ context.Context, convID, taskID string) ([]attackgraph.Milestone, error) {
	f.gotConv, f.gotTask = convID, taskID
	if f.milestonesErr != nil {
		return nil, f.milestonesErr
	}
	return f.milestones, nil
}

func TestAttackGraphHandler(t *testing.T) {
	t.Run("200 返回图并透传参数", func(t *testing.T) {
		fake := &fakeAttackGraph{graph: attackgraph.Graph{
			TaskID: "o1",
			Nodes:  []attackgraph.Node{{ID: "n1", Kind: attackgraph.KindFinding, Title: "SQLi"}},
		}}
		srv := newTestServer(t, Deps{AttackGraph: fake})
		defer srv.Close()

		req, _ := http.NewRequest("GET", srv.URL+"/attack_graph/o1?conv=c1", nil)
		req.Header.Set("X-API-Key", "k")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != 200 {
			t.Fatalf("状态码=%d，期望 200", resp.StatusCode)
		}
		var g attackgraph.Graph
		if err := json.NewDecoder(resp.Body).Decode(&g); err != nil {
			t.Fatal(err)
		}
		if g.TaskID != "o1" || len(g.Nodes) != 1 {
			t.Errorf("响应图不符：%+v", g)
		}
		if fake.gotConv != "c1" || fake.gotTask != "o1" {
			t.Errorf("参数透传不符：conv=%q task=%q", fake.gotConv, fake.gotTask)
		}
	})

	// 删除原「type 缺省 active_scan」用例：owner 多态坍缩为统一 task，路由不再有 type 参数与 owner 类型区分。

	t.Run("no rows 返回 404", func(t *testing.T) {
		fake := &fakeAttackGraph{err: errors.New("scan: no rows in result set")}
		srv := newTestServer(t, Deps{AttackGraph: fake})
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
		srv := newTestServer(t, Deps{AttackGraph: &fakeAttackGraph{}})
		defer srv.Close()

		req, _ := http.NewRequest("GET", srv.URL+"/attack_graph/o1", nil)
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

// TestAttackGraphMilestonesHandler 验证 sentinel error → HTTP 状态码的映射（errors.Is 判定，
// 不靠错误文案字符串匹配——即便 attackgraph 包改了错误文案，这里的状态码判定也不受影响）。
func TestAttackGraphMilestonesHandler(t *testing.T) {
	t.Run("200 返回里程碑列表", func(t *testing.T) {
		fake := &fakeAttackGraph{milestones: []attackgraph.Milestone{{Agent: "exploitation", Summary: "拿到 shell", NodeCount: 12}}}
		srv := newTestServer(t, Deps{AttackGraph: fake})
		defer srv.Close()

		req, _ := http.NewRequest("GET", srv.URL+"/attack_graph/o1/milestones?conv=c1", nil)
		req.Header.Set("X-API-Key", "k")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != 200 {
			t.Fatalf("状态码=%d，期望 200", resp.StatusCode)
		}
		var body struct {
			Milestones []attackgraph.Milestone `json:"milestones"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if len(body.Milestones) != 1 || body.Milestones[0].Agent != "exploitation" {
			t.Errorf("响应里程碑不符：%+v", body.Milestones)
		}
	})

	t.Run("未配置 LLM 返回 503", func(t *testing.T) {
		fake := &fakeAttackGraph{milestonesErr: attackgraph.ErrNoSummarizer}
		srv := newTestServer(t, Deps{AttackGraph: fake})
		defer srv.Close()

		req, _ := http.NewRequest("GET", srv.URL+"/attack_graph/o1/milestones", nil)
		req.Header.Set("X-API-Key", "k")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != 503 {
			t.Errorf("状态码=%d，期望 503", resp.StatusCode)
		}
	})

	t.Run("无绑定会话返回 400", func(t *testing.T) {
		fake := &fakeAttackGraph{milestonesErr: attackgraph.ErrNoConversation}
		srv := newTestServer(t, Deps{AttackGraph: fake})
		defer srv.Close()

		req, _ := http.NewRequest("GET", srv.URL+"/attack_graph/o1/milestones", nil)
		req.Header.Set("X-API-Key", "k")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != 400 {
			t.Errorf("状态码=%d，期望 400", resp.StatusCode)
		}
	})

	t.Run("其他错误返回 500", func(t *testing.T) {
		fake := &fakeAttackGraph{milestonesErr: errors.New("拉会话消息: 连接超时")}
		srv := newTestServer(t, Deps{AttackGraph: fake})
		defer srv.Close()

		req, _ := http.NewRequest("GET", srv.URL+"/attack_graph/o1/milestones", nil)
		req.Header.Set("X-API-Key", "k")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != 500 {
			t.Errorf("状态码=%d，期望 500", resp.StatusCode)
		}
	})
}
