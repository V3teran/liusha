package runner

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rs/zerolog"

	"github.com/V3teran/liusha/internal/proxy"
)

type captureSink struct{ got []*proxy.TrafficSnapshot }

func (s *captureSink) SubmitInternal(snap *proxy.TrafficSnapshot) bool {
	s.got = append(s.got, snap)
	return true
}

const ingestBody = `{"agent_run_id":"run-1","host":"target.local","method":"GET","scheme":"http","uri":"/","path":"/","status_code":200}`

func postIngest(h http.HandlerFunc, auth string) int {
	return postIngestBody(h, auth, ingestBody)
}

func postIngestBody(h http.HandlerFunc, auth, body string) int {
	req := httptest.NewRequest(http.MethodPost, "/internal/v1/traffic/ingest", strings.NewReader(body))
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	rec := httptest.NewRecorder()
	h(rec, req)
	return rec.Code
}

// 回灌端点只认沙盒令牌；未配置令牌时拒绝一切（不再有「不验证」的开发模式）。
func TestIngestHandler_RequiresToken(t *testing.T) {
	sink := &captureSink{}
	h := NewIngestHandler(sink, "s3cret", zerolog.Nop())
	for _, auth := range []string{"", "Bearer wrong", "s3cret"} {
		if code := postIngest(h, auth); code != http.StatusUnauthorized {
			t.Fatalf("Authorization=%q: status %d, 期望 401", auth, code)
		}
	}
	if code := postIngest(h, "Bearer s3cret"); code != http.StatusOK || len(sink.got) != 1 {
		t.Fatalf("正确令牌应入队: status=%d got=%d", code, len(sink.got))
	}

	open := NewIngestHandler(sink, "", zerolog.Nop())
	if code := postIngest(open, "Bearer "); code != http.StatusUnauthorized {
		t.Fatalf("未配置令牌应拒绝, status %d", code)
	}
}

// 回灌流量按 run 归属：ingestor 以 agent_run_id 反查任务，缺失即无从归属。
func TestIngestHandler_AttributesTrafficToRun(t *testing.T) {
	sink := &captureSink{}
	h := NewIngestHandler(sink, "s3cret", zerolog.Nop())
	if code := postIngest(h, "Bearer s3cret"); code != http.StatusOK || len(sink.got) != 1 {
		t.Fatalf("status=%d got=%d", code, len(sink.got))
	}
	if sink.got[0].AgentRunID != "run-1" {
		t.Fatalf("AgentRunID = %q，期望 run-1", sink.got[0].AgentRunID)
	}
	missing := `{"host":"target.local","method":"GET","scheme":"http","uri":"/","path":"/","status_code":200}`
	if code := postIngestBody(h, "Bearer s3cret", missing); code != http.StatusBadRequest {
		t.Fatalf("缺 agent_run_id 应 400，got %d", code)
	}
}
