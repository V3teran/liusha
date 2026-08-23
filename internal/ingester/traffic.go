// Package ingester — TrafficIngester，并行于 Cognition 主循环监听 MITM 代理流量。
//
// 流量事件 → Signal{Kind: http_trace} → 直接晋升 endpoint/service Landmark（不经 Actor）
package ingester

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/V3teran/liusha/internal/actor"
	"github.com/V3teran/liusha/internal/registry"
)

// LandmarkWriter 是 TrafficIngester 依赖的写入口（*ledger.Ledger 满足）。
type LandmarkWriter interface {
	Write(ctx context.Context, lm actor.Landmark) (actor.Landmark, error)
}

// TrafficEvent 是 MITM 代理推送的单条 HTTP 事件（JSON Lines 格式）。
type TrafficEvent struct {
	Method     string `json:"method"`
	URL        string `json:"url"`
	StatusCode int    `json:"status_code"`
	Host       string `json:"host"`
	Path       string `json:"path"`
	ReqBody    string `json:"req_body,omitempty"`
	RespBody   string `json:"resp_body,omitempty"`
	DurationMs int64  `json:"duration_ms"`
	CapturedAt string `json:"captured_at"`
}

// TrafficIngester 监听 MITM 代理事件流，将流量晋升为 Landmark。
type TrafficIngester struct {
	taskID string
	ledger LandmarkWriter
}

// New 构造 TrafficIngester。
func New(taskID string, ledger LandmarkWriter) *TrafficIngester {
	return &TrafficIngester{taskID: taskID, ledger: ledger}
}

// Watch 阻塞监听 proxyEventsURL（SSE 或 JSON Lines 流）直到 ctx 取消。
// 每条 TrafficEvent 构造一个 http_trace Signal，直接写入 Ledger。
// 调用方应在 goroutine 中运行：go ingester.Watch(ctx, proxyEventsURL)
func (t *TrafficIngester) Watch(ctx context.Context, proxyEventsURL string) {
	for {
		if ctx.Err() != nil {
			return
		}
		if err := t.stream(ctx, proxyEventsURL); err != nil {
			if ctx.Err() != nil {
				return
			}
			slog.WarnContext(ctx, "traffic ingester: stream error, retrying",
				"err", err, "url", proxyEventsURL)
			select {
			case <-ctx.Done():
				return
			case <-time.After(3 * time.Second):
			}
		}
	}
}

func (t *TrafficIngester) stream(ctx context.Context, url string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("ingester: build request: %w", err)
	}
	req.Header.Set("Accept", "application/x-ndjson")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("ingester: connect: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("ingester: proxy status %d", resp.StatusCode)
	}

	return t.readLines(ctx, resp.Body)
}

func (t *TrafficIngester) readLines(ctx context.Context, r io.Reader) error {
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		var ev TrafficEvent
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			slog.DebugContext(ctx, "ingester: skip malformed event", "line", line)
			continue
		}
		t.handleEvent(ctx, ev)
	}
	return scanner.Err()
}

func (t *TrafficIngester) handleEvent(ctx context.Context, ev TrafficEvent) {
	if ev.URL == "" {
		return
	}

	// 构造 http_trace Signal
	content := fmt.Sprintf("%s %s → %d", ev.Method, ev.URL, ev.StatusCode)
	detail := fmt.Sprintf("method=%s url=%s status=%d duration_ms=%d",
		ev.Method, ev.URL, ev.StatusCode, ev.DurationMs)

	capturedAt := time.Now()
	if ev.CapturedAt != "" {
		if parsed, err := time.Parse(time.RFC3339, ev.CapturedAt); err == nil {
			capturedAt = parsed
		}
	}

	sig := registry.Signal{
		Kind:       registry.SignalHTTPTrace,
		ToolName:   "mitmproxy",
		Content:    content,
		Detail:     detail,
		CapturedAt: capturedAt,
	}

	// 构造 endpoint Landmark（service 由 host 推导）
	locator := ev.URL
	kind := actor.LandmarkEndpoint
	if ev.Path == "" || ev.Path == "/" {
		kind = actor.LandmarkService
	}

	lm := actor.Landmark{
		Ref: actor.LandmarkRef{
			Domain:  "web",
			RefKind: string(kind),
			Locator: locator,
		},
		Kind:    kind,
		State:   actor.LandmarkConfirmed, // 流量即证实，直接 confirmed
		Summary: content,
		Signals: []registry.Signal{sig},
		TaskID:  t.taskID,
	}
	lm.Confidence = actor.CalcConfidence(lm.Signals)

	if _, err := t.ledger.Write(ctx, lm); err != nil {
		slog.WarnContext(ctx, "ingester: ledger write error", "err", err, "url", ev.URL)
	}
}
