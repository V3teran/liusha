import type { TrafficSummary } from '@/api/types'
import { buildTrafficColumns } from './trafficColumns'
import { DataTable } from '@/components/DataTable'

interface TrafficTableProps {
  rows: TrafficSummary[]
  loading: boolean
  hasFilter: boolean
  showHost: boolean
  selectedId: number | null
  onRowClick: (row: TrafficSummary) => void
}

// 密集流量表：列定义在 trafficColumns 单点声明，表外壳走 DataTable 通用组件。
export function TrafficTable({ rows, loading, hasFilter, showHost, selectedId, onRowClick }: TrafficTableProps) {
  return (
    <DataTable
      rows={rows}
      columns={buildTrafficColumns(showHost) as never}
      minWidth={1080}
      loading={loading}
      hasFilter={hasFilter}
      emptyText="代理捕获流量"
      selectedId={selectedId}
      getRowId={(row) => String(row.id)}
      onRowClick={onRowClick}
      rowAriaLabel={(row) => `查看流量详情 ${row.method} ${row.path}`}
      rowError={(row) => row.status_code >= 500}
    />
  )
}
