// Package workspace 提供沙盒工作区的文件检索与 CRUD（宿主视角）。
//
// 工作区根目录即 launcher bind 进沙盒的宿主目录（sandbox.workspace_dir，
// 默认 ~/data/liusha/workspace）：树按 <assignment>/<task> 两级分层，沙盒
// 在任务目录下分 work/out/state 子目录。本包在同一路径树
// 上做安全的文件操作——路径解析被钳制在根目录内，穿越序列（.. / 绝对
// 路径 / 符号链接逃逸）一律拒绝。
package workspace

import (
	"archive/zip"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

// Store 是工作区文件操作的入口。所有路径参数都是相对工作区根的斜杠路径
// （顶层段即任务目录；容器内外同一棵树的语义由调用方保证：compose 以
// 同路径挂载）。
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

// MaxReadBytes 是单次读取的内容上限（超出返回截断标记）。
const MaxReadBytes = 4 << 20 // 4 MiB

// StateDirName 是沙盒会话状态目录（chromium 登录态与 $HOME 工具落盘，
// 见 sandbox 包 exec 环境）。它是沙盒内部状态而非操作员工件：文件面
// （树/读写/下载/归档）整体排除，数据按留存策略保留在盘。
const StateDirName = "state"

// isStatePath 报告相对路径是否落在会话状态子树。工作区树是批次/任务两级
// 分层（<assignment>/<task>/…），state 与 work/out 同挂在任务目录下——
// 排除判定必须认第三段，按第二段判（<assignment>/state）永远落空，state
// 子树会整棵漏进文件面。
func isStatePath(rel string) bool {
	segs := strings.SplitN(rel, "/", 4)
	return len(segs) >= 3 && segs[2] == StateDirName
}

// isHiddenPath 报告相对路径是否落在顶层点前缀子树（附件暂存区 .staging/）。
// 点前缀顶层目录是控制面中转层（会话消息附件的下发前落点），不是任务工件：
// 与 state 同判——文件面（树/下载/归档）整体排除，resolve 不拒绝（下载与
// 搬家通道仍需可达）。
func isHiddenPath(rel string) bool {
	first := rel
	if i := strings.IndexAny(first, "/\\"); i >= 0 {
		first = first[:i]
	}
	return strings.HasPrefix(first, ".")
}

// Entry 是文件树的一个节点（Path 为相对工作区根的斜杠路径，目录带尾
// 斜杠便于前端区分；Depth 为缩进层级）。
type Entry struct {
	Path    string    `json:"path"`
	Depth   int       `json:"depth"`
	Size    int64     `json:"size"`
	IsDir   bool      `json:"is_dir"`
	ModTime time.Time `json:"mod_time"`
}

// Tree 返回整棵工作区文件树。排序：顶层任务目录按活动时间最新在前
// （新任务的工件一眼可见），组内目录在前、路径字典序。
func (s *Store) Tree() ([]Entry, error) {
	var out []Entry
	err := filepath.WalkDir(s.root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p == s.root {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(s.root, p)
		if err != nil {
			return err
		}
		// 会话状态子树不属于文件面：整枝跳过（数据保留在盘）
		if isStatePath(rel) {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		// 顶层点前缀子树（附件暂存 .staging/）同样不属文件面：整枝跳过
		if isHiddenPath(rel) {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		name := filepath.ToSlash(rel)
		depth := strings.Count(name, "/")
		if d.IsDir() {
			name += "/"
		}
		out = append(out, Entry{Path: name, Depth: depth, Size: info.Size(), IsDir: d.IsDir(), ModTime: info.ModTime()})
		return nil
	})
	if err != nil {
		return nil, err
	}

	// 顶层任务目录按 mtime 降序赋组序；组内目录前、路径序；顶层散文件殿后。
	topDirs := map[string]time.Time{}
	topRank := map[string]int{}
	rank := 0
	for _, e := range out {
		if e.Depth != 0 || !e.IsDir {
			continue
		}
		topDirs[strings.TrimSuffix(e.Path, "/")] = e.ModTime
	}
	names := make([]string, 0, len(topDirs))
	for name := range topDirs {
		names = append(names, name)
	}
	sort.Slice(names, func(i, j int) bool { return topDirs[names[i]].After(topDirs[names[j]]) })
	for _, n := range names {
		topRank[n] = rank
		rank++
	}
	fileRank := rank
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		ka, kb := treeGroupKey(a, topRank, fileRank), treeGroupKey(b, topRank, fileRank)
		if ka != kb {
			return ka < kb
		}
		return a.Path < b.Path
	})
	return out, nil
}

func treeGroupKey(e Entry, topRank map[string]int, fileRank int) string {
	top := e.Path
	if i := strings.Index(e.Path, "/"); i >= 0 {
		top = e.Path[:i+1]
	}
	if r, ok := topRank[top]; ok {
		return fmt.Sprintf("%04d|%s", r, e.Path)
	}
	return fmt.Sprintf("%04d|%s", fileRank, e.Path)
}

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

// Read 读取工作区内的一个文件。
func (s *Store) Read(rel string) (FileContent, error) {
	full, err := s.resolve(rel)
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
	defer func() { _ = f.Close() }()

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

// OpenFile 打开工作区内的一个文件供流式读取（下载通道）。
func (s *Store) OpenFile(rel string) (*os.File, os.FileInfo, error) {
	full, err := s.resolve(rel)
	if err != nil {
		return nil, nil, err
	}
	info, err := os.Stat(full)
	if err != nil {
		return nil, nil, err
	}
	if info.IsDir() {
		return nil, nil, fmt.Errorf("%s 是目录（目录下载走 ZipTo）", rel)
	}
	f, err := os.Open(full) // #nosec G304 // 路径已经 resolve 钳制校验
	if err != nil {
		return nil, nil, err
	}
	return f, info, nil
}

// ZipTo 把工作区内的一个目录打包写入 w（下载通道，流式不落临时文件）。
func (s *Store) ZipTo(rel string, w io.Writer) error {
	full, err := s.resolve(rel)
	if err != nil {
		return err
	}
	info, err := os.Stat(full)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("%s 不是目录", rel)
	}
	base := filepath.Base(full)
	zw := zip.NewWriter(w)
	err = filepath.WalkDir(full, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		arc, err := filepath.Rel(full, p)
		if err != nil {
			return err
		}
		// 目录归档同样排除会话状态子树（数据保留在盘，不进交付包）
		if arc != "." && (arc == StateDirName || strings.HasPrefix(arc, StateDirName+"/")) {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			_, err = zw.Create(path.Join(base, filepath.ToSlash(arc)) + "/")
			return err
		}
		// 不归档符号链接：工作区内容来自不受信沙箱，symlink 可指向宿主任意
		// 路径（TOCTOU 逃逸），交付包只收真实文件。
		if d.Type()&fs.ModeSymlink != 0 {
			return nil
		}
		dInfo, err := d.Info()
		if err != nil {
			return err
		}
		hdr := &zip.FileHeader{Name: path.Join(base, filepath.ToSlash(arc)), Modified: dInfo.ModTime()}
		hdr.SetMode(dInfo.Mode())
		zwEntry, err := zw.CreateHeader(hdr)
		if err != nil {
			return err
		}
		f, err := os.Open(p) // #nosec G304,G122 // WalkDir 根来自 resolve 校验，symlink 已在上方跳过（归档仅收真实文件）
		if err != nil {
			return err
		}
		defer func() { _ = f.Close() }()
		_, err = io.Copy(zwEntry, f)
		return err
	})
	if err != nil {
		_ = zw.Close()
		return err
	}
	return zw.Close()
}

// Write 创建或覆盖工作区内的一个文件（父目录自动创建；空数据合法）。
func (s *Store) Write(rel string, data []byte) error {
	full, err := s.resolve(rel)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
		return err
	}
	// #nosec G306 -- 工作区文件是共享工件（沙箱回连/交付下载），0640 组可读为刻意选择，非密钥。
	return os.WriteFile(full, data, 0o640)
}

// ErrTooLarge 报告写入内容超出大小上限（半成品文件已清理）。
var ErrTooLarge = errors.New("内容超过大小上限")

// ErrExists 报告目标路径已存在（调用方决定改名重试还是报错）。
var ErrExists = errors.New("目标已存在")

// WriteFrom 流式写入一个文件（父目录自动创建），写入量超过 limit 即中止、
// 清理半成品并返回 ErrTooLarge。附件上传通道用：http 层已把 multipart 解析成
// 临时文件，此处直接拷贝落盘，不经全量内存缓冲。
func (s *Store) WriteFrom(rel string, r io.Reader, limit int64) (int64, error) {
	full, err := s.resolve(rel)
	if err != nil {
		return 0, err
	}
	if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
		return 0, err
	}
	// #nosec G304,G302 -- 路径已经 resolve 钳制校验；0640 组可读为刻意选择（共享工件，沙箱回连/交付下载），非密钥。
	f, err := os.OpenFile(full, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o640)
	if err != nil {
		return 0, err
	}
	n, copyErr := io.Copy(f, io.LimitReader(r, limit+1))
	closeErr := f.Close()
	if copyErr == nil {
		copyErr = closeErr
	}
	if copyErr == nil && n > limit {
		_ = os.Remove(full)
		return n, ErrTooLarge
	}
	if copyErr != nil {
		_ = os.Remove(full)
	}
	return n, copyErr
}

// RemoveIfEmptyDir 删除一个空目录（非空/不存在均为 no-op）——暂存中转层的
// 失败清理用：目录里可能有并发上传的他人文件，只在真空时移除，绝不递归。
func (s *Store) RemoveIfEmptyDir(rel string) error {
	full, err := s.resolve(rel)
	if err != nil {
		return err
	}
	_ = os.Remove(full)
	return nil
}

// Stat 返回工作区内路径的文件信息（不存在返回 fs.ErrNotExist）。
func (s *Store) Stat(rel string) (fs.FileInfo, error) {
	full, err := s.resolve(rel)
	if err != nil {
		return nil, err
	}
	return os.Stat(full) // #nosec G304 -- 路径已经 resolve 钳制校验
}

// ReadDir 列出工作区内一个目录的直接子项（含隐藏子树——附件暂存清扫是
// 唯一需要枚举 .staging 的通道；目录不存在返回 fs.ErrNotExist）。
func (s *Store) ReadDir(rel string) ([]fs.FileInfo, error) {
	full, err := s.resolve(rel)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(full) // #nosec G304 -- 路径已经 resolve 钳制校验
	if err != nil {
		return nil, err
	}
	out := make([]fs.FileInfo, 0, len(entries))
	for _, e := range entries {
		info, err := e.Info()
		if err != nil {
			continue // 并发删除的竞态子项跳过
		}
		out = append(out, info)
	}
	return out, nil
}

// MoveFile 把工作区内一个文件改移到新相对路径（同根内 rename，不跨文件系统）。
// 目标已存在返回 ErrExists（不覆盖）——重名消解由调用方决定（附件落位要
// 保住双方：inbox 里可能是早前上传的同名件，不能静默顶掉）。
func (s *Store) MoveFile(oldRel, newRel string) error {
	src, err := s.resolve(oldRel)
	if err != nil {
		return err
	}
	dst, err := s.resolve(newRel)
	if err != nil {
		return err
	}
	if _, err := os.Stat(dst); err == nil {
		return ErrExists
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o750); err != nil {
		return err
	}
	return os.Rename(src, dst)
}

// Mkdir 创建工作区内的一级目录（父目录自动创建）。
func (s *Store) Mkdir(rel string) error {
	full, err := s.resolve(rel)
	if err != nil {
		return err
	}
	return os.MkdirAll(full, 0o750)
}

// Delete 删除工作区内的文件或目录（目录递归；根目录受保护）。
func (s *Store) Delete(rel string) error {
	clean := path.Clean("/" + strings.ReplaceAll(rel, "\\", "/"))
	clean = strings.TrimPrefix(clean, "/")
	if clean == "" || clean == "." {
		return fmt.Errorf("拒绝删除工作区根目录")
	}
	full, err := s.resolve(rel)
	if err != nil {
		return err
	}
	return os.RemoveAll(full)
}

// resolve 把相对路径钳制为根目录内的绝对路径：穿越序列、绝对路径、
// 转义符号链接（解析后逃出根目录）一律拒绝。
func (s *Store) resolve(rel string) (string, error) {
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
	if isStatePath(clean) {
		return "", fmt.Errorf("%s 为沙盒会话状态，不在文件面", StateDirName)
	}
	full := filepath.Join(s.root, filepath.FromSlash(clean))
	if resolved, err := filepath.EvalSymlinks(filepath.Dir(full)); err == nil {
		if resolved != s.root && !strings.HasPrefix(resolved, s.root+string(filepath.Separator)) {
			return "", fmt.Errorf("符号链接逃逸工作区: %q", rel)
		}
	}
	return full, nil
}

// isSafeSegment 对单段路径做黑名单校验：拒绝空段、相对引用（./..）与控制
// 字符；其余 unicode 字符（中文笔记、带空格的名字等）全部合法——工作区是
// 用户与 agent 的自由文件面，字符白名单会误伤真实文件名。
func isSafeSegment(seg string) bool {
	if seg == "" || seg == "." || seg == ".." {
		return false
	}
	for _, r := range seg {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}
