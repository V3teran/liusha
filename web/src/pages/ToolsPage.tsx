import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { Search } from 'lucide-react'
import { listTools, getTool, assignTool } from '@/api/config'
import type { Tool, ToolDetail, ToolKind } from '@/api/types'
import { ToolDetailPanel } from '@/features/tools/ToolDetailPanel'
import { KIND_LABEL, categoryLabel } from '@/features/tools/toolMeta'

// 种类段：全部 / 内部函数 / 外部 CLI。'' = 不过滤。
type KindFilter = '' | ToolKind

const KIND_SEGMENTS: { value: KindFilter; label: string }[] = [
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
function groupTools(tools: Tool[]): KindGroup[] {
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

// 工具目录（主从双栏）：左栏按种类/分类分组、可搜索、点选；右栏常驻详情面板。
// 有界目录（代码来源），左栏加载全量并分组滚动，故无翻页（分组与分页天然冲突）。
export function ToolsPage() {
  const [kind, setKind] = useState<KindFilter>('')
  const [query, setQuery] = useState('')
  const [tools, setTools] = useState<Tool[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')

  const [selected, setSelected] = useState<string | null>(null)
  const [detail, setDetail] = useState<ToolDetail | null>(null)
  const [detailLoading, setDetailLoading] = useState(false)

  // savingCode 标记装配回存中的智能体，禁用其胶囊防重复点。装配态源出 detail.agents（后端权威）。
  const [savingCode, setSavingCode] = useState<string | null>(null)

  const listSeq = useRef(0)
  const detailSeq = useRef(0)

  // 列表取数：kind 即时、query 防抖 250ms；seq 丢弃过期响应。走无 page 全量接口。
  useEffect(() => {
    const seq = ++listSeq.current
    const run = async () => {
      setLoading(true)
      setError('')
      try {
        const res = await listTools({ kind: kind || undefined, q: query || undefined })
        if (seq !== listSeq.current) return
        setTools(res.tools)
      } catch (e) {
        if (seq !== listSeq.current) return
        setError(e instanceof Error ? e.message : '加载工具目录失败')
        setTools([])
      } finally {
        if (seq === listSeq.current) setLoading(false)
      }
    }
    const timer = setTimeout(run, query ? 250 : 0)
    return () => clearTimeout(timer)
  }, [kind, query])

  const groups = useMemo(() => groupTools(tools), [tools])

  // 渲染序扁平列表（function 组在前的可视顺序）——默认选中取它的首项，
  // 保证左栏可视第一行就是高亮行（数据序 tools[0] 可能在滚动区深处的 cli 组）。
  const renderOrder = useMemo(
    () => groups.flatMap((g) => g.categories.flatMap((c) => c.tools)),
    [groups],
  )

  // 选中项跟随列表：当前选中仍在列表则保留，否则选中可视首项（主从视图恒有选中）。
  useEffect(() => {
    if (renderOrder.length === 0) {
      setSelected(null)
      return
    }
    setSelected((prev) =>
      prev && renderOrder.some((t) => t.name === prev) ? prev : renderOrder[0].name,
    )
  }, [renderOrder])

  // 详情取数：选中变化即拉，seq 丢弃过期。
  useEffect(() => {
    if (!selected) {
      setDetail(null)
      return
    }
    const seq = ++detailSeq.current
    setDetailLoading(true)
    getTool(selected)
      .then((d) => {
        if (seq === detailSeq.current) setDetail(d)
      })
      .catch(() => {
        if (seq === detailSeq.current) setDetail(null)
      })
      .finally(() => {
        if (seq === detailSeq.current) setDetailLoading(false)
      })
  }, [selected])

  // 从工具侧装/卸某智能体的该工具：乐观翻转 detail.agents，调后端 assignTool；
  // 失败回滚并提示。写入是后端权威（改该智能体 function_tools/cli_tools 回存）。
  const onToggle = useCallback(
    async (code: string, next: boolean) => {
      const tool = detail?.tool
      if (!tool) return
      const flip = (involved: boolean): ToolDetail | null => {
        setDetail((prev) =>
          prev
            ? {
                ...prev,
                agents: prev.agents.map((a) => (a.code === code ? { ...a, involved } : a)),
              }
            : prev,
        )
        return null
      }
      setSavingCode(code)
      flip(next)
      try {
        await assignTool(tool.name, code, next)
      } catch (e) {
        flip(!next) // 回滚
        alert(e instanceof Error ? e.message : '保存失败')
      } finally {
        setSavingCode(null)
      }
    },
    [detail],
  )

  return (
    <div className="flex h-full min-h-0 flex-col">
      <header className="flex items-center justify-between border-b border-border px-6 py-4">
        <div className="min-w-0">
          <h1 className="text-base font-semibold text-text">工具</h1>
          <p className="mt-0.5 text-sm text-text-secondary">
            内部与外部工具的统一编目
          </p>
        </div>
      </header>

      {/* 搜索移出标题行，与左栏种类 tab 同一视觉层级——筛选控件挨着数据，不占标题行。 */}
      <div className="flex items-center border-b border-border bg-surface/50 px-6 py-3">
        <div className="relative">
          <Search className="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-text-tertiary" />
          <input
            type="search"
            value={query}
            spellCheck={false}
            placeholder="搜索工具名 / 描述"
            onChange={(e) => setQuery(e.target.value)}
            className="h-9 w-80 rounded-lg border border-border bg-surface pl-10 pr-3 text-sm text-text outline-none transition-all placeholder:text-text-tertiary focus:border-accent focus:glow-accent"
          />
        </div>
      </div>

      <div className="grid min-h-0 flex-1 grid-cols-[minmax(240px,320px)_1fr] gap-4 overflow-hidden p-6">
        <ToolMasterList
          groups={groups}
          segments={KIND_SEGMENTS}
          kind={kind}
          onKind={setKind}
          loading={loading}
          error={error}
          empty={tools.length === 0}
          emptyHint={query ? '无匹配工具' : '工具目录为空——检查后端同步是否成功'}
          selected={selected}
          onSelect={setSelected}
        />
        <ToolDetailPanel
          detail={detail}
          loading={detailLoading}
          savingCode={savingCode}
          onToggle={onToggle}
        />
      </div>
    </div>
  )
}

// 左栏主列表：种类分组头（含计数）+ 分类小标题 + 工具行。整栏独立滚动。
// 选中行变化时自动滚入视野（block:nearest 不打断当前视口）——默认选中可能
// 落在滚动区深处，不滚则高亮不可见。
function ToolMasterList({
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

  // 键盘 ↑↓ 在工具行间移动选中（listbox 惯例）：容器持有焦点，行本身不必逐个 Tab。
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
    <div className="flex min-h-0 flex-col overflow-hidden rounded-xl border border-border bg-surface">
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
                  : 'border-b-transparent text-muted hover:bg-surface-2/60 hover:text-text')
              }
            >
              {seg.label}
            </button>
          )
        })}
      </div>
      <div
        ref={scrollRef}
        role="listbox"
        aria-label="工具列表"
        tabIndex={0}
        onKeyDown={onListKeyDown}
        className="min-h-0 flex-1 overflow-y-auto focus-visible:outline-none focus-visible:border-accent"
      >
        {loading ? (
          <div className="tac-cursor py-16 text-center font-mono text-sm text-muted">加载中</div>
        ) : error ? (
          <div className="px-4 py-16 text-center font-mono text-sm text-sev-critical">⚠ {error}</div>
        ) : empty ? (
          <div className="px-4 py-16 text-center text-sm text-muted">{emptyHint}</div>
        ) : (
          groups.map((g) => (
            <section key={g.kind}>
              {g.categories.map((c) => (
                <div key={c.category}>
                  <p className="sticky top-0 z-10 border-b border-border bg-surface-2/95 px-4 pb-1 pt-3 text-xs uppercase tracking-wide text-faint backdrop-blur">
                    {categoryLabel(c.category)}
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

// 单个工具行：名称（等宽）+ 描述单行省略。选中态左侧荧光竖条（tac-row-active）。
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
      <span className="truncate font-mono text-sm font-semibold text-text">{tool.name}</span>
      <span className="truncate text-xs text-muted">{tool.description || '暂无描述'}</span>
    </button>
  )
}
