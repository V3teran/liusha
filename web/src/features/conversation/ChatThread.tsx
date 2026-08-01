import { useCallback, useEffect, useRef, useState } from 'react'
import { useConversationStore } from '@/stores/conversation'
import { dayKey, dayLabel } from '@/lib/format'
import { buildThreadRows } from '@/lib/threadRows'
import { useTypewriter } from '@/hooks/useTypewriter'
import { MessageItem } from './MessageItem'
import { StepTools } from './StepTools'
import { ReasoningCard } from './cards/ReasoningCard'
import { Avatar } from './cards/Avatar'

// 会话主线：从 store 读有序消息逐条渲染，末尾挂流式推理活动气泡（逐字打字机）。
// 自动滚底：仅当用户本就贴在底部时，新消息/增量才把视图顶到最新——向上翻看历史时不打扰。
export function ChatThread() {
  const messages = useConversationStore((s) => s.messages)
  const liveReasoning = useConversationStore((s) => s.liveReasoning)
  const liveAgentName = useConversationStore((s) => s.liveAgentName)
  const elRef = useRef<HTMLDivElement>(null)
  const [atBottom, setAtBottom] = useState(true)
  const [unread, setUnread] = useState(0)
  const prevLenRef = useRef(0)

  // 流式推理逐字揭示：后端 reasoning 流经 eino ReAct 图被 ConcatMessageStream 拍平（逐 token 在
  // eino 内部即被抽干），到前端时整段 delta 毫秒内涌出。直接绑 liveReasoning 会整块蹦出、无逐字感。
  // useTypewriter 把「已到达全文」按稳定节奏本地揭示，与网络到达节奏解耦（业界通行：ChatGPT/Claude UI）。
  const typedReasoning = useTypewriter(liveReasoning)

  // 在消息流中按天插入分隔条（今天 / 昨天 / 日期）——跨天会话一眼可辨，内联卡片只显示时分秒。
  // step：本次「用户指令」内的全局推理步号——每条 reasoning(想) 递增一步，跨所有 agent 统一计数。
  // 分组逻辑抽到 lib/threadRows（与轨迹版 TimelineThread 共用，避免两处分歧）。
  const rows = buildThreadRows(messages, { dayKey, dayLabel })

  const nearBottom = useCallback(() => {
    const e = elRef.current
    if (!e) return true
    return e.scrollHeight - e.scrollTop - e.clientHeight < 120
  }, [])

  const stickToBottom = useCallback(() => {
    const stick = nearBottom()
    requestAnimationFrame(() => {
      if (stick && elRef.current) elRef.current.scrollTop = elRef.current.scrollHeight
    })
  }, [nearBottom])

  const onScroll = useCallback(() => {
    setAtBottom((wasBottom) => {
      const isBottom = nearBottom()
      if (isBottom && !wasBottom) setUnread(0)
      return isBottom
    })
  }, [nearBottom])

  const scrollToBottom = useCallback(() => {
    if (elRef.current) elRef.current.scrollTop = elRef.current.scrollHeight
    setUnread(0)
  }, [])

  // 消息增减：贴底则自动滚底；脱离底部则累加未读计数（驱动浮标）。
  useEffect(() => {
    const n = messages.length
    const prev = prevLenRef.current
    if (nearBottom()) {
      stickToBottom()
    } else if (n > prev) {
      setUnread((u) => u + (n - prev))
    }
    prevLenRef.current = n
  }, [messages.length, nearBottom, stickToBottom])

  // 流式逐字揭示时贴底跟随滚动，不计未读（增量不是新消息）。
  useEffect(() => {
    stickToBottom()
  }, [typedReasoning, stickToBottom])

  return (
    <div className="relative flex min-h-0 flex-1 flex-col">
      {/* aria-live=polite：新落定的消息/工具组会被屏读器播报（不打断当前朗读）。
          aria-relevant=additions：只播报新增节点，忽略滚动引起的移除/重排。 */}
      <div
        ref={elRef}
        className="flex-1 overflow-y-auto p-3"
        role="log"
        aria-live="polite"
        aria-relevant="additions"
        aria-label="对话消息"
        onScroll={onScroll}
      >
        <div className="flex flex-col gap-2.5">
          {rows.map((r) => {
            if (r.kind === 'divider') {
              return (
                <div key={r.key} className="flex items-center gap-3 text-[11.5px] text-muted">
                  <span className="h-px flex-1 bg-border" />
                  <span className="flex-shrink-0 font-mono tracking-wide">{r.label}</span>
                  <span className="h-px flex-1 bg-border" />
                </div>
              )
            }
            if (r.kind === 'tools') return <StepTools key={r.key} tools={r.tools} />
            return <MessageItem key={r.key} msg={r.msg} step={r.step} />
          })}
          {/* 流式推理活动气泡：套与落定推理卡相同的头像行布局，并传 agentName 让配色/标签一致。 */}
          {liveReasoning && (
            <div className="flex items-start gap-2.5" aria-hidden="true">
              <div className="w-[30px] flex-shrink-0">
                <Avatar who="agent" />
              </div>
              <div className="flex min-w-0 flex-1 flex-col">
                <ReasoningCard text={typedReasoning} agentName={liveAgentName || undefined} streaming />
              </div>
            </div>
          )}
        </div>
      </div>
      {/* 跳到最新浮标：脱离底部时出现，带未读计数。 */}
      {!atBottom && (
        <button
          type="button"
          onClick={scrollToBottom}
          className="absolute bottom-4 left-1/2 z-10 flex -translate-x-1/2 items-center gap-1.5 rounded-full bg-accent px-3.5 py-1.5 text-[12.5px] font-medium text-white shadow-lg hover:bg-accent-hover"
        >
          <span>{unread > 0 ? `${unread > 99 ? '99+' : unread} 条新消息` : '回到最新'}</span>
          <span className="text-[13px] leading-none">↓</span>
        </button>
      )}
    </div>
  )
}
