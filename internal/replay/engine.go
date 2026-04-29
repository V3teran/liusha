package replay

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/V3teran/liusha/internal/credential"
	"github.com/V3teran/liusha/internal/logx"
)

// defaultConcurrency 在调用方传入 0 / 负数时兜底，避免 semaphore 死锁。
const defaultConcurrency = 5

// Engine 持有共享的 *http.Client；timeout 由调用方通过 ctx 控制，不在这里 hardcode。
type Engine struct {
	client *http.Client
}

// NewEngine 构造一个 Engine。client 为 nil 时回退到 http.DefaultClient。
func NewEngine(client *http.Client) *Engine {
	if client == nil {
		client = http.DefaultClient
	}
	return &Engine{client: client}
}

// ReplayWithIdentity 按 id 中的 Credentials 替换 raw 的对应位置后发出请求。
// raw 不会被原地修改：headers / body / URL 全部走深拷贝路径。
func (e *Engine) ReplayWithIdentity(ctx context.Context, raw RawRequest, id credential.Identity) (Response, error) {
	finalURL, body, headers, err := applyIdentity(raw, id)
	if err != nil {
		return Response{IdentityName: id.Name, ErrorMessage: err.Error()}, fmt.Errorf("apply identity: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, raw.Method, finalURL, bytes.NewReader(body))
	if err != nil {
		return Response{IdentityName: id.Name, ErrorMessage: err.Error()}, fmt.Errorf("build request: %w", err)
	}
	req.Header = headers

	start := time.Now()
	resp, err := e.client.Do(req)
	if err != nil {
		return Response{
			IdentityName: id.Name,
			Latency:      time.Since(start),
			ErrorMessage: err.Error(),
		}, fmt.Errorf("do request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return Response{
			IdentityName: id.Name,
			StatusCode:   resp.StatusCode,
			Headers:      resp.Header,
			Latency:      time.Since(start),
			ErrorMessage: err.Error(),
		}, fmt.Errorf("read response body: %w", err)
	}

	return Response{
		IdentityName: id.Name,
		StatusCode:   resp.StatusCode,
		Headers:      resp.Header,
		Body:         respBody,
		Latency:      time.Since(start),
	}, nil
}

// ReplayMultiIdentity 受控并发地用 ids 全量重放同一份 raw；返回切片顺序与 ids 严格对齐。
// concurrency <= 0 时使用 defaultConcurrency 兜底。单次失败不会中止其他身份，
// 错误信息以 Response.ErrorMessage 暴露。
func (e *Engine) ReplayMultiIdentity(
	ctx context.Context,
	raw RawRequest,
	ids []credential.Identity,
	concurrency int,
) ([]Response, error) {
	if concurrency <= 0 {
		concurrency = defaultConcurrency
	}
	out := make([]Response, len(ids))
	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup

	for i, id := range ids {
		wg.Add(1)
		sem <- struct{}{}
		go func(idx int, identity credential.Identity) {
			defer wg.Done()
			defer func() { <-sem }()
			// ReplayWithIdentity 已把错误塞进 Response.ErrorMessage，这里忽略 err 不影响顺序对齐。
			r, _ := e.ReplayWithIdentity(ctx, raw, identity)
			out[idx] = r
		}(i, id)
	}
	wg.Wait()
	return out, nil
}

// applyIdentity 在 raw 的深拷贝上按 Identity 做凭证替换；
// 返回 (finalURL, finalBody, finalHeaders, err)，三者均为新分配。
func applyIdentity(raw RawRequest, id credential.Identity) (string, []byte, http.Header, error) {
	headers := raw.Headers.Clone()
	if headers == nil {
		headers = http.Header{}
	}
	body := append([]byte(nil), raw.Body...)

	parsed, err := url.Parse(raw.URL)
	if err != nil {
		return "", nil, nil, fmt.Errorf("parse url %q: %w", raw.URL, err)
	}
	q := parsed.Query()

	contentType := strings.ToLower(strings.TrimSpace(strings.Split(headers.Get("Content-Type"), ";")[0]))
	kind := classifyBody(contentType, body, id)

	var bodyForm url.Values
	var bodyJSON map[string]any
	switch kind {
	case bodyForm_:
		bodyForm, _ = url.ParseQuery(string(body))
	case bodyJSON_:
		if jerr := json.Unmarshal(body, &bodyJSON); jerr != nil {
			// JSON 解析失败时降级为 raw（不替换 body 类凭证），避免破坏请求。
			lg := logx.New("replay")
			lg.Warn().
				Str("identity", id.Name).
				Err(jerr).
				Msg("json body 解析失败，跳过 body 凭证替换")
			bodyJSON = nil
			kind = bodyRaw_
		}
	}

	for _, c := range id.Credentials {
		switch c.Type {
		case credential.TypeHeaders:
			if c.Value == "" {
				headers.Del(c.Key)
			} else {
				headers.Set(c.Key, c.Value)
			}
		case credential.TypeQuery:
			if c.Value == "" {
				q.Del(c.Key)
			} else {
				q.Set(c.Key, c.Value)
			}
		case credential.TypeBody:
			switch kind {
			case bodyForm_:
				if c.Value == "" {
					bodyForm.Del(c.Key)
				} else {
					bodyForm.Set(c.Key, c.Value)
				}
			case bodyJSON_:
				if c.Value == "" {
					delete(bodyJSON, c.Key)
				} else {
					bodyJSON[c.Key] = c.Value
				}
			default:
				lg := logx.New("replay")
				lg.Warn().
					Str("identity", id.Name).
					Str("content_type", contentType).
					Str("key", c.Key).
					Msg("body content-type 非 form/json，跳过 body 凭证替换")
			}
		}
	}

	parsed.RawQuery = q.Encode()

	switch kind {
	case bodyForm_:
		body = []byte(bodyForm.Encode())
	case bodyJSON_:
		encoded, err := json.Marshal(bodyJSON)
		if err != nil {
			return "", nil, nil, fmt.Errorf("encode json body: %w", err)
		}
		body = encoded
	}

	// 匿名身份默认剥离会话凭证，避免误带原请求的 Cookie / Authorization。
	if id.Name == credential.AnonymousName {
		headers.Del("Cookie")
		headers.Del("Authorization")
	}

	return parsed.String(), body, headers, nil
}

// bodyKind 标识当前 body 的可解析形态，决定 TypeBody 凭证如何写回。
type bodyKind int

const (
	bodyRaw_ bodyKind = iota
	bodyForm_
	bodyJSON_
)

func classifyBody(contentType string, body []byte, id credential.Identity) bodyKind {
	if len(body) == 0 || !hasBodyCredential(id) {
		return bodyRaw_
	}
	switch contentType {
	case "application/x-www-form-urlencoded":
		return bodyForm_
	case "application/json":
		return bodyJSON_
	default:
		return bodyRaw_
	}
}

func hasBodyCredential(id credential.Identity) bool {
	for _, c := range id.Credentials {
		if c.Type == credential.TypeBody {
			return true
		}
	}
	return false
}
