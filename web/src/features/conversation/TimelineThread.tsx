import { useCallback, useEffect, useRef, useState } from 'react'
import { Bot, Bug, Rocket, Brain, Wrench, Archive, type LucideIcon } from 'lucide-react'
import { useConversationStore } from '@/stores/conversation'
import { dayKey, dayLabel } from '@/lib/format'
import { buildThreadRows, type ThreadRow } from '@/lib/threadRows'
import { classifyMessage } from '@/lib/messageKind'
import { agentAccent, agentLabel } from '@/lib/agentColor'
import { useTypewriter } from '@/hooks/useTypewriter'
import { cn } from '@/lib/utils'
import { MessageItem } from './MessageItem'
import { StepTools } from './StepTools'
import { ReasoningCard } from './cards/ReasoningCard'

// 每行的 agent 归属：驱动缩进（非 orchestrator = 子代理，缩进）+ 徽章配色。
// reasoning/tool 取 Metadata.AgentName；tools 组取首条 tool 的 agent；其余（user/finding/派发）归 root。
function rowAgent(r: ThreadRow): string {
  if (r.kind === 'tools') return r.tools[0]?.Metadata?.AgentName || ''
  if (r.kind === 'msg') {
    const tag = classifyMessage(r.msg)
    if (tag === 'reasoning' || tag === 'tool-call' || tag === 'tool-result') {
      return r.msg.Metadata?.AgentName || ''
    }
  }
  return ''
}

// 子代理缩进：orchestrator 与无归属（root）不缩进；侦察/利用等子代理缩进 + 配色左边框。
function isSubAgent(agent: string): boolean {
  return !!agent && agent !== 'orchestrator'
}

// 行图标：每行按语义分类取一个 lucide 图标（想=Brain、工具=Wrench、派发=Rocket、
// 漏洞=Bug、压缩=Archive、会话消息=Bot），替代原来的空心圆点——图标本身就是分类索引，
// 信息密度比纯装饰性圆点高，且与 AppShell/Composer 已在用的 lucide 图标语言保持统一。
function rowIcon(r: ThreadRow): LucideIcon {
  if (r.kind !== 'msg') return Wrench
  const tag = classifyMessage(r.msg)
  switch (tag) {
    case 'reasoning':
      return Brain
    case 'spawn':
    case 'spawn-done':
      return Rocket
    case 'finding':
      return Bug
    case 'compaction':
      return Archive
    default:
      return Bot
  }
}

// active 作战轨迹：把 agent 推理流渲染成垂直时间轴，而非聊天气泡。
// 专为「观测 AI 自主作战」设计——每行是一个语义图标徽章（想/工具/派发/漏洞），子代理（非
// orchestrator）缩进 + 该 agent 配色左边框，一眼看出「编排在派活、侦察/利用在并行干活」。
// 不再叠加贯穿全程的竖线：子代理分组已由左边框承担，两种视觉线索同时存在才是显脏的根因。
export function TimelineThread() {
  const messages = useConversationStore((s) => s.messages)
  const liveReasoning = useConversationStore((s) => s.liveReasoning)
  const liveAgentName = useConversationStore((s) => s.liveAgentName)
  const elRef = useRef<HTMLDivElement>(null)
  const [atBottom, setAtBottom] = useState(true)
  const [unread, setUnread] = useState(0)
  const prevLenRef = useRef(0)

  const typedReasoning = useTypewriter(liveReasoning)
  const rows = buildThreadRows(messages, { dayKey, dayLabel })
  // 未读计数以「渲染行(动态)」为单位，而非原始消息条数：一次推理+工具调用后端落
  // reasoning+tool_call+tool_result 三条消息，但渲染只成 2 行（想 + 折叠工具组），
  // 数原始消息会与用户实际看到的行数对不上。divider 是日期分隔，不计。
  const activityCount = rows.reduce((n, r) => (r.kind === 'divider' ? n : n + 1), 0)

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

  useEffect(() => {
    const n = activityCount
    const prev = prevLenRef.current
    if (nearBottom()) {
      stickToBottom()
    } else if (n > prev) {
      setUnread((u) => u + (n - prev))
    }
    prevLenRef.current = n
  }, [activityCount, nearBottom, stickToBottom])

  useEffect(() => {
    stickToBottom()
  }, [typedReasoning, stickToBottom])

  const liveIsSub = isSubAgent(liveAgentName)
  const liveAccent = agentAccent(liveAgentName).accent

  return (
    <div className="relative flex min-h-0 flex-1 flex-col">
      <div
        ref={elRef}
        className="flex-1 overflow-y-auto py-4 pl-2 pr-4"
        role="log"
        aria-live="polite"
        aria-relevant="additions"
        aria-label="作战轨迹"
        onScroll={onScroll}
      >
        {rows.map((r) => {
          if (r.kind === 'divider') {
            return (
              <div key={r.key} className="ml-[30px] my-2.5 flex items-center gap-3 text-[11.5px] text-muted">
                <span className="h-px flex-1 bg-border" />
                <span className="flex-shrink-0 font-mono tracking-wide">{r.label}</span>
                <span className="h-px flex-1 bg-border" />
              </div>
            )
          }
          const agent = rowAgent(r)
          const sub = isSubAgent(agent)
          const accent = agentAccent(agent).accent
          const soft = agentAccent(agent).soft
          const Icon = rowIcon(r)
          return (
            <div key={r.key} className={cn('flex items-start gap-2.5', sub && 'ml-[26px]')}>
              <span
                className="mt-0.5 flex h-5 w-5 flex-shrink-0 items-center justify-center rounded-md"
                style={{ color: accent, background: soft }}
              >
                <Icon className="h-3 w-3" strokeWidth={2.25} />
              </span>
              <div
                className={cn('min-w-0 flex-1 pb-3.5', sub && 'border-l-2 pl-3')}
                style={sub ? { borderColor: accent } : undefined}
              >
                {sub && (
                  <span
                    className="mb-1.5 inline-block rounded px-2 py-0.5 text-[11px] font-semibold"
                    style={{ color: accent, background: soft }}
                  >
                    {agentLabel(agent)}
                  </span>
                )}
                {r.kind === 'tools' ? <StepTools tools={r.tools} /> : <MessageItem msg={r.msg} step={r.step} />}
              </div>
            </div>
          )
        })}

        {/* 流式活动节点：与落定行同布局（图标徽章 + 缩进），逐字打字机揭示 */}
        {liveReasoning && (
          <div className={cn('flex items-start gap-2.5', liveIsSub && 'ml-[26px]')} aria-hidden="true">
            <span
              className="mt-0.5 flex h-5 w-5 flex-shrink-0 animate-pulse items-center justify-center rounded-md"
              style={{ color: liveAccent, background: agentAccent(liveAgentName).soft }}
            >
              <Brain className="h-3 w-3" strokeWidth={2.25} />
            </span>
            <div
              className={cn('min-w-0 flex-1 pb-3.5', liveIsSub && 'border-l-2 pl-3')}
              style={liveIsSub ? { borderColor: liveAccent } : undefined}
            >
              {liveIsSub && (
                <span
                  className="mb-1.5 inline-block rounded px-2 py-0.5 text-[11px] font-semibold"
                  style={{ color: liveAccent, background: agentAccent(liveAgentName).soft }}
                >
                  {agentLabel(liveAgentName)}
                </span>
              )}
              <ReasoningCard text={typedReasoning} agentName={liveAgentName || undefined} streaming />
            </div>
          </div>
        )}
      </div>

      {!atBottom && (
        <button
          type="button"
          onClick={scrollToBottom}
          className="absolute bottom-4 left-1/2 z-10 flex -translate-x-1/2 items-center gap-1.5 rounded-full bg-accent px-3.5 py-1.5 text-[12.5px] font-medium text-white shadow-lg hover:bg-accent-hover"
        >
          <span>{unread > 0 ? `${unread > 99 ? '99+' : unread} 条新动态` : '回到最新'}</span>
          <span className="text-[13px] leading-none">↓</span>
        </button>
      )}
    </div>
  )
}
