import { keepPreviousData, useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import {
  createCorpusEntry,
  deleteCorpusEntry,
  getCorpusEntry,
  getCorpusFacets,
  getCorpusStatus,
  importCorpus,
  listCorpus,
  updateCorpusEntry,
  type CorpusDraft,
  type CorpusFilters,
} from '@/api/corpus'
import type { HistoryOptions } from '@/hooks/useSelectedParam'
import { useUrlFilterFields, useUrlPageState } from '@/hooks/useUrlPageState'

// 知识库的服务端数据：键都在 ['corpus'] 前缀下，任何写入后整体失效（列表、分面、状态、详情
// 都可能随之变化：条数、印证数、待补向量数）。

const CORPUS_KEY = ['corpus'] as const

export const corpusKeys = {
  list: (f: CorpusFilters, page: number, size: number) => [...CORPUS_KEY, 'list', f, page, size] as const,
  facets: (f: CorpusFilters) => [...CORPUS_KEY, 'facets', f] as const,
  status: [...CORPUS_KEY, 'status'] as const,
  detail: (id: string) => [...CORPUS_KEY, 'detail', id] as const,
}

// 筛选写在 URL：search / scenario / general / source / page / size（打开的条目是 ?selected=）。
const FIELDS = { search: 'search', scenario: 'scenario', general: 'general', source: 'source' } as const

export interface CorpusUrlState {
  filters: CorpusFilters
  page: number
  size: number
  setFilters: (next: CorpusFilters, opts?: HistoryOptions) => void
  setPage: (page: number) => void
  setSize: (size: number) => void
  resetFilters: () => void
}

export function useCorpusFilters(): CorpusUrlState {
  const { filters: raw, setFilters: setRaw, resetFilters } = useUrlFilterFields(FIELDS)
  const { page, size, setPage, setSize } = useUrlPageState()
  const general = raw.general === 'true'
  const filters: CorpusFilters = {
    search: raw.search,
    scenario: general ? '' : raw.scenario,
    general,
    source: raw.source,
  }
  const setFilters = (next: CorpusFilters, opts?: HistoryOptions) =>
    setRaw(
      {
        search: next.search,
        scenario: next.general ? '' : next.scenario,
        general: next.general ? 'true' : '',
        source: next.source,
      },
      opts,
    )
  return { filters, page, size, setFilters, setPage, setSize, resetFilters }
}

export function useCorpusList(f: CorpusFilters, page: number, size: number) {
  return useQuery({
    queryKey: corpusKeys.list(f, page, size),
    queryFn: () => listCorpus(f, page, size),
    placeholderData: keepPreviousData,
  })
}

export function useCorpusFacets(f: CorpusFilters) {
  return useQuery({ queryKey: corpusKeys.facets(f), queryFn: () => getCorpusFacets(f), placeholderData: keepPreviousData })
}

export function useCorpusStatus() {
  return useQuery({ queryKey: corpusKeys.status, queryFn: getCorpusStatus })
}

/** 单条知识与印证过它的任务；id 为空不请求。 */
export function useCorpusDetail(id: string) {
  return useQuery({ queryKey: corpusKeys.detail(id), queryFn: () => getCorpusEntry(id), enabled: id !== '' })
}

function useInvalidateCorpus() {
  const qc = useQueryClient()
  return () => qc.invalidateQueries({ queryKey: CORPUS_KEY })
}

export function useCreateCorpusEntry() {
  const invalidate = useInvalidateCorpus()
  return useMutation({ mutationFn: (d: CorpusDraft) => createCorpusEntry(d), onSuccess: invalidate })
}

export function useUpdateCorpusEntry() {
  const invalidate = useInvalidateCorpus()
  return useMutation({
    mutationFn: ({ id, changes }: { id: string; changes: Partial<CorpusDraft> }) => updateCorpusEntry(id, changes),
    onSuccess: invalidate,
  })
}

export function useDeleteCorpusEntry() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (id: string) => deleteCorpusEntry(id),
    // 已删除条目的详情不再重取（抽屉此刻还开着，重取只会拿到 404）；其余随删除整体失效
    onSuccess: (_, id) => {
      const gone = corpusKeys.detail(id)
      return qc.invalidateQueries({
        queryKey: CORPUS_KEY,
        predicate: (q) => !(q.queryKey.length === gone.length && q.queryKey.every((k, i) => k === gone[i])),
      })
    },
  })
}

export function useImportCorpus() {
  const invalidate = useInvalidateCorpus()
  return useMutation({
    mutationFn: ({ files, scenario }: { files: File[]; scenario: string | null }) => importCorpus(files, scenario),
    onSuccess: invalidate,
  })
}
