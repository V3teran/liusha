import { describe, expect, it, vi, beforeEach } from 'vitest'
import { patchAgent, resetAgent, deleteSkill, listTools, listToolCandidates, getTool, assignTool } from './config'

// 用假 fetch 断言 config.ts 打的 URL/method/body 与后端契约一致。
function mockFetch(status: number, json: unknown) {
  const fn = vi.fn().mockResolvedValue({
    ok: status >= 200 && status < 300,
    status,
    json: () => Promise.resolve(json),
  })
  vi.stubGlobal('fetch', fn)
  return fn
}

describe('config API 客户端', () => {
  beforeEach(() => {
    vi.unstubAllGlobals()
  })

  it('patchAgent PATCH /agents/:code：只含改动字段，名单为 {on, off}', async () => {
    const fn = mockFetch(200, { agent: { code: 'planner' } })
    await patchAgent('planner', {
      charter: '我的章程',
      complexity: 'simple',
      skills: { on: ['bac'], off: [] },
    })
    const [url, init] = fn.mock.calls[0]
    expect(url).toBe('/api/agents/planner')
    expect(init.method).toBe('PATCH')
    const body = JSON.parse(init.body)
    expect(body).toEqual({ charter: '我的章程', complexity: 'simple', skills: { on: ['bac'], off: [] } })
  })

  it('resetAgent DELETE …/overrides[/:field]', async () => {
    const fn = mockFetch(200, { agent: { code: 'planner' } })
    await resetAgent('planner', 'charter')
    expect(fn.mock.calls[0][0]).toBe('/api/agents/planner/overrides/charter')
    expect(fn.mock.calls[0][1].method).toBe('DELETE')
    await resetAgent('planner')
    expect(fn.mock.calls[1][0]).toBe('/api/agents/planner/overrides')
  })

  it('deleteSkill 处理 204 无响应体（不把成功的删除误报为失败）', async () => {
    const fn = vi.fn().mockResolvedValue({ ok: true, status: 204, json: () => Promise.reject(new Error('empty body')) })
    vi.stubGlobal('fetch', fn)
    await expect(deleteSkill('mine')).resolves.toBeUndefined()
  })

  it('listTools 分页透传 page/size/q/kind 到 query', async () => {
    const fn = mockFetch(200, { tools: [], total: 0 })
    await listTools({ page: 2, size: 12, q: 'nmap', kind: 'cli' })
    const url = fn.mock.calls[0][0] as string
    expect(url).toMatch(/^\/api\/tools\?/)
    const qs = new URLSearchParams(url.split('?')[1])
    expect(qs.get('page')).toBe('2')
    expect(qs.get('size')).toBe('12')
    expect(qs.get('q')).toBe('nmap')
    expect(qs.get('kind')).toBe('cli')
  })

  it('listTools 无参时不带 query（全量口径）', async () => {
    const fn = mockFetch(200, { tools: [], total: 0 })
    await listTools()
    expect(fn.mock.calls[0][0]).toBe('/api/tools')
  })

  it('listToolCandidates 仅按 kind 过滤、拆出 tools 数组', async () => {
    const fn = mockFetch(200, { tools: [{ name: 'read_credentials', kind: 'function', category: 'credentials', description: 'd', sort_order: 0 }], total: 1 })
    const tools = await listToolCandidates('function')
    expect(new URLSearchParams((fn.mock.calls[0][0] as string).split('?')[1]).get('kind')).toBe('function')
    expect(tools).toHaveLength(1)
    expect(tools[0].name).toBe('read_credentials')
  })

  it('getTool 打 /tools/:name 并对名字做 URL 编码', async () => {
    const fn = mockFetch(200, { tool: { name: 'run_command' }, agents: [] })
    await getTool('run_command')
    expect(fn.mock.calls[0][0]).toBe('/api/tools/run_command')
  })

  it('assignTool 打 PUT /tools/:name/agents/:code 带 involved', async () => {
    const fn = mockFetch(200, { code: 'recon', involved: true })
    await assignTool('write_credential', 'recon', true)
    const [url, init] = fn.mock.calls[0]
    expect(url).toBe('/api/tools/write_credential/agents/recon')
    expect(init.method).toBe('PUT')
    expect(JSON.parse(init.body).involved).toBe(true)
  })
})
