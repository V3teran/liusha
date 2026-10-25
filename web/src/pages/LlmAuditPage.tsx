import { useMemo, useState } from 'react'
import { LlmAuditFilterBar } from '@/features/llm-audit/LlmAuditFilterBar'
import { LlmAuditTable } from '@/features/llm-audit/LlmAuditTable'
import { LlmAuditSearchBar } from '@/features/llm-audit/LlmAuditSearchBar'
import { LlmInvocationDrawer } from '@/features/llm-audit/LlmInvocationDrawer'
import { useLlmAuditFilters } from '@/features/llm-audit/useLlmAuditFilters'
import { useLlmAuditSearch, useLlmInvocationDetail } from '@/features/llm-audit/useLlmAuditQueries'
import type { LLMInvocationSummary } from '@/api/types'
import { humanTokens, compactNumber } from '@/lib/format'
import { PaginationToolbar } from '@/components/ui/PaginationToolbar'
import { StatChip } from '@/components/StatChip'

export function LlmAuditPage() {
  const [searchQuery, setSearchQuery] = useState('')
  const [selectedId, setSelectedId] = useState<number | null>(null)
  const [drawerOpen, setDrawerOpen] = useState(false)

  const { filters, page, size, setFilters, setPage, setSize, resetFilters } = useLlmAuditFilters()

  // 全局搜索：空搜索时返回所有数据（按时间倒序）
  const searchQueryResult = useLlmAuditSearch({
    search: searchQuery,
    page,
    size,
    ...filters,
  })

  const detailQuery = useLlmInvocationDetail(selectedId)

  const openDetail = (row: LLMInvocationSummary) => {
    setSelectedId(row.id)
    setDrawerOpen(true)
  }

  const rows = searchQueryResult.data?.items ?? []
  const total = searchQueryResult.data?.total ?? 0
  const totalPages = Math.ceil(total / size)
  const hasFilter = !!(filters.role || filters.model || filters.onlyErr || filters.start || filters.end)

  const showProvider = useMemo(() => {
    const providers = new Set(rows.map((r) => r.provider))
    return providers.size > 1
  }, [rows])

  const { roles, models } = useMemo(() => {
    const roleSet = new Set(rows.map((r) => r.role))
    const modelSet = new Set(rows.map((r) => r.model))
    return {
      roles: Array.from(roleSet).sort(),
      models: Array.from(modelSet).sort(),
    }
  }, [rows])

  const stats = useMemo(() => {
    if (rows.length === 0) return null
    const in_tokens = rows.reduce((sum, r) => sum + r.in_tokens, 0)
    const cached_tokens = rows.reduce((sum, r) => sum + r.cached_tokens, 0)
    const cache_rate = in_tokens > 0 ? (cached_tokens / in_tokens) * 100 : 0
    return {
      calls: rows.length,
      in_tokens,
      out_tokens: rows.reduce((sum, r) => sum + r.out_tokens, 0),
      cached_tokens,
      cache_rate,
    }
  }, [rows])

  return (
    <div className="flex h-full min-h-0 flex-col">
      <header className="flex items-center justify-between border-b border-border bg-surface px-6 py-4">
        <div>
          <h1 className="text-base font-semibold text-text">LLM 审计</h1>
          <p className="mt-0.5 text-sm text-text-secondary">全局搜索和监控所有 LLM 调用</p>
        </div>
      </header>

      <div className="flex flex-wrap items-center gap-4 border-b border-border bg-surface/50 px-6 py-4">
        <LlmAuditSearchBar value={searchQuery} onChange={setSearchQuery} />
        {stats && (
          <div className="flex flex-wrap items-center gap-2">
            <StatChip color="var(--accent)" label="调用" value={humanTokens(stats.calls)} />
            <StatChip color="#38bdf8" label="输入" value={compactNumber(stats.in_tokens)} title={humanTokens(stats.in_tokens)} />
            <StatChip color="#34d399" label="输出" value={compactNumber(stats.out_tokens)} title={humanTokens(stats.out_tokens)} />
            <StatChip color="#a78bfa" label="缓存" value={compactNumber(stats.cached_tokens)} title={humanTokens(stats.cached_tokens)} />
            <StatChip
              color="#f59e0b"
              label="缓存率"
              value={`${stats.cache_rate.toFixed(1)}%`}
              title={`${humanTokens(stats.cached_tokens)} / ${humanTokens(stats.in_tokens)}`}
            />
          </div>
        )}
      </div>

      <div className="relative flex min-h-0 flex-1 flex-col overflow-hidden bg-surface">
        {searchQueryResult.isError ? (
          <div className="flex h-full items-center justify-center">
            <p className="text-sm text-sev-critical">
              加载失败：{searchQueryResult.error instanceof Error ? searchQueryResult.error.message : '未知错误'}
            </p>
          </div>
        ) : (
          <>
            <LlmAuditFilterBar
              filters={filters}
              roles={roles}
              models={models}
              onChange={setFilters}
              onReset={resetFilters}
              trailing={
                <PaginationToolbar
                  page={page}
                  pageSize={size}
                  total={total}
                  totalPages={totalPages}
                  onPageChange={setPage}
                  onPageSizeChange={setSize}
                  loading={searchQueryResult.isPending}
                />
              }
            />

            <div className="min-h-0 flex-1 overflow-auto px-6 pb-6">
              <LlmAuditTable
                rows={rows}
                loading={searchQueryResult.isPending}
                hasFilter={hasFilter || !!searchQuery}
                showProvider={showProvider}
                onRowClick={openDetail}
              />
            </div>
          </>
        )}
      </div>

      <LlmInvocationDrawer
        open={drawerOpen}
        detail={detailQuery.data ?? null}
        loading={detailQuery.isPending && selectedId != null}
        error={detailQuery.isError ? (detailQuery.error instanceof Error ? detailQuery.error.message : '加载详情失败') : ''}
        onOpenChange={setDrawerOpen}
      />
    </div>
  )
}
