import { useEffect, useMemo, useRef } from 'react'
import type { Tool, ToolKind } from '@/api/types'
import { KIND_LABEL, categoryLabel } from './toolMeta'

// 种类段：全部 / 内部函数 / 外部 CLI。'' = 不过滤。
export type KindFilter = '' | ToolKind

export const KIND_SEGMENTS: { value: KindFilter; label: string }[] = [
  { value: '', label: '全部' },
  { value: 'function', label: KIND_LABEL.function },
  { value: 'cli', label: KIND_LABEL.cli },
]

// 分组结构：种类 → 分类 → 工具。左栏两级分组渲染。
interface CategoryGroup {
  category: string
  tools: Tool[]
}
interface KindGroup {
  kind: ToolKind
  count: number
  categories: CategoryGroup[]
}

const KIND_ORDER: ToolKind[] = ['function', 'cli']

// 把有序扁平列表按 (kind → category) 两级分组。入参已由后端按 kind, sort_order, name 排序，
// Map 保留首见顺序 → 分类顺序跟随 sort_order，稳定。
export function groupTools(tools: Tool[]): KindGroup[] {
  const byKind = new Map<ToolKind, Map<string, Tool[]>>()
  for (const t of tools) {
    let cats = byKind.get(t.kind)
    if (!cats) {
      cats = new Map()
      byKind.set(t.kind, cats)
    }
    const arr = cats.get(t.category) ?? []
    arr.push(t)
    cats.set(t.category, arr)
  }
  const out: KindGroup[] = []
  for (const kind of KIND_ORDER) {
    const cats = byKind.get(kind)
    if (!cats) continue
    let count = 0
    const categories: CategoryGroup[] = []
    for (const [category, ts] of cats) {
      count += ts.length
      categories.push({ category, tools: ts })
    }
    out.push({ kind, count, categories })
  }
  return out
}

// 左栏主列表：种类分组头（含计数）+ 分类小标题 + 工具行。整栏独立滚动。
// 选中行变化时自动滚入视野（block:nearest 不打断当前视口）——默认选中可能
// 落在滚动区深处，不滚则高亮不可见。
export function ToolMasterList({
  groups,
  segments,
  kind,
  onKind,
  loading,
  error,
  empty,
  emptyHint,
  selected,
  onSelect,
}: {
  groups: KindGroup[]
  segments: { value: KindFilter; label: string }[]
  kind: KindFilter
  onKind: (k: KindFilter) => void
  loading: boolean
  error: string
  empty: boolean
  emptyHint: string
  selected: string | null
  onSelect: (name: string) => void
}) {
  const scrollRef = useRef<HTMLDivElement | null>(null)
  // 渲染序工具名列表：键盘 ↑↓ 导航的次序来源（与可视顺序一致）。
  const orderedNames = useMemo(
    () => groups.flatMap((g) => g.categories.flatMap((c) => c.tools.map((t) => t.name))),
    [groups],
  )

  // 键盘 ↑↓ 在工具行间移动选中：容器持有焦点，行本身不必逐个 Tab。容器是具名
  // region 而非 listbox——行是带 aria-pressed 的按钮，listbox 只允许 option 子项。
  const onListKeyDown = (e: React.KeyboardEvent) => {
    if (e.key !== "ArrowDown" && e.key !== "ArrowUp") return
    e.preventDefault()
    if (orderedNames.length === 0) return
    const cur = orderedNames.indexOf(selected ?? "")
    const next =
      e.key === "ArrowDown"
        ? orderedNames[Math.min(cur + 1, orderedNames.length - 1)]
        : orderedNames[Math.max(cur - 1, 0)]
    if (next !== selected) onSelect(next)
  }

  useEffect(() => {
    if (!selected) return
    const active = scrollRef.current?.querySelector('button[aria-pressed="true"]')
    // jsdom 无 scrollIntoView；可选链调用防御测试环境。
    active?.scrollIntoView?.({ block: "nearest" })
  }, [selected, loading])

  return (
    <div className="flex min-h-0 flex-col overflow-hidden rounded-xl border border-border bg-surface shadow-sm">
      {/* 顶部种类 tab：整宽等分，切换即过滤，无需滚动到下方分组 */}
      <div className="flex flex-shrink-0 border-b border-border">
        {segments.map((seg) => {
          const active = kind === seg.value
          return (
            <button
              key={seg.value}
              type="button"
              onClick={() => onKind(seg.value)}
              aria-pressed={active}
              className={
                'flex-1 border-b-2 px-2 py-2.5 text-sm font-medium transition-colors ' +
                (active
                  ? 'border-b-accent bg-surface-2 text-text'
                  : 'border-b-transparent text-text-secondary hover:bg-surface-2/60 hover:text-text')
              }
            >
              {seg.label}
            </button>
          )
        })}
      </div>
      <div
        ref={scrollRef}
        role="region"
        aria-label="工具列表"
        tabIndex={0}
        onKeyDown={onListKeyDown}
        className="min-h-0 flex-1 overflow-y-auto focus-visible:outline-none focus-visible:border-accent"
      >
        {loading ? (
          <div className="terminal-cursor py-16 text-center font-mono text-sm text-text-secondary">加载中</div>
        ) : error ? (
          <div className="px-4 py-16 text-center font-mono text-sm text-sev-critical">⚠ {error}</div>
        ) : empty ? (
          <div className="px-4 py-16 text-center text-sm text-text-secondary">{emptyHint}</div>
        ) : (
          groups.map((g) => (
            <section key={g.kind}>
              {g.categories.map((c) => (
                <div key={c.category}>
                  <p className="sticky top-0 z-10 border-b border-border bg-surface-2/95 px-4 pb-1 pt-3 text-xs uppercase tracking-wide text-text-tertiary backdrop-blur">
                    {categoryLabel(c.tools[0])}
                  </p>
                  {c.tools.map((t) => (
                    <ToolListRow
                      key={t.name}
                      tool={t}
                      active={t.name === selected}
                      onClick={() => onSelect(t.name)}
                    />
                  ))}
                </div>
              ))}
            </section>
          ))
        )}
      </div>
    </div>
  )
}

// 单个工具行：名称（等宽，沙盒未安装时附标记）+ 描述单行省略。选中态左侧荧光竖条（row-selected）。
function ToolListRow({
  tool,
  active,
  onClick,
}: {
  tool: Tool
  active: boolean
  onClick: () => void
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      aria-pressed={active}
      className={
        'flex w-full flex-col gap-0.5 border-l-2 px-4 py-2 text-left transition-colors ' +
        (active
          ? 'border-l-accent bg-surface-2'
          : 'border-l-transparent hover:bg-surface-2/60')
      }
    >
      <span className="flex min-w-0 items-center gap-2">
        <span className="truncate font-mono text-sm font-semibold text-text">{tool.name}</span>
        {tool.installed === false && <span className="flex-shrink-0 text-xs text-status-warning">未安装</span>}
      </span>
      <span className="truncate text-xs text-text-secondary">{tool.description || '暂无描述'}</span>
    </button>
  )
}
