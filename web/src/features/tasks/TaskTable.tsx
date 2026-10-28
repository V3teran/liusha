import { flexRender, getCoreRowModel, useReactTable } from '@tanstack/react-table'
import type { TaskSummary } from '@/api/types'
import { cn } from '@/lib/utils'
import { columnWidthPercents } from '@/lib/tableLayout'
import { buildTaskColumns } from './taskColumns'

interface TaskTableProps {
  rows: TaskSummary[]
  loading: boolean
  hasFilter: boolean
  offset: number // # 序号起点（(page-1)*size）
  selectedId: string | null
  onRowClick: (row: TaskSummary) => void
}

const SKELETON_ROWS = 8

// 任务表：镜像 TrafficTable 的 react-table 骨架（table-fixed + colgroup 百分比宽度），
// 行点击选中、键盘可达、选中态 inset 描边。
export function TaskTable({ rows, loading, hasFilter, offset, selectedId, onRowClick }: TaskTableProps) {
  const columns = buildTaskColumns(offset)
  const table = useReactTable({
    data: rows,
    columns,
    getCoreRowModel: getCoreRowModel(),
    getRowId: (row) => row.id,
  })
  const leafColumns = table.getAllLeafColumns()
  const widthPercents = columnWidthPercents(leafColumns.map((col) => col.columnDef.size ?? 0))
  const colCount = leafColumns.length

  return (
    <div className="overflow-x-auto rounded-2xl border border-border bg-surface shadow">
      {/* table-fixed + colgroup 百分比宽度随容器缩放；min-width 兜底防挤压——窄屏时容器出
          横向滚动条，列宽不再被压缩变形/值截断（全站表格统一约定）。 */}
      <table className="w-full min-w-[1180px] border-collapse table-fixed">
        <colgroup>
          {leafColumns.map((col, i) => (
            <col key={col.id} style={{ width: widthPercents[i] }} />
          ))}
        </colgroup>
        <thead>
          {table.getHeaderGroups().map((hg) => (
            <tr key={hg.id} className="border-b border-border">
              {hg.headers.map((h) => (
                <th
                  key={h.id}
                  className={cn(
                    'px-3 py-2.5 text-left text-xs font-semibold uppercase tracking-wide text-muted opacity-70',
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
                  <td key={c} className="px-3 py-2">
                    <span className="block h-2.5 animate-pulse rounded bg-surface-2" />
                  </td>
                ))}
              </tr>
            ))
          ) : rows.length === 0 ? (
            <tr>
              <td colSpan={colCount} className="px-4 py-11 text-center text-sm text-muted">
                {hasFilter ? '当前筛选无匹配任务' : '暂无任务——右上角「新建任务」下发第一批'}
              </td>
            </tr>
          ) : (
            table.getRowModel().rows.map((row) => {
              const v = row.original
              const selected = v.id === selectedId
              return (
                <tr
                  key={row.id}
                  role="button"
                  tabIndex={0}
                  aria-label={`查看任务详情 ${v.brief.slice(0, 24)}`}
                  aria-selected={selected}
                  onClick={() => onRowClick(v)}
                  onKeyDown={(e) => {
                    if (e.key === 'Enter' || e.key === ' ') {
                      e.preventDefault()
                      onRowClick(v)
                    }
                  }}
                  className="cursor-pointer border-b border-border text-sm transition-colors last:border-b-0 hover:bg-surface-2 aria-selected:!bg-accent/12 aria-selected:shadow-[inset_2px_0_0_var(--accent)]"
                >
                  {row.getVisibleCells().map((cell) => (
                    <td key={cell.id} className="overflow-hidden px-3 py-2">
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
