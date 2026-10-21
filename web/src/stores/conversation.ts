import { create } from 'zustand'
import type { Message } from '@/api/types'

// LaneBuffers 是单个 agent 活动道的双通道缓冲：thinking（模型内部 CoT，折叠区
// 流式展示）与 text（ReAct thought 正文，打字机）。终态消息到达即整道清空。
interface LaneBuffers {
  thinking: string
  text: string
}

interface ConversationState {
  messages: Message[]
  seqSet: Set<number>
  lastSeq: number
  // 流式活动气泡：按 agent 分道（认知循环四 agent 并发思考 + 聊天助手）。
  // liveStreams 是 agent → 双通道缓冲；liveOrder 记录首现序（渲染保序）。
  // 道 = agent_name（delta 帧携带；聊天助手为 assistant），终态消息到达即清对应道：
  // agent 推理道由 kind=reasoning 终态清、聊天道由 assistant 完整消息清。
  liveStreams: Record<string, LaneBuffers>
  liveOrder: string[]
  // 「正在思考」占位：用户消息发出 → 首个 AI 信号（增量帧或 assistant 消息落地）
  // 之间的空窗指示。空窗 = 异步意图分类 + 首 token 延迟（可达十几秒），
  // 无占位则用户无从区分「在干活」与「挂了」（业界 ChatGPT/Claude 同款范式）。
  thinking: boolean
  beginThinking(): void
  ingest(m: Message): void
  appendReasoningDelta(chunk: string, agentName?: string, channel?: 'thinking' | 'text'): void
  reset(): void
}

// 终态消息清哪条道：agent 推理终态（metadata.kind=reasoning）清该 agent 的道，
// 聊天助手完整回复（role=assistant 的普通消息）清 assistant 道。
// 任务生命周期终态节点（task_status 的 completed/aborted/failed）清全部道——
// 任务已结束，一切流式活动随之收场（停止后四 agent 的打字气泡不得残留；
// 正常收敛时各道已被自己的终态清过，此处幂等兜底）。
function clearedLane(m: Message): string | undefined {
  if (m.Metadata?.kind === 'reasoning') return m.Metadata.agent_name || undefined
  if (m.Kind === 'message' && m.Role === 'assistant') return 'assistant'
  return undefined
}

// isTaskFinalStatus 报告消息是否为任务生命周期终态节点（清全部道的信号）。
function isTaskFinalStatus(m: Message): boolean {
  if (m.Metadata?.kind !== 'task_status') return false
  return ['completed', 'aborted', 'failed'].includes(m.Metadata.status ?? '')
}

function dropLane(streams: Record<string, LaneBuffers>, order: string[], lane: string) {
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
  thinking: false,

  // beginThinking 在用户消息发出时置位（Composer 发送成功即调）；由首个 AI 信号
  // 清除——appendReasoningDelta 的任何增量（聊天打字机 or agent 思考道）或 ingest
  // 落地的 assistant 消息（回答终稿 / reasoning 终态 / 提示消息 / 扫描派发事件）。
  // 用户自己消息的 ingest（补拉）不清除。
  beginThinking() {
    set({ thinking: true })
  },

  ingest(m: Message) {
    const { seqSet, messages, liveStreams, liveOrder, thinking } = get()
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
    const lanes = isTaskFinalStatus(m)
      ? { liveStreams: {} as Record<string, LaneBuffers>, liveOrder: [] as string[] }
      : lane
        ? dropLane(liveStreams, liveOrder, lane)
        : { liveStreams, liveOrder }

    set({
      messages: nextMessages,
      seqSet: nextSeqSet,
      lastSeq: m.Seq > get().lastSeq ? m.Seq : get().lastSeq,
      // 任意 assistant 侧消息落地 = AI 已在时间线可见地干活，占位退场。
      thinking: thinking && m.Role === 'user',
      ...lanes,
    })
  },

  // appendReasoningDelta 累积一段增量到该 agent 的道的对应通道（channel ∈
  // {thinking, text}，缺省 text；无 agent_name 归 assistant）。thinking 通道驱动
  // 折叠思考区流式、text 通道驱动打字机；新道进 liveOrder 尾部。
  appendReasoningDelta(chunk: string, agentName?: string, channel?: 'thinking' | 'text') {
    if (!chunk) return
    const lane = agentName?.trim() || 'assistant'
    const ch: keyof LaneBuffers = channel === 'thinking' ? 'thinking' : 'text'
    set((state) => {
      const existed = lane in state.liveStreams
      const buf = state.liveStreams[lane] || { thinking: '', text: '' }
      return {
        liveStreams: { ...state.liveStreams, [lane]: { ...buf, [ch]: buf[ch] + chunk } },
        liveOrder: existed ? state.liveOrder : [...state.liveOrder, lane],
        // 首个增量到达：活动气泡本体已是「在干活」的证明，占位退场。
        thinking: false,
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
      // thinking 刻意保留：新会话发送链路里 onStarted → open() → reset() 会紧随
      // beginThinking() 执行，清掉会让占位气泡闪没。跨会话清除由拉回历史的
      // ingest 兜底——旧会话历史必含 assistant 侧消息，落一条即清；新发起会话
      // 历史只有 user 消息（不清），占位正确持续到首个 AI 信号。
      thinking: get().thinking,
    })
  },
}))
