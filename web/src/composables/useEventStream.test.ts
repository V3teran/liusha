import { describe, it, expect, vi, beforeEach } from 'vitest'
import { setActivePinia, createPinia } from 'pinia'
import { openEventStream } from './useEventStream'
import { useConversationStore } from '../stores/conversation'

// Mock EventSource，模拟浏览器 API（含 addEventListener / onopen / onerror）。
class MockES {
  static last: MockES
  url: string
  withCredentials: boolean
  onmessage: ((e: { data: string; lastEventId: string }) => void) | null = null
  onopen: (() => void) | null = null
  onerror: (() => void) | null = null
  listeners: Record<string, (e: { data: string }) => void> = {}
  closed = false
  constructor(url: string, init?: { withCredentials?: boolean }) {
    this.url = url
    this.withCredentials = !!init?.withCredentials
    MockES.last = this
  }
  addEventListener(name: string, fn: (e: { data: string }) => void) {
    this.listeners[name] = fn
  }
  close() {
    this.closed = true
  }
}

// openEventStream 现在先 await authStream（fetch 换 cookie）再开 EventSource，故连接是异步的。
// flush 等一个微任务轮，让 connect() 跑到创建 EventSource。
const flush = () => new Promise((r) => setTimeout(r, 0))

describe('useEventStream', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.stubGlobal('EventSource', MockES as any)
    // mock fetch：authStream 调 POST /stream-auth，返回 ok 即可。
    vi.stubGlobal('fetch', vi.fn(async () => ({ ok: true, status: 200 }) as Response))
  })

  it('鉴权后用 withCredentials 订阅正确 URL，帧灌进 store', async () => {
    const store = useConversationStore()
    const handle = openEventStream('conv-1', store)
    await flush()

    expect(MockES.last.url).toContain('/api/conversations/conv-1/stream')
    expect(MockES.last.withCredentials).toBe(true)

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

    expect(store.messages).toHaveLength(1)
    expect(store.messages[0].ID).toBe('m1')
    expect(store.messages[0].Seq).toBe(1)

    handle.close()
    expect(MockES.last.closed).toBe(true)
  })

  it('坏帧（无效 JSON）应该被忽略', async () => {
    const store = useConversationStore()
    const handle = openEventStream('conv-2', store)
    await flush()

    MockES.last.onmessage!({ data: 'not json', lastEventId: '1' })

    expect(store.messages).toHaveLength(0)
    handle.close()
  })

  it('多帧应该按 seq 有序插入 store', async () => {
    const store = useConversationStore()
    const handle = openEventStream('conv-3', store)
    await flush()

    const frame = (seq: number, id: string) => ({
      data: JSON.stringify({
        Seq: seq,
        ID: id,
        ConversationID: 'conv-3',
        Role: 'assistant',
        Kind: 'message',
        Content: 'msg' + seq,
        Metadata: null,
        CreatedAt: '',
      }),
      lastEventId: String(seq),
    })

    MockES.last.onmessage!(frame(3, 'm3'))
    MockES.last.onmessage!(frame(1, 'm1'))
    MockES.last.onmessage!(frame(2, 'm2'))

    expect(store.messages).toHaveLength(3)
    expect(store.messages[0].Seq).toBe(1)
    expect(store.messages[1].Seq).toBe(2)
    expect(store.messages[2].Seq).toBe(3)

    handle.close()
  })
})
