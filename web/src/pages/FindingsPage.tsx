import { useMemo, useState } from 'react'
import { createColumnHelper, flexRender, getCoreRowModel, useReactTable } from '@tanstack/react-table'
import { ChevronRight, Search, StickyNote } from 'lucide-react'
import { updateFindingTriage, getFindingStat } from '@/api/client'
import type { FindingRow } from '@/api/types'
import { SEVERITIES, severityColor, severityLabel } from '@/lib/severity'
import { FINDING_STATUS_OPTIONS, findingStatusMeta } from '@/lib/findingStatus'
import { Badge } from '@/components/ui/badge'
import { PaginationToolbar } from '@/components/ui/PaginationToolbar'
import { columnWidthPercents } from '@/lib/tableLayout'
import { cn } from '@/lib/utils'
import { FindingDrawer } from '@/features/finding/FindingDrawer'
import { useFindingFilters } from '@/features/finding/useFindingFilters'
import { useQueryClient, keepPreviousData, useQuery } from '@tanstack/react-query'
import { useFindingList } from '@/features/finding/useFindingQueries'

function sevVar(sev: string): string {
  return severityColor[sev.toLowerCase()] ?? '#6e7681'
}

const columnHelper = createColumnHelper<FindingRow>()

/**
 * FindingsPage - 漏洞管理页面（平铺台账）
 */
export function FindingsPage() {
  const { filters, page, size, setFilters, setPage, setSize } = useFindingFilters()
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

  const hasFilter = !!(filters.host || filters.severity || filters.status || filters.source)

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
          return <Badge color={sevVar(sev)} size="sm">{severityLabel[sev.toLowerCase()] || sev}</Badge>
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
            <Badge
              dot={false}
              size="sm"
              color={source === 'auto' ? 'var(--source-auto)' : 'var(--source-manual)'}
            >
              {source === 'auto' ? '被动代理' : '主动下发'}
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

  const table = useReactTable({
    data: visible,
    columns,
    getCoreRowModel: getCoreRowModel(),
    getRowId: (row) => row.id,
  })
  const leafColumns = table.getAllLeafColumns()
  const widthPercents = columnWidthPercents(leafColumns.map((col) => col.columnDef.size ?? 0))

  const listError = listQuery.isError
    ? (listQuery.error instanceof Error ? listQuery.error.message : '加载失败')
    : ''

  const hasData = rows.length > 0

  return (
    <div className="flex h-full flex-col bg-background">
      {/* 统一头部 */}
      <header className="flex items-center justify-between border-b border-border bg-surface px-6 py-4">
        <div>
          <h1 className="text-base font-semibold text-text">漏洞</h1>
          <p className="mt-0.5 text-sm text-text-secondary">智能体发现的安全漏洞与配置缺陷</p>
        </div>
      </header>

      <div className="min-h-0 flex-1 overflow-y-auto p-6">
        {listQuery.isPending ? (
          <div className="py-16 text-center text-sm text-text-secondary">
            <div className="inline-block h-6 w-6 animate-spin rounded-full border-2 border-accent border-t-transparent" />
            <p className="mt-3">加载中…</p>
          </div>
        ) : listError ? (
          <div className="py-16 text-center text-sm text-status-error">⚠ {listError}</div>
        ) : !hasData ? (
          <div className="py-16 text-center text-sm text-text-secondary">
            {hasFilter ? '当前筛选无匹配漏洞' : '暂无漏洞——发起扫描或挂代理收流量后，AI 挖到的漏洞会汇总到此'}
          </div>
        ) : (
          <>
            {/* 紧凑统计卡片（瘦身版）*/}
            <div className="mb-5 flex items-center gap-6 rounded-xl border border-border bg-surface px-6 py-3 shadow-sm">
              {/* 总数指示（删除大图标）*/}
              <div className="flex items-center gap-4">
                <div>
                  <div className="text-2xl font-bold tabular-nums text-text">{total}</div>
                </div>
              </div>

              {/* 分隔线 */}
              <div className="h-10 w-px bg-border" />

              {/* 快速统计 */}
              <div className="flex flex-1 items-center gap-4 text-xs">
                <div>
                  <span className="text-text-tertiary">待处理</span>
                  <span className="ml-2 font-mono text-lg font-semibold tabular-nums text-text">
                    {stat?.by_status?.open ?? 0}
                  </span>
                </div>
                <div>
                  <span className="text-text-tertiary">已确认</span>
                  <span className="ml-2 font-mono text-lg font-semibold tabular-nums text-text">
                    {stat?.by_status?.confirmed ?? 0}
                  </span>
                </div>
              </div>

              {/* 严重度分布条（加粗到 h-4）*/}
              <div className="flex-1">
                <div className="mb-2 flex h-4 gap-0.5 overflow-hidden rounded-full bg-surface-2">
                  {sevCounts
                    .filter((c) => c.n)
                    .map((x) => (
                      <button
                        key={x.sev}
                        type="button"
                        onClick={() => toggleSevFilter(x.sev)}
                        title={`${severityLabel[x.sev]} ${x.n}`}
                        style={{ flex: x.n, background: sevVar(x.sev) }}
                        className={cn(
                          'rounded-sm transition-all hover:opacity-80',
                          filters.severity === x.sev && 'ring-2 ring-inset ring-white',
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
                      className={cn(
                        'flex items-center gap-1.5 rounded-md px-2 py-1 transition-colors',
                        x.n ? 'hover:bg-surface-2' : 'cursor-default opacity-40',
                        filters.severity === x.sev && 'bg-surface-2 ring-1 ring-inset ring-border-strong',
                      )}
                    >
                      <span className="h-2 w-2 rounded-sm" style={{ background: sevVar(x.sev) }} />
                      <span className="text-text-secondary">{severityLabel[x.sev]}</span>
                      <span className="font-mono font-semibold tabular-nums text-text">{x.n}</span>
                    </button>
                  ))}
                </div>
              </div>
            </div>

            {/* 搜索筛选栏（全站统一顺序：枚举下拉（粗→细）→ 关键词搜索 →（右端）分页） */}
            <div className="-mx-6 mb-3 flex items-center gap-3 border-b border-border bg-surface/30 px-6 pb-3">
              <select
                value={filters.source}
                onChange={(e) => setFilters({ ...filters, source: e.target.value })}
                className="h-9 w-32 rounded-lg border border-border bg-surface px-3 text-sm text-text outline-none transition-colors focus:border-accent focus:glow-accent"
              >
                <option value="">全部来源</option>
                <option value="manual">主动下发</option>
                <option value="auto">被动代理</option>
              </select>

              <select
                value={filters.status}
                onChange={(e) => setFilters({ ...filters, status: e.target.value })}
                className="h-9 w-32 rounded-lg border border-border bg-surface px-3 text-sm text-text outline-none transition-colors focus:border-accent focus:glow-accent"
              >
                <option value="">全部状态</option>
                {FINDING_STATUS_OPTIONS.map((o) => (
                  <option key={o.value} value={o.value}>
                    {o.label}
                  </option>
                ))}
              </select>

              <div className="relative flex-1">
                <Search className="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-text-tertiary" />
                <input
                  type="text"
                  value={query}
                  onChange={(e) => setQuery(e.target.value)}
                  placeholder="搜索漏洞标题 / host / 路径…"
                  spellCheck={false}
                  aria-label="搜索漏洞标题、host 或路径"
                  className="h-9 w-full rounded-lg border border-border bg-surface pl-10 pr-3 text-sm text-text outline-none transition-all placeholder:text-text-tertiary focus:border-accent focus:glow-accent"
                />
              </div>

              <PaginationToolbar
                page={page}
                pageSize={size}
                total={total}
                totalPages={totalPages}
                onPageChange={setPage}
                onPageSizeChange={setSize}
                loading={listQuery.isFetching}
              />
            </div>

            {/* 表格（min-width 兜底：窄屏出横向滚动条，列不压缩——全站表格统一约定） */}
            <div className="overflow-x-auto rounded-xl border border-border bg-surface shadow-sm">
                <table className="w-full min-w-[1040px] border-collapse table-fixed">
                  <colgroup>
                    {leafColumns.map((col, i) => (
                      <col key={col.id} style={{ width: widthPercents[i] }} />
                    ))}
                  </colgroup>
                  <thead>
                    {table.getHeaderGroups().map((hg) => (
                      <tr key={hg.id} className="border-b border-border">
                        {hg.headers.map((h) => (
                          <th
                            key={h.id}
                            className={cn(
                              'px-4 py-3 text-left text-xs font-semibold uppercase tracking-wide text-text-tertiary',
                              h.column.columnDef.meta?.align === 'right' && 'text-right',
                            )}
                          >
                            {flexRender(h.column.columnDef.header, h.getContext())}
                          </th>
                        ))}
                      </tr>
                    ))}
                  </thead>
                  <tbody>
                    {table.getRowModel().rows.map((row) => {
                      const f = row.original
                      return (
                        <tr
                          key={row.id}
                          role="button"
                          tabIndex={0}
                          aria-label={`查看漏洞详情：${f.summary}`}
                          onClick={() => openDrawer(f)}
                          onKeyDown={(e) => {
                            if (e.key === 'Enter' || e.key === ' ') {
                              e.preventDefault()
                              openDrawer(f)
                            }
                          }}
                          style={{ '--sev': sevVar(f.severity) } as React.CSSProperties}
                          className={cn(
                            'group cursor-pointer border-b border-l-2 border-border border-l-transparent text-sm transition-all',
                            'last:border-b-0 hover:bg-surface-2 hover:border-l-[var(--sev)]',
                            savingId === f.id && 'opacity-50',
                          )}
                        >
                          {row.getVisibleCells().map((cell) => (
                            <td key={cell.id} className="overflow-hidden px-4 py-2.5">
                              {flexRender(cell.column.columnDef.cell, cell.getContext())}
                            </td>
                          ))}
                        </tr>
                      )
                    })}
                  </tbody>
                </table>
              </div>
          </>
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
