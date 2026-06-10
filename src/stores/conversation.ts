import { defineStore } from 'pinia'
import type { Message } from '../api/types'

// 按 seq 有序去重持有当前对话的消息。SSE 补历史 + 实时可能重叠，靠 seq 去重。
export const useConversationStore = defineStore('conversation', {
  state: () => ({
    messages: [] as Message[],
    seqSet: new Set<number>(),
    lastSeq: 0,
  }),
  actions: {
    ingest(m: Message) {
      // 重复消息直接返回（seq 去重）。
      if (this.seqSet.has(m.Seq)) return

      this.seqSet.add(m.Seq)

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

    reset() {
      this.messages = []
      this.seqSet = new Set()
      this.lastSeq = 0
    },
  },
})
