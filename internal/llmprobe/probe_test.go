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
// 认证，可拒绝工具、拒绝 max_tokens、不提供模型列表——覆盖各类兼容端点的真实差异。
type upstream struct {
	protocol      string // 提供的协议
	prefix        string // 协议挂载的路径前缀
	scheme        fwllm.AuthScheme
	rejectTools   bool
	rejectMaxTok  bool
	noModelList   bool
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
	if u.protocol == llmconfig.ProviderTypeAnthropic {
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
		if u.protocol == llmconfig.ProviderTypeAnthropic {
			_, _ = w.Write([]byte(`{"id":"x","type":"message","role":"assistant","model":"m","content":[{"type":"text","text":"pong"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`))
		} else {
			_, _ = w.Write([]byte(`{"id":"x","object":"chat.completion","model":"m","choices":[{"index":0,"message":{"role":"assistant","content":"pong"},"finish_reason":"stop"}]}`))
		}
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

// stepKeys 返回检查清单的顺序。
func stepKeys(d httpapi.Diagnosis) string {
	keys := make([]string, len(d.Steps))
	for i, s := range d.Steps {
		keys[i] = s.Key
	}
	return strings.Join(keys, ",")
}

// 清单按依赖顺序：选模型依赖模型列表，工具调用依赖选定的模型。
const wantOrder = "endpoint,auth,models,model,tools"

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

// 标准情形：全部打通，画像、模型列表、实际请求地址齐全。
func TestDiagnose_AllGreen(t *testing.T) {
	base := serve(t, &upstream{protocol: llmconfig.ProviderTypeOpenAICompat, prefix: "/v1"})
	d := diagnose(t, httpapi.ProviderProbeSpec{Type: llmconfig.ProviderTypeOpenAICompat, BaseURL: base + "/v1/chat/completions", DefaultModel: "m"})

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
	if strings.Join(d.Models, ",") != "m-a,m-b" || !d.ModelsSupported {
		t.Fatalf("模型列表应排序返回: %v", d.Models)
	}
	if got := stepKeys(d); got != wantOrder {
		t.Fatalf("清单顺序应为 %s，得 %s", wantOrder, got)
	}
}

// 地址缺版本段（中转网关常见）：补 /v1 后打通，给出地址建议。
func TestDiagnose_SuggestsVersionSegment(t *testing.T) {
	base := serve(t, &upstream{protocol: llmconfig.ProviderTypeOpenAICompat, prefix: "/v1"})
	d := diagnose(t, httpapi.ProviderProbeSpec{Type: llmconfig.ProviderTypeOpenAICompat, BaseURL: base, DefaultModel: "m"})
	if !d.OK || d.Suggestion == nil || d.Suggestion.BaseURL != base+"/v1" || d.Suggestion.Type != llmconfig.ProviderTypeOpenAICompat {
		t.Fatalf("应建议补 /v1: %+v", d.Suggestion)
	}
}

// 只认 Bearer 的 Anthropic 兼容网关：x-api-key 被拒后换 Bearer 打通，画像记录 bearer。
func TestDiagnose_NegotiatesAuthScheme(t *testing.T) {
	base := serve(t, &upstream{protocol: llmconfig.ProviderTypeAnthropic, prefix: "/anthropic", scheme: fwllm.AuthBearer})
	d := diagnose(t, httpapi.ProviderProbeSpec{Type: llmconfig.ProviderTypeAnthropic, BaseURL: base + "/anthropic", DefaultModel: "m"})
	if !d.OK || d.Profile.AuthScheme != "bearer" {
		t.Fatalf("应协商出 bearer: %+v", d.Profile)
	}
	if stepOf(t, d, httpapi.StepAuth).Detail != "Authorization: Bearer" {
		t.Fatalf("鉴权步骤应说明方式: %+v", stepOf(t, d, httpapi.StepAuth))
	}
}

// 协议选错（OpenAI 兼容填了 Anthropic 端点）：路径不存在，换协议实测后给出协议建议。
func TestDiagnose_SuggestsOtherProtocol(t *testing.T) {
	base := serve(t, &upstream{protocol: llmconfig.ProviderTypeAnthropic, prefix: "/anthropic", scheme: fwllm.AuthXAPIKey})
	d := diagnose(t, httpapi.ProviderProbeSpec{Type: llmconfig.ProviderTypeOpenAICompat, BaseURL: base + "/anthropic", DefaultModel: "m"})
	if d.OK {
		t.Fatal("选错协议不应判为可用")
	}
	if stepOf(t, d, httpapi.StepEndpoint).Status != httpapi.StepFail {
		t.Fatalf("失败应落在端点项: %+v", d.Steps)
	}
	if d.Suggestion == nil || d.Suggestion.Type != llmconfig.ProviderTypeAnthropic || d.Suggestion.BaseURL != base+"/anthropic" ||
		d.Suggestion.AuthScheme != "x-api-key" {
		t.Fatalf("应建议改用 Anthropic 协议并带上实测通过的鉴权方式: %+v", d.Suggestion)
	}
}

// 不接受 max_tokens 的推理模型：画像记录 max_completion_tokens。
func TestDiagnose_DetectsOutputLimitParam(t *testing.T) {
	base := serve(t, &upstream{protocol: llmconfig.ProviderTypeOpenAICompat, prefix: "/v1", rejectMaxTok: true})
	d := diagnose(t, httpapi.ProviderProbeSpec{Type: llmconfig.ProviderTypeOpenAICompat, BaseURL: base + "/v1", DefaultModel: "m"})
	if !d.OK || d.Profile.OutputLimitParam != "max_completion_tokens" {
		t.Fatalf("应识别出 max_completion_tokens: %+v", d.Profile)
	}
}

// 拒绝工具定义的端点：工具项失败、画像记录不支持；不提供模型列表只是提示，不影响可用。
func TestDiagnose_ToolsAndModelListCapabilities(t *testing.T) {
	base := serve(t, &upstream{protocol: llmconfig.ProviderTypeAnthropic, prefix: "", scheme: fwllm.AuthXAPIKey, rejectTools: true, noModelList: true})
	d := diagnose(t, httpapi.ProviderProbeSpec{Type: llmconfig.ProviderTypeAnthropic, BaseURL: base + "/v1", DefaultModel: "m"})
	if !d.OK {
		t.Fatalf("对话可用即判可用: %+v", d.Steps)
	}
	if d.Profile.SupportsTools == nil || *d.Profile.SupportsTools {
		t.Fatalf("应识别为不支持工具: %+v", d.Profile)
	}
	if s := stepOf(t, d, httpapi.StepModels); s.Status != httpapi.StepWarn || !strings.Contains(s.Detail, "不提供模型列表") {
		t.Fatalf("模型列表缺失应只是提示: %+v", s)
	}
}

// 未填模型：以模型列表打通后自动选用列表第一个，按它完整实测，一次给出可用结论。
func TestDiagnose_WithoutModelAutoSelectsFirst(t *testing.T) {
	base := serve(t, &upstream{protocol: llmconfig.ProviderTypeOpenAICompat, prefix: "/v1"})
	d := diagnose(t, httpapi.ProviderProbeSpec{Type: llmconfig.ProviderTypeOpenAICompat, BaseURL: base + "/v1"})
	if !d.OK || d.AutoModel != "m-a" || len(d.Models) != 2 {
		t.Fatalf("应自动选用列表第一个并打通: ok=%v auto=%q models=%v", d.OK, d.AutoModel, d.Models)
	}
	model := stepOf(t, d, httpapi.StepModel)
	if model.Status != httpapi.StepOK || !strings.Contains(model.Detail, "已自动选用") {
		t.Fatalf("模型项应注明自动选用: %+v", model)
	}
	if d.Profile.SupportsTools == nil || d.Profile.OutputLimitParam == "" || stepOf(t, d, httpapi.StepTools).Status != httpapi.StepOK {
		t.Fatalf("应按自动选用的模型完成工具与输出上限实测: %+v", d.Profile)
	}
	if got := stepKeys(d); got != wantOrder {
		t.Fatalf("清单顺序应为 %s，得 %s", wantOrder, got)
	}
}

// 自动选用的模型不可用（列表里的非对话模型等）：如实报失败并引导另选，不判可用。
func TestDiagnose_AutoSelectedModelRejected(t *testing.T) {
	base := serve(t, &upstream{protocol: llmconfig.ProviderTypeOpenAICompat, prefix: "/v1", knownModel: "m-b"})
	d := diagnose(t, httpapi.ProviderProbeSpec{Type: llmconfig.ProviderTypeOpenAICompat, BaseURL: base + "/v1"})
	model := stepOf(t, d, httpapi.StepModel)
	if d.OK || d.AutoModel != "m-a" || model.Status != httpapi.StepFail ||
		!strings.Contains(model.Detail, "自动选用的 m-a 不可用") || !strings.Contains(model.Detail, "另选") {
		t.Fatalf("应报自动选用的模型不可用: ok=%v %+v", d.OK, model)
	}
	if len(d.Models) != 2 {
		t.Fatalf("仍应带回模型列表供另选: %v", d.Models)
	}
}

// 未填模型、已过鉴权但列表请求失败（限流、上游错误）：失败落在模型列表项。
func TestDiagnose_WithoutModelListFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"message":"rate limited"}}`))
	}))
	t.Cleanup(srv.Close)
	d := diagnose(t, httpapi.ProviderProbeSpec{Type: llmconfig.ProviderTypeOpenAICompat, BaseURL: srv.URL + "/v1"})
	models, model := stepOf(t, d, httpapi.StepModels), stepOf(t, d, httpapi.StepModel)
	if d.OK || models.Status != httpapi.StepFail || !strings.Contains(models.Detail, "429") || model.Status != httpapi.StepSkip {
		t.Fatalf("失败应落在模型列表项: %+v", d.Steps)
	}
	if got := stepKeys(d); got != wantOrder {
		t.Fatalf("清单顺序应为 %s，得 %s", wantOrder, got)
	}
}

// 密钥错误：两种鉴权方式都被拒，失败落在鉴权项并说明尝试过的组合。
func TestDiagnose_AuthFailure(t *testing.T) {
	base := serve(t, &upstream{protocol: llmconfig.ProviderTypeOpenAICompat, prefix: "/v1"})
	d := diagnose(t, httpapi.ProviderProbeSpec{Type: llmconfig.ProviderTypeOpenAICompat, BaseURL: base + "/v1", DefaultModel: "m", APIKey: "sk-wrong"})
	auth := stepOf(t, d, httpapi.StepAuth)
	if d.OK || auth.Status != httpapi.StepFail || !strings.Contains(auth.Detail, "401") || !strings.Contains(auth.Detail, "已尝试 2 种") {
		t.Fatalf("应判鉴权失败: %+v", d.Steps)
	}
}

func TestResolveKey_ClassifiesMissingSources(t *testing.T) {
	t.Setenv("LIUSHA_PROBE_EMPTY", "")
	d := New(fakeStore{"env-empty": {Code: "env-empty", APIKeyEnv: "LIUSHA_PROBE_EMPTY"}}, nil)
	if _, err := d.resolveKey(context.Background(), httpapi.ProviderProbeSpec{Code: "ghost"}); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("已存 provider 不存在应透传 ErrNoRows（HTTP 404），got %v", err)
	}
	if _, err := d.resolveKey(context.Background(), httpapi.ProviderProbeSpec{Code: "env-empty"}); !errors.Is(err, fwllm.ErrNotConfigured) {
		t.Fatalf("取不到密钥应是 ErrNotConfigured（HTTP 400），got %v", err)
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

// 网关把「路径不存在」包成 200 + 错误体：响应不符合协议，与 404 同等对待——换协议实测并给出建议；
// 不填模型时空的模型列表同样证明不了端点。
func TestDiagnose_EnvelopeErrorIsOffProtocol(t *testing.T) {
	base := serve(t, &upstream{protocol: llmconfig.ProviderTypeAnthropic, prefix: "/api/anthropic", scheme: fwllm.AuthXAPIKey, envelope404: true})
	for _, model := range []string{"m", ""} {
		d := diagnose(t, httpapi.ProviderProbeSpec{Type: llmconfig.ProviderTypeOpenAICompat, BaseURL: base + "/api/anthropic", DefaultModel: model})
		if d.OK || stepOf(t, d, httpapi.StepEndpoint).Status != httpapi.StepFail {
			t.Fatalf("model=%q: 协议不符应判端点失败: %+v", model, d.Steps)
		}
		if !strings.Contains(stepOf(t, d, httpapi.StepEndpoint).Detail, "不符合所选协议") {
			t.Fatalf("model=%q: 应说明响应不符合协议: %+v", model, stepOf(t, d, httpapi.StepEndpoint))
		}
		if d.Suggestion == nil || d.Suggestion.Type != llmconfig.ProviderTypeAnthropic {
			t.Fatalf("model=%q: 应建议 Anthropic 协议: %+v", model, d.Suggestion)
		}
	}
}

// 上游给出的具体原因（如余额不足）随错误一起透出。
func TestDiagnose_SurfacesUpstreamReason(t *testing.T) {
	base := serve(t, &upstream{protocol: llmconfig.ProviderTypeOpenAICompat, prefix: "/v1", quotaExceeded: "余额不足或无可用资源包"})
	d := diagnose(t, httpapi.ProviderProbeSpec{Type: llmconfig.ProviderTypeOpenAICompat, BaseURL: base + "/v1", DefaultModel: "m"})
	model := stepOf(t, d, httpapi.StepModel)
	if model.Status != httpapi.StepFail || !strings.Contains(model.Detail, "429") || !strings.Contains(model.Detail, "上游：余额不足或无可用资源包") {
		t.Fatalf("应带出上游原因: %+v", model)
	}
	if got := stepKeys(d); got != wantOrder {
		t.Fatalf("清单顺序应为 %s，得 %s", wantOrder, got)
	}
}

// 已过鉴权但对话失败（如模型名填错）：照常拉模型列表，用户可直接从中改选。
func TestDiagnose_ModelFailureStillListsModels(t *testing.T) {
	base := serve(t, &upstream{protocol: llmconfig.ProviderTypeOpenAICompat, prefix: "/v1", knownModel: "m-a"})
	d := diagnose(t, httpapi.ProviderProbeSpec{Type: llmconfig.ProviderTypeOpenAICompat, BaseURL: base + "/v1", DefaultModel: "typo"})
	models, model := stepOf(t, d, httpapi.StepModels), stepOf(t, d, httpapi.StepModel)
	if model.Status != httpapi.StepFail || !strings.Contains(model.Detail, "Model Not Exist") {
		t.Fatalf("失败应落在模型项并带上游原因: %+v", model)
	}
	if d.OK || models.Status != httpapi.StepOK || models.Request != "GET "+base+"/v1/models" || strings.Join(d.Models, ",") != "m-a,m-b" {
		t.Fatalf("鉴权已通过时应拉回模型列表: %+v models=%v", d.Steps, d.Models)
	}
}

// 端点或鉴权失败：模型列表随其后各项一并跳过。
func TestDiagnose_ModelListSkippedBeforeAuth(t *testing.T) {
	base := serve(t, &upstream{protocol: llmconfig.ProviderTypeOpenAICompat, prefix: "/v1"})
	d := diagnose(t, httpapi.ProviderProbeSpec{Type: llmconfig.ProviderTypeOpenAICompat, BaseURL: base + "/v1", DefaultModel: "m", APIKey: "sk-wrong"})
	if stepOf(t, d, httpapi.StepModels).Status != httpapi.StepSkip || len(d.Models) != 0 {
		t.Fatalf("鉴权失败时不应拉列表: %+v", d.Steps)
	}
	if got := stepKeys(d); got != wantOrder {
		t.Fatalf("清单顺序应为 %s，得 %s", wantOrder, got)
	}
}

// 未填模型、端点不提供模型列表（如只实现 /v1/messages 的 Anthropic 兼容端点）：列表 404
// 判定不了端点对错——如实报「无法判定」并引导填写模型，而不是报端点错误；填上模型即打通。
func TestDiagnose_ModelListMissingWithoutModelIsInconclusive(t *testing.T) {
	base := serve(t, &upstream{protocol: llmconfig.ProviderTypeAnthropic, prefix: "/anthropic", scheme: fwllm.AuthXAPIKey, noModelList: true})
	d := diagnose(t, httpapi.ProviderProbeSpec{Type: llmconfig.ProviderTypeAnthropic, BaseURL: base + "/anthropic"})
	endpoint, model := stepOf(t, d, httpapi.StepEndpoint), stepOf(t, d, httpapi.StepModel)
	if d.OK || endpoint.Status != httpapi.StepWarn || !strings.Contains(endpoint.Detail, "无法判定") {
		t.Fatalf("应判无法判定而非端点错误: %+v", d.Steps)
	}
	if model.Status != httpapi.StepFail || !strings.Contains(model.Detail, "请填写模型名") {
		t.Fatalf("应引导填写模型: %+v", model)
	}

	d = diagnose(t, httpapi.ProviderProbeSpec{Type: llmconfig.ProviderTypeAnthropic, BaseURL: base + "/anthropic", DefaultModel: "m"})
	if !d.OK || stepOf(t, d, httpapi.StepModels).Status != httpapi.StepWarn {
		t.Fatalf("填上模型应打通，模型列表仅提示: %+v", d.Steps)
	}
}

// siblingHost 模拟常见部署：Anthropic 兼容接口挂在 /anthropic 下且不提供模型列表，
// 同一主机根路径提供 OpenAI 兼容的模型列表（同一把密钥，Bearer 鉴权）。
func siblingHost(t *testing.T) string {
	t.Helper()
	anth := &upstream{protocol: llmconfig.ProviderTypeAnthropic, prefix: "/anthropic", scheme: fwllm.AuthXAPIKey, noModelList: true}
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

// Anthropic 兼容端点不提供模型列表时，从同主机的 OpenAI 兼容端点取可选模型并注明来源；
// 可用性仍以对话实测为准（填了模型才判可用）。
func TestDiagnose_SiblingModelList(t *testing.T) {
	base := siblingHost(t)

	d := diagnose(t, httpapi.ProviderProbeSpec{Type: llmconfig.ProviderTypeAnthropic, BaseURL: base + "/anthropic", DefaultModel: "m"})
	models := stepOf(t, d, httpapi.StepModels)
	if !d.OK || models.Status != httpapi.StepOK || models.Request != "GET "+base+"/v1/models" || !strings.Contains(models.Detail, "同主机的 OpenAI 兼容端点") {
		t.Fatalf("应从同主机旁路取模型并注明来源: %+v", models)
	}
	if strings.Join(d.Models, ",") != "chat-flash,chat-pro" || d.ModelsSupported {
		t.Fatalf("模型来自旁路，端点本身不算提供列表: %v supported=%v", d.Models, d.ModelsSupported)
	}

	// 未填模型：旁路列表的第一个被自动选用，按它对话实测，一次打通
	d = diagnose(t, httpapi.ProviderProbeSpec{Type: llmconfig.ProviderTypeAnthropic, BaseURL: base + "/anthropic"})
	if !d.OK || d.AutoModel != "chat-flash" || len(d.Models) != 2 {
		t.Fatalf("未填模型：应自动选用旁路列表第一个并打通: ok=%v auto=%q %+v", d.OK, d.AutoModel, d.Steps)
	}
	if stepOf(t, d, httpapi.StepEndpoint).Request != "POST "+base+"/anthropic/v1/messages" {
		t.Fatalf("可用性应以对 Anthropic 端点的对话实测为准: %+v", stepOf(t, d, httpapi.StepEndpoint))
	}
	if got := stepKeys(d); got != wantOrder {
		t.Fatalf("清单顺序应为 %s，得 %s", wantOrder, got)
	}
}

func TestSiblingListBases(t *testing.T) {
	cases := []struct {
		protocol, base string
		want           []string
	}{
		{llmconfig.ProviderTypeAnthropic, "https://api.example.com/anthropic", []string{"https://api.example.com/v1", "https://api.example.com"}},
		{llmconfig.ProviderTypeAnthropic, "https://api.example.com/api/anthropic/", []string{"https://api.example.com/api/v1", "https://api.example.com/api"}},
		{llmconfig.ProviderTypeAnthropic, "https://api.example.com", nil},
		{llmconfig.ProviderTypeAnthropic, "https://api.example.com/claude", nil},
		{llmconfig.ProviderTypeOpenAICompat, "https://api.example.com/anthropic", nil},
	}
	for _, tc := range cases {
		if got := siblingListBases(tc.protocol, tc.base); strings.Join(got, ",") != strings.Join(tc.want, ",") {
			t.Fatalf("%s %s: want %v, got %v", tc.protocol, tc.base, tc.want, got)
		}
	}
}
