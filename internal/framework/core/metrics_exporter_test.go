package core

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/V3teran/liusha/internal/framework/llm"
)

// ─────────────────────────────────────────────
//  MetricsExporter 测试
// ─────────────────────────────────────────────

func TestMetricsExporter_Creation(t *testing.T) {
	exporter := NewMetricsExporter("test_namespace")
	if exporter == nil {
		t.Fatal("exporter 不应为 nil")
	}

	if exporter.registry == nil {
		t.Error("registry 不应为 nil")
	}
}

func TestMetricsExporter_Handler(t *testing.T) {
	exporter := NewMetricsExporter("test_namespace")
	handler := exporter.Handler()

	if handler == nil {
		t.Fatal("handler 不应为 nil")
	}

	// 创建测试请求
	req := httptest.NewRequest("GET", "/metrics", nil)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	// 验证响应
	resp := w.Result()
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("状态码 = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("读取响应失败: %v", err)
	}

	// 验证包含指标
	bodyStr := string(body)
	if !strings.Contains(bodyStr, "test_namespace") {
		t.Error("响应应包含 namespace")
	}
}

func TestMetricsExporter_SetGauges(t *testing.T) {
	exporter := NewMetricsExporter("test_namespace")

	// 设置 gauge 值
	exporter.SetActiveAgents(5.0)
	exporter.SetQueuedTasks(10.0)

	// 验证可以获取指标
	req := httptest.NewRequest("GET", "/metrics", nil)
	w := httptest.NewRecorder()
	exporter.Handler().ServeHTTP(w, req)

	resp := w.Result()
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("读取响应失败: %v", err)
	}

	bodyStr := string(body)
	if !strings.Contains(bodyStr, "active_agents") {
		t.Error("应包含 active_agents 指标")
	}
	if !strings.Contains(bodyStr, "queued_tasks") {
		t.Error("应包含 queued_tasks 指标")
	}
}

// ─────────────────────────────────────────────
//  PrometheusCallback 测试
// ─────────────────────────────────────────────

func TestPrometheusCallback_AgentLifecycle(t *testing.T) {
	exporter := NewMetricsExporter("test")
	callback := NewPrometheusCallback(exporter)
	ctx := context.Background()

	// Agent 启动
	callback.OnAgentStart(ctx, AgentStartEvent{
		TaskID:    "task-1",
		AgentName: "test-agent",
		StartTime: time.Now(),
	})

	// Agent 结束（成功）
	callback.OnAgentEnd(ctx, AgentEndEvent{
		TaskID:    "task-1",
		AgentName: "test-agent",
		Duration:  100 * time.Millisecond,
		Error:     nil,
		StartTime: time.Now(),
	})

	// 验证指标
	req := httptest.NewRequest("GET", "/metrics", nil)
	w := httptest.NewRecorder()
	exporter.Handler().ServeHTTP(w, req)

	body, _ := io.ReadAll(w.Result().Body)
	bodyStr := string(body)

	if !strings.Contains(bodyStr, "agent_start_total") {
		t.Error("应包含 agent_start_total")
	}
	if !strings.Contains(bodyStr, "agent_end_total") {
		t.Error("应包含 agent_end_total")
	}
	if !strings.Contains(bodyStr, "agent_duration_seconds") {
		t.Error("应包含 agent_duration_seconds")
	}
}

func TestPrometheusCallback_AgentLifecycle_WithError(t *testing.T) {
	exporter := NewMetricsExporter("test")
	callback := NewPrometheusCallback(exporter)
	ctx := context.Background()

	// Agent 启动
	callback.OnAgentStart(ctx, AgentStartEvent{
		TaskID:    "task-2",
		AgentName: "error-agent",
		StartTime: time.Now(),
	})

	// Agent 结束（失败）
	callback.OnAgentEnd(ctx, AgentEndEvent{
		TaskID:    "task-2",
		AgentName: "error-agent",
		Duration:  50 * time.Millisecond,
		Error:     errors.New("执行失败"),
		StartTime: time.Now(),
	})

	// 验证错误指标
	req := httptest.NewRequest("GET", "/metrics", nil)
	w := httptest.NewRecorder()
	exporter.Handler().ServeHTTP(w, req)

	body, _ := io.ReadAll(w.Result().Body)
	bodyStr := string(body)

	if !strings.Contains(bodyStr, "agent_error_total") {
		t.Error("应包含 agent_error_total")
	}
}

func TestPrometheusCallback_ToolLifecycle(t *testing.T) {
	exporter := NewMetricsExporter("test")
	callback := NewPrometheusCallback(exporter)
	ctx := context.Background()

	// Tool 执行
	callback.OnToolStart(ctx, ToolStartEvent{
		TaskID:    "task-1",
		ToolName:  "test-tool",
		Input:     map[string]any{},
		StartTime: time.Now(),
	})

	callback.OnToolEnd(ctx, ToolEndEvent{
		TaskID:    "task-1",
		ToolName:  "test-tool",
		Output:    "result",
		Duration:  20 * time.Millisecond,
		Error:     nil,
		StartTime: time.Now(),
	})

	// 验证指标
	req := httptest.NewRequest("GET", "/metrics", nil)
	w := httptest.NewRecorder()
	exporter.Handler().ServeHTTP(w, req)

	body, _ := io.ReadAll(w.Result().Body)
	bodyStr := string(body)

	if !strings.Contains(bodyStr, "tool_call_total") {
		t.Error("应包含 tool_call_total")
	}
	if !strings.Contains(bodyStr, "tool_duration_seconds") {
		t.Error("应包含 tool_duration_seconds")
	}
}

func TestPrometheusCallback_LLMLifecycle(t *testing.T) {
	exporter := NewMetricsExporter("test")
	callback := NewPrometheusCallback(exporter)
	ctx := context.Background()

	// LLM 调用
	callback.OnLLMStart(ctx, LLMStartEvent{
		TaskID:     "task-1",
		ProviderID: "openai",
		ModelID:    "gpt-4",
		Request:    llm.Request{},
		StartTime:  time.Now(),
	})

	callback.OnLLMEnd(ctx, LLMEndEvent{
		TaskID:     "task-1",
		ProviderID: "openai",
		ModelID:    "gpt-4",
		Response: llm.Response{
			Usage: llm.Usage{
				InTokens:     100,
				OutTokens:    50,
				CachedTokens: 20,
			},
		},
		Duration:  300 * time.Millisecond,
		Error:     nil,
		StartTime: time.Now(),
	})

	// 验证指标
	req := httptest.NewRequest("GET", "/metrics", nil)
	w := httptest.NewRecorder()
	exporter.Handler().ServeHTTP(w, req)

	body, _ := io.ReadAll(w.Result().Body)
	bodyStr := string(body)

	if !strings.Contains(bodyStr, "llm_call_total") {
		t.Error("应包含 llm_call_total")
	}
	if !strings.Contains(bodyStr, "llm_tokens_total") {
		t.Error("应包含 llm_tokens_total")
	}
	if !strings.Contains(bodyStr, "llm_duration_seconds") {
		t.Error("应包含 llm_duration_seconds")
	}
	if !strings.Contains(bodyStr, "llm_cached_tokens_total") {
		t.Error("应包含 llm_cached_tokens_total")
	}
}

// ─────────────────────────────────────────────
//  MetricsServer 测试
// ─────────────────────────────────────────────

func TestMetricsServer_StartShutdown(t *testing.T) {
	exporter := NewMetricsExporter("test")
	server := NewMetricsServer(exporter, "localhost:0") // 随机端口

	// 启动服务器
	if err := server.Start(); err != nil {
		t.Fatalf("启动服务器失败: %v", err)
	}

	// 等待启动
	time.Sleep(100 * time.Millisecond)

	// 关闭服务器
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		t.Errorf("关闭服务器失败: %v", err)
	}
}

// ─────────────────────────────────────────────
//  并发测试
// ─────────────────────────────────────────────

func TestPrometheusCallback_Concurrent(t *testing.T) {
	exporter := NewMetricsExporter("test")
	callback := NewPrometheusCallback(exporter)

	const numGoroutines = 20
	done := make(chan bool, numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		go func(id int) {
			defer func() { done <- true }()

			ctx := context.Background()

			// 模拟 Agent 生命周期
			callback.OnAgentStart(ctx, AgentStartEvent{
				TaskID:    "task-concurrent",
				AgentName: "agent",
				StartTime: time.Now(),
			})

			time.Sleep(10 * time.Millisecond)

			callback.OnAgentEnd(ctx, AgentEndEvent{
				TaskID:    "task-concurrent",
				AgentName: "agent",
				Duration:  10 * time.Millisecond,
				Error:     nil,
				StartTime: time.Now(),
			})

			// 模拟 LLM 调用
			callback.OnLLMStart(ctx, LLMStartEvent{
				TaskID:     "task-concurrent",
				ProviderID: "openai",
				ModelID:    "gpt-4",
				Request:    llm.Request{},
				StartTime:  time.Now(),
			})

			callback.OnLLMEnd(ctx, LLMEndEvent{
				TaskID:     "task-concurrent",
				ProviderID: "openai",
				ModelID:    "gpt-4",
				Response: llm.Response{
					Usage: llm.Usage{InTokens: 10, OutTokens: 5},
				},
				Duration:  50 * time.Millisecond,
				StartTime: time.Now(),
			})
		}(i)
	}

	// 等待所有 goroutine 完成
	for i := 0; i < numGoroutines; i++ {
		<-done
	}

	// 验证指标仍然可读
	req := httptest.NewRequest("GET", "/metrics", nil)
	w := httptest.NewRecorder()
	exporter.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("并发后指标接口状态码 = %d, want %d", w.Code, http.StatusOK)
	}
}
