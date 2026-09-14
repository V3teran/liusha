package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// ─────────────────────────────────────────────
//  文件读取工具
// ─────────────────────────────────────────────

// FileReadTool 文件读取工具
type FileReadTool struct {
	maxSize int64 // 最大文件大小（字节）
}

// NewFileReadTool 创建文件读取工具
func NewFileReadTool(maxSize int64) *FileReadTool {
	if maxSize <= 0 {
		maxSize = 10 * 1024 * 1024 // 默认 10MB
	}
	return &FileReadTool{
		maxSize: maxSize,
	}
}

// Name 工具名称
func (t *FileReadTool) Name() string {
	return "file_read"
}

// Description 工具描述
func (t *FileReadTool) Description() string {
	return "读取文件内容"
}

// Parameters 参数 Schema
func (t *FileReadTool) Parameters() json.RawMessage {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path": map[string]any{
				"type":        "string",
				"description": "文件路径",
			},
		},
		"required": []string{"path"},
	}
	data, _ := json.Marshal(schema)
	return data
}

// Execute 执行工具
func (t *FileReadTool) Execute(ctx context.Context, input string) (string, error) {
	// 解析输入
	var params struct {
		Path string `json:"path"`
	}

	if err := json.Unmarshal([]byte(input), &params); err != nil {
		return "", fmt.Errorf("参数解析失败: %w", err)
	}

	if params.Path == "" {
		return "", fmt.Errorf("文件路径不能为空")
	}

	// 检查文件大小
	info, err := os.Stat(params.Path)
	if err != nil {
		return "", fmt.Errorf("文件不存在: %w", err)
	}

	if info.Size() > t.maxSize {
		return "", fmt.Errorf("文件过大: %d 字节，最大允许 %d 字节", info.Size(), t.maxSize)
	}

	// 读取文件
	data, err := os.ReadFile(params.Path)
	if err != nil {
		return "", fmt.Errorf("读取文件失败: %w", err)
	}

	return string(data), nil
}

// ─────────────────────────────────────────────
//  文件写入工具
// ─────────────────────────────────────────────

// FileWriteTool 文件写入工具
type FileWriteTool struct {
	allowedDirs []string // 允许写入的目录
}

// NewFileWriteTool 创建文件写入工具
func NewFileWriteTool(allowedDirs []string) *FileWriteTool {
	return &FileWriteTool{
		allowedDirs: allowedDirs,
	}
}

// Name 工具名称
func (t *FileWriteTool) Name() string {
	return "file_write"
}

// Description 工具描述
func (t *FileWriteTool) Description() string {
	return "写入文件内容"
}

// Parameters 参数 Schema
func (t *FileWriteTool) Parameters() json.RawMessage {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path": map[string]any{
				"type":        "string",
				"description": "文件路径",
			},
			"content": map[string]any{
				"type":        "string",
				"description": "文件内容",
			},
			"append": map[string]any{
				"type":        "boolean",
				"description": "是否追加（默认覆盖）",
			},
		},
		"required": []string{"path", "content"},
	}
	data, _ := json.Marshal(schema)
	return data
}

// Execute 执行工具
func (t *FileWriteTool) Execute(ctx context.Context, input string) (string, error) {
	// 解析输入
	var params struct {
		Path    string `json:"path"`
		Content string `json:"content"`
		Append  bool   `json:"append"`
	}

	if err := json.Unmarshal([]byte(input), &params); err != nil {
		return "", fmt.Errorf("参数解析失败: %w", err)
	}

	if params.Path == "" {
		return "", fmt.Errorf("文件路径不能为空")
	}

	// 检查目录权限
	if !t.isAllowedPath(params.Path) {
		return "", fmt.Errorf("不允许写入此路径: %s", params.Path)
	}

	// 确保目录存在
	dir := filepath.Dir(params.Path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", fmt.Errorf("创建目录失败: %w", err)
	}

	// 写入文件
	var err error
	if params.Append {
		// 追加模式
		f, err := os.OpenFile(params.Path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if err != nil {
			return "", fmt.Errorf("打开文件失败: %w", err)
		}
		defer f.Close()

		_, err = f.WriteString(params.Content)
	} else {
		// 覆盖模式
		err = os.WriteFile(params.Path, []byte(params.Content), 0644)
	}

	if err != nil {
		return "", fmt.Errorf("写入文件失败: %w", err)
	}

	return fmt.Sprintf("成功写入 %d 字节到 %s", len(params.Content), params.Path), nil
}

// isAllowedPath 检查路径是否允许
func (t *FileWriteTool) isAllowedPath(path string) bool {
	if len(t.allowedDirs) == 0 {
		return true // 无限制
	}

	absPath, err := filepath.Abs(path)
	if err != nil {
		return false
	}

	for _, allowed := range t.allowedDirs {
		absAllowed, err := filepath.Abs(allowed)
		if err != nil {
			continue
		}

		// 检查是否在允许的目录下
		rel, err := filepath.Rel(absAllowed, absPath)
		if err != nil {
			continue
		}

		// 不包含 ".." 说明在子目录下
		if !filepath.IsAbs(rel) && len(rel) > 0 && rel[0] != '.' {
			return true
		}
	}

	return false
}

// ─────────────────────────────────────────────
//  文件删除工具
// ─────────────────────────────────────────────

// FileDeleteTool 文件删除工具
type FileDeleteTool struct {
	allowedDirs []string // 允许删除的目录
}

// NewFileDeleteTool 创建文件删除工具
func NewFileDeleteTool(allowedDirs []string) *FileDeleteTool {
	return &FileDeleteTool{
		allowedDirs: allowedDirs,
	}
}

// Name 工具名称
func (t *FileDeleteTool) Name() string {
	return "file_delete"
}

// Description 工具描述
func (t *FileDeleteTool) Description() string {
	return "删除文件"
}

// Parameters 参数 Schema
func (t *FileDeleteTool) Parameters() json.RawMessage {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path": map[string]any{
				"type":        "string",
				"description": "文件路径",
			},
		},
		"required": []string{"path"},
	}
	data, _ := json.Marshal(schema)
	return data
}

// Execute 执行工具
func (t *FileDeleteTool) Execute(ctx context.Context, input string) (string, error) {
	// 解析输入
	var params struct {
		Path string `json:"path"`
	}

	if err := json.Unmarshal([]byte(input), &params); err != nil {
		return "", fmt.Errorf("参数解析失败: %w", err)
	}

	if params.Path == "" {
		return "", fmt.Errorf("文件路径不能为空")
	}

	// 检查目录权限
	if !t.isAllowedPath(params.Path) {
		return "", fmt.Errorf("不允许删除此路径: %s", params.Path)
	}

	// 删除文件
	if err := os.Remove(params.Path); err != nil {
		return "", fmt.Errorf("删除文件失败: %w", err)
	}

	return fmt.Sprintf("成功删除文件: %s", params.Path), nil
}

// isAllowedPath 检查路径是否允许
func (t *FileDeleteTool) isAllowedPath(path string) bool {
	if len(t.allowedDirs) == 0 {
		return true // 无限制
	}

	absPath, err := filepath.Abs(path)
	if err != nil {
		return false
	}

	for _, allowed := range t.allowedDirs {
		absAllowed, err := filepath.Abs(allowed)
		if err != nil {
			continue
		}

		rel, err := filepath.Rel(absAllowed, absPath)
		if err != nil {
			continue
		}

		if !filepath.IsAbs(rel) && len(rel) > 0 && rel[0] != '.' {
			return true
		}
	}

	return false
}

// ─────────────────────────────────────────────
//  文件列表工具
// ─────────────────────────────────────────────

// FileListTool 文件列表工具
type FileListTool struct{}

// NewFileListTool 创建文件列表工具
func NewFileListTool() *FileListTool {
	return &FileListTool{}
}

// Name 工具名称
func (t *FileListTool) Name() string {
	return "file_list"
}

// Description 工具描述
func (t *FileListTool) Description() string {
	return "列出目录下的文件"
}

// Parameters 参数 Schema
func (t *FileListTool) Parameters() json.RawMessage {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path": map[string]any{
				"type":        "string",
				"description": "目录路径",
			},
		},
		"required": []string{"path"},
	}
	data, _ := json.Marshal(schema)
	return data
}

// Execute 执行工具
func (t *FileListTool) Execute(ctx context.Context, input string) (string, error) {
	// 解析输入
	var params struct {
		Path string `json:"path"`
	}

	if err := json.Unmarshal([]byte(input), &params); err != nil {
		return "", fmt.Errorf("参数解析失败: %w", err)
	}

	if params.Path == "" {
		return "", fmt.Errorf("目录路径不能为空")
	}

	// 读取目录
	entries, err := os.ReadDir(params.Path)
	if err != nil {
		return "", fmt.Errorf("读取目录失败: %w", err)
	}

	// 构建文件列表
	var files []map[string]any
	for _, entry := range entries {
		info, _ := entry.Info()
		fileInfo := map[string]any{
			"name":  entry.Name(),
			"is_dir": entry.IsDir(),
		}
		if info != nil {
			fileInfo["size"] = info.Size()
			fileInfo["mode"] = info.Mode().String()
		}
		files = append(files, fileInfo)
	}

	// 转换为 JSON
	result, _ := json.Marshal(files)
	return string(result), nil
}
