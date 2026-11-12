package toolstore

import (
	"context"
	"fmt"

	"github.com/V3teran/liusha/internal/registry"
	"github.com/V3teran/liusha/internal/tools/manifest"
)

// Reconcile 把两套工具体系的事实源（代码）幂等同步进 tool 目录表：
//   - function 工具：registry.FunctionToolCatalog（进程内声明式清单）
//   - cli 工具     ：工具清单（manifest.Default）
//
// 语义：按体系分别 upsert + 按体系 prune 掉不再存在的旧行（下线工具）。启动期调用一次即可。
// 返回同步与清理的计数，供启动日志观测。
func Reconcile(ctx context.Context, store *Store, m *manifest.ToolManifest) (upserted, pruned int, err error) {
	// ① function 工具：按 catalog 声明顺序赋 sort_order，稳定前端展示。
	var keepFn []string
	for i, meta := range registry.FunctionToolCatalog {
		t := Definition{
			Name:         meta.Name,
			Kind:         KindFunction,
			Category:     string(meta.Category),
			CategoryName: meta.Category.Name(),
			Description:  meta.Description,
			SortOrder:    i,
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

	// ② cli 工具
	var keepCLI []string
	categories := m.CategoryByCode()
	for i, mt := range m.Tools {
		name := categories[mt.Category].Name
		if name == "" {
			name = mt.Category
		}
		t := Definition{
			Name:         mt.Name,
			Kind:         KindCLI,
			Category:     mt.Category,
			CategoryName: name,
			Description:  mt.Description,
			SortOrder:    i,
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
