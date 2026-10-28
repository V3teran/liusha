import { beforeEach, describe, expect, it } from 'vitest'
import { useConversationStore } from './conversation'
import type { Message, ScanEvent } from '@/api/types'

const msg = (seq: number): Message => ({
  seq: seq,
  id: `m${seq}`,
  conversation_id: 'c1',
  role: 'tool',
  kind: 'event',
  content: '',
  metadata: null,
  created_at: '2026-06-10T00:00:00Z',
})

const reasoningFinal = (seq: number, agent: string): Message => ({
  ...msg(seq),
  role: 'assistant',
  metadata: { kind: 'reasoning', agent_name: agent } as ScanEvent,
})

describe('conversation store', () => {
  beforeEach(() => useConversationStore.getState().reset())

  it('按 seq 升序插入', () => {
    const s = useConversationStore.getState()
    s.ingest(msg(3))
    s.ingest(msg(1))
    s.ingest(msg(2))
    expect(useConversationStore.getState().messages.map((m) => m.seq)).toEqual([1, 2, 3])
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
    s.appendReasoningDelta('执行', 'executor')
    s.appendReasoningDelta('再规划', 'planner')
    const state = useConversationStore.getState()
    expect(state.liveStreams).toEqual({
      planner: { thinking: '', text: '规划再规划' },
      executor: { thinking: '', text: '执行' },
    })
    expect(state.liveOrder).toEqual(['planner', 'executor'])
  })

  it('无 agent_name 归 assistant 道', () => {
    const s = useConversationStore.getState()
    s.appendReasoningDelta('你好', undefined)
    expect(useConversationStore.getState().liveStreams).toEqual({
      assistant: { thinking: '', text: '你好' },
    })
  })

  it('agent 推理终态只清该 agent 的道，其余道保留', () => {
    const s = useConversationStore.getState()
    s.appendReasoningDelta('规划', 'planner')
    s.appendReasoningDelta('执行', 'executor')
    s.ingest(reasoningFinal(2, 'executor'))
    const state = useConversationStore.getState()
    expect(state.liveStreams).toEqual({ planner: { thinking: '', text: '规划' } })
    expect(state.liveOrder).toEqual(['planner'])
  })

  it('assistant 完整消息清聊天道', () => {
    const s = useConversationStore.getState()
    s.appendReasoningDelta('回答', undefined)
    s.appendReasoningDelta('思考', 'planner')
    s.ingest({ ...msg(2), role: 'assistant', kind: 'message', content: '回答全文' })
    const state = useConversationStore.getState()
    expect(state.liveStreams).toEqual({ planner: { thinking: '', text: '思考' } })
  })

  it('补历史的终态消息同样清道（重连场景）', () => {
    const s = useConversationStore.getState()
    s.appendReasoningDelta('推理', 'evaluator')
    // 断线重连后补历史：终态消息经 ingest 到达
    s.ingest(reasoningFinal(1, 'evaluator'))
    expect(useConversationStore.getState().liveStreams).toEqual({})
  })

  // ── 双通道（thinking / text）──

  it('thinking 与 text 分通道累积，互不混流', () => {
    const s = useConversationStore.getState()
    s.appendReasoningDelta('内部推演', 'executor', 'thinking')
    s.appendReasoningDelta('正文陈述', 'executor', 'text')
    s.appendReasoningDelta('（缺省归 text）', 'executor')
    expect(useConversationStore.getState().liveStreams).toEqual({
      executor: { thinking: '内部推演', text: '正文陈述（缺省归 text）' },
    })
  })

  // ── thinking 占位 ──

  it('thinking 占位：置位后由首个增量帧清除（任意通道）', () => {
    const s = useConversationStore.getState()
    s.beginThinking()
    expect(useConversationStore.getState().thinking).toBe(true)
    s.appendReasoningDelta('首字', undefined)
    expect(useConversationStore.getState().thinking).toBe(false)
    s.beginThinking()
    s.appendReasoningDelta('思考首字', 'planner', 'thinking')
    expect(useConversationStore.getState().thinking).toBe(false)
  })

  it('thinking 占位：assistant 消息落地清除（回答终稿先于首增量的降级场景）', () => {
    const s = useConversationStore.getState()
    s.beginThinking()
    s.ingest({ ...msg(2), role: 'assistant', kind: 'message', content: '回答全文' })
    expect(useConversationStore.getState().thinking).toBe(false)
  })

  it('thinking 占位：用户消息 ingest（补拉）不清除', () => {
    const s = useConversationStore.getState()
    s.beginThinking()
    s.ingest({ ...msg(1), role: 'user', kind: 'message', content: '我自己的消息' })
    expect(useConversationStore.getState().thinking).toBe(true)
  })

  it('thinking 占位：reset 保留（新会话发送链路 open→reset 紧随置位，清掉会闪没）', () => {
    const s = useConversationStore.getState()
    s.beginThinking()
    s.reset()
    expect(useConversationStore.getState().thinking).toBe(true)
  })

  it('thinking 占位：切旧会话由历史 ingest 清除（assistant 消息落地）', () => {
    const s = useConversationStore.getState()
    s.beginThinking()
    s.reset()
    // 切到旧会话：拉回的历史含 assistant 回答
    s.ingest({ ...msg(1), role: 'assistant', kind: 'message', content: '旧回答' })
    expect(useConversationStore.getState().thinking).toBe(false)
  })

  // ── 任务生命周期终态节点清全部道 ──

  it('task_status 终态节点清全部活动道（停止后气泡不残留）', () => {
    const s = useConversationStore.getState()
    s.appendReasoningDelta('规划中', 'planner')
    s.appendReasoningDelta('执行中', 'executor')
    s.appendReasoningDelta('回答', undefined)
    s.ingest({
      ...msg(3), role: 'assistant', kind: 'event',
      metadata: { kind: 'task_status', status: 'aborted' } as ScanEvent,
    })
    const state = useConversationStore.getState()
    expect(state.liveStreams).toEqual({})
    expect(state.liveOrder).toEqual([])
  })

  it('task_status 非终态节点（dispatched）不清道', () => {
    const s = useConversationStore.getState()
    s.appendReasoningDelta('规划中', 'planner')
    s.ingest({
      ...msg(3), role: 'assistant', kind: 'event',
      metadata: { kind: 'task_status', status: 'dispatched' } as ScanEvent,
    })
    expect(useConversationStore.getState().liveStreams).toEqual({ planner: { thinking: '', text: '规划中' } })
  })
})
