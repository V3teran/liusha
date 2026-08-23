// Package tool 实现工具目录（tool 表）的持久化层。
//
// 两套工具体系统一入库为可查询目录：
//   - kind=function：进程内原生函数工具，事实源 = registry.FunctionToolCatalog
//   - kind=cli     ：外置沙箱 CLI 工具，事实源 = deployments/.../tools.yaml
//
// 代码为事实源，本表是启动期 Reconcile 幂等同步出的副本（见 reconcile.go）。
// 前端工具模块据此检索/分页/展示，并供智能体配置页「选择工具」而非填空。
package tool

import "time"

// Kind 是 tool.kind 的取值（与 DB CHECK 双保险）。
type Kind string

const (
	KindFunction Kind = "function" // 进程内原生函数工具
	KindCLI      Kind = "cli"       // 外置沙箱 CLI 工具
)

// Tool 是 tool 表行的 Go 表示。
type Tool struct {
	Name        string
	Kind        Kind
	Category    string
	Description string
	SortOrder   int
	SyncedAt    time.Time
}

// ListParams 是 Store.List 的查询入参（可选过滤 + 分页）。
//   - Q     ：按 name/description 模糊匹配（空 = 不过滤）
//   - Kind  ：按体系过滤（空 = 两套都要）
//   - Limit ：<=0 表示不分页（全量），供无翻页场景复用
//   - Offset：分页偏移
type ListParams struct {
	Q      string
	Kind   Kind
	Limit  int
	Offset int
}
