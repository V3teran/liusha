import { describe, expect, it, vi, beforeEach } from 'vitest'
import {
  getCompactionSettings,
  saveCompactionSettings,
  getRuntimeSettings,
  saveRuntimeSettings,
  getProxyFilterSettings,
  saveProxyFilterSettings,
} from './settings'
import { setApiKey } from './client'
import type { ProxyFilterSettings } from './types'

// 假 fetch：断言 settings.ts 打的 URL/method/body 与后端 /settings 契约一致。
function mockFetch(status: number, json: unknown) {
  const fn = vi.fn().mockResolvedValue({
    ok: status >= 200 && status < 300,
    status,
    json: () => Promise.resolve(json),
  })
  vi.stubGlobal('fetch', fn)
  return fn
}

describe('settings API 客户端', () => {
  beforeEach(() => {
    setApiKey('k')
    vi.unstubAllGlobals()
  })

  it('getCompactionSettings 拆出 compaction 快照', async () => {
    const fn = mockFetch(200, {
      compaction: { trigger_ratio: 0.8, trailing_budget_ratio: 0.5, compactor_timeout_seconds: 30 },
    })
    const v = await getCompactionSettings()
    expect(v.trigger_ratio).toBe(0.8)
    expect(fn.mock.calls[0][0]).toBe('/api/settings/compaction')
  })

  it('saveCompactionSettings PUT 整组并回传', async () => {
    const body = { trigger_ratio: 0.75, trailing_budget_ratio: 0.4, compactor_timeout_seconds: 45 }
    const fn = mockFetch(200, { compaction: body })
    const v = await saveCompactionSettings(body)
    expect(v.compactor_timeout_seconds).toBe(45)
    const [url, init] = fn.mock.calls[0]
    expect(url).toBe('/api/settings/compaction')
    expect(init.method).toBe('PUT')
    expect(JSON.parse(init.body)).toEqual(body)
  })

  it('getRuntimeSettings 拆出 runtime 快照', async () => {
    mockFetch(200, {
      runtime: { step_tool_timeout_seconds: 60, run_tail_bytes: 4096, findings_limit_in_prompt: 100 },
    })
    const v = await getRuntimeSettings()
    expect(v.run_tail_bytes).toBe(4096)
  })

  it('saveRuntimeSettings PUT /settings/runtime', async () => {
    const body = { step_tool_timeout_seconds: 90, run_tail_bytes: 8192, findings_limit_in_prompt: 200 }
    const fn = mockFetch(200, { runtime: body })
    await saveRuntimeSettings(body)
    expect(fn.mock.calls[0][0]).toBe('/api/settings/runtime')
    expect(fn.mock.calls[0][1].method).toBe('PUT')
  })

  it('getProxyFilterSettings 拆出 proxy_filter 快照', async () => {
    const pf: ProxyFilterSettings = {
      allow_hosts: ['*.x.com'],
      exclude_methods: ['CONNECT'],
      exclude_hosts: [],
      exclude_upgrade_protocols: [],
      exclude_suffixes: [],
      exclude_content_types: [],
      exclude_status_codes: [304],
      max_request_body_size: 1048576,
      max_response_body_size: 1048576,
    }
    mockFetch(200, { proxy_filter: pf })
    const v = await getProxyFilterSettings()
    expect(v.allow_hosts).toEqual(['*.x.com'])
    expect(v.exclude_status_codes).toEqual([304])
  })

  it('saveProxyFilterSettings PUT /settings/proxy-filter 透传过滤规则', async () => {
    const pf: ProxyFilterSettings = {
      allow_hosts: ['*.target.com'],
      exclude_methods: ['OPTIONS'],
      exclude_hosts: [],
      exclude_upgrade_protocols: [],
      exclude_suffixes: ['.css'],
      exclude_content_types: [],
      exclude_status_codes: [204, 304],
      max_request_body_size: 2097152,
      max_response_body_size: 4194304,
    }
    const fn = mockFetch(200, { proxy_filter: pf })
    await saveProxyFilterSettings(pf)
    const [url, init] = fn.mock.calls[0]
    expect(url).toBe('/api/settings/proxy-filter')
    expect(init.method).toBe('PUT')
    expect(JSON.parse(init.body)).toEqual(pf)
  })
})
