package httpapi

import (
	"context"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5"
)

// fakeProber 记录收到的规格并返回预置结果。
type fakeProber struct {
	diagnosis Diagnosis
	list      ModelList
	err       error
	in        ProviderProbeSpec
}

func (f *fakeProber) Diagnose(_ context.Context, spec ProviderProbeSpec) (Diagnosis, error) {
	f.in = spec
	return f.diagnosis, f.err
}

func (f *fakeProber) ListModels(_ context.Context, spec ProviderProbeSpec) (ModelList, error) {
	f.in = spec
	return f.list, f.err
}

// 两个实测入口共有的边界校验：协议、地址、密钥来源，以及已存 provider 不存在时 404。
var probeTargetCases = []struct {
	name string
	err  error
	body map[string]any
	want int
}{
	{"未选协议", nil, map[string]any{"base_url": "https://x", "api_key": "k", "default_model": "m"}, 400},
	{"Base URL 非法", nil, map[string]any{"type": "anthropic_messages", "base_url": "x", "api_key": "k", "default_model": "m"}, 400},
	{"无密钥来源", nil, map[string]any{"type": "anthropic_messages", "base_url": "https://x", "default_model": "m"}, 400},
	{"已存 provider 不存在", fmt.Errorf("取已存 provider: %w", pgx.ErrNoRows), map[string]any{"type": "anthropic_messages", "base_url": "https://x", "code": "ghost", "default_model": "m"}, 404},
}

func TestProbeHandlers_ValidateTarget(t *testing.T) {
	for _, path := range []string{"/api/models/providers/diagnose", "/api/models/providers/models"} {
		for _, tc := range probeTargetCases {
			t.Run(path+"/"+tc.name, func(t *testing.T) {
				srv := newTestServer(t, Deps{Models: &fakeModel{}, ProviderProber: &fakeProber{err: tc.err}})
				defer srv.Close()
				code, body := doJSON(t, "POST", srv.URL+path, tc.body)
				if code != tc.want {
					t.Fatalf("want %d, got %d (%v)", tc.want, code, body)
				}
			})
		}
	}
}

func TestDiagnoseProviderHandler(t *testing.T) {
	valid := map[string]any{"type": "anthropic_messages", "base_url": "https://api.example.com/anthropic", "api_key": "k", "default_model": " m ", "auth_scheme": "bearer", "output_limit_param": "max_tokens", "reasoning_effort": "max"}
	fp := &fakeProber{diagnosis: Diagnosis{OK: true, Steps: []DiagnosisStep{{Key: StepEndpoint, Status: StepOK}}}}
	srv := newTestServer(t, Deps{Models: &fakeModel{}, ProviderProber: fp})
	defer srv.Close()

	code, body := doJSON(t, "POST", srv.URL+"/api/models/providers/diagnose", valid)
	if code != 200 || body["ok"] != true {
		t.Fatalf("诊断结果应原样返回: %d %v", code, body)
	}
	if fp.in.DefaultModel != "m" || fp.in.AuthScheme != "bearer" || fp.in.Type != "anthropic_messages" || fp.in.OutputLimitParam != "max_tokens" || fp.in.ReasoningEffort != "max" {
		t.Fatalf("规格应透传（模型名去空白）: %+v", fp.in)
	}

	// 思考深度不在档位表里：入口就拒绝，不发任何上游请求
	fp.in = ProviderProbeSpec{}
	badEffort := map[string]any{"type": "anthropic_messages", "base_url": "https://x", "api_key": "k", "default_model": "m", "reasoning_effort": "xhigh"}
	if code, body := doJSON(t, "POST", srv.URL+"/api/models/providers/diagnose", badEffort); code != 400 {
		t.Fatalf("非法思考深度应 400，got %d (%v)", code, body)
	}
	if fp.in != (ProviderProbeSpec{}) {
		t.Fatalf("非法思考深度不应到达实测: %+v", fp.in)
	}

	// 可用性只能由对话请求证明：没填模型直接拒绝，不发任何上游请求
	fp.in = ProviderProbeSpec{}
	noModel := map[string]any{"type": "anthropic_messages", "base_url": "https://x", "api_key": "k", "default_model": "  "}
	if code, body := doJSON(t, "POST", srv.URL+"/api/models/providers/diagnose", noModel); code != 400 {
		t.Fatalf("未填模型应 400，got %d (%v)", code, body)
	}
	if fp.in.Type != "" {
		t.Fatalf("未填模型不应调用实测: %+v", fp.in)
	}
}

func TestListProviderModelsHandler(t *testing.T) {
	fp := &fakeProber{list: ModelList{Models: []string{"m-a", "m-b"}, Status: StepOK, Request: "GET https://x/models"}}
	srv := newTestServer(t, Deps{Models: &fakeModel{}, ProviderProber: fp})
	defer srv.Close()

	// 不要求模型：拉列表正是为了选模型
	code, body := doJSON(t, "POST", srv.URL+"/api/models/providers/models",
		map[string]any{"type": "openai_chat_completions", "base_url": "https://x/v1", "code": "p1", "auth_scheme": "x-api-key"})
	if code != 200 || body["status"] != StepOK {
		t.Fatalf("列表结果应原样返回: %d %v", code, body)
	}
	if models, _ := body["models"].([]any); len(models) != 2 {
		t.Fatalf("应带回模型: %v", body)
	}
	if fp.in.Code != "p1" || fp.in.AuthScheme != "x-api-key" || fp.in.BaseURL != "https://x/v1" {
		t.Fatalf("规格应透传: %+v", fp.in)
	}
}
