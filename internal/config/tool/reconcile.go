package tool

import (
	"context"
	"fmt"

	"github.com/V3teran/liusha/internal/einotools"
	"github.com/V3teran/liusha/internal/tools/manifest"
)

// Reconcile 把两套工具体系的事实源（代码）幂等同步进 tool 目录表：
//   - function 工具：einotools.FunctionToolCatalog（进程内声明式清单）
//   - cli 工具     ：tools.yaml 解析出的 Manifest（可为 nil：加载失败时降级，只同步 function 部分）
//
// 语义：按体系分别 upsert + 按体系 prune 掉不再存在的旧行（下线工具）。启动期调用一次即可。
// prune 按 kind 限定作用域：manifest 为 nil 时完全不碰既有 cli 目录（不误删）。
// 返回同步与清理的计数，供启动日志观测。
func Reconcile(ctx context.Context, store *Store, m *manifest.Manifest) (upserted, pruned int, err error) {
	// ① function 工具：按 catalog 声明顺序赋 sort_order，稳定前端展示。
	var keepFn []string
	for i, meta := range einotools.FunctionToolCatalog {
		t := Tool{
			Name:        meta.Name,
			Kind:        KindFunction,
			Category:    string(meta.Category),
			Description: meta.Description,
			SortOrder:   i,
		}
		if e := store.Upsert(ctx, t); e != nil {
			return upserted, pruned, fmt.Errorf("reconcile function tools: %w", e)
		}
		keepFn = append(keepFn, t.Name)
		upserted++
	}
	n, e := store.PruneExceptKind(ctx, KindFunction, keepFn)
	if e != nil {
		return upserted, pruned, fmt.Errorf("reconcile prune function: %w", e)
	}
	pruned += n

	// ② cli 工具：manifest 可能 nil（tools.yaml 缺失），此时整段跳过——既不同步也不 prune 既有 cli。
	if m == nil {
		return upserted, pruned, nil
	}
	var keepCLI []string
	for i, mt := range m.Tools {
		t := Tool{
			Name:        mt.Name,
			Kind:        KindCLI,
			Category:    mt.Category,
			Description: mt.Description,
			SortOrder:   i,
		}
		if e := store.Upsert(ctx, t); e != nil {
			return upserted, pruned, fmt.Errorf("reconcile cli tools: %w", e)
		}
		keepCLI = append(keepCLI, t.Name)
		upserted++
	}
	n, e = store.PruneExceptKind(ctx, KindCLI, keepCLI)
	if e != nil {
		return upserted, pruned, fmt.Errorf("reconcile prune cli: %w", e)
	}
	pruned += n

	return upserted, pruned, nil
}
