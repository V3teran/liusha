import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import {
  listCredentialHosts,
  getCredentialDetail,
  saveCredentialIdentity,
  deleteCredentialIdentity,
} from './credentials'

// fetch 污染治理：每个用例后强制还原原生 fetch（对齐 client.test.ts 的口径）。
const nativeFetch = globalThis.fetch
afterEach(() => {
  ;(globalThis as any).fetch = nativeFetch
})

describe('凭证库 API', () => {
  beforeEach(() => {
    sessionStorage.clear()
    ;(globalThis as any).fetch = vi.fn()
  })

  afterEach(() => {
    vi.clearAllMocks()
    ;(globalThis as any).fetch = undefined
  })

  it('listCredentialHosts GET /credentials', async () => {
    const mockFetch = vi.fn().mockResolvedValue({
      ok: true,
      json: vi.fn().mockResolvedValue({ hosts: [{ host: 'a.com', identities: 1, sessions: 2 }] }),
    })
    ;(globalThis as any).fetch = mockFetch

    const hosts = await listCredentialHosts()

    expect(hosts[0].host).toBe('a.com')
    expect(mockFetch).toHaveBeenCalledWith('/api/credentials')
  })

  it('getCredentialDetail GET 单 host 明文详情', async () => {
    const mockFetch = vi.fn().mockResolvedValue({
      ok: true,
      json: vi.fn().mockResolvedValue({ host: 'a.com', identities: [] }),
    })
    ;(globalThis as any).fetch = mockFetch

    await getCredentialDetail('a.com')

    expect(mockFetch).toHaveBeenCalledWith('/api/credentials/a.com')
  })

  it('saveCredentialIdentity PUT 单登录身份', async () => {
    const mockFetch = vi.fn().mockResolvedValue({ ok: true, json: vi.fn().mockResolvedValue({ ok: true }) })
    ;(globalThis as any).fetch = mockFetch

    const identity = { username: 'admin', role: 'admin', credentials: [{ type: 'headers', key: 'Cookie', value: 'sid=1' }] }
    await saveCredentialIdentity('a.com:8080', identity)

    expect(mockFetch).toHaveBeenCalledWith('/api/credentials/a.com%3A8080/usernames', expect.objectContaining({
      method: 'PUT',
      body: JSON.stringify(identity),
    }))
  })

  it('deleteCredentialIdentity 编码用户名（会话用户名含冒号）', async () => {
    const mockFetch = vi.fn().mockResolvedValue({ ok: true, json: vi.fn().mockResolvedValue({ ok: true }) })
    ;(globalThis as any).fetch = mockFetch

    await deleteCredentialIdentity('a.com', 'task:t1:session')

    expect(mockFetch).toHaveBeenCalledWith(
      '/api/credentials/a.com/usernames/task%3At1%3Asession',
      expect.objectContaining({ method: 'DELETE' }),
    )
  })
})
