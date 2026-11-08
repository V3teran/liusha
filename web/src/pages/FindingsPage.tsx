import { SearchInput } from '@/components/SearchInput'
import { FilterBar } from '@/components/FilterBar'
import { Select } from '@/components/ui/Select'
import { PageHeader } from '@/components/PageHeader'
import { useMemo, useState } from 'react'
import { createColumnHelper } from '@tanstack/react-table'
import { ChevronRight, StickyNote } from 'lucide-react'
import { updateFindingTriage, getFindingStat } from '@/api/findings'
import type { FindingRow } from '@/api/types'
import { SEVERITIES, severityColorOf, severityLabel } from '@/lib/severity'
import { DataTable } from '@/components/DataTable'
import { FINDING_STATUS_OPTIONS, findingStatusMeta } from '@/features/finding/findingStatus'
import { Badge } from '@/components/ui/Badge'
import { PaginationToolbar } from '@/components/ui/PaginationToolbar'
import { cn } from '@/lib/utils'
import { FindingDrawer } from '@/features/finding/FindingDrawer'
import { useFindingFilters } from '@/features/finding/useFindingFilters'
import { useQueryClient, keepPreviousData, useQuery } from '@tanstack/react-query'
import { useFindingList } from '@/features/finding/useFindingQueries'
import { SOURCE_OPTIONS, sourceColor, sourceLabel } from '@/lib/source'

const columnHelper = createColumnHelper<FindingRow>()

/**
 * FindingsPage - 漏洞管理页面（平铺台账）
 */
export function FindingsPage() {
  const { filters, page, size, setFilters, setPage, setSize, resetFilters } = useFindingFilters()
  const queryClient = useQueryClient()

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

  const [query, setQuery] = useState('')
  const [savingId, setSavingId] = useState('')
  const [drawerOpen, setDrawerOpen] = useState(false)
  const [drawerFinding, setDrawerFinding] = useState<FindingRow | null>(null)

  const visible = useMemo(() => {
    const q = query.trim().toLowerCase()
    if (!q) return rows
    return rows.filter((f) => `${f.summary}${f.host}${f.target?.path ?? ''}`.toLowerCase().includes(q))
  }, [rows, query])

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

  const hasFilter = !!(query || filters.host || filters.severity || filters.status || filters.source)

  const columns = useMemo(
    () => [
      columnHelper.accessor('seq', {
        header: '#',
        size: 64,
        meta: { align: 'right' },
        cell: (ctx) => (
          <span className="block text-right font-mono text-xs tabular-nums text-text-tertiary">{ctx.getValue()}</span>
        ),
      }),
      columnHelper.accessor('severity', {
        header: '严重度',
        size: 90,
        cell: (ctx) => {
          const sev = ctx.getValue()
          return <Badge color={severityColorOf(sev)} size="sm">{severityLabel[sev.toLowerCase()] || sev}</Badge>
        },
      }),
      columnHelper.accessor('summary', {
        header: '漏洞',
        size: 400,
        cell: (ctx) => {
          const f = ctx.row.original
          return (
            <span className="flex items-center gap-2 overflow-hidden">
              <span className="truncate" title={f.summary}>{f.summary}</span>
              {f.triage_note && (
                <StickyNote className="h-3.5 w-3.5 flex-shrink-0 text-text-tertiary" aria-label="有处置备注" />
              )}
            </span>
          )
        },
      }),
      columnHelper.display({
        id: 'location',
        header: '地址',
        size: 240,
        cell: (ctx) => {
          const f = ctx.row.original
          return (
            <span
              className="overflow-hidden truncate font-mono text-xs text-text-secondary"
              title={`${f.target?.method || ''} ${f.host}${f.target?.path || ''}${f.cwe_id ? ` · ${f.cwe_id}` : ''}`}
            >
              {f.target?.method} {f.host}
              {f.target?.path}
              {f.cwe_id && ` · ${f.cwe_id}`}
            </span>
          )
        },
      }),
      columnHelper.accessor('source', {
        header: '来源',
        size: 90,
        cell: (ctx) => {
          const source = ctx.getValue()
          return (
            <Badge dot={false} size="sm" color={sourceColor(source)}>
              {sourceLabel(source)}
            </Badge>
          )
        },
      }),
      columnHelper.accessor('status', {
        header: '状态',
        size: 96,
        cell: (ctx) => {
          const meta = findingStatusMeta(ctx.getValue())
          return <Badge color={meta.color} size="sm">{meta.label}</Badge>
        },
      }),
      columnHelper.display({
        id: 'expand',
        header: '',
        size: 48,
        cell: () => <ChevronRight className="h-4 w-4 text-text-tertiary opacity-50" />,
      }),
    ],
    [],
  )

  const listError = listQuery.isError
    ? (listQuery.error instanceof Error ? listQuery.error.message : '加载失败')
    : ''

  const resetAll = () => {
    resetFilters()
    setQuery('')
  }

  return (
    <div className="flex h-full min-h-0 flex-col">
      <PageHeader title="漏洞" subtitle="智能体发现的安全漏洞与配置缺陷" />

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
          placeholder="搜索漏洞标题 / host / 路径…"
          ariaLabel="搜索漏洞标题、host 或路径"
        />
      </FilterBar>

      <div className="min-h-0 flex-1 overflow-y-auto px-6 py-4">
        {listError ? (
          <div className="py-16 text-center text-sm text-sev-critical">⚠ {listError}</div>
        ) : (
          /* 表格：外壳（列宽/骨架/空态/行交互）走 DataTable 通用组件；
             严重度左条（--sev 悬停）与保存中降透明经 rowStyle/rowClassName 注入 */
          <DataTable
            rows={visible}
            columns={columns as never}
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
