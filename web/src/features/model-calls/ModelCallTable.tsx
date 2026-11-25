import type { LLMInvocationSummary } from '@/api/types'
import { buildModelCallColumns, type ModelCallTableMeta } from './modelCallColumns'
import { DataTable } from '@/components/DataTable'

interface ModelCallTableProps {
  rows: LLMInvocationSummary[]
  loading: boolean
  hasFilter: boolean
  showProvider: boolean
  llmTimeoutMs?: number
  meta: ModelCallTableMeta
  onRowClick: (row: LLMInvocationSummary) => void
}

// 密集审计表：列定义在 modelCallColumns 单点声明，表外壳走 DataTable 通用组件。
export function ModelCallTable({ rows, loading, hasFilter, showProvider, llmTimeoutMs, meta, onRowClick }: ModelCallTableProps) {
  return (
    <DataTable
      ariaLabel="LLM 调用列表"
      rows={rows}
      columns={buildModelCallColumns(showProvider, llmTimeoutMs) as never}
      stickyHeader
      loading={loading}
      hasFilter={hasFilter}
      emptyNoun="LLM 调用记录"
      emptyHint="任务运行和对话中的每次 LLM 调用都会记录在这里。"
      meta={meta as never}
      getRowId={(row) => String(row.id)}
      onRowClick={onRowClick}
      rowError={(row) => !!row.error_message}
      rowAriaLabel={(row) => `查看调用详情 #${row.id}`}
    />
  )
}
