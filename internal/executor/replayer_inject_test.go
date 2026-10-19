package executor

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/V3teran/liusha/internal/credential"
)

// 注入身份选择：持久身份优先于自动会话快照——HGetAll 返回序任意，盲取 [0]
// 曾把 security 停在 impossible 的任务临时会话注入复放，全部假设打到修复版
// 难度整体证伪。
func TestInjectSessionContext_PrefersDurableIdentity(t *testing.T) {
	// 故意按"自动快照在前"排列（模拟 HGetAll 任意序的不利情况）
	r := NewReplayer().WithCredentials(&stubCredProvider{identities: []credential.Identity{
		{
			Name: "task:t1:session",
			Credentials: []credential.Credential{
				{Type: credential.TypeHeaders, Key: "Cookie", Value: "PHPSESSID=auto; security=impossible"},
			},
		},
		{
			Name: "admin_low",
			Credentials: []credential.Credential{
				{Type: credential.TypeHeaders, Key: "Cookie", Value: "PHPSESSID=durable; security=low"},
			},
		},
	}}, "t.local:8080")

	recipe := json.RawMessage(`{"request":{"method":"GET","url":"http://t.local:8080/xss_r/?name=p"},"baseline":null}`)
	out := r.injectSessionContext(context.Background(), recipe)

	var rc struct {
		Request struct {
			Headers map[string]string `json:"headers"`
		} `json:"request"`
	}
	if err := json.Unmarshal(out, &rc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	got := rc.Request.Headers["cookie"]
	if got != "PHPSESSID=durable; security=low" {
		t.Fatalf("应注入持久身份的凭据，got: %q", got)
	}
}

// 无持久身份时自动快照兜底；配方自带的 cookie 键不被覆盖。
func TestInjectSessionContext_FallbackAndExplicitWin(t *testing.T) {
	r := NewReplayer().WithCredentials(&stubCredProvider{identities: []credential.Identity{
		{Name: "task:t1:session", Credentials: []credential.Credential{
			{Type: credential.TypeHeaders, Key: "Cookie", Value: "PHPSESSID=auto; security=low"},
		}},
	}}, "t.local:8080")

	// 无自带 cookie：快照兜底
	out := r.injectSessionContext(context.Background(), json.RawMessage(`{"request":{"method":"GET","url":"http://t.local:8080/x"}}`))
	var rc struct {
		Request struct {
			Headers map[string]string `json:"headers"`
		} `json:"request"`
	}
	if err := json.Unmarshal(out, &rc); err != nil || rc.Request.Headers["cookie"] != "PHPSESSID=auto; security=low" {
		t.Fatalf("无持久身份应回落自动快照: %v %q", err, rc.Request.Headers["cookie"])
	}

	// 配方自带 cookie：不覆盖
	out2 := r.injectSessionContext(context.Background(), json.RawMessage(`{"request":{"method":"GET","url":"http://t.local:8080/x","headers":{"cookie":"PHPSESSID=mine; security=low"}}}`))
	var rc2 struct {
		Request struct {
			Headers map[string]string `json:"headers"`
		} `json:"request"`
	}
	if err := json.Unmarshal(out2, &rc2); err != nil || rc2.Request.Headers["cookie"] != "PHPSESSID=mine; security=low" {
		t.Fatalf("配方自带 cookie 优先: %v %q", err, rc2.Request.Headers["cookie"])
	}
}

type stubCredProvider struct{ identities []credential.Identity }

func (p *stubCredProvider) GetIdentitiesByHost(context.Context, string) ([]credential.Identity, error) {
	return p.identities, nil
}

func (p *stubCredProvider) BatchSave(_ context.Context, _ map[string][]credential.Identity, _ int) error {
	return nil
}

func (p *stubCredProvider) Delete(_ context.Context, _ string) error {
	return nil
}
