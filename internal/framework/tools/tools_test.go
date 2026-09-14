package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestToolRegistry(t *testing.T) {
	ctx := context.Background()

	t.Run("注册和获取工具", func(t *testing.T) {
		registry := NewToolRegistry()

		tool := NewFuncTool("test_tool", "测试工具", nil, func(ctx context.Context, input string) (string, error) {
			return "result", nil
		})

		err := registry.Register(tool)
		if err != nil {
			t.Fatalf("Register() error = %v", err)
		}

		retrieved, ok := registry.Get("test_tool")
		if !ok {
			t.Error("工具未找到")
		}

		if retrieved.Name() != "test_tool" {
			t.Errorf("Name() = %q, want %q", retrieved.Name(), "test_tool")
		}
	})

	t.Run("重复注册", func(t *testing.T) {
		registry := NewToolRegistry()

		tool := NewFuncTool("test", "测试", nil, nil)
		_ = registry.Register(tool)

		err := registry.Register(tool)
		if err == nil {
			t.Error("期望错误，但成功了")
		}
	})

	t.Run("列出所有工具", func(t *testing.T) {
		registry := NewToolRegistry()

		tool1 := NewFuncTool("tool1", "工具1", nil, nil)
		tool2 := NewFuncTool("tool2", "工具2", nil, nil)

		_ = registry.Register(tool1)
		_ = registry.Register(tool2)

		tools := registry.List()
		if len(tools) != 2 {
			t.Errorf("List() len = %d, want 2", len(tools))
		}
	})

	t.Run("执行工具", func(t *testing.T) {
		registry := NewToolRegistry()

		tool := NewFuncTool("echo", "回显", nil, func(ctx context.Context, input string) (string, error) {
			return "echo: " + input, nil
		})

		_ = registry.Register(tool)

		result, err := registry.Execute(ctx, "echo", "test")
		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}

		if result != "echo: test" {
			t.Errorf("result = %q, want %q", result, "echo: test")
		}
	})
}

func TestHTTPGetTool(t *testing.T) {
	ctx := context.Background()

	t.Run("成功请求", func(t *testing.T) {
		// 创建测试服务器
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != "GET" {
				t.Errorf("Method = %s, want GET", r.Method)
			}
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("Hello, World!"))
		}))
		defer server.Close()

		tool := NewHTTPGetTool(5 * time.Second)

		input := map[string]any{
			"url": server.URL,
		}
		inputJSON, _ := json.Marshal(input)

		result, err := tool.Execute(ctx, string(inputJSON))
		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}

		if result != "Hello, World!" {
			t.Errorf("result = %q, want %q", result, "Hello, World!")
		}
	})

	t.Run("带请求头", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			auth := r.Header.Get("Authorization")
			if auth != "Bearer token123" {
				t.Errorf("Authorization = %q, want %q", auth, "Bearer token123")
			}
			w.Write([]byte("Authorized"))
		}))
		defer server.Close()

		tool := NewHTTPGetTool(5 * time.Second)

		input := map[string]any{
			"url": server.URL,
			"headers": map[string]string{
				"Authorization": "Bearer token123",
			},
		}
		inputJSON, _ := json.Marshal(input)

		result, err := tool.Execute(ctx, string(inputJSON))
		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}

		if result != "Authorized" {
			t.Errorf("result = %q, want %q", result, "Authorized")
		}
	})

	t.Run("HTTP 错误", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
			w.Write([]byte("Not Found"))
		}))
		defer server.Close()

		tool := NewHTTPGetTool(5 * time.Second)

		input := map[string]any{
			"url": server.URL,
		}
		inputJSON, _ := json.Marshal(input)

		_, err := tool.Execute(ctx, string(inputJSON))
		if err == nil {
			t.Error("期望错误，但成功了")
		}
	})
}

func TestHTTPPostTool(t *testing.T) {
	ctx := context.Background()

	t.Run("成功请求", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != "POST" {
				t.Errorf("Method = %s, want POST", r.Method)
			}

			contentType := r.Header.Get("Content-Type")
			if contentType != "application/json" {
				t.Errorf("Content-Type = %q, want %q", contentType, "application/json")
			}

			w.Write([]byte("Created"))
		}))
		defer server.Close()

		tool := NewHTTPPostTool(5 * time.Second)

		input := map[string]any{
			"url":  server.URL,
			"body": `{"name":"test"}`,
		}
		inputJSON, _ := json.Marshal(input)

		result, err := tool.Execute(ctx, string(inputJSON))
		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}

		if result != "Created" {
			t.Errorf("result = %q, want %q", result, "Created")
		}
	})
}

func TestFileReadTool(t *testing.T) {
	ctx := context.Background()

	t.Run("读取文件", func(t *testing.T) {
		// 创建临时文件
		tmpFile, err := os.CreateTemp("", "test-*.txt")
		if err != nil {
			t.Fatal(err)
		}
		defer os.Remove(tmpFile.Name())

		content := "测试内容"
		tmpFile.WriteString(content)
		tmpFile.Close()

		tool := NewFileReadTool(10 * 1024 * 1024)

		input := map[string]any{
			"path": tmpFile.Name(),
		}
		inputJSON, _ := json.Marshal(input)

		result, err := tool.Execute(ctx, string(inputJSON))
		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}

		if result != content {
			t.Errorf("result = %q, want %q", result, content)
		}
	})

	t.Run("文件不存在", func(t *testing.T) {
		tool := NewFileReadTool(10 * 1024 * 1024)

		input := map[string]any{
			"path": "/nonexistent/file.txt",
		}
		inputJSON, _ := json.Marshal(input)

		_, err := tool.Execute(ctx, string(inputJSON))
		if err == nil {
			t.Error("期望错误，但成功了")
		}
	})
}

func TestFileWriteTool(t *testing.T) {
	ctx := context.Background()

	t.Run("写入文件", func(t *testing.T) {
		tmpDir := t.TempDir()

		tool := NewFileWriteTool([]string{tmpDir})

		filePath := filepath.Join(tmpDir, "test.txt")
		content := "测试内容"

		input := map[string]any{
			"path":    filePath,
			"content": content,
		}
		inputJSON, _ := json.Marshal(input)

		result, err := tool.Execute(ctx, string(inputJSON))
		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}

		if !strings.Contains(result, "成功") {
			t.Errorf("result = %q, should contain '成功'", result)
		}

		// 验证文件内容
		data, _ := os.ReadFile(filePath)
		if string(data) != content {
			t.Errorf("file content = %q, want %q", string(data), content)
		}
	})

	t.Run("追加模式", func(t *testing.T) {
		tmpDir := t.TempDir()

		tool := NewFileWriteTool([]string{tmpDir})

		filePath := filepath.Join(tmpDir, "test.txt")

		// 第一次写入
		input1 := map[string]any{
			"path":    filePath,
			"content": "第一行\n",
		}
		inputJSON1, _ := json.Marshal(input1)
		tool.Execute(ctx, string(inputJSON1))

		// 第二次追加
		input2 := map[string]any{
			"path":    filePath,
			"content": "第二行\n",
			"append":  true,
		}
		inputJSON2, _ := json.Marshal(input2)
		tool.Execute(ctx, string(inputJSON2))

		// 验证
		data, _ := os.ReadFile(filePath)
		if string(data) != "第一行\n第二行\n" {
			t.Errorf("file content = %q, want %q", string(data), "第一行\n第二行\n")
		}
	})

	t.Run("目录权限检查", func(t *testing.T) {
		tmpDir := t.TempDir()

		tool := NewFileWriteTool([]string{tmpDir})

		// 尝试写入不允许的目录
		input := map[string]any{
			"path":    "/tmp/forbidden.txt",
			"content": "test",
		}
		inputJSON, _ := json.Marshal(input)

		_, err := tool.Execute(ctx, string(inputJSON))
		if err == nil {
			t.Error("期望错误，但成功了")
		}
	})
}

func TestFileDeleteTool(t *testing.T) {
	ctx := context.Background()

	t.Run("删除文件", func(t *testing.T) {
		tmpDir := t.TempDir()

		// 创建测试文件
		filePath := filepath.Join(tmpDir, "test.txt")
		os.WriteFile(filePath, []byte("test"), 0644)

		tool := NewFileDeleteTool([]string{tmpDir})

		input := map[string]any{
			"path": filePath,
		}
		inputJSON, _ := json.Marshal(input)

		result, err := tool.Execute(ctx, string(inputJSON))
		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}

		if !strings.Contains(result, "成功") {
			t.Errorf("result should contain '成功'")
		}

		// 验证文件已删除
		if _, err := os.Stat(filePath); !os.IsNotExist(err) {
			t.Error("文件应该已删除")
		}
	})
}

func TestFileListTool(t *testing.T) {
	ctx := context.Background()

	t.Run("列出文件", func(t *testing.T) {
		tmpDir := t.TempDir()

		// 创建测试文件
		os.WriteFile(filepath.Join(tmpDir, "file1.txt"), []byte("test1"), 0644)
		os.WriteFile(filepath.Join(tmpDir, "file2.txt"), []byte("test2"), 0644)
		os.Mkdir(filepath.Join(tmpDir, "subdir"), 0755)

		tool := NewFileListTool()

		input := map[string]any{
			"path": tmpDir,
		}
		inputJSON, _ := json.Marshal(input)

		result, err := tool.Execute(ctx, string(inputJSON))
		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}

		// 解析结果
		var files []map[string]any
		if err := json.Unmarshal([]byte(result), &files); err != nil {
			t.Fatalf("JSON 解析失败: %v", err)
		}

		if len(files) != 3 {
			t.Errorf("len(files) = %d, want 3", len(files))
		}
	})
}

func TestShellTool(t *testing.T) {
	ctx := context.Background()

	t.Run("执行简单命令", func(t *testing.T) {
		tool := NewShellTool(5 * time.Second)

		input := map[string]any{
			"command": "echo 'Hello, World!'",
		}
		inputJSON, _ := json.Marshal(input)

		result, err := tool.Execute(ctx, string(inputJSON))
		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}

		if !strings.Contains(result, "Hello, World!") {
			t.Errorf("result should contain 'Hello, World!'")
		}
	})

	t.Run("禁止危险命令", func(t *testing.T) {
		tool := NewShellTool(5 * time.Second)

		input := map[string]any{
			"command": "rm -rf /",
		}
		inputJSON, _ := json.Marshal(input)

		_, err := tool.Execute(ctx, string(inputJSON))
		if err == nil {
			t.Error("期望错误，但成功了")
		}
	})

	t.Run("命令白名单", func(t *testing.T) {
		tool := NewShellTool(5 * time.Second).WithAllowedCommands([]string{"echo", "ls"})

		// 允许的命令
		input1 := map[string]any{
			"command": "echo test",
		}
		inputJSON1, _ := json.Marshal(input1)

		_, err := tool.Execute(ctx, string(inputJSON1))
		if err != nil {
			t.Errorf("允许的命令应该成功: %v", err)
		}

		// 不允许的命令
		input2 := map[string]any{
			"command": "pwd",
		}
		inputJSON2, _ := json.Marshal(input2)

		_, err = tool.Execute(ctx, string(inputJSON2))
		if err == nil {
			t.Error("不允许的命令应该失败")
		}
	})
}

func TestToolResult(t *testing.T) {
	t.Run("成功结果", func(t *testing.T) {
		result := NewSuccessResult("output")

		if !result.Success {
			t.Error("Success should be true")
		}

		if result.Output != "output" {
			t.Errorf("Output = %q, want %q", result.Output, "output")
		}

		json := result.ToJSON()
		if !strings.Contains(json, "success") {
			t.Error("JSON should contain 'success'")
		}
	})

	t.Run("错误结果", func(t *testing.T) {
		result := NewErrorResult(fmt.Errorf("test error"))

		if result.Success {
			t.Error("Success should be false")
		}

		if result.Error != "test error" {
			t.Errorf("Error = %q, want %q", result.Error, "test error")
		}
	})
}
