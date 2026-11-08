import { flexRender, getCoreRowModel, useReactTable, type ColumnDef, type TableMeta } from '@tanstack/react-table'
import { cn } from '@/lib/utils'
import { columnPixelWidths, totalColumnWidth } from '@/lib/tableLayout'

const SKELETON_ROWS = 8

interface DataTableProps<T> {
  rows: T[]
  columns: ColumnDef<T, any>[]
  /** 单元格水平内边距（px-3 / px-4，表头同步） */
  cellPadding?: 'px-3' | 'px-4'
  loading: boolean
  hasFilter?: boolean
  /** 空态文案（hasFilter 区分「无匹配」与「暂无」两态） */
  emptyText: string
  /** 行数据错误标记（如 HTTP 5xx 流量行整行红染） */
  rowError?: (row: T) => boolean
  /** 行级内联样式（如漏洞行把严重度色挂到 --sev 变量驱动左边条悬停） */
  rowStyle?: (row: T) => React.CSSProperties | undefined
  /** 行 className 追加（如保存中降透明度） */
  rowClassName?: (row: T) => string | undefined
  /** 选中态（行 aria-selected + inset 描边；不传则行无选中语义） */
  selectedId?: string | number | null
  /** 行主键（默认 row.id；getRowId 同源） */
  getRowId?: (row: T) => string
  /** 行可点击（点击/键盘 Enter/Space 触发），不传则行不可点 */
  onRowClick?: (row: T) => void
  /** 行 aria-label（无障碍；onRowClick 存在时建议提供） */
  rowAriaLabel?: (row: T) => string
  /** react-table meta（列工厂内 ctx.table.options.meta 消费，如 # 序号列 startIndex） */
  meta?: TableMeta<T>
  /** 表头 sticky（长列表滚动时列头常驻） */
  stickyHeader?: boolean
  /** 表格无障碍名（可点击行时表格以具名 grid 暴露） */
  ariaLabel?: string
}

/**
 * react-table 通用外壳：table-fixed + colgroup 设计像素列宽（columnDef.size 单点
 * 定义——窄容器锁像素出真实横向滚动，宽容器按权重等比拉伸，见 tableLayout）+
 * 骨架屏/空态/行点击与键盘可达/选中态描边。
 * 之前此骨架在 Task/Traffic/LlmAudit/Findings 四张表各复制一份（~80 行/份）。
 */
export function DataTable<T>({ rows, columns, cellPadding = 'px-4', loading, hasFilter, emptyText, rowError, selectedId, getRowId, onRowClick, rowAriaLabel, rowStyle, rowClassName, meta, stickyHeader, ariaLabel }: DataTableProps<T>) {
  const table = useReactTable({
    data: rows,
    columns,
    getCoreRowModel: getCoreRowModel(),
    getRowId: getRowId ?? ((row: T) => String((row as { id?: unknown }).id ?? '')),
    meta,
  })
  const leafColumns = table.getAllLeafColumns()
  const sizes = leafColumns.map((col) => col.columnDef.size ?? 1)
  const colWidths = columnPixelWidths(sizes)
  const minWidth = totalColumnWidth(sizes)
  const colCount = leafColumns.length
  const cellCls = `overflow-hidden ${cellPadding} py-2`

  return (
    <div className="overflow-x-auto rounded-2xl border border-border bg-surface shadow">
      {/* 可选行表格走 WAI-ARIA grid 模式：表格为 grid、行保持原生 row 语义——选中态
          aria-selected 在 row 上合法，单元格内可放交互控件（复制按钮等）。 */}
      <table
        role={onRowClick ? 'grid' : undefined}
        aria-label={ariaLabel}
        className="w-full border-collapse table-fixed"
        style={{ minWidth }}
      >
        <colgroup>
          {leafColumns.map((col, i) => (
            <col key={col.id} style={{ width: colWidths[i] }} />
          ))}
        </colgroup>
        <thead className={cn(stickyHeader && 'sticky top-0 z-10 bg-surface')}>
          {table.getHeaderGroups().map((hg) => (
            <tr key={hg.id} className="border-b border-border">
              {hg.headers.map((h) => (
                <th
                  key={h.id}
                  className={cn(
                    `${cellPadding} py-2.5 text-left text-xs font-semibold uppercase tracking-wide text-text-tertiary`,
                    h.column.columnDef.meta?.align === 'right' && 'text-right',
                  )}
                >
                  {flexRender(h.column.columnDef.header, h.getContext())}
                </th>
              ))}
            </tr>
          ))}
        </thead>
        <tbody>
          {loading && rows.length === 0 ? (
            Array.from({ length: SKELETON_ROWS }).map((_, i) => (
              <tr key={i} className="border-b border-border last:border-b-0">
                {Array.from({ length: colCount }).map((_, c) => (
                  <td key={c} className={cellCls}>
                    <span className="block h-2.5 animate-pulse rounded bg-surface-2" />
                  </td>
                ))}
              </tr>
            ))
          ) : rows.length === 0 ? (
            <tr>
              <td colSpan={colCount} className="px-4 py-11 text-center text-sm text-text-secondary">
                {hasFilter ? `当前筛选无匹配（${emptyText}）` : `暂无${emptyText}`}
              </td>
            </tr>
          ) : (
            table.getRowModel().rows.map((row) => {
              const v = row.original
              const clickable = !!onRowClick
              const selected = clickable && selectedId != null && row.id === String(selectedId)
              return (
                <tr
                  key={row.id}
                  tabIndex={clickable ? 0 : undefined}
                  aria-label={rowAriaLabel?.(v)}
                  aria-selected={clickable ? selected : undefined}
                  onClick={clickable ? () => onRowClick(v) : undefined}
                  onKeyDown={
                    clickable
                      ? (e) => {
                          if (e.key === 'Enter' || e.key === ' ') {
                            e.preventDefault()
                            onRowClick(v)
                          }
                        }
                      : undefined
                  }
                  style={rowStyle?.(v)}
                  data-error={rowError?.(v)}
                  className={cn(
                    'border-b border-border text-sm transition-colors last:border-b-0',
                    clickable && 'cursor-pointer hover:bg-surface-2',
                    clickable && selected && 'bg-accent/12 shadow-[inset_2px_0_0_var(--accent)]',
                    rowError?.(v) && 'bg-sev-critical/[0.08] hover:bg-sev-critical/[0.14]',
                    rowClassName?.(v),
                  )}
                >
                  {row.getVisibleCells().map((cell) => (
                    <td key={cell.id} className={cellCls}>
                      {flexRender(cell.column.columnDef.cell, cell.getContext())}
                    </td>
                  ))}
                </tr>
              )
            })
          )}
        </tbody>
      </table>
    </div>
  )
}
