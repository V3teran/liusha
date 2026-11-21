import { useEffect, useId, useState } from 'react'
import { Search } from 'lucide-react'
import { errorText } from '@/api/client'
import type { CorpusEntry, CorpusSearchResult } from '@/api/corpus'
import { DataTable } from '@/components/DataTable'
import { Button } from '@/components/ui/Button'
import { INPUT_CLASS } from '@/components/ui/control'
import { corpusHitColumns } from './corpusColumns'
import { CorpusScopeSelect } from './CorpusScopeSelect'
import { useCorpusRetrievalParams, useCorpusSearch } from './useCorpusQueries'

function modeLine(r: CorpusSearchResult): string {
  if (r.fallback) return `字面检索 · 覆盖率 ≥ ${r.threshold} · 语义检索暂不可用：${r.fallback}`
  return r.mode === 'semantic' ? `语义检索 · 相关度 ≥ ${r.threshold}` : `字面检索 · 覆盖率 ≥ ${r.threshold}`
}

interface CorpusRetrievalProps {
  selectedId: string
  onOpenEntry: (id: string) => void
  /** 当前结果里的条目，供详情抽屉在详情取回前先撑住 */
  onHits: (entries: CorpusEntry[]) => void
}

/**
 * 检索测试：以某个场景下的任务视角，执行与智能体 search_corpus 相同的检索——一条查询实际会
 * 拿到哪些知识、各自的得分。查询与视角写在 URL（?q= & as=）。
 */
export function CorpusRetrieval({ selectedId, onOpenEntry, onHits }: CorpusRetrievalProps) {
  const params = useCorpusRetrievalParams()
  const [query, setQuery] = useState(params.query)
  const [scenario, setScenario] = useState<string | null>(params.scenario || null)
  const search = useCorpusSearch(params.query, params.scenario)
  const queryId = useId()
  const scopeId = useId()
  const hits = search.data?.hits ?? []

  // URL 被外部改写（前进后退）时同步表单
  useEffect(() => {
    setQuery(params.query)
    setScenario(params.scenario || null)
  }, [params.query, params.scenario])

  useEffect(() => {
    onHits(hits.map((h) => h.entry))
    // eslint-disable-next-line react-hooks/exhaustive-deps -- 只随结果变化
  }, [search.data])

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <form
        className="flex flex-shrink-0 flex-wrap items-end gap-3 border-b border-border px-6 py-3"
        onSubmit={(e) => {
          e.preventDefault()
          if (query.trim()) params.submit(query.trim(), scenario ?? '')
        }}
      >
        <div className="flex min-w-[14rem] flex-1 flex-col gap-1.5">
          <label htmlFor={queryId} className="text-xs font-medium text-text-secondary">
            查询
          </label>
          <input
            id={queryId}
            className={INPUT_CLASS}
            value={query}
            placeholder="一句话描述遇到的情境，如：接口返回的数据是编码过的，怎么处理？"
            onChange={(e) => setQuery(e.target.value)}
          />
        </div>
        <div className="flex w-44 flex-col gap-1.5">
          <label htmlFor={scopeId} className="text-xs font-medium text-text-secondary">
            任务场景
          </label>
          <CorpusScopeSelect id={scopeId} value={scenario} onChange={setScenario} emptyLabel="无场景" />
        </div>
        <Button type="submit" disabled={!query.trim() || search.isFetching}>
          <Search />
          {search.isFetching ? '检索中…' : '检索'}
        </Button>
      </form>

      <div className="min-h-0 flex-1 overflow-y-auto px-6 py-4" aria-live="polite">
        {search.isError ? (
          <p role="alert" className="py-16 text-center text-sm text-sev-critical">
            ⚠ {errorText(search.error, '检索失败')}
          </p>
        ) : search.data ? (
          <div className="flex flex-col gap-3">
            <p className="text-xs text-text-tertiary">{modeLine(search.data)}</p>
            <DataTable
              ariaLabel="检索结果"
              rows={hits}
              columns={corpusHitColumns as never}
              loading={false}
              emptyNoun="相关的知识"
              emptyHint="知识库里没有与这条查询相关的条目，智能体此时会拿到空结果。"
              getRowId={(h) => h.entry.id}
              selectedId={selectedId || null}
              onRowClick={(h) => onOpenEntry(h.entry.id)}
              rowAriaLabel={(h) => `查看知识：${h.entry.title}`}
            />
          </div>
        ) : null}
      </div>
    </div>
  )
}
