package core

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// ─────────────────────────────────────────────
//  Profiler 测试
// ─────────────────────────────────────────────

func TestProfiler_Creation(t *testing.T) {
	config := DefaultProfilerConfig()
	profiler := NewProfiler(config)

	if profiler == nil {
		t.Fatal("profiler 不应为 nil")
	}

	if profiler.config.Enabled != config.Enabled {
		t.Error("配置应该被正确设置")
	}
}

func TestProfiler_DisabledCreation(t *testing.T) {
	config := ProfilerConfig{
		Enabled: false,
	}
	profiler := NewProfiler(config)

	if profiler == nil {
		t.Fatal("即使禁用，profiler 也不应为 nil")
	}
}

func TestProfiler_Handler(t *testing.T) {
	config := DefaultProfilerConfig()
	profiler := NewProfiler(config)
	handler := profiler.Handler()

	if handler == nil {
		t.Fatal("handler 不应为 nil")
	}

	// 测试索引页
	req := httptest.NewRequest("GET", "/debug/pprof/", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	resp := w.Result()
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("状态码 = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("读取响应失败: %v", err)
	}

	bodyStr := string(body)
	if !strings.Contains(bodyStr, "pprof") {
		t.Error("索引页应包含 'pprof'")
	}
	if !strings.Contains(bodyStr, "CPU Profile") {
		t.Error("索引页应包含 'CPU Profile'")
	}
}

func TestProfiler_HeapProfile(t *testing.T) {
	config := DefaultProfilerConfig()
	profiler := NewProfiler(config)
	handler := profiler.Handler()

	req := httptest.NewRequest("GET", "/debug/pprof/heap", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	resp := w.Result()
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("状态码 = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	contentType := resp.Header.Get("Content-Type")
	if contentType != "application/octet-stream" {
		t.Errorf("Content-Type = %s, want application/octet-stream", contentType)
	}
}

func TestProfiler_GoroutineProfile(t *testing.T) {
	config := DefaultProfilerConfig()
	profiler := NewProfiler(config)
	handler := profiler.Handler()

	req := httptest.NewRequest("GET", "/debug/pprof/goroutine", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	resp := w.Result()
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("状态码 = %d, want %d", resp.StatusCode, http.StatusOK)
	}
}

func TestProfiler_CPUProfile(t *testing.T) {
	if testing.Short() {
		t.Skip("跳过耗时的 CPU profile 测试")
	}

	config := DefaultProfilerConfig()
	profiler := NewProfiler(config)
	handler := profiler.Handler()

	// CPU profile 需要采样时间，设置为 1 秒
	req := httptest.NewRequest("GET", "/debug/pprof/profile?seconds=1", nil)
	w := httptest.NewRecorder()

	// 在后台运行，避免阻塞
	done := make(chan bool)
	go func() {
		handler.ServeHTTP(w, req)
		done <- true
	}()

	// 等待完成或超时
	select {
	case <-done:
		resp := w.Result()
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Errorf("状态码 = %d, want %d", resp.StatusCode, http.StatusOK)
		}
	case <-time.After(3 * time.Second):
		t.Error("CPU profile 超时")
	}
}

func TestProfiler_GetRuntimeStats(t *testing.T) {
	config := DefaultProfilerConfig()
	profiler := NewProfiler(config)

	stats := profiler.GetRuntimeStats()

	// 验证基本字段
	if stats.NumGoroutine <= 0 {
		t.Error("NumGoroutine 应该 > 0")
	}

	if stats.Alloc == 0 {
		t.Error("Alloc 应该 > 0")
	}

	if stats.Sys == 0 {
		t.Error("Sys 应该 > 0")
	}

	// TotalAlloc 应该 >= Alloc
	if stats.TotalAlloc < stats.Alloc {
		t.Error("TotalAlloc 应该 >= Alloc")
	}
}

func TestProfiler_NotFoundHandler(t *testing.T) {
	config := DefaultProfilerConfig()
	profiler := NewProfiler(config)
	handler := profiler.Handler()

	req := httptest.NewRequest("GET", "/debug/pprof/nonexistent", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	resp := w.Result()
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("状态码 = %d, want %d", resp.StatusCode, http.StatusNotFound)
	}
}

// ─────────────────────────────────────────────
//  ProfilingCallback 测试
// ─────────────────────────────────────────────

func TestProfilingCallback_Creation(t *testing.T) {
	config := DefaultProfilerConfig()
	profiler := NewProfiler(config)
	callback := NewProfilingCallback(profiler)

	if callback == nil {
		t.Fatal("callback 不应为 nil")
	}

	if callback.profiler != profiler {
		t.Error("profiler 应该被正确设置")
	}
}

func TestProfilingCallback_AgentLifecycle(t *testing.T) {
	config := DefaultProfilerConfig()
	profiler := NewProfiler(config)
	callback := NewProfilingCallback(profiler)
	ctx := context.Background()

	// 记录启动前状态
	statsBefore := profiler.GetRuntimeStats()

	// Agent 启动
	callback.OnAgentStart(ctx, AgentStartEvent{
		TaskID:    "task-1",
		AgentName: "test-agent",
		StartTime: time.Now(),
	})

	// Agent 结束
	callback.OnAgentEnd(ctx, AgentEndEvent{
		TaskID:    "task-1",
		AgentName: "test-agent",
		Duration:  100 * time.Millisecond,
		Error:     nil,
		StartTime: time.Now(),
	})

	// 记录结束后状态
	statsAfter := profiler.GetRuntimeStats()

	// 基本验证：应该能获取到统计信息
	if statsAfter.NumGoroutine < 0 {
		t.Error("NumGoroutine 不应为负数")
	}

	// TotalAlloc 应该是递增的
	if statsAfter.TotalAlloc < statsBefore.TotalAlloc {
		t.Error("TotalAlloc 应该递增")
	}
}

// ─────────────────────────────────────────────
//  DefaultProfilerConfig 测试
// ─────────────────────────────────────────────

func TestDefaultProfilerConfig(t *testing.T) {
	config := DefaultProfilerConfig()

	if !config.Enabled {
		t.Error("默认应该启用")
	}

	if config.CPUProfileRate <= 0 {
		t.Error("CPUProfileRate 应该 > 0")
	}

	if config.MemProfileRate <= 0 {
		t.Error("MemProfileRate 应该 > 0")
	}

	if config.MutexProfileFraction < 0 {
		t.Error("MutexProfileFraction 应该 >= 0")
	}

	if config.BlockProfileRate < 0 {
		t.Error("BlockProfileRate 应该 >= 0")
	}
}

// ─────────────────────────────────────────────
//  集成测试
// ─────────────────────────────────────────────

func TestProfiler_RuntimeStatsOverTime(t *testing.T) {
	if testing.Short() {
		t.Skip("跳过耗时测试")
	}

	config := DefaultProfilerConfig()
	profiler := NewProfiler(config)

	// 记录初始状态
	stats1 := profiler.GetRuntimeStats()

	// 执行一些工作
	data := make([]byte, 1024*1024) // 分配 1MB
	for i := range data {
		data[i] = byte(i)
	}

	// 等待一段时间
	time.Sleep(100 * time.Millisecond)

	// 记录新状态
	stats2 := profiler.GetRuntimeStats()

	// 验证内存分配增加
	if stats2.TotalAlloc <= stats1.TotalAlloc {
		t.Error("TotalAlloc 应该增加")
	}

	// 验证 Alloc 可能增加（取决于 GC）
	// 注意：Alloc 可能减少（如果发生了 GC），所以不做强制检查

	// 验证 NumGC 可能增加
	// 注意：NumGC 也可能不变，取决于是否触发 GC
}

func TestProfiler_MultipleProfiles(t *testing.T) {
	config := DefaultProfilerConfig()
	profiler := NewProfiler(config)
	handler := profiler.Handler()

	profiles := []string{
		"/debug/pprof/heap",
		"/debug/pprof/goroutine",
		"/debug/pprof/block",
		"/debug/pprof/mutex",
		"/debug/pprof/allocs",
		"/debug/pprof/threadcreate",
	}

	for _, path := range profiles {
		t.Run(path, func(t *testing.T) {
			req := httptest.NewRequest("GET", path, nil)
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, req)

			resp := w.Result()
			defer resp.Body.Close()

			if resp.StatusCode != http.StatusOK {
				t.Errorf("%s 状态码 = %d, want %d", path, resp.StatusCode, http.StatusOK)
			}
		})
	}
}
