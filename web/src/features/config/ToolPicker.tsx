import { useMemo } from 'react'

// 工具多选器：按 category 分组（桶内按 name 字典序）渲染可勾选的工具标签。
// 候选只要求「白名单键 + 分组 + 说明」最小结构——Tool 与 Skill 派生对象都天然满足，
// 不为复用伪造 Tool 全字段。
export interface PickerCandidate {
  name: string
  category: string
  description: string
}

export function ToolPicker({
  catalog,
  selected,
  onToggle,
  onSelectAll,
  emptyHint,
}: {
  catalog: PickerCandidate[]
  selected: string[]
  onToggle: (name: string) => void
  // 全选动作（收拢为 ["*"]，展示层等价于目录全集勾选）；未提供或已全选时不渲染按钮。
  onSelectAll?: () => void
  emptyHint: string
}) {
  const byCategory = useMemo(() => {
    const byCat = new Map<string, PickerCandidate[]>()
    for (const t of catalog) {
      const cat = t.category || '未分类'
      const bucket = byCat.get(cat) ?? []
      bucket.push(t)
      byCat.set(cat, bucket)
    }
    return [...byCat.entries()]
      .sort(([a], [b]) => a.localeCompare(b))
      .map(([cat, tools]) => [cat, [...tools].sort((a, b) => a.name.localeCompare(b.name))] as const)
  }, [catalog])

  if (byCategory.length === 0) {
    return <p className="text-sm text-muted">{emptyHint}</p>
  }

  const allSelected = catalog.every((t) => selected.includes(t.name))

  // 结构：固定头部（计数 + 全选，不随列表滚动）+ 内部滚动区。
  // picker 以 flex-1 铺满 tab 内容区剩余高度——单一滚动上下文（列表内部），
  // 外层抽屉体不再出现第二根滚轮。
  return (
    <div className="flex min-h-0 flex-1 flex-col overflow-hidden rounded-md border border-border bg-background">
      {/* 头部定高 h-8：全选按钮出现/消失不改变行高，列表区不跳动。 */}
      <div className="flex h-8 flex-shrink-0 items-center justify-between border-b border-border px-3">
        <span className="text-xs text-faint">共 {catalog.length} 项</span>
        {onSelectAll && !allSelected && (
          <button
            type="button"
            onClick={onSelectAll}
            className="rounded-md border border-border px-2 py-0.5 text-xs text-muted transition-colors hover:border-accent/60 hover:text-accent"
          >
            全选
          </button>
        )}
      </div>
      <div className="min-h-0 flex-1 space-y-3 overflow-y-auto p-3">
        {byCategory.map(([cat, tools]) => (
        <div key={cat}>
          <p className="mb-1 font-mono text-xs uppercase tracking-wide text-muted">{cat}</p>
          <div className="flex flex-wrap gap-2">
            {tools.map((t) => {
              const active = selected.includes(t.name)
              return (
                <button
                  key={t.name}
                  type="button"
                  title={t.description}
                  aria-pressed={active}
                  onClick={() => onToggle(t.name)}
                  className={
                    'rounded-md border px-2 py-1 font-mono text-xs transition-colors ' +
                    (active
                      ? 'border-accent bg-accent/15 text-accent'
                      : 'border-border text-text hover:border-accent/60')
                  }
                >
                  {t.name}
                </button>
              )
            })}
          </div>
        </div>
      ))}
      </div>
    </div>
  )
}
