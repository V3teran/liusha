import type { Message } from '../api/types'
import type { useConversationStore } from '../stores/conversation'

type Store = ReturnType<typeof useConversationStore>

export interface StreamHandle {
  close(): void
}

/**
 * 订阅某对话的 SSE 流。
 *
 * EventSource 自带重连 + Last-Event-ID 续传；
 * withCredentials 让浏览器自动带 liusha_stream cookie（同源部署）。
 *
 * @param convID 对话 ID
 * @param store 对话 store 实例
 * @returns 流句柄，调用 close() 关闭连接
 */
export function openEventStream(convID: string, store: Store): StreamHandle {
  const es = new EventSource(`/api/conversations/${convID}/stream`, {
    withCredentials: true,
  })

  es.onmessage = (e: MessageEvent) => {
    try {
      const m = JSON.parse(e.data) as Message
      store.ingest(m)
    } catch {
      // 坏帧忽略（不该发生；后端帧是 json.Marshal）
    }
  }

  // event:delta —— 流式推理增量瞬时帧（无 seq、不落库），累积成逐字打字机活动气泡。
  es.addEventListener('delta', (e: MessageEvent) => {
    try {
      const { text } = JSON.parse(e.data) as { delta: boolean; text: string }
      store.appendReasoningDelta(text)
    } catch {
      // 坏帧忽略
    }
  })

  return {
    close: () => es.close(),
  }
}
