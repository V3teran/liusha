// Package workspace 提供沙盒任务工作区的文件检索与 CRUD（宿主视角）。
//
// 工作区根目录即 launcher bind 进沙盒的宿主目录（sandbox.workspace_dir，
// 默认 ~/data/liusha/workspace）：容器内 sandbox-server 按 taskID 分
// work/out/profile 子目录，本包在同一路径树上做安全的文件操作——路径
// 解析被钳制在根目录内，穿越序列（.. / 绝对路径 / 符号链接逃逸）一律拒绝。
package workspace

import (
	"encoding/base64"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

// Store 是工作区文件操作的入口。所有路径参数都是相对 taskID 的斜杠路径
// （容器内外同一棵树的语义由调用方保证：compose 以同路径挂载）。
type Store struct {
	root string
}

// NewStore 校验并定位工作区根目录（不存在则创建）。
func NewStore(root string) (*Store, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("workspace root: %w", err)
	}
	if err := os.MkdirAll(abs, 0o750); err != nil {
		return nil, fmt.Errorf("mkdir workspace root %s: %w", abs, err)
	}
	// 根目录解析为真实路径：符号链接逃逸检测的前缀比对必须基于同一解析
	// 基准（macOS 的临时目录本体就是符号链接）。
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return nil, fmt.Errorf("resolve workspace root %s: %w", abs, err)
	}
	return &Store{root: resolved}, nil
}

// Root 返回工作区根目录绝对路径。
func (s *Store) Root() string { return s.root }

// TaskDir 是一个任务工作区的汇总视图。
type TaskDir struct {
	Name    string    `json:"name"`
	ModTime time.Time `json:"mod_time"`
}

// Tasks 列出根目录下的全部任务工作区（最新活动在前）。
func (s *Store) Tasks() ([]TaskDir, error) {
	entries, err := os.ReadDir(s.root)
	if err != nil {
		return nil, err
	}
	tasks := make([]TaskDir, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		tasks = append(tasks, TaskDir{Name: e.Name(), ModTime: info.ModTime()})
	}
	sort.Slice(tasks, func(i, j int) bool { return tasks[i].ModTime.After(tasks[j].ModTime) })
	return tasks, nil
}

// Entry 是文件树的一个节点（Path 为相对 taskID 的斜杠路径，目录带尾斜杠
// 便于前端区分）。
type Entry struct {
	Path    string    `json:"path"`
	Size    int64     `json:"size"`
	IsDir   bool      `json:"is_dir"`
	ModTime time.Time `json:"mod_time"`
}

// Tree 返回任务工作区的完整文件树（深度优先，目录在前、同类按名排序）。
func (s *Store) Tree(taskID string) ([]Entry, error) {
	taskRoot, err := s.taskRoot(taskID)
	if err != nil {
		return nil, err
	}
	var out []Entry
	err = filepath.WalkDir(taskRoot, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p == taskRoot {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(taskRoot, p)
		if err != nil {
			return err
		}
		name := filepath.ToSlash(rel)
		if d.IsDir() {
			name += "/"
		}
		out = append(out, Entry{Path: name, Size: info.Size(), IsDir: d.IsDir(), ModTime: info.ModTime()})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].IsDir != out[j].IsDir {
			return out[i].IsDir
		}
		return out[i].Path < out[j].Path
	})
	return out, nil
}

// MaxReadBytes 是单次读取的内容上限（超出返回截断标记）。
const MaxReadBytes = 4 << 20 // 4 MiB

// FileContent 是读取结果：文本原样返回，二进制以 base64 编码并标记
// binary（前端据此禁用编辑器）。
type FileContent struct {
	Path      string    `json:"path"`
	Binary    bool      `json:"binary"`
	Content   string    `json:"content"`
	Size      int64     `json:"size"`
	Truncated bool      `json:"truncated"`
	ModTime   time.Time `json:"mod_time"`
}

// Read 读取任务工作区内的一个文件。
func (s *Store) Read(taskID, rel string) (FileContent, error) {
	full, err := s.resolve(taskID, rel)
	if err != nil {
		return FileContent{}, err
	}
	info, err := os.Stat(full)
	if err != nil {
		return FileContent{}, err
	}
	if info.IsDir() {
		return FileContent{}, fmt.Errorf("%s 是目录", rel)
	}
	f, err := os.Open(full) // #nosec G304 // 路径已经 resolve 钳制校验
	if err != nil {
		return FileContent{}, err
	}
	defer f.Close()

	data := make([]byte, MaxReadBytes)
	n, err := f.Read(data)
	if err != nil && n == 0 {
		return FileContent{}, err
	}
	data = data[:n]
	content := string(data)
	binary := !utf8.Valid(data)
	if binary {
		content = base64.StdEncoding.EncodeToString(data)
	}
	return FileContent{
		Path:      filepath.ToSlash(rel),
		Binary:    binary,
		Content:   content,
		Size:      info.Size(),
		Truncated: info.Size() > int64(n),
		ModTime:   info.ModTime(),
	}, nil
}

// Write 创建或覆盖任务工作区内的一个文件（父目录自动创建；空数据合法）。
func (s *Store) Write(taskID, rel string, data []byte) error {
	full, err := s.resolve(taskID, rel)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
		return err
	}
	return os.WriteFile(full, data, 0o640)
}

// Mkdir 创建任务工作区内的一级目录（父目录自动创建）。
func (s *Store) Mkdir(taskID, rel string) error {
	full, err := s.resolve(taskID, rel)
	if err != nil {
		return err
	}
	return os.MkdirAll(full, 0o750)
}

// Delete 删除任务工作区内的文件或目录（目录递归）。
func (s *Store) Delete(taskID, rel string) error {
	full, err := s.resolve(taskID, rel)
	if err != nil {
		return err
	}
	if full == s.taskRootUnchecked(taskID) {
		return fmt.Errorf("拒绝删除任务工作区根目录")
	}
	return os.RemoveAll(full)
}

// taskRoot 校验 taskID 形态并返回其目录。
func (s *Store) taskRoot(taskID string) (string, error) {
	if !isSafeSegment(taskID) {
		return "", fmt.Errorf("非法任务标识: %q", taskID)
	}
	return filepath.Join(s.root, taskID), nil
}

func (s *Store) taskRootUnchecked(taskID string) string {
	return filepath.Join(s.root, taskID)
}

// resolve 把「taskID + 相对路径」钳制为根目录内的绝对路径：穿越序列、
// 绝对路径、转义符号链接（解析后逃出根目录）一律拒绝。
func (s *Store) resolve(taskID, rel string) (string, error) {
	if !isSafeSegment(taskID) {
		return "", fmt.Errorf("非法任务标识: %q", taskID)
	}
	// 绝对路径与穿越序列看原始形式即拒绝——规范化吞掉前导斜杠/抵消的
	// ".." 是猜测性放行，显式意图不值得猜。
	if strings.HasPrefix(rel, "/") || filepath.IsAbs(rel) {
		return "", fmt.Errorf("拒绝绝对路径: %q", rel)
	}
	for _, seg := range strings.FieldsFunc(rel, func(r rune) bool { return r == '/' || r == '\\' }) {
		if seg == ".." {
			return "", fmt.Errorf("路径穿越序列: %q", rel)
		}
	}
	clean := path.Clean("/" + strings.ReplaceAll(rel, "\\", "/"))
	clean = strings.TrimPrefix(clean, "/")
	if clean == "" || clean == "." {
		return "", fmt.Errorf("文件路径必填")
	}
	for _, seg := range strings.Split(clean, "/") {
		if !isSafeSegment(seg) {
			return "", fmt.Errorf("非法路径段: %q", seg)
		}
	}
	full := filepath.Join(s.root, taskID, filepath.FromSlash(clean))
	if resolved, err := filepath.EvalSymlinks(filepath.Dir(full)); err == nil {
		if resolved != s.root && !strings.HasPrefix(resolved, s.root+string(filepath.Separator)) {
			return "", fmt.Errorf("符号链接逃逸工作区: %q", rel)
		}
	}
	return full, nil
}

// isSafeSegment 限制单段路径为文件名安全字符（uuid 任务目录、常规文件名
// 都满足；点开头的隐藏文件合法）。
func isSafeSegment(seg string) bool {
	if seg == "" || seg == "." || seg == ".." {
		return false
	}
	for _, r := range seg {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '-', r == '_', r == '.', r == ' ':
		default:
			return false
		}
	}
	return true
}
