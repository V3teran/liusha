import { Children, useEffect, useMemo, useState, type ReactNode } from 'react'
import { ChevronLeft, ChevronRight, Plus } from 'lucide-react'

// 每页卡片数：4 列 × 3 行的整屏网格。超过才出翻页条，数据少时零干扰。
const PAGE_SIZE = 12

interface ConfigListShellProps {
  title: string
  subtitle: string // 一句话说明该资源是什么
  loading: boolean
  error: string
  empty: boolean
  emptyHint: string
  onNew: () => void
  children: ReactNode // 卡片集合（每个资源一张 ConfigCard）
}

// 配置管理三页共用的列表外壳：头部（标题 + 说明 + 新建）+ 加载/错误/空态 + 卡片网格 + 客户端分页。
// 各页只管把「卡片」作为 children 注入；网格布局、翻页、状态分支统一在此。
export function ConfigListShell({
  title,
  subtitle,
  loading,
  error,
  empty,
  emptyHint,
  onNew,
  children,
}: ConfigListShellProps) {
  const items = useMemo(() => Children.toArray(children), [children])
  const totalPages = Math.max(1, Math.ceil(items.length / PAGE_SIZE))
  const [page, setPage] = useState(0)

  // 数据量变化（重载/增删）后夹紧页码，避免停在已不存在的空页。
  useEffect(() => {
    setPage((p) => Math.min(p, totalPages - 1))
  }, [totalPages])

  const start = page * PAGE_SIZE
  const pageItems = items.slice(start, start + PAGE_SIZE)

  return (
    <div className="flex h-full min-h-0 flex-col">
      <header className="flex items-center justify-between border-b border-border px-6 py-4">
        <div>
          <h1 className="tac-prompt font-mono text-[15px] font-semibold text-text">{title}</h1>
          <p className="mt-0.5 text-[12.5px] text-muted">{subtitle}</p>
        </div>
        <button
          type="button"
          onClick={onNew}
          className="flex items-center gap-1.5 rounded-lg bg-accent px-3.5 py-1.5 text-[13px] text-white transition-all hover:bg-accent-hover hover:shadow-[var(--glow-accent-strong)]"
        >
          <Plus className="h-4 w-4" />
          新建
        </button>
      </header>

      <div className="min-h-0 flex-1 overflow-y-auto p-6">
        {loading ? (
          <div className="tac-cursor py-16 text-center font-mono text-[13.5px] text-muted">加载中</div>
        ) : error ? (
          <div className="py-16 text-center font-mono text-[13.5px] text-sev-critical">⚠ {error}</div>
        ) : empty ? (
          <div className="py-16 text-center text-[13.5px] text-muted">{emptyHint}</div>
        ) : (
          <div className="grid grid-cols-[repeat(auto-fill,minmax(220px,1fr))] gap-3">{pageItems}</div>
        )}
      </div>

      {totalPages > 1 && (
        <PagerBar page={page} totalPages={totalPages} count={items.length} onPage={setPage} />
      )}
    </div>
  )
}

// 翻页条：上一页/下一页 + 「第 x / y 页」。仅在多于一页时由外壳挂出。
function PagerBar({
  page,
  totalPages,
  count,
  onPage,
}: {
  page: number
  totalPages: number
  count: number
  onPage: (p: number) => void
}) {
  const btn =
    'flex items-center gap-1 rounded-md border border-border px-2.5 py-1 text-[12.5px] text-text transition-colors hover:border-accent/60 hover:bg-surface-2 disabled:cursor-not-allowed disabled:opacity-40 disabled:hover:border-border disabled:hover:bg-transparent'
  return (
    <div className="flex items-center justify-between border-t border-border px-6 py-2.5">
      <span className="font-mono text-[12px] text-muted">共 {count} 项</span>
      <div className="flex items-center gap-2">
        <button type="button" onClick={() => onPage(page - 1)} disabled={page === 0} className={btn}>
          <ChevronLeft className="h-3.5 w-3.5" />
          上一页
        </button>
        <span className="font-mono text-[12.5px] text-muted">
          第 {page + 1} / {totalPages} 页
        </span>
        <button
          type="button"
          onClick={() => onPage(page + 1)}
          disabled={page >= totalPages - 1}
          className={btn}
        >
          下一页
          <ChevronRight className="h-3.5 w-3.5" />
        </button>
      </div>
    </div>
  )
}

// 配置卡片：整卡可点开编辑抽屉。顶部 code（等宽）+ 右侧徽章/元信息，下方 name。
// 从「整行」改为网格卡片，消除单行铺满的空旷感。
export function ConfigRow({
  code,
  name,
  onClick,
  right,
  dimmed = false,
  active = false,
}: {
  code: string
  name: string
  onClick: () => void
  right?: ReactNode // 右侧徽章/元信息
  dimmed?: boolean // disabled 项淡显
  active?: boolean // 选中态（主从视图预留）：荧光竖条 + 描边
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      className={
        'flex min-h-[76px] flex-col justify-between gap-2 rounded-xl border bg-surface px-4 py-3 text-left transition-all ' +
        (active
          ? 'tac-row-active'
          : 'border-border hover:border-accent/60 hover:bg-surface-2 hover:shadow-[var(--glow-accent)]') +
        (dimmed ? ' opacity-55' : '')
      }
    >
      <div className="flex items-start justify-between gap-2">
        <code className="flex-shrink-0 rounded border border-accent/30 bg-accent-soft px-1.5 py-0.5 font-mono text-[11.5px] text-accent">
          {code}
        </code>
        {right}
      </div>
      <span className="min-w-0 truncate text-[13.5px] text-text">{name}</span>
    </button>
  )
}
