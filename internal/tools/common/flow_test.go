package common

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/V3teran/liusha/internal/flow"
)

func TestStripHostPort(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"ipv4 带端口", "111.229.193.40:34280", "111.229.193.40"},
		{"ipv4 无端口", "111.229.193.40", "111.229.193.40"},
		{"域名带端口", "example.com:8080", "example.com"},
		{"域名无端口", "example.com", "example.com"},
		{"ipv6 带端口", "[::1]:8080", "::1"},
		{"空串", "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := stripHostPort(c.in); got != c.want {
				t.Fatalf("stripHostPort(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

// captureStore 记录最近一次 ListByOwnerFiltered 收到的 filter，用于断言归一化。
type captureStore struct {
	gotFilter flow.ListFilter
}

func (s *captureStore) ListByOwnerFiltered(_ context.Context, _ string, f flow.ListFilter) ([]flow.FlowSummary, error) {
	s.gotFilter = f
	return nil, nil
}

func (s *captureStore) GetByID(_ context.Context, _ int64) (flow.Flow, error) {
	return flow.Flow{}, nil
}

// list_flows 的 host 过滤必须在落库匹配前剥端口——http_flow.host 存的是裸 host，
// 带端口的 p.Host 默认值 / LLM 传入值若不归一化，精确匹配永远落空。
func TestListFlowsStripsPortFromDefaultHost(t *testing.T) {
	store := &captureStore{}
	lf := &ListFlows{Store: store, OwnerID: "owner-1", Host: "111.229.193.40:34280"}

	if _, err := lf.Execute(context.Background(), json.RawMessage(`{}`)); err != nil {
		t.Fatalf("Execute 空参数报错: %v", err)
	}
	if store.gotFilter.Host != "111.229.193.40" {
		t.Fatalf("默认 host 未剥端口：filter.Host = %q, want %q", store.gotFilter.Host, "111.229.193.40")
	}
}

func TestListFlowsStripsPortFromArgHost(t *testing.T) {
	store := &captureStore{}
	lf := &ListFlows{Store: store, OwnerID: "owner-1", Host: "fallback"}

	args := json.RawMessage(`{"host":"example.com:8443"}`)
	if _, err := lf.Execute(context.Background(), args); err != nil {
		t.Fatalf("Execute 报错: %v", err)
	}
	if store.gotFilter.Host != "example.com" {
		t.Fatalf("LLM 传入 host 未剥端口：filter.Host = %q, want %q", store.gotFilter.Host, "example.com")
	}
}
