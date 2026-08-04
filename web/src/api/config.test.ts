import { describe, expect, it, vi, beforeEach } from 'vitest'
import { saveScenario, savePlaybook, deletePlaybook, saveHunter } from './config'
import { setApiKey } from './client'
import type { ScenarioConfig, PlaybookConfig, HunterConfig } from './types'

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
  playbook_id: 'pb-1',
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
      playbook_id: 'pb-1',
      enabled: true,
    })
  })

  it('saveScenario 有 id 时 PUT /scenarios/:id', async () => {
    const fn = mockFetch(200, { scenario: { ...SC, id: 's1' } })
    await saveScenario({ ...SC, id: 's1' })
    const [url, init] = fn.mock.calls[0]
    expect(url).toBe('/api/scenarios/s1')
    expect(init.method).toBe('PUT')
  })

  it('savePlaybook 把有序 hunterIDs 放进 body.hunters', async () => {
    const fn = mockFetch(200, { playbook: { id: 'pb-1' } })
    const pb: PlaybookConfig = { id: 'pb-1', code: 'c', name: 'n', description: '', enabled: true }
    await savePlaybook(pb, ['h-a', 'h-b'])
    const [url, init] = fn.mock.calls[0]
    expect(url).toBe('/api/playbooks/pb-1')
    expect(JSON.parse(init.body).hunters).toEqual(['h-a', 'h-b'])
  })

  it('saveHunter 传 kind/tools/max_iterations', async () => {
    const fn = mockFetch(200, { hunter: { id: 'h1' } })
    const h: HunterConfig = {
      id: '',
      code: 'recon',
      kind: 'domain',
      name: '侦察',
      description: '',
      body: 'B',
      tools: ['http_get'],
      max_iterations: 12,
      enabled: true,
    }
    await saveHunter(h)
    const body = JSON.parse(fn.mock.calls[0][1].body)
    expect(body.kind).toBe('domain')
    expect(body.tools).toEqual(['http_get'])
    expect(body.max_iterations).toBe(12)
  })

  it('deletePlaybook 遇 409 抛后端中文 error', async () => {
    mockFetch(409, { error: '该剧本仍被场景引用，请先解除引用再删除' })
    await expect(deletePlaybook('pb-1')).rejects.toThrow('该剧本仍被场景引用')
  })
})
