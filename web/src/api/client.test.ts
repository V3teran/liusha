import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import {
  getApiKey, setApiKey, listConversations, listMessages, startChat, followUp, abortScan,
  listLLMInvocations,
  getLLMInvocationStat, getLLMInvocationFacets,
  listCredentialHosts, getCredentialDetail, saveCredentialIdentity,
  deleteCredentialIdentity, purgeCredentialSessions, deleteCredentialHost,
  deleteConversation, renameConversation, authStream, bootstrapApiKey,
  listFindings, listFindingHosts, updateFindingTriage,
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
    it('listConversations 返回会话列表 + hasMore，且带 limit/offset query', async () => {
      const mockFetch = vi.fn().mockResolvedValue({
        ok: true,
        json: vi.fn().mockResolvedValue({
          conversations: [{ ID: 'c1', Title: 'scan1', TaskID: 's1', Source: 'manual', CreatedAt: '2026-06-10T00:00:00Z', UpdatedAt: '2026-06-10T00:00:00Z' }],
          has_more: true,
        }),
      })
      ;(global as any).fetch = mockFetch

      const result = await listConversations(30, 30)

      expect(mockFetch).toHaveBeenCalledWith(
        expect.stringContaining('/conversations?limit=30&offset=30'),
        expect.anything(),
      )
      expect(result.conversations).toHaveLength(1)
    })

    it('listConversations 传 source 时带 source query（服务端过滤，保证分页边界正确）', async () => {
      const mockFetch = vi.fn().mockResolvedValue({
        ok: true,
        json: vi.fn().mockResolvedValue({ conversations: [], has_more: false }),
      })
      ;(global as any).fetch = mockFetch

      await listConversations(30, 0, 'auto')

      expect(mockFetch).toHaveBeenCalledWith(expect.stringContaining('source=auto'), expect.anything())
    })

    it('listConversations 把后端 null（Go nil slice）归一为空数组，不透传 null', async () => {
      const mockFetch = vi.fn().mockResolvedValue({
        ok: true,
        json: vi.fn().mockResolvedValue({ conversations: null, has_more: false }),
      })
      ;(global as any).fetch = mockFetch

      const result = await listConversations(30, 0, 'auto')

      expect(result.conversations).toEqual([])
      expect(result.conversations).toHaveLength(0)
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

      const result = await startChat('scan target')

      expect(result.conversation_id).toBe('conv-123')
      expect(result.scan_id).toBe('scan-456')
      expect(mockFetch).toHaveBeenCalledWith('/api/chat', {
        method: 'POST',
        headers: {
          'X-API-Key': 'my-key',
          'Content-Type': 'application/json',
        },
        body: JSON.stringify({ brief: 'scan target' }),
      })
    })

    it('请求失败时抛出错误', async () => {
      const mockFetch = vi.fn().mockResolvedValue({ ok: false, status: 401 })
      ;(global as any).fetch = mockFetch

      await expect(listConversations()).rejects.toThrow('GET /conversations?limit=30&offset=0 → 401')
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

  describe('凭证库', () => {
    it('listCredentialHosts GET /credentials', async () => {
      const mockFetch = vi.fn().mockResolvedValue({
        ok: true,
        json: vi.fn().mockResolvedValue({ hosts: [{ host: 'a.com', identities: 1, sessions: 2 }] }),
      })
      ;(global as any).fetch = mockFetch

      const hosts = await listCredentialHosts()

      expect(hosts[0].host).toBe('a.com')
      expect(mockFetch).toHaveBeenCalledWith('/api/credentials', { headers: { 'X-API-Key': '' } })
    })

    it('getCredentialDetail GET 单 host 明文详情', async () => {
      const mockFetch = vi.fn().mockResolvedValue({
        ok: true,
        json: vi.fn().mockResolvedValue({ host: 'a.com', identities: [] }),
      })
      ;(global as any).fetch = mockFetch

      await getCredentialDetail('a.com')

      expect(mockFetch).toHaveBeenCalledWith('/api/credentials/a.com', { headers: { 'X-API-Key': '' } })
    })

    it('saveCredentialIdentity PUT 单登录身份', async () => {
      const mockFetch = vi.fn().mockResolvedValue({ ok: true, json: vi.fn().mockResolvedValue({ ok: true }) })
      ;(global as any).fetch = mockFetch

      const identity = { username: 'admin', role: 'admin', credentials: [{ type: 'headers', key: 'Cookie', value: 'sid=1' }] }
      await saveCredentialIdentity('a.com:8080', identity)

      expect(mockFetch).toHaveBeenCalledWith('/api/credentials/a.com%3A8080/usernames', expect.objectContaining({
        method: 'PUT',
        body: JSON.stringify(identity),
      }))
    })

    it('deleteCredentialIdentity 编码用户名（会话用户名含冒号）', async () => {
      const mockFetch = vi.fn().mockResolvedValue({ ok: true, json: vi.fn().mockResolvedValue({ ok: true }) })
      ;(global as any).fetch = mockFetch

      await deleteCredentialIdentity('a.com', 'task:t1:session')

      expect(mockFetch).toHaveBeenCalledWith(
        '/api/credentials/a.com/usernames/task%3At1%3Asession',
        expect.objectContaining({ method: 'DELETE' }),
      )
    })

    it('purgeCredentialSessions 返回删除数', async () => {
      const mockFetch = vi.fn().mockResolvedValue({ ok: true, json: vi.fn().mockResolvedValue({ deleted: 3 }) })
      ;(global as any).fetch = mockFetch

      const n = await purgeCredentialSessions('a.com')

      expect(n).toBe(3)
      expect(mockFetch).toHaveBeenCalledWith('/api/credentials/a.com/sessions', expect.objectContaining({ method: 'DELETE' }))
    })

    it('deleteCredentialHost DELETE 整 host', async () => {
      const mockFetch = vi.fn().mockResolvedValue({ ok: true, json: vi.fn().mockResolvedValue({ ok: true }) })
      ;(global as any).fetch = mockFetch

      await deleteCredentialHost('a.com')

      expect(mockFetch).toHaveBeenCalledWith('/api/credentials/a.com', expect.objectContaining({ method: 'DELETE' }))
    })
  })

  describe('会话管理', () => {
    it('deleteConversation DELETE 到 /conversations/:id', async () => {
      const mockFetch = vi.fn().mockResolvedValue({ ok: true, status: 200 })
      ;(global as any).fetch = mockFetch

      await deleteConversation('c1')

      expect(mockFetch).toHaveBeenCalledWith('/api/conversations/c1', expect.objectContaining({ method: 'DELETE' }))
    })

    it('deleteConversation 409 抛 SCAN_ACTIVE 哨兵', async () => {
      ;(global as any).fetch = vi.fn().mockResolvedValue({ ok: false, status: 409 })

      await expect(deleteConversation('c1')).rejects.toThrow('SCAN_ACTIVE')
    })

    it('renameConversation PATCH title', async () => {
      const mockFetch = vi.fn().mockResolvedValue({ ok: true, status: 200, json: () => Promise.resolve({ title: '新标题' }) })
      ;(global as any).fetch = mockFetch

      await renameConversation('c1', '新标题')

      expect(mockFetch).toHaveBeenCalledWith(
        '/api/conversations/c1',
        expect.objectContaining({ method: 'PATCH', body: JSON.stringify({ title: '新标题' }) }),
      )
    })

    it('authStream POST 到 stream-auth 且带 credentials:include', async () => {
      const mockFetch = vi.fn().mockResolvedValue({ ok: true, status: 200 })
      ;(global as any).fetch = mockFetch

      await authStream('c1')

      expect(mockFetch).toHaveBeenCalledWith(
        '/api/conversations/c1/stream-auth',
        expect.objectContaining({ method: 'POST', credentials: 'include' }),
      )
    })

    it('authStream 失败抛错', async () => {
      ;(global as any).fetch = vi.fn().mockResolvedValue({ ok: false, status: 401 })

      await expect(authStream('c1')).rejects.toThrow()
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

  describe('全局漏洞台账', () => {
    it('listFindings 返回分页响应（findings + total/page/size）', async () => {
      const mockFetch = vi.fn().mockResolvedValue({
        ok: true,
        json: vi.fn().mockResolvedValue({ findings: [{ id: 'f1', severity: 'high' }], total: 1, page: 1, size: 50 }),
      })
      ;(global as any).fetch = mockFetch

      const res = await listFindings()

      expect(res.findings).toHaveLength(1)
      expect(res.total).toBe(1)
      expect(mockFetch).toHaveBeenCalledWith('/api/findings', { headers: { 'X-API-Key': '' } })
    })

    it('listFindings 只带真值筛选参数拼 query', async () => {
      const mockFetch = vi.fn().mockResolvedValue({ ok: true, json: vi.fn().mockResolvedValue({ findings: [] }) })
      ;(global as any).fetch = mockFetch

      await listFindings({ host: 'a.com', severity: '', status: 'open' })

      const url = mockFetch.mock.calls[0][0] as string
      expect(url).toContain('host=a.com')
      expect(url).toContain('status=open')
      expect(url).not.toContain('severity=')
    })

    it('listFindings 带 page/size 拼 query', async () => {
      const mockFetch = vi.fn().mockResolvedValue({ ok: true, json: vi.fn().mockResolvedValue({ findings: [] }) })
      ;(global as any).fetch = mockFetch

      await listFindings({ page: 2, size: 100 })

      const url = mockFetch.mock.calls[0][0] as string
      expect(url).toContain('page=2')
      expect(url).toContain('size=100')
    })

    it('updateFindingTriage PATCH 状态/严重度/备注，返回更新后的行', async () => {
      const mockFetch = vi.fn().mockResolvedValue({
        ok: true,
        json: vi.fn().mockResolvedValue({ finding: { id: 'f1', status: 'confirmed' } }),
      })
      ;(global as any).fetch = mockFetch

      const row = await updateFindingTriage('f1', 'confirmed', 'high', '已核实')

      expect(row.status).toBe('confirmed')
      expect(mockFetch).toHaveBeenCalledWith(
        '/api/findings/f1/status',
        expect.objectContaining({ method: 'PATCH', body: JSON.stringify({ status: 'confirmed', severity: 'high', note: '已核实' }) }),
      )
    })
  })
}
)
