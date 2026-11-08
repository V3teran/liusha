import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import {
  listConversations,
  listMessages,
  startChat,
  followUp,
  abortTask,
  deleteConversation,
  renameConversation,
} from './conversations'

// fetch 污染治理：每个用例后强制还原原生 fetch（对齐 client.test.ts 的口径）。
const nativeFetch = globalThis.fetch
afterEach(() => {
  ;(globalThis as any).fetch = nativeFetch
})

describe('会话域 API', () => {
  beforeEach(() => {
    ;(globalThis as any).fetch = vi.fn()
  })

  afterEach(() => {
    vi.clearAllMocks()
    ;(globalThis as any).fetch = undefined
  })

  describe('HTTP 请求', () => {
    it('listConversations 返回会话列表 + hasMore，且带 limit/offset query', async () => {
      const mockFetch = vi.fn().mockResolvedValue({
        ok: true,
        json: vi.fn().mockResolvedValue({
          conversations: [{ ID: 'c1', Title: 'scan1', TaskID: 's1', Source: 'direct', CreatedAt: '2026-06-10T00:00:00Z', UpdatedAt: '2026-06-10T00:00:00Z' }],
          has_more: true,
        }),
      })
      ;(globalThis as any).fetch = mockFetch

      const result = await listConversations(30, 30)

      expect(mockFetch).toHaveBeenCalledWith(
        expect.stringContaining('/conversations?limit=30&offset=30'),
      )
      expect(result.conversations).toHaveLength(1)
    })

    it('listConversations 传 source 时带 source query（服务端过滤，保证分页边界正确）', async () => {
      const mockFetch = vi.fn().mockResolvedValue({
        ok: true,
        json: vi.fn().mockResolvedValue({ conversations: [], has_more: false }),
      })
      ;(globalThis as any).fetch = mockFetch

      await listConversations(30, 0, 'proxy')

      expect(mockFetch).toHaveBeenCalledWith(expect.stringContaining('source=proxy'))
    })

    it('listConversations 把后端 null（Go nil slice）归一为空数组，不透传 null', async () => {
      const mockFetch = vi.fn().mockResolvedValue({
        ok: true,
        json: vi.fn().mockResolvedValue({ conversations: null, has_more: false }),
      })
      ;(globalThis as any).fetch = mockFetch

      const result = await listConversations(30, 0, 'proxy')

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
      ;(globalThis as any).fetch = mockFetch

      await listMessages('c1', 100)

      expect(mockFetch).toHaveBeenCalledWith('/api/conversations/c1/messages?after_seq=100')
    })

    it('startChat 发送 POST 并返回 conversation_id', async () => {
      const mockFetch = vi.fn().mockResolvedValue({
        ok: true,
        json: vi.fn().mockResolvedValue({ conversation_id: 'conv-123', task_id: 't-456' }),
      })
      ;(globalThis as any).fetch = mockFetch

      const result = await startChat('scan target')

      expect(result.conversation_id).toBe('conv-123')
      expect(result.task_id).toBe('t-456')
      expect(mockFetch).toHaveBeenCalledWith('/api/chat', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ brief: 'scan target' }),
      })
    })

    it('请求失败时抛出错误（含后端 error body）', async () => {
      const mockFetch = vi.fn().mockResolvedValue({
        ok: false,
        status: 500,
        json: async () => ({ error: '内部错误' }),
      })
      ;(globalThis as any).fetch = mockFetch

      await expect(listConversations()).rejects.toThrow('GET 500: 内部错误')
    })
  })

  describe('多轮方法', () => {
    it('followUp POST 到 /conversations/:id/messages 带 content', async () => {
      const mockFetch = vi.fn().mockResolvedValue({
        ok: true,
        status: 200,
        json: vi.fn().mockResolvedValue({ intent: 'action', task_id: 't-new' }),
      })
      ;(globalThis as any).fetch = mockFetch

      const result = await followUp('c1', '深挖')

      expect(mockFetch).toHaveBeenCalledWith('/api/conversations/c1/messages', expect.objectContaining({ method: 'POST', body: JSON.stringify({ content: '深挖' }) }))
      expect(result.intent).toBe('action')
    })

    it('followUp 非 2xx 抛错（busy 裁决已异步化，无 409 特例）', async () => {
      const mockFetch = vi.fn().mockResolvedValue({
        ok: false,
        status: 500,
        json: vi.fn().mockResolvedValue({}),
      })
      ;(globalThis as any).fetch = mockFetch

      await expect(followUp('c1', 'x')).rejects.toThrow('500')
    })

    it('abortTask POST 到 /conversations/:id/abort', async () => {
      const mockFetch = vi.fn().mockResolvedValue({
        ok: true,
        status: 200,
        json: vi.fn().mockResolvedValue({ aborted: true }),
      })
      ;(globalThis as any).fetch = mockFetch

      await abortTask('c1')

      expect(mockFetch).toHaveBeenCalledWith('/api/conversations/c1/abort', expect.objectContaining({ method: 'POST' }))
    })
  })

  describe('会话管理', () => {
    it('deleteConversation DELETE 到 /conversations/:id', async () => {
      const mockFetch = vi.fn().mockResolvedValue({ ok: true, status: 200 })
      ;(globalThis as any).fetch = mockFetch

      await deleteConversation('c1')

      expect(mockFetch).toHaveBeenCalledWith('/api/conversations/c1', expect.objectContaining({ method: 'DELETE' }))
    })

    it('deleteConversation 409 抛 TASK_ACTIVE 哨兵', async () => {
      ;(globalThis as any).fetch = vi.fn().mockResolvedValue({ ok: false, status: 409 })

      await expect(deleteConversation('c1')).rejects.toThrow('TASK_ACTIVE')
    })

    it('renameConversation PATCH title', async () => {
      const mockFetch = vi.fn().mockResolvedValue({ ok: true, status: 200, json: () => Promise.resolve({ title: '新标题' }) })
      ;(globalThis as any).fetch = mockFetch

      await renameConversation('c1', '新标题')

      expect(mockFetch).toHaveBeenCalledWith(
        '/api/conversations/c1',
        expect.objectContaining({ method: 'PATCH', body: JSON.stringify({ title: '新标题' }) }),
      )
    })
  })
})
