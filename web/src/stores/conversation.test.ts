import { beforeEach, describe, expect, it } from 'vitest'
import { useConversationStore } from './conversation'
import type { Message } from '@/api/types'

const msg = (seq: number): Message => ({
  Seq: seq,
  ID: `m${seq}`,
  ConversationID: 'c1',
  Role: 'tool',
  Kind: 'event',
  Content: '',
  Metadata: null,
  CreatedAt: '2026-06-10T00:00:00Z',
})

describe('conversation store', () => {
  beforeEach(() => useConversationStore.getState().reset())

  it('按 seq 升序插入', () => {
    const s = useConversationStore.getState()
    s.ingest(msg(3))
    s.ingest(msg(1))
    s.ingest(msg(2))
    expect(useConversationStore.getState().messages.map((m) => m.Seq)).toEqual([1, 2, 3])
  })

  it('seq 去重（重连补历史与实时重叠）', () => {
    const s = useConversationStore.getState()
    s.ingest(msg(1))
    s.ingest(msg(1))
    expect(useConversationStore.getState().messages).toHaveLength(1)
  })

  it('lastSeq 反映最大 seq', () => {
    const s = useConversationStore.getState()
    s.ingest(msg(5))
    s.ingest(msg(2))
    expect(useConversationStore.getState().lastSeq).toBe(5)
  })

  it('reset 清空', () => {
    const s = useConversationStore.getState()
    s.ingest(msg(1))
    s.appendReasoningDelta('x', 'orchestrator')
    s.reset()
    const state = useConversationStore.getState()
    expect(state.messages).toHaveLength(0)
    expect(state.lastSeq).toBe(0)
    expect(state.liveReasoning).toBe('')
    expect(state.liveAgentName).toBe('')
  })

  it('appendReasoningDelta 累积文本并记住 agent 名', () => {
    const s = useConversationStore.getState()
    s.appendReasoningDelta('分析', 'exploitation')
    s.appendReasoningDelta('中', 'exploitation')
    const state = useConversationStore.getState()
    expect(state.liveReasoning).toBe('分析中')
    expect(state.liveAgentName).toBe('exploitation')
  })

  it('reasoning 最终帧落定 → 清空活动气泡 + agent 名', () => {
    const s = useConversationStore.getState()
    s.appendReasoningDelta('思考', 'reconnaissance')
    // 最终 reasoning 消息到达
    s.ingest({ ...msg(2), Metadata: { Kind: 'reasoning' } as never })
    const state = useConversationStore.getState()
    expect(state.liveReasoning).toBe('')
    expect(state.liveAgentName).toBe('')
  })
})
