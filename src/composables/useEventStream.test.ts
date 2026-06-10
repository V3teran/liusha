import { describe, it, expect, vi, beforeEach } from 'vitest'
import { setActivePinia, createPinia } from 'pinia'
import { openEventStream } from './useEventStream'
import { useConversationStore } from '../stores/conversation'

// Mock EventSource，模拟浏览器 API
class MockES {
  static last: MockES
  url: string
  withCredentials: boolean
  onmessage: ((e: { data: string; lastEventId: string }) => void) | null = null
  closed = false
  constructor(url: string, init?: { withCredentials?: boolean }) {
    this.url = url
    this.withCredentials = !!init?.withCredentials
    MockES.last = this
  }
  close() {
    this.closed = true
  }
}

describe('useEventStream', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.stubGlobal('EventSource', MockES as any)
  })

  it('用 withCredentials 订阅正确 URL，帧灌进 store', () => {
    const store = useConversationStore()
    const handle = openEventStream('conv-1', store)

    // 验证 URL 包含对话 ID 和流端点
    expect(MockES.last.url).toContain('/api/conversations/conv-1/stream')
    // 验证启用 withCredentials 以自动携带 cookie
    expect(MockES.last.withCredentials).toBe(true)

    // 模拟 SSE 事件到达
    MockES.last.onmessage!({
      data: JSON.stringify({
        Seq: 1,
        ID: 'm1',
        ConversationID: 'conv-1',
        Role: 'tool',
        Kind: 'event',
        Content: 'x',
        Metadata: null,
        CreatedAt: '',
      }),
      lastEventId: '1',
    })

    // 验证消息被灌进 store
    expect(store.messages).toHaveLength(1)
    expect(store.messages[0].ID).toBe('m1')
    expect(store.messages[0].Seq).toBe(1)

    // 验证 close 正常工作
    handle.close()
    expect(MockES.last.closed).toBe(true)
  })

  it('坏帧（无效 JSON）应该被忽略', () => {
    const store = useConversationStore()
    const handle = openEventStream('conv-2', store)

    // 发送坏帧
    MockES.last.onmessage!({
      data: 'not json',
      lastEventId: '1',
    })

    // 存储中无消息
    expect(store.messages).toHaveLength(0)

    handle.close()
  })

  it('多帧应该按 seq 有序插入 store', () => {
    const store = useConversationStore()
    const handle = openEventStream('conv-3', store)

    // 发送乱序帧
    MockES.last.onmessage!({
      data: JSON.stringify({
        Seq: 3,
        ID: 'm3',
        ConversationID: 'conv-3',
        Role: 'assistant',
        Kind: 'message',
        Content: 'msg3',
        Metadata: null,
        CreatedAt: '',
      }),
      lastEventId: '3',
    })

    MockES.last.onmessage!({
      data: JSON.stringify({
        Seq: 1,
        ID: 'm1',
        ConversationID: 'conv-3',
        Role: 'user',
        Kind: 'message',
        Content: 'msg1',
        Metadata: null,
        CreatedAt: '',
      }),
      lastEventId: '1',
    })

    MockES.last.onmessage!({
      data: JSON.stringify({
        Seq: 2,
        ID: 'm2',
        ConversationID: 'conv-3',
        Role: 'assistant',
        Kind: 'message',
        Content: 'msg2',
        Metadata: null,
        CreatedAt: '',
      }),
      lastEventId: '2',
    })

    // 验证消息按 seq 有序
    expect(store.messages).toHaveLength(3)
    expect(store.messages[0].Seq).toBe(1)
    expect(store.messages[1].Seq).toBe(2)
    expect(store.messages[2].Seq).toBe(3)

    handle.close()
  })
})
