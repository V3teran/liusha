import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { getApiKey, setApiKey, listRoles, listConversations, listMessages, startChat } from './client'

describe('API 客户端', () => {
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

  describe('HTTP 请求', () => {
    it('listRoles 添加 X-API-Key header', async () => {
      setApiKey('my-key')
      const mockFetch = vi.fn().mockResolvedValue({
        ok: true,
        json: vi.fn().mockResolvedValue({ roles: [{ id: 'r1', name: 'role1', description: 'desc', mode: 'active' }] }),
      })
      ;(global as any).fetch = mockFetch

      await listRoles()

      expect(mockFetch).toHaveBeenCalledWith('/api/roles', {
        headers: { 'X-API-Key': 'my-key' },
      })
    })

    it('listConversations 返回对话列表', async () => {
      const mockFetch = vi.fn().mockResolvedValue({
        ok: true,
        json: vi.fn().mockResolvedValue({
          conversations: [{ ID: 'c1', Title: 'scan1', ScanID: 's1', RoleID: 'r1', Status: 'running', CreatedAt: '2026-06-10T00:00:00Z', UpdatedAt: '2026-06-10T00:00:00Z' }],
        }),
      })
      ;(global as any).fetch = mockFetch

      const result = await listConversations()

      expect(result).toHaveLength(1)
      expect(result[0].ID).toBe('c1')
    })

    it('listMessages 接受 afterSeq 参数', async () => {
      const mockFetch = vi.fn().mockResolvedValue({
        ok: true,
        json: vi.fn().mockResolvedValue({
          messages: [{ Seq: 1, ID: 'm1', ConversationID: 'c1', Role: 'user', Kind: 'message', Content: 'hi', Metadata: null, CreatedAt: '2026-06-10T00:00:00Z' }],
        }),
      })
      ;(global as any).fetch = mockFetch

      await listMessages('c1', 100)

      expect(mockFetch).toHaveBeenCalledWith('/api/conversations/c1/messages?after_seq=100', {
        headers: { 'X-API-Key': '' },
      })
    })

    it('startChat 发送 POST 并返回 conversation_id', async () => {
      setApiKey('my-key')
      const mockFetch = vi.fn().mockResolvedValue({
        ok: true,
        json: vi.fn().mockResolvedValue({ conversation_id: 'conv-123', scan_id: 'scan-456' }),
      })
      ;(global as any).fetch = mockFetch

      const result = await startChat('scan target', 'r1')

      expect(result.conversation_id).toBe('conv-123')
      expect(result.scan_id).toBe('scan-456')
      expect(mockFetch).toHaveBeenCalledWith('/api/chat', {
        method: 'POST',
        headers: {
          'X-API-Key': 'my-key',
          'Content-Type': 'application/json',
        },
        body: JSON.stringify({ brief: 'scan target', role_id: 'r1' }),
      })
    })

    it('请求失败时抛出错误', async () => {
      const mockFetch = vi.fn().mockResolvedValue({ ok: false, status: 401 })
      ;(global as any).fetch = mockFetch

      await expect(listRoles()).rejects.toThrow('GET /roles → 401')
    })
  })
})
