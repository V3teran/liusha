package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"
)

func serve(t *testing.T, h http.HandlerFunc) string {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv.URL
}

// TestLoginLiusha_Failures 验证登录的失败分支都报错：非 200、200 但无会话 Cookie。
func TestLoginLiusha_Failures(t *testing.T) {
	cases := map[string]http.HandlerFunc{
		"密码错误": func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusUnauthorized) },
		"无 Cookie": func(w http.ResponseWriter, r *http.Request) {
			http.SetCookie(w, &http.Cookie{Name: "other", Value: "x"})
		},
	}
	for name, h := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := loginLiusha(t.Context(), serve(t, h), "admin", "pw", nil); err == nil {
				t.Fatal("期望登录失败")
			}
		})
	}
}

// TestLoginLiusha_Unreachable 验证 liusha 未启动时登录报错而非挂起。
func TestLoginLiusha_Unreachable(t *testing.T) {
	if _, err := loginLiusha(t.Context(), "http://127.0.0.1:1", "a", "b", nil); err == nil {
		t.Fatal("期望连接失败")
	}
}

// TestLiushaClient_CreateTaskErrors 验证建任务的失败分支：非 2xx 带上后端原因、
// 未返回 task_id（brief 被判为非扫描意图）、响应不是 JSON。
func TestLiushaClient_CreateTaskErrors(t *testing.T) {
	cases := []struct {
		name, body, wantSub string
		status              int
	}{
		{"后端拒绝", `{"error":"LLM 未配置"}`, "LLM 未配置", http.StatusBadRequest},
		{"无 task_id", `{"tasks":[]}`, "task_id", http.StatusCreated},
		{"非 JSON", `<html>`, "解析", http.StatusOK},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			url := serve(t, func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			})
			c := &liushaClient{base: url, session: &http.Cookie{Name: liushaSessionCookie, Value: "s"}, client: http.DefaultClient}
			_, err := c.createTask(t.Context(), "t", "brief")
			if err == nil || !strings.Contains(err.Error(), tc.wantSub) {
				t.Fatalf("err=%v，期望包含 %q", err, tc.wantSub)
			}
		})
	}
}

// TestLiushaClient_CreateTaskRejectsSplit 验证一个 brief 建出多个任务时（brief 含换行被按行拆）
// 报错，并把建出的任务全部终止——否则未被跟踪的任务会在后台持续消耗 LLM 额度。
func TestLiushaClient_CreateTaskRejectsSplit(t *testing.T) {
	var terminated []string
	url := serve(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/tasks":
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"tasks":[{"task_id":"a"},{"task_id":"b"}]}`))
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/control"):
			terminated = append(terminated, strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/tasks/"), "/control"))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})
	c := &liushaClient{base: url, session: &http.Cookie{Name: liushaSessionCookie, Value: "s"}, client: http.DefaultClient}
	_, err := c.createTask(t.Context(), "t", "brief")
	if err == nil || !strings.Contains(err.Error(), "2 个任务") {
		t.Fatalf("err=%v，期望报告建出了 2 个任务", err)
	}
	if !slices.Equal(terminated, []string{"a", "b"}) {
		t.Fatalf("应终止全部建出的任务，实际终止 %v", terminated)
	}
}

// TestBenchClient_NonBusinessError 验证非统一错误体的非 2xx（如网关 HTML、422）返回裸状态错误。
func TestBenchClient_NonBusinessError(t *testing.T) {
	url := serve(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte("<html>bad gateway</html>"))
	})
	_, err := newBenchClient(url, "tok", nil).listChallenges(t.Context())
	if err == nil || !strings.Contains(err.Error(), "HTTP 502") {
		t.Fatalf("err=%v", err)
	}
	var be *benchError
	if errors.As(err, &be) {
		t.Fatalf("不应解析为业务错误：%v", be)
	}
}

// TestBenchClient_ListChallenges 验证列题响应被解码。
func TestBenchClient_ListChallenges(t *testing.T) {
	url := serve(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/openapi/v1/challenges" {
			t.Errorf("path=%s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`[{"unique_code":"web_01","difficulty":"easy","flag_count":1,"container_status":"stopped"}]`))
	})
	chs, err := newBenchClient(url+"/", "tok", nil).listChallenges(t.Context())
	if err != nil || len(chs) != 1 || chs[0].UniqueCode != "web_01" || chs[0].FlagCount != 1 {
		t.Fatalf("chs=%+v err=%v", chs, err)
	}
}

// TestBenchError_Retriable 验证可重试错误码的判定。
func TestBenchError_Retriable(t *testing.T) {
	for code, want := range map[string]bool{
		codeResourceUnavailable: true, codeInternalError: true, codeDuplicate: false, codeTaskNotFound: false,
	} {
		if got := (&benchError{Code: code}).retriable(); got != want {
			t.Errorf("%s retriable=%v, want %v", code, got, want)
		}
	}
	if !strings.Contains((&benchError{HTTPStatus: 503, Code: "x", Message: "m"}).Error(), "503") {
		t.Error("Error() 应带 HTTP 状态码")
	}
}

// TestVPNPrecheck 验证 VPN 预检只在 200 且 status=ok 时通过。
func TestVPNPrecheck(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
		wantOK bool
	}{
		{"连通", http.StatusOK, `{"client_ip":"10.8.0.2","status":"ok"}`, true},
		{"状态异常", http.StatusOK, `{"status":"down"}`, false},
		{"非 200", http.StatusForbidden, `{"status":"ok"}`, false},
		{"非 JSON（不是靶场网关）", http.StatusOK, `<html>`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			url := serve(t, func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			})
			err := vpnPrecheck(t.Context(), http.DefaultClient, url)
			if (err == nil) != tc.wantOK {
				t.Fatalf("err=%v, wantOK=%v", err, tc.wantOK)
			}
		})
	}
	if err := vpnPrecheck(t.Context(), &http.Client{Timeout: time.Second}, "http://127.0.0.1:1"); err == nil {
		t.Fatal("不可达时应失败")
	}
}

// TestReleaseLeftovers 验证只释放容器处于 pending / available 的遗留靶场。
func TestReleaseLeftovers(t *testing.T) {
	f := newFakeStack(t)
	chs := []challenge{
		{UniqueCode: "a", ContainerStatus: "available"},
		{UniqueCode: "b", ContainerStatus: "stopped"},
		{UniqueCode: "c", ContainerStatus: "pending"},
		{UniqueCode: "d", ContainerStatus: "stop_pending"},
	}
	releaseLeftovers(t.Context(), newBenchClient(f.srv.URL, "tok", nil), zerolog.Nop(), chs)
	_, closes, _, _, _ := f.snapshot()
	if !slices.Equal(closes, []string{"a", "c"}) {
		t.Fatalf("closes=%v", closes)
	}
}

// TestLoadConfig 验证必填项校验、缺省值与并发上限钳制。
func TestLoadConfig(t *testing.T) {
	set := func(t *testing.T, kv map[string]string) {
		for _, k := range []string{"BENCHMARK_BASE_URL", "BENCHMARK_TOKEN", "LIUSHA_TSB_PASS",
			"LIUSHA_ADMIN_INITIAL_PASSWORD", "LIUSHA_TSB_CONCURRENCY", "LIUSHA_TSB_POLL_SECONDS"} {
			t.Setenv(k, kv[k])
		}
	}
	full := map[string]string{"BENCHMARK_BASE_URL": "https://b", "BENCHMARK_TOKEN": "tok", "LIUSHA_ADMIN_INITIAL_PASSWORD": "pw"}

	for _, missing := range []string{"BENCHMARK_BASE_URL", "BENCHMARK_TOKEN", "LIUSHA_ADMIN_INITIAL_PASSWORD"} {
		t.Run("缺 "+missing, func(t *testing.T) {
			kv := map[string]string{}
			for k, v := range full {
				if k != missing {
					kv[k] = v
				}
			}
			set(t, kv)
			if _, err := loadConfig(); err == nil {
				t.Fatalf("缺 %s 应报错", missing)
			}
		})
	}

	t.Run("缺省值", func(t *testing.T) {
		set(t, full)
		cfg, err := loadConfig()
		if err != nil {
			t.Fatal(err)
		}
		if cfg.concurrency != 3 || cfg.liushaBase != "http://127.0.0.1:8580" || cfg.liushaPass != "pw" ||
			cfg.pollInterval != 20*time.Second || cfg.probeURL != defaultProbeURL {
			t.Fatalf("cfg=%+v", cfg)
		}
	})

	for in, want := range map[string]int{"9": 3, "0": 1, "2": 2, "abc": 3} {
		t.Run("并发 "+in, func(t *testing.T) {
			kv := map[string]string{"LIUSHA_TSB_CONCURRENCY": in}
			for k, v := range full {
				kv[k] = v
			}
			set(t, kv)
			cfg, err := loadConfig()
			if err != nil || cfg.concurrency != want {
				t.Fatalf("concurrency=%d, want %d (err=%v)", cfg.concurrency, want, err)
			}
		})
	}
}

// TestLiushaClient_CreateTaskUsesCTFScenario 验证跑分任务以 ctf 场景下发（flag 回报规约在场景里）。
func TestLiushaClient_CreateTaskUsesCTFScenario(t *testing.T) {
	var got map[string]any
	url := serve(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&got)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"tasks":[{"task_id":"a"}]}`))
	})
	c := &liushaClient{base: url, session: &http.Cookie{Name: liushaSessionCookie, Value: "s"}, client: http.DefaultClient}
	if _, err := c.createTask(t.Context(), "t", "brief"); err != nil {
		t.Fatal(err)
	}
	if got["scenario"] != "ctf" {
		t.Fatalf("应以 ctf 场景下发，请求体 %v", got)
	}
}
