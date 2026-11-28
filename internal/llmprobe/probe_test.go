package llmprobe

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	fwllm "github.com/V3teran/liusha/internal/framework/llm"
	"github.com/V3teran/liusha/internal/httpapi"
	"github.com/V3teran/liusha/internal/llmconfig"
)

// upstream 是可配置的协议服务替身：只在指定前缀下提供指定协议，按配置的鉴权方式
// 认证，可拒绝工具、拒绝 max_tokens、不提供或回空模型列表——覆盖各类兼容端点的真实差异。
type upstream struct {
	protocol      string // 提供的协议
	prefix        string // 协议挂载的路径前缀
	scheme        fwllm.AuthScheme
	rejectTools   bool
	rejectMaxTok  bool
	noModelList   bool
	emptyModels   bool   // 模型列表接口回空列表
	envelope404   bool   // 未知路径回 200 + 网关错误体（而非 404）
	quotaExceeded string // 非空：对话请求回 429 并带此说明
	knownModel    string // 非空：对话请求的模型名不是它时回 400「模型不存在」
	requestedPath []string
}

const key = "sk-real"

func (u *upstream) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	u.requestedPath = append(u.requestedPath, r.Method+" "+r.URL.Path)
	w.Header().Set("Content-Type", "application/json")
	authed := false
	switch u.scheme {
	case fwllm.AuthXAPIKey:
		authed = r.Header.Get("X-Api-Key") == key
	default:
		authed = r.Header.Get("Authorization") == "Bearer "+key
	}
	if !authed {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"invalid key","type":"authentication_error"}}`))
		return
	}
	chat, models := u.prefix+"/chat/completions", u.prefix+"/models"
	if u.protocol == llmconfig.ProviderTypeAnthropicMessages {
		chat, models = u.prefix+"/v1/messages", u.prefix+"/v1/models"
	}
	switch {
	case r.Method == http.MethodPost && r.URL.Path == chat && u.quotaExceeded != "":
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"code":"1113","message":"` + u.quotaExceeded + `"}}`))
	case r.Method == http.MethodPost && r.URL.Path == chat:
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		if _, has := body["tools"]; has && u.rejectTools {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"message":"tools is not supported","type":"invalid_request_error"}}`))
			return
		}
		if u.knownModel != "" && body["model"] != u.knownModel {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"message":"Model Not Exist","type":"invalid_request_error"}}`))
			return
		}
		if _, has := body["max_tokens"]; has && u.rejectMaxTok {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"message":"Unsupported parameter: 'max_tokens' is not supported with this model. Use 'max_completion_tokens' instead.","type":"invalid_request_error"}}`))
			return
		}
		if u.protocol == llmconfig.ProviderTypeAnthropicMessages {
			// Anthropic 适配器只走流式链路：对话应答是一条事件流。
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte(`event: message_start
data: {"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","model":"m","content":[],"stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":1,"output_tokens":1}}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"pong"}}

event: content_block_stop
data: {"type":"content_block_stop","index":0}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null},"usage":{"output_tokens":2}}

event: message_stop
data: {"type":"message_stop"}
`))
		} else {
			_, _ = w.Write([]byte(`{"id":"x","object":"chat.completion","model":"m","choices":[{"index":0,"message":{"role":"assistant","content":"pong"},"finish_reason":"stop"}]}`))
		}
	case r.Method == http.MethodGet && r.URL.Path == models && u.emptyModels:
		_, _ = w.Write([]byte(`{"object":"list","data":[],"has_more":false}`))
	case r.Method == http.MethodGet && r.URL.Path == models && !u.noModelList:
		_, _ = w.Write([]byte(`{"object":"list","data":[{"id":"m-b","object":"model","type":"model"},{"id":"m-a","object":"model","type":"model"}],"has_more":false}`))
	case u.envelope404:
		_, _ = w.Write([]byte(`{"code":500,"msg":"404 NOT_FOUND","success":false}`))
	default:
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":{"message":"not found"}}`))
	}
}

func serve(t *testing.T, u *upstream) string {
	t.Helper()
	srv := httptest.NewServer(u)
	t.Cleanup(srv.Close)
	return srv.URL
}

func diagnose(t *testing.T, spec httpapi.ProviderProbeSpec) httpapi.Diagnosis {
	t.Helper()
	if spec.APIKey == "" && spec.Code == "" {
		spec.APIKey = key
	}
	res, err := New(nil, nil).Diagnose(context.Background(), spec)
	if err != nil {
		t.Fatalf("diagnose: %v", err)
	}
	return res
}

func listModels(t *testing.T, spec httpapi.ProviderProbeSpec) httpapi.ModelList {
	t.Helper()
	if spec.APIKey == "" && spec.Code == "" {
		spec.APIKey = key
	}
	res, err := New(nil, nil).ListModels(context.Background(), spec)
	if err != nil {
		t.Fatalf("list models: %v", err)
	}
	return res
}

// stepKeys 返回检查清单的顺序。
func stepKeys(d httpapi.Diagnosis) string {
	keys := make([]string, len(d.Steps))
	for i, s := range d.Steps {
		keys[i] = s.Key
	}
	return strings.Join(keys, ",")
}

// 清单按依赖顺序：工具调用依赖模型可用。
const wantOrder = "endpoint,auth,model,tools"

func stepOf(t *testing.T, d httpapi.Diagnosis, key string) httpapi.DiagnosisStep {
	t.Helper()
	for _, s := range d.Steps {
		if s.Key == key {
			return s
		}
	}
	t.Fatalf("缺少步骤 %s: %+v", key, d.Steps)
	return httpapi.DiagnosisStep{}
}

// 标准情形：全部打通，画像与实际请求地址齐全；测试连接不拉模型列表。
func TestDiagnose_AllGreen(t *testing.T) {
	u := &upstream{protocol: llmconfig.ProviderTypeOpenAIChatCompletions, prefix: "/v1"}
	base := serve(t, u)
	d := diagnose(t, httpapi.ProviderProbeSpec{Type: llmconfig.ProviderTypeOpenAIChatCompletions, BaseURL: base + "/v1/chat/completions", DefaultModel: "m"})

	if !d.OK || d.Suggestion != nil {
		t.Fatalf("应全部打通且无建议: %+v", d)
	}
	if d.BaseURL != base+"/v1" {
		t.Fatalf("误粘的操作路径应被归一掉: %s", d.BaseURL)
	}
	if got := stepOf(t, d, httpapi.StepEndpoint).Request; got != "POST "+base+"/v1/chat/completions" {
		t.Fatalf("应展示实际请求地址: %s", got)
	}
	if d.Profile.AuthScheme != "bearer" || d.Profile.OutputLimitParam != "max_tokens" || d.Profile.SupportsTools == nil || !*d.Profile.SupportsTools {
		t.Fatalf("画像错误: %+v", d.Profile)
	}
	if got := stepKeys(d); got != wantOrder {
		t.Fatalf("清单顺序应为 %s，得 %s", wantOrder, got)
	}
	for _, p := range u.requestedPath {
		if strings.HasPrefix(p, "GET ") {
			t.Fatalf("测试连接不应拉模型列表: %v", u.requestedPath)
		}
	}
}

// 地址缺版本段（中转网关常见）：补 /v1 后打通，给出地址建议。
func TestDiagnose_SuggestsVersionSegment(t *testing.T) {
	base := serve(t, &upstream{protocol: llmconfig.ProviderTypeOpenAIChatCompletions, prefix: "/v1"})
	d := diagnose(t, httpapi.ProviderProbeSpec{Type: llmconfig.ProviderTypeOpenAIChatCompletions, BaseURL: base, DefaultModel: "m"})
	if !d.OK || d.Suggestion == nil || d.Suggestion.BaseURL != base+"/v1" || d.Suggestion.Type != llmconfig.ProviderTypeOpenAIChatCompletions {
		t.Fatalf("应建议补 /v1: %+v", d.Suggestion)
	}
}

// 只认 Bearer 的 Anthropic 兼容网关：x-api-key 被拒后换 Bearer 打通，画像记录 bearer。
func TestDiagnose_NegotiatesAuthScheme(t *testing.T) {
	base := serve(t, &upstream{protocol: llmconfig.ProviderTypeAnthropicMessages, prefix: "/anthropic", scheme: fwllm.AuthBearer})
	d := diagnose(t, httpapi.ProviderProbeSpec{Type: llmconfig.ProviderTypeAnthropicMessages, BaseURL: base + "/anthropic", DefaultModel: "m"})
	if !d.OK || d.Profile.AuthScheme != "bearer" {
		t.Fatalf("应协商出 bearer: %+v", d.Profile)
	}
	if stepOf(t, d, httpapi.StepAuth).Detail != "Authorization: Bearer" {
		t.Fatalf("鉴权步骤应说明方式: %+v", stepOf(t, d, httpapi.StepAuth))
	}
}

// 协议选错（OpenAI 兼容填了 Anthropic 端点）：路径不存在，换协议实测后给出协议建议。
func TestDiagnose_SuggestsOtherProtocol(t *testing.T) {
	base := serve(t, &upstream{protocol: llmconfig.ProviderTypeAnthropicMessages, prefix: "/anthropic", scheme: fwllm.AuthXAPIKey})
	d := diagnose(t, httpapi.ProviderProbeSpec{Type: llmconfig.ProviderTypeOpenAIChatCompletions, BaseURL: base + "/anthropic", DefaultModel: "m"})
	if d.OK {
		t.Fatal("选错协议不应判为可用")
	}
	if stepOf(t, d, httpapi.StepEndpoint).Status != httpapi.StepFail {
		t.Fatalf("失败应落在端点项: %+v", d.Steps)
	}
	if d.Suggestion == nil || d.Suggestion.Type != llmconfig.ProviderTypeAnthropicMessages || d.Suggestion.BaseURL != base+"/anthropic" ||
		d.Suggestion.AuthScheme != "x-api-key" {
		t.Fatalf("应建议改用 Anthropic 协议并带上实测通过的鉴权方式: %+v", d.Suggestion)
	}
	if got := stepKeys(d); got != wantOrder {
		t.Fatalf("清单顺序应为 %s，得 %s", wantOrder, got)
	}
}

// 不接受 max_tokens 的推理模型：画像记录 max_completion_tokens。
func TestDiagnose_DetectsOutputLimitParam(t *testing.T) {
	base := serve(t, &upstream{protocol: llmconfig.ProviderTypeOpenAIChatCompletions, prefix: "/v1", rejectMaxTok: true})
	d := diagnose(t, httpapi.ProviderProbeSpec{Type: llmconfig.ProviderTypeOpenAIChatCompletions, BaseURL: base + "/v1", DefaultModel: "m"})
	if !d.OK || d.Profile.OutputLimitParam != "max_completion_tokens" {
		t.Fatalf("应识别出 max_completion_tokens: %+v", d.Profile)
	}
}

// 拒绝工具定义的端点：工具项失败、画像记录不支持，配置仍可用。
func TestDiagnose_ToolsRejected(t *testing.T) {
	base := serve(t, &upstream{protocol: llmconfig.ProviderTypeAnthropicMessages, prefix: "", scheme: fwllm.AuthXAPIKey, rejectTools: true})
	d := diagnose(t, httpapi.ProviderProbeSpec{Type: llmconfig.ProviderTypeAnthropicMessages, BaseURL: base + "/v1", DefaultModel: "m"})
	if !d.OK {
		t.Fatalf("对话可用即判可用: %+v", d.Steps)
	}
	if d.Profile.SupportsTools == nil || *d.Profile.SupportsTools || stepOf(t, d, httpapi.StepTools).Status != httpapi.StepFail {
		t.Fatalf("应识别为不支持工具: %+v %+v", d.Profile, d.Steps)
	}
}

// 密钥错误：两种鉴权方式都被拒，失败落在鉴权项并说明尝试过的组合，其后各项跳过。
func TestDiagnose_AuthFailure(t *testing.T) {
	base := serve(t, &upstream{protocol: llmconfig.ProviderTypeOpenAIChatCompletions, prefix: "/v1"})
	d := diagnose(t, httpapi.ProviderProbeSpec{Type: llmconfig.ProviderTypeOpenAIChatCompletions, BaseURL: base + "/v1", DefaultModel: "m", APIKey: "sk-wrong"})
	auth := stepOf(t, d, httpapi.StepAuth)
	if d.OK || auth.Status != httpapi.StepFail || !strings.Contains(auth.Detail, "401") || !strings.Contains(auth.Detail, "已尝试 2 种") {
		t.Fatalf("应判鉴权失败: %+v", d.Steps)
	}
	if stepOf(t, d, httpapi.StepModel).Status != httpapi.StepSkip || stepOf(t, d, httpapi.StepTools).Status != httpapi.StepSkip {
		t.Fatalf("鉴权失败后各项应跳过: %+v", d.Steps)
	}
	if got := stepKeys(d); got != wantOrder {
		t.Fatalf("清单顺序应为 %s，得 %s", wantOrder, got)
	}
}

// 已过鉴权但对话失败（如模型名填错）：失败落在模型项并带上游原因，工具调用跳过。
func TestDiagnose_ModelFailure(t *testing.T) {
	base := serve(t, &upstream{protocol: llmconfig.ProviderTypeOpenAIChatCompletions, prefix: "/v1", knownModel: "m-a"})
	d := diagnose(t, httpapi.ProviderProbeSpec{Type: llmconfig.ProviderTypeOpenAIChatCompletions, BaseURL: base + "/v1", DefaultModel: "typo"})
	model := stepOf(t, d, httpapi.StepModel)
	if d.OK || model.Status != httpapi.StepFail || !strings.Contains(model.Detail, "Model Not Exist") {
		t.Fatalf("失败应落在模型项并带上游原因: %+v", model)
	}
	if stepOf(t, d, httpapi.StepAuth).Status != httpapi.StepOK || stepOf(t, d, httpapi.StepTools).Status != httpapi.StepSkip {
		t.Fatalf("鉴权已过、工具调用跳过: %+v", d.Steps)
	}
	if got := stepKeys(d); got != wantOrder {
		t.Fatalf("清单顺序应为 %s，得 %s", wantOrder, got)
	}
}

func TestResolveKey_ClassifiesMissingSources(t *testing.T) {
	p := New(fakeStore{"no-key": {Code: "no-key"}}, nil)
	if _, err := p.resolveKey(context.Background(), httpapi.ProviderProbeSpec{Code: "ghost"}); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("已存 provider 不存在应透传 ErrNoRows（HTTP 404），got %v", err)
	}
	if _, err := p.resolveKey(context.Background(), httpapi.ProviderProbeSpec{Code: "no-key"}); !errors.Is(err, fwllm.ErrNotConfigured) {
		t.Fatalf("取不到密钥应是 ErrNotConfigured（HTTP 400），got %v", err)
	}
	if _, err := p.ListModels(context.Background(), httpapi.ProviderProbeSpec{Type: llmconfig.ProviderTypeOpenAIChatCompletions, BaseURL: "https://x", Code: "ghost"}); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("拉取模型列表同样按密钥来源报错，got %v", err)
	}
}

type fakeStore map[string]llmconfig.Provider

func (f fakeStore) ProviderByCode(_ context.Context, code string) (llmconfig.Provider, error) {
	if p, ok := f[code]; ok {
		return p, nil
	}
	return llmconfig.Provider{}, pgx.ErrNoRows
}

func TestHumanizeProbeErr_PrefersStatusCode(t *testing.T) {
	for code, want := range map[int]string{401: "鉴权失败", 404: "找不到资源", 400: "请求被拒绝（400）", 429: "触发限流"} {
		if got := humanizeProbeErr(&fwllm.HTTPError{Code: code, Inner: errors.New("upstream")}); !strings.HasPrefix(got, want) {
			t.Fatalf("HTTP %d: want prefix %q, got %q", code, want, got)
		}
	}
}

// 网关把「路径不存在」包成 200 + 错误体：响应不符合协议，与 404 同等对待——换协议实测并给出建议。
func TestDiagnose_EnvelopeErrorIsOffProtocol(t *testing.T) {
	base := serve(t, &upstream{protocol: llmconfig.ProviderTypeAnthropicMessages, prefix: "/api/anthropic", scheme: fwllm.AuthXAPIKey, envelope404: true})
	d := diagnose(t, httpapi.ProviderProbeSpec{Type: llmconfig.ProviderTypeOpenAIChatCompletions, BaseURL: base + "/api/anthropic", DefaultModel: "m"})
	endpoint := stepOf(t, d, httpapi.StepEndpoint)
	if d.OK || endpoint.Status != httpapi.StepFail || !strings.Contains(endpoint.Detail, "不符合所选协议") {
		t.Fatalf("协议不符应判端点失败并说明: %+v", d.Steps)
	}
	if d.Suggestion == nil || d.Suggestion.Type != llmconfig.ProviderTypeAnthropicMessages {
		t.Fatalf("应建议 Anthropic 协议: %+v", d.Suggestion)
	}
}

// 上游给出的具体原因（如余额不足）随错误一起透出。
func TestDiagnose_SurfacesUpstreamReason(t *testing.T) {
	base := serve(t, &upstream{protocol: llmconfig.ProviderTypeOpenAIChatCompletions, prefix: "/v1", quotaExceeded: "余额不足或无可用资源包"})
	d := diagnose(t, httpapi.ProviderProbeSpec{Type: llmconfig.ProviderTypeOpenAIChatCompletions, BaseURL: base + "/v1", DefaultModel: "m"})
	model := stepOf(t, d, httpapi.StepModel)
	if model.Status != httpapi.StepFail || !strings.Contains(model.Detail, "429") || !strings.Contains(model.Detail, "上游：余额不足或无可用资源包") {
		t.Fatalf("应带出上游原因: %+v", model)
	}
}

// 标准情形：排序去重返回，带上实际请求；只发模型列表请求。
func TestListModels_Sorted(t *testing.T) {
	u := &upstream{protocol: llmconfig.ProviderTypeOpenAIChatCompletions, prefix: "/v1"}
	base := serve(t, u)
	l := listModels(t, httpapi.ProviderProbeSpec{Type: llmconfig.ProviderTypeOpenAIChatCompletions, BaseURL: base + "/v1/chat/completions"})
	if l.Status != httpapi.StepOK || strings.Join(l.Models, ",") != "m-a,m-b" || l.Request != "GET "+base+"/v1/models" || l.Detail != "" {
		t.Fatalf("应取回排序后的列表（地址先归一）: %+v", l)
	}
	if strings.Join(u.requestedPath, ",") != "GET /v1/models" {
		t.Fatalf("只应发一次模型列表请求: %v", u.requestedPath)
	}
}

// 记录的鉴权方式被拒：换另一种再拉；两种都被拒才报鉴权失败。
func TestListModels_NegotiatesAuthScheme(t *testing.T) {
	base := serve(t, &upstream{protocol: llmconfig.ProviderTypeAnthropicMessages, prefix: "/anthropic", scheme: fwllm.AuthBearer})
	l := listModels(t, httpapi.ProviderProbeSpec{Type: llmconfig.ProviderTypeAnthropicMessages, BaseURL: base + "/anthropic", AuthScheme: "x-api-key"})
	if l.Status != httpapi.StepOK || len(l.Models) != 2 {
		t.Fatalf("x-api-key 被拒后应换 Bearer 取到列表: %+v", l)
	}

	l = listModels(t, httpapi.ProviderProbeSpec{Type: llmconfig.ProviderTypeAnthropicMessages, BaseURL: base + "/anthropic", APIKey: "sk-wrong"})
	if l.Status != httpapi.StepFail || !strings.Contains(l.Detail, "401") || !strings.Contains(l.Detail, "已尝试 2 种鉴权方式") || len(l.Models) != 0 {
		t.Fatalf("两种鉴权方式都被拒应报鉴权失败: %+v", l)
	}
}

// 端点不提供模型列表（404）：只是提示，说明它判定不了地址对错，并引导直接填写模型。
// 不补版本段：列表证明不了地址讲的是所选协议。
func TestListModels_NotProvided(t *testing.T) {
	u := &upstream{protocol: llmconfig.ProviderTypeOpenAIChatCompletions, prefix: "/v1"}
	base := serve(t, u)
	l := listModels(t, httpapi.ProviderProbeSpec{Type: llmconfig.ProviderTypeOpenAIChatCompletions, BaseURL: base})
	if l.Status != httpapi.StepWarn || !strings.Contains(l.Detail, "返回 404") || !strings.Contains(l.Detail, "直接填写模型名") || len(l.Models) != 0 {
		t.Fatalf("列表接口不存在应只是提示: %+v", l)
	}
	if strings.Join(u.requestedPath, ",") != "GET /models" {
		t.Fatalf("不应补版本段另试: %v", u.requestedPath)
	}
}

// 空列表不是错误：提示直接填写。
func TestListModels_EmptyList(t *testing.T) {
	base := serve(t, &upstream{protocol: llmconfig.ProviderTypeOpenAIChatCompletions, prefix: "/v1", emptyModels: true})
	l := listModels(t, httpapi.ProviderProbeSpec{Type: llmconfig.ProviderTypeOpenAIChatCompletions, BaseURL: base + "/v1"})
	if l.Status != httpapi.StepWarn || !strings.Contains(l.Detail, "空列表") || l.Models == nil {
		t.Fatalf("空列表应是提示且 models 非 nil: %+v", l)
	}
}

// 已过鉴权但拉取失败（限流、上游错误）：报失败并带原因。
func TestListModels_UpstreamFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"message":"rate limited"}}`))
	}))
	t.Cleanup(srv.Close)
	l := listModels(t, httpapi.ProviderProbeSpec{Type: llmconfig.ProviderTypeOpenAIChatCompletions, BaseURL: srv.URL + "/v1"})
	if l.Status != httpapi.StepFail || !strings.Contains(l.Detail, "429") || strings.Contains(l.Detail, "已尝试") {
		t.Fatalf("应报拉取失败（非鉴权问题不换鉴权方式）: %+v", l)
	}
}

// siblingHost 模拟常见部署：Anthropic 兼容接口挂在 /anthropic 下且不提供模型列表，
// 同一主机根路径提供 OpenAI 兼容的模型列表（同一把密钥，Bearer 鉴权）。
func siblingHost(t *testing.T) string {
	t.Helper()
	anth := &upstream{protocol: llmconfig.ProviderTypeAnthropicMessages, prefix: "/anthropic", scheme: fwllm.AuthXAPIKey, noModelList: true}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/v1/models" && r.Header.Get("Authorization") == "Bearer "+key {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"object":"list","data":[{"id":"chat-pro","object":"model"},{"id":"chat-flash","object":"model"}]}`))
			return
		}
		anth.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

// Anthropic 兼容端点不提供模型列表时，从同主机的 OpenAI 兼容端点取可选模型并注明来源。
func TestListModels_Sibling(t *testing.T) {
	base := siblingHost(t)
	l := listModels(t, httpapi.ProviderProbeSpec{Type: llmconfig.ProviderTypeAnthropicMessages, BaseURL: base + "/anthropic"})
	if l.Status != httpapi.StepOK || strings.Join(l.Models, ",") != "chat-flash,chat-pro" ||
		l.Request != "GET "+base+"/v1/models" || !strings.Contains(l.Detail, "同主机的 OpenAI 兼容端点") {
		t.Fatalf("应从同主机旁路取模型并注明来源: %+v", l)
	}
}

func TestSiblingListBases(t *testing.T) {
	cases := []struct {
		protocol, base string
		want           []string
	}{
		{llmconfig.ProviderTypeAnthropicMessages, "https://api.example.com/anthropic", []string{"https://api.example.com/v1", "https://api.example.com"}},
		{llmconfig.ProviderTypeAnthropicMessages, "https://api.example.com/api/anthropic/", []string{"https://api.example.com/api/v1", "https://api.example.com/api"}},
		{llmconfig.ProviderTypeAnthropicMessages, "https://api.example.com", nil},
		{llmconfig.ProviderTypeAnthropicMessages, "https://api.example.com/claude", nil},
		{llmconfig.ProviderTypeOpenAIChatCompletions, "https://api.example.com/anthropic", nil},
	}
	for _, tc := range cases {
		if got := siblingListBases(tc.protocol, tc.base); strings.Join(got, ",") != strings.Join(tc.want, ",") {
			t.Fatalf("%s %s: want %v, got %v", tc.protocol, tc.base, tc.want, got)
		}
	}
}
