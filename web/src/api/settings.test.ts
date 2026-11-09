import { describe, expect, it, vi, beforeEach } from 'vitest'
import { getRuntimeSettings, getSettings, patchSettings, resetSettings } from './settings'

// 假 fetch：断言 settings.ts 打的 URL/method/body 与后端 /settings/:group 契约一致。
function mockFetch(status: number, json: unknown) {
  const fn = vi.fn().mockResolvedValue({
    ok: status >= 200 && status < 300,
    status,
    json: () => Promise.resolve(json),
  })
  vi.stubGlobal('fetch', fn)
  return fn
}

const detail = {
  values: { run_tail_bytes: 8192 },
  defaults: { run_tail_bytes: 8192 },
  overrides: {},
}

describe('settings API 客户端', () => {
  beforeEach(() => {
    vi.unstubAllGlobals()
  })

  it('getSettings 打 /settings/:group（路由段即分组取值）', async () => {
    const fn = mockFetch(200, detail)
    const d = await getSettings('proxy_filter')
    expect(fn.mock.calls[0][0]).toBe('/api/settings/proxy_filter')
    expect(d.overrides).toEqual({})
  })

  it('patchSettings PATCH 只含改动字段，名单为 {on, off}', async () => {
    const fn = mockFetch(200, detail)
    await patchSettings('proxy_filter', { exclude_hosts: { on: ['cdn.example'], off: [] }, max_request_body_size: 1024 })
    const [url, init] = fn.mock.calls[0]
    expect(url).toBe('/api/settings/proxy_filter')
    expect(init.method).toBe('PATCH')
    expect(JSON.parse(init.body)).toEqual({ exclude_hosts: { on: ['cdn.example'], off: [] }, max_request_body_size: 1024 })
  })

  it('resetSettings DELETE …/overrides[/:field]', async () => {
    const fn = mockFetch(200, detail)
    await resetSettings('runtime', 'run_tail_bytes')
    expect(fn.mock.calls[0][0]).toBe('/api/settings/runtime/overrides/run_tail_bytes')
    expect(fn.mock.calls[0][1].method).toBe('DELETE')
  })

  it('getRuntimeSettings 返回生效值（审计页超时判定同源）', async () => {
    mockFetch(200, detail)
    expect(await getRuntimeSettings()).toEqual({ run_tail_bytes: 8192 })
  })
})
