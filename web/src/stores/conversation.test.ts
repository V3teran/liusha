import { beforeEach, describe, expect, it } from 'vitest'
import { useConversationStore } from './conversation'
import type { Message, ScanEvent } from '@/api/types'

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

const reasoningFinal = (seq: number, agent: string): Message => ({
  ...msg(seq),
  Role: 'assistant',
  Metadata: { Kind: 'reasoning', AgentName: agent } as ScanEvent,
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
    s.appendReasoningDelta('x', 'planner')
    s.reset()
    const state = useConversationStore.getState()
    expect(state.messages).toHaveLength(0)
    expect(state.lastSeq).toBe(0)
    expect(state.liveStreams).toEqual({})
    expect(state.liveOrder).toEqual([])
  })

  it('增量按 agent 分道累积，互不串道', () => {
    const s = useConversationStore.getState()
    s.appendReasoningDelta('规划', 'planner')
    s.appendReasoningDelta('中', 'planner')
    s.appendReasoningDelta('执行', 'executor')
    const state = useConversationStore.getState()
    expect(state.liveStreams).toEqual({ planner: '规划中', executor: '执行' })
    expect(state.liveOrder).toEqual(['planner', 'executor'])
  })

  it('无 agent_name 的增量归 assistant 道（聊天打字机）', () => {
    const s = useConversationStore.getState()
    s.appendReasoningDelta('你好')
    expect(useConversationStore.getState().liveStreams).toEqual({ assistant: '你好' })
  })

  it('agent 推理终态只清该 agent 的道，其余道保留', () => {
    const s = useConversationStore.getState()
    s.appendReasoningDelta('规划', 'planner')
    s.appendReasoningDelta('执行', 'executor')
    s.ingest(reasoningFinal(2, 'executor'))
    const state = useConversationStore.getState()
    expect(state.liveStreams).toEqual({ planner: '规划' })
    expect(state.liveOrder).toEqual(['planner'])
  })

  it('assistant 完整消息清聊天道', () => {
    const s = useConversationStore.getState()
    s.appendReasoningDelta('回答', undefined)
    s.appendReasoningDelta('思考', 'planner')
    s.ingest({ ...msg(2), Role: 'assistant', Kind: 'message', Content: '回答全文' })
    const state = useConversationStore.getState()
    expect(state.liveStreams).toEqual({ planner: '思考' })
  })

  it('补历史的终态消息同样清道（重连场景）', () => {
    const s = useConversationStore.getState()
    s.appendReasoningDelta('推理', 'evaluator')
    // 断线重连后补历史：终态消息经 ingest 到达
    s.ingest(reasoningFinal(1, 'evaluator'))
    expect(useConversationStore.getState().liveStreams).toEqual({})
  })
})
