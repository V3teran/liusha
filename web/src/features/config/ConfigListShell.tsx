import type { ReactNode } from 'react'
import { AlertTriangle, Inbox, Plus } from 'lucide-react'
import { PageHeader } from '@/components/PageHeader'
import { FilterBar } from '@/components/FilterBar'
import { SearchInput } from '@/components/SearchInput'
import { Button } from '@/components/ui/Button'
import { PaginationToolbar } from '@/components/ui/PaginationToolbar'
import { cn } from '@/lib/utils'

/** 服务端分页参数（配置资源列表一律走服务端分页 + 每页条数）。 */
export interface ServerPaging {
  page: number
  totalPages: number
  count: number
  onPage: (page: number) => void
  size: number
  onSize: (size: number) => void
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
  server: ServerPaging
  headerExtra?: ReactNode
}

/**
 * ConfigListShell - 配置资源（智能体 / 技能）卡片列表页外壳。
 *
 * 版式与全站列表页同构：页头（标题 + 新建）→ 筛选栏（左搜索 / 右分页）→ 卡片网格滚动区。
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
  return (
    <div className="flex h-full min-h-0 flex-col">
      <PageHeader title={title} subtitle={subtitle}>
        {headerExtra}
        {onNew && (
          <Button onClick={onNew}>
            <Plus strokeWidth={2.4} />
            新建
          </Button>
        )}
      </PageHeader>

      <FilterBar
        label={`${title}筛选`}
        trailing={
          <PaginationToolbar
            page={server.page}
            pageSize={server.size}
            total={server.count}
            totalPages={server.totalPages}
            onPageChange={server.onPage}
            onPageSizeChange={server.onSize}
          />
        }
      >
        {search && (
          <SearchInput
            className="w-64"
            value={search.value}
            onChange={search.onChange}
            placeholder={search.placeholder ?? '搜索'}
          />
        )}
      </FilterBar>

      <div className="min-h-0 flex-1 overflow-y-auto px-6 py-4">
        {loading ? (
          <div role="status" className="flex flex-col items-center justify-center py-20">
            <div className="h-8 w-8 animate-spin rounded-full border-2 border-accent border-t-transparent" aria-hidden />
            <p className="mt-4 font-mono text-sm text-text-secondary terminal-cursor">加载中</p>
          </div>
        ) : error ? (
          <div role="alert" className="flex flex-col items-center justify-center py-20">
            <span className="flex h-12 w-12 items-center justify-center rounded-full bg-status-error/10 text-status-error">
              <AlertTriangle aria-hidden className="h-6 w-6" />
            </span>
            <p className="mt-4 font-mono text-sm text-status-error">{error}</p>
          </div>
        ) : empty ? (
          <div className="flex flex-col items-center justify-center py-20">
            <span className="flex h-12 w-12 items-center justify-center rounded-full bg-surface-2 text-text-tertiary">
              <Inbox aria-hidden className="h-6 w-6" />
            </span>
            <p className="mt-4 text-sm text-text-secondary">{emptyHint}</p>
          </div>
        ) : (
          <div className="grid grid-cols-[repeat(auto-fill,minmax(320px,1fr))] gap-4">{children}</div>
        )}
      </div>
    </div>
  )
}

/**
 * ConfigRow - 配置卡片
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
  // 标题支持节点：如「中文名 + 英文标识后缀」的复合标题（字符串照旧）。
  name: ReactNode
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
