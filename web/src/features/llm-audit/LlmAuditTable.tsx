import type { LLMInvocationSummary } from '@/api/types'
import { buildLlmAuditColumns, type LlmAuditTableMeta } from './llmAuditColumns'
import { DataTable } from '@/components/DataTable'

interface LlmAuditTableProps {
  rows: LLMInvocationSummary[]
  loading: boolean
  hasFilter: boolean
  showProvider: boolean
  llmTimeoutMs?: number
  meta: LlmAuditTableMeta
  onRowClick: (row: LLMInvocationSummary) => void
}

// 密集审计表：列定义在 llmAuditColumns 单点声明，表外壳走 DataTable 通用组件。
export function LlmAuditTable({ rows, loading, hasFilter, showProvider, llmTimeoutMs, meta, onRowClick }: LlmAuditTableProps) {
  return (
    <DataTable
      rows={rows}
      columns={buildLlmAuditColumns(showProvider, llmTimeoutMs) as never}
      minWidth={970}
      stickyHeader
      loading={loading}
      hasFilter={hasFilter}
      emptyText=" LLM 调用记录"
      meta={meta as never}
      getRowId={(row) => String(row.id)}
      onRowClick={onRowClick}
      rowError={(row) => !!row.error_message}
      rowAriaLabel={(row) => `查看调用详情 ${row.request_id}`}
    />
  )
}
