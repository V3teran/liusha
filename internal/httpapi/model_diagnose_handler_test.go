package httpapi

import (
	"context"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5"
)

// fakeDiagnoser 记录收到的规格并返回预置结果。
type fakeDiagnoser struct {
	got Diagnosis
	err error
	in  ProviderProbeSpec
}

func (f *fakeDiagnoser) Diagnose(_ context.Context, spec ProviderProbeSpec) (Diagnosis, error) {
	f.in = spec
	return f.got, f.err
}

func TestDiagnoseProviderHandler(t *testing.T) {
	valid := map[string]any{"type": "anthropic", "base_url": "https://api.example.com/anthropic", "api_key": "k", "default_model": " m ", "auth_scheme": "bearer"}
	cases := []struct {
		name string
		fd   *fakeDiagnoser
		body map[string]any
		want int
	}{
		{"诊断结果原样返回", &fakeDiagnoser{got: Diagnosis{OK: true, Steps: []DiagnosisStep{{Key: StepEndpoint, Status: StepOK}}}}, valid, 200},
		{"未选协议", &fakeDiagnoser{}, map[string]any{"base_url": "https://x", "api_key": "k"}, 400},
		{"Base URL 非法", &fakeDiagnoser{}, map[string]any{"type": "anthropic", "base_url": "x", "api_key": "k"}, 400},
		{"无密钥来源", &fakeDiagnoser{}, map[string]any{"type": "anthropic", "base_url": "https://x"}, 400},
		{"已存 provider 不存在", &fakeDiagnoser{err: fmt.Errorf("取已存 provider: %w", pgx.ErrNoRows)}, map[string]any{"type": "anthropic", "base_url": "https://x", "code": "ghost"}, 404},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := newTestServer(t, Deps{Models: &fakeModel{}, ProviderDiagnoser: tc.fd})
			defer srv.Close()
			code, body := doJSON(t, "POST", srv.URL+"/api/models/providers/diagnose", tc.body)
			if code != tc.want {
				t.Fatalf("want %d, got %d (%v)", tc.want, code, body)
			}
		})
	}

	fd := &fakeDiagnoser{}
	srv := newTestServer(t, Deps{Models: &fakeModel{}, ProviderDiagnoser: fd})
	defer srv.Close()
	doJSON(t, "POST", srv.URL+"/api/models/providers/diagnose", valid)
	if fd.in.DefaultModel != "m" || fd.in.AuthScheme != "bearer" || fd.in.Type != "anthropic" {
		t.Fatalf("规格应透传（模型名去空白）: %+v", fd.in)
	}
}
