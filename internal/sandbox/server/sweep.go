package server

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/V3teran/liusha/internal/logx"
)

var (
	sweepLog   = logx.New("sandbox.sweep")
	releaseLog = logx.New("sandbox.release")
)

// sweepInterval 是工作区留存清扫的周期。留存期以天计，6 小时的粒度足够；每次清扫要遍历
// 全部任务目录，不宜更频繁。
const sweepInterval = 6 * time.Hour

// RunWorkspaceSweep 按留存天数周期清扫超期的任务工作区（启动时先扫一次），直到 ctx 取消。
// retentionDays ≤ 0 = 永久保留，直接返回。
func RunWorkspaceSweep(ctx context.Context, root string, retentionDays int) {
	if retentionDays <= 0 {
		return
	}
	ticker := time.NewTicker(sweepInterval)
	defer ticker.Stop()
	for {
		cutoff := time.Now().AddDate(0, 0, -retentionDays)
		for _, removed := range sweepExpiredTaskDirs(root, cutoff) {
			sweepLog.Info().Str("dir", removed).Msg("任务工作区超出留存期，已清扫")
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// sweepExpiredTaskDirs 清扫 root 下 <assignment>/<task> 布局中久无活动的任务目录，
// 返回被删目录（相对 root）。
//
// 粒度是任务而不是批次：同一批次的任务可能相隔很久续跑，按批次目录 mtime 判断会把
// 刚写入证据的任务连带删掉（子孙写入不刷新祖先目录的 mtime）。任务的「最近活动」
// 取其子树内最新的 mtime；批次目录在其下任务清空后才删。点前缀目录（附件暂存）
// 有独立的清扫器，不在此处理。
func sweepExpiredTaskDirs(root string, cutoff time.Time) []string {
	var removed []string
	assignments, err := os.ReadDir(root)
	if err != nil {
		sweepLog.Warn().Err(err).Str("root", root).Msg("读取工作区根目录失败")
		return nil
	}
	for _, a := range assignments {
		if !a.IsDir() || strings.HasPrefix(a.Name(), ".") {
			continue
		}
		asgDir := filepath.Join(root, a.Name())
		tasks, err := os.ReadDir(asgDir)
		if err != nil {
			sweepLog.Warn().Err(err).Str("dir", asgDir).Msg("读取批次工作区失败")
			continue
		}
		for _, t := range tasks {
			if !t.IsDir() {
				continue
			}
			taskDir := filepath.Join(asgDir, t.Name())
			if newestModTime(taskDir).After(cutoff) {
				continue
			}
			if err := os.RemoveAll(taskDir); err != nil {
				sweepLog.Warn().Err(err).Str("dir", taskDir).Msg("清扫任务工作区失败")
				continue
			}
			removed = append(removed, filepath.Join(a.Name(), t.Name()))
		}
		// 批次目录只在已空时删除（os.Remove 对非空目录失败，即保留）。
		if err := os.Remove(asgDir); err == nil {
			removed = append(removed, a.Name())
		}
	}
	return removed
}

// newestModTime 返回目录子树内最新的修改时间（含目录自身）。不跟随符号链接。
func newestModTime(dir string) time.Time {
	var newest time.Time
	_ = filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil //nolint:nilerr // 并发删除等竞态：跳过该项继续，不中断整棵子树的活动判断
		}
		if info, ierr := d.Info(); ierr == nil && info.ModTime().After(newest) {
			newest = info.ModTime()
		}
		return nil
	})
	return newest
}
