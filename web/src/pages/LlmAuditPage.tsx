import { useMemo, useState } from 'react'
import { LlmAuditFilterBar } from '@/features/llm-audit/LlmAuditFilterBar'
import { LlmAuditTable } from '@/features/llm-audit/LlmAuditTable'
import { LlmInvocationDrawer } from '@/features/llm-audit/LlmInvocationDrawer'
import { useLlmAuditFilters } from '@/features/llm-audit/useLlmAuditFilters'
import { useLlmAuditSearch, useLlmAuditStat, useLlmInvocationDetail, useRuntimeSettings } from '@/features/llm-audit/useLlmAuditQueries'
import type { LLMInvocationSummary } from '@/api/types'
import { cacheRatePct, compactNumber, humanTokens } from '@/lib/format'
import { PaginationToolbar } from '@/components/ui/PaginationToolbar'
import { StatChip } from '@/components/StatChip'

export function LlmAuditPage() {
  const [selectedId, setSelectedId] = useState<number | null>(null)
  const [drawerOpen, setDrawerOpen] = useState(false)

  // 搜索词与筛选/分页同住 URL state（?q=）：刷新、分享链接都能还原完整查询上下文。
  const { query, filters, page, size, setQuery, setFilters, setPage, setSize, resetFilters } = useLlmAuditFilters()

  // 全局搜索：空搜索时返回所有数据（按时间倒序）
  const searchQueryResult = useLlmAuditSearch({
    search: query,
    page,
    size,
    ...filters,
  })

  // 统计是服务端对整个筛选结果集的合计（与翻页无关）——此前用当页行前端聚合，
  // 50 条/页时会显示「调用 50」而分页器写「共 197 条」，同屏自相矛盾。
  const statQuery = useLlmAuditStat({ search: query, ...filters })
  const stats = statQuery.data

  // 单步 LLM 看门狗（runtime 配置现读）：审计页「超时」判定与 runner 执行同源——
  // 此前前端硬编码 300s，后端调看门狗会让超时判定全错。
  const runtimeQuery = useRuntimeSettings()
  const llmTimeoutMs = runtimeQuery.data ? runtimeQuery.data.step_llm_timeout_seconds * 1000 : undefined

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

  return (
    <div className="flex h-full min-h-0 flex-col">
      <header className="flex items-center justify-between border-b border-border bg-surface px-6 py-4">
        <div>
          <h1 className="text-base font-semibold text-text">LLM 审计</h1>
          <p className="mt-0.5 text-sm text-text-secondary">全局搜索和监控所有 LLM 调用</p>
        </div>
      </header>

      <div className="flex flex-wrap items-center gap-4 border-b border-border bg-surface/50 px-6 py-4">
        {stats && (
          <div className="flex flex-wrap items-center gap-2">
            <StatChip color="var(--accent)" label="调用" value={humanTokens(stats.calls)} />
            <StatChip color="#38bdf8" label="输入" value={compactNumber(stats.in_tokens)} title={humanTokens(stats.in_tokens)} />
            <StatChip color="#34d399" label="输出" value={compactNumber(stats.out_tokens)} title={humanTokens(stats.out_tokens)} />
            <StatChip color="#a78bfa" label="缓存" value={compactNumber(stats.cached_tokens)} title={humanTokens(stats.cached_tokens)} />
            <StatChip
              color="#f59e0b"
              label="缓存率"
              value={`${cacheRatePct(stats.in_tokens, stats.cached_tokens).toFixed(1)}%`}
              title={`${humanTokens(stats.cached_tokens)} / ${humanTokens(stats.in_tokens + stats.cached_tokens)}`}
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
              search={query}
              onSearchChange={setQuery}
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
                hasFilter={hasFilter || !!query}
                showProvider={showProvider}
                llmTimeoutMs={llmTimeoutMs}
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
        llmTimeoutMs={llmTimeoutMs}
        onOpenChange={setDrawerOpen}
      />
    </div>
  )
}
