import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import {
  getApiKey, setApiKey, listRoles, listConversations, listMessages, startChat, followUp, abortScan,
  listSessions, startActiveScan, abortSession, getSitemap, listLLMInvocations, listAgentRuns,
  listCredentials, saveCredentialsBatch, deleteCredentials,
} from './client'

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

  describe('多轮方法', () => {
    it('followUp POST 到 /conversations/:id/messages 带 content', async () => {
      setApiKey('k')
      const mockFetch = vi.fn().mockResolvedValue({
        ok: true,
        status: 200,
        json: vi.fn().mockResolvedValue({ intent: 'action', scan_id: 's1' }),
      })
      ;(global as any).fetch = mockFetch

      const result = await followUp('c1', '深挖')

      expect(mockFetch).toHaveBeenCalledWith('/api/conversations/c1/messages', expect.objectContaining({ method: 'POST' }))
      expect(result.intent).toBe('action')
    })

    it('followUp 409 抛带 busy 标记的错', async () => {
      setApiKey('k')
      const mockFetch = vi.fn().mockResolvedValue({
        ok: false,
        status: 409,
        json: vi.fn().mockResolvedValue({ busy: true }),
      })
      ;(global as any).fetch = mockFetch

      await expect(followUp('c1', 'x')).rejects.toMatchObject({ busy: true })
    })

    it('abortScan POST 到 /conversations/:id/abort', async () => {
      setApiKey('k')
      const mockFetch = vi.fn().mockResolvedValue({
        ok: true,
        status: 200,
        json: vi.fn().mockResolvedValue({ aborted: true }),
      })
      ;(global as any).fetch = mockFetch

      await abortScan('c1')

      expect(mockFetch).toHaveBeenCalledWith('/api/conversations/c1/abort', expect.objectContaining({ method: 'POST' }))
    })
  })

  describe('owner 会话 / 扫描', () => {
    it('listSessions 拆出 sessions 数组', async () => {
      ;(global as any).fetch = vi.fn().mockResolvedValue({
        ok: true,
        json: vi.fn().mockResolvedValue({
          sessions: [{ id: 'o1', scope: '{"any":true}', status: 'running', mode: 'passive', created_at: '2026-06-11T00:00:00Z' }],
        }),
      })

      const result = await listSessions()

      expect(result).toHaveLength(1)
      expect(result[0].id).toBe('o1')
    })

    it('listSessions 带 limit 拼 query', async () => {
      const mockFetch = vi.fn().mockResolvedValue({ ok: true, json: vi.fn().mockResolvedValue({ sessions: [] }) })
      ;(global as any).fetch = mockFetch

      await listSessions(20)

      expect(mockFetch).toHaveBeenCalledWith('/api/session?limit=20', { headers: { 'X-API-Key': '' } })
    })

    it('startActiveScan POST brief 返回 owner_id/hunter_id', async () => {
      setApiKey('k')
      const mockFetch = vi.fn().mockResolvedValue({
        ok: true,
        json: vi.fn().mockResolvedValue({ owner_id: 'o9', hunter_id: 'h9' }),
      })
      ;(global as any).fetch = mockFetch

      const result = await startActiveScan('测试 http://t/login admin/pass 只测 XSS')

      expect(result).toEqual({ owner_id: 'o9', hunter_id: 'h9' })
      expect(mockFetch).toHaveBeenCalledWith('/api/scan/active', expect.objectContaining({
        method: 'POST',
        body: JSON.stringify({ brief: '测试 http://t/login admin/pass 只测 XSS' }),
      }))
    })

    it('abortSession POST 到 /session/:id/abort', async () => {
      const mockFetch = vi.fn().mockResolvedValue({ ok: true, json: vi.fn().mockResolvedValue({ ok: true }) })
      ;(global as any).fetch = mockFetch

      await abortSession('o1')

      expect(mockFetch).toHaveBeenCalledWith('/api/session/o1/abort', expect.objectContaining({ method: 'POST' }))
    })
  })

  describe('攻击面 / 审计 / 任务树', () => {
    it('getSitemap 透传 SitemapView', async () => {
      ;(global as any).fetch = vi.fn().mockResolvedValue({
        ok: true,
        json: vi.fn().mockResolvedValue({ owner_id: 'o1', host: 'h', generated_at: '2026-06-11T00:00:00Z', root: null }),
      })

      const view = await getSitemap('o1')

      expect(view.owner_id).toBe('o1')
      expect(view.root).toBeNull()
    })

    it('getSitemap 带 host 拼 query', async () => {
      const mockFetch = vi.fn().mockResolvedValue({ ok: true, json: vi.fn().mockResolvedValue({}) })
      ;(global as any).fetch = mockFetch

      await getSitemap('o1', 'a.com:80')

      expect(mockFetch).toHaveBeenCalledWith('/api/sitemap/o1?host=a.com%3A80', { headers: { 'X-API-Key': '' } })
    })

    it('listLLMInvocations 透传分组响应', async () => {
      ;(global as any).fetch = vi.fn().mockResolvedValue({
        ok: true,
        json: vi.fn().mockResolvedValue({ owner_id: 'o1', total: 1, groups: [{ hunter_id: 'h1', count: 1, invocations: [] }] }),
      })

      const res = await listLLMInvocations('o1')

      expect(res.total).toBe(1)
      expect(res.groups[0].hunter_id).toBe('h1')
    })

    it('listAgentRuns 透传 runs', async () => {
      ;(global as any).fetch = vi.fn().mockResolvedValue({
        ok: true,
        json: vi.fn().mockResolvedValue({ owner_id: 'o1', total: 2, runs: [] }),
      })

      const res = await listAgentRuns('o1')

      expect(res.total).toBe(2)
    })
  })

  describe('凭证库', () => {
    it('listCredentials 按 host 拆 identities', async () => {
      const mockFetch = vi.fn().mockResolvedValue({
        ok: true,
        json: vi.fn().mockResolvedValue({ identities: [{ name: 'admin', role: 'admin', credentials: [] }] }),
      })
      ;(global as any).fetch = mockFetch

      const ids = await listCredentials('a.com')

      expect(ids[0].name).toBe('admin')
      expect(mockFetch).toHaveBeenCalledWith('/api/credential?host=a.com', { headers: { 'X-API-Key': '' } })
    })

    it('saveCredentialsBatch POST ttl + credentials', async () => {
      const mockFetch = vi.fn().mockResolvedValue({ ok: true, json: vi.fn().mockResolvedValue({ ok: true }) })
      ;(global as any).fetch = mockFetch

      await saveCredentialsBatch({ 'a.com': [{ name: 'u', role: 'user', credentials: [] }] }, 3600)

      expect(mockFetch).toHaveBeenCalledWith('/api/credential/batch', expect.objectContaining({
        method: 'POST',
        body: JSON.stringify({ ttl_seconds: 3600, credentials: { 'a.com': [{ name: 'u', role: 'user', credentials: [] }] } }),
      }))
    })

    it('deleteCredentials DELETE 带 host query', async () => {
      const mockFetch = vi.fn().mockResolvedValue({ ok: true, json: vi.fn().mockResolvedValue({ ok: true }) })
      ;(global as any).fetch = mockFetch

      await deleteCredentials('a.com')

      expect(mockFetch).toHaveBeenCalledWith('/api/credential?host=a.com', expect.objectContaining({ method: 'DELETE' }))
    })
  })
})
