import { afterEach, describe, expect, it, vi } from 'vitest'
import {
  getLLMInvocationsFacets,
  getLLMInvocationsStat,
  getLLMInvocationDetail,
  listLLMInvocations,
} from './modelCalls'

// 假 fetch：断言 modelCalls.ts 打的 URL 与后端 /llm/invocations 的契约一致——列表就是资源本身，
// 统计与分面是它的两个侧面，单条是 /:id；文本筛选参数叫 search。
function mockFetch(json: unknown) {
  const fn = vi.fn().mockResolvedValue({ ok: true, status: 200, json: () => Promise.resolve(json) })
  vi.stubGlobal('fetch', fn)
  return fn
}

const calledURL = (fn: ReturnType<typeof vi.fn>) => fn.mock.calls[0][0] as string

afterEach(() => vi.unstubAllGlobals())

describe('modelCalls API 客户端', () => {
  it('列表打 GET /llm/invocations，文本筛选参数是 search', async () => {
    const fn = mockFetch({ items: [], total: 0, page: 1, size: 50 })
    await listLLMInvocations('conv-1', 2, 50, { role: 'planner', task: 't-1' })
    const url = new URL(calledURL(fn), 'http://x')
    expect(url.pathname).toBe('/api/llm/invocations')
    expect(url.searchParams.get('search')).toBe('conv-1')
    expect(url.searchParams.get('role')).toBe('planner')
    expect(url.searchParams.get('task')).toBe('t-1')
    expect(url.searchParams.get('page')).toBe('2')
    expect(url.searchParams.has('q')).toBe(false)
  })

  it('统计与分面是列表的两个侧面，吃同一套筛选参数', async () => {
    const fn = mockFetch({ calls: 3 })
    await getLLMInvocationsStat('conv-1', { onlyErr: true })
    const stat = new URL(calledURL(fn), 'http://x')
    expect(stat.pathname).toBe('/api/llm/invocations/stat')
    expect(stat.searchParams.get('search')).toBe('conv-1')
    expect(stat.searchParams.get('only_err')).toBe('1')

    const facets = mockFetch({ roles: ['planner'], models: ['m'] })
    await getLLMInvocationsFacets('')
    expect(new URL(calledURL(facets), 'http://x').pathname).toBe('/api/llm/invocations/facets')
  })

  it('单条是 /llm/invocations/:id', async () => {
    const fn = mockFetch({ id: 7 })
    await getLLMInvocationDetail(7)
    expect(calledURL(fn)).toBe('/api/llm/invocations/7')
  })
})
