import { useCallback, useEffect, useMemo, useState } from 'react'
import { createColumnHelper, flexRender, getCoreRowModel, useReactTable } from '@tanstack/react-table'
import { ChevronRight, Search, StickyNote } from 'lucide-react'
import { listFindings, updateFindingTriage } from '@/api/client'
import type { FindingFilters, FindingRow } from '@/api/types'
import { SEVERITIES, severityColor, severityLabel, severityRank } from '@/lib/severity'
import { FINDING_STATUS_OPTIONS, findingStatusMeta } from '@/lib/findingStatus'
import { Badge } from '@/components/ui/badge'
import { columnWidthPercents } from '@/lib/tableLayout'
import { cn } from '@/lib/utils'
import { FindingDrawer } from '@/features/finding/FindingDrawer'

function sevVar(sev: string): string {
  return severityColor[sev.toLowerCase()] ?? '#6e7681'
}

const columnHelper = createColumnHelper<FindingRow>()

// 漏洞管理页（全局台账）：跨 task/host 展示所有漏洞（active + passive），支持 triage 处置流转。
// 布局：紧凑的一行 severity 统计条（并入筛选栏上方，不再是占地 100px 的独立大卡片）+
// 搜索/筛选栏 + TanStack Table 渲染的密集表格（真实 <table> 语义，列宽由 column def 单点定义，
// 不再是列头/数据行各自一份 grid-cols 字符串手动同步）。
// 每行整行可点 → 右侧详情抽屉（evidence/PoC/修复建议/状态·严重度·备注编辑）；行尾 › 展开提示。
export function FindingsPage() {
  const [rows, setRows] = useState<FindingRow[]>([])
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')
  const [fHost, setFHost] = useState('')
  const [fSeverity, setFSeverity] = useState('')
  const [fStatus, setFStatus] = useState('')
  const [fMode, setFMode] = useState('')
  const [query, setQuery] = useState('')
  const [allHosts, setAllHosts] = useState<string[]>([])
  const [savingId, setSavingId] = useState('')
  const [drawerOpen, setDrawerOpen] = useState(false)
  const [drawerFinding, setDrawerFinding] = useState<FindingRow | null>(null)

  const load = useCallback(async (filters: { host: string; severity: string; status: string; mode: string }) => {
    setLoading(true)
    setError('')
    try {
      const f: FindingFilters = {}
      if (filters.host) f.host = filters.host
      if (filters.severity) f.severity = filters.severity
      if (filters.status) f.status = filters.status
      if (filters.mode) f.mode = filters.mode
      setRows(await listFindings(f))
    } catch (e) {
      setError(e instanceof Error ? e.message : '加载失败')
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    void load({ host: fHost, severity: fSeverity, status: fStatus, mode: fMode })
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [fHost, fSeverity, fStatus, fMode])

  // host 下拉选项来自一份全量快照（不受当前筛选收窄影响，保持稳定）。
  useEffect(() => {
    void listFindings({}).then(
      (all) => setAllHosts([...new Set(all.map((f) => f.host))].sort()),
      () => {
        // 静默：host 下拉是增强项，失败则退化为空
      },
    )
  }, [])

  // 前端搜索过滤后的行（后端已按维度筛，这里叠加标题/host/path 模糊）。
  const visible = useMemo(() => {
    const q = query.trim().toLowerCase()
    const base = q
      ? rows.filter((f) => `${f.summary}${f.host}${f.target?.path ?? ''}`.toLowerCase().includes(q))
      : rows
    return [...base].sort((a, b) => severityRank(a.severity) - severityRank(b.severity))
  }, [rows, query])

  // severity 计数（紧凑统计条），基于当前可见集。
  const sevCounts = useMemo(
    () => SEVERITIES.map((s) => ({ sev: s, n: visible.filter((f) => f.severity.toLowerCase() === s).length })),
    [visible],
  )

  // 点统计条某个 severity 段 = 按该 severity 筛选（重拉；再点取消）。
  const toggleSevFilter = (sev: string) => setFSeverity((prev) => (prev === sev ? '' : sev))

  const applyUpdated = (updated: FindingRow) => {
    setRows((prev) => prev.map((x) => (x.id === updated.id ? { ...x, ...updated } : x)))
    setDrawerFinding((prev) => (prev?.id === updated.id ? { ...prev, ...updated } : prev))
    if (fStatus && fStatus !== updated.status) void load({ host: fHost, severity: fSeverity, status: fStatus, mode: fMode })
  }

  const openDrawer = (f: FindingRow) => {
    setDrawerFinding(f)
    setDrawerOpen(true)
  }

  // 抽屉内保存 triage（状态 + 严重度 + 备注一起）：乐观更新 + 用返回行覆盖 / 失败回滚。
  const saveTriage = async (payload: { id: string; status: string; severity: string; note: string }) => {
    const before = rows.find((x) => x.id === payload.id)
    setSavingId(payload.id)
    setRows((prev) =>
      prev.map((x) =>
        x.id === payload.id
          ? { ...x, status: payload.status, severity: payload.severity, triage_note: payload.note, triaged_at: new Date().toISOString() }
          : x,
      ),
    )
    try {
      applyUpdated(await updateFindingTriage(payload.id, payload.status, payload.severity, payload.note))
    } catch {
      if (before) setRows((prev) => prev.map((x) => (x.id === payload.id ? before : x)))
      window.alert('处置保存失败，请重试')
    } finally {
      setSavingId('')
    }
  }

  const hasFilter = !!(fHost || fSeverity || fStatus || fMode)

  // 列定义单点声明列宽——不再是列头/数据行各自一份 grid-cols 字符串手动保持同步。
  const columns = useMemo(
    () => [
      columnHelper.accessor('severity', {
        header: '严重度',
        size: 76, // 相对权重（非像素）——经 columnWidthPercents 换算成百分比，随容器宽度缩放
        cell: (ctx) => {
          const sev = ctx.getValue()
          return (
            <Badge color={sevVar(sev)}>{severityLabel[sev.toLowerCase()] || sev}</Badge>
          )
        },
      }),
      columnHelper.accessor('summary', {
        header: '漏洞',
        size: 320, // 权重最大——占比例布局里的剩余大头空间
        cell: (ctx) => {
          const f = ctx.row.original
          return (
            <span className="flex items-center gap-1.5 overflow-hidden truncate">
              {f.summary}
              {f.triage_note && <StickyNote className="h-3 w-3 flex-shrink-0 text-muted" aria-label="有处置备注" />}
            </span>
          )
        },
      }),
      columnHelper.display({
        id: 'location',
        header: '位置',
        size: 280,
        cell: (ctx) => {
          const f = ctx.row.original
          return (
            <span className="overflow-hidden truncate font-mono text-[11.5px] text-muted">
              {f.target?.method} {f.host}
              {f.target?.path}
              {f.cwe_id && ` · ${f.cwe_id}`}
            </span>
          )
        },
      }),
      columnHelper.accessor('mode', {
        header: '来源',
        size: 60,
        cell: (ctx) => {
          const mode = ctx.getValue()
          return (
            <Badge
              dot={false}
              color={mode === 'passive' ? 'var(--mode-passive)' : 'var(--mode-active)'}
            >
              {mode === 'passive' ? '被动' : '主动'}
            </Badge>
          )
        },
      }),
      columnHelper.accessor('status', {
        header: '状态',
        size: 96,
        cell: (ctx) => {
          const meta = findingStatusMeta(ctx.getValue())
          return <Badge color={meta.color}>{meta.label}</Badge>
        },
      }),
      columnHelper.display({
        id: 'expand',
        header: '',
        size: 20,
        cell: () => <ChevronRight className="h-4 w-4 text-muted opacity-50" />,
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

  return (
    <div className="flex h-full min-h-0 flex-col">
      <div className="min-h-0 flex-1 overflow-y-auto p-5.5">
        {loading ? (
          <div className="py-16 text-center text-[13.5px] text-muted">加载中…</div>
        ) : error ? (
          <div className="py-16 text-center text-[13.5px] text-sev-critical">⚠ {error}</div>
        ) : rows.length === 0 ? (
          <div className="py-16 text-center text-[13.5px] text-muted">
            {hasFilter ? '当前筛选无匹配漏洞' : '暂无漏洞——发起扫描或挂代理收流量后，AI 挖到的漏洞会汇总到此'}
          </div>
        ) : (
          <>
            {/* 顶部汇总条：大总数 + severity 堆叠占比条 + 可点图例。此区块信息密度低但是本页
                第一眼要看的东西——总数、待处理数、严重度分布——字号故意放大，不做成紧凑单行。 */}
            <div className="mb-4 flex items-center gap-8 rounded-[18px] border border-border bg-surface px-5.5 py-4.5 shadow">
              <div className="flex flex-shrink-0 items-center gap-3.5">
                <span className="text-[48px] font-extrabold leading-none tracking-tight text-text tabular-nums">
                  {visible.length}
                </span>
                <span className="text-[13px] leading-relaxed text-muted">
                  漏洞总数
                  <br />
                  <em className="text-xs font-normal not-italic text-faint">
                    {visible.filter((f) => f.status === 'open').length} 待处理 ·{' '}
                    {visible.filter((f) => f.status === 'confirmed').length} 已确认
                  </em>
                </span>
              </div>
              <div className="min-w-0 flex-1">
                <div className="flex h-4 gap-0.5 overflow-hidden rounded-full bg-surface-2/60">
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
                          'rounded-[3px] transition-opacity hover:opacity-80',
                          fSeverity === x.sev && 'ring-2 ring-inset ring-text',
                        )}
                      />
                    ))}
                </div>
                <div className="mt-3 flex flex-wrap gap-x-4.5 gap-y-2 text-[12.5px] text-muted">
                  {sevCounts.map((x) => (
                    <button
                      key={x.sev}
                      type="button"
                      disabled={!x.n}
                      onClick={() => toggleSevFilter(x.sev)}
                      className={cn(
                        'select-none rounded-md px-2 py-0.5 transition-colors',
                        x.n ? 'hover:bg-surface-2' : 'cursor-default opacity-40',
                        fSeverity === x.sev && 'bg-surface-2 text-text ring-1 ring-inset ring-border-strong',
                      )}
                    >
                      <i className="mr-1.5 inline-block h-2.5 w-2.5 rounded-[3px] align-[-1px]" style={{ background: sevVar(x.sev) }} />
                      {severityLabel[x.sev]} <b className="text-text tabular-nums">{x.n}</b>
                    </button>
                  ))}
                </div>
              </div>
            </div>

            {/* 搜索 + 筛选栏 */}
            <div className="mb-3.5 flex items-center gap-2.5">
              <div className="flex h-9 flex-1 items-center gap-2 rounded-[10px] border border-border bg-surface px-3">
                <Search className="h-4 w-4 flex-shrink-0 text-muted" />
                <input
                  type="search"
                  value={query}
                  onChange={(e) => setQuery(e.target.value)}
                  placeholder="搜索漏洞标题 / host / 路径…"
                  spellCheck={false}
                  className="flex-1 bg-transparent text-[13.5px] text-text outline-none placeholder:text-muted"
                />
              </div>
              <select value={fMode} onChange={(e) => setFMode(e.target.value)} className="w-[120px] rounded-md border border-border bg-surface px-2 py-1.5 text-sm text-text outline-none focus:border-accent">
                <option value="">全部来源</option>
                <option value="active">主动</option>
                <option value="passive">被动</option>
              </select>
              <select value={fStatus} onChange={(e) => setFStatus(e.target.value)} className="w-[120px] rounded-md border border-border bg-surface px-2 py-1.5 text-sm text-text outline-none focus:border-accent">
                <option value="">全部状态</option>
                {FINDING_STATUS_OPTIONS.map((o) => (
                  <option key={o.value} value={o.value}>
                    {o.label}
                  </option>
                ))}
              </select>
              <select value={fHost} onChange={(e) => setFHost(e.target.value)} className="w-[180px] rounded-md border border-border bg-surface px-2 py-1.5 text-sm text-text outline-none focus:border-accent">
                <option value="">全部 host</option>
                {allHosts.map((h) => (
                  <option key={h} value={h}>
                    {h}
                  </option>
                ))}
              </select>
              <span className="flex-shrink-0 pl-0.5 text-[12.5px] text-muted tabular-nums">{visible.length} 条</span>
            </div>

            {/* 密集表格：真实 <table> 语义，table-fixed + colgroup 百分比宽度——列宽由
                columnDef.size（相对权重）换算成百分比，随容器宽度等比缩放，不会出现
                某列固定 px 不变、把其它列挤到断行的问题。 */}
            <div className="overflow-hidden rounded-[18px] border border-border bg-surface shadow">
              <table className="w-full border-collapse table-fixed">
                <colgroup>
                  {leafColumns.map((col, i) => (
                    <col key={col.id} style={{ width: widthPercents[i] }} />
                  ))}
                </colgroup>
                <thead>
                  {table.getHeaderGroups().map((hg) => (
                    <tr key={hg.id} className="border-b border-border">
                      {hg.headers.map((h) => (
                        <th key={h.id} className="px-4.5 py-3 text-left text-[11px] font-semibold uppercase tracking-wide text-muted opacity-70">
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
                          'cursor-pointer border-b border-l-[3px] border-border border-l-transparent text-[13px] transition-colors',
                          'last:border-b-0 hover:bg-surface-2 hover:border-l-[var(--sev)]',
                          savingId === f.id && 'opacity-55',
                        )}
                      >
                        {row.getVisibleCells().map((cell) => (
                          <td key={cell.id} className="overflow-hidden px-4.5 py-3">
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
