import type { TrafficSummary } from '@/api/types'
import { buildTrafficColumns } from './trafficColumns'
import { DataTable } from '@/components/DataTable'
import { PROXY_SETUP_HINT } from '@/lib/source'

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
      ariaLabel="流量列表"
      rows={rows}
      columns={buildTrafficColumns(showHost) as never}
      loading={loading}
      hasFilter={hasFilter}
      emptyNoun="代理流量"
      emptyHint={PROXY_SETUP_HINT}
      selectedId={selectedId}
      getRowId={(row) => String(row.id)}
      onRowClick={onRowClick}
      rowAriaLabel={(row) => `查看流量详情 ${row.method} ${row.path}`}
      rowError={(row) => row.status_code >= 500}
    />
  )
}
