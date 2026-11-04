import { describe, expect, it, vi, beforeEach, afterEach } from 'vitest'
import { login, logout, me, changePassword } from './auth'

// fetch 污染治理：每个用例后强制还原原生 fetch（对齐 client.test.ts 的口径）。
const nativeFetch = global.fetch
afterEach(() => {
  ;(global as any).fetch = nativeFetch
  vi.clearAllMocks()
})

function mockJSON(status: number, json: unknown) {
  return vi.fn().mockResolvedValue({
    ok: status >= 200 && status < 300,
    status,
    json: () => Promise.resolve(json),
  })
}

describe('登录认证域 API', () => {
  beforeEach(() => {
    ;(global as any).fetch = vi.fn()
  })

  it('login POST /auth/login', async () => {
    const fn = mockJSON(200, { username: 'admin' })
    ;(global as any).fetch = fn

    await login('admin', 'secret-pass')
    expect(fn).toHaveBeenCalledWith('/api/auth/login', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ username: 'admin', password: 'secret-pass' }),
    })
  })

  it('logout POST /auth/logout（幂等）', async () => {
    const fn = mockJSON(200, { ok: true })
    ;(global as any).fetch = fn

    await logout()
    expect(fn).toHaveBeenCalledWith('/api/auth/logout', {
      method: 'POST',
      headers: undefined,
      body: undefined,
    })
  })

  it('me GET /auth/me', async () => {
    const fn = mockJSON(200, { username: 'tester' })
    ;(global as any).fetch = fn

    await expect(me()).resolves.toEqual({ username: 'tester' })
    expect(fn).toHaveBeenCalledWith('/api/auth/me')
  })

  it('changePassword POST snake_case 字段', async () => {
    const fn = mockJSON(200, { ok: true })
    ;(global as any).fetch = fn

    await changePassword('old-pass-x', 'brand-new-pass')
    expect(fn).toHaveBeenCalledWith('/api/auth/password', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ current_password: 'old-pass-x', new_password: 'brand-new-pass' }),
    })
  })

  it('登录失败透传后端错误体', async () => {
    ;(global as any).fetch = mockJSON(401, { error: '用户名或密码错误' })

    await expect(login('admin', 'wrong')).rejects.toThrow('POST 401: 用户名或密码错误')
  })
})
