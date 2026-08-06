import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { Search } from 'lucide-react'
import { listTools, getTool, assignTool } from '@/api/config'
import type { Tool, ToolDetail, ToolKind } from '@/api/types'
import { ToolDetailPanel } from '@/features/tools/ToolDetailPanel'
import { KindBadge } from '@/features/tools/KindBadge'
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

  // 选中项跟随列表：当前选中仍在列表则保留，否则选中首项（主从视图恒有选中）。
  useEffect(() => {
    if (tools.length === 0) {
      setSelected(null)
      return
    }
    setSelected((prev) => (prev && tools.some((t) => t.name === prev) ? prev : tools[0].name))
  }, [tools])

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
      <header className="flex items-center justify-between gap-4 border-b border-border px-6 py-4">
        <div className="min-w-0">
          <h1 className="tac-prompt font-mono text-[15px] font-semibold text-text">工具</h1>
          <p className="mt-0.5 text-[12.5px] text-muted">
            内部函数工具与外部 CLI 工具的统一编目，源出代码、启动期同步入库
          </p>
        </div>
        <div className="relative flex-shrink-0">
          <Search className="pointer-events-none absolute left-2.5 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-muted" />
          <input
            type="search"
            value={query}
            spellCheck={false}
            placeholder="搜索工具名 / 描述"
            onChange={(e) => setQuery(e.target.value)}
            className="w-52 rounded-md border border-border bg-surface py-1.5 pl-8 pr-2.5 text-[13px] text-text outline-none transition-shadow placeholder:text-faint focus:border-accent focus:shadow-[var(--glow-accent)]"
          />
        </div>
      </header>

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
                'flex-1 border-b-2 px-2 py-2.5 text-[12.5px] font-medium transition-colors ' +
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
      <div className="min-h-0 flex-1 overflow-y-auto">
        {loading ? (
          <div className="tac-cursor py-16 text-center font-mono text-[13.5px] text-muted">加载中</div>
        ) : error ? (
          <div className="px-4 py-16 text-center font-mono text-[13px] text-sev-critical">⚠ {error}</div>
        ) : empty ? (
          <div className="px-4 py-16 text-center text-[13px] text-muted">{emptyHint}</div>
        ) : (
          groups.map((g) => (
            <section key={g.kind}>
              <div className="sticky top-0 z-10 flex items-center justify-between gap-2 border-b border-border bg-surface-2/95 px-4 py-2 backdrop-blur">
                <KindBadge kind={g.kind} />
                <span className="font-mono text-[11.5px] text-faint">{g.count}</span>
              </div>
              {g.categories.map((c) => (
                <div key={c.category}>
                  <p className="px-4 pb-1 pt-3 text-[11px] uppercase tracking-wide text-faint">
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
      <span className="truncate font-mono text-[13px] font-semibold text-text">{tool.name}</span>
      <span className="truncate text-[12px] text-muted">{tool.description || '暂无描述'}</span>
    </button>
  )
}
