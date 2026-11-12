package server

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/V3teran/liusha/internal/sandbox"
)

// execOutput 是一次执行专属的产物暂存目录：本次命令的 $OUTPUT_DIR 指向它，而不是任务
// 共享的 out/。同任务的执行可以并发（executor 并行动作），共享目录上的前后快照差分
// 区分不了「并发执行期间别人写的文件」，会把别人的产物报成自己的；暂存目录里新落的
// 文件则只可能来自本次命令。
//
// 契约兼容：暂存目录预置指向 out/ 既有文件的符号链接，`cat "$OUTPUT_DIR/旧产物"` 照常
// 可读，`>>` 原地追加经链接写回 out/ 的同一文件。命令结束后 publish 把新文件移入 out/。
type execOutput struct {
	dir    string               // 本次执行的 $OUTPUT_DIR
	shared string               // 任务共享产物目录 out/
	before map[string]time.Time // 执行前 out/ 顶层文件 → mtime（原地改写的归属判断）
}

// stageOutput 在工作区根 root 下的顶层点目录 .exec/ 建本次执行的暂存产物目录：与 out/ 同一
// 文件系统（移入是原子改名），顶层点目录不进工作区文件面、不受留存清扫。
func stageOutput(root, shared string) (*execOutput, error) {
	parent := filepath.Join(root, ".exec")
	if err := os.MkdirAll(parent, 0o750); err != nil {
		return nil, fmt.Errorf("mkdir %s: %w", parent, err)
	}
	dir, err := os.MkdirTemp(parent, "out-")
	if err != nil {
		return nil, fmt.Errorf("建执行产物暂存目录: %w", err)
	}
	o := &execOutput{dir: dir, shared: shared, before: snapshotDir(shared)}
	for name := range o.before {
		if err := os.Symlink(filepath.Join(shared, name), filepath.Join(dir, name)); err != nil {
			_ = os.RemoveAll(dir)
			return nil, fmt.Errorf("预置既有产物链接 %s: %w", name, err)
		}
	}
	return o, nil
}

// publish 把本次执行的产物移入 out/ 并返回其附件（b64）。归属本次的只有两类：暂存
// 目录里新落的文件，以及经预置链接原地改写过的既有产物。附件上限：总大小
// maxTotalSize、数量 maxFileCount，超限的文件照常落 out/、只给 warning；子目录整体
// 移入 out/，不作附件（不递归，避免 chrome cache 等噪音）。
func (o *execOutput) publish() ([]sandbox.Attachment, []string) {
	defer func() { _ = os.RemoveAll(o.dir) }()
	entries, err := os.ReadDir(o.dir)
	if err != nil {
		return nil, []string{fmt.Sprintf("scan output dir: %v", err)}
	}
	var (
		files    []sandbox.Attachment
		warnings []string
		total    int64
	)
	attach := func(name, path string) {
		if len(files) >= maxFileCount {
			warnings = append(warnings, fmt.Sprintf("file '%s' skipped (count limit %d reached)", name, maxFileCount))
			return
		}
		info, err := os.Stat(path)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("stat '%s': %v", name, err))
			return
		}
		if total+info.Size() > maxTotalSize {
			warnings = append(warnings, fmt.Sprintf("file '%s' skipped (would exceed total limit %dKB)", name, maxTotalSize/1024))
			return
		}
		data, err := os.ReadFile(path) // #nosec G304 // 路径限于本任务产物目录
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("read '%s': %v", name, err))
			return
		}
		files = append(files, sandbox.Attachment{Name: name, B64: base64.StdEncoding.EncodeToString(data)})
		total += info.Size()
	}

	for _, e := range entries {
		name := e.Name()
		staged := filepath.Join(o.dir, name)
		target := filepath.Join(o.shared, name)
		if o.isPrelinked(name, staged) {
			// 预置链接：只有经它原地改写过的既有产物归属本次。
			if info, err := os.Stat(target); err == nil && !info.ModTime().Equal(o.before[name]) {
				attach(name, target)
			}
			continue
		}
		if e.IsDir() {
			if err := moveInto(staged, target); err != nil {
				warnings = append(warnings, fmt.Sprintf("move subdirectory '%s': %v", name, err))
			} else {
				warnings = append(warnings, fmt.Sprintf("subdirectory '%s' skipped (not recursive)", name))
			}
			continue
		}
		// 先读暂存件再移入：并发执行写出同名文件时，各自附件是各自的内容。
		attach(name, staged)
		if err := os.Rename(staged, target); err != nil {
			warnings = append(warnings, fmt.Sprintf("move '%s' into out/: %v", name, err))
		}
	}
	return files, warnings
}

// isPrelinked 判断暂存目录里的 name 是否仍是 stageOutput 预置的那条链接。
func (o *execOutput) isPrelinked(name, staged string) bool {
	if _, existed := o.before[name]; !existed {
		return false
	}
	dest, err := os.Readlink(staged)
	return err == nil && dest == filepath.Join(o.shared, name)
}

// moveInto 把暂存目录下的子目录并入 out/：目标不存在时整体改名，已存在时逐项合并。
func moveInto(src, dst string) error {
	if _, err := os.Lstat(dst); os.IsNotExist(err) {
		return os.Rename(src, dst)
	}
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dst, 0o750); err != nil {
		return err
	}
	for _, e := range entries {
		s, d := filepath.Join(src, e.Name()), filepath.Join(dst, e.Name())
		if e.IsDir() {
			if err := moveInto(s, d); err != nil {
				return err
			}
			continue
		}
		if err := os.Rename(s, d); err != nil {
			return err
		}
	}
	return nil
}
