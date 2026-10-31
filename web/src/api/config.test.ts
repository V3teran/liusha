import { describe, expect, it, vi, beforeEach } from 'vitest'
import { saveAgent, listTools, listToolCandidates, getTool, assignTool } from './config'
import { setApiKey } from './client'

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
    setApiKey('k')
    vi.unstubAllGlobals()
  })

  it('saveAgent PUT 部分更新：URL id 定位、body 即纯变更集', async () => {
    const fn = mockFetch(200, { agent: { id: 'h1' } })
    await saveAgent('h1', {
      system_prompt: 'SP',
      description: '侦察排头兵，负责目标测绘',
      function_tools: ['http_request'],
      max_iterations: 12,
      complexity: 'medium',
      skills: ['dom-xss', 'bac'],
    })
    const [url, init] = fn.mock.calls[0]
    expect(url).toBe('/api/agents/h1')
    expect(init.method).toBe('PUT')
    const body = JSON.parse(init.body)
    // 键名与后端 agentUpdate 契约严格一致（system_prompt/complexity/skills；kind 已删除）。
    expect(body.kind).toBeUndefined()
    // 目标行由 URL id 权威定位：body 即纯变更集，无任何寻址字段。
    expect(body.code).toBeUndefined()
    expect(body.system_prompt).toBe('SP')
    expect(body.description).toBe('侦察排头兵，负责目标测绘')
    expect(body.complexity).toBe('medium')
    expect(body.skills).toEqual(['dom-xss', 'bac'])
    expect(body.function_tools).toEqual(['http_request'])
    expect(body.max_iterations).toBe(12)
    // 未提及字段不出现在 body——后端部分更新语义下不被零值覆盖。
    expect(body.cli_tools).toBeUndefined()
    expect(body.name).toBeUndefined()
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
    const fn = mockFetch(200, { tools: [{ name: 'read_insights', kind: 'function', category: 'findings', description: 'd', sort_order: 0 }], total: 1 })
    const tools = await listToolCandidates('function')
    expect(new URLSearchParams((fn.mock.calls[0][0] as string).split('?')[1]).get('kind')).toBe('function')
    expect(tools).toHaveLength(1)
    expect(tools[0].name).toBe('read_insights')
  })

  it('getTool 打 /tools/:name 并对名字做 URL 编码', async () => {
    const fn = mockFetch(200, { tool: { name: 'run_command' }, agents: [] })
    await getTool('run_command')
    expect(fn.mock.calls[0][0]).toBe('/api/tools/run_command')
  })

  it('assignTool 打 PUT /tools/:name/agents/:code 带 involved', async () => {
    const fn = mockFetch(200, { code: 'recon', involved: true })
    await assignTool('write_insight', 'recon', true)
    const [url, init] = fn.mock.calls[0]
    expect(url).toBe('/api/tools/write_insight/agents/recon')
    expect(init.method).toBe('PUT')
    expect(JSON.parse(init.body).involved).toBe(true)
  })
})
