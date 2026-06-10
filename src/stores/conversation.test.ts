import { setActivePinia, createPinia } from 'pinia'
import { beforeEach, describe, it, expect } from 'vitest'
import { useConversationStore } from './conversation'
import type { Message } from '../api/types'

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
  beforeEach(() => setActivePinia(createPinia()))

  it('按 seq 升序插入', () => {
    const s = useConversationStore()
    s.ingest(msg(3))
    s.ingest(msg(1))
    s.ingest(msg(2))
    expect(s.messages.map((m) => m.Seq)).toEqual([1, 2, 3])
  })

  it('seq 去重（重连补历史与实时重叠）', () => {
    const s = useConversationStore()
    s.ingest(msg(1))
    s.ingest(msg(1))
    expect(s.messages).toHaveLength(1)
  })

  it('lastSeq 反映最大 seq', () => {
    const s = useConversationStore()
    s.ingest(msg(5))
    s.ingest(msg(2))
    expect(s.lastSeq).toBe(5)
  })

  it('reset 清空', () => {
    const s = useConversationStore()
    s.ingest(msg(1))
    s.reset()
    expect(s.messages).toHaveLength(0)
    expect(s.lastSeq).toBe(0)
  })
})
