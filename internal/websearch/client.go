// Package websearch 是公开网页的检索客户端：向部署方接入的搜索接口发一次查询，取回网页结果列表。
//
// 程序只认约定、不认服务，代码里没有一份搜索服务的名单（名单会过时，也把选择权从部署方手里
// 拿走）。接入一家搜索服务要定的只有三样，都是部署配置（config.yaml 的 web_search 段）：
//   - 接口地址：程序在它后面追加查询词 q 发 GET；接口自带的固定参数（语言、条数……）写进地址里
//   - 鉴权头名：密钥放在哪个请求头里
//   - 密钥
//
// 响应不必适配：两种事实上的惯例都认（见 parse）。
package websearch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/textproto"
	"net/url"
	"strings"
)

// DefaultLimit / MaxLimit 是一次检索至多取回的条数：没给 limit 时用前者，给了也不超过后者。
// 结果只用来挑出值得读全文的几页，列表再长也读不完。
const (
	DefaultLimit = 8
	MaxLimit     = 20
)

// MaxSnippetRunes 是单条摘要至多保留的字符数：摘要只用来判断值不值得打开全文。
const MaxSnippetRunes = 500

// maxResponseBytes 是一次响应至多读取的字节数：一页结果远小于此，超出的部分是接口异常
// （如返回了整页 HTML），读到上限即止，不把响应整个装进内存。
const maxResponseBytes = 1 << 20

// DefaultAuthHeader 是鉴权头名的缺省值：标准做法，值是 Bearer <密钥>。
const DefaultAuthHeader = "Authorization"

// Result 是一条检索结果。
type Result struct {
	Title   string // 网页标题
	URL     string // 网页地址
	Snippet string // 内容摘要（接口给出的片段）
}

// StatusError 是搜索接口返回了非 2xx 状态。只有状态码，不带响应正文——正文可能是一整页 HTML。
type StatusError struct {
	Code int
}

func (e *StatusError) Error() string { return fmt.Sprintf("搜索接口返回 %d", e.Code) }

// Client 是搜索接口的客户端。
type Client struct {
	endpoint   string
	authHeader string
	apiKey     string
	http       *http.Client
}

// New 构造客户端。
//
// endpoint 是搜索接口的地址。apiKey 非空时随请求发送，放在 authHeader 指名的头里：头名是
// Authorization（缺省）时值写成 Bearer <密钥>，其余头名放裸密钥——两种是各自头名的惯例用法。
func New(endpoint, authHeader, apiKey string) *Client {
	if authHeader == "" {
		authHeader = DefaultAuthHeader
	}
	c := &Client{
		endpoint:   endpoint,
		authHeader: textproto.CanonicalMIMEHeaderKey(authHeader),
		apiKey:     apiKey,
	}
	// 重定向时由自己决定带不带密钥：标准库只会摘掉 Authorization，配了别的头名时缺省会
	// 跟着重定向把密钥带去别的主机
	c.http = &http.Client{CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 10 {
			return errors.New("搜索接口重定向次数过多")
		}
		if c.carriesSecret(req, via) {
			return errors.New("搜索接口把请求重定向到了别处，已中止：密钥不随重定向带出")
		}
		return nil
	}}
	return c
}

// carriesSecret 报告这次重定向后的请求会不会把密钥带离原主机。
//
// req 是即将发出的下一跳，via 是已经发出的各跳（via[0] 是最初发给搜索接口的那个）。标准库生成
// 后续请求时已经删掉发往别处的 Authorization（按主机名判定，不看端口）；这里补上自定义头名，
// 并连端口一起比——同主机不同端口是另一个服务。
func (c *Client) carriesSecret(req *http.Request, via []*http.Request) bool {
	if c.apiKey == "" || len(via) == 0 {
		return false
	}
	if !strings.Contains(req.Header.Get(c.authHeader), c.apiKey) {
		return false // 这一跳本来就不带密钥
	}
	from, to := via[0].URL, req.URL
	return !sameOrigin(from, to)
}

// sameOrigin 报告两个地址是不是同一处（协议、主机、端口都相同）。比较前把 scheme 与 host
// 归一：主机名不区分大小写，缺省端口与显式写出的缺省端口是同一个。
func sameOrigin(a, b *url.URL) bool {
	port := func(u *url.URL) string {
		if p := u.Port(); p != "" {
			return p
		}
		switch strings.ToLower(u.Scheme) {
		case "https":
			return "443"
		case "http":
			return "80"
		}
		return ""
	}
	return strings.EqualFold(a.Scheme, b.Scheme) && strings.EqualFold(a.Hostname(), b.Hostname()) && port(a) == port(b)
}

// Search 发出一次查询，按接口给出的顺序至多返回 limit 条（limit <= 0 用 DefaultLimit，超过
// MaxLimit 按 MaxLimit）；没有地址的结果与重复地址的结果去掉。
//
// 故障以 error 交回且不含接口地址——地址的参数里可能写着密钥，而这个错误会进模型调用、
// 时间线与运行日志。ctx 结束时原样交回它的错误。
func (c *Client) Search(ctx context.Context, query string, limit int) ([]Result, error) {
	u, err := url.Parse(c.endpoint)
	if err != nil {
		return nil, errors.New("搜索接口地址无效")
	}
	q := u.Query()
	q.Set("q", query)
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, errors.New("搜索接口地址无效")
	}
	req.Header.Set("Accept", "application/json")
	if c.apiKey != "" {
		if !validHeaderName(c.authHeader) {
			return nil, errors.New("web_search.auth_header 不是合法的请求头名")
		}
		if textproto.CanonicalMIMEHeaderKey(c.authHeader) == DefaultAuthHeader {
			req.Header.Set(c.authHeader, "Bearer "+c.apiKey)
		} else {
			req.Header.Set(c.authHeader, c.apiKey)
		}
	}
	resp, err := c.http.Do(req)
	if err != nil {
		// *url.Error 的文本里带着完整地址：只留底层原因（连接被拒、超时、取消……），
		// 以及我们自己中止重定向时给的那句原因
		var uerr *url.Error
		if errors.As(err, &uerr) {
			err = uerr.Err
		}
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, err // 调用方据此区分中断与故障
		}
		return nil, fmt.Errorf("请求搜索接口: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &StatusError{Code: resp.StatusCode}
	}
	results, err := parse(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return nil, fmt.Errorf("搜索接口的响应不是约定的 JSON: %w", err)
	}
	return collect(results, limit), nil
}

// validHeaderName 报告 s 是不是合法的请求头名（RFC 9110 的 token：字母、数字与少数符号）。
// 头名由部署方配置，写错了要让请求发不出去时如实报出，而不是带着一个发不出去的头去调接口。
func validHeaderName(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if !isTokenChar(s[i]) {
			return false
		}
	}
	return true
}

func isTokenChar(c byte) bool {
	switch {
	case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		return true
	}
	return strings.IndexByte("!#$%&'*+-.^_`|~", c) >= 0
}

// rawResult 是一条结果在各家的通用字段：摘要有的叫 content、有的叫 description。
type rawResult struct {
	Title       string `json:"title"`
	URL         string `json:"url"`
	Content     string `json:"content"`
	Description string `json:"description"`
}

func (r rawResult) snippet() string {
	if r.Content != "" {
		return r.Content
	}
	return r.Description
}

// parse 读出响应里的结果列表。两种事实上的惯例都认，认不出（两种都没有）才报错：
//
//	{"results":[{"title","url","content"}]}                  平铺
//	{"web":{"results":[{"title","url","description"}]}}      嵌在 web 下
//
// 按这个形状认，而不是按服务认：程序不知道对面是哪一家，部署方也不必为接一家服务写适配。
func parse(r io.Reader) ([]rawResult, error) {
	var body struct {
		Results []rawResult `json:"results"`
		Web     struct {
			Results []rawResult `json:"results"`
		} `json:"web"`
	}
	if err := json.NewDecoder(r).Decode(&body); err != nil {
		return nil, err
	}
	switch {
	case body.Results != nil:
		return body.Results, nil
	case body.Web.Results != nil:
		return body.Web.Results, nil
	}
	return nil, errors.New("响应里没有 results 或 web.results")
}

// collect 把接口的结果整理成要交出去的列表。
func collect(raw []rawResult, limit int) []Result {
	if limit <= 0 {
		limit = DefaultLimit
	}
	limit = min(limit, MaxLimit)
	out := make([]Result, 0, min(len(raw), limit))
	seen := make(map[string]bool, len(raw))
	for _, r := range raw {
		u := strings.TrimSpace(r.URL)
		// 只保留带 scheme 与主机的地址：相对路径或裸文字点不开，也不是一个网页
		if u == "" || seen[u] || !absoluteURL(u) {
			continue
		}
		seen[u] = true
		out = append(out, Result{
			Title:   oneLine(r.Title),
			URL:     u,
			Snippet: clip(oneLine(r.snippet()), MaxSnippetRunes),
		})
		if len(out) == limit {
			break
		}
	}
	return out
}

func absoluteURL(s string) bool {
	u, err := url.Parse(s)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}

// oneLine 把连续的空白折成一个空格：标题与摘要各占一行，结果之间才分得开。
func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// clip 把文本截到至多 max 个字符，截了就在末尾加省略号。
func clip(s string, max int) string {
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max]) + "…"
}
