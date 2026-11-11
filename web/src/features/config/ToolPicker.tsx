import { useMemo } from 'react'
import { categoryLabel } from '@/features/tools/toolMeta'
import type { SetDraft } from '@/lib/override'
import { ToggleChip } from '@/components/ui/ToggleChip'

// 名单多选器：按 category 分组（桶内按 name 字典序）渲染可勾选的标签。
// 候选只要求「白名单键 + 分组 + 说明」最小结构——工具与技能都天然满足。
//
// 「全部（含以后新增）」是独立开关而非全选按钮：全部模式下取消某项 = 全部减此项，
// 以后新增的项仍自动生效；关闭开关则保留当前勾选，成为只含这些项的显式名单。
export interface PickerCandidate {
  name: string
  category: string
  category_name?: string // 分类显示名（工具有；技能没有，展示分类码）
  description: string
}

interface ToolPickerProps {
  catalog: PickerCandidate[]
  draft: SetDraft
  onToggle: (name: string) => void
  onToggleAll?: () => void // 未提供则不渲染「全部」开关
  emptyHint: string
}

export function ToolPicker({ catalog, draft, onToggle, onToggleAll, emptyHint }: ToolPickerProps) {
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

  // 勾选了、但已不在目录中的项（引用的工具或技能已下线）：运行期不生效，单独列出供移除。
  const stale = useMemo(
    () => draft.checked.filter((n) => !catalog.some((t) => t.name === n)),
    [draft.checked, catalog],
  )

  if (byCategory.length === 0 && stale.length === 0) {
    return <p className="text-sm text-text-secondary">{emptyHint}</p>
  }

  const checkedCount = draft.checked.filter((n) => catalog.some((t) => t.name === n)).length

  // 结构：固定头部（计数 + 全部开关，不随列表滚动）+ 内部滚动区（单一滚动上下文）。
  return (
    <div className="flex min-h-0 flex-1 flex-col overflow-hidden rounded-md border border-border bg-background">
      <div className="flex h-9 flex-shrink-0 items-center justify-between border-b border-border px-3">
        <span className="text-xs text-text-tertiary">
          已选 {checkedCount} / {catalog.length}
        </span>
        {onToggleAll && (
          <label className="flex cursor-pointer items-center gap-2 text-xs text-text-secondary">
            <input type="checkbox" checked={draft.all} onChange={onToggleAll} />
            全部（含以后新增）
          </label>
        )}
      </div>
      <div className="min-h-0 flex-1 space-y-3 overflow-y-auto p-3">
        {stale.length > 0 && (
          <div>
            <p className="mb-1 text-xs text-status-warning">已失效（已下线，运行时不生效，点击移除）</p>
            <div className="flex flex-wrap gap-2">
              {stale.map((name) => (
                <button
                  key={name}
                  type="button"
                  aria-pressed
                  onClick={() => onToggle(name)}
                  className="rounded-md border border-status-warning/60 px-2 py-1 font-mono text-xs text-status-warning line-through"
                >
                  {name}
                </button>
              ))}
            </div>
          </div>
        )}
        {byCategory.map(([cat, tools]) => (
          <div key={cat}>
            <p className="mb-1 font-mono text-xs uppercase tracking-wide text-text-secondary">{categoryLabel(tools[0])}</p>
            <div className="flex flex-wrap gap-2">
              {tools.map((t) => {
                const active = draft.checked.includes(t.name)
                return (
                  <ToggleChip key={t.name} mono pressed={active} title={t.description} onClick={() => onToggle(t.name)}>
                    {t.name}
                  </ToggleChip>
                )
              })}
            </div>
          </div>
        ))}
      </div>
    </div>
  )
}
