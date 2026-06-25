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
	graph                      attackgraph.Graph
	err                        error
	milestones                 []attackgraph.Milestone
	milestonesErr              error
	gotConv, gotType, gotOwner string
}

func (f *fakeAttackGraph) Project(_ context.Context, convID, ownerType, ownerID string) (attackgraph.Graph, error) {
	f.gotConv, f.gotType, f.gotOwner = convID, ownerType, ownerID
	if f.err != nil {
		return attackgraph.Graph{}, f.err
	}
	return f.graph, nil
}

func (f *fakeAttackGraph) ProjectMilestones(_ context.Context, convID string) ([]attackgraph.Milestone, error) {
	f.gotConv = convID
	if f.milestonesErr != nil {
		return nil, f.milestonesErr
	}
	return f.milestones, nil
}

func TestAttackGraphHandler(t *testing.T) {
	t.Run("200 返回图并透传参数", func(t *testing.T) {
		fake := &fakeAttackGraph{graph: attackgraph.Graph{
			OwnerID: "o1",
			Nodes:   []attackgraph.Node{{ID: "n1", Kind: attackgraph.KindFinding, Title: "SQLi"}},
		}}
		srv := newTestServer(t, Deps{AttackGraph: fake})
		defer srv.Close()

		req, _ := http.NewRequest("GET", srv.URL+"/attack_graph/o1?conv=c1&type=active_scan", nil)
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
		if g.OwnerID != "o1" || len(g.Nodes) != 1 {
			t.Errorf("响应图不符：%+v", g)
		}
		if fake.gotConv != "c1" || fake.gotType != "active_scan" || fake.gotOwner != "o1" {
			t.Errorf("参数透传不符：conv=%q type=%q owner=%q", fake.gotConv, fake.gotType, fake.gotOwner)
		}
	})

	t.Run("type 缺省 active_scan", func(t *testing.T) {
		fake := &fakeAttackGraph{}
		srv := newTestServer(t, Deps{AttackGraph: fake})
		defer srv.Close()

		req, _ := http.NewRequest("GET", srv.URL+"/attack_graph/o1", nil)
		req.Header.Set("X-API-Key", "k")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()

		if fake.gotType != "active_scan" {
			t.Errorf("缺省 type=%q，期望 active_scan", fake.gotType)
		}
	})

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
