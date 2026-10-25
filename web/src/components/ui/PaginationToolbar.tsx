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
  'inline-flex h-7 w-7 items-center justify-center rounded-md border border-border text-sm text-text-secondary transition-all hover:border-border-strong hover:bg-surface-2 hover:text-text disabled:pointer-events-none disabled:opacity-40'

/**
 * PaginationToolbar - 统一的翻页工具栏
 *
 * 业界最佳实践（参考 GitHub/Linear）：
 * 1. 总数 + 每页条数 + 翻页按钮统一右对齐
 * 2. 页码显示格式：{page} / {totalPages}
 * 3. 统一的按钮样式和间距
 * 4. 始终显示完整信息（即使只有一页）
 */
export function PaginationToolbar({
  page,
  pageSize,
  total,
  totalPages,
  onPageChange,
  onPageSizeChange,
  loading,
  className = '',
}: PaginationToolbarProps) {
  return (
    <div className={`flex items-center gap-3 ${className}`}>
      <span className="whitespace-nowrap text-sm text-muted">
        共 <strong className="font-semibold text-text">{total}</strong> 条
      </span>

      <PageSizeSelect value={pageSize} onChange={onPageSizeChange} />

      <div className="flex items-center gap-2">
        <button
          type="button"
          disabled={page <= 1 || loading}
          onClick={() => onPageChange(page - 1)}
          aria-label="上一页"
          className={PAGER_BTN}
        >
          <span aria-hidden>‹</span>
        </button>
        <span className="min-w-[3.5rem] text-center text-sm text-muted">
          {page} / {totalPages}
        </span>
        <button
          type="button"
          disabled={page >= totalPages || loading}
          onClick={() => onPageChange(page + 1)}
          aria-label="下一页"
          className={PAGER_BTN}
        >
          <span aria-hidden>›</span>
        </button>
      </div>
    </div>
  )
}
