package einotools

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cloudwego/eino/components/tool"

	"github.com/V3teran/liusha/internal/skill"
)

// ---- skills ----

type fakeLoader struct {
	cards map[string]string // name → body
}

func (f *fakeLoader) List() []*skill.Card {
	out := make([]*skill.Card, 0, len(f.cards))
	for name := range f.cards {
		out = append(out, &skill.Card{Name: name})
	}
	return out
}
func (f *fakeLoader) Load(name string) (*skill.Card, error) {
	body, ok := f.cards[name]
	if !ok {
		return nil, errors.New("not found")
	}
	return &skill.Card{Name: name, Body: body}, nil
}

func TestReadVulnSkill(t *testing.T) {
	loader := &fakeLoader{cards: map[string]string{"bac": "BAC 挖掘指南正文"}}
	rv, err := BuildReadVulnSkill(loader)
	if err != nil {
		t.Fatal(err)
	}
	out := invoke(t, rv, `{"name":"bac"}`)
	if !strings.Contains(out, "BAC 挖掘指南正文") {
		t.Fatalf("read_vuln_skill 输出缺 body: %s", out)
	}
}

func TestReadVulnSkill_EnumInSchema(t *testing.T) {
	loader := &fakeLoader{cards: map[string]string{"bac": "x", "sqli": "y"}}
	rv, _ := BuildReadVulnSkill(loader)
	info, err := rv.Info(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	js, err := info.ParamsOneOf.ToJSONSchema()
	if err != nil {
		t.Fatal(err)
	}
	nameProp, ok := js.Properties.Get("name")
	if !ok {
		t.Fatal("schema 缺 name 字段")
	}
	// 动态 enum 应注入（bac + sqli）
	if len(nameProp.Enum) != 2 {
		t.Errorf("name enum 应有 2 项（动态注入），得到 %v", nameProp.Enum)
	}
}

func TestReadVulnSkill_UnknownNameErrors(t *testing.T) {
	loader := &fakeLoader{cards: map[string]string{"bac": "x"}}
	rv, _ := BuildReadVulnSkill(loader)
	it := rv.(tool.InvokableTool)
	_, err := it.InvokableRun(context.Background(), `{"name":"sqli"}`)
	if err == nil || !strings.Contains(err.Error(), "available") {
		t.Fatalf("未知 name 应报错带 available list，得到: %v", err)
	}
}

func TestReadToolingSkill(t *testing.T) {
	loader := &fakeLoader{cards: map[string]string{"sqlmap": "sqlmap 手册"}}
	rt, err := BuildReadToolingSkill(loader)
	if err != nil {
		t.Fatal(err)
	}
	out := invoke(t, rt, `{"name":"sqlmap"}`)
	if !strings.Contains(out, "sqlmap 手册") {
		t.Fatalf("read_tooling_skill 输出缺 body: %s", out)
	}
}

func TestBuildSkillReader_NilLoaderErrors(t *testing.T) {
	if _, err := BuildReadVulnSkill(nil); err == nil {
		t.Fatal("nil loader 应报错")
	}
}

// ---- replay_traffic ----

// fakeFlowReader 满足 TrafficReader（replay_traffic 用 GetInScope）。
// task 范围隔离收敛到适配器：ok=false 表越界（原 OwnerID 跨 owner 判定）。
type fakeFlowReader struct {
	flow TrafficRecord
	ok   bool
	err  error
}

func (f *fakeFlowReader) GetInScope(_ context.Context, _ int64) (TrafficRecord, bool, error) {
	return f.flow, f.ok, f.err
}

func TestReplayFlow_InheritsAndModifies(t *testing.T) {
	// httptest 目标：回显收到的 header + body，便于断言继承/覆盖
	var gotAuth, gotExtra, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotExtra = r.Header.Get("X-Extra")
		b := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(b)
		gotBody = string(b)
		w.WriteHeader(200)
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()

	store := &fakeFlowReader{ok: true, flow: TrafficRecord{
		ID:             5,
		Method:         "GET",
		URL:            srv.URL,
		RequestHeaders: json.RawMessage(`{"authorization":"Bearer orig"}`),
		RequestBody:    []byte("orig-body"),
	}}
	rf, err := BuildReplayTraffic(store)
	if err != nil {
		t.Fatal(err)
	}
	// 加一个 X-Extra header（其余继承 Authorization），不改 body
	out := invoke(t, rf, `{"id":5,"modifications":{"headers":{"X-Extra":"v1"}}}`)
	if !strings.Contains(out, `"status_code":200`) {
		t.Fatalf("replay 应返回 200: %s", out)
	}
	if gotAuth != "Bearer orig" {
		t.Errorf("应继承原 Authorization，得到 %q", gotAuth)
	}
	if gotExtra != "v1" {
		t.Errorf("应附加 X-Extra，得到 %q", gotExtra)
	}
	if gotBody != "orig-body" {
		t.Errorf("应继承原 body，得到 %q", gotBody)
	}
}

func TestReplayFlow_RejectsCrossTask(t *testing.T) {
	// 越界流量：适配器返回 ok=false（原 OwnerID 跨 owner 判定移到 GetInScope 内）。
	store := &fakeFlowReader{ok: false, flow: TrafficRecord{ID: 5, URL: "http://x"}}
	rf, _ := BuildReplayTraffic(store)
	it := rf.(tool.InvokableTool)
	_, err := it.InvokableRun(context.Background(), `{"id":5}`)
	if err == nil || !strings.Contains(err.Error(), "当前 task") {
		t.Fatalf("跨 task replay 应被拒，得到: %v", err)
	}
}

func TestReplayFlow_IDRequired(t *testing.T) {
	rf, _ := BuildReplayTraffic(&fakeFlowReader{})
	it := rf.(tool.InvokableTool)
	if _, err := it.InvokableRun(context.Background(), `{"id":0}`); err == nil {
		t.Fatal("id<=0 应报错")
	}
}
