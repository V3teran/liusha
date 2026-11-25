package tools

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"

	"github.com/rs/zerolog"

	"github.com/V3teran/liusha/internal/registry"
	"github.com/V3teran/liusha/internal/websearch"
)

type fakeWebSearch struct {
	got struct {
		query string
		limit int
	}
	res []websearch.Result
	err error
}

func (f *fakeWebSearch) Search(_ context.Context, query string, limit int) ([]websearch.Result, error) {
	f.got.query, f.got.limit = query, limit
	return f.res, f.err
}

// searchArgs 拼一份合法的入参；limit 为 0 时不带这个字段（模型常省略它）。
func searchArgs(query string, limit int) string {
	if limit > 0 {
		return `{"query":"` + query + `","limit":` + strconv.Itoa(limit) + `}`
	}
	return `{"query":"` + query + `"}`
}

func searchTool(f *fakeWebSearch) registry.Tool {
	return newWebSearchTool(Deps{WebSearch: f, Logger: zerolog.Nop()}, 0)
}

func TestSearchInternetTool(t *testing.T) {
	t.Run("把查询词与条数交给检索，结果逐条列出", func(t *testing.T) {
		f := &fakeWebSearch{res: []websearch.Result{
			{Title: "标题一", URL: "https://a.test/1", Snippet: "摘要一"},
			{URL: "https://b.test/2", Snippet: "摘要二"},
		}}
		res, err := searchTool(f).Execute(context.Background(), []byte(searchArgs("apache 2.4", 3)))
		if err != nil {
			t.Fatal(err)
		}
		if f.got.query != "apache 2.4" || f.got.limit != 3 {
			t.Fatalf("检索收到 query=%q limit=%d", f.got.query, f.got.limit)
		}
		for _, want := range []string{"[1] 标题一", "https://a.test/1", "摘要一", "[2] https://b.test/2", "摘要二", "未经核实"} {
			if !strings.Contains(res.Output, want) {
				t.Errorf("输出应包含 %q，得：\n%s", want, res.Output)
			}
		}
		if res.Error != "" {
			t.Errorf("不应报错，得 %q", res.Error)
		}
	})

	t.Run("没给条数用默认值，超出上限按上限", func(t *testing.T) {
		f := &fakeWebSearch{}
		tool := searchTool(f)
		if _, err := tool.Execute(context.Background(), []byte(searchArgs("q", 0))); err != nil || f.got.limit != websearch.DefaultLimit {
			t.Fatalf("默认条数应为 %d，得 %d（%v）", websearch.DefaultLimit, f.got.limit, err)
		}
		if _, err := tool.Execute(context.Background(), []byte(searchArgs("q", websearch.MaxLimit+50))); err != nil || f.got.limit != websearch.MaxLimit {
			t.Fatalf("条数应收敛到上限 %d，得 %d（%v）", websearch.MaxLimit, f.got.limit, err)
		}
	})

	t.Run("没有结果时如实说明", func(t *testing.T) {
		res, err := searchTool(&fakeWebSearch{}).Execute(context.Background(), []byte(searchArgs("q", 5)))
		if err != nil || !strings.Contains(res.Output, "没有搜到") {
			t.Fatalf("res=%+v err=%v", res, err)
		}
	})

	t.Run("接口侧故障作为工具错误交回（带工具名前缀），不中断循环", func(t *testing.T) {
		f := &fakeWebSearch{err: &websearch.StatusError{Code: 503}}
		res, err := searchTool(f).Execute(context.Background(), []byte(searchArgs("q", 5)))
		if err != nil {
			t.Fatalf("接口故障不应返回 Go error，得 %v", err)
		}
		if res.Error != "web_search: 搜索接口返回 503" {
			t.Errorf("错误 = %q", res.Error)
		}
	})

	t.Run("取消与超时原样交回", func(t *testing.T) {
		f := &fakeWebSearch{err: context.DeadlineExceeded}
		if _, err := searchTool(f).Execute(context.Background(), []byte(searchArgs("q", 5))); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("err = %v，期望 context.DeadlineExceeded", err)
		}
	})

	t.Run("参数不合法", func(t *testing.T) {
		tool := searchTool(&fakeWebSearch{})
		for name, args := range map[string]string{
			"不是 JSON": `{`,
			"没有查询词":   `{}`,
			"查询词只有空白": `{"query":"   "}`,
		} {
			res, err := tool.Execute(context.Background(), []byte(args))
			if err != nil || !strings.HasPrefix(res.Error, "web_search: ") {
				t.Errorf("%s：应作为工具错误交回，得 res=%+v err=%v", name, res, err)
			}
		}
	})
}
