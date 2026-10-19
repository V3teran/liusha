import { create } from 'zustand'
import type { Message } from '@/api/types'

interface ConversationState {
  messages: Message[]
  seqSet: Set<number>
  lastSeq: number
  // 流式思考活动气泡：按 agent 分道（认知循环四 agent 并发思考 + 聊天助手）。
  // liveStreams 是 agent → 已累积文本；liveOrder 记录首现序（渲染保序）。
  // 道 = agent_name（delta 帧携带；聊天助手为 assistant），终态消息到达即清对应道：
  // agent 推理道由 kind=reasoning 终态清、聊天道由 assistant 完整消息清。
  liveStreams: Record<string, string>
  liveOrder: string[]
  ingest(m: Message): void
  appendReasoningDelta(chunk: string, agentName?: string): void
  reset(): void
}

// 终态消息清哪条道：agent 推理终态（metadata.kind=reasoning）清该 agent 的道，
// 聊天助手完整回复（role=assistant 的普通消息）清 assistant 道。
function clearedLane(m: Message): string | undefined {
  if (m.Metadata?.Kind === 'reasoning') return m.Metadata.AgentName || undefined
  if (m.Kind === 'message' && m.Role === 'assistant') return 'assistant'
  return undefined
}

function dropLane(streams: Record<string, string>, order: string[], lane: string) {
  const next = { ...streams }
  delete next[lane]
  return { liveStreams: next, liveOrder: order.filter((k) => k !== lane) }
}

// 按 seq 有序去重持有当前会话的消息。SSE 补历史 + 实时可能重叠，靠 seq 去重。
export const useConversationStore = create<ConversationState>((set, get) => ({
  messages: [],
  seqSet: new Set<number>(),
  lastSeq: 0,
  liveStreams: {},
  liveOrder: [],

  ingest(m: Message) {
    const { seqSet, messages, liveStreams, liveOrder } = get()
    // 重复消息直接返回（seq 去重）。
    if (seqSet.has(m.Seq)) return

    const nextSeqSet = new Set(seqSet)
    nextSeqSet.add(m.Seq)

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

    const lane = clearedLane(m)
    const lanes = lane ? dropLane(liveStreams, liveOrder, lane) : { liveStreams, liveOrder }

    set({
      messages: nextMessages,
      seqSet: nextSeqSet,
      lastSeq: m.Seq > get().lastSeq ? m.Seq : get().lastSeq,
      ...lanes,
    })
  },

  // appendReasoningDelta 累积一段思考增量到该 agent 的道（无 agent_name 归 assistant），
  // 驱动逐字打字机活动气泡；新道进 liveOrder 尾部。
  appendReasoningDelta(chunk: string, agentName?: string) {
    if (!chunk) return
    const lane = agentName?.trim() || 'assistant'
    set((state) => {
      const existed = lane in state.liveStreams
      return {
        liveStreams: { ...state.liveStreams, [lane]: (state.liveStreams[lane] || '') + chunk },
        liveOrder: existed ? state.liveOrder : [...state.liveOrder, lane],
      }
    })
  },

  reset() {
    set({
      messages: [],
      seqSet: new Set<number>(),
      lastSeq: 0,
      liveStreams: {},
      liveOrder: [],
    })
  },
}))
