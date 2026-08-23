import { describe, expect, it, vi, beforeEach } from 'vitest'
import { saveScenario, saveAgent, deleteAgent, listTools, listToolCandidates, getTool, assignTool } from './config'
import { setApiKey } from './client'
import type { ScenarioConfig, AgentConfig } from './types'

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

const SC: ScenarioConfig = {
  id: '',
  code: 'web_app',
  name: 'Web',
  description: '',
  instruction: 'I',
  engine: 'swarm',
  solo_agent_id: '',
  enabled: true,
}

describe('config API 客户端', () => {
  beforeEach(() => {
    setApiKey('k')
    vi.unstubAllGlobals()
  })

  it('saveScenario 无 id 时 POST，全字段进 body', async () => {
    const fn = mockFetch(200, { scenario: { ...SC, id: 's-new' } })
    await saveScenario(SC)
    const [url, init] = fn.mock.calls[0]
    expect(url).toBe('/api/scenarios')
    expect(init.method).toBe('POST')
    expect(JSON.parse(init.body)).toEqual({
      code: 'web_app',
      name: 'Web',
      description: '',
      instruction: 'I',
      engine: 'swarm',
      solo_agent_id: '',
      enabled: true,
    })
  })

  it('saveScenario solo 引擎透传 solo_agent_id', async () => {
    const fn = mockFetch(200, { scenario: { id: 's2' } })
    await saveScenario({ ...SC, engine: 'solo', solo_agent_id: 'h-recon' })
    expect(JSON.parse(fn.mock.calls[0][1].body).solo_agent_id).toBe('h-recon')
  })

  it('saveScenario 有 id 时 PUT /scenarios/:id', async () => {
    const fn = mockFetch(200, { scenario: { ...SC, id: 's1' } })
    await saveScenario({ ...SC, id: 's1' })
    const [url, init] = fn.mock.calls[0]
    expect(url).toBe('/api/scenarios/s1')
    expect(init.method).toBe('PUT')
  })

  it('saveAgent 传 kind/tools/cli_tools/max_iterations', async () => {
    const fn = mockFetch(200, { agent: { id: 'h1' } })
    const h: AgentConfig = {
      id: '',
      code: 'recon',
      kind: 'domain',
      name: '侦察',
      description: '',
      body: 'B',
      function_tools: ['http_get'],
      cli_tools: ['nmap', 'nuclei'],
      max_iterations: 12,
      enabled: true,
    }
    await saveAgent(h)
    const body = JSON.parse(fn.mock.calls[0][1].body)
    expect(body.kind).toBe('domain')
    expect(body.function_tools).toEqual(['http_get'])
    expect(body.cli_tools).toEqual(['nmap', 'nuclei'])
    expect(body.max_iterations).toBe(12)
  })

  it('deleteAgent 遇 409 抛后端中文 error', async () => {
    mockFetch(409, { error: '该智能体仍被场景引用（solo 场景执行猎手），请先解除引用再删除' })
    await expect(deleteAgent('h-1')).rejects.toThrow('该智能体仍被场景引用')
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
    const fn = mockFetch(200, { tools: [{ name: 'read_findings', kind: 'function', category: 'findings', description: 'd', sort_order: 0 }], total: 1 })
    const tools = await listToolCandidates('function')
    expect(new URLSearchParams((fn.mock.calls[0][0] as string).split('?')[1]).get('kind')).toBe('function')
    expect(tools).toHaveLength(1)
    expect(tools[0].name).toBe('read_findings')
  })

  it('getTool 打 /tools/:name 并对名字做 URL 编码', async () => {
    const fn = mockFetch(200, { tool: { name: 'run_command' }, agents: [] })
    await getTool('run_command')
    expect(fn.mock.calls[0][0]).toBe('/api/tools/run_command')
  })

  it('assignTool 打 PUT /tools/:name/agents/:code 带 involved', async () => {
    const fn = mockFetch(200, { code: 'recon', involved: true })
    await assignTool('write_finding', 'recon', true)
    const [url, init] = fn.mock.calls[0]
    expect(url).toBe('/api/tools/write_finding/agents/recon')
    expect(init.method).toBe('PUT')
    expect(JSON.parse(init.body).involved).toBe(true)
  })
})
