import { beforeEach, describe, expect, it, vi } from 'vitest'
import { openEventStream } from './useEventStream'
import { useConversationStore } from '@/stores/conversation'

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

// openEventStream 先 await authStream（fetch 换 cookie）再开 EventSource，故连接是异步的。
// flush 等一个微任务轮，让 connect() 跑到创建 EventSource。
const flush = () => new Promise((r) => setTimeout(r, 0))

describe('useEventStream', () => {
  beforeEach(() => {
    useConversationStore.getState().reset()
    vi.stubGlobal('EventSource', MockES as unknown as typeof EventSource)
    // mock fetch：authStream 调 POST /stream-auth，返回 ok 即可。
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => ({ ok: true, status: 200 }) as Response),
    )
  })

  it('鉴权后用 withCredentials 订阅正确 URL，帧灌进 store', async () => {
    const store = useConversationStore.getState()
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

    const messages = useConversationStore.getState().messages
    expect(messages).toHaveLength(1)
    expect(messages[0].ID).toBe('m1')
    expect(messages[0].Seq).toBe(1)

    handle.close()
    expect(MockES.last.closed).toBe(true)
  })

  it('坏帧（无效 JSON）应该被忽略', async () => {
    const store = useConversationStore.getState()
    const handle = openEventStream('conv-2', store)
    await flush()

    MockES.last.onmessage!({ data: 'not json', lastEventId: '1' })

    expect(useConversationStore.getState().messages).toHaveLength(0)
    handle.close()
  })

  it('多帧应该按 seq 有序插入 store', async () => {
    const store = useConversationStore.getState()
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

    const messages = useConversationStore.getState().messages
    expect(messages).toHaveLength(3)
    expect(messages[0].Seq).toBe(1)
    expect(messages[1].Seq).toBe(2)
    expect(messages[2].Seq).toBe(3)

    handle.close()
  })

  it('onopen 触发时状态回调为 open', async () => {
    const store = useConversationStore.getState()
    const onStatusChange = vi.fn()
    const handle = openEventStream('conv-4', store, onStatusChange)
    await flush()

    expect(onStatusChange).toHaveBeenCalledWith('connecting')
    MockES.last.onopen!()
    expect(onStatusChange).toHaveBeenCalledWith('open')

    handle.close()
  })

  it('event:delta 帧累积进 store.liveReasoning，带 agent_name', async () => {
    const store = useConversationStore.getState()
    const handle = openEventStream('conv-5', store)
    await flush()

    MockES.last.listeners['delta']({
      data: JSON.stringify({ delta: true, text: '正在分析', agent_name: 'exploitation' }),
    })
    MockES.last.listeners['delta']({
      data: JSON.stringify({ delta: true, text: '中…' }),
    })

    const state = useConversationStore.getState()
    expect(state.liveReasoning).toBe('正在分析中…')
    expect(state.liveAgentName).toBe('exploitation')

    handle.close()
  })

  it('delta 坏帧（无效 JSON）被忽略，不影响 liveReasoning', async () => {
    const store = useConversationStore.getState()
    const handle = openEventStream('conv-5b', store)
    await flush()

    MockES.last.listeners['delta']({ data: 'not json' })

    expect(useConversationStore.getState().liveReasoning).toBe('')
    handle.close()
  })

  it('onerror 触发后自管重连：关闭旧连接、状态变 reconnecting，退避后重新 authStream + 开新连接', async () => {
    vi.useFakeTimers()
    const store = useConversationStore.getState()
    const onStatusChange = vi.fn()
    const handle = openEventStream('conv-6', store, onStatusChange)
    await vi.advanceTimersByTimeAsync(0) // flush 首次 connect 的 authStream microtask

    const firstES = MockES.last
    firstES.onerror!()
    expect(firstES.closed).toBe(true)
    expect(onStatusChange).toHaveBeenCalledWith('reconnecting')

    // 首次重连退避 1000ms（2^0 * 1000）
    await vi.advanceTimersByTimeAsync(1000)
    expect(MockES.last).not.toBe(firstES) // 新开了一个连接
    expect(MockES.last.url).toContain('/api/conversations/conv-6/stream')

    handle.close()
    vi.useRealTimers()
  })

  it('鉴权失败（authStream 抛错）时退避后重试，不放弃', async () => {
    vi.useFakeTimers()
    let callCount = 0
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => {
        callCount += 1
        if (callCount === 1) throw new Error('unauthorized')
        return { ok: true, status: 200 } as Response
      }),
    )
    const store = useConversationStore.getState()
    const onStatusChange = vi.fn()
    const handle = openEventStream('conv-7', store, onStatusChange)

    await vi.advanceTimersByTimeAsync(0) // 首次 authStream 失败
    expect(onStatusChange).toHaveBeenCalledWith('reconnecting')

    await vi.advanceTimersByTimeAsync(1000) // 退避后重试，第二次 authStream 成功
    expect(callCount).toBeGreaterThanOrEqual(2)

    handle.close()
    vi.useRealTimers()
  })

  it('close() 后阻止后续重连（closed 标记生效）', async () => {
    vi.useFakeTimers()
    const store = useConversationStore.getState()
    const handle = openEventStream('conv-8', store)
    await vi.advanceTimersByTimeAsync(0)

    const es = MockES.last
    handle.close()
    expect(es.closed).toBe(true)

    // close 之后即使触发 onerror，也不应该再开新连接。
    es.onerror?.()
    await vi.advanceTimersByTimeAsync(20000)
    expect(MockES.last).toBe(es) // 没有新连接产生

    vi.useRealTimers()
  })
})
