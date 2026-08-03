import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import {
  getApiKey, setApiKey, listScenarios, listConversations, listMessages, startChat, followUp, abortScan,
  listTasks, startActiveScan, abortTask, getSitemap, listLLMInvocations,
  getLLMInvocationStat, getLLMInvocationFacets,
  listCredentials, saveCredentialsBatch, deleteCredentials,
  deleteConversation, renameConversation, authStream, bootstrapApiKey,
  listFindings, updateFindingTriage, getAttackGraph, getMilestones, getLLMInvocationDetail,
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
    it('listScenarios 添加 X-API-Key header 并拆出 scenarios', async () => {
      setApiKey('my-key')
      const mockFetch = vi.fn().mockResolvedValue({
        ok: true,
        json: vi.fn().mockResolvedValue({ scenarios: [{ id: 'u1', code: 'web_app', name: 'Web 应用', description: 'desc' }] }),
      })
      ;(global as any).fetch = mockFetch

      const scenarios = await listScenarios()

      expect(mockFetch).toHaveBeenCalledWith('/api/scenarios', {
        headers: { 'X-API-Key': 'my-key' },
      })
      expect(scenarios).toHaveLength(1)
      expect(scenarios[0].code).toBe('web_app')
    })

    it('listConversations 返回会话列表 + hasMore，且带 limit/offset query', async () => {
      const mockFetch = vi.fn().mockResolvedValue({
        ok: true,
        json: vi.fn().mockResolvedValue({
          conversations: [{ ID: 'c1', Title: 'scan1', ScanID: 's1', ScenarioID: 'web_app', Source: 'manual', Status: 'running', CreatedAt: '2026-06-10T00:00:00Z', UpdatedAt: '2026-06-10T00:00:00Z' }],
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

      const result = await startChat('scan target', 'web_app')

      expect(result.conversation_id).toBe('conv-123')
      expect(result.scan_id).toBe('scan-456')
      expect(mockFetch).toHaveBeenCalledWith('/api/chat', {
        method: 'POST',
        headers: {
          'X-API-Key': 'my-key',
          'Content-Type': 'application/json',
        },
        body: JSON.stringify({ brief: 'scan target', scenario_id: 'web_app' }),
      })
    })

    it('请求失败时抛出错误', async () => {
      const mockFetch = vi.fn().mockResolvedValue({ ok: false, status: 401 })
      ;(global as any).fetch = mockFetch

      await expect(listScenarios()).rejects.toThrow('GET /scenarios → 401')
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

  describe('owner task / 扫描', () => {
    it('listTasks 拆出 tasks 数组', async () => {
      ;(global as any).fetch = vi.fn().mockResolvedValue({
        ok: true,
        json: vi.fn().mockResolvedValue({
          tasks: [{ id: 'o1', scope: '{"any":true}', status: 'running', scenario_id: 'web-pentest-killchain', created_at: '2026-06-11T00:00:00Z' }],
        }),
      })

      const result = await listTasks()

      expect(result).toHaveLength(1)
      expect(result[0].id).toBe('o1')
    })

    it('listTasks 带 limit 拼 query', async () => {
      const mockFetch = vi.fn().mockResolvedValue({ ok: true, json: vi.fn().mockResolvedValue({ tasks: [] }) })
      ;(global as any).fetch = mockFetch

      await listTasks(20)

      expect(mockFetch).toHaveBeenCalledWith('/api/tasks?limit=20', { headers: { 'X-API-Key': '' } })
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

    it('abortTask POST 到 /tasks/:id/abort', async () => {
      const mockFetch = vi.fn().mockResolvedValue({ ok: true, json: vi.fn().mockResolvedValue({ ok: true }) })
      ;(global as any).fetch = mockFetch

      await abortTask('o1')

      expect(mockFetch).toHaveBeenCalledWith('/api/tasks/o1/abort', expect.objectContaining({ method: 'POST' }))
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

    it('listLLMInvocations 透传扁平 items + 分页游标', async () => {
      const mockFetch = vi.fn().mockResolvedValue({
        ok: true,
        json: vi
          .fn()
          .mockResolvedValue({ task_id: 't1', total: 1, next_after: 5, has_more: false, items: [{ id: 5, role: 'orchestrator' }] }),
      })
      ;(global as any).fetch = mockFetch

      const res = await listLLMInvocations('t1', 3, 50)

      expect(res.total).toBe(1)
      expect(res.next_after).toBe(5)
      expect(res.items[0].role).toBe('orchestrator')
      expect(mockFetch).toHaveBeenCalledWith('/api/llm/invocations/t1?after=3&limit=50', { headers: { 'X-API-Key': '' } })
    })

    // 筛选参数必须序列化进 query 交服务端筛（分页下前端筛只会筛到当前页）。
    it('listLLMInvocations 序列化筛选参数', async () => {
      const mockFetch = vi.fn().mockResolvedValue({
        ok: true,
        json: vi.fn().mockResolvedValue({ task_id: 't1', total: 0, next_after: 0, has_more: false, items: [] }),
      })
      ;(global as any).fetch = mockFetch

      await listLLMInvocations('t1', 0, 0, {
        role: 'exploitation',
        model: 'mimo-v2.5',
        onlyErr: true,
        start: '2026-07-20T00:00:00.000Z',
        end: '',
      })

      const url = mockFetch.mock.calls[0][0] as string
      expect(url).toContain('role=exploitation')
      expect(url).toContain('model=mimo-v2.5')
      expect(url).toContain('only_err=1')
      expect(url).toContain('start=2026-07-20T00%3A00%3A00.000Z')
      expect(url).not.toContain('end=') // 空值不发，避免后端把空串当条件
    })

    // 统计与列表必须吃同一套筛选，否则「明细筛剩 3 条、合计仍是全量」自相矛盾。
    it('getLLMInvocationStat 带上同一套筛选参数', async () => {
      const mockFetch = vi.fn().mockResolvedValue({
        ok: true,
        json: vi.fn().mockResolvedValue({ task_id: 't1', calls: 2, in_tokens: 1, out_tokens: 1, cached_tokens: 0, latency_ms: 0 }),
      })
      ;(global as any).fetch = mockFetch

      await getLLMInvocationStat('t1', { role: 'exploitation', onlyErr: true })

      const url = mockFetch.mock.calls[0][0] as string
      expect(url).toContain('/llm/invocations/t1/stat?')
      expect(url).toContain('role=exploitation')
      expect(url).toContain('only_err=1')
      expect(url).not.toContain('after=') // 统计不该带分页游标
    })

    it('getLLMInvocationFacets 拉候选集合', async () => {
      const mockFetch = vi.fn().mockResolvedValue({
        ok: true,
        json: vi.fn().mockResolvedValue({ task_id: 't1', roles: ['orchestrator'], models: ['mimo-v2.5'] }),
      })
      ;(global as any).fetch = mockFetch

      const f = await getLLMInvocationFacets('t1')

      expect(f.roles).toEqual(['orchestrator'])
      expect(mockFetch).toHaveBeenCalledWith('/api/llm/invocations/t1/facets', { headers: { 'X-API-Key': '' } })
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
      const mockFetch = vi.fn().mockResolvedValue({ ok: true, status: 200 })
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
    it('listFindings 拆出 findings 数组', async () => {
      const mockFetch = vi.fn().mockResolvedValue({
        ok: true,
        json: vi.fn().mockResolvedValue({ findings: [{ id: 'f1', severity: 'high' }] }),
      })
      ;(global as any).fetch = mockFetch

      const rows = await listFindings()

      expect(rows).toHaveLength(1)
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

  describe('执行图', () => {
    it('getAttackGraph 透传响应', async () => {
      const graph = { task_id: 't1', conversation_id: 'c1', running: false, nodes: [], edges: [] }
      const mockFetch = vi.fn().mockResolvedValue({ ok: true, json: vi.fn().mockResolvedValue(graph) })
      ;(global as any).fetch = mockFetch

      const g = await getAttackGraph('o1')

      expect(g).toEqual(graph)
      expect(mockFetch).toHaveBeenCalledWith('/api/attack_graph/o1', { headers: { 'X-API-Key': '' } })
    })

    it('getAttackGraph 带 conv 覆盖参数拼 query', async () => {
      const mockFetch = vi.fn().mockResolvedValue({
        ok: true,
        json: vi.fn().mockResolvedValue({ task_id: 't', conversation_id: '', running: false, nodes: [], edges: [] }),
      })
      ;(global as any).fetch = mockFetch

      await getAttackGraph('o1', 'c2')

      expect(mockFetch).toHaveBeenCalledWith('/api/attack_graph/o1?conv=c2', { headers: { 'X-API-Key': '' } })
    })

    it('getMilestones 拆出 milestones 数组', async () => {
      const mockFetch = vi.fn().mockResolvedValue({
        ok: true,
        json: vi.fn().mockResolvedValue({ milestones: [{ agent: 'exploitation', summary: 'x', node_count: 3 }] }),
      })
      ;(global as any).fetch = mockFetch

      const ms = await getMilestones('o1')

      expect(ms).toHaveLength(1)
      expect(mockFetch).toHaveBeenCalledWith('/api/attack_graph/o1/milestones', { headers: { 'X-API-Key': '' } })
    })
  })

  describe('LLM 审计详情', () => {
    it('getLLMInvocationDetail 拉取含 messages/result 的完整记录', async () => {
      const detail = { id: 1, messages: [], result: null }
      const mockFetch = vi.fn().mockResolvedValue({ ok: true, json: vi.fn().mockResolvedValue(detail) })
      ;(global as any).fetch = mockFetch

      const d = await getLLMInvocationDetail('t1', 1)

      expect(d).toEqual(detail)
      expect(mockFetch).toHaveBeenCalledWith('/api/llm/invocations/t1/invocation/1', { headers: { 'X-API-Key': '' } })
    })
  })
})
