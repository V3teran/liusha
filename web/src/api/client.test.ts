import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { getApiKey, setApiKey, bootstrapApiKey } from './client'

// fetch 污染治理：本文件多处替换 global.fetch（甚至置 undefined）——泄漏会让
// 同一次全量运行中排在本文件之后的页面测试全部打到残留 mock 上。每个用例后
// 强制还原原生 fetch。
const nativeFetch = global.fetch
afterEach(() => {
  ;(global as any).fetch = nativeFetch
})

describe('API 客户端内核', () => {
  beforeEach(() => {
    // 清理 sessionStorage
    sessionStorage.clear()
    // Mock fetch
    ;(global as any).fetch = vi.fn()
  })

  afterEach(() => {
    vi.clearAllMocks()
    ;(global as any).fetch = undefined
  })

  describe('API key 管理', () => {
    it('设置和获取 API key', () => {
      setApiKey('test-key-123')
      expect(getApiKey()).toBe('test-key-123')
    })

    it('初始时返回空字符串', () => {
      expect(getApiKey()).toBe('')
    })
  })

  describe('bootstrapApiKey', () => {
    it('已有 key 时不发请求', async () => {
      setApiKey('existing-key')
      const mockFetch = vi.fn()
      ;(global as any).fetch = mockFetch

      await bootstrapApiKey()

      expect(mockFetch).not.toHaveBeenCalled()
    })

    it('无 key 时拉 dev-config.json 并写入', async () => {
      const mockFetch = vi.fn().mockResolvedValue({ ok: true, json: vi.fn().mockResolvedValue({ api_key: 'dev-key' }) })
      ;(global as any).fetch = mockFetch

      await bootstrapApiKey()

      expect(getApiKey()).toBe('dev-key')
    })

    it('dev-config.json 不可达时静默失败，不抛错', async () => {
      ;(global as any).fetch = vi.fn().mockRejectedValue(new Error('network down'))

      await expect(bootstrapApiKey()).resolves.toBeUndefined()
      expect(getApiKey()).toBe('')
    })

    it('dev-config.json 返回非 ok 时静默跳过', async () => {
      ;(global as any).fetch = vi.fn().mockResolvedValue({ ok: false, status: 404 })

      await expect(bootstrapApiKey()).resolves.toBeUndefined()
      expect(getApiKey()).toBe('')
    })
  })
})
