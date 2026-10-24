import { useMemo } from 'react'
import { flexRender, getCoreRowModel, useReactTable } from '@tanstack/react-table'
import { columnWidthPercents } from '@/lib/tableLayout'
import type { CredentialRow } from './useCredentialQueries'
import type { CredentialTableMeta } from './credentialColumns'
import { buildCredentialColumns } from './credentialColumns'

interface CredentialTableProps {
  rows: CredentialRow[]
  loading: boolean
  hasFilter: boolean
  selectedRowId: string | null
  meta: CredentialTableMeta
  onRowClick: (row: CredentialRow) => void
}

const SKELETON_ROWS = 8

// 密集凭证表：真实 <table> 语义（对齐流量表的模式）——列宽由列工厂单点定义，
// table-fixed + colgroup 百分比宽度随容器等比缩放。行点击右侧滑出详情面板。
export function CredentialTable({
  rows,
  loading,
  hasFilter,
  selectedRowId,
  meta,
  onRowClick,
}: CredentialTableProps) {
  // 列定义稳定化 + 不设 getRowId：v8.21.3 下「每渲染新建列数组」或
  // 「getRowId + 整体替换 data」都会让核心行模型缓存陈旧——实测 data
  // 已更新为 1 行、tbody 仍渲染替换前的 2 行。选中态在父层按原始数据
  // 比对（不依赖 row.id），索引 id 对本表无损失。
  const columns = useMemo(() => buildCredentialColumns(), [])
  const table = useReactTable({
    data: rows,
    columns,
    getCoreRowModel: getCoreRowModel(),
    meta,
  })
  const leafColumns = table.getAllLeafColumns()
  const widthPercents = columnWidthPercents(leafColumns.map((col) => col.columnDef.size ?? 0))
  const colCount = leafColumns.length

  return (
    <div className="overflow-hidden rounded-2xl border border-border bg-surface shadow">
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
                <th
                  key={h.id}
                  className="px-4.5 py-2.5 text-left text-xs font-semibold uppercase tracking-wide text-muted opacity-70"
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
                  <td key={c} className="px-4.5 py-2">
                    <span className="block h-2.5 animate-pulse rounded bg-surface-2" />
                  </td>
                ))}
              </tr>
            ))
          ) : rows.length === 0 ? (
            <tr>
              <td colSpan={colCount} className="px-4.5 py-11 text-center text-sm text-muted">
                {hasFilter ? '当前筛选无匹配凭证' : '暂无凭证 · 点击右上角录入，或由扫描任务自动写入'}
              </td>
            </tr>
          ) : (
            table.getRowModel().rows.map((row) => {
              const v = row.original
              const selected = v.rowId === selectedRowId
              return (
                <tr
                  key={row.id}
                  role="button"
                  tabIndex={0}
                  aria-label={`查看用户名 ${v.identity.username} 的凭证`}
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
