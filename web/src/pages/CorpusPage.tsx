import { useEffect, useMemo, useState } from 'react'
import { useSearchParams } from 'react-router-dom'
import * as Tabs from '@radix-ui/react-tabs'
import { FileUp, Plus, X } from 'lucide-react'
import type { CorpusEntry, CorpusFacets, CorpusFilters, CorpusScenarioRef, CorpusStatus } from '@/api/corpus'
import { DataTable } from '@/components/DataTable'
import { FilterBar } from '@/components/FilterBar'
import { PageHeader } from '@/components/PageHeader'
import { SearchInput } from '@/components/SearchInput'
import { Button } from '@/components/ui/Button'
import { PaginationToolbar } from '@/components/ui/PaginationToolbar'
import { Select } from '@/components/ui/Select'
import { corpusColumns } from '@/features/corpus/corpusColumns'
import { CorpusCreateDialog } from '@/features/corpus/CorpusCreateDialog'
import { CorpusDrawer } from '@/features/corpus/CorpusDrawer'
import { CorpusImportDialog } from '@/features/corpus/CorpusImportDialog'
import { CorpusRetrieval } from '@/features/corpus/CorpusRetrieval'
import { useCorpusFacets, useCorpusFilters, useCorpusList, useCorpusStatus } from '@/features/corpus/useCorpusQueries'
import { useScenarioOptions } from '@/features/scenarios/useScenarioQueries'
import { useDebouncedSearch } from '@/hooks/useDebouncedSearch'
import { useSelectedParam } from '@/hooks/useSelectedParam'
import { CORPUS_SOURCE_OPTIONS, corpusScopeLabel } from '@/lib/corpus'
import { cn } from '@/lib/utils'
import { ErrorState } from '@/components/ui/ErrorState'

/** 页面的两个视图：知识条目（缺省）与检索测试，写在 URL 的 ?view=。 */
const VIEW_RETRIEVAL = 'retrieval'

const TAB_TRIGGER =
  'relative -mb-px whitespace-nowrap border-b-2 border-transparent px-3 py-2.5 text-sm text-text-secondary outline-none transition-colors hover:text-text focus-visible:text-text data-[state=active]:border-accent data-[state=active]:font-medium data-[state=active]:text-text'

/** 范围下拉里「通用」的值：场景标识只含小写字母、数字与连字符，不会与它相撞。 */
const GENERAL = '*general'

function scopeValue(f: CorpusFilters): string {
  return f.general ? GENERAL : f.scenario
}

/** 检索方式标识：语义检索启用与否一眼可见；悬停看启用的模型与待补向量的条目。 */
function ModeChip({ status }: { status: CorpusStatus | undefined }) {
  if (!status) return null
  const semantic = status.mode === 'semantic'
  const title = semantic
    ? `向量模型 ${status.embedding_model} · 重排模型 ${status.rerank_model}${status.pending ? ` · ${status.pending} 条待建索引` : ''}`
    : status.unavailable
      ? `${status.unavailable}：只按词元做字面检索`
      : '检索模型未配置：只按词元做字面检索，在「模型配置」页填写密钥后启用语义检索'
  return (
    <span
      title={title}
      className={cn(
        // 手机宽度不显示：页头固定一行，让给标题与动作
        'hidden h-9 items-center gap-1.5 rounded-lg border px-2.5 text-xs sm:inline-flex md:px-3',
        semantic ? 'border-border text-text-secondary' : 'border-status-warning/40 text-status-warning',
      )}
    >
      <span aria-hidden className={cn('h-1.5 w-1.5 rounded-full', semantic ? 'bg-status-success' : 'bg-status-warning')} />
      {semantic ? '语义检索' : '字面检索'}
      {semantic && status.pending > 0 && (
        <span className="hidden text-text-tertiary lg:inline">· {status.pending} 条待建索引</span>
      )}
    </span>
  )
}

function facetCount(facets: CorpusFacets | undefined, pick: (f: CorpusFacets) => number | undefined): string {
  const n = facets ? (pick(facets) ?? 0) : undefined
  return n === undefined ? '' : `（${n}）`
}

/**
 * 知识库：任务收尾沉淀与人工编写的跨任务知识。两个视图：知识条目（服务端筛选与分页）与检索
 * 测试（执行与智能体相同的检索），视图写在 ?view=；打开的条目写在 ?selected=（详情抽屉，
 * 两个视图共用，打开条目不丢检索结果）。
 */
export function CorpusPage() {
  const [searchParams, setSearchParams] = useSearchParams()
  const view = searchParams.get('view') === VIEW_RETRIEVAL ? VIEW_RETRIEVAL : ''
  const setView = (next: string) =>
    setSearchParams((prev) => {
      const p = new URLSearchParams(prev)
      if (next) p.set('view', next)
      else p.delete('view')
      p.delete('selected')
      return p
    })
  const [hits, setHits] = useState<CorpusEntry[]>([])
  const { filters, page, size, setFilters, setPage, setSize, resetFilters } = useCorpusFilters()
  const list = useCorpusList(filters, page, size)
  const facets = useCorpusFacets(filters).data
  const status = useCorpusStatus().data
  const scenarios = useScenarioOptions()
  const [selected, select] = useSelectedParam()
  const [dialog, setDialog] = useState<'' | 'create' | 'import'>('')
  const [imported, setImported] = useState<{
    imported: number
    duplicates: number
  } | null>(null)

  // 关键词在服务端检索，停顿后写入 URL（打字不是导航：改写当前历史）
  const [query, setQuery] = useState(filters.search)
  const debounced = useDebouncedSearch(query.trim())
  useEffect(() => {
    if (debounced !== filters.search) setFilters({ ...filters, search: debounced }, { replace: true })
    // eslint-disable-next-line react-hooks/exhaustive-deps -- 只在防抖值变化时写回
  }, [debounced])
  useEffect(() => {
    setQuery((q) => (q.trim() === filters.search ? q : filters.search))
  }, [filters.search])

  const rows = useMemo(() => list.data?.items ?? [], [list.data])
  const total = list.data?.total ?? 0
  const totalPages = Math.max(1, Math.ceil(total / size))
  const hasFilter = filters.search !== '' || filters.scenario !== '' || filters.general || filters.source !== ''

  // 范围选项：通用 + 全部现存场景（场景是知识的归属，没有条目也列出，计数取服务端分面）+ 已删除
  // 但仍有知识的场景；当前选中的即使都不在其中也保留
  const scopeOptions = useMemo(() => {
    const out: CorpusScenarioRef[] = (scenarios.data ?? []).map((s) => ({ code: s.code, name: s.name, deleted: false }))
    for (const f of facets?.scenarios ?? []) {
      if (f.scenario && !out.some((s) => s.code === f.scenario?.code)) out.push(f.scenario)
    }
    if (filters.scenario && !out.some((s) => s.code === filters.scenario)) {
      out.push({ code: filters.scenario, name: '', deleted: true })
    }
    return out
  }, [scenarios.data, facets, filters.scenario])

  const openEntry = (id: string) => {
    setDialog('')
    select(id)
  }

  return (
    <div className="flex h-full min-h-0 flex-col">
      <PageHeader title="知识库">
        <ModeChip status={status} />
        {/* 窄屏只留图标（页头固定一行） */}
        <Button variant="secondary" aria-label="导入" onClick={() => setDialog('import')}>
          <FileUp />
          <span className="hidden md:inline">导入</span>
        </Button>
        <Button aria-label="录入" onClick={() => setDialog('create')}>
          <Plus />
          <span className="hidden md:inline">录入</span>
        </Button>
      </PageHeader>

      <Tabs.Root value={view} onValueChange={setView} className="flex min-h-0 flex-1 flex-col">
        <Tabs.List aria-label="知识库视图" className="flex flex-shrink-0 gap-1 border-b border-border px-6">
          <Tabs.Trigger value="" className={TAB_TRIGGER}>
            知识条目
          </Tabs.Trigger>
          <Tabs.Trigger value={VIEW_RETRIEVAL} className={TAB_TRIGGER}>
            检索测试
          </Tabs.Trigger>
        </Tabs.List>

        <Tabs.Content value="" className="flex min-h-0 flex-1 flex-col data-[state=inactive]:hidden">
          <FilterBar
            label="知识筛选"
            canReset={hasFilter}
            onReset={() => {
              resetFilters()
              setQuery('')
            }}
            trailing={
              <PaginationToolbar
                page={page}
                pageSize={size}
                total={total}
                totalPages={totalPages}
                onPageChange={setPage}
                onPageSizeChange={setSize}
                loading={list.isFetching}
              />
            }
          >
            <Select
              value={scopeValue(filters)}
              onChange={(e) => {
                const v = e.target.value
                setFilters({
                  ...filters,
                  general: v === GENERAL,
                  scenario: v === GENERAL ? '' : v,
                })
              }}
              className="w-44"
              aria-label="按适用范围筛选"
            >
              <option value="">全部范围</option>
              <option value={GENERAL}>
                通用
                {facetCount(facets, (f) => f.scenarios.find((s) => !s.scenario)?.count)}
              </option>
              {scopeOptions.map((s) => (
                <option key={s.code} value={s.code}>
                  {corpusScopeLabel(s)}
                  {facetCount(facets, (f) => f.scenarios.find((x) => x.scenario?.code === s.code)?.count)}
                </option>
              ))}
            </Select>

            <Select
              value={filters.source}
              onChange={(e) => setFilters({ ...filters, source: e.target.value })}
              className="w-36"
              aria-label="按来源筛选"
            >
              <option value="">全部来源</option>
              {CORPUS_SOURCE_OPTIONS.map((o) => (
                <option key={o.value} value={o.value}>
                  {o.label}
                  {facetCount(facets, (f) => f.sources.find((s) => s.source === o.value)?.count)}
                </option>
              ))}
            </Select>

            <SearchInput
              className="w-64"
              value={query}
              onChange={setQuery}
              placeholder="搜索标题与正文…"
              ariaLabel="搜索知识的标题与正文"
            />
          </FilterBar>

          <div className="min-h-0 flex-1 overflow-y-auto px-6 py-4">
            {imported && (
              <div
                role="status"
                className="mb-3 flex items-center justify-between gap-3 rounded-lg border border-border bg-surface-2 px-3 py-2 text-sm text-text-secondary"
              >
                <span>
                  导入完成：新增 {imported.imported} 条
                  {imported.duplicates > 0 && `，${imported.duplicates} 条与已有正文相同已跳过`}
                </span>
                <button
                  type="button"
                  aria-label="关闭导入结果"
                  onClick={() => setImported(null)}
                  className="rounded-md p-1 text-text-tertiary hover:bg-surface-3 hover:text-text"
                >
                  <X className="h-4 w-4" />
                </button>
              </div>
            )}
            {list.isError ? (
              <ErrorState
                message={list.error instanceof Error ? list.error.message : undefined}
                onRetry={() => void list.refetch()}
              />
            ) : (
              <DataTable
                ariaLabel="知识列表"
                rows={rows}
                columns={corpusColumns as never}
                loading={list.isPending || (list.isFetching && rows.length === 0)}
                hasFilter={hasFilter}
                emptyNoun="知识"
                emptyHint="任务自然完成后沉淀的经验，以及录入、导入的知识会出现在这里。"
                getRowId={(row) => row.id}
                selectedId={selected || null}
                onRowClick={(row) => select(row.id)}
                rowAriaLabel={(row) => `查看知识：${row.title}`}
              />
            )}
          </div>
        </Tabs.Content>

        <Tabs.Content value={VIEW_RETRIEVAL} className="flex min-h-0 flex-1 flex-col data-[state=inactive]:hidden">
          {view === VIEW_RETRIEVAL && (
            <CorpusRetrieval selectedId={selected} onOpenEntry={openEntry} onHits={setHits} />
          )}
        </Tabs.Content>
      </Tabs.Root>

      <CorpusDrawer
        id={selected}
        placeholder={rows.find((r) => r.id === selected) ?? hits.find((h) => h.id === selected) ?? null}
        semantic={status?.mode === 'semantic'}
        onClose={() => select('')}
      />
      <CorpusCreateDialog
        open={dialog === 'create'}
        onOpenChange={(o) => setDialog(o ? 'create' : '')}
        onOpenEntry={openEntry}
      />
      <CorpusImportDialog
        open={dialog === 'import'}
        onOpenChange={(o) => setDialog(o ? 'import' : '')}
        onImported={setImported}
      />
    </div>
  )
}
