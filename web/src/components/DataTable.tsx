import { flexRender, getCoreRowModel, useReactTable, type ColumnDef, type TableMeta } from '@tanstack/react-table'
import { cn } from '@/lib/utils'
import { columnWidthPercents } from '@/lib/tableLayout'

const SKELETON_ROWS = 8

interface DataTableProps<T> {
  rows: T[]
  columns: ColumnDef<T, any>[]
  /** 表格最小宽度（px）——窄屏时容器出横向滚动条，列不再被压缩变形 */
  minWidth?: number
  /** 单元格水平内边距（px-3 / px-4，表头同步） */
  cellPadding?: 'px-3' | 'px-4'
  loading: boolean
  hasFilter?: boolean
  /** 空态文案（hasFilter 区分「无匹配」与「暂无」两态） */
  emptyText: string
  /** 行数据错误标记（如 HTTP 5xx 流量行整行红染） */
  rowError?: (row: T) => boolean
  /** 选中态（aria-selected + inset 描边；不传则行无选中语义） */
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
}

/**
 * react-table 通用外壳：table-fixed + colgroup 百分比列宽（columnDef.size 单点
 * 定义，随容器等比缩放）+ min-width 兜底（窄屏出横向滚动条而非挤压变形，全站
 * 统一约定）+ 骨架屏/空态/行点击与键盘可达/选中态描边。
 * 之前此骨架在 Task/Traffic/LlmAudit/Findings 四张表各复制一份（~80 行/份）。
 */
export function DataTable<T>({ rows, columns, minWidth = 1000, cellPadding = 'px-4', loading, hasFilter, emptyText, rowError, selectedId, getRowId, onRowClick, rowAriaLabel, meta, stickyHeader }: DataTableProps<T>) {
  const table = useReactTable({
    data: rows,
    columns,
    getCoreRowModel: getCoreRowModel(),
    getRowId: getRowId ?? ((row: T) => String((row as { id?: unknown }).id ?? '')),
    meta,
  })
  const leafColumns = table.getAllLeafColumns()
  const widthPercents = columnWidthPercents(leafColumns.map((col) => col.columnDef.size ?? 0))
  const colCount = leafColumns.length
  const cellCls = `overflow-hidden ${cellPadding} py-2`

  return (
    <div className="overflow-x-auto rounded-2xl border border-border bg-surface shadow">
      <table className="w-full border-collapse table-fixed" style={{ minWidth }}>
        <colgroup>
          {leafColumns.map((col, i) => (
            <col key={col.id} style={{ width: widthPercents[i] }} />
          ))}
        </colgroup>
        <thead className={cn(stickyHeader && 'sticky top-0 z-10 bg-surface')}>
          {table.getHeaderGroups().map((hg) => (
            <tr key={hg.id} className="border-b border-border">
              {hg.headers.map((h) => (
                <th
                  key={h.id}
                  className={cn(
                    `${cellPadding} py-2.5 text-left text-xs font-semibold uppercase tracking-wide text-muted opacity-70`,
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
              <td colSpan={colCount} className="px-4 py-11 text-center text-sm text-muted">
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
                  role={clickable ? 'button' : undefined}
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
                  data-error={rowError?.(v)}
                  className={cn(
                    'border-b border-border text-sm transition-colors last:border-b-0',
                    clickable && 'cursor-pointer hover:bg-surface-2',
                    clickable && selected && 'bg-accent/12 shadow-[inset_2px_0_0_var(--accent)]',
                    rowError?.(v) && 'bg-sev-critical/[0.08] hover:bg-sev-critical/[0.14]',
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
