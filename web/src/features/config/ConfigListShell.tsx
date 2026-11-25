import type { ReactNode } from 'react'
import { Plus } from 'lucide-react'
import { PageHeader } from '@/components/PageHeader'
import { FilterBar } from '@/components/FilterBar'
import { SearchInput } from '@/components/SearchInput'
import { Button } from '@/components/ui/Button'
import { PaginationToolbar } from '@/components/ui/PaginationToolbar'
import { EmptyState } from '@/components/ui/EmptyState'
import { cn } from '@/lib/utils'
import { LoadingState } from '@/components/ui/LoadingState'
import { ErrorState } from '@/components/ui/ErrorState'

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
  /** 回车：有未生效的词立即生效（见 SearchInput 的 onSubmit） */
  onSubmit?: () => void
  /** 按当前的词重取最新数据（见 SearchInput 的 onRefresh） */
  onRefresh?: () => void
  placeholder?: string
}

interface ConfigListShellProps {
  title: string
  loading: boolean
  error: string
  /** 加载失败时的重试（重取列表） */
  onRetry?: () => void
  empty: boolean
  /** 空态名词；搜索框有词时显示「没有匹配的X」，否则「暂无X」+ emptyHint */
  noun: string
  emptyHint: string
  onNew?: () => void
  children: ReactNode
  search?: SearchBox
  server: ServerPaging
  headerExtra?: ReactNode
  /** 卡片至多两列：条目少且固定的资源（如四个智能体）铺满两列，不在三列网格里留下落单的一张 */
  columns?: 2
}

/**
 * ConfigListShell - 配置资源（智能体 / 技能）卡片列表页外壳。
 *
 * 版式与全站列表页同构：页头（标题 + 新建）→ 筛选栏（左搜索 / 右分页）→ 卡片网格滚动区。
 */
export function ConfigListShell({
  title,
  loading,
  error,
  onRetry,
  empty,
  noun,
  emptyHint,
  onNew,
  children,
  search,
  server,
  headerExtra,
  columns,
}: ConfigListShellProps) {
  return (
    <div className="flex h-full min-h-0 flex-col">
      <PageHeader title={title}>
        {headerExtra}
        {onNew && (
          <Button onClick={onNew}>
            <Plus />
            新建
          </Button>
        )}
      </PageHeader>

      <FilterBar
        label={`${title}筛选`}
        canReset={!!search?.value}
        onReset={() => search?.onChange('')}
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
            onSubmit={search.onSubmit}
            onRefresh={search.onRefresh}
            placeholder={search.placeholder ?? '搜索'}
          />
        )}
      </FilterBar>

      <div className="min-h-0 flex-1 overflow-y-auto px-6 py-4">
        {loading ? (
          <LoadingState className="py-20" />
        ) : error ? (
          <ErrorState message={error} onRetry={onRetry} className="py-20" />
        ) : empty ? (
          search?.value.trim() ? (
            <EmptyState kind="no-match" scope="search" noun={noun} className="py-20" />
          ) : (
            <EmptyState kind="empty" noun={noun} hint={emptyHint} className="py-20" />
          )
        ) : (
          <div
            className={
              columns === 2
                ? 'grid grid-cols-1 gap-4 lg:grid-cols-2'
                : 'grid grid-cols-[repeat(auto-fill,minmax(320px,1fr))] gap-4'
            }
          >
            {children}
          </div>
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
  meta,
  dimmed = false,
  active = false,
}: {
  // 标题支持节点：如「中文名 + 英文标识后缀」的复合标题（字符串照旧）。
  name: ReactNode
  description?: string
  onClick: () => void
  right?: ReactNode
  // 卡片底部的配置概要（如档位 / 工具数 / 技能数），描述之下、分隔线之上对齐到卡底。
  meta?: ReactNode
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
        'transition-[color,background-color,border-color,box-shadow] duration-200',
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
      {meta && <div className="mt-auto border-t border-border-subtle pt-3">{meta}</div>}
    </button>
  )
}
