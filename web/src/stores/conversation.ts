import { create } from 'zustand'
import type { Message } from '@/api/types'

interface ConversationState {
  messages: Message[]
  seqSet: Set<number>
  lastSeq: number
  // 流式推理活动气泡：SSE event:delta 逐 chunk 累积的文本；最终 reasoning 消息到达即清空。
  // eino 串行执行，同一时刻至多一个 ChatModel 在流式，单缓冲足够（无需按 streamID 分桶）。
  liveReasoning: string
  // 当前流式推理所属 agent 名（delta 帧带 agent_name）——驱动活动气泡按 agent 取色 + 标签，
  // 与落定的推理卡样式一致（否则流式为中性灰、落定变彩色）。
  liveAgentName: string
  ingest(m: Message): void
  appendReasoningDelta(chunk: string, agentName?: string): void
  reset(): void
}

// 按 seq 有序去重持有当前会话的消息。SSE 补历史 + 实时可能重叠，靠 seq 去重。
export const useConversationStore = create<ConversationState>((set, get) => ({
  messages: [],
  seqSet: new Set<number>(),
  lastSeq: 0,
  liveReasoning: '',
  liveAgentName: '',

  ingest(m: Message) {
    const { seqSet, messages } = get()
    // 重复消息直接返回（seq 去重）。
    if (seqSet.has(m.Seq)) return

    const nextSeqSet = new Set(seqSet)
    nextSeqSet.add(m.Seq)

    // 推理消息（最终帧）落定 → 清空活动气泡，由正式推理卡接管渲染。
    const clearsLive = m.Metadata?.Kind === 'reasoning'

    // 二分查找插入位置以保持升序。
    // 事件多数尾部追加，但补历史可能乱序到达。
    let lo = 0
    let hi = messages.length
    while (lo < hi) {
      const mid = (lo + hi) >> 1
      if (messages[mid].Seq < m.Seq) {
        lo = mid + 1
      } else {
        hi = mid
      }
    }
    const nextMessages = messages.slice()
    nextMessages.splice(lo, 0, m)

    set({
      messages: nextMessages,
      seqSet: nextSeqSet,
      lastSeq: m.Seq > get().lastSeq ? m.Seq : get().lastSeq,
      ...(clearsLive ? { liveReasoning: '', liveAgentName: '' } : {}),
    })
  },

  // appendReasoningDelta 累积一段流式推理增量（驱动逐字打字机活动气泡）。
  // agentName 随首个 chunk 带入，驱动气泡按 agent 取色 + 标签。
  appendReasoningDelta(chunk: string, agentName?: string) {
    if (!chunk) return
    set((state) => ({
      liveReasoning: state.liveReasoning + chunk,
      liveAgentName: agentName ?? state.liveAgentName,
    }))
  },

  reset() {
    set({
      messages: [],
      seqSet: new Set(),
      lastSeq: 0,
      liveReasoning: '',
      liveAgentName: '',
    })
  },
}))
