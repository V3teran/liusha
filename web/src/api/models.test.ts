import { describe, expect, it, vi, beforeEach } from 'vitest'
import {
  listProviders,
  saveProvider,
  deleteProvider,
  getRouting,
  saveRoleRoute,
  deleteRoleRoute,
} from './models'
import { setApiKey } from './client'
import type { ProviderConfig } from './types'

// 假 fetch：断言 models.ts 打的 URL/method/body 与后端 /models 契约一致。
function mockFetch(status: number, json: unknown) {
  const fn = vi.fn().mockResolvedValue({
    ok: status >= 200 && status < 300,
    status,
    json: () => Promise.resolve(json),
  })
  vi.stubGlobal('fetch', fn)
  return fn
}

const PROV: ProviderConfig = {
  key: 'deepseek',
  type: 'openai_compat',
  base_url: 'https://api.deepseek.com',
  default_model: 'deepseek-chat',
  api_key: 'sk-test-secret',
  key_present: true,
  max_tokens: 4096,
  supports_tools: true,
  supports_vision: false,
  context_window: 65536,
  description: '',
  sort_order: 0,
  enabled: true,
}

describe('models API 客户端', () => {
  beforeEach(() => {
    setApiKey('k')
    vi.unstubAllGlobals()
  })

  it('listProviders 拆出 providers 数组', async () => {
    const fn = mockFetch(200, { providers: [PROV] })
    const rows = await listProviders()
    expect(fn.mock.calls[0][0]).toBe('/api/models/providers')
    expect(rows).toHaveLength(1)
    expect(rows[0].key).toBe('deepseek')
  })

  it('saveProvider 新建走 POST /models/providers，全连接+能力字段进 body（不含 key_present）', async () => {
    const fn = mockFetch(200, { provider: PROV })
    await saveProvider(PROV, true)
    const [url, init] = fn.mock.calls[0]
    expect(url).toBe('/api/models/providers')
    expect(init.method).toBe('POST')
    const body = JSON.parse(init.body)
    expect(body).toEqual({
      key: 'deepseek',
      type: 'openai_compat',
      base_url: 'https://api.deepseek.com',
      default_model: 'deepseek-chat',
      api_key: 'sk-test-secret',
      max_tokens: 4096,
      supports_tools: true,
      supports_vision: false,
      context_window: 65536,
      description: '',
      sort_order: 0,
      enabled: true,
    })
    // 安全：body 只带明文 api_key（走 HTTPS 一次性提交，后端加密落库），不回传 key_present。
    expect(body).not.toHaveProperty('key_present')
    expect(body.api_key).toBe('sk-test-secret')
  })

  it('saveProvider 编辑走 PUT /models/providers/:key（key 做 URL 编码）', async () => {
    const fn = mockFetch(200, { provider: PROV })
    await saveProvider({ ...PROV, key: 'az/gpt' }, false)
    const [url, init] = fn.mock.calls[0]
    expect(url).toBe('/api/models/providers/az%2Fgpt')
    expect(init.method).toBe('PUT')
  })

  it('deleteProvider 遇 409 抛后端中文 error（被角色路由引用）', async () => {
    mockFetch(409, { error: '该 provider 仍被角色路由引用，请先改绑角色再删除' })
    await expect(deleteProvider('deepseek')).rejects.toThrow('该 provider 仍被角色路由引用')
  })

  it('getRouting 打 /models/routing 返回 routes（无别名层）', async () => {
    const fn = mockFetch(200, {
      routes: [{ role: 'planner', provider_key: 'deepseek' }],
    })
    const r = await getRouting()
    expect(fn.mock.calls[0][0]).toBe('/api/models/routing')
    expect(r.routes[0].role).toBe('planner')
    expect(r.routes[0].provider_key).toBe('deepseek')
  })

  it('saveRoleRoute 打 PUT /models/routes/:role 带 provider_key', async () => {
    const fn = mockFetch(200, { route: { role: 'planner', provider_key: 'deepseek' } })
    await saveRoleRoute('planner', 'deepseek')
    const [url, init] = fn.mock.calls[0]
    expect(url).toBe('/api/models/routes/planner')
    expect(init.method).toBe('PUT')
    expect(JSON.parse(init.body)).toEqual({ provider_key: 'deepseek' })
  })

  it('saveRoleRoute 遇 409 抛后端中文 error（provider 不存在）', async () => {
    mockFetch(409, { error: 'provider_key 不存在，请先创建对应 provider' })
    await expect(saveRoleRoute('planner', 'ghost')).rejects.toThrow('provider_key 不存在')
  })

  it('deleteRoleRoute 打 DELETE /models/routes/:role', async () => {
    const fn = mockFetch(200, {})
    await deleteRoleRoute('vision')
    const [url, init] = fn.mock.calls[0]
    expect(url).toBe('/api/models/routes/vision')
    expect(init.method).toBe('DELETE')
  })

  it('所有请求都带 X-API-Key header', async () => {
    const fn = mockFetch(200, { providers: [] })
    await listProviders()
    expect(fn.mock.calls[0][1].headers['X-API-Key']).toBe('k')
  })
})
