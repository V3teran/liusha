import { ref, type Ref } from 'vue'
import type { Message } from '../api/types'
import { authStream } from '../api/client'
import type { useConversationStore } from '../stores/conversation'

type Store = ReturnType<typeof useConversationStore>

// 连接态：'connecting' 首次连接中 / 'open' 已连 / 'reconnecting' 断线退避重连中。
// 顶部状态栏据此显示「重连中…」chip——区分「卡了/断了」与「正常静默」。
export type StreamStatus = 'connecting' | 'open' | 'reconnecting'

export interface StreamHandle {
  close(): void
  status: Ref<StreamStatus>
}

const MAX_BACKOFF_MS = 15000

/**
 * 订阅某对话的 SSE 流。
 *
 * EventSource 不能带 X-API-Key header，鉴权靠 HttpOnly stream cookie——故每次（重）连前
 * 先 `authStream` 用 X-API-Key 换取/刷新该会话的 cookie，再开 EventSource。这样打开任意
 * 会话（新/旧）、长扫描、断线重连都能维持实时（旧实现只在 /chat 下发一次性 cookie → 旧会话
 * 与长扫描 SSE 静默失效）。
 *
 * 自管重连：接管浏览器默认重连（onerror→close→退避后重新 authStream+连），以便每次重连
 * 都刷新 cookie；指数退避上限 15s。
 *
 * @param convID 对话 ID
 * @param store 对话 store 实例
 * @returns 流句柄，调用 close() 关闭连接
 */
export function openEventStream(convID: string, store: Store): StreamHandle {
  let es: EventSource | null = null
  let closed = false
  let retry = 0
  let retryTimer: number | undefined
  const status = ref<StreamStatus>('connecting')

  const connect = async () => {
    if (closed) return
    try {
      await authStream(convID) // 换取/刷新 stream cookie
    } catch {
      // 鉴权失败（如 key 失效）→ 退避后重试，不放弃
      scheduleReconnect()
      return
    }
    if (closed) return

    const src = new EventSource(`/api/conversations/${convID}/stream`, { withCredentials: true })
    es = src

    src.onopen = () => {
      retry = 0 // 连上即重置退避
      status.value = 'open'
    }
    src.onmessage = (e: MessageEvent) => {
      try {
        store.ingest(JSON.parse(e.data) as Message)
      } catch {
        // 坏帧忽略（不该发生；后端帧是 json.Marshal）
      }
    }
    // event:delta —— 流式推理增量瞬时帧（无 seq、不落库），累积成逐字打字机活动气泡。
    src.addEventListener('delta', (e: MessageEvent) => {
      try {
        const { text } = JSON.parse(e.data) as { delta: boolean; text: string }
        store.appendReasoningDelta(text)
      } catch {
        // 坏帧忽略
      }
    })
    src.onerror = () => {
      // 接管重连：关掉本连接，退避后重新 authStream+连（cookie 可能已过期，必须重签）。
      src.close()
      if (es === src) es = null
      scheduleReconnect()
    }
  }

  const scheduleReconnect = () => {
    if (closed) return
    status.value = 'reconnecting'
    const delay = Math.min(1000 * 2 ** retry, MAX_BACKOFF_MS)
    retry += 1
    retryTimer = window.setTimeout(connect, delay)
  }

  connect()

  return {
    status,
    close: () => {
      closed = true
      if (retryTimer) clearTimeout(retryTimer)
      es?.close()
    },
  }
}
