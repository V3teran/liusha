import { ChevronLeft, ChevronRight } from 'lucide-react'
import { cn } from '@/lib/utils'
import { PageSizeSelect } from './PageSizeSelect'

interface PaginationToolbarProps {
  page: number
  pageSize: number
  total: number
  totalPages: number
  onPageChange: (page: number) => void
  onPageSizeChange: (size: number) => void
  loading?: boolean
  className?: string
}

const PAGER_BTN =
  'inline-flex h-8 w-8 items-center justify-center rounded-md border border-border text-text-secondary transition-colors hover:border-border-strong hover:bg-surface-2 hover:text-text disabled:pointer-events-none disabled:opacity-40'

/**
 * PaginationToolbar - 统一的翻页工具栏
 *
 * 分页条惯例（列表页通用形态，放在 FilterBar 的 trailing 槽）：
 * 1. 总数 + 每页条数 + 翻页按钮统一右对齐，控件同为 sm 档（32px）
 * 2. 页码显示格式：{page} / {totalPages}
 * 3. 始终显示完整信息（即使只有一页）
 */
export function PaginationToolbar({
  page,
  pageSize,
  total,
  totalPages,
  onPageChange,
  onPageSizeChange,
  loading,
  className,
}: PaginationToolbarProps) {
  const pages = Math.max(1, totalPages)
  return (
    <nav aria-label="分页" className={cn('flex items-center gap-3', className)}>
      <span className="whitespace-nowrap text-sm text-text-secondary">
        共 <strong className="font-semibold tabular-nums text-text">{total}</strong> 条
      </span>

      <PageSizeSelect value={pageSize} onChange={onPageSizeChange} />

      <div className="flex items-center gap-1.5">
        <button
          type="button"
          disabled={page <= 1 || loading}
          onClick={() => onPageChange(page - 1)}
          aria-label="上一页"
          className={PAGER_BTN}
        >
          <ChevronLeft aria-hidden className="h-4 w-4" />
        </button>
        <span className="min-w-[3.5rem] text-center text-sm tabular-nums text-text-secondary">
          {page} / {pages}
        </span>
        <button
          type="button"
          disabled={page >= pages || loading}
          onClick={() => onPageChange(page + 1)}
          aria-label="下一页"
          className={PAGER_BTN}
        >
          <ChevronRight aria-hidden className="h-4 w-4" />
        </button>
      </div>
    </nav>
  )
}
