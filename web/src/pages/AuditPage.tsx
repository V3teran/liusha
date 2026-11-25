import { PageHeader } from '@/components/PageHeader'
import { PaginationToolbar } from '@/components/ui/PaginationToolbar'
import { ErrorState } from '@/components/ui/ErrorState'
import { AuditFilterBar } from '@/features/audit/AuditFilterBar'
import { AuditTable } from '@/features/audit/AuditTable'
import { useAuditFilters } from '@/features/audit/useAuditFilters'
import { useAuditList } from '@/features/audit/useAuditQueries'

/**
 * 「操作」页：系统级敏感操作的审计链（只读）——谁在何时做了什么。
 * 数据流与 模型调用页同构：筛选/分页进 URL、服务端筛选与分页、动作下拉来自后端词表。
 */
export function AuditPage() {
  const { query, filters, page, size, setQuery, setFilters, setPage, setSize, resetFilters } = useAuditFilters()

  const listQuery = useAuditList({ query, page, size, ...filters })

  const rows = listQuery.data?.items ?? []
  const total = listQuery.data?.total ?? 0
  const actions = listQuery.data?.actions ?? []
  const totalPages = Math.ceil(total / size)
  const hasFilter = !!(filters.action || filters.start || filters.end || query)

  return (
    <div className="flex h-full min-h-0 flex-col">
      <PageHeader title="操作" />

      <AuditFilterBar
        filters={filters}
        actions={actions}
        search={query}
        onSearchChange={setQuery}
        onChange={setFilters}
        onReset={resetFilters}
        onRefresh={() => void listQuery.refetch()}
        trailing={
          <PaginationToolbar
            page={page}
            pageSize={size}
            total={total}
            totalPages={totalPages}
            onPageChange={setPage}
            onPageSizeChange={setSize}
            loading={listQuery.isPending}
          />
        }
      />

      <div className="min-h-0 flex-1 overflow-auto px-6 py-4">
        {listQuery.isError ? (
          <ErrorState
            message={listQuery.error instanceof Error ? listQuery.error.message : undefined}
            onRetry={() => void listQuery.refetch()}
          />
        ) : (
          <AuditTable
            rows={rows}
            loading={listQuery.isPending}
            hasFilter={hasFilter}
            meta={{ startIndex: (page - 1) * size }}
          />
        )}
      </div>
    </div>
  )
}
