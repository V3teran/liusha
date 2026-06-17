import { defineStore } from 'pinia'
import type { Message } from '../api/types'

// 按 seq 有序去重持有当前对话的消息。SSE 补历史 + 实时可能重叠，靠 seq 去重。
export const useConversationStore = defineStore('conversation', {
  state: () => ({
    messages: [] as Message[],
    seqSet: new Set<number>(),
    lastSeq: 0,
    // 流式推理活动气泡：SSE event:delta 逐 chunk 累积的文本；最终 reasoning 消息到达即清空。
    // eino 串行执行，同一时刻至多一个 ChatModel 在流式，单缓冲足够（无需按 streamID 分桶）。
    liveReasoning: '',
  }),
  actions: {
    ingest(m: Message) {
      // 重复消息直接返回（seq 去重）。
      if (this.seqSet.has(m.Seq)) return

      this.seqSet.add(m.Seq)

      // 推理消息（最终帧）落定 → 清空活动气泡，由正式推理卡接管渲染。
      if (m.Metadata?.Kind === 'reasoning') this.liveReasoning = ''

      // 二分查找插入位置以保持升序。
      // 事件多数尾部追加，但补历史可能乱序到达。
      let lo = 0
      let hi = this.messages.length
      while (lo < hi) {
        const mid = (lo + hi) >> 1
        if (this.messages[mid].Seq < m.Seq) {
          lo = mid + 1
        } else {
          hi = mid
        }
      }
      this.messages.splice(lo, 0, m)

      // 更新最大 seq。
      if (m.Seq > this.lastSeq) {
        this.lastSeq = m.Seq
      }
    },

    // appendReasoningDelta 累积一段流式推理增量（驱动逐字打字机活动气泡）。
    appendReasoningDelta(chunk: string) {
      if (!chunk) return
      this.liveReasoning += chunk
    },

    reset() {
      this.messages = []
      this.seqSet = new Set()
      this.lastSeq = 0
      this.liveReasoning = ''
    },
  },
})
