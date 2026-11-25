package httpapi

import (
	"encoding/json"
	"net/http"
	"testing"
)

// 控制台侧栏显示的程序版本：与 liusha version 同一个值；要登录才看得到（不对匿名访客
// 暴露版本，版本号是攻击者挑利用的依据）。
func TestVersionHandler(t *testing.T) {
	srv := newTestServer(t, Deps{Version: "0.15.0"})
	defer srv.Close()

	resp := doReq(t, "GET", srv.URL+"/api/version", "")
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	var body map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["version"] != "0.15.0" {
		t.Errorf("version 得 %q，期望 0.15.0", body["version"])
	}

	anonymous, err := http.Get(srv.URL + "/api/version")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer func() { _ = anonymous.Body.Close() }()
	if anonymous.StatusCode != http.StatusUnauthorized {
		t.Errorf("未登录应 401，得 %d", anonymous.StatusCode)
	}
}
