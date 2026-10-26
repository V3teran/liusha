import { Children, useEffect, useMemo, useState, type ReactNode } from 'react'
import { ChevronLeft, ChevronRight, Plus, Search } from 'lucide-react'
import { PaginationToolbar } from '@/components/ui/PaginationToolbar'
import { cn } from '@/lib/utils'

const PAGE_SIZE = 12

export interface ServerPaging {
  page: number
  totalPages: number
  count: number
  onPage: (page: number) => void
  size?: number
  onSize?: (size: number) => void
}

export interface SearchBox {
  value: string
  onChange: (v: string) => void
  placeholder?: string
}

interface ConfigListShellProps {
  title: string
  subtitle: string
  loading: boolean
  error: string
  empty: boolean
  emptyHint: string
  onNew?: () => void
  children: ReactNode
  search?: SearchBox
  server?: ServerPaging
  headerExtra?: ReactNode
}

/**
 * ConfigListShell - 配置管理页面外壳
 *
 * 设计改进：
 * 1. 更现代的头部设计
 * 2. 更清晰的加载/错误/空状态
 * 3. 更流畅的卡片网格布局
 * 4. 更优雅的分页控件
 */
export function ConfigListShell({
  title,
  subtitle,
  loading,
  error,
  empty,
  emptyHint,
  onNew,
  children,
  search,
  server,
  headerExtra,
}: ConfigListShellProps) {
  const items = useMemo(() => Children.toArray(children), [children])

  const clientTotalPages = Math.max(1, Math.ceil(items.length / PAGE_SIZE))
  const [clientPage, setClientPage] = useState(0)

  useEffect(() => {
    setClientPage((p) => Math.min(p, clientTotalPages - 1))
  }, [clientTotalPages])

  const pageItems = server ? items : items.slice(clientPage * PAGE_SIZE, clientPage * PAGE_SIZE + PAGE_SIZE)

  const showBottomPager = server ? false : items.length > 0

  return (
    <div className="flex h-full min-h-0 flex-col">
      {/* 头部 */}
      <header className="flex items-center justify-between gap-4 border-b border-border bg-surface px-6 py-4">
        <div className="min-w-0">
          <h1 className="text-base font-semibold text-text">
            {title}
          </h1>
          <p className="mt-0.5 text-sm text-text-secondary">{subtitle}</p>
        </div>
        <div className="flex flex-shrink-0 items-center gap-3">
          {headerExtra}
          {onNew && (
            <button
              type="button"
              onClick={onNew}
              className={cn(
                'flex h-9 items-center gap-2 rounded-lg px-4',
                'text-sm font-semibold transition-all',
                'bg-accent-soft text-accent hover:bg-accent/10 hover:text-accent-hover'
              )}
            >
              <Plus className="h-4 w-4" strokeWidth={2.5} />
              新建
            </button>
          )}
        </div>
      </header>

      {/* 搜索/筛选栏 */}
      {(search || (server?.size != null && server.onSize)) && (
        <div className="flex flex-wrap items-center gap-3 border-b border-border bg-surface/30 px-6 py-3">
          {search && (
            <div className="relative">
              <Search className="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-text-tertiary" />
              <input
                type="search"
                value={search.value}
                spellCheck={false}
                placeholder={search.placeholder ?? '搜索'}
                onChange={(e) => search.onChange(e.target.value)}
                className={cn(
                  'h-9 w-72 rounded-lg border border-border bg-surface pl-9 pr-3 text-sm text-text',
                  'outline-none transition-all placeholder:text-text-tertiary',
                  'focus:border-accent focus:glow-accent'
                )}
              />
            </div>
          )}

          {server?.size != null && server.onSize && (server.count > 0 || server.page > 1) && (
            <div className="ml-auto">
              <PaginationToolbar
                page={server.page}
                pageSize={server.size}
                total={server.count}
                totalPages={server.totalPages}
                onPageChange={server.onPage}
                onPageSizeChange={server.onSize}
              />
            </div>
          )}
        </div>
      )}

      {/* 内容区 */}
      <div className="min-h-0 flex-1 overflow-y-auto bg-background p-6">
        {loading ? (
          <div className="flex flex-col items-center justify-center py-20">
            <div className="h-8 w-8 animate-spin rounded-full border-3 border-accent border-t-transparent" />
            <p className="mt-4 font-mono text-sm text-text-secondary terminal-cursor">加载中</p>
          </div>
        ) : error ? (
          <div className="flex flex-col items-center justify-center py-20">
            <div className="flex h-12 w-12 items-center justify-center rounded-full bg-status-error/10">
              <span className="text-2xl">⚠</span>
            </div>
            <p className="mt-4 font-mono text-sm text-status-error">{error}</p>
          </div>
        ) : empty ? (
          <div className="flex flex-col items-center justify-center py-20">
            <div className="flex h-16 w-16 items-center justify-center rounded-full bg-surface-2">
              <span className="text-3xl opacity-50">📦</span>
            </div>
            <p className="mt-4 text-sm text-text-secondary">{emptyHint}</p>
          </div>
        ) : (
          <div className="grid grid-cols-[repeat(auto-fill,minmax(320px,1fr))] gap-4">
            {pageItems}
          </div>
        )}
      </div>

      {/* 服务端分页且无 size 选择器时的底部分页条 */}
      {server && server.size == null && server.count > 0 && (
        <PagerBar
          page={server.page - 1}
          totalPages={server.totalPages}
          count={server.count}
          onPage={(p) => server.onPage(p + 1)}
          showCount
        />
      )}

      {/* 客户端分页的底部分页条 */}
      {showBottomPager && (
        <PagerBar
          page={clientPage}
          totalPages={clientTotalPages}
          count={items.length}
          onPage={setClientPage}
          showCount
        />
      )}
    </div>
  )
}

function PagerBar({
  page,
  totalPages,
  count,
  onPage,
  showCount,
}: {
  page: number
  totalPages: number
  count: number
  onPage: (p: number) => void
  showCount: boolean
}) {
  const btn = cn(
    'flex items-center gap-1.5 rounded-lg border border-border px-3 py-1.5',
    'text-xs text-text transition-colors',
    'hover:border-accent/40 hover:bg-surface-2',
    'disabled:cursor-not-allowed disabled:opacity-40 disabled:hover:border-border disabled:hover:bg-transparent'
  )

  return (
    <div className="flex items-center justify-between border-t border-border bg-surface/50 px-6 py-3">
      {showCount ? <span className="font-mono text-xs text-text-secondary">共 {count} 项</span> : <span />}
      <div className="flex items-center gap-2.5">
        <button type="button" onClick={() => onPage(page - 1)} disabled={page === 0} className={btn}>
          <ChevronLeft className="h-3.5 w-3.5" />
          上一页
        </button>
        <span className="font-mono text-xs text-text-secondary">
          第 <span className="font-semibold text-text">{page + 1}</span> / {totalPages} 页
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

/**
 * ConfigRow - 配置卡片
 *
 * 设计改进：
 * 1. 更精致的卡片样式（柔和阴影、圆角）
 * 2. 更清晰的悬停状态
 * 3. 更流畅的过渡效果
 */
export function ConfigRow({
  name,
  description,
  onClick,
  right,
  leading,
  dimmed = false,
  active = false,
}: {
  name: string
  description?: string
  onClick: () => void
  right?: ReactNode
  // 标题左侧的身份位（如图标/色环），可选——用于展示资源自身的视觉身份。
  leading?: ReactNode
  dimmed?: boolean
  active?: boolean
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      className={cn(
        'group flex min-h-[130px] flex-col gap-3 rounded-xl border bg-surface p-5 text-left',
        'transition-all duration-200',
        active
          ? 'row-selected shadow-md'
          : 'border-border shadow-sm hover:border-accent/40 hover:bg-surface-2 hover:shadow-md hover:glow-accent',
        dimmed && 'opacity-50'
      )}
    >
      <div className="flex items-start justify-between gap-3">
        <div className="flex min-w-0 flex-1 items-center gap-2.5">
          {leading}
          <span className="min-w-0 flex-1 truncate text-base font-semibold text-text group-hover:text-accent transition-colors">
            {name}
          </span>
        </div>
        {right && <div className="flex-shrink-0">{right}</div>}
      </div>
      <p className="truncate-2 min-w-0 text-xs leading-relaxed text-text-secondary">
        {description || '暂无描述'}
      </p>
    </button>
  )
}
