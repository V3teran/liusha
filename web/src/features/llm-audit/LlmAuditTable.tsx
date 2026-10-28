import { flexRender, getCoreRowModel, useReactTable } from '@tanstack/react-table'
import type { LLMInvocationSummary } from '@/api/types'
import { columnWidthPercents } from '@/lib/tableLayout'
import { buildLlmAuditColumns } from './llmAuditColumns'

interface LlmAuditTableProps {
  rows: LLMInvocationSummary[]
  loading: boolean
  hasFilter: boolean
  showProvider: boolean
  llmTimeoutMs?: number // 单步 LLM 看门狗（runtime 配置现读；超时判定同源）
  onRowClick: (row: LLMInvocationSummary) => void
}

// 密集审计表：真实 <table> 语义（对齐 FindingsPage 的 react-table 模式），列宽由
// buildLlmAuditColumns 单点定义——列头/骨架屏/数据行/空态 colSpan 全部以列定义长度为准，
// 增删列只改 llmAuditColumns 一处。
export function LlmAuditTable({ rows, loading, hasFilter, showProvider, llmTimeoutMs, onRowClick }: LlmAuditTableProps) {
  const columns = buildLlmAuditColumns(showProvider, llmTimeoutMs)
  const table = useReactTable({
    data: rows,
    columns,
    getCoreRowModel: getCoreRowModel(),
    getRowId: (row) => String(row.id),
  })
  const leafColumns = table.getAllLeafColumns()
  const colCount = leafColumns.length
  const widthPercents = columnWidthPercents(leafColumns.map((col) => col.columnDef.size ?? 0))

  return (
    <div className="overflow-auto rounded-2xl border border-border bg-surface shadow">
      {/* table-fixed + colgroup 百分比宽度：列宽由 columnDef.size（相对权重）换算成百分比，
          随容器宽度等比缩放——缩小浏览器/侧栏展开时所有列同步变窄，而不是某列固定 px 不变、
          把其它列挤到断行（截图问题的根因）。用 columnDef.size 而非 col.getSize()：
          getSize() 会套 minSize=20 下限钳位，size 较小的列会被钳位失真，破坏比例关系。 */}
      <table className="w-full border-collapse table-fixed">
        <colgroup>
          {leafColumns.map((col, i) => (
            <col key={col.id} style={{ width: widthPercents[i] }} />
          ))}
        </colgroup>
        <thead className="sticky top-0 z-10 bg-surface">
          {table.getHeaderGroups().map((hg) => (
            <tr key={hg.id} className="border-b border-border">
              {hg.headers.map((h) => (
                <th key={h.id} className="px-4 py-2.5 text-left text-xs font-semibold uppercase tracking-wide text-muted opacity-70">
                  {flexRender(h.column.columnDef.header, h.getContext())}
                </th>
              ))}
            </tr>
          ))}
        </thead>
        <tbody>
          {loading && rows.length === 0 ? (
            Array.from({ length: 8 }).map((_, i) => (
              <tr key={i} className="border-b border-border last:border-b-0">
                {Array.from({ length: colCount }).map((_, c) => (
                  <td key={c} className="px-4 py-2">
                    <span className="block h-2.5 animate-pulse rounded bg-surface-2" />
                  </td>
                ))}
              </tr>
            ))
          ) : rows.length === 0 ? (
            <tr>
              <td colSpan={colCount} className="px-4 py-11 text-center text-sm text-muted">
                {hasFilter ? '当前筛选无匹配调用' : '暂无 LLM 调用记录'}
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
                  aria-label={`查看调用详情 ${v.request_id}`}
                  onClick={() => onRowClick(v)}
                  onKeyDown={(e) => {
                    if (e.key === 'Enter' || e.key === ' ') {
                      e.preventDefault()
                      onRowClick(v)
                    }
                  }}
                  className="cursor-pointer border-b border-border text-sm transition-colors last:border-b-0 hover:bg-surface-2 data-[error=true]:bg-sev-critical/[0.08] data-[error=true]:hover:bg-sev-critical/[0.14]"
                  data-error={!!v.error_message}
                >
                  {row.getVisibleCells().map((cell) => (
                    <td key={cell.id} className="overflow-hidden px-4 py-2">
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
