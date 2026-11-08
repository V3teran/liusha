import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { get, post } from './client'

// fetch 污染治理：本文件多处替换 globalThis.fetch（甚至置 undefined）——泄漏会让
// 同一次全量运行中排在本文件之后的页面测试全部打到残留 mock 上。每个用例后
// 强制还原原生 fetch。
const nativeFetch = globalThis.fetch
afterEach(() => {
  ;(globalThis as any).fetch = nativeFetch
  vi.clearAllMocks()
})

describe('API 客户端内核', () => {
  beforeEach(() => {
    ;(globalThis as any).fetch = vi.fn()
  })

  afterEach(() => {
    ;(globalThis as any).fetch = undefined
  })

  it('get 拼接 /api 前缀并返回 JSON', async () => {
    const mockFetch = vi.fn().mockResolvedValue({ ok: true, json: vi.fn().mockResolvedValue({ a: 1 }) })
    ;(globalThis as any).fetch = mockFetch

    await expect(get('/findings')).resolves.toEqual({ a: 1 })
    expect(mockFetch).toHaveBeenCalledWith('/api/findings')
  })

  it('post 带 JSON body 与 Content-Type', async () => {
    const mockFetch = vi.fn().mockResolvedValue({ ok: true, json: vi.fn().mockResolvedValue({ ok: true }) })
    ;(globalThis as any).fetch = mockFetch

    await post('/tasks', { brief: 'x' })

    expect(mockFetch).toHaveBeenCalledWith('/api/tasks', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ brief: 'x' }),
    })
  })

  it('post 无 body 时不设 Content-Type', async () => {
    const mockFetch = vi.fn().mockResolvedValue({ ok: true, json: vi.fn().mockResolvedValue({ ok: true }) })
    ;(globalThis as any).fetch = mockFetch

    await post('/conversations/c1/abort')

    expect(mockFetch).toHaveBeenCalledWith('/api/conversations/c1/abort', {
      method: 'POST',
      headers: undefined,
      body: undefined,
    })
  })

  it('错误响应透传后端中文 error 体', async () => {
    ;(globalThis as any).fetch = vi.fn().mockResolvedValue({
      ok: false,
      status: 500,
      json: vi.fn().mockResolvedValue({ error: '内部错误' }),
    })

    await expect(get('/findings')).rejects.toThrow('GET 500: 内部错误')
  })

  it('错误体非 JSON 时回退状态码文本', async () => {
    ;(globalThis as any).fetch = vi.fn().mockResolvedValue({
      ok: false,
      status: 502,
      json: vi.fn().mockRejectedValue(new Error('not json')),
    })

    await expect(get('/findings')).rejects.toThrow('GET 502')
  })

  it('业务请求 401 抛错并触发登录页跳转', async () => {
    // jsdom 未实现导航：替换 location.assign 为可断言的 spy。
    const assign = vi.fn()
    const originalDescriptor = Object.getOwnPropertyDescriptor(window, 'location')
    Object.defineProperty(window, 'location', {
      configurable: true,
      value: { ...window.location, pathname: '/tasks', assign },
    })

    try {
      ;(globalThis as any).fetch = vi.fn().mockResolvedValue({
        ok: false,
        status: 401,
        json: vi.fn().mockResolvedValue({ error: '未登录或会话已过期' }),
      })

      await expect(get('/findings')).rejects.toThrow('GET 401: 未登录或会话已过期')
      expect(assign).toHaveBeenCalledWith('/login')
    } finally {
      if (originalDescriptor) Object.defineProperty(window, 'location', originalDescriptor)
    }
  })

  it('/auth 域请求 401 只抛错不跳转（登录失败不得被重定向打断）', async () => {
    const assign = vi.fn()
    const originalDescriptor = Object.getOwnPropertyDescriptor(window, 'location')
    Object.defineProperty(window, 'location', {
      configurable: true,
      value: { ...window.location, pathname: '/login', assign },
    })

    try {
      ;(globalThis as any).fetch = vi.fn().mockResolvedValue({
        ok: false,
        status: 401,
        json: vi.fn().mockResolvedValue({ error: '用户名或密码错误' }),
      })

      await expect(post('/auth/login', { username: 'a', password: 'b' })).rejects.toThrow(
        'POST 401: 用户名或密码错误',
      )
      expect(assign).not.toHaveBeenCalled()
    } finally {
      if (originalDescriptor) Object.defineProperty(window, 'location', originalDescriptor)
    }
  })
})
