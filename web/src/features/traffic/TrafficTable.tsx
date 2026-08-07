import { flexRender, getCoreRowModel, useReactTable } from '@tanstack/react-table'
import type { TrafficSummary } from '@/api/types'
import { columnWidthPercents } from '@/lib/tableLayout'
import { buildTrafficColumns } from './trafficColumns'

interface TrafficTableProps {
  rows: TrafficSummary[]
  loading: boolean
  hasFilter: boolean
  showHost: boolean
  onRowClick: (row: TrafficSummary) => void
}

const SKELETON_ROWS = 8

// 密集流量表：真实 <table> 语义（对齐 LlmAuditTable / FindingsPage 的 react-table 模式），
// 列宽由 buildTrafficColumns 单点定义，table-fixed + colgroup 百分比宽度随容器等比缩放。
export function TrafficTable({ rows, loading, hasFilter, showHost, onRowClick }: TrafficTableProps) {
  const columns = buildTrafficColumns(showHost)
  const table = useReactTable({
    data: rows,
    columns,
    getCoreRowModel: getCoreRowModel(),
    getRowId: (row) => String(row.id),
  })
  const leafColumns = table.getAllLeafColumns()
  const widthPercents = columnWidthPercents(leafColumns.map((col) => col.columnDef.size ?? 0))
  const colCount = leafColumns.length

  return (
    <div className="overflow-hidden rounded-[18px] border border-border bg-surface shadow">
      <table className="w-full border-collapse table-fixed">
        <colgroup>
          {leafColumns.map((col, i) => (
            <col key={col.id} style={{ width: widthPercents[i] }} />
          ))}
        </colgroup>
        <thead>
          {table.getHeaderGroups().map((hg) => (
            <tr key={hg.id} className="border-b border-border">
              {hg.headers.map((h) => (
                <th key={h.id} className="px-4.5 py-2.5 text-left text-[11px] font-semibold uppercase tracking-wide text-muted opacity-70">
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
                  <td key={c} className="px-4.5 py-2">
                    <span className="block h-2.5 animate-pulse rounded bg-surface-2" />
                  </td>
                ))}
              </tr>
            ))
          ) : rows.length === 0 ? (
            <tr>
              <td colSpan={colCount} className="px-4.5 py-11 text-center text-[13.5px] text-muted">
                {hasFilter ? '当前筛选无匹配流量' : '暂无代理捕获流量'}
              </td>
            </tr>
          ) : (
            table.getRowModel().rows.map((row) => {
              const v = row.original
              return (
                <tr
                  key={row.id}
                  role="button"
                  tabIndex={0}
                  aria-label={`查看流量详情 ${v.method} ${v.path}`}
                  onClick={() => onRowClick(v)}
                  onKeyDown={(e) => {
                    if (e.key === 'Enter' || e.key === ' ') {
                      e.preventDefault()
                      onRowClick(v)
                    }
                  }}
                  className="cursor-pointer border-b border-border text-[13px] transition-colors last:border-b-0 hover:bg-surface-2 data-[error=true]:bg-sev-critical/[0.08] data-[error=true]:hover:bg-sev-critical/[0.14]"
                  data-error={v.status_code >= 500}
                >
                  {row.getVisibleCells().map((cell) => (
                    <td key={cell.id} className="overflow-hidden px-4.5 py-2">
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
