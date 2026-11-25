import { PageHeader } from '@/components/PageHeader'
import { useMemo } from 'react'
import { useSelectedParam } from '@/hooks/useSelectedParam'
import { ModelCallFilterBar } from '@/features/model-calls/ModelCallFilterBar'
import { ModelCallTable } from '@/features/model-calls/ModelCallTable'
import { LlmInvocationDrawer } from '@/features/model-calls/LlmInvocationDrawer'
import { useModelCallFilters } from '@/features/model-calls/useModelCallFilters'
import { useModelCallFacets, useModelCallSearch, useModelCallStat, useLlmInvocationDetail, useRuntimeSettings } from '@/features/model-calls/useModelCallQueries'
import type { LLMInvocationSummary } from '@/api/types'
import { cacheRatePct, compactNumber, humanTokens } from '@/lib/format'
import { PaginationToolbar } from '@/components/ui/PaginationToolbar'
import { StatChip } from '@/components/StatChip'
import { catColor } from '@/lib/categoricalColor'
import { ErrorState } from '@/components/ui/ErrorState'

export function ModelCallsPage() {
  // 打开的调用写在 URL（?selected=）：刷新、跳到任务 / 对话再后退都还原抽屉
  const [selected, select] = useSelectedParam()
  const selectedId = selected && Number.isFinite(Number(selected)) ? Number(selected) : null

  // 搜索词与筛选/分页同住 URL state（?q=）：刷新、分享链接都能还原完整查询上下文。
  const { query, filters, page, size, setQuery, setFilters, setPage, setSize, resetFilters } = useModelCallFilters()

  // 全局搜索：空搜索时返回所有数据（按时间倒序）
  const searchQueryResult = useModelCallSearch({
    search: query,
    page,
    size,
    ...filters,
  })

  // 统计是服务端对整个筛选结果集的合计（与翻页无关）——此前用当页行前端聚合，
  // 50 条/页时会显示「调用 50」而分页器写「共 197 条」，同屏自相矛盾。
  const statQuery = useModelCallStat({ search: query, ...filters })
  const stats = statQuery.data

  // 单步 LLM 看门狗（runtime 配置现读）：审计页「超时」判定与 runner 执行同源——
  // 此前前端硬编码 300s，后端调看门狗会让超时判定全错。
  const runtimeQuery = useRuntimeSettings()
  const llmTimeoutMs = runtimeQuery.data ? runtimeQuery.data.step_llm_timeout_seconds * 1000 : undefined

  const detailQuery = useLlmInvocationDetail(selectedId)

  const openDetail = (row: LLMInvocationSummary) => select(String(row.id))

  const rows = searchQueryResult.data?.items ?? []
  const total = searchQueryResult.data?.total ?? 0
  const totalPages = Math.ceil(total / size)
  const hasFilter = !!(filters.role || filters.model || filters.onlyErr || filters.start || filters.end)

  const showProvider = useMemo(() => {
    const providers = new Set(rows.map((r) => r.provider))
    return providers.size > 1
  }, [rows])

  // 角色 / 模型选项来自服务端（整个筛选结果集），不从当前页的行推导
  const facetsQuery = useModelCallFacets({ search: query, ...filters })
  const roles = facetsQuery.data?.roles ?? []
  const models = facetsQuery.data?.models ?? []

  return (
    <div className="flex h-full min-h-0 flex-col">
      <PageHeader title="模型调用" />

      {/* 指标区：整个筛选结果集的服务端合计。预留一行高度，数据到达前后布局不跳动。 */}
      <div className="flex min-h-[52px] flex-shrink-0 flex-wrap items-center gap-2 border-b border-border-subtle px-6 py-3">
        {stats && (
          <>
            <StatChip color="var(--accent)" label="调用" value={humanTokens(stats.calls)} />
            <StatChip color={catColor('sky')} label="输入" value={compactNumber(stats.in_tokens)} title={humanTokens(stats.in_tokens)} />
            <StatChip color={catColor('emerald')} label="输出" value={compactNumber(stats.out_tokens)} title={humanTokens(stats.out_tokens)} />
            <StatChip color={catColor('violet')} label="缓存" value={compactNumber(stats.cached_tokens)} title={humanTokens(stats.cached_tokens)} />
            <StatChip
              color={catColor('orange')}
              label="缓存率"
              value={`${cacheRatePct(stats.in_tokens, stats.cached_tokens).toFixed(1)}%`}
              title={`${humanTokens(stats.cached_tokens)} / ${humanTokens(stats.in_tokens + stats.cached_tokens)}`}
            />
          </>
        )}
      </div>

      <ModelCallFilterBar
        filters={filters}
        roles={roles}
        models={models}
        search={query}
        onSearchChange={setQuery}
        onChange={setFilters}
        onReset={resetFilters}
        onRefresh={() => {
          // 列表、统计、分面是同一筛选的三个侧面：一起重取，否则同一屏里的数字会来自不同的时刻
          void searchQueryResult.refetch()
          void statQuery.refetch()
          void facetsQuery.refetch()
        }}
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

      <div className="min-h-0 flex-1 overflow-auto px-6 py-4">
        {searchQueryResult.isError ? (
          <ErrorState
            message={searchQueryResult.error instanceof Error ? searchQueryResult.error.message : undefined}
            onRetry={() => void searchQueryResult.refetch()}
          />
        ) : (
          <ModelCallTable
            rows={rows}
            loading={searchQueryResult.isPending}
            hasFilter={hasFilter || !!query}
            showProvider={showProvider}
            llmTimeoutMs={llmTimeoutMs}
            meta={{ startIndex: (page - 1) * size }}
            onRowClick={openDetail}
          />
        )}
      </div>

      <LlmInvocationDrawer
        open={selectedId != null}
        detail={detailQuery.data ?? null}
        loading={detailQuery.isPending && selectedId != null}
        error={detailQuery.isError ? (detailQuery.error instanceof Error ? detailQuery.error.message : '加载详情失败') : ''}
        onRetry={() => void detailQuery.refetch()}
        llmTimeoutMs={llmTimeoutMs}
        onOpenChange={(open) => {
          if (!open) select('')
        }}
      />
    </div>
  )
}
