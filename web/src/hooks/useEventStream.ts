import type { Message } from '@/api/types'
import { authStream } from '@/api/client'
import type { useConversationStore } from '@/stores/conversation'

type Store = ReturnType<typeof useConversationStore.getState>

// 连接态：'connecting' 首次连接中 / 'open' 已连 / 'reconnecting' 断线退避重连中。
// 顶部状态栏据此显示「重连中…」chip——区分「卡了/断了」与「正常静默」。
export type StreamStatus = 'connecting' | 'open' | 'reconnecting'

export interface StreamHandle {
  close(): void
}

const MAX_BACKOFF_MS = 15000

/** SSE 帧回调：默认帧（Message JSON）与 event:delta（流式增量）分开派发。 */
export interface StreamHandlers {
  onMessage(msg: Message): void
  onDelta?(text: string, agentName?: string): void
}

/**
 * connectEventStream 是订阅任意会话 SSE 流的底层原语：鉴权换 cookie → 开 EventSource →
 * 断线自管重连（指数退避，上限 15s）。回调交给上层决定帧落地方式——聊天页灌进 zustand
 * store 渲染消息列表；执行图页只需要「有新帧到达」这个信号去触发 react-query 重拉，
 * 不需要真的持有消息内容。两者共享这份连接/重连逻辑，不必各自实现一套退避。
 *
 * EventSource 不能带 X-API-Key header，鉴权靠 HttpOnly stream cookie——故每次（重）连前
 * 先 `authStream` 用 X-API-Key 换取/刷新该会话的 cookie，再开 EventSource。这样打开任意
 * 会话（新/旧）、长扫描、断线重连都能维持实时。
 *
 * @param convID 会话 ID
 * @param handlers 帧回调
 * @param onStatusChange 连接状态变化回调（驱动组件里的"重连中…"提示）
 * @returns 流句柄，调用 close() 关闭连接
 */
export function connectEventStream(
  convID: string,
  handlers: StreamHandlers,
  onStatusChange?: (status: StreamStatus) => void,
): StreamHandle {
  let es: EventSource | null = null
  let closed = false
  let retry = 0
  let retryTimer: number | undefined

  const setStatus = (s: StreamStatus) => onStatusChange?.(s)

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
      setStatus('open')
    }
    src.onmessage = (e: MessageEvent) => {
      try {
        handlers.onMessage(JSON.parse(e.data) as Message)
      } catch {
        // 坏帧忽略（不该发生；后端帧是 json.Marshal）
      }
    }
    // event:delta —— 流式推理增量瞬时帧（无 seq、不落库），累积成逐字打字机活动气泡。
    src.addEventListener('delta', (e: MessageEvent) => {
      if (!handlers.onDelta) return
      try {
        const { text, agent_name } = JSON.parse(e.data) as {
          delta: boolean
          text: string
          agent_name?: string
        }
        handlers.onDelta(text, agent_name)
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
    setStatus('reconnecting')
    const delay = Math.min(1000 * 2 ** retry, MAX_BACKOFF_MS)
    retry += 1
    retryTimer = window.setTimeout(connect, delay)
  }

  setStatus('connecting')
  connect()

  return {
    close: () => {
      closed = true
      if (retryTimer) clearTimeout(retryTimer)
      es?.close()
    },
  }
}

/**
 * 订阅某会话的 SSE 流，把帧灌进会话 store（聊天页用）。
 * 薄封装：把 connectEventStream 的通用回调绑到 store 的 ingest/appendReasoningDelta。
 *
 * @param convID 会话 ID
 * @param store 会话 store（zustand getState() 快照）
 * @param onStatusChange 连接状态变化回调（驱动组件里的"重连中…"提示）
 * @returns 流句柄，调用 close() 关闭连接
 */
export function openEventStream(
  convID: string,
  store: Store,
  onStatusChange?: (status: StreamStatus) => void,
): StreamHandle {
  return connectEventStream(
    convID,
    {
      onMessage: (msg) => store.ingest(msg),
      onDelta: (text, agentName) => store.appendReasoningDelta(text, agentName),
    },
    onStatusChange,
  )
}
