import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import {
  listCredentials,
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

  it('listCredentials 把关键词与分页交给服务端', async () => {
    const mockFetch = vi.fn().mockResolvedValue({
      ok: true,
      json: vi.fn().mockResolvedValue({ items: [{ host: 'a.com', username: 'admin' }], total: 1 }),
    })
    ;(globalThis as any).fetch = mockFetch

    const res = await listCredentials('Cookie=a b', 2, 50)

    expect(res.total).toBe(1)
    expect(mockFetch).toHaveBeenCalledWith('/api/credentials?page=2&size=50&search=Cookie%3Da+b')
  })

  it('listCredentials 无关键词时不带 search 参数', async () => {
    const mockFetch = vi.fn().mockResolvedValue({ ok: true, json: vi.fn().mockResolvedValue({ items: [], total: 0 }) })
    ;(globalThis as any).fetch = mockFetch

    await listCredentials('', 1, 10)

    expect(mockFetch).toHaveBeenCalledWith('/api/credentials?page=1&size=10')
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
