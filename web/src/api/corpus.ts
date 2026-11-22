/**
 * 知识库域 API 客户端（/corpus 系列）。
 *
 * 知识条目的场景归属：null = 通用（任何任务可见），否则是一个场景（deleted = 场景已被删除）。
 * 检索测试与 agent 的 search_corpus 是同一次检索，以某个场景下的任务视角执行。
 */

import { API_BASE, del, get, patch, post, raise } from './client'

export type CorpusSource = 'agent' | 'expert'
export type CorpusMode = 'semantic' | 'lexical'

export interface CorpusScenarioRef {
  code: string
  name: string
  deleted: boolean
}

export interface CorpusEntry {
  id: string
  title: string
  content: string
  scenario: CorpusScenarioRef | null
  source: CorpusSource
  /** 已有向量（语义召回可见）；向量化失败的条目由后台补齐 */
  indexed: boolean
  confirmations: number
  created_at: string
  updated_at: string
}

export interface CorpusConfirmation {
  task_id: string
  task_goal: string
  confirmed_at: string
}

export interface CorpusDetail {
  entry: CorpusEntry
  confirmations: CorpusConfirmation[]
}

/** 列表筛选：scenario 与 general 互斥（只看某场景 / 只看通用），都空 = 不按场景筛选。 */
export interface CorpusFilters {
  search: string
  scenario: string
  general: boolean
  source: string
}

export interface CorpusFacets {
  scenarios: { scenario: CorpusScenarioRef | null; count: number }[]
  sources: { source: CorpusSource; count: number }[]
}

export interface CorpusStatus {
  mode: CorpusMode
  /** 非空 = 语义检索不可用且非「未配置」（如密文解不开），据此区分「去配置」与「配置有问题」 */
  unavailable?: string
  embedding_model: string
  rerank_model: string
  total: number
  /** 尚无向量、等待补齐的条目数 */
  pending: number
}

export interface CorpusSearchResult {
  mode: CorpusMode
  /** 非空 = 语义检索出了故障、本次退回字面检索，值为原因 */
  fallback: string
  /** 本次检索方式的得分门槛：语义检索是重排相关度，字面检索是词元覆盖率 */
  threshold: number
  hits: { entry: CorpusEntry; score: number }[]
}

/** 新建与修改的字段；scenario 为 null 即通用。 */
export interface CorpusDraft {
  title: string
  content: string
  scenario: string | null
}

function filterParams(f: CorpusFilters): URLSearchParams {
  const p = new URLSearchParams()
  if (f.search) p.set('search', f.search)
  if (f.general) p.set('general', 'true')
  else if (f.scenario) p.set('scenario', f.scenario)
  if (f.source) p.set('source', f.source)
  return p
}

export function listCorpus(
  f: CorpusFilters,
  page: number,
  size: number,
): Promise<{ items: CorpusEntry[]; total: number }> {
  const p = filterParams(f)
  p.set('page', String(page))
  p.set('size', String(size))
  return get(`/corpus?${p}`)
}

export function getCorpusFacets(f: CorpusFilters): Promise<CorpusFacets> {
  const q = filterParams(f).toString()
  return get(`/corpus/facets${q ? '?' + q : ''}`)
}

export function getCorpusStatus(): Promise<CorpusStatus> {
  return get('/corpus/status')
}

export function getCorpusEntry(id: string): Promise<CorpusDetail> {
  return get(`/corpus/${encodeURIComponent(id)}`)
}

export function createCorpusEntry(d: CorpusDraft): Promise<CorpusEntry> {
  return post('/corpus', d)
}

/** 只提交改动的字段（scenario: null 即改为通用）。 */
export function updateCorpusEntry(id: string, changes: Partial<CorpusDraft>): Promise<CorpusEntry> {
  return patch(`/corpus/${encodeURIComponent(id)}`, changes)
}

export function deleteCorpusEntry(id: string): Promise<void> {
  return del(`/corpus/${encodeURIComponent(id)}`)
}

/** 检索测试：以 scenario 场景下的任务视角检索（'' = 无场景的任务，只见通用知识）。 */
export function searchCorpus(query: string, scenario: string): Promise<CorpusSearchResult> {
  return post('/corpus/search', { query, scenario })
}

/** 导入预览里的一条：切分出的条目，duplicate = 与已有正文或本批前面的条目相同（导入时跳过）。 */
export interface CorpusImportSection {
  file: string
  title: string
  content: string
  duplicate: boolean
}

async function postImport<T>(files: File[], fields: Record<string, string>): Promise<T> {
  const form = new FormData()
  for (const f of files) form.append('files', f)
  for (const [k, v] of Object.entries(fields)) form.append(k, v)
  const path = '/corpus/import'
  const res = await fetch(API_BASE + path, { method: 'POST', body: form })
  if (!res.ok) await raise(res, 'POST', path)
  return res.json()
}

/** 预览一批 Markdown 会被切成哪些条目（不写入）。 */
export async function previewCorpusImport(files: File[]): Promise<CorpusImportSection[]> {
  return (
    await postImport<{ sections: CorpusImportSection[] }>(files, {
      dry_run: 'true',
    })
  ).sections
}

/** 导入 Markdown：按标题切成条目，作为人工内容写入；scenario 为 null 即通用。 */
export function importCorpus(
  files: File[],
  scenario: string | null,
): Promise<{ imported: number; duplicates: number }> {
  return postImport(files, scenario ? { scenario } : {})
}
