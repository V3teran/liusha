import { DataTable } from '@/components/DataTable'
import { buildAuditColumns, type AuditTableMeta } from './auditColumns'
import type { AuditEvent } from '@/api/audit'

// 审计表：只读流水，无行点击钻取——metadata 就地在详情列展示，无需抽屉。
export function AuditTable({
  rows,
  loading,
  hasFilter,
  meta,
}: {
  rows: AuditEvent[]
  loading: boolean
  hasFilter: boolean
  meta: AuditTableMeta
}) {
  return (
    <DataTable
      ariaLabel="审计事件列表"
      rows={rows}
      columns={buildAuditColumns() as never}
      stickyHeader
      loading={loading}
      hasFilter={hasFilter}
      emptyNoun="审计事件"
      emptyHint="登录、任务创建与中止、凭证写删等敏感操作都会记录在这里。"
      meta={meta as never}
      getRowId={(row) => String(row.id)}
    />
  )
}
