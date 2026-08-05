import { describe, expect, it, vi, beforeEach } from 'vitest'
import { saveScenario, saveHunter, deleteHunter, listToolingTools } from './config'
import { setApiKey } from './client'
import type { ScenarioConfig, HunterConfig } from './types'

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
  domain: 'web',
  engine: 'swarm',
  solo_hunter_id: '',
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
      domain: 'web',
      engine: 'swarm',
      solo_hunter_id: '',
      enabled: true,
    })
  })

  it('saveScenario solo 引擎透传 solo_hunter_id', async () => {
    const fn = mockFetch(200, { scenario: { id: 's2' } })
    await saveScenario({ ...SC, engine: 'solo', solo_hunter_id: 'h-recon' })
    expect(JSON.parse(fn.mock.calls[0][1].body).solo_hunter_id).toBe('h-recon')
  })

  it('saveScenario 有 id 时 PUT /scenarios/:id', async () => {
    const fn = mockFetch(200, { scenario: { ...SC, id: 's1' } })
    await saveScenario({ ...SC, id: 's1' })
    const [url, init] = fn.mock.calls[0]
    expect(url).toBe('/api/scenarios/s1')
    expect(init.method).toBe('PUT')
  })

  it('saveHunter 传 kind/tools/cli_tools/max_iterations', async () => {
    const fn = mockFetch(200, { hunter: { id: 'h1' } })
    const h: HunterConfig = {
      id: '',
      code: 'recon',
      kind: 'domain',
      name: '侦察',
      description: '',
      body: 'B',
      tools: ['http_get'],
      cli_tools: ['nmap', 'nuclei'],
      max_iterations: 12,
      enabled: true,
    }
    await saveHunter(h)
    const body = JSON.parse(fn.mock.calls[0][1].body)
    expect(body.kind).toBe('domain')
    expect(body.tools).toEqual(['http_get'])
    expect(body.cli_tools).toEqual(['nmap', 'nuclei'])
    expect(body.max_iterations).toBe(12)
  })

  it('deleteHunter 遇 409 抛后端中文 error', async () => {
    mockFetch(409, { error: '该猎手仍被场景引用（solo 场景执行猎手），请先解除引用再删除' })
    await expect(deleteHunter('h-1')).rejects.toThrow('该猎手仍被场景引用')
  })

  it('listToolingTools 拆 { tools } 信封', async () => {
    mockFetch(200, { tools: [{ name: 'nmap', category: 'recon', description: '端口扫描' }] })
    const tools = await listToolingTools()
    expect(tools).toEqual([{ name: 'nmap', category: 'recon', description: '端口扫描' }])
  })
})
