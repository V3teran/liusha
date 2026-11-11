import type { Message } from '@/api/types'
import { API_BASE } from '@/api/client'
import type { useConversationStore } from '@/stores/conversation'

type Store = ReturnType<typeof useConversationStore.getState>

// 连接态：'connecting' 首次连接中 / 'open' 已连 / 'reconnecting' 断线退避重连中。
// 顶部状态栏据此显示「重连中…」chip——区分「卡了/断了」与「正常静默」。
export type StreamStatus = 'connecting' | 'open' | 'reconnecting'

export interface StreamHandle {
  close(): void
}

const MAX_BACKOFF_MS = 15000

/** SSE 帧回调：默认帧（Message JSON）与 event:delta（流式增量）分开派发。
 * channel 区分增量双通道：thinking（模型内部 CoT）/ text（正文）。 */
export interface StreamHandlers {
  onMessage(msg: Message): void
  onDelta?(text: string, agentName?: string, channel?: 'thinking' | 'text'): void
}

/**
 * connectEventStream 是订阅任意会话 SSE 流的底层原语：开 EventSource →
 * 断线自管重连（指数退避，上限 15s）。帧落地方式由回调决定——openEventStream 把它绑到
 * 对话 store（灌进 zustand 渲染消息列表）。
 *
 * 鉴权与普通端点同源同路径：登录会话 Cookie（liusha_session）随同源 EventSource
 * 自动携带，无需换取专用 token。
 *
 * @param convID 会话 ID
 * @param handlers 帧回调
 * @param onStatusChange 连接状态变化回调（驱动组件里的"重连中…"提示）
 * @returns 流句柄，调用 close() 关闭连接
 */
function connectEventStream(
  convID: string,
  handlers: StreamHandlers,
  onStatusChange?: (status: StreamStatus) => void,
): StreamHandle {
  let es: EventSource | null = null
  let closed = false
  let retry = 0
  let retryTimer: number | undefined

  const setStatus = (s: StreamStatus) => onStatusChange?.(s)

  const connect = () => {
    if (closed) return
    const src = new EventSource(`${API_BASE}/conversations/${convID}/stream`, { withCredentials: true })
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
        const { text, agent_name, channel } = JSON.parse(e.data) as {
          delta: boolean
          text: string
          agent_name?: string
          channel?: 'thinking' | 'text'
        }
        handlers.onDelta(text, agent_name, channel)
      } catch {
        // 坏帧忽略
      }
    })
    src.onerror = () => {
      // 接管重连：关掉本连接，退避后重连（会话失效时由页面级 401 拦截接管跳登录）。
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
      onDelta: (text, agentName, channel) => store.appendReasoningDelta(text, agentName, channel),
    },
    onStatusChange,
  )
}
