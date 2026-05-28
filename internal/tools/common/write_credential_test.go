package common

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/V3teran/liusha/internal/credential"
)

// fakeProvider 实现 credential.Provider 给单测用，记录最近一次 BatchSave 入参。
type fakeProvider struct {
	saved      map[string][]credential.Identity
	ttl        int
	saveErr    error
	identities map[string][]credential.Identity // 给 GetIdentitiesByHost 用（write_credential 不调，留空即可）
}

func (f *fakeProvider) BatchSave(_ context.Context, byHost map[string][]credential.Identity, ttlSeconds int) error {
	if f.saveErr != nil {
		return f.saveErr
	}
	f.saved = byHost
	f.ttl = ttlSeconds
	return nil
}
func (f *fakeProvider) GetIdentitiesByHost(_ context.Context, host string) ([]credential.Identity, error) {
	return f.identities[host], nil
}
func (f *fakeProvider) List(_ context.Context, host string) (map[string][]credential.Identity, error) {
	return map[string][]credential.Identity{host: f.identities[host]}, nil
}
func (f *fakeProvider) Delete(_ context.Context, _ string) error { return nil }

func TestWriteCredential_Success(t *testing.T) {
	fp := &fakeProvider{}
	tool := &WriteCredential{Provider: fp, Host: "target.com"}

	args := json.RawMessage(`{
		"name":"admin",
		"role":"admin",
		"credentials":[
			{"type":"headers","key":"Cookie","value":"PHPSESSID=abc; security=low"},
			{"type":"headers","key":"Authorization","value":"Bearer xyz"},
			{"type":"body","key":"csrf_token","value":"deadbeef"}
		]
	}`)
	res, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("Execute err=%v", err)
	}
	if fp.ttl != 0 {
		t.Errorf("ttl=%d want 0（永久）", fp.ttl)
	}
	ids := fp.saved["target.com"]
	if len(ids) != 1 {
		t.Fatalf("saved identities=%d want 1", len(ids))
	}
	id := ids[0]
	if id.Name != "admin" || id.Role != "admin" {
		t.Errorf("name/role: got %q/%q", id.Name, id.Role)
	}
	if len(id.Credentials) != 3 {
		t.Fatalf("credentials len=%d want 3", len(id.Credentials))
	}
	if id.Credentials[0].Type != credential.TypeHeaders || id.Credentials[0].Key != "Cookie" {
		t.Errorf("creds[0]: %+v", id.Credentials[0])
	}
	if id.Credentials[2].Type != credential.TypeBody {
		t.Errorf("creds[2].type=%q want body", id.Credentials[2].Type)
	}
	if !strings.Contains(res.Summary, "host=target.com") || !strings.Contains(res.Summary, "name=admin") {
		t.Errorf("summary=%q", res.Summary)
	}
}

func TestWriteCredential_RejectAnonymous(t *testing.T) {
	tool := &WriteCredential{Provider: &fakeProvider{}, Host: "x.com"}
	args := json.RawMessage(`{"name":"anonymous","credentials":[{"type":"headers","key":"X","value":"y"}]}`)
	_, err := tool.Execute(context.Background(), args)
	if err == nil {
		t.Fatal("anonymous 应被拒")
	}
	if !strings.Contains(err.Error(), "anonymous") {
		t.Errorf("err=%q want mention anonymous", err.Error())
	}
}

func TestWriteCredential_RejectEmptyName(t *testing.T) {
	tool := &WriteCredential{Provider: &fakeProvider{}, Host: "x.com"}
	cases := []string{
		`{"name":"","credentials":[{"type":"headers","key":"X","value":"y"}]}`,
		`{"name":"   ","credentials":[{"type":"headers","key":"X","value":"y"}]}`,
	}
	for i, c := range cases {
		if _, err := tool.Execute(context.Background(), json.RawMessage(c)); err == nil {
			t.Errorf("case %d 应被拒（空 name）", i)
		}
	}
}

func TestWriteCredential_RejectEmptyCredentials(t *testing.T) {
	tool := &WriteCredential{Provider: &fakeProvider{}, Host: "x.com"}
	args := json.RawMessage(`{"name":"admin","credentials":[]}`)
	_, err := tool.Execute(context.Background(), args)
	if err == nil || !strings.Contains(err.Error(), "至少 1 条") {
		t.Errorf("空 credentials 应被拒，err=%v", err)
	}
}

func TestWriteCredential_RejectInvalidType(t *testing.T) {
	tool := &WriteCredential{Provider: &fakeProvider{}, Host: "x.com"}
	args := json.RawMessage(`{"name":"admin","credentials":[{"type":"path","key":"X","value":"y"}]}`)
	_, err := tool.Execute(context.Background(), args)
	if err == nil || !strings.Contains(err.Error(), "非法") {
		t.Errorf("非法 type 应被拒，err=%v", err)
	}
}

func TestWriteCredential_RejectEmptyKey(t *testing.T) {
	tool := &WriteCredential{Provider: &fakeProvider{}, Host: "x.com"}
	args := json.RawMessage(`{"name":"admin","credentials":[{"type":"headers","key":"","value":"y"}]}`)
	_, err := tool.Execute(context.Background(), args)
	if err == nil || !strings.Contains(err.Error(), "key 必填") {
		t.Errorf("空 key 应被拒，err=%v", err)
	}
}

func TestWriteCredential_MissingHostInjection(t *testing.T) {
	tool := &WriteCredential{Provider: &fakeProvider{}, Host: ""}
	args := json.RawMessage(`{"name":"admin","credentials":[{"type":"headers","key":"X","value":"y"}]}`)
	_, err := tool.Execute(context.Background(), args)
	if err == nil || !strings.Contains(err.Error(), "Host 未注入") {
		t.Errorf("无 Host 应被拒，err=%v", err)
	}
}

func TestWriteCredential_ProviderError(t *testing.T) {
	saveErr := errors.New("redis down")
	tool := &WriteCredential{Provider: &fakeProvider{saveErr: saveErr}, Host: "x.com"}
	args := json.RawMessage(`{"name":"admin","credentials":[{"type":"headers","key":"X","value":"y"}]}`)
	_, err := tool.Execute(context.Background(), args)
	if err == nil || !strings.Contains(err.Error(), "redis down") {
		t.Errorf("provider 错应透传，err=%v", err)
	}
}

func TestWriteCredential_OverwriteSameName(t *testing.T) {
	fp := &fakeProvider{}
	tool := &WriteCredential{Provider: fp, Host: "x.com"}
	args1 := json.RawMessage(`{"name":"admin","credentials":[{"type":"headers","key":"Cookie","value":"v1"}]}`)
	args2 := json.RawMessage(`{"name":"admin","credentials":[{"type":"headers","key":"Cookie","value":"v2"}]}`)
	if _, err := tool.Execute(context.Background(), args1); err != nil {
		t.Fatalf("write 1: %v", err)
	}
	if _, err := tool.Execute(context.Background(), args2); err != nil {
		t.Fatalf("write 2: %v", err)
	}
	saved := fp.saved["x.com"][0].Credentials[0].Value
	if saved != "v2" {
		t.Errorf("覆盖后 value=%q want v2", saved)
	}
}
