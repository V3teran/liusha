package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/V3teran/liusha/internal/httpreplay"
)

// fakeTraffic 是内存 TrafficSource：按 id 返回预置的源流量。
type fakeTraffic struct {
	rec map[int64]httpreplay.Source
}

func (f fakeTraffic) GetInScope(_ context.Context, id int64) (httpreplay.Source, bool, error) {
	s, ok := f.rec[id]
	return s, ok, nil
}

func intp(i int) *int { return &i }

// newServer 起一个回显服务：/admin 返 200+"secret data"，其余 403+"forbidden"。
func newServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/admin" {
			w.Header().Set("X-Flag", "leaked")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("secret data for admin"))
			return
		}
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte("forbidden"))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func recipeJSON(t *testing.T, r ReplayRecipe) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestReplay_Confirmed(t *testing.T) {
	srv := newServer(t)
	tr := fakeTraffic{rec: map[int64]httpreplay.Source{
		1: {ID: 1, Method: "GET", URL: srv.URL + "/admin"},
	}}
	r := NewReplayer(tr)

	prims := recipeJSON(t, ReplayRecipe{
		TrafficID: 1,
		Assert: Assertion{
			StatusCode:     intp(200),
			BodyContains:   []string{"secret data"},
			HeaderContains: map[string]string{"X-Flag": "leaked"},
		},
	})
	res, err := r.Replay(context.Background(), prims)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Confirmed {
		t.Fatalf("期望坐实，got refuted；evidence=%s", res.Evidence)
	}
	if res.DurationMs < 0 {
		t.Errorf("DurationMs 应 >= 0")
	}
}

func TestReplay_RefutedByStatus(t *testing.T) {
	srv := newServer(t)
	tr := fakeTraffic{rec: map[int64]httpreplay.Source{
		1: {ID: 1, Method: "GET", URL: srv.URL + "/denied"},
	}}
	r := NewReplayer(tr)

	prims := recipeJSON(t, ReplayRecipe{
		TrafficID: 1,
		Assert:    Assertion{StatusCode: intp(200)}, // 实际 403 → 证伪
	})
	res, err := r.Replay(context.Background(), prims)
	if err != nil {
		t.Fatal(err)
	}
	if res.Confirmed {
		t.Fatal("期望证伪（403 != 200），got 坐实")
	}
	// 证伪也要有证据（供审计）。
	if len(res.Evidence) == 0 {
		t.Error("证伪也应带 evidence")
	}
}

func TestReplay_BodyAbsent_AuthBypass(t *testing.T) {
	srv := newServer(t)
	// 删 cookie 后仍拿到 admin 数据、body 不含 "login" → 认证绕过坐实。
	tr := fakeTraffic{rec: map[int64]httpreplay.Source{
		1: {ID: 1, Method: "GET", URL: srv.URL + "/admin"},
	}}
	r := NewReplayer(tr)

	prims := recipeJSON(t, ReplayRecipe{
		TrafficID: 1,
		Assert: Assertion{
			StatusCode: intp(200),
			BodyAbsent: []string{"login", "sign in"},
		},
	})
	res, err := r.Replay(context.Background(), prims)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Confirmed {
		t.Fatalf("期望坐实（无跳登录）；evidence=%s", res.Evidence)
	}
}

func TestReplay_EmptyAssert_Rejected(t *testing.T) {
	tr := fakeTraffic{rec: map[int64]httpreplay.Source{1: {ID: 1, URL: "http://x/"}}}
	r := NewReplayer(tr)

	prims := recipeJSON(t, ReplayRecipe{TrafficID: 1, Assert: Assertion{}})
	_, err := r.Replay(context.Background(), prims)
	if err == nil {
		t.Fatal("空断言应报错（拒绝橡皮图章），got nil")
	}
}

func TestReplay_TrafficNotFound(t *testing.T) {
	tr := fakeTraffic{rec: map[int64]httpreplay.Source{}}
	r := NewReplayer(tr)

	prims := recipeJSON(t, ReplayRecipe{TrafficID: 99, Assert: Assertion{StatusCode: intp(200)}})
	_, err := r.Replay(context.Background(), prims)
	if err == nil {
		t.Fatal("越界/不存在流量应报错")
	}
}

func TestReplay_BadTrafficID(t *testing.T) {
	r := NewReplayer(fakeTraffic{})
	prims := recipeJSON(t, ReplayRecipe{TrafficID: 0, Assert: Assertion{StatusCode: intp(200)}})
	if _, err := r.Replay(context.Background(), prims); err == nil {
		t.Fatal("traffic_id<=0 应报错")
	}
}

func TestReplay_MalformedRecipe(t *testing.T) {
	r := NewReplayer(fakeTraffic{})
	if _, err := r.Replay(context.Background(), json.RawMessage(`{not json`)); err == nil {
		t.Fatal("坏 JSON 配方应报错")
	}
}

func TestReplay_NilTrafficSource(t *testing.T) {
	r := NewReplayer(nil)
	prims := recipeJSON(t, ReplayRecipe{TrafficID: 1, Assert: Assertion{StatusCode: intp(200)}})
	if _, err := r.Replay(context.Background(), prims); err == nil {
		t.Fatal("nil TrafficSource 应报错")
	}
}

func strp(s string) *string { return &s }

// idServer 模拟"服务器现造值"：POST /create 回 {"id":"<随机>"}，
// GET /item?id=<那个 id> 才返 200+"item body"，其余 id 返 404。
func idServer(t *testing.T, freshID string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/create":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id":"` + freshID + `","status":"ok"}`))
		case "/item":
			if r.URL.Query().Get("id") == freshID {
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte("item body for " + freshID))
				return
			}
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte("not found"))
		default:
			w.WriteHeader(http.StatusForbidden)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// opaque-id 链：源流量里的 id 已失效，须先 POST /create 抽新 id，注入主请求 query 再 GET /item。
func TestReplay_Resolve_OpaqueID(t *testing.T) {
	srv := idServer(t, "fresh-42")
	tr := fakeTraffic{rec: map[int64]httpreplay.Source{
		1: {ID: 1, Method: "POST", URL: srv.URL + "/create"},
		2: {ID: 2, Method: "GET", URL: srv.URL + "/item?id=STALE"}, // 录制时的旧 id 已失效
	}}
	r := NewReplayer(tr)

	prims := recipeJSON(t, ReplayRecipe{
		Resolve: &ResolveStep{
			TrafficID: 1,
			Extract:   Extractor{Name: "newid", Source: "body", JSON: "id"},
		},
		TrafficID: 2,
		Modifications: httpreplay.Mods{
			Query: map[string]*string{"id": strp("{{newid}}")},
		},
		Assert: Assertion{
			StatusCode:   intp(200),
			BodyContains: []string{"item body for fresh-42"},
		},
	})
	res, err := r.Replay(context.Background(), prims)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Confirmed {
		t.Fatalf("期望坐实（新鲜 id 注入后 GET 命中）；evidence=%s", res.Evidence)
	}
}

// 护栏2：抽值未命中 = 硬错误，绝不静默 pass。
func TestReplay_Resolve_ExtractMiss_HardError(t *testing.T) {
	srv := idServer(t, "fresh-7")
	tr := fakeTraffic{rec: map[int64]httpreplay.Source{
		1: {ID: 1, Method: "POST", URL: srv.URL + "/create"},
		2: {ID: 2, Method: "GET", URL: srv.URL + "/item"},
	}}
	r := NewReplayer(tr)

	prims := recipeJSON(t, ReplayRecipe{
		Resolve: &ResolveStep{
			TrafficID: 1,
			Extract:   Extractor{Name: "newid", Source: "body", JSON: "nonexistent"}, // 抽不到
		},
		TrafficID:     2,
		Modifications: httpreplay.Mods{Query: map[string]*string{"id": strp("{{newid}}")}},
		Assert:        Assertion{StatusCode: intp(200)},
	})
	if _, err := r.Replay(context.Background(), prims); err == nil {
		t.Fatal("抽值未命中应硬错误，got nil（静默 pass 即橡皮图章）")
	}
}

// tokenServer 模拟"服务器现造 token"：GET /prepare 下发 Set-Cookie session + X-CSRF-Token 头，
// POST /act 须同时带对的 cookie 与 header 才 200，否则 401。
func tokenServer(t *testing.T, session, csrf string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/prepare":
			w.Header().Set("Set-Cookie", "session="+session+"; Expires=Wed, 21 Oct 2099 07:28:00 GMT; Path=/; HttpOnly")
			w.Header().Set("X-CSRF-Token", csrf)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("prepared"))
		case "/act":
			cookieOK := false
			if c, err := r.Cookie("session"); err == nil && c.Value == session {
				cookieOK = true
			}
			if cookieOK && r.Header.Get("X-CSRF-Token") == csrf {
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte("action done"))
				return
			}
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte("unauthorized"))
		default:
			w.WriteHeader(http.StatusForbidden)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// set-cookie 抽值：准备请求拿新 session cookie，注入主请求 Cookie 头。
func TestReplay_Resolve_SetCookieExtract(t *testing.T) {
	srv := tokenServer(t, "fresh-abc", "csrf-xyz")
	tr := fakeTraffic{rec: map[int64]httpreplay.Source{
		1: {ID: 1, Method: "GET", URL: srv.URL + "/prepare"},
		2: {ID: 2, Method: "POST", URL: srv.URL + "/act"},
	}}
	r := NewReplayer(tr)

	prims := recipeJSON(t, ReplayRecipe{
		Resolve: &ResolveStep{
			TrafficID: 1,
			Extract:   Extractor{Name: "sess", Source: "set-cookie:session"},
		},
		TrafficID: 2,
		Modifications: httpreplay.Mods{
			Headers: map[string]*string{"Cookie": strp("session={{sess}}"), "X-CSRF-Token": strp("csrf-xyz")},
		},
		Assert: Assertion{StatusCode: intp(200), BodyContains: []string{"action done"}},
	})
	res, err := r.Replay(context.Background(), prims)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Confirmed {
		t.Fatalf("期望坐实（set-cookie 抽 session 注入）；evidence=%s", res.Evidence)
	}
}

// header 抽值：准备请求响应头拿 X-CSRF-Token，注入主请求头。
func TestReplay_Resolve_HeaderExtract(t *testing.T) {
	srv := tokenServer(t, "fresh-abc", "csrf-xyz")
	tr := fakeTraffic{rec: map[int64]httpreplay.Source{
		1: {ID: 1, Method: "GET", URL: srv.URL + "/prepare"},
		2: {ID: 2, Method: "POST", URL: srv.URL + "/act"},
	}}
	r := NewReplayer(tr)

	prims := recipeJSON(t, ReplayRecipe{
		Resolve: &ResolveStep{
			TrafficID: 1,
			Extract:   Extractor{Name: "csrf", Source: "header:x-csrf-token"},
		},
		TrafficID: 2,
		Modifications: httpreplay.Mods{
			Headers: map[string]*string{"Cookie": strp("session=fresh-abc"), "X-CSRF-Token": strp("{{csrf}}")},
		},
		Assert: Assertion{StatusCode: intp(200), BodyContains: []string{"action done"}},
	})
	res, err := r.Replay(context.Background(), prims)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Confirmed {
		t.Fatalf("期望坐实（header 抽 csrf 注入）；evidence=%s", res.Evidence)
	}
}

// 护栏1自洽性 + LOW 修复：regex 与 json 同设即互斥报错，不静默偏袒。
func TestReplay_Resolve_RegexJSONMutex(t *testing.T) {
	srv := idServer(t, "x")
	tr := fakeTraffic{rec: map[int64]httpreplay.Source{
		1: {ID: 1, Method: "POST", URL: srv.URL + "/create"},
		2: {ID: 2, Method: "GET", URL: srv.URL + "/item"},
	}}
	r := NewReplayer(tr)

	prims := recipeJSON(t, ReplayRecipe{
		Resolve: &ResolveStep{
			TrafficID: 1,
			Extract:   Extractor{Name: "v", Source: "body", Regex: `"id":"([^"]+)"`, JSON: "id"},
		},
		TrafficID:     2,
		Modifications: httpreplay.Mods{Query: map[string]*string{"id": strp("{{v}}")}},
		Assert:        Assertion{StatusCode: intp(200)},
	})
	if _, err := r.Replay(context.Background(), prims); err == nil {
		t.Fatal("regex 与 json 同设应报错（互斥），got nil")
	}
}

// extractor 支持 regex 抽 body。
func TestReplay_Resolve_RegexExtract(t *testing.T) {
	srv := idServer(t, "abc123")
	tr := fakeTraffic{rec: map[int64]httpreplay.Source{
		1: {ID: 1, Method: "POST", URL: srv.URL + "/create"},
		2: {ID: 2, Method: "GET", URL: srv.URL + "/item?id=OLD"},
	}}
	r := NewReplayer(tr)

	prims := recipeJSON(t, ReplayRecipe{
		Resolve: &ResolveStep{
			TrafficID: 1,
			Extract:   Extractor{Name: "newid", Source: "body", Regex: `"id":"([^"]+)"`},
		},
		TrafficID:     2,
		Modifications: httpreplay.Mods{Query: map[string]*string{"id": strp("{{newid}}")}},
		Assert:        Assertion{StatusCode: intp(200), BodyContains: []string{"abc123"}},
	})
	res, err := r.Replay(context.Background(), prims)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Confirmed {
		t.Fatalf("期望坐实（regex 抽 id 注入）；evidence=%s", res.Evidence)
	}
}
