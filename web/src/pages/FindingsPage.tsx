import { SearchInput } from '@/components/SearchInput'
import { FilterBar } from '@/components/FilterBar'
import { Select } from '@/components/ui/Select'
import { PageHeader } from '@/components/PageHeader'
import { useEffect, useMemo, useState } from 'react'
import { updateFindingTriage, getFindingStat } from '@/api/findings'
import type { FindingRow } from '@/api/types'
import { SEVERITIES, severityColorOf, severityLabel } from '@/lib/severity'
import { DataTable } from '@/components/DataTable'
import { FINDING_STATUS_OPTIONS } from '@/features/finding/findingStatus'
import { findingColumns } from '@/features/finding/findingColumns'
import { PaginationToolbar } from '@/components/ui/PaginationToolbar'
import { cn } from '@/lib/utils'
import { FindingDrawer } from '@/features/finding/FindingDrawer'
import { useFindingFilters } from '@/features/finding/useFindingFilters'
import { useQueryClient, keepPreviousData, useQuery } from '@tanstack/react-query'
import { useFindingList } from '@/features/finding/useFindingQueries'
import { SOURCE_OPTIONS } from '@/lib/source'
import { useTaskDetail } from '@/features/tasks/useTaskQueries'
import { shortDateTime } from '@/lib/format'
import { EntityPicker, type PickerResult } from '@/components/ui/EntityPicker'
import { getFindingTaskFacets } from '@/api/findings'
import type { FindingFilters } from '@/api/types'
import { useDebouncedSearch } from '@/hooks/useDebouncedSearch'

/**
 * 「任务」选择框的候选：有漏洞的任务，由服务端按目标关键词检索、限量（任务会持续增长，不一次
 * 列全）。结构化筛选（来源、状态、严重度、主机）照常收窄候选；同一目标常被反复下发，次要
 * 信息里的创建时间用来区分。
 */
function fetchTaskOptions(filters: FindingFilters) {
  return async (q: string): Promise<PickerResult> => {
    const { tasks, has_more } = await getFindingTaskFacets(filters, q)
    return {
      options: tasks.map((t) => ({
        value: t.id,
        label: t.goal.replace(/\s+/g, ' '),
        detail: `${shortDateTime(t.created_at)} 创建 · ${t.count} 个漏洞`,
      })),
      hasMore: has_more,
    }
  }
}

/**
 * FindingsPage - 漏洞管理页面（平铺台账）
 */
export function FindingsPage() {
  const { filters, page, size, setFilters, setPage, setSize, resetFilters } = useFindingFilters()
  const queryClient = useQueryClient()
  // 选中任务的显示名（选中项可能不在当前候选里，如经链接带入）
  const selectedTask = useTaskDetail(filters.task || null)
  // 任务候选只随结构化筛选变化：关键词搜索针对漏洞本身
  const facetFilters: FindingFilters = {
    source: filters.source,
    status: filters.status,
    severity: filters.severity,
    host: filters.host,
  }

  // 关键词搜索在服务端执行（覆盖全部页，与分页、统计同一口径），停顿后写入 URL
  const [query, setQuery] = useState(filters.search ?? '')
  const debouncedQuery = useDebouncedSearch(query.trim())
  useEffect(() => {
    if (debouncedQuery !== (filters.search ?? '')) setFilters({ ...filters, search: debouncedQuery })
    // eslint-disable-next-line react-hooks/exhaustive-deps -- 只在防抖值变化时写回
  }, [debouncedQuery])
  // URL 被外部改写（清空筛选、前进后退）时同步输入框；与输入框去空白后一致时不动（保留正在输入的空格）
  useEffect(() => {
    setQuery((q) => (q.trim() === (filters.search ?? '') ? q : (filters.search ?? '')))
  }, [filters.search])

  const listQuery = useFindingList(filters, page, size)

  const rows = useMemo(() => listQuery.data?.findings ?? [], [listQuery.data])
  const total = listQuery.data?.total ?? 0

  // 头部统计走服务端聚合（与列表同筛选口径、与翻页无关）——此前按当前页行
  // 自算，与「共 N 条」的全量口径同屏自相矛盾（LLM 审计页同款问题已修过）。
  const statQuery = useQuery({
    queryKey: ['findings', 'stat', filters],
    queryFn: () => getFindingStat(filters),
    placeholderData: keepPreviousData,
  })
  const stat = statQuery.data
  const totalPages = Math.max(1, Math.ceil(total / size))

  const [savingId, setSavingId] = useState('')
  const [drawerOpen, setDrawerOpen] = useState(false)
  const [drawerFinding, setDrawerFinding] = useState<FindingRow | null>(null)


  const sevCounts = useMemo(
    () =>
      SEVERITIES.map((s) => ({
        sev: s,
        n: stat?.by_severity?.[s.toLowerCase()] ?? 0,
      })),
    [stat],
  )

  const toggleSevFilter = (sev: string) => setFilters({ ...filters, severity: filters.severity === sev ? '' : sev })

  const invalidateList = () => void queryClient.invalidateQueries({ queryKey: ['findings', 'list'] })

  const openDrawer = (f: FindingRow) => {
    setDrawerFinding(f)
    setDrawerOpen(true)
  }

  const saveTriage = async (payload: { id: string; status: string; severity: string; note: string }) => {
    setSavingId(payload.id)
    try {
      const updated = await updateFindingTriage(payload.id, payload.status, payload.severity, payload.note)
      setDrawerFinding((prev) => (prev?.id === updated.id ? { ...prev, ...updated } : prev))
      invalidateList()
    } catch {
      window.alert('处置保存失败，请重试')
    } finally {
      setSavingId('')
    }
  }

  const hasFilter = !!(filters.search || filters.task || filters.host || filters.severity || filters.status || filters.source)

  const listError = listQuery.isError
    ? (listQuery.error instanceof Error ? listQuery.error.message : '加载失败')
    : ''

  const resetAll = () => {
    resetFilters()
    setQuery('')
  }

  return (
    <div className="flex h-full min-h-0 flex-col">
      <PageHeader title="漏洞" subtitle="每条附复现证据链与配方；可处置：确认 / 已修复 / 误报 / 接受" />

      {/* 指标卡：台账总量 + 处置进度 + 严重度分布（分布条与图例可点击按等级筛选） */}
      <section aria-label="漏洞概况" className="flex-shrink-0 px-6 pt-4">
        <div className="flex flex-wrap items-center gap-x-6 gap-y-3 rounded-xl border border-border bg-surface px-6 py-3 shadow-sm">
          <div className="text-2xl font-bold tabular-nums text-text" aria-label={`共 ${total} 个漏洞`}>
            {total}
          </div>

          <div className="h-10 w-px bg-border" aria-hidden />

          <dl className="flex flex-1 items-center gap-4 whitespace-nowrap text-xs">
            <div className="flex items-baseline gap-2">
              <dt className="text-text-tertiary">待处理</dt>
              <dd className="font-mono text-lg font-semibold tabular-nums text-text">{stat?.by_status?.open ?? 0}</dd>
            </div>
            <div className="flex items-baseline gap-2">
              <dt className="text-text-tertiary">已确认</dt>
              <dd className="font-mono text-lg font-semibold tabular-nums text-text">{stat?.by_status?.confirmed ?? 0}</dd>
            </div>
          </dl>

          {/* 窄屏（< md）分布条独占一行，避免与计数挤在同一行被压扁 */}
          <div className="min-w-0 flex-1 basis-full md:basis-0">
            <div className="mb-2 flex h-4 gap-0.5 overflow-hidden rounded-full bg-surface-2">
              {sevCounts
                .filter((c) => c.n)
                .map((x) => (
                  <button
                    key={x.sev}
                    type="button"
                    onClick={() => toggleSevFilter(x.sev)}
                    title={`${severityLabel[x.sev]} ${x.n}`}
                    aria-label={`按${severityLabel[x.sev]}筛选（${x.n}）`}
                    aria-pressed={filters.severity === x.sev}
                    style={{ flex: x.n, background: severityColorOf(x.sev) }}
                    className={cn(
                      'rounded-sm transition-opacity hover:opacity-80',
                      filters.severity === x.sev && 'ring-2 ring-inset ring-surface',
                    )}
                  />
                ))}
            </div>
            <div className="flex flex-wrap gap-3 text-xs">
              {sevCounts.map((x) => (
                <button
                  key={x.sev}
                  type="button"
                  disabled={!x.n}
                  onClick={() => toggleSevFilter(x.sev)}
                  aria-pressed={filters.severity === x.sev}
                  className={cn(
                    'flex items-center gap-1.5 rounded-md px-2 py-1 transition-colors',
                    x.n ? 'hover:bg-surface-2' : 'cursor-default opacity-40',
                    filters.severity === x.sev && 'bg-surface-2 ring-1 ring-inset ring-border-strong',
                  )}
                >
                  <span className="h-2 w-2 rounded-sm" style={{ background: severityColorOf(x.sev) }} />
                  <span className="text-text-secondary">{severityLabel[x.sev]}</span>
                  <span className="font-mono font-semibold tabular-nums text-text">{x.n}</span>
                </button>
              ))}
            </div>
          </div>
        </div>
      </section>

      {/* 筛选栏（全站统一顺序：枚举下拉（粗→细）→ 关键词搜索 → 清空 →（右端）分页）。
          来源取值对齐 assignment.source（direct / proxy）。 */}
      <FilterBar
        label="漏洞筛选"
        canReset={hasFilter}
        onReset={resetAll}
        className="mt-4"
        trailing={
          <PaginationToolbar
            page={page}
            pageSize={size}
            total={total}
            totalPages={totalPages}
            onPageChange={setPage}
            onPageSizeChange={setSize}
            loading={listQuery.isFetching}
          />
        }
      >
        <Select
          value={filters.source}
          onChange={(e) => setFilters({ ...filters, source: e.target.value })}
          className="w-32"
          aria-label="按来源筛选"
        >
          <option value="">全部来源</option>
          {SOURCE_OPTIONS.map((o) => (
            <option key={o.value} value={o.value}>
              {o.label}
            </option>
          ))}
        </Select>

        <EntityPicker
          value={filters.task ?? ''}
          valueLabel={selectedTask.data?.goal.replace(/\s+/g, ' ')}
          onChange={(task) => setFilters({ ...filters, task })}
          fetchOptions={fetchTaskOptions(facetFilters)}
          queryKey={['findings', 'task-options', facetFilters]}
          emptyLabel="全部任务"
          searchPlaceholder="搜索任务目标"
          ariaLabel="按任务筛选"
          className="w-full sm:w-64"
        />

        <Select
          value={filters.status}
          onChange={(e) => setFilters({ ...filters, status: e.target.value })}
          className="w-32"
          aria-label="按处置状态筛选"
        >
          <option value="">全部状态</option>
          {FINDING_STATUS_OPTIONS.map((o) => (
            <option key={o.value} value={o.value}>
              {o.label}
            </option>
          ))}
        </Select>

        <SearchInput
          className="w-64"
          value={query}
          onChange={setQuery}
          placeholder="搜索漏洞、主机、路径、任务目标…"
          ariaLabel="搜索漏洞、主机、路径或任务目标"
        />
      </FilterBar>

      <div className="min-h-0 flex-1 overflow-y-auto px-6 py-4">
        {listError ? (
          <div className="py-16 text-center text-sm text-sev-critical">⚠ {listError}</div>
        ) : (
          /* 表格：外壳（列宽/骨架/空态/行交互）走 DataTable 通用组件；
             严重度左条（--sev 悬停）与保存中降透明经 rowStyle/rowClassName 注入 */
          <DataTable
            ariaLabel="漏洞列表"
            rows={rows}
            columns={findingColumns as never}
            loading={listQuery.isPending || (listQuery.isFetching && rows.length === 0)}
            hasFilter={hasFilter}
            emptyText="漏洞——发起扫描或挂代理收流量后，AI 挖到的漏洞会汇总到此"
            getRowId={(row) => row.id}
            onRowClick={openDrawer}
            rowAriaLabel={(f) => `查看漏洞详情：${f.summary}`}
            rowStyle={(f) => ({ '--sev': severityColorOf(f.severity) }) as React.CSSProperties}
            rowClassName={(f) => (savingId === f.id ? 'opacity-50' : undefined)}
          />
        )}
      </div>

      <FindingDrawer
        open={drawerOpen}
        finding={drawerFinding}
        onOpenChange={setDrawerOpen}
        onSave={(p) => void saveTriage(p)}
      />
    </div>
  )
}
