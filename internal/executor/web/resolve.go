package web

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/V3teran/liusha/internal/httpreplay"
)

// ResolveStep 是主 replay 前的单个准备请求：重发一条源流量，从其响应抽一个新鲜值。
// 专治 replay 时无法从源流量复用的服务器现造值（opaque-id / nonce / 过期 token）。
// 刻意无 Assert 字段——准备请求是取值不是判漏，对其响应下断言即越界（护栏1）。
type ResolveStep struct {
	TrafficID     int64           `json:"traffic_id"`
	Modifications httpreplay.Mods `json:"modifications,omitempty"`
	Extract       Extractor       `json:"extract"`
}

// Extractor 从准备请求响应里抽一个值，命名后供主请求 modifications 以 {{name}} 引用。
// Source 选值域：body / header:<名> / set-cookie:<名>；Regex 单捕获组或 JSON 点路径二选一。
type Extractor struct {
	Name   string `json:"name"`
	Source string `json:"source"`
	Regex  string `json:"regex,omitempty"`
	JSON   string `json:"json,omitempty"`
}

// extract 从重发结果里抽值。抽不到返回 error——绝不返回空串静默放过（护栏2）。
func (e Extractor) extract(res httpreplay.Result) (string, error) {
	if e.Name == "" {
		return "", fmt.Errorf("extractor.name 必填")
	}
	if e.Regex != "" && e.JSON != "" {
		return "", fmt.Errorf("extractor %q: regex 与 json 互斥，只能给一个", e.Name)
	}
	raw, err := e.rawSource(res)
	if err != nil {
		return "", err
	}
	switch {
	case e.Regex != "":
		re, err := regexp.Compile(e.Regex)
		if err != nil {
			return "", fmt.Errorf("extractor %q: 正则编译失败: %w", e.Name, err)
		}
		m := re.FindStringSubmatch(raw)
		if len(m) < 2 {
			return "", fmt.Errorf("extractor %q: 正则 %q 无捕获组命中", e.Name, e.Regex)
		}
		return m[1], nil
	case e.JSON != "":
		return extractJSONPath(raw, e.JSON, e.Name)
	default:
		if strings.TrimSpace(raw) == "" {
			return "", fmt.Errorf("extractor %q: 源值为空", e.Name)
		}
		return raw, nil
	}
}

// rawSource 按 Source 取出待抽的原始文本域。
func (e Extractor) rawSource(res httpreplay.Result) (string, error) {
	src := strings.TrimSpace(e.Source)
	switch {
	case src == "" || src == "body":
		return string(res.ResponseBody), nil
	case strings.HasPrefix(src, "header:"):
		name := strings.ToLower(strings.TrimPrefix(src, "header:"))
		v, ok := res.ResponseHeaders[name]
		if !ok {
			return "", fmt.Errorf("extractor %q: 响应无 header %q", e.Name, name)
		}
		return v, nil
	case strings.HasPrefix(src, "set-cookie:"):
		cookie := strings.TrimPrefix(src, "set-cookie:")
		v, ok := cookieValue(res.ResponseHeaders["set-cookie"], cookie)
		if !ok {
			return "", fmt.Errorf("extractor %q: set-cookie 无 %q", e.Name, cookie)
		}
		return v, nil
	default:
		return "", fmt.Errorf("extractor %q: 未知 source %q（body / header:<名> / set-cookie:<名>）", e.Name, e.Source)
	}
}

// extractJSONPath 按点路径（如 data.token）从 JSON 文本取标量值。
func extractJSONPath(raw, path, name string) (string, error) {
	var cur any
	if err := json.Unmarshal([]byte(raw), &cur); err != nil {
		return "", fmt.Errorf("extractor %q: 响应非 JSON: %w", name, err)
	}
	for _, seg := range strings.Split(path, ".") {
		obj, ok := cur.(map[string]any)
		if !ok {
			return "", fmt.Errorf("extractor %q: 路径 %q 在 %q 处非对象", name, path, seg)
		}
		cur, ok = obj[seg]
		if !ok {
			return "", fmt.Errorf("extractor %q: 路径 %q 无 %q", name, path, seg)
		}
	}
	switch v := cur.(type) {
	case string:
		return v, nil
	case float64:
		return jsonNumber(v), nil
	case bool:
		if v {
			return "true", nil
		}
		return "false", nil
	default:
		return "", fmt.Errorf("extractor %q: 路径 %q 命中非标量值", name, path)
	}
}

func jsonNumber(f float64) string {
	if f == float64(int64(f)) {
		return fmt.Sprintf("%d", int64(f))
	}
	return fmt.Sprintf("%v", f)
}

// cookieValue 从 Set-Cookie 头文本里取指定 cookie 的值。
func cookieValue(setCookie, name string) (string, bool) {
	for _, part := range strings.Split(setCookie, ",") {
		kv := strings.SplitN(strings.TrimSpace(part), ";", 2)[0]
		eq := strings.SplitN(kv, "=", 2)
		if len(eq) == 2 && strings.TrimSpace(eq[0]) == name {
			return strings.TrimSpace(eq[1]), true
		}
	}
	return "", false
}

// injectResolved 把 mods 里所有 {{name}} 占位替换为抽出的新鲜值。
// 遍历 Query/Headers/BodyFields/Body/URL 的字符串值做整串替换。
func injectResolved(mods httpreplay.Mods, name, val string) httpreplay.Mods {
	ph := "{{" + name + "}}"
	repl := func(s string) string { return strings.ReplaceAll(s, ph, val) }
	replPtr := func(p *string) *string {
		if p == nil {
			return nil
		}
		s := repl(*p)
		return &s
	}
	replMap := func(m map[string]*string) map[string]*string {
		if m == nil {
			return nil
		}
		out := make(map[string]*string, len(m))
		for k, v := range m {
			out[k] = replPtr(v)
		}
		return out
	}
	mods.URL = repl(mods.URL)
	mods.Query = replMap(mods.Query)
	mods.Headers = replMap(mods.Headers)
	mods.BodyFields = replMap(mods.BodyFields)
	mods.Body = replPtr(mods.Body)
	return mods
}
