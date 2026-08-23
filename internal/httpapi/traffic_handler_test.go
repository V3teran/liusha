package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/V3teran/liusha/internal/traffic"
)

// fakeTraffic 是 TrafficAPI 的内存实现，用于路由级单测。
type fakeTraffic struct {
	rows         []traffic.ProxySummary
	total        int
	hosts        []string
	contentTypes []string
	byID         map[int64]traffic.ProxyTraffic
	err          error
	gotFilter    traffic.ProxyListFilter // 列表收到的筛选（断言 query 解析）
}

func (f *fakeTraffic) ListPagedGlobal(_ context.Context, filter traffic.ProxyListFilter) ([]traffic.ProxySummary, error) {
	f.gotFilter = filter
	if f.err != nil {
		return nil, f.err
	}
	return f.rows, nil
}

func (f *fakeTraffic) CountGlobal(_ context.Context, _ traffic.ProxyListFilter) (int, error) {
	return f.total, f.err
}

func (f *fakeTraffic) DistinctHosts(_ context.Context) ([]string, error) {
	return f.hosts, f.err
}

func (f *fakeTraffic) DistinctContentTypes(_ context.Context) ([]string, error) {
	return f.contentTypes, f.err
}

func (f *fakeTraffic) GetByID(_ context.Context, id int64) (traffic.ProxyTraffic, error) {
	v, ok := f.byID[id]
	if !ok {
		return traffic.ProxyTraffic{}, errors.New("no rows in result set")
	}
	return v, nil
}

func TestTrafficHandler(t *testing.T) {
	t.Run("列表：筛选+分页透传，返回 items/total", func(t *testing.T) {
		fake := &fakeTraffic{
			rows: []traffic.ProxySummary{
				{ID: 2, Host: "a.com", Method: "GET", Path: "/x", StatusCode: 200, RespLen: 128, CapturedAt: time.Now()},
			},
			total: 37,
		}
		srv := newTestServer(t, Deps{Traffic: fake})
		defer srv.Close()

		req, _ := http.NewRequest("GET", srv.URL+"/traffic?host=a.com&method=get&path=/x*&content_type=application/json&search=admin*&status_min=200&status_max=299&since=2026-08-01T00:00:00Z&until=2026-08-07T00:00:00Z&page=2&size=10", nil)
		req.Header.Set("X-API-Key", "k")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()

		var body struct {
			Total int `json:"total"`
			Page  int `json:"page"`
			Size  int `json:"size"`
			Items []struct {
				ID     int64  `json:"id"`
				Host   string `json:"host"`
				Method string `json:"method"`
			} `json:"items"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body.Total != 37 || body.Page != 2 || body.Size != 10 {
			t.Errorf("分页元信息不符: %+v", body)
		}
		if len(body.Items) != 1 || body.Items[0].Host != "a.com" {
			t.Fatalf("items 不符: %+v", body.Items)
		}
		// query 解析：method 原样透传（upper 归一在 store 层 proxyWhere）、offset=(page-1)*size、status 边界透传。
		if fake.gotFilter.Method != "get" {
			t.Errorf("method 透传错，got %q", fake.gotFilter.Method)
		}
		if fake.gotFilter.Offset != 10 || fake.gotFilter.Limit != 10 {
			t.Errorf("分页透传错: limit=%d offset=%d", fake.gotFilter.Limit, fake.gotFilter.Offset)
		}
		if fake.gotFilter.StatusMin != 200 || fake.gotFilter.StatusMax != 299 {
			t.Errorf("status 边界透传错: %+v", fake.gotFilter)
		}
		// 新增维度：content_type 等值、search 跨列检索、since/until 时间区间（RFC3339）。
		if fake.gotFilter.ContentType != "application/json" || fake.gotFilter.Search != "admin*" {
			t.Errorf("content_type/search 透传错: %+v", fake.gotFilter)
		}
		wantSince := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
		wantUntil := time.Date(2026, 8, 7, 0, 0, 0, 0, time.UTC)
		if !fake.gotFilter.Since.Equal(wantSince) || !fake.gotFilter.Until.Equal(wantUntil) {
			t.Errorf("时间区间透传错: since=%v until=%v", fake.gotFilter.Since, fake.gotFilter.Until)
		}
	})

	t.Run("content-types 下拉：nil 归一为 []", func(t *testing.T) {
		fake := &fakeTraffic{contentTypes: nil}
		srv := newTestServer(t, Deps{Traffic: fake})
		defer srv.Close()

		req, _ := http.NewRequest("GET", srv.URL+"/traffic/content-types", nil)
		req.Header.Set("X-API-Key", "k")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()

		var body struct {
			ContentTypes []string `json:"content_types"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body.ContentTypes == nil {
			t.Error("content_types nil 应归一为空数组")
		}
	})

	t.Run("size 超上限收敛", func(t *testing.T) {
		fake := &fakeTraffic{}
		srv := newTestServer(t, Deps{Traffic: fake})
		defer srv.Close()

		req, _ := http.NewRequest("GET", srv.URL+"/traffic?size=99999", nil)
		req.Header.Set("X-API-Key", "k")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if fake.gotFilter.Limit != maxTrafficPageSize {
			t.Errorf("超上限应收敛到 %d，got %d", maxTrafficPageSize, fake.gotFilter.Limit)
		}
	})

	t.Run("hosts 下拉：nil 归一为 []", func(t *testing.T) {
		fake := &fakeTraffic{hosts: nil}
		srv := newTestServer(t, Deps{Traffic: fake})
		defer srv.Close()

		req, _ := http.NewRequest("GET", srv.URL+"/traffic/hosts", nil)
		req.Header.Set("X-API-Key", "k")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()

		var body struct {
			Hosts []string `json:"hosts"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body.Hosts == nil {
			t.Error("hosts nil 应归一为空数组")
		}
	})

	t.Run("详情：命中返回 body，未命中 404", func(t *testing.T) {
		fake := &fakeTraffic{byID: map[int64]traffic.ProxyTraffic{
			5: {ID: 5, Host: "a.com", Method: "POST", URL: "https://a.com/login", Path: "/login",
				StatusCode: 200, ContentType: "text/plain", HTTPVersion: "HTTP/1.1",
				RequestRaw:  []byte("POST /login HTTP/1.1\r\nHost: a.com\r\n\r\nu=admin"),
				ResponseRaw: []byte("HTTP/1.1 200 OK\r\nContent-Type: text/plain\r\n\r\nok"),
				ConsumedBy:  []traffic.ConsumerTask{{TaskID: "t1", ScenarioID: "api-pentest", Host: "a.com", Status: "completed"}}},
		}}
		srv := newTestServer(t, Deps{Traffic: fake})
		defer srv.Close()

		// 命中
		req, _ := http.NewRequest("GET", srv.URL+"/traffic/5", nil)
		req.Header.Set("X-API-Key", "k")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		var body struct {
			ID          int64  `json:"id"`
			RequestRaw  string `json:"request_raw"`
			ResponseRaw string `json:"response_raw"`
			ContentType string `json:"content_type"`
			ConsumedBy  []struct {
				TaskID string `json:"task_id"`
				ConvID string `json:"conv_id"`
			} `json:"consumed_by"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if body.ID != 5 || !strings.Contains(body.RequestRaw, "u=admin") || !strings.Contains(body.ResponseRaw, "ok") {
			t.Errorf("详情 raw 报文不符: %+v", body)
		}
		if len(body.ConsumedBy) != 1 || body.ConsumedBy[0].TaskID != "t1" {
			t.Errorf("consumed_by 不符: %+v", body.ConsumedBy)
		}

		// 未命中 → 404
		req2, _ := http.NewRequest("GET", srv.URL+"/traffic/999", nil)
		req2.Header.Set("X-API-Key", "k")
		resp2, err := http.DefaultClient.Do(req2)
		if err != nil {
			t.Fatal(err)
		}
		defer resp2.Body.Close()
		if resp2.StatusCode != 404 {
			t.Errorf("未命中应 404，got %d", resp2.StatusCode)
		}
	})
}
